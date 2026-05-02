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
