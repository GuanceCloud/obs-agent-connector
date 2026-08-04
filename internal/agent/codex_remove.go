package agent

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

func resolveCodexRemove(p Definition) Definition {
	if command, ok := resolveCodexCommandPath(); ok {
		p.RemoveCmds = [][]string{
			{command, "plugin", "remove", "tracing@codex-otel-plugin"},
			{command, "plugin", "marketplace", "remove", "codex-otel-plugin"},
		}
		return p
	}
	p.RemoveCmds = nil
	return p
}

func resolveCodexCommandPath() (string, bool) {
	return resolveCodexCommandPathForOS(runtime.GOOS)
}

func resolveCodexCommandPathForOS(goos string) (string, bool) {
	candidates := []string{
		strings.TrimSpace(os.Getenv("CODEX_BINARY")),
		strings.TrimSpace(os.Getenv("CODEX_CLI_PATH")),
	}

	if goos == "windows" {
		candidates = append(candidates, windowsCodexCandidates()...)
	} else {
		candidates = append(candidates,
			"/Applications/ChatGPT.app/Contents/Resources/codex",
			"/Applications/Codex.app/Contents/Resources/codex",
			"/opt/homebrew/bin/codex",
			"/usr/local/bin/codex",
		)
		if home, err := os.UserHomeDir(); err == nil && home != "" {
			candidates = append(candidates,
				filepath.Join(home, ".local", "bin", "codex"),
				filepath.Join(home, "bin", "codex"),
			)
		}
	}

	if pathCommand, err := exec.LookPath("codex"); err == nil && strings.TrimSpace(pathCommand) != "" {
		if goos == "windows" {
			if native, ok := normalizeWindowsCodexPath(pathCommand); ok {
				candidates = append(candidates, native)
			}
		}
		candidates = append(candidates, pathCommand)
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}

	return "", false
}

func windowsCodexCandidates() []string {
	candidates := make([]string, 0, 16)

	if localAppData := strings.TrimSpace(os.Getenv("LOCALAPPDATA")); localAppData != "" {
		candidates = append(candidates,
			filepath.Join(localAppData, "OpenAI", "Codex", "bin", "codex.exe"),
			filepath.Join(localAppData, "Programs", "Codex", "codex.exe"),
			filepath.Join(localAppData, "Programs", "Codex", "bin", "codex.exe"),
			filepath.Join(localAppData, "Programs", "ChatGPT", "codex.exe"),
			filepath.Join(localAppData, "Programs", "ChatGPT", "resources", "codex.exe"),
		)
		candidates = append(candidates, windowsNPMNativeCandidates(filepath.Join(localAppData, "npm"))...)
	}
	if appData := strings.TrimSpace(os.Getenv("APPDATA")); appData != "" {
		candidates = append(candidates,
			filepath.Join(appData, "OpenAI", "Codex", "bin", "codex.exe"),
		)
		candidates = append(candidates, windowsNPMNativeCandidates(filepath.Join(appData, "npm"))...)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		candidates = append(candidates,
			filepath.Join(home, ".codex", "packages", "standalone", "current", "bin", "codex.exe"),
		)
	}

	return candidates
}

func normalizeWindowsCodexPath(pathCommand string) (string, bool) {
	pathCommand = strings.TrimSpace(pathCommand)
	if pathCommand == "" {
		return "", false
	}
	extension := strings.ToLower(filepath.Ext(pathCommand))
	if extension != ".cmd" && extension != ".bat" && extension != ".ps1" {
		return pathCommand, true
	}
	nativeCandidates := windowsNPMNativeCandidates(filepath.Dir(pathCommand))
	for _, candidate := range nativeCandidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, true
		}
	}
	return "", false
}

func windowsNPMNativeCandidates(prefix string) []string {
	prefix = strings.TrimSpace(prefix)
	if prefix == "" {
		return nil
	}

	platforms := [][2]string{
		{"codex-win32-x64", "x86_64-pc-windows-msvc"},
		{"codex-win32-arm64", "aarch64-pc-windows-msvc"},
	}
	if runtime.GOARCH == "arm64" {
		platforms[0], platforms[1] = platforms[1], platforms[0]
	}

	candidates := make([]string, 0, len(platforms)*4)
	for _, platform := range platforms {
		packageName := platform[0]
		target := platform[1]
		roots := []string{
			filepath.Join(prefix, "node_modules", "@openai", "codex", "node_modules", "@openai", packageName),
			filepath.Join(prefix, "node_modules", "@openai", packageName),
		}
		for _, root := range roots {
			vendor := filepath.Join(root, "vendor", target)
			candidates = append(candidates,
				filepath.Join(vendor, "bin", "codex.exe"),
				filepath.Join(vendor, "codex", "codex.exe"),
			)
		}
	}

	return candidates
}

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
