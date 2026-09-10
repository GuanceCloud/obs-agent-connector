package install

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func openClawOptions(t *testing.T) OpenClawOptions {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OPENCLAW_STATE_DIR", "")
	t.Setenv("OPENCLAW_CONFIG_PATH", "")
	exe := filepath.Join(home, "connector with spaces")
	if err := os.WriteFile(exe, []byte("runtime"), 0700); err != nil {
		t.Fatal(err)
	}
	return OpenClawOptions{Home: home, SourceExecutable: exe, DestinationExecutable: exe, Endpoint: "https://example.invalid", XToken: "synthetic-secret", InstallType: "gtrace"}
}
func TestOpenClawLifecycle(t *testing.T) {
	o := openClawOptions(t)
	host := OpenClawConfigPath(o.Home)
	original := map[string]any{"custom": true, "plugins": map[string]any{"allow": []any{"other"}, "entries": map[string]any{"other": map[string]any{"enabled": true}, "openclaw-otel-plugin": map[string]any{"enabled": true, "config": map[string]any{"endpoint": "https://legacy.invalid", "headers": map[string]any{"X-Token": "legacy-secret"}, "enabled": false, "captureContent": "none"}}}}}
	if err := writeJSONAtomic(host, original); err != nil {
		t.Fatal(err)
	}
	result, err := InstallOpenClaw(o)
	if err != nil {
		t.Fatal(err)
	}
	config, _, err := readJSONObjectIfExists(result.ConfigFile)
	if err != nil {
		t.Fatal(err)
	}
	if config["endpoint"] != o.Endpoint || config["enabled"] != false || config["captureContent"] != "none" {
		t.Fatalf("migration lost config: %#v", config)
	}
	if config["tracePath"] != "v1/write/otel-llm" {
		t.Fatal("expected GTrace profile")
	}
	before, _ := os.ReadFile(result.ConfigFile)
	o.NoConfig = true
	o.Endpoint = "https://ignored.invalid"
	o.XToken = "ignored"
	if _, err = InstallOpenClaw(o); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(result.ConfigFile)
	if !bytes.Equal(before, after) {
		t.Fatal("update changed runtime config")
	}
	h, _ := readJSONObject(host)
	plugins := h["plugins"].(map[string]any)
	entries := plugins["entries"].(map[string]any)
	if entries["openclaw-otel-plugin"].(map[string]any)["enabled"] != false {
		t.Fatal("legacy plugin still enabled")
	}
	if len(plugins["load"].(map[string]any)["paths"].([]any)) != 1 {
		t.Fatal("duplicate load path")
	}
	if h["custom"] != true || entries["other"].(map[string]any)["enabled"] != true {
		t.Fatal("unrelated config changed")
	}
	if _, err = RemoveAdapter("openclaw", o.Home, RemoveOptions{PurgeManaged: true}); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(filepath.Dir(result.ConfigFile)); !os.IsNotExist(err) {
		t.Fatal("managed files remain")
	}
	h, _ = readJSONObject(host)
	entries = h["plugins"].(map[string]any)["entries"].(map[string]any)
	if _, ok := entries["obs-agent-connector"]; ok {
		t.Fatal("bridge registration remains")
	}
	if entries["openclaw-otel-plugin"].(map[string]any)["config"] == nil {
		t.Fatal("legacy config deleted without purge")
	}
	if _, err = RemoveAdapter("openclaw", o.Home, RemoveOptions{PurgeConfig: true}); err != nil {
		t.Fatal(err)
	}
	h, _ = readJSONObject(host)
	entries = h["plugins"].(map[string]any)["entries"].(map[string]any)
	if _, ok := entries["openclaw-otel-plugin"].(map[string]any)["config"]; ok {
		t.Fatal("legacy config not purged")
	}
}
func TestOpenClawCustomRootAndMalformedConfig(t *testing.T) {
	o := openClawOptions(t)
	root := filepath.Join(o.Home, "custom-state")
	t.Setenv("OPENCLAW_STATE_DIR", root)
	result, err := InstallOpenClaw(o)
	if err != nil {
		t.Fatal(err)
	}
	if result.HooksFile != filepath.Join(root, "openclaw.json") {
		t.Fatal("custom root ignored")
	}
	bad := []byte(`{"plugins":{"load":{"paths":42}}}`)
	if err = os.WriteFile(result.HooksFile, bad, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = InstallOpenClaw(o); err == nil {
		t.Fatal("expected schema error")
	}
	after, _ := os.ReadFile(result.HooksFile)
	if !bytes.Equal(bad, after) {
		t.Fatal("invalid config overwritten")
	}
}
func TestOpenClawNoConfigFreshDoesNotCreateRuntimeConfig(t *testing.T) {
	o := openClawOptions(t)
	o.NoConfig = true
	result, err := InstallOpenClaw(o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(result.ConfigFile); !os.IsNotExist(err) {
		t.Fatal("no-config wrote telemetry config")
	}
}
