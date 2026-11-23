package app

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	"github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
	telemetryinstall "github.com/GuanceCloud/obs-agent-connector/internal/install"
)

func TestClaudeDesktopDiscoveryUsesMachineFiles(t *testing.T) {
	for _, goos := range []string{"darwin", "linux", "windows"} {
		t.Run(goos, func(t *testing.T) {
			checkClaudeDesktopDiscovery(t, useDesktopMachinePlatform(t, goos))
		})
	}
}

func checkClaudeDesktopDiscovery(t *testing.T, paths desktop.MachinePaths) {
	t.Helper()
	home, accountHome := t.TempDir(), t.TempDir()
	setTestHome(t, home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", t.TempDir())
	t.Setenv("OBS_AGENT_CONNECTOR_CONFIG", filepath.Join(home, "connector.json"))
	originalDiscovery, originalState, originalManagedHome := discoverCandidatesForOS, managedDesktopState, managedDesktopHome
	originalInstall := installManagedDesktop
	t.Cleanup(func() {
		discoverCandidatesForOS, managedDesktopState, managedDesktopHome = originalDiscovery, originalState, originalManagedHome
		installManagedDesktop = originalInstall
	})
	discoverCandidatesForOS = func(goos string) []agent.Candidate {
		definition := agent.Get(desktop.Name)
		return []agent.Candidate{{Plugin: definition, DetectedCmd: "Claude Desktop", Supported: agent.SupportsPlatform(definition, goos)}}
	}
	managedDesktopHome = func() (string, error) { return accountHome, nil }
	var machine telemetryinstall.ManagedDesktopState
	var stateErr error
	managedDesktopState = func() (telemetryinstall.ManagedDesktopState, error) {
		return machine, stateErr
	}
	var installed []telemetryinstall.CodexOptions
	installManagedDesktop = func(options telemetryinstall.CodexOptions) error {
		installed = append(installed, options)
		machine = telemetryinstall.ManagedDesktopState{Policy: true, Runtime: true}
		return nil
	}
	accountConfig := agentfiles.ConfigPath(accountHome, desktop.Name)
	processConfig := agentfiles.ConfigPath(home, desktop.Name)
	const accountConfigBody = `{"enabled":true,"captureContent":"none","headers":{"X-Token":"fixture-token"}}`
	writeClaudeDesktopFixture(t, accountConfig, accountConfigBody)
	writeClaudeDesktopFixture(t, processConfig, `{"enabled":false,"captureContent":"full"}`)
	run := func(args ...string) string {
		t.Helper()
		return captureStdout(t, func() {
			if err := Run(args); err != nil {
				t.Fatal(err)
			}
		})
	}
	discover := func(flags ...string) string {
		t.Helper()
		return run(append([]string{"discover", "--endpoint=https://test.invalid", "--x-token=fixture-token"}, flags...)...)
	}
	output := discover("--dry-run")
	assertDesktopDiscoveryAction(t, output, "missing", "install")
	if !strings.Contains(output, paths.PolicyPath) || !strings.Contains(output, "administrator authorization") || strings.Contains(output, "fixture-token") || len(installed) != 0 {
		t.Fatalf("fresh discovery preview did not safely show machine installation: %s", output)
	}
	discover("--yes")
	if len(installed) != 1 || installed[0].NoConfig || installed[0].XToken != "fixture-token" {
		t.Fatal("fresh discovery did not install the machine registration with user defaults")
	}
	if output := run("list"); !strings.Contains(output, paths.PolicyPath) || !strings.Contains(output, accountConfig) || strings.Contains(output, "fixture-token") {
		t.Fatalf("managed listing did not show the machine policy and account config: %s", output)
	}
	assertDesktopDiscoveryAction(t, discover("--yes"), "managed", "skip")
	output = discover("--update", "--dry-run")
	assertDesktopDiscoveryAction(t, output, "managed", "update")
	if !strings.Contains(output, "administrator authorization") || len(installed) != 1 {
		t.Fatalf("machine discovery update preview omitted authorization or invoked the installer: %s", output)
	}
	discover("--update", "--yes")
	if len(installed) != 2 || !installed[1].NoConfig {
		t.Fatal("machine discovery update failed to preserve user config")
	}
	machine = telemetryinstall.ManagedDesktopState{Runtime: true}
	output = discover("--yes")
	assertDesktopDiscoveryAction(t, output, "partial", "skip")
	if !strings.Contains(output, "Run update claude-desktop") || strings.Contains(output, "All detected Agents already have plugins installed") {
		t.Fatalf("partial discovery did not explain how to reconcile the machine files: %s", output)
	}
	assertDesktopDiscoveryAction(t, discover("--update", "--yes"), "partial", "update")
	if !machine.Policy || len(installed) != 3 || !installed[2].NoConfig {
		t.Fatal("discovery did not reconcile an incomplete machine registration")
	}
	if body, err := os.ReadFile(accountConfig); err != nil || string(body) != accountConfigBody {
		t.Fatalf("discovery update changed the account's capture settings: %v", err)
	}
	stateErr = errors.New("foreign machine policy")
	if err := Run([]string{"discover", "--update", "--yes", "--endpoint=https://test.invalid", "--x-token=fixture-token"}); !errors.Is(err, stateErr) || len(installed) != 3 {
		t.Fatalf("discovery overlooked an ambiguous machine registration: %v", err)
	}
}

func assertDesktopDiscoveryAction(t *testing.T, output, state, action string) {
	t.Helper()
	for _, line := range strings.Split(output, "\n") {
		if fields := strings.Fields(line); len(fields) >= 5 && fields[0] == desktop.Name && fields[len(fields)-3] == state && fields[len(fields)-1] == action {
			return
		}
	}
	t.Fatalf("Claude Desktop discovery did not show %s and %s: %s", state, action, output)
}
