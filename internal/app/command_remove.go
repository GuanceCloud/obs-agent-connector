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
	purgeConfig := fs.Bool("purge-config", false, "Also remove plugin configuration files and built-in adapter upload state")
	newRuntime := fs.Bool("n", false, "Use the new built-in runtime (Claude and Codex only)")
	fs.BoolVar(newRuntime, "new-runtime", false, "Use the new built-in runtime (Claude and Codex only)")

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

	var selected []agent.Definition
	var err error
	if *newRuntime {
		// Built-in removal is intentionally idempotent so a second run can
		// clean orphaned Codex trust state after the Hook itself is gone.
		selected, err = agent.SelectForRuntime(target, true)
	} else {
		selected, err = agent.SelectInstalledForRuntime(target, false)
	}
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		fmt.Println("No installed plugin found to remove.")
		return nil
	}

	fmt.Println("Remove plan:")
	rows := make([][2]string, 0, len(selected)*3)
	for _, p := range selected {
		p = agent.Resolve(p)
		rows = append(rows, [2]string{"Agent", p.Name})
		if p.IsBuiltin() {
			rows = append(rows, [2]string{"Hook", p.BuiltinHookFile + " (managed entry only)"})
			if p.Name == "codex" {
				rows = append(rows, [2]string{"Hook Trust", "matching and orphaned entries in ~/.codex/config.toml"})
			}
		}
		for _, cmd := range p.RemoveCmds {
			rows = append(rows, [2]string{"Command", strings.Join(cmd, " ")})
		}
		for _, path := range p.RemovePaths {
			rows = append(rows, [2]string{"Path", path})
		}
		if *purgeConfig {
			for _, path := range p.ConfigFiles {
				rows = append(rows, [2]string{"Config", path})
			}
		}
	}
	configMode := "kept"
	if *purgeConfig {
		configMode = "removed"
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
		p = agent.Resolve(p)
		if err := removeOne(p, *purgeConfig); err != nil {
			return err
		}
	}
	return nil
}
