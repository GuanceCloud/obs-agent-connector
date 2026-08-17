package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

func cursorPlugin() Definition {
	return Definition{
		Name:                     "cursor",
		PluginName:               "cursor-otel-plugin",
		AgentCommand:             "cursor-agent",
		SupportedPlatforms:       []string{"darwin", "linux", "windows"},
		WindowsInstaller:         "install-release.ps1",
		DiscoveryCommandOptional: true,
		Markers: []string{
			"~/.cursor/hooks/cursor-otel-plugin",
			"~/.cursor/plugins/cursor-otel-plugin",
		},
		ConfigFiles:     []string{"~/.cursor/gtrace.json"},
		EnabledJSONPath: []string{"enabled"},
		RemovePaths: []string{
			"~/.cursor/hooks/cursor-otel-plugin",
			"~/.cursor/plugins/cursor-otel-plugin",
		},
		RemoveCleanupDetails: []string{
			"~/.cursor/hooks.json (remove managed Cursor hook entries)",
		},
		RemoveCleanup:    removeCursorHookConfig,
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

func removeCursorHookConfig(Definition) error {
	path := ExpandHome("~/.cursor/hooks.json")
	body, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil
	}

	var current map[string]any
	if err := json.Unmarshal([]byte(trimmed), &current); err != nil {
		return err
	}
	hooks, _ := current["hooks"].(map[string]any)
	if hooks == nil {
		return nil
	}

	changed := false
	for event, value := range hooks {
		items, ok := value.([]any)
		if !ok {
			continue
		}
		next := make([]any, 0, len(items))
		for _, item := range items {
			if managedCursorHook(item) {
				changed = true
				continue
			}
			next = append(next, item)
		}
		hooks[event] = next
	}
	if !changed {
		return nil
	}

	updated, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return err
	}
	updated = append(updated, '\n')
	return os.WriteFile(path, updated, 0o600)
}

func managedCursorHook(value any) bool {
	item, ok := value.(map[string]any)
	if !ok {
		return false
	}
	command := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(item["command"])), `\`, "/"))
	if command == "" {
		return false
	}
	return strings.Contains(command, "cursor-otel-plugin") ||
		(strings.Contains(command, "obs-agent-connector") && strings.Contains(command, "hook cursor")) ||
		(strings.Contains(command, "agent-telemetry") && strings.Contains(command, "hook cursor"))
}
