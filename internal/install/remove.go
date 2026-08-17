package install

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

type RemoveResult struct {
	Adapter       string
	HookFile      string
	ConfigFile    string
	HookRemoved   bool
	TrustRemoved  bool
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
	case "codebuddy":
		return removeCodeBuddy(home, options)
	case "codex":
		return removeCodex(home, options)
	case "cursor":
		return removeCursor(home, options)
	default:
		return RemoveResult{}, errors.New("unsupported adapter " + adapter)
	}
}

func removeCursor(home string, options RemoveOptions) (RemoveResult, error) {
	result := RemoveResult{Adapter: "cursor", HookFile: filepath.Join(home, ".cursor", "hooks.json"), ConfigFile: filepath.Join(home, ".cursor", "gtrace.json")}
	settings, exists, err := readJSONObjectIfExists(result.HookFile)
	if err != nil {
		return result, err
	}
	if exists {
		hooks, _ := settings["hooks"].(map[string]any)
		managed := managedCursorHook
		if options.ConnectorOnly {
			managed = connectorManagedCursorHook
		}
		for _, event := range cursorHookEvents {
			entries, _ := hooks[event].([]any)
			next := make([]any, 0, len(entries))
			changed := false
			for _, entry := range entries {
				if managed(entry) {
					changed = true
					continue
				}
				next = append(next, entry)
			}
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
		if err := os.RemoveAll(filepath.Join(home, ".cursor", "gtrace")); err != nil {
			return result, err
		}
		result.StatePurged = true
	}
	return result, nil
}

func removeCodeBuddy(home string, options RemoveOptions) (RemoveResult, error) {
	result := RemoveResult{Adapter: "codebuddy", HookFile: filepath.Join(home, ".codebuddy", "settings.json"), ConfigFile: filepath.Join(home, ".codebuddy", "gtrace.json")}
	settings, exists, err := readJSONObjectIfExists(result.HookFile)
	if err != nil {
		return result, err
	}
	if exists {
		hooks, _ := settings["hooks"].(map[string]any)
		managed := managedCodeBuddyHook
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
		if err := os.RemoveAll(filepath.Join(home, ".codebuddy", "gtrace")); err != nil {
			return result, err
		}
		result.StatePurged = true
	}
	return result, nil
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
	managed := managedCodexHook
	if options.ConnectorOnly {
		managed = connectorManagedHook
	}
	var next []any
	var changed bool
	if exists {
		hooks, _ := settings["hooks"].(map[string]any)
		groups, _ := hooks["Stop"].([]any)
		locations := managedCodexTrustLocations(groups, managed)
		next, changed = removeManagedGroups(groups, managed)
		result.TrustRemoved, err = removeCodexTrustEntries(
			filepath.Join(home, ".codex", "config.toml"),
			result.HookFile,
			locations,
			len(next) == 0,
		)
		if err != nil {
			return result, fmt.Errorf("remove Codex Hook trust state: %w", err)
		}
		if changed {
			hooks["Stop"] = next
			result.HookRemoved = true
			if err := writeJSONAtomic(result.HookFile, settings); err != nil {
				return result, err
			}
		}
	} else {
		result.TrustRemoved, err = removeCodexTrustEntries(
			filepath.Join(home, ".codex", "config.toml"),
			result.HookFile,
			nil,
			true,
		)
		if err != nil {
			return result, fmt.Errorf("remove orphaned Codex Hook trust state: %w", err)
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

func managedCodexTrustLocations(groups []any, managed func(any) bool) map[string]struct{} {
	locations := map[string]struct{}{}
	for groupIndex, group := range groups {
		current, ok := group.(map[string]any)
		if !ok {
			continue
		}
		handlers, _ := current["hooks"].([]any)
		for handlerIndex, handler := range handlers {
			candidate := map[string]any{"hooks": []any{handler}}
			if managed(candidate) {
				locations[fmt.Sprintf("%d:%d", groupIndex, handlerIndex)] = struct{}{}
			}
		}
	}
	return locations
}

func removeCodexTrustEntries(path, hookFile string, locations map[string]struct{}, removeAllOrphans bool) (bool, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}

	lines := strings.SplitAfter(string(body), "\n")
	next := make([]string, 0, len(lines))
	removing := false
	changed := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			removing = false
			if key, ok := codexTrustSectionKey(trimmed); ok && codexTrustKeyMatches(key, hookFile, locations, removeAllOrphans) {
				removing = true
				changed = true
				continue
			}
		}
		if !removing {
			next = append(next, line)
		}
	}
	if !changed {
		return false, nil
	}
	return true, writeTextAtomic(path, []byte(strings.Join(next, "")), info.Mode().Perm())
}

func codexTrustSectionKey(header string) (string, bool) {
	const prefix = "[hooks.state."
	if !strings.HasPrefix(header, prefix) || !strings.HasSuffix(header, "]") {
		return "", false
	}
	raw := strings.TrimSpace(header[len(prefix) : len(header)-1])
	if len(raw) >= 2 && raw[0] == '\'' && raw[len(raw)-1] == '\'' {
		return raw[1 : len(raw)-1], true
	}
	value, err := strconv.Unquote(raw)
	if err != nil {
		return "", false
	}
	return value, true
}

func codexTrustKeyMatches(key, hookFile string, locations map[string]struct{}, removeAllOrphans bool) bool {
	normalized := strings.ReplaceAll(key, `\`, "/")
	marker := ":stop:"
	index := strings.LastIndex(strings.ToLower(normalized), marker)
	if index < 0 {
		return false
	}
	keyPath := filepath.Clean(normalized[:index])
	expectedPath := filepath.Clean(strings.ReplaceAll(hookFile, `\`, "/"))
	if runtime.GOOS == "windows" {
		if !strings.EqualFold(keyPath, expectedPath) {
			return false
		}
	} else if keyPath != expectedPath {
		return false
	}
	if removeAllOrphans {
		return true
	}
	_, ok := locations[normalized[index+len(marker):]]
	return ok
}

func writeTextAtomic(path string, body []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), ".codex-config-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if runtime.GOOS != "windows" {
		return os.Rename(tempPath, path)
	}
	backup, err := os.CreateTemp(filepath.Dir(path), ".codex-config-backup-*.toml")
	if err != nil {
		return err
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		return err
	}
	if err := os.Remove(backupPath); err != nil {
		return err
	}
	defer os.Remove(backupPath)
	if err := os.Rename(path, backupPath); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Rename(backupPath, path)
		return err
	}
	return os.Remove(backupPath)
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
