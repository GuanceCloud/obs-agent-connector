package app

import (
	"flag"
	"fmt"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"os"
	"strings"
)

func update(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("update requires a single agent, for example: update codex")
	}

	switch args[0] {
	case "cli", "self":
		return fmt.Errorf("update cli was removed and will move to the version command")
	case "plugin", "plugins", "agent", "agents":
		return updatePlugins(args[1:])
	default:
		return updatePlugins(args)
	}
}

func updatePlugins(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	staticBaseFlag := fs.String("static-base", "", "Installer script and plugin package base URL. Default: connector download source, then endpoint root domain")
	yes := fs.Bool("yes", false, "Skip confirmation")
	dryRun := fs.Bool("dry-run", false, "Print commands without updating")

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
		return fmt.Errorf("unrecognized update arguments: %s", strings.Join(fs.Args(), " "))
	}
	if target != "" {
		selectedTarget, err := agent.Select(target)
		if err != nil {
			return err
		}
		if !agent.SupportsPlatform(selectedTarget[0], currentGOOS) {
			return unsupportedPlatformError(selectedTarget[0], currentGOOS)
		}
	}

	selected, err := agent.SelectInstalled(target)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		fmt.Println("No installed plugin found to update.")
		return nil
	}

	var pluginDownload pluginDownloadConfig
	if !selected[0].IsBuiltin() {
		cfg, _, err := loadConnectorConfig()
		if err != nil {
			return err
		}
		pluginDownload, err = pluginDownloadSettings(*staticBaseFlag, cfg, "")
		if err != nil {
			return err
		}
	}
	fmt.Println("Update plan:")
	targets := make([]string, 0, len(selected))
	for _, p := range selected {
		p = agent.Resolve(p)
		if p.IsBuiltin() {
			targets = append(targets, fmt.Sprintf("%s (reconcile built-in adapter)", p.Name))
			continue
		}
		url, err := downloadSourceURL(pluginDownload, p, currentGOOS)
		if err != nil {
			return err
		}
		targets = append(targets, fmt.Sprintf("%s (%s)", p.Name, url))
	}
	rows := [][2]string{
		{"Targets", strings.Join(targets, ", ")},
		{"Config", "will not be modified"},
	}
	if !selected[0].IsBuiltin() {
		rows = append(rows, [2]string{"Plugin Source", pluginDownload.Source}, [2]string{"Plugin Base URL", pluginDownload.BaseURL})
	}
	printDetails(rows)

	if *dryRun {
		fmt.Println()
		fmt.Println("Command preview:")
		for _, p := range selected {
			p = agent.Resolve(p)
			if p.IsBuiltin() {
				fmt.Printf("reconcile %s hook with the current obs-agent-connector runtime\n", p.Name)
			} else {
				fmt.Println(renderPluginUpdateCommand(pluginDownload, p))
			}
		}
		return nil
	}

	if !*yes {
		ok, err := confirm("Continue plugin update?", true)
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
		if err := updatePluginOne(pluginDownload, p); err != nil {
			return err
		}
	}
	return nil
}
