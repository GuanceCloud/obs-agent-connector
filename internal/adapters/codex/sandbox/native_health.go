package sandbox

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Inspect registration without rewriting files owned by installers or other
// integrations. The observer must not quietly masquerade as a working Trace
// integration when only its own lifecycle events are still arriving.
func nativeMissingHooks(codexHome string) ([]string, error) {
	file, err := os.Open(filepath.Join(codexHome, "hooks.json"))
	if err != nil {
		return nil, err
	}
	defer file.Close()
	body, err := io.ReadAll(io.LimitReader(file, 256*1024+1))
	if err != nil {
		return nil, err
	}
	if len(body) > 256*1024 {
		return nil, fmt.Errorf("hook configuration exceeds health-check byte limit")
	}
	var settings struct {
		Hooks map[string][]struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"hooks"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(body, &settings); err != nil {
		return nil, err
	}
	var missing []string
	for _, event := range []string{"Stop", "PreToolUse", "PostToolUse"} {
		found := false
		for _, group := range settings.Hooks[event] {
			for _, handler := range group.Hooks {
				fields := strings.Fields(handler.Command)
				if handler.Type != "command" || len(fields) < 3 || fields[len(fields)-2] != "hook" || fields[len(fields)-1] != "codex" {
					continue
				}
				executable := strings.Trim(strings.Join(fields[:len(fields)-2], " "), "\"'")
				name := filepath.Base(strings.ReplaceAll(executable, `\`, "/"))
				if name == "obs-agent-connector" || name == "obs-agent-connector.exe" {
					found = true
				}
			}
		}
		if !found {
			missing = append(missing, event)
		}
	}
	return missing, nil
}
