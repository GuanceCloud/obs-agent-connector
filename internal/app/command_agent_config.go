package app

import (
	"flag"
	"fmt"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	installpkg "github.com/GuanceCloud/obs-agent-connector/internal/install"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func configCommand(args []string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("config requires an agent and subcommand, for example: config codex list")
	}

	target := strings.TrimSpace(args[0])
	if target == "" {
		return fmt.Errorf("config requires an agent and subcommand, for example: config codex list")
	}
	if len(args) < 2 {
		return fmt.Errorf("config requires a subcommand, use list or edit")
	}

	switch strings.ToLower(strings.TrimSpace(args[1])) {
	case "list":
		return listAgentConfig(target, args[2:])
	case "edit":
		return editAgentConfig(target, args[2:])
	default:
		return fmt.Errorf("unknown config subcommand %q; available subcommands: list, edit", args[1])
	}
}

func listAgentConfig(target string, args []string) error {
	fs := flag.NewFlagSet("config list", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized config list arguments: %s", strings.Join(fs.Args(), " "))
	}

	p, configPath, err := resolveEditableRuntimeConfig(target)
	if err != nil {
		return err
	}

	current, exists, err := installpkg.ReadRuntimeConfig(configPath)
	if err != nil {
		return fmt.Errorf("read %s runtime config: %w", p.Name, err)
	}

	printDetails([][2]string{
		{"Agent", p.Name},
		{"Config", agent.DisplayPath(configPath)},
		{"Exists", yesNo(exists)},
	})
	fmt.Println()
	printTable([]string{"Parameter", "Current Value"}, runtimeConfigRows(current))
	fmt.Println()
	fmt.Printf("Use \"obs-agent-connector config %s edit\" to update one or more parameters.\n", p.Name)
	fmt.Println("--header and --tag both support one or more KEY=VALUE parameters.")
	return nil
}

func editAgentConfig(target string, args []string) error {
	fs := flag.NewFlagSet("config edit", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	enabledValue := fs.String("enabled", "", "Set runtime enabled to true or false")
	endpoint := fs.String("endpoint", "", "Set the OBS / GTrace endpoint")
	tracePath := fs.String("trace-path", "", "Set the trace upload path")
	metricsPath := fs.String("metrics-path", "", "Set the metrics upload path")
	xToken := fs.String("x-token", "", "Set the GTrace X-Token header")
	captureContent := fs.String("capture-content", "", "Set content capture mode: none, preview, or full")
	maxChars := fs.String("max-chars", "", "Set the maximum captured characters")
	var headers repeatedValue
	var tags repeatedValue
	fs.Var(&headers, "header", "Add one HTTP header KEY=VALUE; supports one or more --header parameters")
	fs.Var(&tags, "tag", "Add one resource attribute KEY=VALUE; supports one or more --tag parameters")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized config edit arguments: %s", strings.Join(fs.Args(), " "))
	}
	if err := validateAssignments(headers, "--header"); err != nil {
		return err
	}
	if err := validateAssignments(tags, "--tag"); err != nil {
		return err
	}

	options := installpkg.CodexOptions{InstallType: fixedType}
	changed := false

	if value := strings.TrimSpace(*enabledValue); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("invalid --enabled value %q; use true or false", *enabledValue)
		}
		options.Enabled = &parsed
		changed = true
	}
	if value := strings.TrimSpace(*endpoint); value != "" {
		options.Endpoint = strings.TrimRight(value, "/")
		changed = true
	}
	if value := strings.Trim(strings.TrimSpace(*tracePath), "/"); value != "" {
		options.TracePath = value
		changed = true
	}
	if value := strings.Trim(strings.TrimSpace(*metricsPath), "/"); value != "" {
		options.MetricsPath = value
		changed = true
	}
	if value := strings.TrimSpace(*xToken); value != "" {
		options.XToken = value
		changed = true
	}
	if len(headers) > 0 {
		options.Headers = append([]string{}, headers...)
		changed = true
	}
	if len(tags) > 0 {
		options.ResourceAttributes = append([]string{}, tags...)
		changed = true
	}
	if value := strings.ToLower(strings.TrimSpace(*captureContent)); value != "" {
		if value != "none" && value != "preview" && value != "full" {
			return fmt.Errorf("unsupported --capture-content %q", *captureContent)
		}
		options.CaptureContent = value
		changed = true
	}
	if value := strings.TrimSpace(*maxChars); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("invalid --max-chars value %q", *maxChars)
		}
		if parsed <= 0 {
			return fmt.Errorf("--max-chars must be positive")
		}
		options.MaxChars = parsed
		changed = true
	}
	if !changed {
		return fmt.Errorf("config edit requires one or more parameters to update")
	}

	p, effectiveConfigPath, err := resolveEditableRuntimeConfig(target)
	if err != nil {
		return err
	}
	configPath := effectiveConfigPath
	if p.IsBuiltin() && len(p.ConfigFiles) > 0 {
		configPath = agent.ExpandHome(p.ConfigFiles[0])
	}
	current, exists, err := installpkg.ReadRuntimeConfig(configPath)
	if err != nil {
		return fmt.Errorf("read %s runtime config: %w", p.Name, err)
	}
	if !exists && effectiveConfigPath != configPath {
		current, exists, err = installpkg.ReadRuntimeConfig(effectiveConfigPath)
		if err != nil {
			return fmt.Errorf("read legacy %s runtime config: %w", p.Name, err)
		}
	}
	next, err := installpkg.MergeRuntimeConfig(current, options, exists)
	if err != nil {
		return fmt.Errorf("merge %s runtime config: %w", p.Name, err)
	}
	if err := installpkg.WriteRuntimeConfig(configPath, next); err != nil {
		return fmt.Errorf("write %s runtime config: %w", p.Name, err)
	}

	fmt.Printf("Updated %s config in %s\n", p.Name, agent.DisplayPath(configPath))
	return nil
}

