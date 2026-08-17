package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

type CodeBuddyOptions struct {
	Home                  string
	SourceExecutable      string
	DestinationExecutable string
	SettingsFile          string
	ConfigFile            string
	Endpoint              string
	TracePath             string
	MetricsPath           string
	InstallType           string
	XToken                string
	Headers               []string
	ResourceAttributes    []string
	CaptureContent        string
	MaxChars              int
	Enabled               *bool
	NoConfig              bool
}

type CodeBuddyResult struct {
	Executable   string
	SettingsFile string
	ConfigFile   string
	Configured   bool
}

func InstallCodeBuddy(options CodeBuddyOptions) (CodeBuddyResult, error) {
	home := options.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return CodeBuddyResult{}, err
		}
	}
	source := options.SourceExecutable
	if source == "" {
		var err error
		source, err = os.Executable()
		if err != nil {
			return CodeBuddyResult{}, err
		}
	}
	destination := options.DestinationExecutable
	if destination == "" {
		name := "obs-agent-connector"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		destination = filepath.Join(home, ".local", "bin", name)
	}
	settingsFile := firstInstallPath(options.SettingsFile, filepath.Join(home, ".codebuddy", "settings.json"))
	configFile := firstInstallPath(options.ConfigFile, agentfiles.ConfigPath(home, "codebuddy"))
	settings, err := readJSONObject(settingsFile)
	if err != nil {
		return CodeBuddyResult{}, fmt.Errorf("parse CodeBuddy settings: %w", err)
	}
	configValue, configExists, err := readJSONObjectIfExists(configFile)
	if err != nil {
		return CodeBuddyResult{}, fmt.Errorf("parse CodeBuddy GTrace config: %w", err)
	}
	if !configExists && options.ConfigFile == "" {
		configValue, configExists, err = readJSONObjectIfExists(filepath.Join(home, ".codebuddy", "gtrace.json"))
		if err != nil {
			return CodeBuddyResult{}, fmt.Errorf("parse legacy CodeBuddy GTrace config: %w", err)
		}
	}
	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		return CodeBuddyResult{}, err
	}
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return CodeBuddyResult{}, err
	}
	if absoluteSource != absoluteDestination {
		if err := copyExecutable(absoluteSource, absoluteDestination); err != nil {
			return CodeBuddyResult{}, err
		}
	}
	if err := writeCodeBuddyHooks(settingsFile, settings, absoluteDestination); err != nil {
		return CodeBuddyResult{}, err
	}
	result := CodeBuddyResult{Executable: absoluteDestination, SettingsFile: settingsFile, ConfigFile: configFile}
	if options.NoConfig {
		return result, nil
	}
	configOptions := CodexOptions{
		Endpoint: options.Endpoint, TracePath: options.TracePath, MetricsPath: options.MetricsPath,
		InstallType: options.InstallType, XToken: options.XToken, Headers: options.Headers,
		ResourceAttributes: options.ResourceAttributes, CaptureContent: options.CaptureContent,
		MaxChars: options.MaxChars, Enabled: options.Enabled,
	}
	if !shouldConfigureGTrace(configExists, configOptions) {
		return result, nil
	}
	next, err := mergeCodexGTraceConfig(configValue, configOptions, configExists)
	if err != nil {
		return result, err
	}
	if err := writeJSONAtomic(configFile, next); err != nil {
		return result, err
	}
	result.Configured = true
	return result, nil
}

func writeCodeBuddyHooks(path string, settings map[string]any, executable string) error {
	if settings == nil {
		settings = map[string]any{}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	command := quoteHookCommand(executable) + " hook codebuddy"
	for _, event := range []string{"Stop", "SessionEnd"} {
		groups, _ := hooks[event].([]any)
		next := make([]any, 0, len(groups)+1)
		for _, group := range groups {
			if !managedCodeBuddyHook(group) {
				next = append(next, group)
			}
		}
		next = append(next, map[string]any{"hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 5}}})
		hooks[event] = next
	}
	return writeJSONWatched(path, settings)
}

func managedCodeBuddyHook(value any) bool {
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
		if strings.Contains(command, "codebuddy-hook") || strings.Contains(command, "codebuddy-otel-plugin") {
			return true
		}
		if strings.Contains(command, "hook codebuddy") && (strings.Contains(command, "obs-agent-connector") || strings.Contains(command, "agent-telemetry") || strings.Contains(command, "codebuddy-otel-plugin") || strings.Contains(command, "codebuddy-hook")) {
			return true
		}
	}
	return false
}

func quoteHookCommand(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
func firstInstallPath(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
