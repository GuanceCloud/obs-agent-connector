package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

var cursorHookEvents = []string{
	"sessionStart",
	"beforeSubmitPrompt",
	"preCompact",
	"afterAgentThought",
	"afterAgentResponse",
	"preToolUse",
	"postToolUse",
	"postToolUseFailure",
	"subagentStart",
	"subagentStop",
	"stop",
	"sessionEnd",
}

type CursorOptions struct {
	Home                  string
	SourceExecutable      string
	DestinationExecutable string
	HooksFile             string
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

type CursorResult struct {
	Executable string
	HooksFile  string
	ConfigFile string
	Configured bool
}

func InstallCursor(options CursorOptions) (CursorResult, error) {
	home := options.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return CursorResult{}, err
		}
	}
	source := options.SourceExecutable
	if source == "" {
		var err error
		source, err = os.Executable()
		if err != nil {
			return CursorResult{}, err
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
	hooksFile := firstInstallPath(options.HooksFile, filepath.Join(home, ".cursor", "hooks.json"))
	configFile := firstInstallPath(options.ConfigFile, filepath.Join(home, ".cursor", "gtrace.json"))
	hooks, err := readJSONObject(hooksFile)
	if err != nil {
		return CursorResult{}, fmt.Errorf("parse Cursor hooks: %w", err)
	}
	configValue, configExists, err := readJSONObjectIfExists(configFile)
	if err != nil {
		return CursorResult{}, fmt.Errorf("parse Cursor GTrace config: %w", err)
	}
	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		return CursorResult{}, err
	}
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return CursorResult{}, err
	}
	if absoluteSource != absoluteDestination {
		if err := copyExecutable(absoluteSource, absoluteDestination); err != nil {
			return CursorResult{}, err
		}
	}
	if err := writeCursorHooks(hooksFile, hooks, absoluteDestination); err != nil {
		return CursorResult{}, err
	}
	result := CursorResult{Executable: absoluteDestination, HooksFile: hooksFile, ConfigFile: configFile}
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

func writeCursorHooks(path string, settings map[string]any, executable string) error {
	if settings == nil {
		settings = map[string]any{}
	}
	settings["version"] = 1
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	for _, event := range cursorHookEvents {
		entries, _ := hooks[event].([]any)
		next := make([]any, 0, len(entries)+1)
		for _, entry := range entries {
			if !managedCursorHook(entry) {
				next = append(next, entry)
			}
		}
		next = append(next, map[string]any{
			"type":    "command",
			"command": quoteCursorCommand(executable) + " hook cursor " + event,
			"matcher": "*",
			"timeout": 5,
		})
		hooks[event] = next
	}
	return writeJSONAtomic(path, settings)
}

func managedCursorHook(value any) bool {
	entry, ok := value.(map[string]any)
	if !ok {
		return false
	}
	command := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(entry["command"])), `\`, "/"))
	return strings.Contains(command, "cursor-otel-plugin") ||
		(strings.Contains(command, "hook cursor") &&
			(strings.Contains(command, "obs-agent-connector") || strings.Contains(command, "agent-telemetry")))
}

func connectorManagedCursorHook(value any) bool {
	entry, ok := value.(map[string]any)
	if !ok {
		return false
	}
	command := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(entry["command"])), `\`, "/"))
	return strings.Contains(command, "obs-agent-connector") && strings.Contains(command, "hook cursor")
}

func quoteCursorCommand(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
