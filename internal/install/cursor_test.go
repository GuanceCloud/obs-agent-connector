package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCursorReconcilesFlatHooksAndPreservesConfig(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "source", "obs-agent-connector")
	destination := filepath.Join(home, ".local", "bin", "obs-agent-connector")
	hooksFile := filepath.Join(home, ".cursor", "hooks.json")
	configFile := filepath.Join(home, ".cursor", "gtrace.json")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(hooksFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{"theme":"dark","hooks":{"stop":[{"command":"echo keep"},{"command":"/tmp/cursor-otel-plugin/bin/hook"}]}}`
	if err := os.WriteFile(hooksFile, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	config := []byte("{\"enabled\":false,\"endpoint\":\"https://existing.example.com\",\"unknown\":true}\n")
	if err := os.WriteFile(configFile, config, 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := InstallCursor(CursorOptions{
		Home: home, SourceExecutable: source, DestinationExecutable: destination,
		HooksFile: hooksFile, ConfigFile: configFile, NoConfig: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Executable != destination {
		t.Fatalf("executable = %q, want %q", result.Executable, destination)
	}
	body, err := os.ReadFile(hooksFile)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "echo keep") || strings.Contains(text, "cursor-otel-plugin") {
		t.Fatalf("Cursor Hooks were not safely reconciled: %s", text)
	}
	var hooks map[string]any
	if err := json.Unmarshal(body, &hooks); err != nil {
		t.Fatal(err)
	}
	if hooks["version"] != float64(1) {
		t.Fatalf("Cursor hooks version = %#v", hooks["version"])
	}
	values := hooks["hooks"].(map[string]any)
	for _, event := range cursorHookEvents {
		entries := values[event].([]any)
		found := false
		for _, value := range entries {
			entry := value.(map[string]any)
			command := strings.ReplaceAll(entry["command"].(string), `\\`, `\`)
			if strings.Contains(command, destination) && strings.Contains(command, "hook cursor "+event) {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing managed %s Hook: %s", event, text)
		}
	}
	updatedConfig, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(updatedConfig) != string(config) {
		t.Fatalf("NoConfig changed Cursor config:\nwant %s\n got %s", config, updatedConfig)
	}
}

func TestRemoveCursorPreservesUnrelatedHooksAndConfigByDefault(t *testing.T) {
	home := t.TempDir()
	hooksFile := filepath.Join(home, ".cursor", "hooks.json")
	configFile := filepath.Join(home, ".cursor", "gtrace.json")
	if err := os.MkdirAll(filepath.Dir(hooksFile), 0o755); err != nil {
		t.Fatal(err)
	}
	settings := map[string]any{"version": 1, "hooks": map[string]any{
		"stop": []any{
			map[string]any{"command": `"/tmp/obs-agent-connector" hook cursor stop`},
			map[string]any{"command": "echo keep"},
		},
	}}
	writeTestJSON(t, hooksFile, settings)
	if err := os.WriteFile(configFile, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RemoveAdapter("cursor", home, RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.HookRemoved || result.ConfigRemoved {
		t.Fatalf("unexpected remove result: %#v", result)
	}
	body, err := os.ReadFile(hooksFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "hook cursor") || !strings.Contains(string(body), "echo keep") {
		t.Fatalf("unexpected remaining Hooks: %s", body)
	}
	if _, err := os.Stat(configFile); err != nil {
		t.Fatalf("Cursor config must be preserved: %v", err)
	}
}
