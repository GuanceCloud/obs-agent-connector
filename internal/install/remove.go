package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type RemoveResult struct {
	Adapter       string
	HookFile      string
	ConfigFile    string
	HookRemoved   bool
	ConfigRemoved bool
	StatePurged   bool
}

type RemoveOptions struct {
	PurgeConfig   bool
	PurgeState    bool
	ConnectorOnly bool
}

func RemoveAdapter(adapter, home string, options RemoveOptions) (RemoveResult, error) {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return RemoveResult{}, err
		}
	}
	switch adapter {
	case "claude":
		return removeClaude(home, options)
	case "codex":
		return removeCodex(home, options)
	default:
		return RemoveResult{}, errors.New("unsupported adapter " + adapter)
	}
}

func RemoveRuntime(home string) error {
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return err
		}
	}
	names := []string{"agent-telemetry", "gtrace-agent"}
	if runtime.GOOS == "windows" {
		names = []string{"agent-telemetry.exe", "gtrace-agent.exe"}
	}
	for _, name := range names {
		path := filepath.Join(home, ".local", "bin", name)
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func removeClaude(home string, options RemoveOptions) (RemoveResult, error) {
	result := RemoveResult{
		Adapter:    "claude",
		HookFile:   filepath.Join(home, ".claude", "settings.json"),
		ConfigFile: filepath.Join(home, ".claude", "gtrace.json"),
	}
	settings, exists, err := readJSONObjectIfExists(result.HookFile)
	if err != nil {
		return result, err
	}
	if exists {
		hooks, _ := settings["hooks"].(map[string]any)
		managed := managedClaudeHook
		if options.ConnectorOnly {
			managed = connectorManagedHook
		}
		for _, event := range []string{"Stop", "SessionEnd"} {
			groups, _ := hooks[event].([]any)
			next, changed := removeManagedGroups(groups, managed)
			if changed {
				hooks[event] = next
				result.HookRemoved = true
			}
		}
		if result.HookRemoved {
			if err := writeJSONAtomic(result.HookFile, settings); err != nil {
				return result, err
			}
		}
	}
	if options.PurgeConfig {
		if err := removeFileIfExists(result.ConfigFile); err != nil {
			return result, err
		}
		result.ConfigRemoved = true
	}
	if options.PurgeState {
		for _, name := range []string{"obs-agent-connector", "agent-telemetry", "gtrace-agent"} {
			if err := os.RemoveAll(filepath.Join(home, ".claude", "state", name)); err != nil {
				return result, err
			}
		}
		result.StatePurged = true
	}
	return result, nil
}

func removeCodex(home string, options RemoveOptions) (RemoveResult, error) {
	result := RemoveResult{
		Adapter:    "codex",
		HookFile:   filepath.Join(home, ".codex", "hooks.json"),
		ConfigFile: filepath.Join(home, ".codex", "gtrace.json"),
	}
	settings, exists, err := readJSONObjectIfExists(result.HookFile)
	if err != nil {
		return result, err
	}
	if exists {
		hooks, _ := settings["hooks"].(map[string]any)
		groups, _ := hooks["Stop"].([]any)
		managed := managedCodexHook
		if options.ConnectorOnly {
			managed = connectorManagedHook
		}
		next, changed := removeManagedGroups(groups, managed)
		if changed {
			hooks["Stop"] = next
			result.HookRemoved = true
			if err := writeJSONAtomic(result.HookFile, settings); err != nil {
				return result, err
			}
		}
	}
	if options.PurgeConfig {
		if err := removeFileIfExists(result.ConfigFile); err != nil {
			return result, err
		}
		result.ConfigRemoved = true
	}
	if options.PurgeState {
		for _, name := range []string{"obs-agent-connector", "agent-telemetry", "gtrace-agent"} {
			if err := os.RemoveAll(filepath.Join(home, ".codex", "state", name)); err != nil {
				return result, err
			}
		}
		result.StatePurged = true
	}
	return result, nil
}

func connectorManagedHook(value any) bool {
	group, ok := value.(map[string]any)
	if !ok {
		return false
	}
	handlers, _ := group["hooks"].([]any)
	for _, item := range handlers {
		handler, ok := item.(map[string]any)
		if !ok {
			continue
		}
		command := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(handler["command"])), `\`, "/"))
		if strings.Contains(command, "obs-agent-connector") {
			return true
		}
	}
	return false
}

func removeManagedGroups(groups []any, managed func(any) bool) ([]any, bool) {
	next := make([]any, 0, len(groups))
	changed := false
	for _, group := range groups {
		if managed(group) {
			changed = true
			continue
		}
		next = append(next, group)
	}
	return next, changed
}

func removeFileIfExists(path string) error {
	err := os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
