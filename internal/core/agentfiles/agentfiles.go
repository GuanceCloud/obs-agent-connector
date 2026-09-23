package agentfiles

import (
	"os"
	"path/filepath"
	"strings"
)

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

// CodexHome returns the data directory used by the Codex CLI. CODEX_HOME is
// independent from the operating-system user home, which still owns connector
// managed files under .obs-agent-connector.
func CodexHome(home string) string {
	if configured, ok := ConfiguredCodexHome(); ok {
		return configured
	}
	return filepath.Join(home, ".codex")
}

func ConfiguredCodexHome() (string, bool) {
	configured := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if configured == "" {
		return "", false
	}
	return filepath.Clean(configured), true
}
