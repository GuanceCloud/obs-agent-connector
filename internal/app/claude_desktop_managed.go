package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	"github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
	telemetryinstall "github.com/GuanceCloud/obs-agent-connector/internal/install"
)

var (
	installManagedDesktop = telemetryinstall.InstallClaudeDesktopManaged
	removeManagedDesktop  = telemetryinstall.RemoveClaudeDesktopManaged
	managedDesktopState   = telemetryinstall.ClaudeDesktopManagedState
	managedDesktopHome    = desktop.ManagedHome
	managedDesktopPaths   = desktop.ManagedPaths
)

func managedDesktopTarget(target string) (bool, error) {
	isDesktop := strings.EqualFold(strings.TrimSpace(target), desktop.Name)
	if isDesktop && !agent.SupportsPlatform(agent.Get(desktop.Name), currentGOOS) {
		return false, unsupportedPlatformError(agent.Get(desktop.Name), currentGOOS)
	}
	return isDesktop, nil
}

func managedDesktopPlan(paths desktop.MachinePaths) [][2]string {
	return [][2]string{
		{"Registration", "Machine-level Hooks (administrator authorization required)"},
		{"System Hook policy", paths.PolicyPath},
		{"System runtime", paths.RuntimePath},
	}
}

func runManagedDesktopInstall(input installInput, update bool) error {
	if err := installManagedDesktop(telemetryinstall.CodexOptions{
		Endpoint: input.Endpoint, TracePath: input.TracePath, MetricsPath: input.MetricsPath,
		InstallType: fixedType, XToken: input.XToken, Headers: input.Headers,
		ResourceAttributes: builtinResourceAttributes(input), CaptureContent: input.CaptureContent,
		MaxChars: input.MaxChars, Enabled: input.Enabled, NoConfig: update,
	}); err != nil {
		return err
	}
	printSingleDetail("Activation", "Start a new local Code or Cowork session to load the managed Hooks.")
	return nil
}

func changeManagedDesktop(action string, yes, dryRun bool) error {
	paths, err := managedDesktopPaths()
	if err != nil {
		return err
	}
	fmt.Printf("%s Claude Desktop machine files\n", capitalizeWord(action))
	rows := append(managedDesktopPlan(paths), [2]string{"User configuration", "kept"})
	printDetails(rows)
	if dryRun {
		return nil
	}
	state, err := managedDesktopState()
	if err != nil {
		return err
	}
	if !state.Policy && !state.Runtime {
		if action == "update" {
			return fmt.Errorf("Claude Desktop machine files are absent. Run install claude-desktop to deploy them")
		}
		fmt.Println("No Claude Desktop machine files to remove.")
		return nil
	}
	if action == "update" && state.Blocked != nil {
		return state.Blocked
	}
	if !yes {
		ok, err := confirm("Continue managed Claude Desktop "+action+"?", action == "update")
		if err != nil {
			return err
		}
		if !ok {
			printSingleDetail("Result", "canceled")
			return nil
		}
	}
	if action == "update" {
		return runManagedDesktopInstall(installInput{}, true)
	}
	if err := removeManagedDesktop(""); err != nil {
		return err
	}
	printSingleDetail("Registration", "Machine Hook policy and runtime removed.")
	return nil
}

func claudeDesktopManagedStatus() error {
	paths, err := managedDesktopPaths()
	if err != nil {
		return err
	}
	state, err := managedDesktopState()
	if err != nil {
		return err
	}
	p := agent.Resolve(agent.Get(desktop.Name))
	path, err := desktopAccountConfigPath()
	if err != nil {
		return err
	}
	configPath := agent.FirstExistingPath([]string{path})
	rows := [][2]string{
		{"Agent", desktop.Name},
		{"System Hook policy present", yesNo(state.Policy)},
		{"System runtime present", yesNo(state.Runtime)},
		{"System Hook policy", paths.PolicyPath},
		{"System runtime", paths.RuntimePath},
		{"User config", displayPathOrDash(configPath)},
		{"Enabled for current user", pluginEnabledStatus(p, configPath)},
	}
	if state.Blocked != nil {
		rows = append(rows, [2]string{"Policy check", state.Blocked.Error()})
	}
	printDetails(rows)
	return nil
}

func desktopAccountConfigPath() (string, error) {
	home, err := managedDesktopHome()
	if err != nil {
		return "", err
	}
	return agentfiles.ConfigPath(home, desktop.Name), nil
}

func editableDesktopConfigPath() (string, error) {
	path, err := desktopAccountConfigPath()
	if err != nil {
		return "", err
	}
	_, err = os.Stat(path)
	if err == nil {
		return path, nil
	}
	if !os.IsNotExist(err) {
		return "", fmt.Errorf("inspect Claude Desktop user config: %w", err)
	}
	state, err := managedDesktopState()
	if err != nil {
		return "", err
	}
	if state.Policy && state.Runtime {
		return path, nil
	}
	if state.Policy || state.Runtime {
		return "", fmt.Errorf("Claude Desktop machine files are incomplete. Run update claude-desktop before setting up a new user config")
	}
	return "", fmt.Errorf("Claude Desktop machine files are absent. Run install claude-desktop to deploy them")
}

func claudeDesktopKeepsUploadLedger() bool {
	if !desktop.SupportsManagedOS(currentGOOS) {
		return false
	}
	state, err := managedDesktopState()
	return err != nil || state.Policy || state.Runtime
}
