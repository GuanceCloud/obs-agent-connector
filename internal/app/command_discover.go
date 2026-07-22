package app

import (
	"flag"
	"fmt"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"os"
	"strings"
)

func discover(args []string) error {
	fs := flag.NewFlagSet("discover", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	endpoint := fs.String("endpoint", "", "GTrace endpoint")
	xToken := fs.String("x-token", "", "GTrace X-Token")
	staticBaseFlag := fs.String("static-base", "", "Installer script and plugin package base URL. Default: connector download source, then endpoint root domain")
	yes := fs.Bool("yes", false, "Skip confirmation")
	dryRun := fs.Bool("dry-run", false, "Print planned actions without installing")
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
	pending := make([]agent.Candidate, 0, len(candidates))
	hasUnsupported := false
	for _, candidate := range candidates {
		if !candidate.Supported {
			hasUnsupported = true
			rows = append(rows, []string{candidate.Plugin.Name, candidate.DetectedCmd, "n/a", "unsupported on windows"})
			continue
		}
		pluginState := "missing"
		action := "install"
		if candidate.InstalledPath != "" {
			pluginState = "installed"
			action = "skip"
		} else {
			pending = append(pending, candidate)
		}
		rows = append(rows, []string{candidate.Plugin.Name, candidate.DetectedCmd, pluginState, action})
	}

	fmt.Println("Discover plan:")
	printTable([]string{"AGENT", "COMMAND", "PLUGIN", "ACTION"}, rows)

	if len(pending) == 0 {
		if hasUnsupported {
			fmt.Println("No supported missing plugins found.")
		} else {
			fmt.Println("All detected Agents already have plugins installed.")
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
		ok, err := confirm("Continue discovery install?", true)
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
		if candidate.InstalledPath != "" {
			results = append(results, discoverResult{
				Agent:  candidate.Plugin.Name,
				Result: "skipped",
				Detail: "plugin already installed",
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

		results = append(results, discoverResult{
			Agent:  candidate.Plugin.Name,
			Result: "installed",
			Detail: fmt.Sprintf("agent_id=%s agent_name=%s", perAgent.AgentID, perAgent.AgentName),
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
