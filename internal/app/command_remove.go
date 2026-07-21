package app

import (
	"flag"
	"fmt"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"os"
	"strings"
)

func remove(args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	yes := fs.Bool("yes", false, "Skip confirmation")
	dryRun := fs.Bool("dry-run", false, "Print what would be removed")
	purgeConfig := fs.Bool("purge-config", false, "Also remove plugin configuration files")

	target := ""
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
		flagArgs = args[1:]
	}
	if target == "" {
		return fmt.Errorf("remove requires an agent, for example: remove codex")
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized remove arguments: %s", strings.Join(fs.Args(), " "))
	}

	selected, err := agent.SelectInstalled(target)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		fmt.Println("No installed plugin found to remove.")
		return nil
	}

	fmt.Println("Remove plan:")
	for _, p := range selected {
		p = agent.Resolve(p)
		fmt.Printf("  - %s\n", p.Name)
		for _, cmd := range p.RemoveCmds {
			fmt.Printf("    command: %s\n", strings.Join(cmd, " "))
		}
		for _, path := range p.RemovePaths {
			fmt.Printf("    path: %s\n", path)
		}
		if *purgeConfig {
			for _, path := range p.ConfigFiles {
				fmt.Printf("    config: %s\n", path)
			}
		}
	}
	if !*purgeConfig {
		fmt.Println("Configuration files will be kept. Use --purge-config to remove them.")
	}

	if *dryRun {
		return nil
	}

	if !*yes {
		ok, err := confirm("Continue removal?", false)
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
		if err := removeOne(p, *purgeConfig); err != nil {
			return err
		}
	}
	return nil
}