func resolveEditableRuntimeConfig(target string) (agent.Definition, string, error) {
	selected, err := agent.Select(target)
	if err != nil {
		return agent.Definition{}, "", err
	}

	p := agent.Resolve(selected[0])
	if !agent.SupportsPlatform(p, currentGOOS) {
		return agent.Definition{}, "", unsupportedPlatformError(p, currentGOOS)
	}
	if !supportsEditableRuntimeConfig(p) {
		return agent.Definition{}, "", fmt.Errorf("%s does not support config; its runtime config is not a managed gtrace.json file", p.Name)
	}

	configPath := agent.FirstExistingPath(p.ConfigFiles)
	if configPath == "" && len(p.ConfigFiles) > 0 {
		configPath = agent.ExpandHome(p.ConfigFiles[0])
	}
	if strings.TrimSpace(configPath) == "" {
		return agent.Definition{}, "", fmt.Errorf("%s config file was not found; expected one of: %s", p.Name, strings.Join(p.ConfigFiles, ", "))
	}

	if _, installed := agent.InstalledMarker(p); !installed && !agent.PathExists(configPath) {
		return agent.Definition{}, "", fmt.Errorf("%s plugin is not installed", p.Name)
	}
	return p, configPath, nil
}

func supportsEditableRuntimeConfig(p agent.Definition) bool {
	switch p.Name {
	case "hermes", "openclaw":
		return false
	}
	if len(p.ConfigFiles) == 0 {
		return false
	}
	for _, rawPath := range p.ConfigFiles {
		if strings.EqualFold(filepath.Base(rawPath), "gtrace.json") {
			return true
		}
	}
	return false
}

func runtimeConfigRows(current map[string]any) [][]string {
	headers := stringMapFromJSON(current["headers"])
	xToken := "-"
	if value := strings.TrimSpace(headers["X-Token"]); value != "" {
		xToken = "<configured>"
		delete(headers, "X-Token")
	}
	if xToken == "-" {
		if value, ok := current["x_token"].(string); ok && strings.TrimSpace(value) != "" {
			xToken = "<configured>"
		}
	}

	return [][]string{
		{"enabled", runtimeConfigBoolValue(current, "enabled")},
		{"endpoint", runtimeConfigStringValue(current, "endpoint", "base_url")},
		{"trace-path", runtimeConfigStringValue(current, "tracePath", "trace_path")},
		{"metrics-path", runtimeConfigStringValue(current, "metricsPath", "metrics_path")},
		{"x-token", xToken},
		{"header", runtimeConfigAssignments(headers)},
		{"tag", runtimeConfigAssignments(anyMapToStringMap(objectValue(current["resourceAttributes"])))},
		{"capture-content", runtimeConfigStringValue(current, "captureContent", "capture_content")},
		{"max-chars", runtimeConfigIntValue(current, "max_chars", "maxChars")},
	}
}

func runtimeConfigBoolValue(current map[string]any, key string) string {
	value, ok := current[key]
	if !ok {
		return "-"
	}
	switch currentValue := value.(type) {
	case bool:
		if currentValue {
			return "true"
		}
		return "false"
	case string:
		normalized := strings.ToLower(strings.TrimSpace(currentValue))
		if normalized != "" {
			return normalized
		}
	}
	return "-"
}

func runtimeConfigStringValue(current map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := current[key].(string)
		if ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "-"
}

func runtimeConfigIntValue(current map[string]any, keys ...string) string {
	for _, key := range keys {
		switch value := current[key].(type) {
		case int:
			if value > 0 {
				return strconv.Itoa(value)
			}
		case float64:
			if value > 0 {
				return strconv.Itoa(int(value))
			}
		case string:
			if strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}
	return "-"
}

func runtimeConfigAssignments(values map[string]string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(sortedMapEntries(values), ", ")
}

func anyMapToStringMap(values map[string]any) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		text := strings.TrimSpace(fmt.Sprint(value))
		if text == "" {
			continue
		}
		result[key] = text
	}
	return result
}

func objectValue(value any) map[string]any {
	out := map[string]any{}
	current, ok := value.(map[string]any)
	if !ok {
		return out
	}
	for key, item := range current {
		out[key] = item
	}
	return out
}
