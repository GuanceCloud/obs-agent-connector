package app

import (
	"flag"
	"fmt"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"os"
	"strings"
)

func install(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	endpoint := fs.String("endpoint", "", "GTrace endpoint")
	xToken := fs.String("x-token", "", "GTrace X-Token")
	agentID := fs.String("agent-id", "", "GTrace agent_id tag")
	agentName := fs.String("agent-name", "", "GTrace agent_name tag")
	staticBaseFlag := fs.String("static-base", "", "Installer script and plugin package base URL. Default: connector download source, then endpoint root domain")
	yes := fs.Bool("yes", false, "Skip confirmation")
	dryRun := fs.Bool("dry-run", false, "Print commands without installing")

	target := ""
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
		flagArgs = args[1:]
	}

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized install arguments: %s", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("install requires a single agent, for example: install codex")
	}

	selected, err := agent.Select(target)
	if err != nil {
		return err
	}
	if !agent.SupportsPlatform(selected[0], currentGOOS) {
		return unsupportedPlatformError(selected[0], currentGOOS)
	}
	selected, err = agent.ResolveForInstall(selected)
	if err != nil {
		return err
	}

	cfg, _, err := loadConnectorConfig()
	if err != nil {
		return err
	}

	input, err := resolveInstallInput(installInput{
		Endpoint:  strings.TrimSpace(*endpoint),
		XToken:    strings.TrimSpace(*xToken),
		AgentID:   strings.TrimSpace(*agentID),
		AgentName: strings.TrimSpace(*agentName),
	}, cfg, selected[0].Name)
	if err != nil {
		return err
	}

	pluginDownload, err := pluginDownloadSettings(*staticBaseFlag, cfg, input.Endpoint)
	if err != nil {
		return err
	}
	fmt.Println()
	fmt.Println("Install plan:")
	targets := make([]string, 0, len(selected))
	for _, p := range selected {
		p = agent.Resolve(p)
		url, err := downloadSourceURL(pluginDownload, p, currentGOOS)
		if err != nil {
			return err
		}
		targets = append(targets, fmt.Sprintf("%s (%s)", p.Name, url))
	}
	rows := [][2]string{
		{"Targets", strings.Join(targets, ", ")},
		{"Plugin Source", pluginDownload.Source},
		{"Plugin Base URL", pluginDownload.BaseURL},
		{"Type", fixedType},
		{"Endpoint", input.Endpoint},
		{"X-Token", input.XToken},
	}
	if len(input.GlobalTags) > 0 {
		rows = append(rows, [2]string{"Global Tags", strings.Join(input.GlobalTags, ", ")})
	}
	rows = append(rows,
		[2]string{"Agent ID", input.AgentID},
		[2]string{"Agent Name", input.AgentName},
	)
	printDetails(rows)

	if *dryRun {
		fmt.Println()
		fmt.Println("Command preview:")
		for _, p := range selected {
			p = agent.Resolve(p)
			fmt.Println(renderInstallCommand(pluginDownload, p, input))
		}
		return nil
	}

	if !*yes {
		ok, err := confirm("Continue installation?", true)
		if err != nil {
			return err
		}
		if !ok {
			printSingleDetail("Result", "canceled")
			return nil
		}
	}

	for _, p := range selected {
		p = agent.Resolve(p)
		if err := installOne(pluginDownload, p, input); err != nil {
			return err
		}
	}

	return nil
}
