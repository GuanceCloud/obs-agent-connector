package install

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

var kiroHookEvents = []string{
	"UserPromptSubmit",
	"PreToolUse",
	"PostToolUse",
	"Stop",
}

type KiroOptions struct {
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

type KiroResult struct {
	Executable string
	HooksFile  string
	ConfigFile string
	Configured bool
}

func InstallKiro(options KiroOptions) (KiroResult, error) {
	home := options.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return KiroResult{}, err
		}
	}
	source := options.SourceExecutable
	if source == "" {
		var err error
		source, err = os.Executable()
		if err != nil {
			return KiroResult{}, err
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
	hooksFile := firstInstallPath(options.HooksFile, filepath.Join(home, ".kiro", "hooks", "obs-agent-connector.json"))
	configFile := firstInstallPath(options.ConfigFile, agentfiles.ConfigPath(home, "kiro"))
	hooks, err := readJSONObject(hooksFile)
	if err != nil {
		return KiroResult{}, fmt.Errorf("parse Kiro Hooks: %w", err)
	}
	configValue, configExists, err := readJSONObjectIfExists(configFile)
	if err != nil {
		return KiroResult{}, fmt.Errorf("parse Kiro GTrace config: %w", err)
	}
	if !configExists && options.ConfigFile == "" {
		configValue, configExists, err = readJSONObjectIfExists(filepath.Join(home, ".kiro", "gtrace.json"))
		if err != nil {
			return KiroResult{}, fmt.Errorf("parse legacy Kiro GTrace config: %w", err)
		}
	}

	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		return KiroResult{}, err
	}
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return KiroResult{}, err
	}
	if absoluteSource != absoluteDestination {
		if err := copyExecutable(absoluteSource, absoluteDestination); err != nil {
			return KiroResult{}, err
		}
	}
	if err := writeKiroHooks(hooksFile, hooks, absoluteDestination); err != nil {
		return KiroResult{}, err
	}

	result := KiroResult{Executable: absoluteDestination, HooksFile: hooksFile, ConfigFile: configFile}
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

func writeKiroHooks(path string, current map[string]any, executable string) error {
	if current == nil {
		current = map[string]any{}
	}
	current["version"] = "v1"
	entries, _ := current["hooks"].([]any)
	next := make([]any, 0, len(entries)+len(kiroHookEvents))
	for _, entry := range entries {
		if !managedKiroHook(entry) {
			next = append(next, entry)
		}
	}
	for _, event := range kiroHookEvents {
		next = append(next, map[string]any{
			"name":        "obs-agent-connector-" + strings.ToLower(event),
			"description": "Collect Kiro telemetry with obs-agent-connector",
			"trigger":     event,
			"action": map[string]any{
				"type":    "command",
				"command": quoteHookCommand(executable) + " hook kiro " + event,
			},
			"timeout": 5,
			"enabled": true,
		})
	}
	current["hooks"] = next
	return writeJSONAtomic(path, current)
}

func managedKiroHook(value any) bool {
	entry, ok := value.(map[string]any)
	if !ok {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(fmt.Sprint(entry["name"])))
	if strings.HasPrefix(name, "obs-agent-connector-") {
		return true
	}
	action, _ := entry["action"].(map[string]any)
	command := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(action["command"])), `\`, "/"))
	return strings.Contains(command, "hook kiro") &&
		(strings.Contains(command, "obs-agent-connector") || strings.Contains(command, "kiro-otel-plugin"))
}
