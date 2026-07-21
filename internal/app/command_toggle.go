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

func enable(args []string) error {
	return togglePlugin(args, true)
}

func disable(args []string) error {
	return togglePlugin(args, false)
}

func togglePlugin(args []string, enabled bool) error {
	commandName := "disable"
	stateLabel := "disabled"
	if enabled {
		commandName = "enable"
		stateLabel = "enabled"
	}

	fs := flag.NewFlagSet(commandName, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "Print the config change without writing")

	target := ""
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
		flagArgs = args[1:]
	}
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("%s requires an agent, for example: %s codex", commandName, commandName)
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized %s arguments: %s", commandName, strings.Join(fs.Args(), " "))
	}

	selected, err := agent.SelectInstalled(target)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		fmt.Printf("No installed plugin found to %s.\n", commandName)
		return nil
	}

	p := agent.Resolve(selected[0])
	if len(p.EnabledJSONPath) == 0 {
		return fmt.Errorf("%s does not support %s; its runtime config is not a supported JSON enabled switch", p.Name, commandName)
	}

	configPath := agent.FirstExistingPath(p.ConfigFiles)
	if configPath == "" {
		return fmt.Errorf("%s config file was not found; expected one of: %s", p.Name, strings.Join(p.ConfigFiles, ", "))
	}

	displayPath := agent.DisplayPath(configPath)
	if *dryRun {
		fmt.Printf("%s %s by setting %s -> %t\n", stateLabel, p.Name, displayPath, enabled)
		return nil
	}

	if err := setJSONBoolPath(configPath, p.EnabledJSONPath, enabled); err != nil {
		return fmt.Errorf("failed to %s %s: %w", commandName, p.Name, err)
	}

	fmt.Printf("%s %s in %s\n", capitalizeWord(stateLabel), p.Name, displayPath)
	return nil
}

func setJSONBoolPath(path string, jsonPath []string, value bool) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	var root map[string]any
	if len(strings.TrimSpace(string(data))) == 0 {
		root = map[string]any{}
	} else if err := json.Unmarshal(data, &root); err != nil {
		return fmt.Errorf("parse JSON %s: %w", filepath.Base(path), err)
	}

	setNestedJSONBool(root, jsonPath, value)

	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	return os.WriteFile(path, encoded, 0o644)
}

func setNestedJSONBool(root map[string]any, jsonPath []string, value bool) {
	current := root
	for i, key := range jsonPath {
		if i == len(jsonPath)-1 {
			current[key] = value
			return
		}

		next, ok := current[key]
		if !ok {
			child := map[string]any{}
			current[key] = child
			current = child
			continue
		}

		child, ok := next.(map[string]any)
		if !ok {
			child = map[string]any{}
			current[key] = child
		}
		current = child
	}
}

func capitalizeWord(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
