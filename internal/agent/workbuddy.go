package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func workBuddyPlugin() Definition {
	return Definition{
		Name:                     "workbuddy",
		Backend:                  BackendBuiltin,
		BuiltinHookFile:          "~/.workbuddy/settings.json",
		PluginName:               "obs-agent-connector",
		AgentCommand:             "workbuddy",
		SupportedPlatforms:       []string{"darwin", "windows"},
		DiscoveryCommandOptional: true,
		Markers: []string{
			"~/.workbuddy/settings.json",
		},
		ConfigFiles:      []string{"~/.obs-agent-connector/workbuddy/gtrace.json", "~/.workbuddy/gtrace.json"},
		EnabledJSONPath:  []string{"enabled"},
		Resolve:          resolveWorkBuddyPlugin,
		ResolveInstall:   resolveWorkBuddyForInstall,
		ResolveDiscovery: resolveWorkBuddyForDiscovery,
	}
}

func resolveWorkBuddyPlugin(p Definition) Definition {
	if profileDir, ok := detectExistingWorkBuddyConfigDir(); ok {
		return withWorkBuddyProfile(p, profileDir)
	}
	return withWorkBuddyProfile(p, "~/.workbuddy")
}

func resolveWorkBuddyForInstall(p Definition) (Definition, error) {
	profileDir, ok := detectExistingWorkBuddyConfigDir()
	if !ok {
		return Definition{}, fmt.Errorf("workbuddy profile directory was not found; start WorkBuddy before installing its plugin")
	}
	return withWorkBuddyProfile(p, profileDir), nil
}

func resolveWorkBuddyForDiscovery(p Definition) (Definition, bool) {
	profileDir, ok := detectExistingWorkBuddyConfigDir()
	if !ok {
		return Definition{}, false
	}
	return withWorkBuddyProfile(p, profileDir), true
}

func withWorkBuddyProfile(p Definition, profileDir string) Definition {
	resolved := p
	profileDir = strings.TrimSpace(profileDir)
	if profileDir == "" {
		profileDir = "~/.workbuddy"
	}
	resolved.Env = []string{"WORKBUDDY_CONFIG_DIR=" + profileDir}
	resolved.BuiltinHookFile = profileDir + "/settings.json"
	resolved.Markers = []string{resolved.BuiltinHookFile}
	// Retain migration evidence only while the legacy plugin is registered.
	var settings struct {
		EnabledPlugins map[string]bool `json:"enabledPlugins"`
	}
	if body, err := os.ReadFile(ExpandHome(resolved.BuiltinHookFile)); err == nil && json.Unmarshal(body, &settings) == nil && settings.EnabledPlugins["workbuddy-otel-plugin@guance"] {
		resolved.Markers = append(resolved.Markers, profileDir+"/plugins/marketplaces/guance/plugins/workbuddy-otel-plugin")
	}
	resolved.ConfigFiles = []string{"~/.obs-agent-connector/workbuddy/gtrace.json", profileDir + "/gtrace.json"}
	resolved.EnabledJSONPath = []string{"enabled"}
	return resolved
}

func detectExistingWorkBuddyConfigDir() (string, bool) {
	for _, value := range []string{
		strings.TrimSpace(os.Getenv("WORKBUDDY_CONFIG_DIR")),
		strings.TrimSpace(os.Getenv("CODEBUDDY_CONFIG_DIR")),
	} {
		if value == "" {
			continue
		}
		expanded := ExpandHome(value)
		if PathExists(expanded) {
			return value, true
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	profileDir := filepath.Join(home, ".workbuddy")
	if PathExists(profileDir) {
		return "~/.workbuddy", true
	}
	return "", false
}
