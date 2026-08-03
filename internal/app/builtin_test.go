package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
)

func TestBuiltinUpdateReconcilesLegacyHookAndPreservesConfigState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	executable := filepath.Join(home, ".local", "bin", "obs-agent-connector")
	originalExecutable := currentExecutable
	currentExecutable = func() (string, error) { return executable, nil }
	t.Cleanup(func() { currentExecutable = originalExecutable })

	settingsPath := filepath.Join(home, ".claude", "settings.json")
	configPath := filepath.Join(home, ".claude", "gtrace.json")
	statePath := filepath.Join(home, ".claude", "state", "gtrace-agent", "uploads", "turn", "completed.json")
	for _, path := range []string{settingsPath, configPath, statePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	settings := `{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"/tmp/agent-telemetry","args":["hook","claude"]}]},{"hooks":[{"type":"command","command":"echo keep"}]}]}}`
	config := []byte(`{"enabled":false,"endpoint":"https://existing.example.com","captureContent":"none","unknown":{"keep":true}}` + "\n")
	if err := os.WriteFile(settingsPath, []byte(settings), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"completed":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	plugin, ok := agent.Get("claude").WithBuiltin()
	if !ok {
		t.Fatal("claude must support the built-in runtime")
	}
	if err := installBuiltinAdapter(plugin, installInput{}, true); err != nil {
		t.Fatal(err)
	}
	updatedSettings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updatedSettings)
	if !strings.Contains(text, executable) || !strings.Contains(text, "echo keep") || strings.Contains(text, "/tmp/agent-telemetry") {
		t.Fatalf("legacy Hook was not safely reconciled: %s", text)
	}
	updatedConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(updatedConfig) != string(config) {
		t.Fatalf("--no-config changed runtime config:\nwant %s\n got %s", config, updatedConfig)
	}
	if _, err := os.Stat(statePath); err != nil {
		t.Fatalf("upload state must be preserved: %v", err)
	}
}

func TestBuiltinInstallDryRunDoesNotPrintToken(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".obs-agent-connector", "config.json")
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", configPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	const secret = "agent_secret_should_not_be_printed"
	if err := os.WriteFile(configPath, []byte(`{"endpoint":"https://example.com","x_token":"`+secret+`"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(t, func() {
		if err := install([]string{"codex", "-n", "--dry-run", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	if strings.Contains(output, secret) {
		t.Fatalf("install output exposed X-Token: %s", output)
	}
	if !strings.Contains(output, "built into obs-agent-connector") || !strings.Contains(output, "<configured>") {
		t.Fatalf("unexpected built-in install plan: %s", output)
	}
}

func TestInstallDefaultsToLegacyPlugin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".obs-agent-connector", "config.json")
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", configPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"endpoint":"https://example.com","x_token":"secret","plugin_base_url":"https://static.example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	output := captureStdout(t, func() {
		if err := install([]string{"codex", "--dry-run", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "codex-otel-plugin") || strings.Contains(output, "built into obs-agent-connector") {
		t.Fatalf("default install must use the legacy plugin: %s", output)
	}
}

func TestTargetedCommandsRejectNewRuntimeForUnsupportedAgent(t *testing.T) {
	tests := map[string]func([]string) error{
		"install": install,
		"status":  status,
		"update":  update,
		"remove":  remove,
		"enable":  enable,
		"disable": disable,
	}
	for name, command := range tests {
		t.Run(name, func(t *testing.T) {
			err := command([]string{"opencode", "-n"})
			if err == nil {
				t.Fatal("expected unsupported new runtime error")
			}
			want := "opencode does not support -n/--new-runtime; supported agents: claude, codex"
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("expected %q, got %v", want, err)
			}
		})
	}
}

func TestUsageIncludesBuiltinRemovalAndConnectorUninstallBehavior(t *testing.T) {
	output := captureStdout(t, printUsage)
	for _, expected := range []string{
		"remove <agent> [-n]   Remove an Agent plugin; -n removes its built-in Hook",
		"uninstall             Uninstall obs-agent-connector and its managed built-in Hooks",
		"obs-agent-connector remove codex -n",
	} {
		if !strings.Contains(output, expected) {
			t.Fatalf("expected usage to contain %q, got:\n%s", expected, output)
		}
	}
}

func TestRedactInstallerArgsHidesToken(t *testing.T) {
	args := []string{"install.sh", "--endpoint", "https://example.com", "--x-token", "secret", "--tag", "env=test"}
	redacted := strings.Join(redactInstallerArgs(args), " ")
	if strings.Contains(redacted, "secret") || !strings.Contains(redacted, "<redacted>") {
		t.Fatalf("token was not redacted: %s", redacted)
	}
}

func TestInstallRejectsInvalidHeaderBeforeRegisteringHook(t *testing.T) {
	err := install([]string{"codex", "--header", "invalid"})
	if err == nil || !strings.Contains(err.Error(), "--header must use non-empty KEY=VALUE syntax") {
		t.Fatalf("expected assignment validation error, got %v", err)
	}
}
