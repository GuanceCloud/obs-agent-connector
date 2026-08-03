package agent

import (
	"encoding/json"
	"os"
	"strings"
)

func removeCodexRegistration(p Definition) error {
	configFile := ExpandHome("~/.codex/config.toml")
	hooksFile := ExpandHome("~/.codex/hooks.json")

	if err := removeCodexConfigSections(configFile, hooksFile); err != nil {
		return err
	}
	if err := removeCodexHooks(hooksFile); err != nil {
		return err
	}
	return nil
}

func removeCodexConfigSections(configFile, hooksFile string) error {
	body, err := os.ReadFile(configFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	current := string(body)
	current = removeTOMLSection(current, `[marketplaces.codex-otel-plugin]`)
	current = removeTOMLSection(current, `[plugins."tracing@codex-otel-plugin"]`)
	current = removeTOMLSection(current, `[plugins."tracing@codex-observability-plugin"]`)
	if hooksFile != "" {
		prefix := `[hooks.state."` + strings.ReplaceAll(hooksFile, `\`, `\\`) + `:`
		current = removeTOMLSectionsMatching(current, func(header string) bool {
			return strings.HasPrefix(header, prefix)
		})
	}

	current = strings.TrimRight(current, "\n\t ")
	if current != "" {
		current += "\n"
	}
	return os.WriteFile(configFile, []byte(current), 0o644)
}

func removeCodexHooks(hooksFile string) error {
	body, err := os.ReadFile(hooksFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var config map[string]any
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(trimmed), &config); err != nil {
		return err
	}

	hooksValue, ok := config["hooks"].(map[string]any)
	if !ok || hooksValue == nil {
		return nil
	}

	groups, ok := hooksValue["Stop"].([]any)
	if !ok {
		return nil
	}

	stopGroups := make([]any, 0, len(groups))
	for _, group := range groups {
		groupMap, ok := group.(map[string]any)
		if !ok {
			stopGroups = append(stopGroups, group)
			continue
		}
		handlers, _ := groupMap["hooks"].([]any)
		remove := false
		for _, handler := range handlers {
			handlerMap, ok := handler.(map[string]any)
			if !ok {
				continue
			}
			command, _ := handlerMap["command"].(string)
			if isCodexGTraceHookCommand(command) {
				remove = true
				break
			}
		}
		if !remove {
			stopGroups = append(stopGroups, groupMap)
		}
	}
	hooksValue["Stop"] = stopGroups

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(hooksFile, data, 0o644)
}

func isCodexGTraceHookCommand(command string) bool {
	command = strings.ReplaceAll(command, `\`, `/`)
	return strings.Contains(command, "codex-hook-wrapper.js") ||
		strings.Contains(command, "codex-otel-plugin") ||
		strings.Contains(command, "codex-observability-plugin") ||
		strings.Contains(command, "/codex-hook") ||
		strings.Contains(command, " codex-hook")
}

func removeTOMLSection(source, header string) string {
	return removeTOMLSectionsMatching(source, func(value string) bool {
		return strings.TrimSpace(value) == strings.TrimSpace(header)
	})
}

func removeTOMLSectionsMatching(source string, predicate func(string) bool) string {
	if strings.TrimSpace(source) == "" {
		return source
	}

	lines := strings.Split(source, "\n")
	out := make([]string, 0, len(lines))
	skip := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			skip = predicate(trimmed)
		}
		if skip {
			continue
		}
		out = append(out, line)
	}
	return strings.Join(out, "\n")
}
