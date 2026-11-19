package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestResolvePrecedenceAndMapMerge(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "work")
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cwd, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeConfig(t, filepath.Join(home, ".claude", "gtrace.json"), map[string]any{
		"enabled":            true,
		"endpoint":           "http://global.example",
		"headers":            map[string]any{"X-Global": "1"},
		"resourceAttributes": map[string]any{"from": "global"},
	})
	writeConfig(t, filepath.Join(cwd, ".claude", "gtrace.json"), map[string]any{
		"endpoint":           "http://local.example",
		"headers":            map[string]any{"X-Local": "1"},
		"resourceAttributes": map[string]any{"from": "local", "local": "yes"},
	})
	env := map[string]string{
		"CLAUDE_PLUGIN_OPTION_OTEL_EXPORTER_OTLP_ENDPOINT":     "http://plugin.example",
		"CLAUDE_PLUGIN_OPTION_OTEL_EXPORTER_OTLP_HEADERS":      "X-Plugin=1",
		"CLAUDE_PLUGIN_OPTION_CLAUDE_OTEL_RESOURCE_ATTRIBUTES": `{"from":"plugin"}`,
	}

	cfg := Resolve(ResolveOptions{Env: env, Home: home, Cwd: cwd})
	if !cfg.Enabled {
		t.Fatal("expected file config to enable exporter")
	}
	if cfg.Transport.Endpoint != "http://local.example" {
		t.Fatalf("endpoint = %q", cfg.Transport.Endpoint)
	}
	for key, want := range map[string]string{"X-Global": "1", "X-Local": "1", "X-Plugin": "1"} {
		if got := cfg.Transport.Headers[key]; got != want {
			t.Fatalf("header %s = %q, want %q", key, got, want)
		}
	}
	if cfg.ResourceAttributes["from"] != "local" || cfg.ResourceAttributes["local"] != "yes" {
		t.Fatalf("unexpected resource attributes: %#v", cfg.ResourceAttributes)
	}

	env["OTEL_EXPORTER_OTLP_ENDPOINT"] = "http://env.example"
	cfg = Resolve(ResolveOptions{Env: env, Home: home, Cwd: cwd})
	if cfg.Transport.Endpoint != "http://env.example" {
		t.Fatalf("shell environment did not win: %q", cfg.Transport.Endpoint)
	}
}

func TestResolveInfersGTraceMetricsPath(t *testing.T) {
	cfg := Resolve(ResolveOptions{
		Env: map[string]string{
			"CLAUDE_OTEL_ENABLED":         "true",
			"OTEL_EXPORTER_OTLP_ENDPOINT": "https://llm-openway.guance.com",
			"CLAUDE_OTEL_TRACE_PATH":      "v1/write/otel-llm",
		},
		Home: t.TempDir(),
		Cwd:  t.TempDir(),
	})
	if cfg.Transport.TracePath != "v1/write/otel-llm" {
		t.Fatalf("trace path = %q", cfg.Transport.TracePath)
	}
	if cfg.Transport.MetricsPath != "v1/write/otel-metrics" {
		t.Fatalf("metrics path = %q", cfg.Transport.MetricsPath)
	}
}

func TestResolveDisabledExplicitly(t *testing.T) {
	cfg := Resolve(ResolveOptions{
		Env: map[string]string{
			"CLAUDE_OTEL_ENABLED":         "false",
			"OTEL_EXPORTER_OTLP_ENDPOINT": "https://collector.example",
		},
		Home: t.TempDir(),
		Cwd:  t.TempDir(),
	})
	if cfg.Enabled {
		t.Fatal("explicit false must disable exporter")
	}
}

func TestResolveDefaultHookLogFileName(t *testing.T) {
	root := t.TempDir()
	cfg := Resolve(ResolveOptions{
		Env:  map[string]string{},
		Home: root,
		Cwd:  root,
	})
	expected := filepath.Join(root, ".obs-agent-connector", "claude", "gtrace-hooks.json")
	if cfg.HookLogFile != expected {
		t.Fatalf("hook log file = %q, want %q", cfg.HookLogFile, expected)
	}
}

