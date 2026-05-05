package client

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFutuBridgePathUsesExplicitEnv(t *testing.T) {
	t.Setenv("FUTU_BRIDGE", "./custom/futu_bridge.py")

	got := futuBridgePath()
	want := filepath.Clean("./custom/futu_bridge.py")
	if got != want {
		t.Fatalf("expected explicit FUTU_BRIDGE path %q, got %q", want, got)
	}
}

func TestFutuBridgePathDoesNotSearchWorkingDirectory(t *testing.T) {
	t.Setenv("FUTU_BRIDGE", "")
	// Point HOME at an empty dir so the ~/.guanfu and ~/.config/guanfu
	// fallbacks have nothing to find — forces the executable-sibling default.
	t.Setenv("HOME", t.TempDir())

	got := futuBridgePath()
	if got == "futu_bridge.py" || got == filepath.Join("internal", "client", "futu_bridge.py") {
		t.Fatalf("unexpected cwd-relative bridge path: %q", got)
	}
	if filepath.Dir(got) == "." {
		t.Fatalf("expected executable-relative bridge path, got %q", got)
	}

	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	want := filepath.Join(filepath.Dir(exe), "futu_bridge.py")
	if got != want {
		t.Fatalf("expected executable-relative path %q, got %q", want, got)
	}
}

func TestFutuBridgePathFallsBackToHomeGuanfu(t *testing.T) {
	t.Setenv("FUTU_BRIDGE", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	bridgeDir := filepath.Join(home, ".guanfu")
	if err := os.MkdirAll(bridgeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bridgePath := filepath.Join(bridgeDir, "futu_bridge.py")
	if err := os.WriteFile(bridgePath, []byte("# stub"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := futuBridgePath()
	if got != bridgePath {
		t.Fatalf("expected ~/.guanfu fallback %q, got %q", bridgePath, got)
	}
}

func TestFutuBridgePathFallsBackToConfigGuanfu(t *testing.T) {
	t.Setenv("FUTU_BRIDGE", "")
	home := t.TempDir()
	t.Setenv("HOME", home)

	bridgeDir := filepath.Join(home, ".config", "guanfu")
	if err := os.MkdirAll(bridgeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	bridgePath := filepath.Join(bridgeDir, "futu_bridge.py")
	if err := os.WriteFile(bridgePath, []byte("# stub"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	got := futuBridgePath()
	if got != bridgePath {
		t.Fatalf("expected ~/.config/guanfu fallback %q, got %q", bridgePath, got)
	}
}
