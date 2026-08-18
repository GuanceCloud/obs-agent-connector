package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrefersLocalGtraceConfig(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	cwd := filepath.Join(base, "workspace")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	globalConfig := `{
  "enabled": false,
  "endpoint": "http://global.example.com",
  "tracePath": "global/traces",
  "metricsPath": "global/metrics",
  "timeout_ms": 12345
}`
	localConfig := `{
  "enabled": true,
  "endpoint": "http://local.example.com/",
  "tracePath": "/local/traces/",
  "metricsPath": "/local/metrics/",
  "resourceAttributes": {
    "app_id": "codex-local"
  }
}`
	if err := os.WriteFile(filepath.Join(home, ".codex", "gtrace.json"), []byte(globalConfig), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".codex", "gtrace.json"), []byte(localConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Resolve(ResolveOptions{Home: home, Cwd: cwd})
	if err != nil {
		t.Fatal(err)
	}

	if !cfg.Enabled {
		t.Fatalf("expected local config enabled=true")
	}
	if cfg.Endpoint != "http://local.example.com" {
		t.Fatalf("unexpected endpoint: %s", cfg.Endpoint)
	}
	if cfg.TracePath != "local/traces" || cfg.MetricsPath != "local/metrics" {
		t.Fatalf("unexpected paths: trace=%s metrics=%s", cfg.TracePath, cfg.MetricsPath)
	}
	if cfg.TimeoutMs != 12345 {
		t.Fatalf("expected timeout 12345, got %d", cfg.TimeoutMs)
	}
	if cfg.ResourceAttributes["app_id"] != "codex-local" {
		t.Fatalf("expected app_id resource attribute, got %#v", cfg.ResourceAttributes)
	}
	if cfg.CaptureContent != "preview" {
		t.Fatalf("unexpected capture content: %s", cfg.CaptureContent)
	}
}

func TestResolveCaptureContentNone(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(home, ".codex", "gtrace.json"),
		[]byte(`{"captureContent":"none"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	cfg, err := Resolve(ResolveOptions{Home: home, Cwd: base})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.CaptureContent != "none" {
		t.Fatalf("capture content = %q", cfg.CaptureContent)
	}
}

func TestResolveManagedConfigAndHookLogPath(t *testing.T) {
	home := t.TempDir()
	legacyDir := filepath.Join(home, ".codex")
	managedDir := filepath.Join(home, ".obs-agent-connector", "codex")
	if err := os.MkdirAll(legacyDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(managedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacyDir, "gtrace.json"), []byte(`{"endpoint":"https://legacy.example"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(managedDir, "gtrace.json"), []byte(`{"enabled":true,"endpoint":"https://managed.example","hook_log_file":"/tmp/legacy.log"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Resolve(ResolveOptions{Home: home, Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Enabled || cfg.Endpoint != "https://managed.example" {
		t.Fatalf("managed config did not win: %#v", cfg)
	}
	if want := filepath.Join(managedDir, "gtrace-hooks.json"); cfg.HookLogFile != want {
		t.Fatalf("HookLogFile = %q, want %q", cfg.HookLogFile, want)
	}
}

func TestResolveParsesManagedHeadersAndTags(t *testing.T) {
	base := t.TempDir()
	home := filepath.Join(base, "home")
	if err := os.MkdirAll(filepath.Join(home, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}

	configBody := `{
  "enabled": true,
  "endpoint": "https://llm-openway.guance.com",
  "headers": {
    "X-Token": "agent_new",
    "To-Headless": "true"
  },
  "tags": ["agent_id=newid", "agent_name=newname"]
}`
	if err := os.WriteFile(filepath.Join(home, ".codex", "gtrace.json"), []byte(configBody), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Resolve(ResolveOptions{Home: home, Cwd: base})
	if err != nil {
		t.Fatal(err)
	}

	if cfg.Headers["X-Token"] != "agent_new" {
		t.Fatalf("unexpected X-Token: %#v", cfg.Headers)
	}
	if cfg.ResourceAttributes["agent_id"] != "newid" || cfg.ResourceAttributes["agent_name"] != "newname" {
		t.Fatalf("unexpected resource attributes: %#v", cfg.ResourceAttributes)
	}
}
