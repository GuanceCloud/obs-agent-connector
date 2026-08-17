package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallClaudeCopiesBinaryAndPreservesHooks(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source", "agent-telemetry")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := filepath.Join(root, "home", ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	initial := map[string]any{
		"theme": "dark",
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo keep"}}},
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "python claude_otel_hook.py"}}},
			},
		},
	}
	writeTestJSON(t, settings, initial)
	destination := filepath.Join(root, "home", ".local", "bin", "agent-telemetry")

	for range 2 {
		result, err := InstallClaude(ClaudeOptions{
			Home:                  filepath.Join(root, "home"),
			SourceExecutable:      source,
			DestinationExecutable: destination,
			SettingsFile:          settings,
		})
		if err != nil {
			t.Fatal(err)
		}
		if result.Executable != destination || result.SettingsFile != settings {
			t.Fatalf("unexpected result: %#v", result)
		}
	}
	body, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "binary" {
		t.Fatalf("copied binary = %q", body)
	}

	var current map[string]any
	body, err = os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &current); err != nil {
		t.Fatal(err)
	}
	if current["theme"] != "dark" {
		t.Fatalf("unknown settings were lost: %#v", current)
	}
	hooks := current["hooks"].(map[string]any)
	stop := hooks["Stop"].([]any)
	sessionEnd := hooks["SessionEnd"].([]any)
	if len(stop) != 2 || len(sessionEnd) != 1 {
		t.Fatalf("installer is not idempotent: stop=%#v sessionEnd=%#v", stop, sessionEnd)
	}
	managed := sessionEnd[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if got := managed["command"]; got != `"`+destination+`" hook claude` {
		t.Fatalf("managed command = %q", got)
	}
	if _, exists := managed["args"]; exists {
		t.Fatalf("managed Hook still uses unsupported args: %#v", managed)
	}
}

func TestInstallClaudeRejectsInvalidSettingsWithoutOverwrite(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "agent-telemetry")
	settings := filepath.Join(root, "settings.json")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settings, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(root, "installed", "agent-telemetry")
	_, err := InstallClaude(ClaudeOptions{
		SourceExecutable:      source,
		DestinationExecutable: destination,
		SettingsFile:          settings,
	})
	if err == nil {
		t.Fatal("expected invalid settings error")
	}
	body, readErr := os.ReadFile(settings)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(body) != "{broken" {
		t.Fatalf("invalid settings were overwritten: %q", body)
	}
	if _, statErr := os.Stat(destination); !os.IsNotExist(statErr) {
		t.Fatalf("runtime copied before settings validation: %v", statErr)
	}
}

func TestInstallClaudeConfiguresGTraceAndPreservesUnknownFields(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source := filepath.Join(root, "agent-telemetry")
	configPath := filepath.Join(home, ".claude", "gtrace.json")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, configPath, map[string]any{
		"enabled": false,
		"unknown": "keep",
		"headers": map[string]any{"X-Custom": "keep"},
	})

	result, err := InstallClaude(ClaudeOptions{
		Home:               home,
		SourceExecutable:   source,
		Endpoint:           "https://new.example/",
		InstallType:        "gtrace",
		XToken:             "placeholder",
		ResourceAttributes: []string{"env=test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Configured {
		t.Fatalf("configuration was not written: %#v", result)
	}
	var config map[string]any
	readTestJSON(t, configPath, &config)
	if config["enabled"] != false || config["endpoint"] != "https://new.example" || config["unknown"] != "keep" {
		t.Fatalf("configuration was not preserved: %#v", config)
	}
	headers := config["headers"].(map[string]any)
	if headers["X-Custom"] != "keep" || headers["X-Token"] != "placeholder" {
		t.Fatalf("unexpected headers: %#v", headers)
	}
}

func writeTestJSON(t *testing.T, path string, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}
