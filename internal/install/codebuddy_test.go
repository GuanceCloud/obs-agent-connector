package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallCodeBuddyIsIdempotentAndPreservesSettings(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source := filepath.Join(root, "obs-agent-connector")
	settings := filepath.Join(home, ".codebuddy", "settings.json")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, settings, map[string]any{"theme": "dark", "hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo keep"}}}, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/tmp/codebuddy-hook"}}}}}})
	for range 2 {
		_, err := InstallCodeBuddy(CodeBuddyOptions{Home: home, SourceExecutable: source, DestinationExecutable: filepath.Join(home, ".local", "bin", "obs-agent-connector"), SettingsFile: settings, NoConfig: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	var current map[string]any
	readTestJSON(t, settings, &current)
	if current["theme"] != "dark" {
		t.Fatalf("unknown setting lost: %#v", current)
	}
	hooks := current["hooks"].(map[string]any)
	if len(hooks["Stop"].([]any)) != 2 || len(hooks["SessionEnd"].([]any)) != 1 {
		t.Fatalf("Hook install is not idempotent: %#v", hooks)
	}
	body, _ := os.ReadFile(settings)
	if !strings.Contains(string(body), "hook codebuddy") || !strings.Contains(string(body), "echo keep") || strings.Contains(string(body), "/tmp/codebuddy-hook") {
		t.Fatalf("unexpected settings: %s", body)
	}
}

func TestInstallCodeBuddyPreservesSettingsFileIdentity(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source := filepath.Join(root, "obs-agent-connector")
	settings := filepath.Join(home, ".codebuddy", "settings.json")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(settings), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, settings, map[string]any{"theme": "dark"})
	before, err := os.Stat(settings)
	if err != nil {
		t.Fatal(err)
	}
	_, err = InstallCodeBuddy(CodeBuddyOptions{
		Home:                  home,
		SourceExecutable:      source,
		DestinationExecutable: source,
		SettingsFile:          settings,
		NoConfig:              true,
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(settings)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("settings file was replaced, which breaks active file watchers")
	}
}

func TestInstallCodeBuddyNoConfigPreservesExistingConfig(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source := filepath.Join(root, "obs-agent-connector")
	configPath := filepath.Join(home, ".codebuddy", "gtrace.json")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte(`{"enabled":false,"endpoint":"https://existing.example","unknown":{"keep":true}}` + "\n")
	if err := os.WriteFile(configPath, original, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := InstallCodeBuddy(CodeBuddyOptions{Home: home, SourceExecutable: source, DestinationExecutable: source, NoConfig: true})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(updated) != string(original) {
		t.Fatalf("--no-config changed config: %s", updated)
	}
}

func TestRemoveCodeBuddyPreservesUnrelatedHooksAndPurgesOnRequest(t *testing.T) {
	home := t.TempDir()
	settings := filepath.Join(home, ".codebuddy", "settings.json")
	configPath := filepath.Join(home, ".codebuddy", "gtrace.json")
	statePath := filepath.Join(home, ".codebuddy", "gtrace", "uploads", "state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, settings, map[string]any{"hooks": map[string]any{"Stop": []any{map[string]any{"hooks": []any{map[string]any{"command": `"/tmp/obs-agent-connector" hook codebuddy`}}}, map[string]any{"hooks": []any{map[string]any{"command": "echo keep"}}}}, "SessionEnd": []any{map[string]any{"hooks": []any{map[string]any{"command": `"/tmp/obs-agent-connector" hook codebuddy`}}}}}})
	writeTestJSON(t, configPath, map[string]any{"enabled": true})
	if err := os.WriteFile(statePath, []byte("{}"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := RemoveAdapter("codebuddy", home, RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.HookRemoved || result.ConfigRemoved || result.StatePurged {
		t.Fatalf("unexpected result: %#v", result)
	}
	var current map[string]any
	readTestJSON(t, settings, &current)
	if len(current["hooks"].(map[string]any)["Stop"].([]any)) != 1 {
		t.Fatalf("user Hook was not preserved: %#v", current)
	}
	result, err = RemoveAdapter("codebuddy", home, RemoveOptions{PurgeConfig: true, PurgeState: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.ConfigRemoved || !result.StatePurged {
		t.Fatalf("purge not reported: %#v", result)
	}
	if _, err := os.Stat(configPath); !os.IsNotExist(err) {
		t.Fatalf("config remains: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codebuddy", "gtrace")); !os.IsNotExist(err) {
		t.Fatalf("state remains: %v", err)
	}
}
