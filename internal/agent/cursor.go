package agent

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func cursorPlugin() Definition {
	return Definition{
		Name:                     "cursor",
		Backend:                  BackendBuiltin,
		BuiltinHookFile:          "~/.cursor/hooks.json",
		PluginName:               "cursor-otel-plugin",
		AgentCommand:             "cursor-agent",
		SupportedPlatforms:       []string{"darwin", "linux", "windows"},
		DiscoveryCommandOptional: true,
		Markers: []string{
			"~/.cursor/hooks.json",
			"~/.cursor/hooks/cursor-otel-plugin",
			"~/.cursor/plugins/cursor-otel-plugin",
		},
		ConfigFiles:     []string{"~/.obs-agent-connector/cursor/gtrace.json", "~/.cursor/gtrace.json"},
		EnabledJSONPath: []string{"enabled"},
		RemovePaths: []string{
			"~/.cursor/hooks/cursor-otel-plugin",
			"~/.cursor/plugins/cursor-otel-plugin",
		},
		RemoveCleanupDetails: []string{
			"~/.cursor/hooks.json (remove managed Cursor hook entries)",
		},
		ResolveInstall:   resolveCursorForInstall,
		ResolveDiscovery: resolveCursorForDiscovery,
	}
}

func resolveCursorForInstall(p Definition) (Definition, error) {
	if command, ok := resolveCursorCommandPath(); ok {
		p.AgentCommand = command
		return p, nil
	}
	if PathExists(ExpandHome("~/.cursor")) {
		return p, nil
	}
	return Definition{}, fmt.Errorf("cursor Agent data directory was not found; start Cursor before installing its plugin")
}

func resolveCursorForDiscovery(p Definition) (Definition, bool) {
	if command, ok := resolveCursorCommandPath(); ok {
		p.AgentCommand = command
		return p, true
	}
	if PathExists(ExpandHome("~/.cursor")) {
		return p, true
	}
	return Definition{}, false
}

func resolveCursorCommandPath() (string, bool) {
	candidates := []string{
		strings.TrimSpace(os.Getenv("CURSOR_BINARY")),
		strings.TrimSpace(os.Getenv("CURSOR_AGENT_BINARY")),
		strings.TrimSpace(os.Getenv("CURSOR_CLI_PATH")),
	}
	for _, name := range []string{"cursor-agent", "cursor"} {
		if pathCommand, err := exec.LookPath(name); err == nil && strings.TrimSpace(pathCommand) != "" {
			candidates = append(candidates, pathCommand)
		}
	}
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}
