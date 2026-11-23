package app

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
)

func listPlugins(args []string) error {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized list arguments: %s", strings.Join(fs.Args(), " "))
	}

	rows := [][]string{}
	selected, err := agent.Select("")
	if err != nil {
		return err
	}
	for _, definition := range selected {
		p := agent.Resolve(definition)
		if p.Name == desktop.Name {
			if !agent.SupportsPlatform(p, currentGOOS) {
				continue
			}
			state, err := managedDesktopState()
			if err != nil || !state.Policy || !state.Runtime {
				continue
			}
			paths, err := managedDesktopPaths()
			if err != nil {
				continue
			}
			path, _ := desktopAccountConfigPath()
			configPath := agent.FirstExistingPath([]string{path})
			rows = append(rows, []string{
				p.Name,
				displayVersion(managedDesktopRuntimeVersion(paths.RuntimePath)),
				displayPathOrDash(configPath),
				paths.PolicyPath,
			})
			continue
		}
		installedAt, ok := agent.InstalledMarker(p)
		if !ok {
			continue
		}
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
	if len(rows) == 0 {
		fmt.Println("No installed plugins found.")
		return nil
	}

	printTable([]string{"AGENT", "VERSION", "CONFIG", "PATH"}, rows)
	return nil
}
