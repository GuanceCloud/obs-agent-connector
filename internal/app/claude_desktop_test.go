package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
	telemetryinstall "github.com/GuanceCloud/obs-agent-connector/internal/install"
)

func TestClaudeDesktopMachineLifecycle(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			checkClaudeDesktopMachineLifecycle(t, useDesktopMachinePlatform(t, goos))
		})
	}
}

func TestClaudeDesktopReportsInstalledRuntimeVersion(t *testing.T) {
	paths := useDesktopMachinePlatform(t, "linux")
	home := t.TempDir()
	setTestHome(t, home)
	originalState, originalHome := managedDesktopState, managedDesktopHome
	originalProbe, originalVersion := managedDesktopRuntimeVersion, version
	t.Cleanup(func() {
		managedDesktopState, managedDesktopHome = originalState, originalHome
		managedDesktopRuntimeVersion, version = originalProbe, originalVersion
	})
	version = "v9.9.9"
	managedDesktopHome = func() (string, error) { return home, nil }
	state := telemetryinstall.ManagedDesktopState{Policy: true, Runtime: true}
	managedDesktopState = func() (telemetryinstall.ManagedDesktopState, error) { return state, nil }
	var runtimeVersion string
	probes := 0
	managedDesktopRuntimeVersion = func(path string) string {
		if path != paths.RuntimePath {
			t.Fatalf("version probe used %q, want %q", path, paths.RuntimePath)
		}
		probes++
		return runtimeVersion
	}
	run := func(args ...string) string {
		t.Helper()
		return captureStdout(t, func() {
			if err := Run(args); err != nil {
				t.Fatal(err)
			}
		})
	}
	for _, tc := range []struct{ reported, displayed string }{
		{"v0.1.32-claude-desktop-rc2", "v0.1.32-claude-desktop-rc2"},
		{"", "-"},
	} {
		runtimeVersion = tc.reported
		list := run("list")
		line := ""
		for _, row := range strings.Split(list, "\n") {
			if strings.HasPrefix(row, desktop.Name+" ") {
				line = row
				break
			}
		}
		if fields := strings.Fields(line); len(fields) < 2 || fields[1] != tc.displayed {
			t.Fatalf("list did not report runtime version %q:\n%s", tc.displayed, list)
		}
		if output := run("status", desktop.Name); !strings.Contains(output, "System runtime version    : "+tc.displayed+"\n") {
			t.Fatalf("status did not report runtime version %q:\n%s", tc.displayed, output)
		}
	}
	state.Runtime = false
	run("list")
	run("status", desktop.Name)
	if probes != 4 {
		t.Fatalf("expected one probe per command with a verified runtime, got %d", probes)
	}
}

func TestReadManagedDesktopRuntimeVersionUnavailable(t *testing.T) {
	if got := readManagedDesktopRuntimeVersion(filepath.Join(t.TempDir(), "missing-runtime")); got != "" {
		t.Fatalf("missing runtime returned version %q", got)
	}
}

