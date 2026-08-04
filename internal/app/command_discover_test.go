package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverRejectsRemovedNewRuntimeFlag(t *testing.T) {
	home := t.TempDir()
	binDir := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", binDir)
	for _, name := range []string{"codex", "opencode"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	configPath := filepath.Join(home, ".obs-agent-connector", "config.json")
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", configPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"endpoint":"https://example.invalid","x_token":"secret"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	previousGOOS := currentGOOS
	currentGOOS = "linux"
	t.Cleanup(func() { currentGOOS = previousGOOS })

	err := discover([]string{"-n", "--dry-run"})
	if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -n") {
		t.Fatalf("expected removed -n flag error, got %v", err)
	}
}
