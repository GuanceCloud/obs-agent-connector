package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoverNewRuntimeSkipsUnsupportedExternalPlugins(t *testing.T) {
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

	output := captureStdout(t, func() {
		if err := discover([]string{"-n", "--dry-run"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "New runtime unsupported (skipped): opencode") {
		t.Fatalf("expected explicit unsupported notice, got:\n%s", output)
	}
	if !strings.Contains(output, "opencode") || !strings.Contains(output, "unsupported") {
		t.Fatalf("expected unsupported OpenCode plan row, got:\n%s", output)
	}
	if strings.Contains(output, "Plugin Source") || strings.Contains(output, "opencode-otel-plugin") {
		t.Fatalf("-n discover must not plan an external OpenCode install:\n%s", output)
	}
	if strings.Contains(output, "secret") {
		t.Fatalf("discover output exposed X-Token: %s", output)
	}
}
