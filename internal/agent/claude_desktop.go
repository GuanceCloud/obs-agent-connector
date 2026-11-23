package agent

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

func claudeDesktopPlugin() Definition {
	return Definition{
		Name:               desktop.Name,
		Backend:            BackendBuiltin,
		PluginName:         "obs-agent-connector",
		AgentCommand:       "Claude Desktop",
		SupportedPlatforms: []string{"darwin", "linux", "windows"},
		ConfigFiles:        []string{agentfiles.ConfigPath("~", desktop.Name)},
		EnabledJSONPath:    []string{"enabled"},
		Resolve:            resolveClaudeDesktopPaths,
		DiscoverCommand:    discoverClaudeDesktopCommand,
	}
}

func resolveClaudeDesktopPaths(p Definition) Definition {
	if paths, err := desktop.ManagedPaths(); err == nil {
		p.BuiltinHookFile = paths.PolicyPath
	}
	return p
}

func discoverClaudeDesktopCommand(goos string) (string, bool) {
	goos = strings.ToLower(strings.TrimSpace(goos))
	if goos == "" {
		goos = runtime.GOOS
	}
	return findClaudeDesktopCommand(goos, isClaudeDesktopMSIXInstalled)
}

func findClaudeDesktopCommand(goos string, msixInstalled func() bool) (string, bool) {
	switch goos {
	case "darwin":
		home, _ := os.UserHomeDir()
		bundle := filepath.Join("Claude.app", "Contents", "MacOS", "Claude")
		command := FirstExistingPath([]string{
			filepath.Join(home, "Applications", bundle),
			filepath.Join("/Applications", bundle),
		})
		return lookupAgentCommand(command)
	case "linux":
		return lookupAgentCommand("claude-desktop")
	case "windows":
		if msixInstalled() {
			return "Claude (MSIX)", true
		}
	}
	return "", false
}
