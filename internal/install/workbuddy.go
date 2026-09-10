package install

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	wbconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/workbuddy/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

type WorkBuddyOptions struct {
	CodeBuddyOptions
	ProfileDir string
}

var workBuddyEvents = []string{"UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop", "StopFailure", "SessionEnd", "SubagentStart", "SubagentStop"}

func workBuddyProfile(home string) string {
	return wbconfig.ProfileDir(home, map[string]string{"WORKBUDDY_CONFIG_DIR": os.Getenv("WORKBUDDY_CONFIG_DIR"), "CODEBUDDY_CONFIG_DIR": os.Getenv("CODEBUDDY_CONFIG_DIR")})
}

func InstallWorkBuddy(o WorkBuddyOptions) (CodeBuddyResult, error) {
	var result CodeBuddyResult
	home := o.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return result, err
		}
	}
	profile := o.ProfileDir
	if profile == "" {
		profile = workBuddyProfile(home)
	}
	if strings.HasPrefix(profile, "~/") {
		profile = filepath.Join(home, profile[2:])
	}
	profile, err := filepath.Abs(profile)
	if err != nil {
		return result, err
	}
	settingsPath := firstInstallPath(o.SettingsFile, filepath.Join(profile, "settings.json"))
	configPath := firstInstallPath(o.ConfigFile, agentfiles.ConfigPath(home, "workbuddy"))
	settings, err := readJSONObject(settingsPath)
	if err != nil {
		return result, fmt.Errorf("parse WorkBuddy settings: %w", err)
	}
	registryPath := filepath.Join(profile, "plugins", "installed_plugins.json")
	registry, registryExists, err := readJSONObjectIfExists(registryPath)
	if err != nil {
		return result, fmt.Errorf("parse WorkBuddy plugin registry: %w", err)
	}
	for _, key := range []string{"hooks", "enabledPlugins"} {
		if value := settings[key]; value != nil {
			if _, ok := value.(map[string]any); !ok {
				return result, fmt.Errorf("WorkBuddy settings %s must be an object", key)
			}
		}
	}
	if value := registry["plugins"]; value != nil {
		if _, ok := value.(map[string]any); !ok {
			return result, fmt.Errorf("WorkBuddy registry plugins must be an object")
		}
	}
	if hooks, ok := settings["hooks"].(map[string]any); ok {
		for _, event := range workBuddyEvents {
			if value := hooks[event]; value != nil {
				if _, ok := value.([]any); !ok {
					return result, fmt.Errorf("WorkBuddy Hook %s must be an array", event)
				}
			}
		}
	}
	current, exists, err := readJSONObjectIfExists(configPath)
	if err != nil {
		return result, err
	}
	if !exists && o.ConfigFile == "" {
		current, exists, err = readJSONObjectIfExists(filepath.Join(profile, "gtrace.json"))
		if err != nil {
			return result, err
		}
	}
	var next map[string]any
	configure := false
	if !o.NoConfig {
		for old, key := range map[string]string{"capture_content": "captureContent", "max_chars": "maxChars", "timeout_ms": "timeoutMs"} {
			if current[key] == nil && current[old] != nil {
				current[key] = current[old]
			}
		}
		if capture, ok := current["captureContent"].(bool); ok {
			if capture {
				current["captureContent"] = "preview"
			} else {
				current["captureContent"] = "none"
			}
		}
		opts := CodexOptions{Endpoint: o.Endpoint, TracePath: o.TracePath, MetricsPath: o.MetricsPath, InstallType: o.InstallType, XToken: o.XToken, Headers: o.Headers, ResourceAttributes: o.ResourceAttributes, CaptureContent: o.CaptureContent, MaxChars: o.MaxChars, Enabled: o.Enabled}
		configure = shouldConfigureGTrace(exists, opts)
		if configure {
			next, err = mergeCodexGTraceConfig(current, opts, exists)
			if err != nil {
				return result, err
			}
			if o.MaxChars > 0 {
				next["maxChars"] = o.MaxChars
			}
		}
	}
	source := o.SourceExecutable
	if source == "" {
		source, err = os.Executable()
		if err != nil {
			return result, err
		}
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return result, err
	}
	destination := firstInstallPath(o.DestinationExecutable, source)
	destination, err = filepath.Abs(destination)
	if err != nil {
		return result, err
	}
	if source != destination {
		if err = copyExecutable(source, destination); err != nil {
			return result, err
		}
	}
	// Keep exact bytes for rollback if any part of registration/configuration fails.
	paths := []string{settingsPath, registryPath}
	if configure {
		paths = append(paths, configPath)
	}
	restore, err := snapshotWorkBuddyFiles(paths)
	if err != nil {
		return result, err
	}
	committed := false
	defer func() {
		if !committed {
			restore()
		}
	}()
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	stripWorkBuddyHooks(hooks, false)
	command := quoteHookCommand(destination) + " hook workbuddy --profile " + quoteHookCommand(profile)
	for _, event := range workBuddyEvents {
		groups, _ := hooks[event].([]any)
		group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 5}}}
		if strings.Contains(event, "Tool") || strings.HasPrefix(event, "Subagent") {
			group["matcher"] = ".*"
		}
		hooks[event] = append(groups, group)
	}
	// Disable the old plugin explicitly; keep its files and unrelated marketplace entries.
	enabled, ok := settings["enabledPlugins"].(map[string]any)
	if !ok {
		enabled = map[string]any{}
		settings["enabledPlugins"] = enabled
	}
	enabled["workbuddy-otel-plugin@guance"] = false
	if registryExists {
		if plugins, ok := registry["plugins"].(map[string]any); ok {
			if _, ok := plugins["workbuddy-otel-plugin@guance"]; ok {
				delete(plugins, "workbuddy-otel-plugin@guance")
				if err = writeJSONAtomic(registryPath, registry); err != nil {
					return result, err
				}
			}
		}
	}
	if err = writeJSONWatched(settingsPath, settings); err != nil {
		return result, err
	}
	result = CodeBuddyResult{Executable: destination, SettingsFile: settingsPath, ConfigFile: configPath}
	if configure {
		if err = writeJSONAtomic(configPath, next); err != nil {
			return result, err
		}
		result.Configured = true
	}
	committed = true
	return result, nil
}

