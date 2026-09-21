package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeConnectorConfigPreservesUnknownAndPrivacyFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	original := `{
  "endpoint": "https://old.example.com",
  "x_token": "old-token",
  "headers": {"Authorization": "keep"},
  "capture_content": "none",
  "unknown": {"keep": true}
}`
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeConnectorConfig([]string{
		"--path", path,
		"--download-base-url", "https://static.example.com/obs-agent-connector/",
		"--plugin-source", "oss",
		"--endpoint", "https://new.example.com",
		"--x-token", "new-token",
		"--global-tags", "env=prod\nteam=platform",
	}); err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	if value["endpoint"] != "https://new.example.com" || value["x_token"] != "new-token" {
		t.Fatalf("explicit values were not updated: %#v", value)
	}
	if value["capture_content"] != "none" || value["unknown"].(map[string]any)["keep"] != true {
		t.Fatalf("unknown/privacy values were not preserved: %#v", value)
	}
	if value["headers"].(map[string]any)["Authorization"] != "keep" {
		t.Fatalf("headers were not preserved: %#v", value)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("connector config mode must be 0600, got %o", info.Mode().Perm())
	}
}

func TestMergeConnectorConfigKeepsExistingValuesWhenArgumentsAreEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"endpoint":"https://existing.example.com","x_token":"existing-token"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeConnectorConfig([]string{"--path", path}); err != nil {
		t.Fatal(err)
	}
	cfg, err := readConnectorConfigObject(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["endpoint"] != "https://existing.example.com" || cfg["x_token"] != "existing-token" {
		t.Fatalf("empty arguments overwrote existing config: %#v", cfg)
	}
}

func TestMergeConnectorConfigPreservesDistinctUnicodeTagsAndCommaValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	userName := "\u6d4b\u8bd5\u7528\u6237"
	tags := strings.Join([]string{
		"user_id=user-redacted",
		"user_name=" + userName,
		"regions=us-east,us-west",
	}, "\n")
	if err := mergeConnectorConfig([]string{"--path", path, "--global-tags", tags}); err != nil {
		t.Fatal(err)
	}

	cfg, err := readConnectorConfigObject(path)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := cfg["global_tags"].([]any)
	if !ok || len(got) != 3 {
		t.Fatalf("global_tags = %#v, want three entries", cfg["global_tags"])
	}
	for index, want := range []string{"user_id=user-redacted", "user_name=" + userName, "regions=us-east,us-west"} {
		if got[index] != want {
			t.Fatalf("global_tags[%d] = %#v, want %q", index, got[index], want)
		}
	}
}

func TestMergeConnectorConfigRejectsCommaJoinedTags(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	err := mergeConnectorConfig([]string{
		"--path", path,
		"--global-tags", "user_id=user-redacted,user_name=test-user,env=prod",
	})
	if err == nil {
		t.Fatal("expected comma-joined global tags to be rejected")
	}
	if !strings.Contains(err.Error(), "-Tag @") {
		t.Fatalf("expected actionable PowerShell guidance, got %q", err)
	}
	if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
		t.Fatalf("invalid tag input must not write config, stat error = %v", statErr)
	}
}

func TestMergeConnectorConfigReplacesExistingFileOnWindows(t *testing.T) {
	previousGOOS := currentGOOS
	currentGOOS = "windows"
	t.Cleanup(func() { currentGOOS = previousGOOS })
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"endpoint":"https://old.example.com","unknown":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mergeConnectorConfig([]string{"--path", path, "--endpoint", "https://new.example.com"}); err != nil {
		t.Fatal(err)
	}
	cfg, err := readConnectorConfigObject(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg["endpoint"] != "https://new.example.com" || cfg["unknown"] != true {
		t.Fatalf("Windows replacement did not preserve merged config: %#v", cfg)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".config-backup-*.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary backups were not cleaned up: %v", matches)
	}
}
