package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDisableCodexSetsEnabledFalse(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	markerPath := filepath.Join(home, ".codex", "plugin-sources", "codex-otel-plugin", "plugins", "tracing")
	configPath := filepath.Join(home, ".codex", "gtrace.json")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{\"enabled\":true,\"endpoint\":\"https://example.com\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := disable([]string{"codex"}); err != nil {
		t.Fatal(err)
	}

	config := readJSONFile(t, configPath)
	if enabled, ok := config["enabled"].(bool); !ok || enabled {
		t.Fatalf("expected enabled=false, got %#v", config["enabled"])
	}
}

func TestEnableOpenClawSetsNestedEnabledTrue(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	markerPath := filepath.Join(home, ".openclaw", "extensions", "openclaw-otel-plugin")
	configPath := filepath.Join(home, ".openclaw", "openclaw.json")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	content := `{
  "plugins": {
    "entries": {
      "openclaw-otel-plugin": {
        "enabled": false
      }
    }
  }
}
`
	if err := os.WriteFile(configPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := enable([]string{"openclaw"}); err != nil {
		t.Fatal(err)
	}

	config := readJSONFile(t, configPath)
	plugins := config["plugins"].(map[string]any)
	entries := plugins["entries"].(map[string]any)
	entry := entries["openclaw-otel-plugin"].(map[string]any)
	if enabled, ok := entry["enabled"].(bool); !ok || !enabled {
		t.Fatalf("expected nested enabled=true, got %#v", entry["enabled"])
	}
}

func TestDisableHermesReturnsUnsupportedError(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	markerPath := filepath.Join(home, ".hermes", "plugins", "hermes-otel-plugin")
	configPath := filepath.Join(home, ".hermes", "config.yaml")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("hermes_otel_plugin:\n  enabled: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := disable([]string{"hermes"})
	if err == nil {
		t.Fatal("expected unsupported error")
	}
	if !strings.Contains(err.Error(), "does not support disable") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func setTestHome(t *testing.T, home string) {
	t.Helper()
	previousHome := os.Getenv("HOME")
	if err := os.Setenv("HOME", home); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Setenv("HOME", previousHome)
	})
}

func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	return value
}
