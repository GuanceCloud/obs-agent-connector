package install

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

type ClaudeOptions struct {
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

type ClaudeResult struct {
	Executable   string
	SettingsFile string
	ConfigFile   string
	Configured   bool
}

func InstallClaude(options ClaudeOptions) (ClaudeResult, error) {
	home := options.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return ClaudeResult{}, err
		}
	}
	source := options.SourceExecutable
	if source == "" {
		var err error
		source, err = os.Executable()
		if err != nil {
			return ClaudeResult{}, err
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
	settingsFile := options.SettingsFile
	if settingsFile == "" {
		settingsFile = filepath.Join(home, ".claude", "settings.json")
	}
	configFile := options.ConfigFile
	if configFile == "" {
		configFile = filepath.Join(home, ".claude", "gtrace.json")
	}

	settings, err := readJSONObject(settingsFile)
	if err != nil {
		return ClaudeResult{}, fmt.Errorf("parse Claude settings: %w", err)
	}
	configValue, configExists, err := readJSONObjectIfExists(configFile)
	if err != nil {
		return ClaudeResult{}, fmt.Errorf("parse Claude GTrace config: %w", err)
	}

	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		return ClaudeResult{}, err
	}
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return ClaudeResult{}, err
	}
	if absoluteSource != absoluteDestination {
		if err := copyExecutable(absoluteSource, absoluteDestination); err != nil {
			return ClaudeResult{}, err
		}
	}
	if err := writeClaudeHooks(settingsFile, settings, absoluteDestination); err != nil {
		return ClaudeResult{}, err
	}
	result := ClaudeResult{
		Executable:   absoluteDestination,
		SettingsFile: settingsFile,
		ConfigFile:   configFile,
	}
	if options.NoConfig {
		return result, nil
	}
	configOptions := CodexOptions{
		Endpoint:           options.Endpoint,
		TracePath:          options.TracePath,
		MetricsPath:        options.MetricsPath,
		InstallType:        options.InstallType,
		XToken:             options.XToken,
		Headers:            options.Headers,
		ResourceAttributes: options.ResourceAttributes,
		CaptureContent:     options.CaptureContent,
		MaxChars:           options.MaxChars,
		Enabled:            options.Enabled,
	}
	if !shouldConfigureGTrace(configExists, configOptions) {
		return result, nil
	}
	nextConfig, err := mergeCodexGTraceConfig(configValue, configOptions, configExists)
	if err != nil {
		return result, err
	}
	if err := writeJSONAtomic(configFile, nextConfig); err != nil {
		return result, err
	}
	result.Configured = true
	return result, nil
}

func writeClaudeHooks(path string, settings map[string]any, command string) error {
	if settings == nil {
		settings = map[string]any{}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	hookCommand := quoteHookCommand(command) + " hook claude"
	for _, event := range []string{"Stop", "SessionEnd"} {
		groups, _ := hooks[event].([]any)
		next := make([]any, 0, len(groups)+1)
		for _, group := range groups {
			if !managedClaudeHook(group) {
				next = append(next, group)
			}
		}
		next = append(next, map[string]any{
			"hooks": []any{
				map[string]any{
					"type":          "command",
					"command":       hookCommand,
					"timeout":       60,
					"statusMessage": "Uploading Claude telemetry to GTrace",
				},
			},
		})
		hooks[event] = next
	}
	return writeJSONWatched(path, settings)
}

func managedClaudeHook(value any) bool {
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
		command := strings.ToLower(strings.TrimSpace(fmt.Sprint(handler["command"])))
		if strings.Contains(command, "obs-agent-connector") ||
			strings.Contains(command, "agent-telemetry") ||
			strings.Contains(command, "gtrace-agent") ||
			strings.Contains(command, "claude_otel_hook") ||
			strings.Contains(command, "claude-otel-plugin") {
			return true
		}
		args, _ := handler["args"].([]any)
		if len(args) >= 2 && fmt.Sprint(args[0]) == "hook" && fmt.Sprint(args[1]) == "claude" {
			return true
		}
	}
	return false
}

func copyExecutable(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		return err
	}
	temp := destination + ".tmp"
	output, err := os.OpenFile(temp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		_ = os.Remove(temp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(temp)
		return closeErr
	}
	if err := os.Chmod(temp, 0o755); err != nil {
		_ = os.Remove(temp)
		return err
	}
	return os.Rename(temp, destination)
}

func writeJSONAtomic(path string, value any) error {
	body, err := marshalJSON(value)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, body, 0o600); err != nil {
		return err
	}
	return os.Rename(temp, path)
}

// writeJSONWatched preserves an existing file's identity so applications that
// watch the file itself (instead of its parent directory) receive the update.
// New files still use the atomic writer.
func writeJSONWatched(path string, value any) error {
	body, err := marshalJSON(value)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if os.IsNotExist(err) {
		return writeJSONAtomic(path, value)
	}
	if err != nil {
		return err
	}
	if _, err := file.WriteAt(body, 0); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Truncate(int64(len(body))); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func marshalJSON(value any) ([]byte, error) {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(body, '\n'), nil
}
