package vm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const limaHostAgentCleanupTimeout = 8 * time.Second

// hostAgentRunning checks the hostagent recorded for this instance instead of
// trusting ha.pid alone. PID files can outlive a crashed hostagent and a
// reused PID must never be treated as a Lima process.
func (m *Manager) hostAgentRunning(ctx context.Context) (bool, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, fmt.Errorf("读取用户目录失败: %w", err)
	}
	pidPath := filepath.Join(home, ".lima", m.instanceName, "ha.pid")
	data, err := os.ReadFile(pidPath)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取 Lima hostagent PID 失败: %w", err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || pid <= 1 {
		return false, nil
	}

	command, err := exec.CommandContext(ctx, "/bin/ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		return false, nil
	}
	line := strings.TrimSpace(string(command))
	return strings.Contains(line, "limactl hostagent") && strings.Contains(line, m.instanceName), nil
}

func (m *Manager) waitForHostAgentExit(ctx context.Context, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = limaHostAgentCleanupTimeout
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		running, err := m.hostAgentRunning(ctx)
		if err != nil {
			return err
		}
		if !running {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("Lima hostagent 在 %s 内未退出", timeout)
		case <-ticker.C:
		}
	}
}

func (m *Manager) forceStopLima(ctx context.Context) error {
	output, err := runHostCommand(ctx, "limactl", "stop", "--force", "--tty=false", m.instanceName)
	if err != nil {
		return fmt.Errorf("强制停止 Lima 实例失败: %s (%w)", strings.TrimSpace(output), err)
	}
	if err := m.waitForHostAgentExit(ctx, limaHostAgentCleanupTimeout); err != nil {
		return fmt.Errorf("等待 Lima hostagent 清理失败: %w", err)
	}
	return nil
}

// recoverStaleHostAgent repairs the state left when a previous limactl stop
// removed ha.sock but the hostagent process remained alive. It only acts on a
// process whose command line identifies both Lima hostagent and this instance.
func (m *Manager) recoverStaleHostAgent(ctx context.Context) error {
	running, err := m.hostAgentRunning(ctx)
	if err != nil {
		return err
	}
	if !running {
		return nil
	}
	return m.forceStopLima(ctx)
}

func limaStartNeedsRecovery(output string) bool {
	message := strings.ToLower(output)
	return strings.Contains(message, "ha.sock") ||
		(strings.Contains(message, "configuration errors") && strings.Contains(message, "hostagent"))
}
