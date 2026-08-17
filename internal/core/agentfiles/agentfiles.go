package agentfiles

import "path/filepath"

const (
	RootDirectoryName = ".obs-agent-connector"
	ConfigFileName    = "gtrace.json"
	HookLogFileName   = "gtrace-hooks.json"
)

func Directory(home, agent string) string {
	return filepath.Join(home, RootDirectoryName, agent)
}

func ConfigPath(home, agent string) string {
	return filepath.Join(Directory(home, agent), ConfigFileName)
}

func HookLogPath(home, agent string) string {
	return filepath.Join(Directory(home, agent), HookLogFileName)
}
