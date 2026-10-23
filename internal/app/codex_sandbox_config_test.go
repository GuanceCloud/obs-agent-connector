package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	installpkg "github.com/GuanceCloud/obs-agent-connector/internal/install"
)

func TestCodexLogsConfigListEditAndValidation(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	path := filepath.Join(home, ".obs-agent-connector", "codex", "gtrace.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	before := []byte(`{"enabled":true,"endpoint":"https://trace.example","tracePath":"traces","metricsPath":"metrics","headers":{"X-Token":"fixture-secret"}}`)
	if err := os.WriteFile(path, before, 0o600); err != nil {
		t.Fatal(err)
	}
	output := captureStdout(t, func() {
		if err := configCommand([]string{"codex", "list"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, text := range []string{"logs-path", "sandbox-enabled", "false (default)", "agent_sandbox (fixed)", "automatic Codex sandbox collection is unavailable"} {
		if !strings.Contains(output, text) {
			t.Fatalf("missing %s in config list", text)
		}
	}
	if strings.Contains(output, "fixture-secret") {
		t.Fatal("displayed an auth value")
	}
	if err := configCommand([]string{"codex", "edit", "--sandbox-enabled=true"}); err == nil {
		t.Fatal("enabled bridge without Logs route")
	}
	unchanged, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(unchanged) != string(before) {
		t.Fatal("invalid edit changed runtime config")
	}
	output = captureStdout(t, func() {
		if err := configCommand([]string{"codex", "edit", "--logs-path=v1/write/otel-logs", "--sandbox-enabled=true", "--sandbox-max-pending=128"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "independent runtime producer") {
		t.Fatal("configuration implied automatic collection")
	}
	updated := readJSONFile(t, path)
	if updated["logsPath"] != "v1/write/otel-logs" || updated["sandbox"].(map[string]any)["enabled"] != true {
		t.Fatal("Logs settings not persisted")
	}
	if updated["headers"].(map[string]any)["X-Token"] != "fixture-secret" || updated["tracePath"] != "traces" || updated["metricsPath"] != "metrics" {
		t.Fatal("existing settings changed")
	}
	if err := configCommand([]string{"codex", "edit", "--logs-path=otel/v1/logs"}); err != nil {
		t.Fatal(err)
	}
	updated = readJSONFile(t, path)
	if updated["otel_logs_url"] != nil || updated["logsPath"] != "otel/v1/logs" {
		t.Fatal("retired URL setting remains in config")
	}
	if err := install([]string{"claude", "--logs-path=v1/logs", "--dry-run"}); err == nil {
		t.Fatal("silently accepted Codex-only options for another Agent")
	}
}

func TestCodexSandboxConfigDoesNotDisplayRetiredURL(t *testing.T) {
	rows := codexSandboxConfigRows(map[string]any{"otel_logs_url": "https://user:password@logs.example/v1/logs?access_token=fixture-secret"})
	for _, row := range rows {
		for _, value := range row {
			if strings.Contains(value, "password") || strings.Contains(value, "fixture-secret") || strings.Contains(value, "logs-url") {
				t.Fatal("displayed URL authentication")
			}
		}
	}
}

func TestCodexBuiltinInstallForwardsSandboxOptions(t *testing.T) {
	old := installCodexAdapter
	var captured installpkg.CodexSandboxOptions
	installCodexAdapter = func(o installpkg.CodexOptions) (installpkg.CodexResult, error) {
		captured = o.Sandbox
		return installpkg.CodexResult{TrustSkipped: true}, nil
	}
	t.Cleanup(func() { installCodexAdapter = old })
	logsPath, enabled := "v1/logs", true
	if err := installBuiltinAdapter(agent.Get("codex"), installInput{CodexSandbox: installpkg.CodexSandboxOptions{LogsPath: &logsPath, Enabled: &enabled}}, false); err != nil {
		t.Fatal(err)
	}
	if captured.LogsPath == nil || *captured.LogsPath != logsPath || captured.Enabled == nil || !*captured.Enabled {
		t.Fatal("installer lost explicit sandbox options")
	}
}
