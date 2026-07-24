package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func workBuddyPlugin() Definition {
	return Definition{
		Name:                     "workbuddy",
		PluginName:               "workbuddy-otel-plugin",
		AgentCommand:             "workbuddy",
		WindowsInstaller:         "install-release.ps1",
		PackageScript:            "scripts/install.sh",
		PackageArgs:              []string{"--refresh"},
		DiscoveryCommandOptional: true,
		Markers: []string{
			"~/.workbuddy/plugins/marketplaces/guance/plugins/workbuddy-otel-plugin",
		},
		ConfigFiles:     []string{"~/.workbuddy/gtrace.json"},
		EnabledJSONPath: []string{"enabled"},
		RemovePaths: []string{
			"~/.workbuddy/plugins/marketplaces/guance/plugins/workbuddy-otel-plugin",
		},
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
	resolved.Markers = []string{
		profileDir + "/plugins/marketplaces/guance/plugins/workbuddy-otel-plugin",
	}
	resolved.ConfigFiles = []string{profileDir + "/gtrace.json"}
	resolved.EnabledJSONPath = []string{"enabled"}
	resolved.RemovePaths = []string{
		profileDir + "/plugins/marketplaces/guance/plugins/workbuddy-otel-plugin",
	}
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
