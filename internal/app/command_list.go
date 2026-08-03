package app

import (
	"flag"
	"fmt"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"os"
	"strings"
)

func listPlugins(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	newRuntime := fs.Bool("n", false, "Use the new built-in runtime for Claude and Codex")
	fs.BoolVar(newRuntime, "new-runtime", false, "Use the new built-in runtime for Claude and Codex")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized list arguments: %s", strings.Join(fs.Args(), " "))
	}

	rows := [][]string{}
	found := false
	selected, err := agent.SelectForRuntime("", *newRuntime)
	if err != nil {
		return err
	}
	for _, definition := range selected {
		p := agent.Resolve(definition)
		installedAt, ok := agent.InstalledMarker(p)
		if !ok {
			continue
		}
		found = true
		configPath := agent.FirstExistingPath(p.ConfigFiles)
		if configPath == "" {
			configPath = "-"
		}
		rows = append(rows, []string{
			p.Name,
			displayVersion(installedPluginVersion(p)),
			agent.DisplayPath(configPath),
			agent.DisplayPath(installedAt),
		})
	}
	if !found {
		fmt.Println("No installed plugins found.")
		return nil
	}

	printTable([]string{"AGENT", "VERSION", "CONFIG", "PATH"}, rows)
	return nil
}
