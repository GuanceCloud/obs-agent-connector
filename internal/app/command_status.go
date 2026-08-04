package app

import (
	"encoding/json"
	"flag"
	"fmt"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"os"
	"path/filepath"
	"strings"
)

func status(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	target := ""
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
		flagArgs = args[1:]
	}
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("status requires an agent, for example: status codex")
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized status arguments: %s", strings.Join(fs.Args(), " "))
	}

	selected, err := agent.Select(target)
	if err != nil {
		return err
	}
	p := agent.Resolve(selected[0])

	installedPath, installed := agent.InstalledMarker(p)
	configPath := agent.FirstExistingPath(p.ConfigFiles)
	enabledStatus := pluginEnabledStatus(p, configPath)

	rows := [][2]string{
		{"Agent", p.Name},
		{"Command", p.AgentCommand},
		{"Supported", yesNo(agent.SupportsPlatform(p, currentGOOS))},
		{"Installed", yesNo(installed)},
		{"Version", displayVersion(installedPluginVersion(p))},
		{"Config", displayPathOrDash(configPath)},
		{"Path", displayPathOrDash(installedPath)},
		{"Enabled", enabledStatus},
	}

	printDetails(rows)
	return nil
}

func pluginEnabledStatus(p agent.Definition, configPath string) string {
	if len(p.EnabledJSONPath) == 0 {
		return "unsupported"
	}
	if strings.TrimSpace(configPath) == "" {
		return "-"
	}

	value, found, err := readJSONBoolPath(configPath, p.EnabledJSONPath)
	if err != nil {
		return "invalid"
	}
	if !found {
		return "missing"
	}
	if value {
		return "true"
	}
	return "false"
}

func readJSONBoolPath(path string, jsonPath []string) (bool, bool, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false, err
	}
	data, err = normalizeJSONBytes(data)
	if err != nil {
		return false, false, fmt.Errorf("decode JSON %s: %w", filepath.Base(path), err)
	}

	var root map[string]any
	if len(strings.TrimSpace(string(data))) == 0 {
		return false, false, nil
	}
	if err := json.Unmarshal(data, &root); err != nil {
		return false, false, fmt.Errorf("parse JSON %s: %w", filepath.Base(path), err)
	}

	var current any = root
	for _, key := range jsonPath {
		object, ok := current.(map[string]any)
		if !ok {
			return false, false, nil
		}
		next, ok := object[key]
		if !ok {
			return false, false, nil
		}
		current = next
	}

	value, ok := current.(bool)
	if !ok {
		return false, false, nil
	}
	return value, true, nil
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func displayPathOrDash(path string) string {
	if strings.TrimSpace(path) == "" {
		return "-"
	}
	return agent.DisplayPath(path)
}
