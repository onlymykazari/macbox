package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lulalulaluobo/macbox/pkg/config"
	"github.com/lulalulaluobo/macbox/pkg/docker"
	"github.com/lulalulaluobo/macbox/pkg/vm"
)

type vmPortCandidate struct {
	Port        int      `json:"port"`
	Addresses   []string `json:"addresses"`
	Process     string   `json:"process,omitempty"`
	PID         int      `json:"pid,omitempty"`
	Forwarded   bool     `json:"forwarded"`
	Source      string   `json:"source"`
	Publishable bool     `json:"publishable"`
	Reason      string   `json:"reason,omitempty"`
}

type vmPortForward struct {
	Port      int    `json:"port"`
	Source    string `json:"source"`
	Listening bool   `json:"listening"`
	Process   string `json:"process,omitempty"`
	Removable bool   `json:"removable"`
}

type publishVMPortRequest struct {
	Port int `json:"port"`
}

func (s *Server) handleVMListeningPorts(w http.ResponseWriter, r *http.Request) {
	cfgSnapshot, err := config.Snapshot(s.cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("读取端口配置失败: %v", err))
		return
	}
	status, err := s.vmMgr.GetStatusContext(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("读取虚拟机状态失败: %v", err))
		return
	}
	if status == nil || status.Status != "Running" {
		vmStatus := "Stopped"
		if status != nil && status.Status != "" {
			vmStatus = status.Status
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"ports":    []vmPortCandidate{},
			"vmStatus": vmStatus,
		})
		return
	}

	listCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	listening, err := s.vmMgr.ListListeningPorts(listCtx)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, "无法读取 VM 当前监听端口，请稍后重试")
		return
	}
	manual := config.NormalizeManualPublishedPorts(cfgSnapshot.Terminal.ManualPublishedPorts, cfgSnapshot.VM.ForwardedPorts)
	forwarded := config.NormalizeForwardedPorts(cfgSnapshot.VM.ForwardedPorts)
	reserved := reservedVMPorts(cfgSnapshot, status)
	result := make([]vmPortCandidate, 0, len(listening))
	for _, item := range listening {
		candidate := vmPortCandidate{
			Port:        item.Port,
			Addresses:   item.Addresses,
			Process:     item.Process,
			PID:         item.PID,
			Source:      "none",
			Publishable: true,
		}
		if containsInt(manual, item.Port) {
			candidate.Forwarded = true
			candidate.Source = vm.PortForwardSourceManual
			candidate.Publishable = false
			candidate.Reason = "该端口已由 Web 终端发布"
		} else if containsInt(forwarded, item.Port) {
			candidate.Forwarded = true
			candidate.Source = vm.PortForwardSourceManaged
			candidate.Publishable = false
			candidate.Reason = "该端口已经由 Docker 或系统发布"
		} else if reason := reserved[item.Port]; reason != "" {
			candidate.Publishable = false
			candidate.Reason = reason
		} else if err := hostPortAvailable(cfgSnapshot.ListenAddress, item.Port); err != nil {
			candidate.Publishable = false
			candidate.Reason = "Mac 本机端口已被占用"
		}
		result = append(result, candidate)
	}

	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ports":    result,
		"vmStatus": status.Status,
	})
}

func (s *Server) handleVMPortForwardsList(w http.ResponseWriter, r *http.Request) {
	cfgSnapshot, err := config.Snapshot(s.cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("读取端口转发配置失败: %v", err))
		return
	}
	status, err := s.vmMgr.GetStatusContext(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("读取虚拟机状态失败: %v", err))
		return
	}

	listeningByPort := make(map[int]vm.ListeningPort)
	if status != nil && status.Status == "Running" {
		listCtx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
		listening, listErr := s.vmMgr.ListListeningPorts(listCtx)
		cancel()
		if listErr != nil {
			writeError(w, http.StatusServiceUnavailable, "无法读取 VM 当前监听端口，请稍后重试")
			return
		}
		for _, item := range listening {
			listeningByPort[item.Port] = item
		}
	}

	manual := config.NormalizeManualPublishedPorts(cfgSnapshot.Terminal.ManualPublishedPorts, cfgSnapshot.VM.ForwardedPorts)
	forwarded := config.NormalizeForwardedPorts(cfgSnapshot.VM.ForwardedPorts)
	result := make([]vmPortForward, 0, len(forwarded))
	for _, port := range forwarded {
		source := vm.PortForwardSourceManaged
		if containsInt(manual, port) {
			source = vm.PortForwardSourceManual
		}
		item, listening := listeningByPort[port]
		result = append(result, vmPortForward{
			Port:      port,
			Source:    source,
			Listening: listening,
			Process:   item.Process,
			Removable: source == vm.PortForwardSourceManual,
		})
	}

	bindAddress := config.NormalizeListenAddress(cfgSnapshot.ListenAddress)
	vmStatus := "Stopped"
	if status != nil && status.Status != "" {
		vmStatus = status.Status
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"ports":           result,
		"bindAddress":     bindAddress,
		"requiresRestart": s.vmMgr.IsConfigDirty(),
		"vmStatus":        vmStatus,
	})
}

