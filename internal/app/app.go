package app

import "fmt"

const (
	appName            = "obs-agent-connector"
	fixedType          = "gtrace"
	defaultStaticBase  = "https://static.guance.com"
	pluginSourceOSS    = "oss"
	pluginSourceGitHub = "github"
	configDirName      = ".obs-agent-connector"
	configFileName     = "config.json"
)

var version = "dev"

func Run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "list":
		return listPlugins(args[1:])
	case "status":
		return status(args[1:])
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
	case "uninstall":
		return uninstallConnector(args[1:])
	case "version":
		return showVersion(args[1:])
	case "internal":
		if len(args) >= 2 && args[1] == "merge-config" {
			return mergeConnectorConfig(args[2:])
		}
		return fmt.Errorf("unknown internal command")
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
  list [-n]             List installed Agent plugins
  status <agent> [-n]   Show one Agent plugin status
  discover [-n]         Detect local Agents; install missing plugins, or sync all with -u
  install <agent> [-n]  Install an Agent plugin
  enable <agent> [-n]   Enable one installed Agent plugin
  disable <agent> [-n]  Disable one installed Agent plugin
  update <agent> [-n]   Update one installed Agent plugin
  remove <agent> [-n]   Remove an Agent plugin
  uninstall             Uninstall obs-agent-connector
  version               Show version and check for updates

Examples:
  obs-agent-connector discover
  obs-agent-connector discover -n
  obs-agent-connector discover -u
  obs-agent-connector status codex
  obs-agent-connector install codex
  obs-agent-connector install codex -n
  obs-agent-connector install opencode
  obs-agent-connector install qoder
  obs-agent-connector enable codex
  obs-agent-connector disable codex
  obs-agent-connector update codex
  obs-agent-connector remove codex
  obs-agent-connector uninstall
  obs-agent-connector version

`, appName)
}
