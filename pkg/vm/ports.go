package vm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lulalulaluobo/macbox/pkg/config"
)

const (
	ManualForwardPortMin     = 1024
	ManualForwardPortMax     = 65535
	PortForwardSourceManual  = "manual"
	PortForwardSourceManaged = "managed"
)

var (
	ErrManualForwardPortNotOwned = errors.New("端口不是由手工服务发布管理")
	ErrManualForwardPortInvalid  = errors.New("手工发布端口必须在 1024-65535 范围内")
	ssProcessPattern             = regexp.MustCompile(`users:\(\("([^"]+)"(?:,pid=([0-9]+))?`)
)

// ListeningPort describes one TCP port currently listening inside the VM.
type ListeningPort struct {
	Port      int      `json:"port"`
	Addresses []string `json:"addresses"`
	Process   string   `json:"process,omitempty"`
	PID       int      `json:"pid,omitempty"`
}

type PortForwardChange struct {
	Changed bool
	Source  string
}

func ValidateManualForwardPort(port int) error {
	if port < ManualForwardPortMin || port > ManualForwardPortMax {
		return ErrManualForwardPortInvalid
	}
	return nil
}

// ListListeningPorts asks the VM for TCP listeners. Process metadata is
// best-effort: unprivileged ss output is still useful when the privileged
// query is unavailable.
func (m *Manager) ListListeningPorts(ctx context.Context) ([]ListeningPort, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	output, err := m.Exec(queryCtx, "sudo", "ss", "-H", "-lntp")
	if err != nil {
		output, err = m.Exec(queryCtx, "ss", "-H", "-lnt")
	}
	if err != nil {
		return nil, fmt.Errorf("读取 VM 监听端口失败: %w", err)
	}
	return parseSSListeningPorts(output), nil
}

func parseSSListeningPorts(output string) []ListeningPort {
	type aggregate struct {
		addresses map[string]struct{}
		process   string
		pid       int
	}
	ports := make(map[int]*aggregate)

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		listenIndex := -1
		for index, field := range fields {
			if field == "LISTEN" {
				listenIndex = index
				break
			}
		}
		if listenIndex < 0 || listenIndex+3 >= len(fields) {
			continue
		}
		address, port, ok := splitSSLocalAddress(fields[listenIndex+3])
		if !ok {
			continue
		}

		item := ports[port]
		if item == nil {
			item = &aggregate{addresses: make(map[string]struct{})}
			ports[port] = item
		}
		item.addresses[address] = struct{}{}
		processStart := listenIndex + 5
		if processStart > len(fields) {
			processStart = len(fields)
		}
		process, pid := parseSSProcess(fields[processStart:])
		if item.process == "" && process != "" {
			item.process = process
		}
		if item.pid == 0 && pid > 0 {
			item.pid = pid
		}
	}

	result := make([]ListeningPort, 0, len(ports))
	for port, item := range ports {
		addresses := make([]string, 0, len(item.addresses))
		for address := range item.addresses {
			addresses = append(addresses, address)
		}
		sort.Strings(addresses)
		result = append(result, ListeningPort{Port: port, Addresses: addresses, Process: item.process, PID: item.pid})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Port < result[j].Port })
	return result
}

func splitSSLocalAddress(raw string) (string, int, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", 0, false
	}
	if host, portText, err := net.SplitHostPort(raw); err == nil {
		port, err := strconv.Atoi(portText)
		if err == nil && port >= 1 && port <= 65535 {
			if host == "" {
				host = "*"
			}
			return host, port, true
		}
	}
	separator := strings.LastIndex(raw, ":")
	if separator < 0 || separator == len(raw)-1 {
		return "", 0, false
	}
	port, err := strconv.Atoi(raw[separator+1:])
	if err != nil || port < 1 || port > 65535 {
		return "", 0, false
	}
	host := strings.Trim(raw[:separator], "[]")
	if host == "" {
		host = "*"
	}
	return host, port, true
}

func parseSSProcess(fields []string) (string, int) {
	match := ssProcessPattern.FindStringSubmatch(strings.Join(fields, " "))
	if len(match) == 0 {
		return "", 0
	}
	pid := 0
	if len(match) > 2 && match[2] != "" {
		pid, _ = strconv.Atoi(match[2])
	}
	return match[1], pid
}

func (m *Manager) ManualPublishedPorts() ([]int, error) {
	snapshot, err := config.Snapshot(m.cfg)
	if err != nil {
		return nil, err
	}
	return config.NormalizeManualPublishedPorts(snapshot.Terminal.ManualPublishedPorts, snapshot.VM.ForwardedPorts), nil
}

func (m *Manager) AddManualForwardedPort(port int) (PortForwardChange, error) {
	if err := ValidateManualForwardPort(port); err != nil {
		return PortForwardChange{}, err
	}
	change := PortForwardChange{}
	if err := config.Update(m.cfg, func(updated *config.Config) error {
		forwarded := config.NormalizeForwardedPorts(updated.VM.ForwardedPorts)
		manual := config.NormalizeManualPublishedPorts(updated.Terminal.ManualPublishedPorts, forwarded)
		if containsPort(manual, port) {
			change.Source = PortForwardSourceManual
			updated.VM.ForwardedPorts = forwarded
			updated.Terminal.ManualPublishedPorts = manual
			return nil
		}
		if containsPort(forwarded, port) {
			change.Source = PortForwardSourceManaged
			updated.VM.ForwardedPorts = forwarded
			updated.Terminal.ManualPublishedPorts = manual
			return nil
		}
		updated.VM.ForwardedPorts = config.NormalizeForwardedPorts(append(forwarded, port))
		updated.Terminal.ManualPublishedPorts = config.NormalizeManualPublishedPorts(append(manual, port), updated.VM.ForwardedPorts)
		change.Changed = true
		change.Source = PortForwardSourceManual
		return nil
	}); err != nil {
		return PortForwardChange{}, fmt.Errorf("保存手工端口发布配置失败: %w", err)
	}
	if change.Changed {
		m.SetConfigDirty(true)
	}
	return change, nil
}

func (m *Manager) RemoveManualForwardedPort(port int) (PortForwardChange, error) {
	if err := ValidateManualForwardPort(port); err != nil {
		return PortForwardChange{}, err
	}
	change := PortForwardChange{Source: PortForwardSourceManual}
	if err := config.Update(m.cfg, func(updated *config.Config) error {
		forwarded := config.NormalizeForwardedPorts(updated.VM.ForwardedPorts)
		manual := config.NormalizeManualPublishedPorts(updated.Terminal.ManualPublishedPorts, forwarded)
		if !containsPort(manual, port) {
			return ErrManualForwardPortNotOwned
		}
		updated.Terminal.ManualPublishedPorts = removePort(manual, port)
		updated.VM.ForwardedPorts = removePort(forwarded, port)
		change.Changed = true
		return nil
	}); err != nil {
		return PortForwardChange{}, fmt.Errorf("删除手工端口发布配置失败: %w", err)
	}
	if change.Changed {
		m.SetConfigDirty(true)
	}
	return change, nil
}

func containsPort(ports []int, target int) bool {
	for _, port := range ports {
		if port == target {
			return true
		}
	}
	return false
}

func removePort(ports []int, target int) []int {
	result := make([]int, 0, len(ports))
	for _, port := range ports {
		if port != target {
			result = append(result, port)
		}
	}
	return result
}
