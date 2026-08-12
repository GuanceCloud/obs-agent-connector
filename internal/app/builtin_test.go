package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
)

func TestCodeBuddyBuiltinUpdateReconcilesLegacyHookAndPreservesConfigState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	executable := filepath.Join(home, ".local", "bin", "obs-agent-connector")
	originalExecutable := currentExecutable
	currentExecutable = func() (string, error) { return executable, nil }
	t.Cleanup(func() { currentExecutable = originalExecutable })

	settingsPath := filepath.Join(home, ".codebuddy", "settings.json")
	configPath := filepath.Join(home, ".codebuddy", "gtrace.json")
	statePath := filepath.Join(home, ".codebuddy", "gtrace", "uploads", "turn", "completed.json")
	for _, path := range []string{settingsPath, configPath, statePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	settings := `{"theme":"dark","hooks":{"Stop":[{"hooks":[{"type":"command","command":"/tmp/codebuddy-otel-plugin/bin/codebuddy-hook"}]},{"hooks":[{"type":"command","command":"echo keep"}]}]}}`
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

	plugin := agent.Get("codebuddy")
	if err := installBuiltinAdapter(plugin, installInput{}, true); err != nil {
		t.Fatal(err)
	}
	updatedSettings, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updatedSettings)
	if !strings.Contains(text, executable) || !strings.Contains(text, "echo keep") || strings.Contains(text, "/tmp/codebuddy-otel-plugin") {
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

func TestCodeBuddyBuiltinInstallDryRunDoesNotPrintToken(t *testing.T) {
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
		if err := install([]string{"codebuddy", "--dry-run", "--yes"}); err != nil {
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

func TestCodexBuiltinInstallReconcilesLegacyHookAndPreservesConfigState(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	executable := filepath.Join(home, ".local", "bin", "obs-agent-connector")
	originalExecutable := currentExecutable
	currentExecutable = func() (string, error) { return executable, nil }
	t.Cleanup(func() { currentExecutable = originalExecutable })

	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	configPath := filepath.Join(home, ".codex", "gtrace.json")
	for _, path := range []string{hooksPath, configPath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hooks := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"echo keep"}]},{"hooks":[{"type":"command","command":"/tmp/codex-otel-plugin/bin/codex-hook"}]}]}}`
	config := []byte("{\"enabled\":false,\"endpoint\":\"https://existing.example.com\",\"unknown\":{\"keep\":true}}\n")
	if err := os.WriteFile(hooksPath, []byte(hooks), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, config, 0o600); err != nil {
		t.Fatal(err)
	}

	plugin := agent.Get("codex")
	if err := installBuiltinAdapter(plugin, installInput{}, true); err != nil {
		t.Fatal(err)
	}

	updatedHooks, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updatedHooks)
	if !strings.Contains(text, executable) || !strings.Contains(text, "hook codex") || !strings.Contains(text, "echo keep") || strings.Contains(text, "codex-otel-plugin") {
		t.Fatalf("legacy Codex Hook was not safely reconciled: %s", text)
	}
	updatedConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(updatedConfig) != string(config) {
		t.Fatalf("--no-config changed runtime config:\nwant %s\n got %s", config, updatedConfig)
	}
}

func TestCodexInstallUsesBuiltin(t *testing.T) {
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
	if !strings.Contains(output, "codex (built into obs-agent-connector)") || strings.Contains(output, "codex-otel-plugin") {
		t.Fatalf("Codex install must use the built-in adapter: %s", output)
	}
}

func TestCodeBuddyInstallUsesBuiltin(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	configPath := filepath.Join(home, ".obs-agent-connector", "config.json")
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", configPath)
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configPath, []byte(`{"endpoint":"https://example.com","x_token":"synthetic-secret"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	output := captureStdout(t, func() {
		if err := install([]string{"codebuddy", "--dry-run", "--yes"}); err != nil {
			t.Fatal(err)
		}
	})
	if !strings.Contains(output, "codebuddy (built into obs-agent-connector)") || strings.Contains(output, "codebuddy-otel-plugin") {
		t.Fatalf("unexpected install plan: %s", output)
	}
}

