package install

import (
	"archive/zip"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/buildinfo"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

func InstallClaudeDesktop(options CodexOptions) (string, error) {
	home := options.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	home, err := filepath.Abs(home)
	if err != nil {
		return "", err
	}
	source := options.SourceExecutable
	if source == "" {
		source, err = os.Executable()
		if err != nil {
			return "", err
		}
	}
	source, err = filepath.Abs(source)
	if err != nil {
		return "", err
	}

	configPath := agentfiles.ConfigPath(home, desktop.Name)
	var config map[string]any
	if !options.NoConfig {
		current, exists, err := readJSONObjectIfExists(configPath)
		if err != nil {
			return "", fmt.Errorf("parse Claude Desktop GTrace config: %w", err)
		}
		if shouldConfigureGTrace(exists, options) {
			config, err = mergeCodexGTraceConfig(current, options, exists)
			if err != nil {
				return "", err
			}
		}
	}

	bundlePath := desktop.BundlePath(home)
	executable := desktop.ExecutablePath(home)
	if err := os.MkdirAll(filepath.Dir(bundlePath), 0o700); err != nil {
		return "", err
	}
	if source != executable {
		if err := copyExecutable(source, executable); err != nil {
			return "", fmt.Errorf("install Claude Desktop runtime: %w", err)
		}
	}
	if config != nil {
		if err := writeJSONAtomic(configPath, config); err != nil {
			return "", fmt.Errorf("write Claude Desktop GTrace config: %w", err)
		}
	}
	if err := writeClaudeDesktopBundle(bundlePath, executable, home); err != nil {
		return "", fmt.Errorf("write Claude Desktop plugin bundle: %w", err)
	}
	return bundlePath, nil
}

func writeClaudeDesktopBundle(bundlePath, executable, home string) error {
	version := strings.TrimPrefix(buildinfo.Version, "v")
	if version == "dev" || version == "" {
		version = "0.0.0"
	}
	manifest := map[string]any{
		"name":        desktop.PluginName,
		"version":     version,
		"description": "Collect local Claude Desktop Code and Cowork telemetry with OBS.",
	}
	handler := map[string]any{
		"type":          "command",
		"command":       executable,
		"args":          []string{"hook", desktop.Name, home},
		"timeout":       60,
		"statusMessage": "Uploading Claude telemetry to GTrace",
	}
	group := []map[string]any{{"hooks": []map[string]any{handler}}}
	hooks := map[string]any{
		"hooks": map[string]any{"Stop": group, "SessionEnd": group},
	}
	files := []struct {
		name string
		data map[string]any
	}{
		{".claude-plugin/plugin.json", manifest},
		{"hooks/hooks.json", hooks},
	}

	var body bytes.Buffer
	archive := zip.NewWriter(&body)
	for _, file := range files {
		encoded, err := marshalJSON(file.data)
		if err != nil {
			return err
		}
		entry, err := archive.Create(file.name)
		if err != nil {
			return err
		}
		if _, err := entry.Write(encoded); err != nil {
			return err
		}
	}
	if err := archive.Close(); err != nil {
		return err
	}
	return writeTextAtomic(bundlePath, body.Bytes(), 0o600)
}

func removeClaudeDesktop(home string, options RemoveOptions) (RemoveResult, error) {
	result := RemoveResult{
		Adapter:    desktop.Name,
		HookFile:   desktop.BundlePath(home),
		ConfigFile: agentfiles.ConfigPath(home, desktop.Name),
	}
	if err := removeFileIfExists(desktop.ExecutablePath(home)); err != nil {
		return result, err
	}
	if options.PurgeState {
		if err := removeFileIfExists(agentfiles.HookLogPath(home, desktop.Name)); err != nil {
			return result, err
		}
	}
	if options.PurgeConfig {
		if err := removeFileIfExists(result.ConfigFile); err != nil {
			return result, err
		}
		result.ConfigRemoved = true
	}
	// Keep the bundle as the lifecycle marker until cleanup has succeeded.
	if err := os.Remove(result.HookFile); err == nil {
		result.HookRemoved = true
	} else if !os.IsNotExist(err) {
		return result, err
	}
	return result, nil
}
