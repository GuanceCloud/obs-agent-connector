package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	claudeconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	claudehook "github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/hook"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
	telemetryinstall "github.com/GuanceCloud/obs-agent-connector/internal/install"
)

func TestClaudeDesktopTerminalReplayAfterRemovingLegacyHook(t *testing.T) {
	for _, tc := range []struct {
		name, goos           string
		command              []string
		managed, customState bool
	}{
		{"managed with default state", "windows", []string{"remove", "claude", "--purge-config", "--yes"}, true, false},
		{"managed with custom state", "linux", []string{"remove", "claude", "--purge-config", "--yes"}, true, true},
		{"uninstall with managed state", "darwin", []string{"uninstall", "--yes"}, true, true},
		{"uninstall without a machine registration", "darwin", []string{"uninstall", "--yes"}, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, project := t.TempDir(), t.TempDir()
			setTestHome(t, home)
			t.Setenv("USERPROFILE", home)
			t.Setenv("PATH", t.TempDir())
			t.Setenv("CODEX_HOME", "")
			t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", filepath.Join(home, "connector.json"))
			originalExecutable, originalGOOS, originalState, originalRemove := currentExecutable, currentGOOS, managedDesktopState, removeManagedDesktop
			t.Cleanup(func() {
				currentExecutable, currentGOOS, managedDesktopState, removeManagedDesktop = originalExecutable, originalGOOS, originalState, originalRemove
			})
			executable := filepath.Join(home, "obs-agent-connector")
			writeClaudeDesktopFixture(t, executable, "connector")
			currentExecutable = func() (string, error) { return executable, nil }
			currentGOOS = tc.goos
			managedDesktopState = func() (telemetryinstall.ManagedDesktopState, error) {
				return telemetryinstall.ManagedDesktopState{Policy: tc.managed, Runtime: tc.managed}, nil
			}
			removeManagedDesktop = func(string) error {
				t.Fatal("legacy removal or generic uninstall reached the privileged Desktop remover")
				return nil
			}
			legacySettings := filepath.Join(home, ".claude", "settings.json")
			legacyConfig := filepath.Join(home, ".claude", "gtrace.json")
			managedConfig := agentfiles.ConfigPath(home, "claude")
			desktopConfig := agentfiles.ConfigPath(home, desktop.Name)
			writeClaudeDesktopFixture(t, legacySettings, `{"hooks":{"Stop":[{"hooks":[{"command":"obs-agent-connector hook claude"}]}]}}`)
			const desktopConfigBody = `{"enabled":true,"endpoint":"https://test.invalid","captureContent":"none"}`
			writeClaudeDesktopFixture(t, desktopConfig, desktopConfigBody)
			legacy := map[string]any{"headers": map[string]any{"X-Token": "old-legacy"}}
			managed := map[string]any{"enabled": true, "endpoint": "https://test.invalid", "captureContent": "none", "headers": map[string]any{"X-Token": "old-managed"}}
			stateDir := filepath.Join(home, ".claude", "state", "gtrace-agent")
			if tc.customState {
				legacy["state_dir"] = filepath.Join(home, ".claude", "old-state")
				stateDir = filepath.Join(agentfiles.Directory(home, "claude"), "custom-state")
				managed["state_dir"] = stateDir
			}
			for path, config := range map[string]map[string]any{legacyConfig: legacy, managedConfig: managed} {
				if err := telemetryinstall.WriteRuntimeConfig(path, config); err != nil {
					t.Fatal(err)
				}
			}
			transcript := filepath.Join(project, "turn.jsonl")
			writeClaudeDesktopFixture(t, transcript, `{"type":"user","uuid":"turn-1","timestamp":"2026-06-16T01:00:00Z","message":{"content":"Reply with OK."}}
{"type":"assistant","timestamp":"2026-06-16T01:00:01Z","message":{"id":"msg-1","model":"claude-test","stop_reason":"end_turn","content":[{"type":"text","text":"OK"}],"usage":{"input_tokens":10,"output_tokens":2}}}
`)
			requests := map[string]int{}
			receiver := &http.Client{Transport: claudeRemovalReceiver(func(r *http.Request) (*http.Response, error) {
				requests[r.URL.Path]++
				return &http.Response{StatusCode: http.StatusOK, Body: http.NoBody}, nil
			})}
			payload := map[string]any{"session_id": "test-session", "transcript_path": transcript, "hook_event_name": "Stop"}
			resolve := claudeconfig.ResolveOptions{Home: home, Cwd: project, Env: map[string]string{}}
			config := claudeconfig.Resolve(resolve)
			options := claudehook.RunOptions{Config: &config, Payload: payload, SkipWait: true, HTTPClient: receiver, Entrypoint: "claude-desktop-3p"}
			if err := claudehook.RunWithOptions(options); err != nil {
				t.Fatal(err)
			}
			if requests["/v1/traces"] != 1 || requests["/v1/metrics"] != 1 {
				t.Fatalf("initial Stop requests = %v, want one of each signal", requests)
			}
			output := captureStdout(t, func() {
				if err := Run(tc.command); err != nil {
					t.Fatal(err)
				}
			})
			if !tc.managed && tc.command[0] == "uninstall" {
				for _, path := range []string{stateDir, desktopConfig, managedConfig, executable} {
					if _, err := os.Stat(path); !os.IsNotExist(err) {
						t.Fatalf("uninstall left %s: %v", path, err)
					}
				}
				return
			}
			if !strings.Contains(output, "Shared Claude upload state") {
				t.Fatalf("removal plan should describe shared state preservation: %s", output)
			}
			if body, err := os.ReadFile(legacySettings); err != nil || strings.Contains(string(body), "obs-agent-connector") {
				t.Fatalf("legacy Hook still registered: %s, %v", body, err)
			}
			if body, err := os.ReadFile(desktopConfig); err != nil || string(body) != desktopConfigBody {
				t.Fatalf("Desktop capture and transport settings changed: %v", err)
			}
			configAfter, exists, err := telemetryinstall.ReadRuntimeConfig(managedConfig)
			if err != nil || exists != tc.customState || (exists && (len(configAfter) != 1 || configAfter["state_dir"] != stateDir)) {
				t.Fatalf("managed legacy settings were not cleaned: %v, %v, %v", configAfter, exists, err)
			}
			if tc.command[0] == "remove" {
				configAfter, exists, err := telemetryinstall.ReadRuntimeConfig(legacyConfig)
				if err != nil || exists != tc.customState || (exists && (len(configAfter) != 1 || configAfter["state_dir"] != legacy["state_dir"])) {
					t.Fatalf("legacy settings were not cleaned: %v, %v, %v", configAfter, exists, err)
				}
			}
			payload["hook_event_name"] = "SessionEnd"
			resolve.Desktop = true
			config = claudeconfig.Resolve(resolve)
			options.Desktop = true
			if config.StateDir != stateDir || !config.Enabled {
				t.Fatalf("Desktop replay ledger = %q, want %q with collection enabled", config.StateDir, stateDir)
			}
			if err := claudehook.RunWithOptions(options); err != nil {
				t.Fatal(err)
			}
			if requests["/v1/traces"] != 1 || requests["/v1/metrics"] != 1 {
				t.Fatalf("SessionEnd repeated the existing turn upload: %v", requests)
			}
		})
	}
}

type claudeRemovalReceiver func(*http.Request) (*http.Response, error)

func (f claudeRemovalReceiver) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func writeClaudeDesktopFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
