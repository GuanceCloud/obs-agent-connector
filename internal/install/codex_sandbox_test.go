package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestCodexSandboxDefaultsAndExplicitRouteSwitchPreserveConfig(t *testing.T) {
	current := map[string]any{"endpoint": "https://collector.example", "headers": map[string]any{"X-Token": "fixture"}, "tracePath": "traces", "metricsPath": "metrics"}
	defaults, err := MergeCodexRuntimeConfig(current, CodexOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if defaults["otel_logs_url"] != "" || defaults["logsPath"] != "" {
		t.Fatal("guessed a Logs endpoint")
	}
	if defaults["sandbox"].(map[string]any)["enabled"] != false {
		t.Fatal("enabled bridge without a producer")
	}
	logsURL, enabled, limit := "https://logs.example/otel/v1/logs", true, 64
	configured, err := MergeCodexRuntimeConfig(defaults, CodexOptions{Sandbox: CodexSandboxOptions{LogsURL: &logsURL, Enabled: &enabled, MaxPending: &limit}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if configured["otel_logs_url"] != logsURL || configured["sandbox"].(map[string]any)["max_pending"] != limit {
		t.Fatal("explicit Logs config missing")
	}
	if defaults["sandbox"].(map[string]any)["enabled"] != false {
		t.Fatal("merge mutated input config")
	}
	configured["sandbox"].(map[string]any)["producer_hint"] = "preserve"
	path := "otel/v1/logs"
	switched, err := MergeCodexRuntimeConfig(configured, CodexOptions{Sandbox: CodexSandboxOptions{LogsPath: &path}}, true)
	if err != nil {
		t.Fatal(err)
	}
	if switched["otel_logs_url"] != "" || switched["logsPath"] != path {
		t.Fatal("URL override masked relative route edit")
	}
	wire, _ := json.Marshal(switched)
	var normalized map[string]any
	if err := json.Unmarshal(wire, &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized["headers"].(map[string]any)["X-Token"] != "fixture" || switched["tracePath"] != "traces" || switched["metricsPath"] != "metrics" || switched["sandbox"].(map[string]any)["producer_hint"] != "preserve" {
		t.Fatal("unrelated configuration lost")
	}
	shared, err := MergeRuntimeConfig(current, CodexOptions{}, true)
	if err != nil {
		t.Fatal(err)
	}
	if shared["sandbox"] != nil || shared["otel_logs_url"] != nil {
		t.Fatal("Codex defaults leaked into shared config")
	}
}

func TestCodexSandboxFlagsAndInvalidConfigAreRejectedBeforeInstallation(t *testing.T) {
	for _, args := range [][]string{
		{"--sandbox-enabled=invalid"},
		{"--sandbox-max-pending=0"},
		{"--sandbox-max-pending=4097"},
		{"--logs-url=https://logs.example/v1/logs", "--logs-path=logs"},
	} {
		if _, err := ParseCodexInstallArgs(args); err == nil {
			t.Fatalf("accepted invalid flags %v", args)
		}
	}
	options, err := ParseCodexInstallArgs([]string{"--logs-url=https://logs.example/v1/logs", "--sandbox-enabled=true", "--sandbox-max-pending=32"})
	if err != nil || options.Sandbox.Enabled == nil || !*options.Sandbox.Enabled || *options.Sandbox.MaxPending != 32 {
		t.Fatalf("flags not parsed: %v", err)
	}
	root := t.TempDir()
	source := filepath.Join(root, "source")
	destination := filepath.Join(root, "bin", "connector")
	hooks := filepath.Join(root, "hooks.json")
	if err := os.WriteFile(source, []byte("fixture-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	enabled := true
	_, err = InstallCodex(CodexOptions{Home: root, SourceExecutable: source, DestinationExecutable: destination, HooksFile: hooks, ConfigFile: filepath.Join(root, "gtrace.json"), Endpoint: "https://trace.example", SkipTrust: true, Sandbox: CodexSandboxOptions{Enabled: &enabled}})
	if err == nil {
		t.Fatal("enabled bridge without a Logs route")
	}
	for _, path := range []string{destination, hooks} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("modified installation before validating Logs config")
		}
	}
}

func TestInstallCodexSeedsVisibleSandboxDefaultsAndNoConfigPreservesBytes(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "source")
	path := filepath.Join(root, "gtrace.json")
	if err := os.WriteFile(source, []byte("fixture-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	o := CodexOptions{Home: root, SourceExecutable: source, DestinationExecutable: filepath.Join(root, "bin", "connector"), HooksFile: filepath.Join(root, "hooks.json"), ConfigFile: path, Endpoint: "https://trace.example", SkipTrust: true}
	if _, err := InstallCodex(o); err != nil {
		t.Fatal(err)
	}
	var config map[string]any
	readTestJSON(t, path, &config)
	if config["sandbox"] == nil || config["otel_logs_url"] != "" || config["logsPath"] != "" {
		t.Fatal("installer did not write visible Logs fields")
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	logsURL, enabled := "https://logs.example/v1/logs", true
	o.NoConfig = true
	o.Sandbox = CodexSandboxOptions{LogsURL: &logsURL, Enabled: &enabled}
	if _, err := InstallCodex(o); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("--no-config rewrote runtime configuration")
	}
}
