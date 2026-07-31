package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func opencodePlugin() Definition {
	return Definition{
		Name:                     "opencode",
		PluginName:               "opencode-otel-plugin",
		AgentCommand:             "opencode",
		WindowsInstaller:         "install-release.ps1",
		PackageScript:            "scripts/install.sh",
		DiscoveryCommandOptional: true,
		Markers: []string{
			"~/.config/opencode/plugins/opencode-otel-plugin",
		},
		ConfigFiles:     []string{"~/.config/opencode/gtrace.json"},
		EnabledJSONPath: []string{"enabled"},
		RemovePaths: []string{
			"~/.config/opencode/plugins/opencode-otel-plugin",
		},
		Resolve:          resolveOpencodePlugin,
		ResolveInstall:   resolveOpencodeForInstall,
		ResolveDiscovery: resolveOpencodeForDiscovery,
	}
}

func resolveOpencodePlugin(p Definition) Definition {
	if home := strings.TrimSpace(os.Getenv("OPENCODE_HOME")); home != "" {
		return withOpencodeHome(p, home)
	}
	return withOpencodeHome(p, "~/.config/opencode")
}

func resolveOpencodeForInstall(p Definition) (Definition, error) {
	return resolveOpencodePlugin(p), nil
}

func resolveOpencodeForDiscovery(p Definition) (Definition, bool) {
	resolved := resolveOpencodePlugin(p)
	if _, err := exec.LookPath(resolved.AgentCommand); err == nil {
		return resolved, true
	}
	if home, ok := detectExistingOpencodeHome(); ok {
		return withOpencodeHome(p, home), true
	}
	return Definition{}, false
}

func withOpencodeHome(p Definition, home string) Definition {
	resolved := p
	home = strings.TrimSpace(home)
	if home == "" {
		home = "~/.config/opencode"
	}
	resolved.Env = []string{"OPENCODE_HOME=" + home}
	resolved.Markers = []string{
		home + "/plugins/opencode-otel-plugin",
	}
	resolved.ConfigFiles = []string{home + "/gtrace.json"}
	resolved.EnabledJSONPath = []string{"enabled"}
	resolved.RemovePaths = []string{
		home + "/plugins/opencode-otel-plugin",
	}
	return resolved
}

func detectExistingOpencodeHome() (string, bool) {
	if value := strings.TrimSpace(os.Getenv("OPENCODE_HOME")); value != "" {
		if PathExists(ExpandHome(value)) {
			return value, true
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}
	configDir := filepath.Join(home, ".config", "opencode")
	if PathExists(configDir) {
		return "~/.config/opencode", true
	}
	return "", false
}
