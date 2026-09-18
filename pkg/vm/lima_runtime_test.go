package vm

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/lulalulaluobo/macbox/pkg/config"
)

func TestLimaStartNeedsRecovery(t *testing.T) {
	for _, output := range []string{
		"Instance \\\"macbox\\\" has configuration errors: failed to connect to ha.sock",
		`failed to connect to hostagent: configuration errors`,
	} {
		if !limaStartNeedsRecovery(output) {
			t.Errorf("limaStartNeedsRecovery(%q) = false", output)
		}
	}
	for _, output := range []string{
		"disk image is missing",
		"instance is already running",
	} {
		if limaStartNeedsRecovery(output) {
			t.Errorf("limaStartNeedsRecovery(%q) = true", output)
		}
	}
}

func TestVMRestartIntegration(t *testing.T) {
	if os.Getenv("MACBOX_VM_INTEGRATION") != "1" {
		t.Skip("set MACBOX_VM_INTEGRATION=1 to restart the local MacBox VM")
	}

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("load MacBox config: %v", err)
	}
	mgr := NewManager(cfg)
	status, err := mgr.GetStatusContext(context.Background())
	if err != nil {
		t.Fatalf("read VM status: %v", err)
	}
	if status == nil || status.Status != "Running" {
		t.Skipf("VM is not running: %+v", status)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	projectRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve project root: %v", err)
	}
	if err := mgr.Restart(ctx, projectRoot); err != nil {
		t.Fatalf("restart VM: %v", err)
	}
	status, err = mgr.GetStatusContext(ctx)
	if err != nil {
		t.Fatalf("read VM status after restart: %v", err)
	}
	if status == nil || status.Status != "Running" {
		t.Fatalf("VM status after restart = %+v, want Running", status)
	}
}
