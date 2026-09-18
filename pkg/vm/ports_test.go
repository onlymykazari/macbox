package vm

import (
	"errors"
	"testing"

	"github.com/lulalulaluobo/macbox/pkg/config"
)

func TestParseSSListeningPorts(t *testing.T) {
	output := `LISTEN 0 4096 0.0.0.0:3000 0.0.0.0:* users:(("node",pid=123,fd=20))
LISTEN 0 4096 127.0.0.1:11434 0.0.0.0:* users:(("ollama",pid=88,fd=3))
LISTEN 0 4096 [::]:8080 [::]:* users:(("java",pid=66,fd=12))
LISTEN 0 4096 [::]:3000 [::]:* users:(("node",pid=123,fd=21))
LISTEN 0 4096 0.0.0.0:9000 0.0.0.0:*
not a valid ss line`

	got := parseSSListeningPorts(output)
	if len(got) != 4 {
		t.Fatalf("parsed %d ports, want 4: %#v", len(got), got)
	}
	if got[0].Port != 3000 || got[0].Process != "node" || got[0].PID != 123 {
		t.Fatalf("port 3000 = %#v", got[0])
	}
	if len(got[0].Addresses) != 2 || got[0].Addresses[0] != "0.0.0.0" || got[0].Addresses[1] != "::" {
		t.Fatalf("port 3000 addresses = %#v", got[0].Addresses)
	}
	if got[1].Port != 8080 || got[1].Addresses[0] != "::" {
		t.Fatalf("IPv6 listener = %#v", got[1])
	}
	if got[2].Port != 9000 || got[2].Process != "" || got[2].PID != 0 {
		t.Fatalf("listener without process metadata = %#v", got[2])
	}
}

func TestManualForwardOwnership(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	mgr := NewManager(config.DefaultConfig())

	change, err := mgr.AddManualForwardedPort(3000)
	if err != nil || !change.Changed || change.Source != PortForwardSourceManual {
		t.Fatalf("add manual port = %#v, %v", change, err)
	}

	change, err = mgr.AddManualForwardedPort(5244)
	if err != nil || change.Changed || change.Source != PortForwardSourceManaged {
		t.Fatalf("add managed port = %#v, %v", change, err)
	}

	change, err = mgr.AddManualForwardedPort(3000)
	if err != nil || change.Changed || change.Source != PortForwardSourceManual {
		t.Fatalf("re-add manual port = %#v, %v", change, err)
	}

	change, err = mgr.RemoveManualForwardedPort(3000)
	if err != nil || !change.Changed {
		t.Fatalf("remove manual port = %#v, %v", change, err)
	}
	if _, err := mgr.RemoveManualForwardedPort(5244); !errors.Is(err, ErrManualForwardPortNotOwned) {
		t.Fatalf("remove managed port error = %v, want ownership error", err)
	}

	snapshot, err := config.Snapshot(mgr.cfg)
	if err != nil {
		t.Fatalf("snapshot config: %v", err)
	}
	if containsPort(snapshot.VM.ForwardedPorts, 3000) || containsPort(snapshot.Terminal.ManualPublishedPorts, 3000) {
		t.Fatalf("removed manual port remains in config: %+v", snapshot)
	}
}

func TestManualForwardPortValidation(t *testing.T) {
	for _, port := range []int{1, 1023, 65536} {
		if err := ValidateManualForwardPort(port); err == nil {
			t.Errorf("port %d should be rejected", port)
		}
	}
	for _, port := range []int{1024, 3000, 65535} {
		if err := ValidateManualForwardPort(port); err != nil {
			t.Errorf("port %d rejected: %v", port, err)
		}
	}
}

func TestConfigDirtyRevisionDoesNotClearNewerChanges(t *testing.T) {
	mgr := NewManager(config.DefaultConfig())
	mgr.SetConfigDirty(true)
	first := mgr.ConfigDirtyRevision()

	mgr.SetConfigDirty(true)
	if mgr.ClearConfigDirtyIfRevision(first) {
		t.Fatal("cleared config dirty state for an obsolete revision")
	}
	if !mgr.IsConfigDirty() {
		t.Fatal("newer config change was marked clean")
	}

	current := mgr.ConfigDirtyRevision()
	if !mgr.ClearConfigDirtyIfRevision(current) {
		t.Fatal("failed to clear current config revision")
	}
	if mgr.IsConfigDirty() {
		t.Fatal("current config revision remains dirty")
	}
}

func TestConfigManualPortsAreIntersectedOnParse(t *testing.T) {
	data := []byte(`vm:
  forwardedPorts: [3000, 5244]
terminal:
  manualPublishedPorts: [3000, 8080, 3000]
`)
	cfg, err := config.Parse(data)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	if len(cfg.Terminal.ManualPublishedPorts) != 1 || cfg.Terminal.ManualPublishedPorts[0] != 3000 {
		t.Fatalf("manual ports = %#v", cfg.Terminal.ManualPublishedPorts)
	}
}