func TestResolveManagedConfigOverridesLegacyProjectConfig(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	writeConfig(t, filepath.Join(cwd, ".claude", "gtrace.json"), map[string]any{
		"enabled":  false,
		"endpoint": "https://legacy.example",
	})
	writeConfig(t, filepath.Join(home, ".obs-agent-connector", "claude", "gtrace.json"), map[string]any{
		"enabled":       true,
		"endpoint":      "https://managed.example",
		"hook_log_file": "/tmp/legacy.log",
	})
	cfg := Resolve(ResolveOptions{Env: map[string]string{}, Home: home, Cwd: cwd})
	if !cfg.Enabled || cfg.Transport.Endpoint != "https://managed.example" {
		t.Fatalf("managed config did not win: %#v", cfg)
	}
	if want := filepath.Join(home, ".obs-agent-connector", "claude", "gtrace-hooks.json"); cfg.HookLogFile != want {
		t.Fatalf("HookLogFile = %q, want %q", cfg.HookLogFile, want)
	}
}

func writeConfig(t *testing.T, path string, value any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestDesktopConfigUsesOwnDestinationAndLegacyState(t *testing.T) {
	home := t.TempDir()
	cwd := t.TempDir()
	osOverride := map[string]string{
		"CLAUDE_OTEL_ENABLED":         "true",
		"OTEL_EXPORTER_OTLP_ENDPOINT": "https://env.invalid",
		"OTEL_EXPORTER_OTLP_HEADERS":  "Authorization=legacy-environment",
	}
	stateDir := filepath.Join(home, "custom-claude-state")
	writeConfig(t, filepath.Join(home, ".claude", "gtrace.json"), map[string]any{
		"state_dir": filepath.Join(home, "legacy-state"),
	})
	writeConfig(t, filepath.Join(cwd, ".claude", "gtrace.json"), map[string]any{
		"state_dir": filepath.Join(home, "project-state"),
	})
	writeConfig(t, filepath.Join(home, ".obs-agent-connector", "claude", "gtrace.json"), map[string]any{
		"enabled":   true,
		"endpoint":  "https://legacy.invalid",
		"state_dir": stateDir,
		"headers":   map[string]any{"X-Token": "legacy-token"},
	})
	desktopOptions := ResolveOptions{Desktop: true, Env: osOverride, Home: home, Cwd: cwd}
	if cfg := Resolve(desktopOptions); cfg.Enabled || cfg.StateDir != stateDir {
		t.Fatalf("Desktop without config should share state and remain inactive: enabled=%v state=%q", cfg.Enabled, cfg.StateDir)
	}

	desktopPath := filepath.Join(home, ".obs-agent-connector", "claude-desktop", "gtrace.json")
	config := map[string]any{
		"enabled":        true,
		"captureContent": "none",
		"endpoint":       "https://desktop.invalid",
		"headers":        map[string]any{"X-Token": "desktop-token"},
	}
	writeConfig(t, desktopPath, config)
	desktop := Resolve(desktopOptions)
	legacy := Resolve(ResolveOptions{Env: osOverride, Home: home, Cwd: cwd})
	if !desktop.Enabled || desktop.Transport.Endpoint != "https://desktop.invalid" ||
		desktop.Transport.Headers["X-Token"] != "desktop-token" || desktop.Transport.Headers["Authorization"] != "" ||
		desktop.CaptureContent != "none" || desktop.StateDir != legacy.StateDir ||
		desktop.HookLogFile != filepath.Join(home, ".obs-agent-connector", "claude-desktop", "gtrace-hooks.json") {
		t.Fatalf("unexpected Desktop configuration: %#v", desktop)
	}
	config["enabled"] = false
	writeConfig(t, desktopPath, config)
	if Resolve(desktopOptions).Enabled {
		t.Fatal("legacy or environment settings re-enabled the Desktop Hook")
	}
}

func TestDesktopConfigSharesDefaultStateWithoutLegacyInstall(t *testing.T) {
	home := t.TempDir()
	options := ResolveOptions{Home: home, Cwd: t.TempDir(), Env: map[string]string{}}
	legacy := Resolve(options)
	options.Desktop = true
	desktop := Resolve(options)
	if desktop.StateDir != legacy.StateDir || desktop.StateDir != filepath.Join(home, ".claude", "state", "gtrace-agent") {
		t.Fatalf("unexpected Desktop state path %q", desktop.StateDir)
	}
}
