package app

import (
	"flag"
	"fmt"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"os"
	"strings"
)

type discoverStep struct {
	Candidate agent.Candidate
	Action    string
}

func discover(args []string) error {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	endpoint := fs.String("endpoint", "", "GTrace endpoint")
	xToken := fs.String("x-token", "", "GTrace X-Token")
	staticBaseFlag := fs.String("static-base", "", "Installer script and plugin package base URL. Default: connector download source, then endpoint root domain")
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

	candidates := agent.DiscoverCandidatesForOS(currentGOOS)
	if len(candidates) == 0 {
		fmt.Println("No supported Agents detected.")
		return nil
	}

	rows := make([][]string, 0, len(candidates))
	steps := make([]discoverStep, 0, len(candidates))
	hasUnsupported := false
	for _, candidate := range candidates {
		version := displayVersion(candidate.InstalledVersion)
		if !candidate.Supported {
			hasUnsupported = true
			rows = append(rows, []string{candidate.Plugin.Name, candidate.DetectedCmd, "n/a", version, "unsupported"})
			continue
		}
		pluginState := "missing"
		action := "install"
		if candidate.InstalledPath != "" && *updateMode {
			pluginState = "installed"
			action = "update"
			steps = append(steps, discoverStep{Candidate: candidate, Action: action})
		} else if candidate.InstalledPath != "" {
			pluginState = "installed"
			action = "skip"
		} else {
			steps = append(steps, discoverStep{Candidate: candidate, Action: action})
		}
		rows = append(rows, []string{candidate.Plugin.Name, candidate.DetectedCmd, pluginState, version, action})
	}

	fmt.Println("Discover plan:")
	printTable([]string{"AGENT", "COMMAND", "PLUGIN", "VERSION", "ACTION"}, rows)

	if len(steps) == 0 {
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

	pluginDownload, err := pluginDownloadSettings(*staticBaseFlag, cfg, input.Endpoint)
	if err != nil {
		return fmt.Errorf("discover failed: %w", err)
	}
	fmt.Printf("Plugin Source: %s\n", pluginDownload.Source)
	fmt.Printf("Plugin Base URL: %s\n", pluginDownload.BaseURL)
	fmt.Printf("Endpoint: %s\n", input.Endpoint)
	fmt.Printf("X-Token: %s\n", input.XToken)

	if *dryRun {
		return nil
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
			fmt.Println("Canceled.")
			return nil
		}
	}

	results := make([]discoverResult, 0, len(candidates))
	hadFailure := false
	for _, candidate := range candidates {
		if !candidate.Supported {
			results = append(results, discoverResult{
				Agent:  candidate.Plugin.Name,
				Result: "skipped",
				Detail: "not supported on windows",
			})
			continue
		}
		if candidate.InstalledPath != "" && !*updateMode {
			results = append(results, discoverResult{
				Agent:  candidate.Plugin.Name,
				Result: "skipped",
				Detail: "plugin already installed version=" + displayVersion(candidate.InstalledVersion),
			})
			continue
		}

		if candidate.InstalledPath != "" {
			beforeVersion := candidate.InstalledVersion
			if err := updatePluginOne(pluginDownload, candidate.Plugin); err != nil {
				hadFailure = true
				results = append(results, discoverResult{
					Agent:  candidate.Plugin.Name,
					Result: "failed",
					Detail: err.Error(),
				})
				continue
			}
			afterVersion := agent.InstalledVersion(candidate.Plugin)
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

		if err := installOne(pluginDownload, candidate.Plugin, perAgent); err != nil {
			hadFailure = true
			results = append(results, discoverResult{
				Agent:  candidate.Plugin.Name,
				Result: "failed",
				Detail: err.Error(),
			})
			continue
		}

		installedVersion := agent.InstalledVersion(candidate.Plugin)
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

func displayVersion(version string) string {
	version = strings.TrimSpace(version)
	if version == "" {
		return "-"
	}
	return version
}
