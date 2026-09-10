package app

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/agent"
)

func listAgents(args []string) error {
	fs := flag.NewFlagSet("agents", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized agents arguments: %s", strings.Join(fs.Args(), " "))
	}

	var rows [][]string
	for _, name := range agent.Names() {
		definition := agent.Get(name)
		var platforms []string
		for _, platform := range []struct{ goos, label string }{
			{"linux", "Linux"}, {"darwin", "macOS"}, {"windows", "Windows"},
		} {
			if agent.SupportsPlatform(definition, platform.goos) {
				platforms = append(platforms, platform.label)
			}
		}
		rows = append(rows, []string{name, strings.Join(platforms, ", ")})
	}
	printTable([]string{"AGENT", "PLATFORMS"}, rows)
	return nil
}
