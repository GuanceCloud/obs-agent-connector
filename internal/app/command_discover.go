package app

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
)

var discoverCandidatesForOS = agent.DiscoverCandidatesForOS

const incompleteDesktopDiscovery = "machine files are incomplete. Run update claude-desktop to reconcile them"

type discoverStep struct {
	Candidate    agent.Candidate
	State        string
	Action       string
	Blocked      error
	DesktopPaths desktop.MachinePaths
}

func discover(args []string) error {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	endpoint := fs.String("endpoint", "", "GTrace endpoint")
	xToken := fs.String("x-token", "", "GTrace X-Token")
	staticBaseFlag := fs.String("static-base", "", "Installer script and plugin package base URL. OSS paths use the agent_plugins directory")
	yes := fs.Bool("yes", false, "Skip confirmation")
	dryRun := fs.Bool("dry-run", false, "Print planned actions without installing")
	updateMode := fs.Bool("u", false, "Update installed plugins and install missing ones")
	fs.BoolVar(updateMode, "update", false, "Update installed plugins and install missing ones")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized discover arguments: %s", strings.Join(fs.Args(), " "))
	}

	cfg, _, err := loadConnectorConfig()
	if err != nil {
		return fmt.Errorf("discover failed to load connector config: %w", err)
	}

	input, err := resolveCommonInstallInput(installInput{
		Endpoint: strings.TrimSpace(*endpoint),
		XToken:   strings.TrimSpace(*xToken),
	}, cfg)
	if err != nil {
		return fmt.Errorf("discover failed: %w", err)
	}

	candidates := discoverCandidatesForOS(currentGOOS)
	if len(candidates) == 0 {
		fmt.Println("No supported Agents detected.")
		return nil
	}

	rows := make([][]string, 0, len(candidates))
	steps := make([]discoverStep, 0, len(candidates))
	hasUnsupported := false
	hasPartialDesktop := false
	hasChanges := false
	needsExternalDownload := false
	var desktopPlan [][2]string
	var desktopBlocker error
	for _, candidate := range candidates {
		step := planDiscoveryStep(candidate, *updateMode)
		steps = append(steps, step)
		rows = append(rows, []string{candidate.Plugin.Name, candidate.DetectedCmd, step.State, displayVersion(step.Candidate.InstalledVersion), step.Action})
		hasUnsupported = hasUnsupported || step.Action == "unsupported"
		hasPartialDesktop = hasPartialDesktop || step.State == "partial"
		if step.Blocked != nil {
			desktopBlocker = step.Blocked
		}
		if step.Action == "install" || step.Action == "update" {
			hasChanges = true
			if candidate.Plugin.Name == desktop.Name {
				desktopPlan = managedDesktopPlan(step.DesktopPaths)
			}
			needsExternalDownload = needsExternalDownload || !candidate.Plugin.IsBuiltin()
		}
	}

	fmt.Println("Discover plan:")
	printTable([]string{"AGENT", "COMMAND", "PLUGIN", "VERSION", "ACTION"}, rows)
	if desktopBlocker != nil {
		printSingleDetail("Claude Desktop", desktopBlocker.Error())
	}
	if !hasChanges {
		if desktopBlocker != nil {
			return desktopBlocker
		}
		if hasPartialDesktop {
			printSingleDetail("Claude Desktop", incompleteDesktopDiscovery)
			return nil
		}
		if *updateMode {
			if hasUnsupported {
				fmt.Println("No supported plugins found to update.")
			} else {
				fmt.Println("All detected Agents already have plugins installed.")
			}
		} else {
			if hasUnsupported {
				fmt.Println("No supported missing plugins found.")
			} else {
				fmt.Println("All detected Agents already have plugins installed.")
			}
		}
		return nil
	}

	var pluginDownload pluginDownloadConfig
	if needsExternalDownload {
		pluginDownload, err = pluginDownloadSettings(*staticBaseFlag, cfg, input.Endpoint)
		if err != nil {
			return fmt.Errorf("discover failed: %w", err)
		}
	}
	detailRows := [][2]string{
		{"Endpoint", input.Endpoint},
		{"X-Token", "<configured>"},
	}
	if needsExternalDownload {
		detailRows = append(detailRows, [2]string{"Plugin Source", pluginDownload.Source}, [2]string{"Plugin Base URL", pluginDownload.BaseURL})
	}
	if len(input.GlobalTags) > 0 {
		detailRows = append(detailRows, [2]string{"Global Tags", strings.Join(input.GlobalTags, ", ")})
	}
	detailRows = append(detailRows, desktopPlan...)
	printDetails(detailRows)

	if *dryRun {
		return desktopBlocker
	}

	if !*yes {
		label := "Continue discovery install?"
		if *updateMode {
			label = "Continue discovery sync?"
		}
		ok, err := confirm(label, true)
		if err != nil {
			return err
		}
		if !ok {
			printSingleDetail("Result", "canceled")
			return nil
		}
	}

	results := make([]discoverResult, 0, len(candidates))
	hadFailure := false
	for _, step := range steps {
		candidate := step.Candidate
		switch step.Action {
		case "unsupported":
			detail := candidate.UnsupportedReason
			if strings.TrimSpace(detail) == "" {
				detail = "not supported on this platform"
			}
			results = append(results, discoverResult{
				Agent:  candidate.Plugin.Name,
				Result: "skipped",
				Detail: detail,
			})
			continue
		case "blocked":
			hadFailure = true
			results = append(results, discoverResult{Agent: desktop.Name, Result: "failed", Detail: step.Blocked.Error()})
			continue
		case "skip":
			detail := "plugin already installed version=" + displayVersion(candidate.InstalledVersion)
			switch step.State {
			case "managed":
				detail = "machine registration already present: " + step.DesktopPaths.PolicyPath
			case "partial":
				detail = incompleteDesktopDiscovery
			}
			results = append(results, discoverResult{
				Agent:  candidate.Plugin.Name,
				Result: "skipped",
				Detail: detail,
			})
			continue
		}

		if step.Action == "update" {
			if candidate.Plugin.Name == desktop.Name {
				if err := runManagedDesktopInstall(installInput{}, true); err != nil {
					hadFailure = true
					results = append(results, discoverResult{Agent: desktop.Name, Result: "failed", Detail: err.Error()})
					continue
				}
				results = append(results, discoverResult{Agent: desktop.Name, Result: "updated", Detail: step.DesktopPaths.PolicyPath})
				continue
			}
			beforeVersion := installedPluginVersion(candidate.Plugin)
			if err := updatePluginOne(pluginDownload, candidate.Plugin); err != nil {
				hadFailure = true
				results = append(results, discoverResult{
					Agent:  candidate.Plugin.Name,
					Result: "failed",
					Detail: err.Error(),
				})
				continue
			}
			afterVersion := installedPluginVersion(candidate.Plugin)
			detail := fmt.Sprintf("version=%s", displayVersion(afterVersion))
			if beforeVersion != "" && afterVersion != "" && beforeVersion != afterVersion {
				detail = fmt.Sprintf("version %s -> %s", beforeVersion, afterVersion)
			}
			results = append(results, discoverResult{
				Agent:  candidate.Plugin.Name,
				Result: "updated",
				Detail: detail,
			})
			continue
		}

		perAgent, err := resolveInstallInput(installInput{
			Endpoint: input.Endpoint,
			XToken:   input.XToken,
		}, cfg, candidate.Plugin.Name)
		if err != nil {
			hadFailure = true
			results = append(results, discoverResult{
				Agent:  candidate.Plugin.Name,
				Result: "failed",
				Detail: err.Error(),
			})
			continue
		}

		if candidate.Plugin.Name == desktop.Name {
			err = runManagedDesktopInstall(perAgent, false)
		} else {
			err = installOne(pluginDownload, candidate.Plugin, perAgent)
		}
		if err != nil {
			hadFailure = true
			results = append(results, discoverResult{
				Agent:  candidate.Plugin.Name,
				Result: "failed",
				Detail: err.Error(),
			})
			continue
		}
		if candidate.Plugin.Name == desktop.Name {
			results = append(results, discoverResult{Agent: desktop.Name, Result: "installed", Detail: step.DesktopPaths.PolicyPath})
			continue
		}

		installedVersion := installedPluginVersion(candidate.Plugin)
		results = append(results, discoverResult{
			Agent:  candidate.Plugin.Name,
			Result: "installed",
			Detail: fmt.Sprintf("version=%s agent_id=%s agent_name=%s", displayVersion(installedVersion), perAgent.AgentID, perAgent.AgentName),
		})
	}

	fmt.Println()
	fmt.Println("Discover summary:")
	summaryRows := make([][]string, 0, len(results))
	for _, result := range results {
		summaryRows = append(summaryRows, []string{result.Agent, result.Result, result.Detail})
	}
	printTable([]string{"AGENT", "RESULT", "DETAIL"}, summaryRows)

	if hadFailure {
		return fmt.Errorf("one or more plugin installs failed")
	}

	return nil
}

func planDiscoveryStep(candidate agent.Candidate, update bool) discoverStep {
	step := discoverStep{Candidate: candidate, State: "missing", Action: "install"}
	if !candidate.Supported {
		step.State, step.Action = "n/a", "unsupported"
		return step
	}
	if candidate.Plugin.Name == desktop.Name {
		paths, err := managedDesktopPaths()
		if err != nil {
			step.State, step.Action, step.Blocked = "conflict", "blocked", err
			return step
		}
		step.DesktopPaths = paths
		state, err := managedDesktopState()
		if err != nil {
			step.State, step.Action, step.Blocked = "conflict", "blocked", err
			return step
		}
		if state.Policy || state.Runtime {
			step.State = "managed"
			if !state.Policy || !state.Runtime {
				step.State = "partial"
			}
			if update && state.Blocked != nil {
				step.Action, step.Blocked = "blocked", state.Blocked
				return step
			}
		}
	} else if candidate.InstalledPath != "" {
		step.State = "installed"
	}
	if step.State != "missing" {
		step.Action = "skip"
		if update {
			step.Action = "update"
		}
	}
	return step
}

func displayVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return "-"
	}
	return version
}
