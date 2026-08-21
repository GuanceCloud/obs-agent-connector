package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallKiroWritesV3HooksAndManagedConfig(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "obs-agent-connector")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	hooksFile := filepath.Join(home, ".kiro", "hooks", "obs-agent-connector.json")
	if err := os.MkdirAll(filepath.Dir(hooksFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooksFile, []byte(`{"version":"v1","hooks":[{"name":"keep","trigger":"Stop","action":{"type":"command","command":"echo keep"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	enabled := true
	result, err := InstallKiro(KiroOptions{
		Home: home, SourceExecutable: source, DestinationExecutable: source,
		Endpoint: "https://example.invalid", InstallType: "gtrace", XToken: "agent_test", Enabled: &enabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ConfigFile != filepath.Join(home, ".obs-agent-connector", "kiro", "gtrace.json") || !result.Configured {
		t.Fatalf("unexpected install result: %#v", result)
	}
	body, err := os.ReadFile(hooksFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "echo keep") {
		t.Fatalf("unrelated Kiro Hook was not preserved: %s", body)
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatal(err)
	}
	entries := value["hooks"].([]any)
	if len(entries) != len(kiroHookEvents)+1 {
		t.Fatalf("unexpected Kiro Hook count: %d", len(entries))
	}
	for _, event := range kiroHookEvents {
		if !strings.Contains(string(body), "hook kiro "+event) {
			t.Fatalf("missing %s Kiro Hook: %s", event, body)
		}
	}
}

func TestInstallKiroNoConfigPreservesRuntimeConfig(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "obs-agent-connector")
	configFile := filepath.Join(home, ".obs-agent-connector", "kiro", "gtrace.json")
	for _, path := range []string{source, configFile} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	original := []byte("{\"enabled\":false,\"unknown\":true}\n")
	if err := os.WriteFile(configFile, original, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallKiro(KiroOptions{Home: home, SourceExecutable: source, DestinationExecutable: source, NoConfig: true}); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != string(original) {
		t.Fatalf("--no-config changed Kiro config: %s", body)
	}
}

func TestRemoveKiroPreservesUnrelatedHookEntries(t *testing.T) {
	home := t.TempDir()
	source := filepath.Join(home, "obs-agent-connector")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallKiro(KiroOptions{Home: home, SourceExecutable: source, DestinationExecutable: source}); err != nil {
		t.Fatal(err)
	}
	hooksFile := filepath.Join(home, ".kiro", "hooks", "obs-agent-connector.json")
	value, err := readJSONObject(hooksFile)
	if err != nil {
		t.Fatal(err)
	}
	value["hooks"] = append(value["hooks"].([]any), map[string]any{
		"name": "keep", "trigger": "Stop", "action": map[string]any{"type": "command", "command": "echo keep"},
	})
	if err := writeJSONAtomic(hooksFile, value); err != nil {
		t.Fatal(err)
	}
	result, err := RemoveAdapter("kiro", home, RemoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !result.HookRemoved {
		t.Fatalf("managed Kiro Hooks were not removed: %#v", result)
	}
	body, err := os.ReadFile(hooksFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "echo keep") || strings.Contains(string(body), "hook kiro") {
		t.Fatalf("unexpected remaining Kiro Hooks: %s", body)
	}
}