func TestLifecycleCommandsRejectRemovedNewRuntimeFlag(t *testing.T) {
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
			if err == nil || !strings.Contains(err.Error(), "flag provided but not defined: -n") {
				t.Fatalf("expected removed -n flag error, got %v", err)
			}
		})
	}
}

func TestUsageDoesNotAdvertiseNewRuntimeMode(t *testing.T) {
	output := captureStdout(t, printUsage)
	if strings.Contains(output, "[-n]") || strings.Contains(output, "new-runtime") || strings.Contains(output, "codex -n") {
		t.Fatalf("usage must not advertise the removed runtime mode:\n%s", output)
	}
	for _, expected := range []string{"install codebuddy", "install codex", "config codex list", "remove codex"} {
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

func TestCodexBuiltinRemoveAlsoCleansLegacyPluginResidue(t *testing.T) {
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CODEX_BINARY", "")
	t.Setenv("CODEX_CLI_PATH", "")

	hooksPath := filepath.Join(home, ".codex", "hooks.json")
	configPath := filepath.Join(home, ".codex", "config.toml")
	gtracePath := filepath.Join(home, ".codex", "gtrace.json")
	legacySourcePath := filepath.Join(home, ".codex", "plugin-sources", "codex-otel-plugin", "plugins", "tracing")
	legacyCachePath := filepath.Join(home, ".codex", "plugins", "cache", "codex-otel-plugin")
	for _, path := range []string{hooksPath, configPath, gtracePath, legacySourcePath, legacyCachePath} {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(legacySourcePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(legacyCachePath, 0o755); err != nil {
		t.Fatal(err)
	}
	hooksBody, err := json.Marshal(map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": filepath.Join(home, ".local", "bin", "obs-agent-connector") + " hook codex"}}},
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "/tmp/codex-otel-plugin/bin/codex-hook"}}},
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo keep"}}},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	hooksBody = append(hooksBody, '\n')
	if err := os.WriteFile(hooksPath, hooksBody, 0o644); err != nil {
		t.Fatal(err)
	}
	hookKeyManaged := hooksPath + ":stop:0:0"
	hookKeyLegacy := hooksPath + ":stop:1:0"
	toml := strings.Join([]string{
		`[marketplaces.codex-otel-plugin]`,
		`source = "keep-removing"`,
		"",
		`[plugins."tracing@codex-otel-plugin"]`,
		`enabled = true`,
		"",
		`[hooks.state."` + hookKeyManaged + `"]`,
		`trusted_hash = "managed-hash"`,
		"",
		`[hooks.state."` + hookKeyLegacy + `"]`,
		`trusted_hash = "legacy-hash"`,
		"",
		`[unrelated]`,
		`enabled = true`,
		"",
	}, "\n")
	if err := os.WriteFile(configPath, []byte(toml), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(gtracePath, []byte(`{"enabled":true}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := remove([]string{"codex", "--yes"}); err != nil {
		t.Fatal(err)
	}

	updatedHooks, err := os.ReadFile(hooksPath)
	if err != nil {
		t.Fatal(err)
	}
	hookText := string(updatedHooks)
	if strings.Contains(hookText, "hook codex") || strings.Contains(hookText, "codex-otel-plugin") || !strings.Contains(hookText, "echo keep") {
		t.Fatalf("expected managed and legacy Codex hooks removed while preserving unrelated entries: %s", hookText)
	}
	updatedConfig, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	configText := string(updatedConfig)
	for _, removed := range []string{"marketplaces.codex-otel-plugin", `plugins."tracing@codex-otel-plugin"`, "managed-hash", "legacy-hash"} {
		if strings.Contains(configText, removed) {
			t.Fatalf("expected legacy Codex registration %q to be removed: %s", removed, configText)
		}
	}
	if !strings.Contains(configText, "[unrelated]") {
		t.Fatalf("expected unrelated config to be preserved: %s", configText)
	}
	if _, err := os.Stat(legacySourcePath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy source path removed, got %v", err)
	}
	if _, err := os.Stat(legacyCachePath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy cache path removed, got %v", err)
	}
	if _, err := os.Stat(gtracePath); err != nil {
		t.Fatalf("expected runtime config to be preserved without purge: %v", err)
	}
}
