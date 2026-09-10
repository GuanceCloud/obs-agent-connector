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
	legacyConfigPath := filepath.Join(home, ".codex", "gtrace.json")
	configPath := filepath.Join(home, ".obs-agent-connector", "codex", "gtrace.json")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(legacyConfigPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyConfigPath, []byte("{\"enabled\":true,\"endpoint\":\"https://example.com\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := disable([]string{"codex"}); err != nil {
		t.Fatal(err)
	}

	config := readJSONFile(t, configPath)
	if enabled, ok := config["enabled"].(bool); !ok || enabled {
		t.Fatalf("expected enabled=false, got %#v", config["enabled"])
	}
	if config["endpoint"] != "https://example.com" {
		t.Fatalf("legacy config values were not migrated: %#v", config)
	}
}

func TestEnableOpenClawSetsManagedEnabledTrue(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	markerPath := filepath.Join(home, ".obs-agent-connector", "openclaw", "plugin", "runtime.json")
	configPath := filepath.Join(home, ".obs-agent-connector", "openclaw", "gtrace.json")
	if err := os.MkdirAll(filepath.Dir(markerPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(markerPath, []byte(`{"command":"obs-agent-connector","args":["hook","openclaw"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"enabled":false,"endpoint":"https://example.com"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := enable([]string{"openclaw"}); err != nil {
		t.Fatal(err)
	}
	if enabled, _ := readJSONFile(t, configPath)["enabled"].(bool); !enabled {
		t.Fatal("expected enabled=true")
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

func TestDisableOpencodeSetsEnabledFalse(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	markerPath := filepath.Join(home, ".config", "opencode", "plugins", "opencode-otel-plugin")
	configPath := filepath.Join(home, ".config", "opencode", "gtrace.json")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte("{\"enabled\":true,\"endpoint\":\"https://example.com\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := disable([]string{"opencode"}); err != nil {
		t.Fatal(err)
	}

	config := readJSONFile(t, configPath)
	if enabled, ok := config["enabled"].(bool); !ok || enabled {
		t.Fatalf("expected enabled=false, got %#v", config["enabled"])
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
