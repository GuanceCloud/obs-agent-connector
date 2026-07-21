package app

import "fmt"

const (
	appName           = "obs-agent-connector"
	fixedType         = "gtrace"
	defaultStaticBase = "https://static.guance.com"
	configDirName     = ".obs-agent-connector"
	configFileName    = "config.json"
)

var version = "dev"

func Run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "list":
		return listPlugins()
	case "discover":
		return discover(args[1:])
	case "install":
		return install(args[1:])
	case "enable":
		return enable(args[1:])
	case "disable":
		return disable(args[1:])
	case "update":
		return update(args[1:])
	case "remove":
		return remove(args[1:])
	case "version":
		return showVersion(args[1:])
	case "-h", "--help", "help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Printf(`%s

Usage:
  obs-agent-connector <command> [arguments]

Commands:
  list                  List installed Agent plugins
  discover              Detect local Agents and install missing plugins
  install <agent>       Install an Agent plugin
  enable <agent>        Enable one installed Agent plugin
  disable <agent>       Disable one installed Agent plugin
  update <agent>        Update one installed Agent plugin
  remove <agent>        Remove an Agent plugin
  version               Show version and check for updates

Examples:
  obs-agent-connector discover
  obs-agent-connector install codex
  obs-agent-connector install qoder
  obs-agent-connector enable codex
  obs-agent-connector disable codex
  obs-agent-connector update codex
  obs-agent-connector remove codex
  obs-agent-connector version

`, appName)
}
