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
	purgeConfig := fs.Bool("purge-config", false, "Also remove legacy or external Agent configuration and upload state")

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
	selected = agent.ResolveForRemove(selected)
	if len(selected) == 0 {
		fmt.Println("No installed plugin found to remove.")
		return nil
	}

	fmt.Println("Remove plan:")
	rows := make([][2]string, 0, len(selected)*3)
	for _, p := range selected {
		rows = append(rows, [2]string{"Agent", p.Name})
		if p.IsBuiltin() {
			rows = append(rows, [2]string{"Hook", p.BuiltinHookFile + " (managed entry only)"})
		}
		for _, cmd := range p.RemoveCmds {
			rows = append(rows, [2]string{"Command", strings.Join(cmd, " ")})
		}
		for _, item := range p.RemoveCleanupDetails {
			rows = append(rows, [2]string{"Cleanup", item})
		}
		for _, path := range p.RemovePaths {
			rows = append(rows, [2]string{"Path", path})
		}
		if p.IsBuiltin() {
			rows = append(rows, [2]string{"Managed Files", "remove ~/.obs-agent-connector/" + p.Name})
		}
		if *purgeConfig {
			for _, path := range p.ConfigFiles {
				rows = append(rows, [2]string{"Config", path})
			}
		}
	}
	configMode := "legacy and external config kept"
	if *purgeConfig {
		configMode = "all config removed"
	}
	rows = append(rows, [2]string{"Config Mode", configMode})
	printDetails(rows)

	if *dryRun {
		return nil
	}

	if !*yes {
		ok, err := confirm("Continue removal?", false)
		if err != nil {
			return err
		}
		if !ok {
			printSingleDetail("Result", "canceled")
			return nil
		}
	}

	for _, p := range selected {
		if err := removeOne(p, *purgeConfig); err != nil {
			return err
		}
	}
	return nil
}
