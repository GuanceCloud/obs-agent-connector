package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRemoveClaudeDeletesConfigAndPurgesStateOnRequest(t *testing.T) {
	home := t.TempDir()
	settingsPath := filepath.Join(home, ".claude", "settings.json")
	configPath := filepath.Join(home, ".claude", "gtrace.json")
	statePath := filepath.Join(home, ".claude", "state", "agent-telemetry")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(statePath, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, settingsPath, map[string]any{
		"theme": "dark",
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{"hooks": []any{map[string]any{
					"type": "command", "command": "/tmp/agent-telemetry", "args": []any{"hook", "claude"},
				}}},
				map[string]any{"hooks": []any{map[string]any{
					"type": "command", "command": "echo keep",
				}}},
			},
			"SessionEnd": []any{
				map[string]any{"hooks": []any{map[string]any{
					"type": "command", "command": "/tmp/gtrace-agent", "args": []any{"hook", "claude"},
				}}},
			},
		},
	})
	writeTestJSON(t, configPath, map[string]any{"enabled": true})

	result, err := RemoveAdapter("claude", home, RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.HookRemoved || result.ConfigRemoved || result.StatePurged {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err := os.Stat(configPath); err != nil {
		t.Fatalf("config should be preserved: %v", err)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("state should be preserved without purge: %v", err)
	}

	var settings map[string]any
	readTestJSON(t, settingsPath, &settings)
	stop := settings["hooks"].(map[string]any)["Stop"].([]any)
	sessionEnd := settings["hooks"].(map[string]any)["SessionEnd"].([]any)
	if len(stop) != 1 || len(sessionEnd) != 0 || settings["theme"] != "dark" {
		t.Fatalf("unrelated settings were not preserved: %#v", settings)
	}

	result, err = RemoveAdapter("claude", home, RemoveOptions{PurgeConfig: true, PurgeState: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ConfigRemoved || !result.StatePurged {
		t.Fatalf("purge was not reported: %#v", result)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("config was not purged: %v", err)
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("state was not purged: %v", err)
	}
}

func TestConnectorOnlyRemovalPreservesLegacyHook(t *testing.T) {
	home := t.TempDir()
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, hooksPath, map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/tmp/agent-telemetry hook codex"}}},
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/tmp/obs-agent-connector hook codex"}}},
			},
		},
	})
	legacyTrustKey := hooksPath + ":stop:0:0"
	connectorTrustKey := hooksPath + ":stop:1:0"
	toml := fmt.Sprintf(`model = "keep-me"

[hooks.state.%q]
trusted_hash = "legacy-hash"

[hooks.state.%q]
trusted_hash = "connector-hash"

[unrelated]
enabled = true
`, legacyTrustKey, connectorTrustKey)
	if err := os.WriteFile(configPath, []byte(toml), 0o640); err != nil {
		t.Fatal(err)
	}

	result, err := RemoveAdapter("codex", home, RemoveOptions{ConnectorOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.HookRemoved || !result.TrustRemoved {
		t.Fatalf("connector Hook and trust should be removed: %#v", result)
	}
	var settings map[string]any
	readTestJSON(t, hooksPath, &settings)
	groups := settings["hooks"].(map[string]any)["Stop"].([]any)
	if len(groups) != 1 {
		t.Fatalf("legacy Hook should be preserved: %#v", groups)
	}
	updated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	if strings.Contains(text, connectorTrustKey) || strings.Contains(text, "connector-hash") {
		t.Fatalf("connector trust state was not removed: %s", text)
	}
	if !strings.Contains(text, legacyTrustKey) || !strings.Contains(text, "legacy-hash") || !strings.Contains(text, "[unrelated]") {
		t.Fatalf("unrelated trust and config were not preserved: %s", text)
	}
	if info, err := os.Stat(configPath); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("config permissions changed: info=%v err=%v", info, err)
	}
}

func TestRemoveCodexCleansOrphanedTrustWhenHookListIsEmpty(t *testing.T) {
	home := t.TempDir()
	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	configPath := filepath.Join(home, ".codex", "config.toml")
	if err := os.MkdirAll(filepath.Dir(hooksPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, hooksPath, map[string]any{"hooks": map[string]any{"Stop": []any{}}})
	firstKey := hooksPath + ":stop:0:0"
	secondKey := hooksPath + ":stop:1:0"
	otherKey := filepath.Join(home, ".codex", "project-hooks.json") + ":stop:0:0"
	toml := fmt.Sprintf(`model = "keep-me"

[hooks.state.%q]
trusted_hash = "first-stale-hash"

[hooks.state.%q]
trusted_hash = "second-stale-hash"

[hooks.state.%q]
trusted_hash = "other-hook-hash"
`, firstKey, secondKey, otherKey)
	if err := os.WriteFile(configPath, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}

	result, err := RemoveAdapter("codex", home, RemoveOptions{ConnectorOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.HookRemoved || !result.TrustRemoved {
		t.Fatalf("expected only orphaned trust removal: %#v", result)
	}
	updated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	for _, removed := range []string{firstKey, secondKey, "first-stale-hash", "second-stale-hash"} {
		if strings.Contains(text, removed) {
			t.Fatalf("orphaned trust state %q remains: %s", removed, text)
		}
	}
	if !strings.Contains(text, otherKey) || !strings.Contains(text, "other-hook-hash") || !strings.Contains(text, `model = "keep-me"`) {
		t.Fatalf("unrelated config was not preserved: %s", text)
	}
}