func snapshotWorkBuddyFiles(paths []string) (func(), error) {
	type snapshot struct {
		path    string
		body    []byte
		existed bool
		mode    os.FileMode
	}
	var snapshots []snapshot
	for _, path := range paths {
		body, err := os.ReadFile(path)
		if os.IsNotExist(err) {
			snapshots = append(snapshots, snapshot{path: path})
			continue
		}
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot{path: path, body: body, existed: true, mode: info.Mode().Perm()})
	}
	return func() {
		for i := len(snapshots) - 1; i >= 0; i-- {
			s := snapshots[i]
			if s.existed {
				_ = writeTextAtomic(s.path, s.body, s.mode)
			} else {
				_ = removeFileIfExists(s.path)
			}
		}
	}, nil
}

// Filter individual handlers, not whole groups, to preserve user Hooks sharing a group.
func stripWorkBuddyHooks(hooks map[string]any, connectorOnly bool) bool {
	changed := false
	for event, value := range hooks {
		groups, ok := value.([]any)
		if !ok {
			continue
		}
		next := make([]any, 0, len(groups))
		for _, value := range groups {
			group, ok := value.(map[string]any)
			if !ok {
				next = append(next, value)
				continue
			}
			handlers, ok := group["hooks"].([]any)
			if !ok {
				next = append(next, value)
				continue
			}
			keep := make([]any, 0, len(handlers))
			removed := false
			for _, value := range handlers {
				handler, _ := value.(map[string]any)
				command, _ := handler["command"].(string)
				managed := strings.Contains(command, "obs-agent-connector") && strings.Contains(command, "hook workbuddy")
				if !connectorOnly {
					managed = managed || (strings.Contains(command, "workbuddy-otel-plugin") && strings.Contains(command, "workbuddy-hook"))
				}
				if managed {
					changed = true
					removed = true
				} else {
					keep = append(keep, value)
				}
			}
			if !removed {
				next = append(next, group)
			} else if len(keep) > 0 {
				group["hooks"] = keep
				next = append(next, group)
			}
		}
		hooks[event] = next
	}
	return changed
}

func removeWorkBuddy(home string, o RemoveOptions) (RemoveResult, error) {
	profile := workBuddyProfile(home)
	result := RemoveResult{Adapter: "workbuddy", HookFile: filepath.Join(profile, "settings.json"), ConfigFile: agentfiles.ConfigPath(home, "workbuddy")}
	settings, exists, err := readJSONObjectIfExists(result.HookFile)
	if err != nil {
		return result, err
	}
	if exists {
		hooks, _ := settings["hooks"].(map[string]any)
		result.HookRemoved = stripWorkBuddyHooks(hooks, o.ConnectorOnly)
		if result.HookRemoved {
			if err = writeJSONWatched(result.HookFile, settings); err != nil {
				return result, err
			}
		}
	}
	if o.PurgeConfig {
		if err = removeConfigFiles(result.ConfigFile, filepath.Join(profile, "gtrace.json")); err != nil {
			return result, err
		}
		result.ConfigRemoved = true
	}
	if o.PurgeState {
		if err = os.RemoveAll(filepath.Join(agentfiles.Directory(home, "workbuddy"), "state")); err != nil {
			return result, err
		}
		if err = removeFileIfExists(agentfiles.HookLogPath(home, "workbuddy")); err != nil {
			return result, err
		}
		result.StatePurged = true
	}
	return result, nil
}