func checkClaudeDesktopMachineLifecycle(t *testing.T, paths desktop.MachinePaths) {
	t.Helper()
	home := t.TempDir()
	setTestHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", filepath.Join(home, "connector.json"))
	originalStatus := managedDesktopState
	originalInstall, originalRemove := installManagedDesktop, removeManagedDesktop
	originalHome := managedDesktopHome
	t.Cleanup(func() {
		managedDesktopState = originalStatus
		installManagedDesktop, removeManagedDesktop = originalInstall, originalRemove
		managedDesktopHome = originalHome
	})
	managedDesktopHome = func() (string, error) { return home, nil }
	var state telemetryinstall.ManagedDesktopState
	managedDesktopState = func() (telemetryinstall.ManagedDesktopState, error) { return state, nil }
	var applied []telemetryinstall.CodexOptions
	installManagedDesktop = func(options telemetryinstall.CodexOptions) error {
		applied = append(applied, options)
		state = telemetryinstall.ManagedDesktopState{Policy: true, Runtime: true}
		return nil
	}
	removals := 0
	removeManagedDesktop = func(string) error {
		removals++
		state = telemetryinstall.ManagedDesktopState{}
		return nil
	}
	run := func(args ...string) string {
		t.Helper()
		return captureStdout(t, func() {
			if err := Run(args); err != nil {
				t.Fatal(err)
			}
		})
	}
	installArgs := []string{"install", desktop.Name, "--endpoint", "https://test.invalid", "--x-token", "test-token", "--capture-content", "none"}
	if output := run(append(installArgs, "--dry-run")...); !strings.Contains(output, paths.PolicyPath) || !strings.Contains(output, "administrator authorization") || strings.Contains(output, "test-token") {
		t.Fatalf("unexpected managed install preview: %s", output)
	}
	for _, command := range []string{"update", "remove"} {
		if output := run(command, desktop.Name, "--dry-run"); !strings.Contains(output, paths.RuntimePath) || !strings.Contains(output, "administrator authorization") {
			t.Fatalf("%s omitted machine preview: %s", command, output)
		}
	}
	if output := run("remove", desktop.Name, "--yes"); !strings.Contains(output, "No Claude Desktop machine files to remove") {
		t.Fatalf("absent machine removal did not report the state: %s", output)
	}
	if err := Run([]string{"update", desktop.Name, "--yes"}); err == nil || !strings.Contains(err.Error(), "install claude-desktop") {
		t.Fatalf("absent machine update omitted the install command: %v", err)
	}
	if len(applied) != 0 || removals != 0 {
		t.Fatal("previewing or operating on absent machine files reached the privileged operation")
	}
	for _, command := range []string{"install", "status", "update", "remove"} {
		for _, option := range []string{"--managed", "--personal"} {
			if err := Run([]string{command, desktop.Name, option}); err == nil || !strings.Contains(err.Error(), "flag provided but not defined") {
				t.Fatalf("%s accepted a removed flag: %v", command, err)
			}
		}
	}
	if err := Run([]string{"remove", desktop.Name, "--purge-config"}); err == nil || !strings.Contains(err.Error(), "unavailable for claude-desktop machine removal") {
		t.Fatalf("machine removal accepted an option for user configuration: %v", err)
	}
	run(append(installArgs, "--yes")...)
	if len(applied) != 1 || applied[0].NoConfig || applied[0].CaptureContent != "none" || applied[0].XToken != "test-token" {
		t.Fatal("managed install lost the user-supplied configuration")
	}
	if output := run("status", desktop.Name); !strings.Contains(output, "System Hook policy present: yes") || !strings.Contains(output, "Enabled for current user") || !strings.Contains(output, paths.PolicyPath) || !strings.Contains(output, paths.RuntimePath) {
		t.Fatalf("unexpected managed status: %s", output)
	}

	// A separate account can create its user configuration without elevation.
	configPath := agentfiles.ConfigPath(home, desktop.Name)
	run("config", desktop.Name, "edit", "--enabled=true", "--endpoint=https://test.invalid", "--x-token=test-token")
	redirectedHome := t.TempDir()
	redirectedConfig := agentfiles.ConfigPath(redirectedHome, desktop.Name)
	decoy := `{"enabled":true,"endpoint":"https://decoy.invalid"}`
	if err := telemetryinstall.WriteClaudeDesktopUserConfig(redirectedConfig, map[string]any{"enabled": true, "endpoint": "https://decoy.invalid"}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", redirectedHome)
	t.Setenv("USERPROFILE", redirectedHome)
	if output := run("status", desktop.Name); !strings.Contains(output, "Enabled for current user  : true") {
		t.Fatalf("managed status did not use the OS account's config: %s", output)
	}
	if output := run("config", desktop.Name, "list"); !strings.Contains(output, "https://test.invalid") || strings.Contains(output, "https://decoy.invalid") {
		t.Fatalf("managed config list used the redirected home: %s", output)
	}
	run("config", desktop.Name, "edit", "--capture-content=none")
	run("disable", desktop.Name)
	if actual := readJSONFile(t, configPath); actual["enabled"] != false || actual["captureContent"] != "none" {
		t.Fatal("managed edit or disable did not change the OS account's config")
	}
	if redirected := readJSONFile(t, redirectedConfig); redirected["enabled"] != true || redirected["captureContent"] != nil {
		t.Fatalf("managed operations wrote the redirected config: %s", decoy)
	}
	run("enable", desktop.Name)
	if actual := readJSONFile(t, configPath); actual["enabled"] != true {
		t.Fatal("managed enable did not change the OS account's config")
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	before, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	state.Blocked = errors.New("machine policy shadowed")
	if err := Run([]string{"update", desktop.Name, "--yes"}); !errors.Is(err, state.Blocked) || len(applied) != 1 {
		t.Fatalf("update ignored a known policy conflict: %v", err)
	}
	state.Blocked = nil
	run("update", desktop.Name, "--yes")
	if len(applied) != 2 || !applied[1].NoConfig {
		t.Fatal("managed update did not preserve user configuration")
	}
	run("remove", desktop.Name, "--yes")
	if output := run("status", desktop.Name); !strings.Contains(output, "System Hook policy present: no") || !strings.Contains(output, "Enabled for current user  : true") {
		t.Fatalf("removal status did not distinguish machine files from the user config: %s", output)
	}
	after, err := os.ReadFile(configPath)
	if err != nil || string(after) != string(before) || removals != 1 {
		t.Fatalf("managed operations disturbed the shared user config: %v", err)
	}
}

func TestClaudeDesktopRetainedConfigUsesAccountHomeDuringMachineRecovery(t *testing.T) {
	for _, tc := range []struct {
		name, command string
		state         telemetryinstall.ManagedDesktopState
	}{
		{"incomplete", "update claude-desktop", telemetryinstall.ManagedDesktopState{Runtime: true}},
		{"absent", "install claude-desktop", telemetryinstall.ManagedDesktopState{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accountHome, redirectedHome := t.TempDir(), t.TempDir()
			setTestHome(t, redirectedHome)
			originalOS, originalState, originalHome := currentGOOS, managedDesktopState, managedDesktopHome
			t.Cleanup(func() {
				currentGOOS, managedDesktopState, managedDesktopHome = originalOS, originalState, originalHome
			})
			currentGOOS = "darwin"
			managedDesktopHome = func() (string, error) { return accountHome, nil }
			managedDesktopState = func() (telemetryinstall.ManagedDesktopState, error) { return tc.state, nil }
			config := agentfiles.ConfigPath(accountHome, desktop.Name)
			decoy := agentfiles.ConfigPath(redirectedHome, desktop.Name)
			const decoyBody = `{"enabled":true,"endpoint":"https://decoy.invalid"}`
			writeClaudeDesktopFixture(t, decoy, decoyBody)
			if err := Run([]string{"config", desktop.Name, "edit", "--enabled=true"}); err == nil || !strings.Contains(err.Error(), tc.command) {
				t.Fatalf("bootstrap without machine files should report %s: %v", tc.command, err)
			}
			if _, err := os.Stat(config); !os.IsNotExist(err) {
				t.Fatalf("bootstrap created a user config during machine recovery: %v", err)
			}
			writeClaudeDesktopFixture(t, config, `{"enabled":true,"endpoint":"https://account.invalid"}`)
			output := captureStdout(t, func() {
				if err := Run([]string{"config", desktop.Name, "list"}); err != nil {
					t.Fatal(err)
				}
			})
			if !strings.Contains(output, "https://account.invalid") || strings.Contains(output, "https://decoy.invalid") {
				t.Fatalf("list did not use the retained OS account config: %s", output)
			}
			for _, args := range [][]string{{"config", desktop.Name, "edit", "--capture-content=none"}, {"disable", desktop.Name}} {
				if err := Run(args); err != nil {
					t.Fatal(err)
				}
			}
			if actual := readJSONFile(t, config); actual["enabled"] != false || actual["captureContent"] != "none" {
				t.Fatal("edit and disable did not update the retained OS account config")
			}
			if body, err := os.ReadFile(decoy); err != nil || string(body) != decoyBody {
				t.Fatalf("the redirected HOME config changed: %v", err)
			}
		})
	}
}

func TestUnsupportedClaudeDesktopPlatform(t *testing.T) {
	originalOS := currentGOOS
	t.Cleanup(func() { currentGOOS = originalOS })
	currentGOOS = "freebsd"
	for _, command := range []string{"install", "status", "update", "remove", "enable", "disable"} {
		if err := Run([]string{command, desktop.Name}); err == nil || !strings.Contains(err.Error(), "is not supported on freebsd") {
			t.Fatalf("%s did not reject an unsupported platform: %v", command, err)
		}
	}
}

func useDesktopMachinePlatform(t *testing.T, goos string) desktop.MachinePaths {
	t.Helper()
	originalOS, originalPaths := currentGOOS, managedDesktopPaths
	t.Cleanup(func() { currentGOOS, managedDesktopPaths = originalOS, originalPaths })
	currentGOOS = goos

	directory := "/Library/Application Support/ClaudeCode"
	policy := "/managed-settings.d/50-obs-claude-desktop.json"
	runtime := "/obs-agent-connector-claude-desktop"
	switch goos {
	case "linux":
		directory = "/etc/claude-code"
	case "windows":
		directory = "C:\\Program Files\\ClaudeCode"
		policy = "\\managed-settings.d\\50-obs-claude-desktop.json"
		runtime = "\\obs-agent-connector-claude-desktop.exe"
	}
	paths := desktop.MachinePaths{Directory: directory, PolicyPath: directory + policy, RuntimePath: directory + runtime}
	managedDesktopPaths = func() (desktop.MachinePaths, error) { return paths, nil }
	return paths
}
