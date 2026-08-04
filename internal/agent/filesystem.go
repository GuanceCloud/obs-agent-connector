package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

func InstalledMarker(p Definition) (string, bool) {
	if p.IsBuiltin() {
		for _, rawPath := range p.Markers {
			path := ExpandHome(rawPath)
			if managedHookFile(path, p.Name) {
				return path, true
			}
		}
		// Legacy plugin directories remain valid installation evidence until
		// the built-in adapter reconciles their hooks.
		if len(p.Markers) > 1 {
			path := FirstExistingPath(p.Markers[1:])
			return path, path != ""
		}
		return "", false
	}
	path := FirstExistingPath(p.Markers)
	return path, path != ""
}

func managedHookFile(path, adapter string) bool {
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var value any
	if json.Unmarshal(body, &value) != nil {
		return false
	}
	return containsManagedHook(value, strings.ToLower(strings.TrimSpace(adapter)))
}

func containsManagedHook(value any, adapter string) bool {
	switch current := value.(type) {
	case []any:
		for _, item := range current {
			if containsManagedHook(item, adapter) {
				return true
			}
		}
	case map[string]any:
		command := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(current["command"])), `\`, "/"))
		managedRuntime := strings.Contains(command, "obs-agent-connector") ||
			strings.Contains(command, "agent-telemetry") ||
			strings.Contains(command, "gtrace-agent") ||
			strings.Contains(command, adapter+"-otel-plugin")
		if adapter == "codebuddy" && (strings.Contains(command, "codebuddy-hook") || strings.Contains(command, "codebuddy-otel-plugin")) {
			return true
		}
		if managedRuntime && strings.Contains(command, "hook "+adapter) {
			return true
		}
		args, _ := current["args"].([]any)
		if managedRuntime && len(args) >= 2 && fmt.Sprint(args[0]) == "hook" && fmt.Sprint(args[1]) == adapter {
			return true
		}
		for _, item := range current {
			if containsManagedHook(item, adapter) {
				return true
			}
		}
	}
	return false
}

func FirstExistingPath(paths []string) string {
	for _, path := range paths {
		expanded := ExpandHome(path)
		if PathExists(expanded) {
			return expanded
		}
	}
	return ""
}

func PathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ExpandHome(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + path[1:]
		}
	}
	return path
}

func DisplayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home+"/") {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}