func (s *Server) handleVMPortForwardPublish(w http.ResponseWriter, r *http.Request) {
	var request publishVMPortRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "端口数据格式不正确")
		return
	}
	if err := vm.ValidateManualForwardPort(request.Port); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	cfgSnapshot, err := config.Snapshot(s.cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("读取端口配置失败: %v", err))
		return
	}
	status, err := s.vmMgr.GetStatusContext(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("读取虚拟机状态失败: %v", err))
		return
	}
	manual := config.NormalizeManualPublishedPorts(cfgSnapshot.Terminal.ManualPublishedPorts, cfgSnapshot.VM.ForwardedPorts)
	if containsInt(manual, request.Port) {
		if s.vmMgr.IsConfigDirty() {
			writeError(w, http.StatusConflict, "该端口已登记，但 VM 配置仍等待重启生效")
			return
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "already_forwarded",
			"port":   request.Port,
			"source": vm.PortForwardSourceManual,
		})
		return
	}
	if containsInt(cfgSnapshot.VM.ForwardedPorts, request.Port) {
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "already_forwarded",
			"port":   request.Port,
			"source": vm.PortForwardSourceManaged,
		})
		return
	}
	if reason := reservedVMPorts(cfgSnapshot, status)[request.Port]; reason != "" {
		writeError(w, http.StatusBadRequest, reason)
		return
	}
	if status == nil || status.Status != "Running" {
		writeError(w, http.StatusConflict, "虚拟机未运行，请先启动虚拟机后再发布服务")
		return
	}
	if err := hostPortAvailable(cfgSnapshot.ListenAddress, request.Port); err != nil {
		writeError(w, http.StatusConflict, "Mac 本机端口已被占用，请更换端口")
		return
	}
	if !s.vmMgr.BeginVMAction("publishing-port") {
		writeError(w, http.StatusConflict, "已有虚拟机操作正在进行")
		return
	}
	if !s.beginBackgroundWork() {
		s.vmMgr.EndVMAction()
		writeError(w, http.StatusServiceUnavailable, "服务正在关闭")
		return
	}
	ctx, cancel := s.operationContext(10 * time.Minute)
	job := s.jobs.addWithStage("vm.port-forward.publish", "validating", 5, "正在准备发布服务端口", cancel)
	go s.runPortForwardPublishJob(ctx, cancel, job.ID, request.Port)
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status": "applying",
		"port":   request.Port,
		"jobId":  job.ID,
	})
}

func (s *Server) runPortForwardPublishJob(ctx context.Context, cancel context.CancelFunc, jobID string, port int) {
	defer s.endBackgroundWork()
	defer s.vmMgr.EndVMAction()
	defer cancel()

	s.jobs.update(jobID, "saving", 20, "正在保存端口发布配置")
	change, err := s.vmMgr.AddManualForwardedPort(port)
	if err != nil {
		s.jobs.finish(jobID, err)
		return
	}
	if !change.Changed {
		s.jobs.update(jobID, "completed", 100, "端口已由 Docker 或系统发布，无需重复重启虚拟机")
		s.jobs.finish(jobID, nil)
		return
	}
	dirtyRevision := s.vmMgr.ConfigDirtyRevision()

	s.jobs.update(jobID, "restarting", 45, "正在应用端口配置并重启虚拟机")
	if err := s.vmMgr.Restart(ctx, s.projectRoot); err != nil {
		s.jobs.finish(jobID, err)
		return
	}
	configApplied := s.vmMgr.ClearConfigDirtyIfRevision(dirtyRevision)
	if err := s.sambaMgr.EnsurePassword(ctx); err != nil {
		// Samba is unrelated to the port forwarding result; keep the job useful
		// and let the diagnostics page expose the auxiliary warning.
		logPortForwardWarning("同步 Samba 凭据失败", err)
	}
	s.migrateManagedApps(ctx)
	s.refreshDataMountContainers(ctx, jobID)

	s.jobs.update(jobID, "verifying", 98, "正在确认端口转发与 VM 服务状态")
	message := "端口转发已生效，但 VM 内服务当前未监听；请检查 Docker、systemd 或 PM2 自启动配置"
	if listening, listErr := s.vmMgr.ListListeningPorts(ctx); listErr == nil && containsListeningPort(listening, port) {
		message = "端口转发已生效，VM 内服务也已恢复监听"
	}
	if !configApplied {
		message += "；期间还有其他配置变更，仍等待下一次重启"
	}
	s.jobs.update(jobID, "completed", 100, message)
	s.jobs.finish(jobID, nil)
}

