package install

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	qwconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

type QwenWorkOptions struct {
	CodeBuddyOptions
	ProfileDir string
}

var qwenWorkEvents = []string{"SessionStart", "UserPromptSubmit", "PreToolUse", "PostToolUse", "PostToolUseFailure", "Stop", "SessionEnd"}

func qwenWorkProfile(home string) string {
	return qwconfig.ProfileDir(home)
}

func InstallQwenWork(o QwenWorkOptions) (CodeBuddyResult, error) {
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
		profile = qwenWorkProfile(home)
	}
	if strings.HasPrefix(profile, "~/") {
		profile = filepath.Join(home, profile[2:])
	}
	profile, err := filepath.Abs(profile)
	if err != nil {
		return result, err
	}
	settingsPath := firstInstallPath(o.SettingsFile, filepath.Join(profile, "settings.json"))
	configPath := firstInstallPath(o.ConfigFile, agentfiles.ConfigPath(home, "qwenwork-cn"))
	settings, err := readJSONObject(settingsPath)
	if err != nil {
		return result, fmt.Errorf("parse QwenWork settings: %w", err)
	}
	if value := settings["hooks"]; value != nil {
		if _, ok := value.(map[string]any); !ok {
			return result, fmt.Errorf("QwenWork hooks must be an object")
		}
	}
	if hooks, ok := settings["hooks"].(map[string]any); ok {
		for _, event := range qwenWorkEvents {
			if value := hooks[event]; value != nil {
				if _, ok := value.([]any); !ok {
					return result, fmt.Errorf("QwenWork Hook %s must be an array", event)
				}
			}
		}
	}
	current, exists, err := readJSONObjectIfExists(configPath)
	if err != nil {
		return result, err
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
	envPath := filepath.Join(profile, ".env")
	configureEnv := !o.NoConfig && (o.Enabled == nil || *o.Enabled)
	var envBody []byte
	envMode := os.FileMode(0600)
	envExists := false
	if configureEnv {
		// Atomic replacement must not detach a user-managed dotenv symlink.
		// Lstat also catches dangling links before ReadFile treats them as absent.
		info, statErr := os.Lstat(envPath)
		if statErr != nil && !os.IsNotExist(statErr) {
			return result, fmt.Errorf("inspect QwenWork environment: %w", statErr)
		}
		if statErr == nil && !info.Mode().IsRegular() {
			return result, fmt.Errorf("QwenWork environment must be a regular file")
		}
		envBody, err = os.ReadFile(envPath)
		if err != nil && !os.IsNotExist(err) {
			return result, fmt.Errorf("read QwenWork environment: %w", err)
		}
		if err == nil {
			envExists = true
			info, statErr := os.Stat(envPath)
			if statErr != nil {
				return result, statErr
			}
			envMode = info.Mode().Perm()
		}
		envBody, err = enableQwenWorkTokenUsage(envBody)
		if err != nil {
			return result, fmt.Errorf("parse QwenWork environment: %w", err)
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
	paths := []string{settingsPath}
	if configure {
		paths = append(paths, configPath)
	}
	if configureEnv {
		paths = append(paths, envPath)
	}
	restore, err := snapshotManagedFiles(paths)
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
	stripQwenWorkHooks(hooks, false)
	command := quoteQwenWorkPath(destination) + " hook qwenwork-cn --profile " + quoteQwenWorkPath(profile)
	for _, event := range qwenWorkEvents {
		groups, _ := hooks[event].([]any)
		group := map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 5}}}
		if strings.Contains(event, "Tool") || strings.HasPrefix(event, "Subagent") {
			group["matcher"] = ".*"
		}
		hooks[event] = append(groups, group)
	}
	if err = writeJSONWatched(settingsPath, settings); err != nil {
		return result, err
	}
	result = CodeBuddyResult{Executable: destination, SettingsFile: settingsPath, ConfigFile: configPath}
	if configureEnv {
		if err = os.MkdirAll(profile, 0700); err != nil {
			return result, err
		}
		if envExists {
			err = writeTextAtomic(envPath, envBody, envMode)
		} else {
			// writeTextAtomic's Windows replacement requires an existing target.
			err = writeFileAtomic(envPath, envBody)
		}
		if err != nil {
			return result, err
		}
	}
	if configure {
		if err = writeJSONAtomic(configPath, next); err != nil {
			return result, err
		}
		result.Configured = true
	}
	committed = true
	return result, nil
}

// Filter individual handlers, not whole groups, to preserve user Hooks sharing a group.
func stripQwenWorkHooks(hooks map[string]any, connectorOnly bool) bool {
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
				managed := managedQwenWorkCommand.MatchString(command)
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

// Match only standalone connector invocations, never arbitrary commands that
// mention the connector in an echo, pipeline or another program's arguments.
var managedQwenWorkCommand = regexp.MustCompile(`^(?:"(?:[^"\r\n]*[\\/])?obs-agent-connector(?:\.exe)?"|'(?:(?:[^']|'"'"')*[\\/])?obs-agent-connector(?:\.exe)?'|(?:[^\s"';&|<>` + "`" + `$()]*[\\/])?obs-agent-connector(?:\.exe)?) hook qwenwork-cn(?: --profile (?:"[^"\r\n]*"|'(?:[^']|'"'"')*'))?$`)

func quoteQwenWorkPath(path string) string {
	if runtime.GOOS == "windows" {
		return quoteHookCommand(path)
	}
	return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'"
}

func removeQwenWork(home string, o RemoveOptions) (RemoveResult, error) {
	profile := qwenWorkProfile(home)
	result := RemoveResult{Adapter: "qwenwork-cn", HookFile: filepath.Join(profile, "settings.json"), ConfigFile: agentfiles.ConfigPath(home, "qwenwork-cn")}
	settings, exists, err := readJSONObjectIfExists(result.HookFile)
	if err != nil {
		return result, err
	}
	if exists {
		hooks, _ := settings["hooks"].(map[string]any)
		result.HookRemoved = stripQwenWorkHooks(hooks, o.ConnectorOnly)
		if result.HookRemoved {
			if err = writeJSONWatched(result.HookFile, settings); err != nil {
				return result, err
			}
		}
	}
	if o.PurgeConfig {
		if err = removeConfigFiles(result.ConfigFile); err != nil {
			return result, err
		}
		result.ConfigRemoved = true
	}
	if o.PurgeState {
		if err = os.RemoveAll(filepath.Join(agentfiles.Directory(home, "qwenwork-cn"), "state")); err != nil {
			return result, err
		}
		if err = removeFileIfExists(agentfiles.HookLogPath(home, "qwenwork-cn")); err != nil {
			return result, err
		}
		result.StatePurged = true
	}
	return result, nil
}
