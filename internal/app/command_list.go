package app

import (
	"fmt"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
)

func listPlugins() error {
	rows := [][]string{}
	found := false
	for _, name := range agent.Names() {
		p := agent.Resolve(agent.Get(name))
		installedAt, ok := agent.InstalledMarker(p)
		if !ok {
			continue
		}
		found = true
		configPath := agent.FirstExistingPath(p.ConfigFiles)
		if configPath == "" {
			configPath = "-"
		}
		rows = append(rows, []string{p.Name, agent.DisplayPath(configPath), agent.DisplayPath(installedAt)})
	}
	if !found {
		fmt.Println("No installed plugins found.")
		return nil
	}

	printTable([]string{"AGENT", "CONFIG", "PATH"}, rows)
	return nil
}
