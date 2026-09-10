package install

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkBuddyInstallMigrateUpdateRemove(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, "WorkBuddy Profile")
	t.Setenv("WORKBUDDY_CONFIG_DIR", profile)
	executable := filepath.Join(home, "obs-agent-connector")
	os.WriteFile(executable, []byte("test executable"), 0700)
	os.MkdirAll(filepath.Join(profile, "plugins"), 0700)
	settings := filepath.Join(profile, "settings.json")
	os.WriteFile(settings, []byte(`{"theme":"dark","enabledPlugins":{"workbuddy-otel-plugin@guance":true,"other@guance":true},"hooks":{"Stop":[{"matcher":"keep","hooks":[{"command":"/old/workbuddy-otel-plugin/src/workbuddy-hook.js"},{"command":"echo user-hook"}]}]}}`), 0600)
	registry := filepath.Join(profile, "plugins", "installed_plugins.json")
	os.WriteFile(registry, []byte(`{"version":2,"plugins":{"workbuddy-otel-plugin@guance":[{}],"other@guance":[{}]}}`), 0600)
	legacy := filepath.Join(profile, "gtrace.json")
	original := `{"endpoint":"https://test.invalid","headers":{"X-Token":"test-only"},"enabled":false,"capture_content":false,"max_chars":123,"debug":true,"custom":"keep"}`
	os.WriteFile(legacy, []byte(original), 0600)
	o := WorkBuddyOptions{ProfileDir: profile, CodeBuddyOptions: CodeBuddyOptions{Home: home, SourceExecutable: executable, DestinationExecutable: executable, NoConfig: true}}
	result, err := InstallWorkBuddy(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(result.ConfigFile); !os.IsNotExist(err) {
		t.Fatal("--no-config wrote a managed config")
	}
	data, _ := os.ReadFile(legacy)
	if string(data) != original {
		t.Fatal("legacy config changed")
	}
	o.NoConfig = false
	o.MaxChars = 456
	if _, err = InstallWorkBuddy(o); err != nil {
		t.Fatal(err)
	}
	cfg, _, err := ReadRuntimeConfig(result.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["enabled"] != false || cfg["captureContent"] != "none" || cfg["maxChars"] != float64(456) || cfg["custom"] != "keep" {
		t.Fatalf("config lost: %+v", cfg)
	}
	before, _ := os.ReadFile(result.ConfigFile)
	o.NoConfig = true
	if _, err = InstallWorkBuddy(o); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(result.ConfigFile)
	if string(before) != string(after) {
		t.Fatal("update rewrote config")
	}
	value, _ := readJSONObject(settings)
	if value["theme"] != "dark" {
		t.Fatal("unrelated settings lost")
	}
	enabled := value["enabledPlugins"].(map[string]any)
	if enabled["workbuddy-otel-plugin@guance"] != false || enabled["other@guance"] != true {
		t.Fatal("plugin migration incorrect")
	}
	data, _ = os.ReadFile(settings)
	if strings.Contains(string(data), "workbuddy-hook.js") || !strings.Contains(string(data), "echo user-hook") {
		t.Fatal("handler migration incorrect")
	}
	if strings.Count(string(data), "hook workbuddy") != len(workBuddyEvents) {
		t.Fatal("hooks duplicated")
	}
	entries, _ := readJSONObject(registry)
	plugins := entries["plugins"].(map[string]any)
	if plugins["workbuddy-otel-plugin@guance"] != nil || plugins["other@guance"] == nil {
		t.Fatal("registry migration incorrect")
	}
	if _, err = RemoveAdapter("workbuddy", home, RemoveOptions{PurgeManaged: true}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(settings)
	if strings.Contains(string(data), "hook workbuddy") || !strings.Contains(string(data), "echo user-hook") {
		t.Fatal("remove damaged hooks")
	}
	if _, err = os.Stat(legacy); err != nil {
		t.Fatal("legacy config deleted")
	}
}

func TestWorkBuddyInvalidSettingsDoesNotMutateConfig(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".workbuddy")
	os.MkdirAll(profile, 0700)
	os.WriteFile(filepath.Join(profile, "settings.json"), []byte("{"), 0600)
	_, err := InstallWorkBuddy(WorkBuddyOptions{ProfileDir: profile, CodeBuddyOptions: CodeBuddyOptions{Home: home, Endpoint: "https://test.invalid"}})
	if err == nil {
		t.Fatal("expected parse error")
	}
	if _, err = os.Stat(filepath.Join(home, ".obs-agent-connector", "workbuddy")); !os.IsNotExist(err) {
		t.Fatal("config created on failure")
	}
}
