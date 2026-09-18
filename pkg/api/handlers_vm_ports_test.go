package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lulalulaluobo/macbox/pkg/auth"
	"github.com/lulalulaluobo/macbox/pkg/config"
	"github.com/lulalulaluobo/macbox/pkg/docker"
	"github.com/lulalulaluobo/macbox/pkg/vm"
)

func TestRunningContainerUsingPort(t *testing.T) {
	containers := []docker.ContainerInfo{
		{Names: "stopped", State: "exited", PortsMap: []docker.PortMapping{{HostPort: 3000, Protocol: "tcp"}}},
		{Names: "udp-only", State: "running", PortsMap: []docker.PortMapping{{HostPort: 3000, Protocol: "udp"}}},
		{Names: "web", State: "running", PortsMap: []docker.PortMapping{{HostPort: 3000, Protocol: "tcp"}}},
	}
	if got := runningContainerUsingPort(containers, 3000); got != "web" {
		t.Fatalf("runningContainerUsingPort() = %q, want web", got)
	}
	if got := runningContainerUsingPort(containers, 3100); got != "" {
		t.Fatalf("runningContainerUsingPort() = %q for unused port", got)
	}
}

func TestReservedVMPorts(t *testing.T) {
	cfg := config.DefaultConfig()
	status := &vm.VMStatus{SSHLocalPort: 60022}
	reserved := reservedVMPorts(cfg, status)
	for port, want := range map[int]string{
		19808: "该端口是 MacBox Web 管理端口",
		4455:  "该端口是 MacBox Samba 服务端口",
		60022: "该端口是 Lima SSH 管理端口",
	} {
		if reserved[port] != want {
			t.Errorf("reserved port %d = %q, want %q", port, reserved[port], want)
		}
	}
}

func TestHostPortAvailableDetectsConflict(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen test port: %v", err)
	}
	defer listener.Close()
	_, portText, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split test address: %v", err)
	}
	var port int
	if _, err := fmt.Sscanf(portText, "%d", &port); err != nil {
		t.Fatalf("parse test port: %v", err)
	}
	if err := hostPortAvailable("127.0.0.1", port); err == nil {
		t.Fatalf("hostPortAvailable accepted occupied port %d", port)
	}
}

func TestPortForwardRoutesRequireAdmin(t *testing.T) {
	server := &Server{mux: http.NewServeMux()}
	server.registerRoutes()
	user := &auth.User{ID: "u-user", Role: "user", Enabled: true}
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/vm/listening-ports"},
		{http.MethodGet, "/api/vm/port-forwards"},
		{http.MethodPost, "/api/vm/port-forwards"},
		{http.MethodDelete, "/api/vm/port-forwards/3000"},
	} {
		req := httptest.NewRequest(test.method, test.path, nil)
		req = req.WithContext(context.WithValue(req.Context(), userContextKey, user))
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		if response.Code != http.StatusForbidden {
			t.Errorf("%s %s status = %d, want 403", test.method, test.path, response.Code)
		}
	}
}