func (s *Server) handleVMPortForwardRemove(w http.ResponseWriter, r *http.Request) {
	port, err := strconv.Atoi(strings.TrimSpace(r.PathValue("port")))
	if err != nil {
		writeError(w, http.StatusBadRequest, "端口格式不正确")
		return
	}
	if err := vm.ValidateManualForwardPort(port); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	cfgSnapshot, err := config.Snapshot(s.cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("读取端口配置失败: %v", err))
		return
	}
	manual := config.NormalizeManualPublishedPorts(cfgSnapshot.Terminal.ManualPublishedPorts, cfgSnapshot.VM.ForwardedPorts)
	if !containsInt(manual, port) {
		writeError(w, http.StatusConflict, "该端口由 Docker 或系统管理，不能在此取消发布")
		return
	}
	if s.dockerClient == nil {
		writeError(w, http.StatusServiceUnavailable, "无法确认 Docker 端口占用，请稍后重试")
		return
	}
	if !s.beginDockerOperation(w) {
		return
	}
	containers, err := s.dockerClient.ListContainersSummary(r.Context())
	if err != nil {
		s.endDockerOperation()
		writeError(w, http.StatusServiceUnavailable, "无法确认 Docker 端口占用，请稍后重试")
		return
	}
	if container := runningContainerUsingPort(containers, port); container != "" {
		s.endDockerOperation()
		writeError(w, http.StatusConflict, "端口当前被 Docker 容器使用，请先在 Docker 面板停止或调整对应服务")
		return
	}
	if !s.vmMgr.BeginVMAction("removing-port") {
		s.endDockerOperation()
		writeError(w, http.StatusConflict, "已有虚拟机操作正在进行")
		return
	}
	if !s.beginBackgroundWork() {
		s.vmMgr.EndVMAction()
		s.endDockerOperation()
		writeError(w, http.StatusServiceUnavailable, "服务正在关闭")
		return
	}
	ctx, cancel := s.operationContext(10 * time.Minute)
	job := s.jobs.addWithStage("vm.port-forward.remove", "saving", 15, "正在删除手工端口发布", cancel)
	go s.runPortForwardRemoveJob(ctx, cancel, job.ID, port)
	writeJSON(w, http.StatusAccepted, map[string]interface{}{
		"status": "removing",
		"port":   port,
		"jobId":  job.ID,
	})
}

func (s *Server) runPortForwardRemoveJob(ctx context.Context, cancel context.CancelFunc, jobID string, port int) {
	defer s.endBackgroundWork()
	defer s.vmMgr.EndVMAction()
	defer s.endDockerOperation()
	defer cancel()

	change, err := s.vmMgr.RemoveManualForwardedPort(port)
	if err != nil {
		s.jobs.finish(jobID, err)
		return
	}
	if !change.Changed {
		s.jobs.finish(jobID, nil)
		return
	}
	dirtyRevision := s.vmMgr.ConfigDirtyRevision()
	s.jobs.update(jobID, "restarting", 45, "正在应用删除配置并重启虚拟机")
	if err := s.vmMgr.Restart(ctx, s.projectRoot); err != nil {
		s.jobs.finish(jobID, err)
		return
	}
	configApplied := s.vmMgr.ClearConfigDirtyIfRevision(dirtyRevision)
	if err := s.sambaMgr.EnsurePassword(ctx); err != nil {
		logPortForwardWarning("同步 Samba 凭据失败", err)
	}
	s.migrateManagedApps(ctx)
	s.refreshDataMountContainers(ctx, jobID)
	message := fmt.Sprintf("端口 %d 已取消发布", port)
	if !configApplied {
		message += "；期间还有其他配置变更，仍等待下一次重启"
	}
	s.jobs.update(jobID, "completed", 100, message)
	s.jobs.finish(jobID, nil)
}

func reservedVMPorts(cfg *config.Config, status *vm.VMStatus) map[int]string {
	reserved := map[int]string{}
	if cfg != nil {
		if cfg.Port > 0 {
			reserved[cfg.Port] = "该端口是 MacBox Web 管理端口"
		}
		if cfg.Samba.Port > 0 {
			reserved[cfg.Samba.Port] = "该端口是 MacBox Samba 服务端口"
		}
	}
	if status != nil && status.SSHLocalPort > 0 {
		reserved[status.SSHLocalPort] = "该端口是 Lima SSH 管理端口"
	}
	return reserved
}

func hostPortAvailable(bindAddress string, port int) error {
	address := net.JoinHostPort(config.NormalizeListenAddress(bindAddress), strconv.Itoa(port))
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	return listener.Close()
}

func containsInt(values []int, target int) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func containsListeningPort(ports []vm.ListeningPort, target int) bool {
	for _, port := range ports {
		if port.Port == target {
			return true
		}
	}
	return false
}

func runningContainerUsingPort(containers []docker.ContainerInfo, port int) string {
	for _, container := range containers {
		if container.State != "running" {
			continue
		}
		for _, mapping := range container.PortsMap {
			if mapping.Protocol == "" || strings.EqualFold(mapping.Protocol, "tcp") {
				if mapping.HostPort == port {
					return container.Names
				}
			}
		}
	}
	return ""
}

func logPortForwardWarning(message string, err error) {
	if err != nil {
		log.Printf("[MacBox] %s: %v", message, err)
	}
}
