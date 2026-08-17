package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConfigListCodexShowsCurrentValuesAndRedactsToken(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	markerPath := filepath.Join(home, ".codex", "plugin-sources", "codex-otel-plugin", "plugins", "tracing")
	configPath := filepath.Join(home, ".obs-agent-connector", "codex", "gtrace.json")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	config := `{
  "enabled": true,
  "endpoint": "https://example.com",
  "tracePath": "custom/traces",
  "metricsPath": "custom/metrics",
  "headers": {
    "X-Token": "agent_secret_value",
    "Region": "cn"
  },
  "resourceAttributes": {
    "env": "prod"
  },
  "captureContent": "preview",
  "max_chars": 4096
}
`
	if err := os.WriteFile(configPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(t, func() {
		if err := configCommand([]string{"codex", "list"}); err != nil {
			t.Fatal(err)
		}
	})

	for _, expected := range []string{
		"Agent",
		"Config",
		"Parameter",
		"enabled",
		"https://example.com",
		"custom/traces",
		"custom/metrics",
		"<configured>",
		"Region=cn",
		"env=prod",
		"--header and --tag both support one or more KEY=VALUE parameters.",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected output to contain %q, got:\n%s", expected, output)
		}
	}
	if strings.Contains(output, "agent_secret_value") {
		t.Fatalf("config list must redact X-Token, got:\n%s", output)
	}
}

func TestConfigEditCodexUpdatesSelectedFieldsAndPreservesUnknown(t *testing.T) {
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
	config := `{
  "enabled": true,
  "endpoint": "https://old.example.com",
  "tracePath": "custom/traces",
  "headers": {
    "Keep": "yes"
  },
  "resourceAttributes": {
    "env": "prod"
  },
  "unknown": {
    "keep": true
  }
}
`
	if err := os.WriteFile(legacyConfigPath, []byte(config), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := configCommand([]string{
		"codex", "edit",
		"--enabled=false",
		"--endpoint=https://llm-openway.truewatch.com",
		"--header", "Region=cn",
		"--tag", "team=platform",
	}); err != nil {
		t.Fatal(err)
	}

	updated := readJSONFile(t, configPath)
	if enabled, ok := updated["enabled"].(bool); !ok || enabled {
		t.Fatalf("expected enabled=false, got %#v", updated["enabled"])
	}
	if endpoint, _ := updated["endpoint"].(string); endpoint != "https://llm-openway.truewatch.com" {
		t.Fatalf("unexpected endpoint %#v", updated["endpoint"])
	}
	if tracePath, _ := updated["tracePath"].(string); tracePath != "custom/traces" {
		t.Fatalf("existing tracePath must be preserved, got %#v", updated["tracePath"])
	}
	headers := updated["headers"].(map[string]any)
	if headers["Keep"] != "yes" || headers["Region"] != "cn" {
		t.Fatalf("unexpected headers %#v", headers)
	}
	resource := updated["resourceAttributes"].(map[string]any)
	if resource["env"] != "prod" || resource["team"] != "platform" {
		t.Fatalf("unexpected resource attributes %#v", resource)
	}
	unknown := updated["unknown"].(map[string]any)
	if keep, ok := unknown["keep"].(bool); !ok || !keep {
		t.Fatalf("unknown config must be preserved, got %#v", unknown)
	}
}

func TestConfigEditCodexCreatesMissingConfigForInstalledPlugin(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	markerPath := filepath.Join(home, ".codex", "plugin-sources", "codex-otel-plugin", "plugins", "tracing")
	configPath := filepath.Join(home, ".obs-agent-connector", "codex", "gtrace.json")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := configCommand([]string{
		"codex", "edit",
		"--enabled=false",
		"--endpoint=https://llm-openway.truewatch.com",
	}); err != nil {
		t.Fatal(err)
	}

	created := readJSONFile(t, configPath)
	if enabled, ok := created["enabled"].(bool); !ok || enabled {
		t.Fatalf("expected enabled=false, got %#v", created["enabled"])
	}
	if endpoint, _ := created["endpoint"].(string); endpoint != "https://llm-openway.truewatch.com" {
		t.Fatalf("unexpected endpoint %#v", created["endpoint"])
	}
	if tracePath, _ := created["tracePath"].(string); tracePath != "v1/write/otel-llm" {
		t.Fatalf("unexpected tracePath %#v", created["tracePath"])
	}
	if metricsPath, _ := created["metricsPath"].(string); metricsPath != "v1/write/otel-metrics" {
		t.Fatalf("unexpected metricsPath %#v", created["metricsPath"])
	}
}

func TestConfigEditRejectsUnsupportedAgentConfig(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)

	markerPath := filepath.Join(home, ".hermes", "plugins", "hermes-otel-plugin")
	if err := os.MkdirAll(markerPath, 0o755); err != nil {
		t.Fatal(err)
	}

	err := configCommand([]string{"hermes", "edit", "--enabled=false"})
	if err == nil {
		t.Fatal("expected unsupported config error")
	}
	if !strings.Contains(err.Error(), "does not support config") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestConfigEditRequiresAtLeastOneParameter(t *testing.T) {
	err := configCommand([]string{"codex", "edit"})
	if err == nil {
		t.Fatal("expected missing parameter error")
	}
	if !strings.Contains(err.Error(), "requires one or more parameters") {
		t.Fatalf("unexpected error: %v", err)
	}
}
