package app

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func mergeConnectorConfig(args []string) error {
	fs := flag.NewFlagSet("internal merge-config", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	path := fs.String("path", "", "Connector config path")
	downloadBaseURL := fs.String("download-base-url", "", "Connector download base URL")
	pluginSource := fs.String("plugin-source", "", "External plugin source")
	pluginBaseURL := fs.String("plugin-base-url", "", "External plugin base URL")
	endpoint := fs.String("endpoint", "", "GTrace endpoint")
	xToken := fs.String("x-token", "", "GTrace X-Token")
	globalTags := fs.String("global-tags", "", "Newline-delimited global tags")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected internal merge-config arguments: %s", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(*path) == "" {
		return fmt.Errorf("--path is required")
	}

	current, err := readConnectorConfigObject(*path)
	if err != nil {
		return err
	}
	setNonEmptyString(current, "download_base_url", strings.TrimRight(strings.TrimSpace(*downloadBaseURL), "/"))
	setNonEmptyString(current, "plugin_source", normalizePluginSource(*pluginSource))
	setNonEmptyString(current, "plugin_base_url", strings.TrimRight(strings.TrimSpace(*pluginBaseURL), "/"))
	setNonEmptyString(current, "endpoint", strings.TrimSpace(*endpoint))
	setNonEmptyString(current, "x_token", strings.TrimSpace(*xToken))
	if tags := splitNonEmptyLines(*globalTags); len(tags) > 0 {
		current["global_tags"] = tags
	}
	return writeConnectorConfigAtomic(*path, current)
}

func readConnectorConfigObject(path string) (map[string]any, error) {
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	body, err = normalizeJSONBytes(body)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(string(body)) == "" {
		return map[string]any{}, nil
	}
	var current map[string]any
	if err := json.Unmarshal(body, &current); err != nil {
		return nil, fmt.Errorf("parse connector config: %w", err)
	}
	if current == nil {
		current = map[string]any{}
	}
	return current, nil
}

func setNonEmptyString(target map[string]any, key, value string) {
	if strings.TrimSpace(value) != "" {
		target[key] = value
	}
}

func splitNonEmptyLines(value string) []string {
	result := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}

func writeConnectorConfigAtomic(path string, value map[string]any) error {
	body, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	body = append(body, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".config-*.tmp")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
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
	return replaceConnectorConfig(tempPath, path)
}

func replaceConnectorConfig(tempPath, path string) error {
	if currentGOOS != "windows" {
		return os.Rename(tempPath, path)
	}
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return os.Rename(tempPath, path)
	} else if err != nil {
		return err
	}
	backup, err := os.CreateTemp(filepath.Dir(path), ".config-backup-*.json")
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
	if err := os.Rename(path, backupPath); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Rename(backupPath, path)
		return err
	}
	return os.Remove(backupPath)
}
