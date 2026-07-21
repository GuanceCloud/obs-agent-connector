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

	staticBase := staticBaseURL(*staticBaseFlag, "")
	fmt.Println("Update plan. Configuration files will not be modified:")
	for _, p := range selected {
		p = agent.Resolve(p)
		url, err := installerURLForOS(staticBase, p, currentGOOS)
		if err != nil {
			return err
		}
		fmt.Printf("  - %s (%s)\n", p.Name, url)
	}

	if *dryRun {
		fmt.Println()
		fmt.Println("Command preview:")
		for _, p := range selected {
			p = agent.Resolve(p)
			fmt.Println(renderPluginUpdateCommand(staticBase, p))
		}
		return nil
	}

	if !*yes {
		ok, err := confirm("Continue plugin update?", true)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Canceled.")
			return nil
		}
	}

	for _, p := range selected {
		p = agent.Resolve(p)
		if err := updatePluginOne(staticBase, p); err != nil {
			return err
		}
	}
	return nil
}
