package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestClaudeDesktopLinuxDiscoveryRequiresDesktopLauncher(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix launcher lookup")
	}
	binDir := t.TempDir()
	t.Setenv("PATH", binDir)
	if err := os.WriteFile(filepath.Join(binDir, "claude"), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, detected := findClaudeDesktopCommand("linux", nil); detected {
		t.Fatal("Desktop discovery accepted the Claude CLI instead of its own launcher")
	}
	if err := os.WriteFile(filepath.Join(binDir, "claude-desktop"), nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if command, detected := findClaudeDesktopCommand("linux", nil); !detected || command != "claude-desktop" {
		t.Fatalf("unexpected Linux Claude Desktop launcher: %q, %v", command, detected)
	}
}

func TestClaudeDesktopWindowsDiscoveryUsesPackageRegistration(t *testing.T) {
	if _, ok := findClaudeDesktopCommand("windows", func() bool { return false }); ok {
		t.Fatal("Desktop discovery accepted an absent MSIX package")
	}
	if command, detected := findClaudeDesktopCommand("windows", func() bool { return true }); !detected || command != "Claude (MSIX)" {
		t.Fatalf("unexpected Windows Claude Desktop discovery result: %q, %v", command, detected)
	}
}

func TestClaudeDesktopMacDiscoveryUsesApplicationBundle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix bundle executable lookup")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	executable := filepath.Join(home, "Applications", "Claude.app", "Contents", "MacOS", "Claude")
	if err := os.MkdirAll(filepath.Dir(executable), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(executable, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	if command, ok := findClaudeDesktopCommand("darwin", nil); !ok || command != executable {
		t.Fatalf("Claude Desktop did not find the user's app bundle: %q", command)
	}
}
