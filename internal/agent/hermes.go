package agent

import (
	"os"
	"os/exec"
	"path/filepath"
)

func hermesPlugin() Definition {
	return Definition{
		Name:                     "hermes",
		PluginName:               "hermes-otel-plugin",
		AgentCommand:             "hermes",
		GitHubUnixInstaller:      "install.sh",
		GitHubReleaseBaseEnv:     "OSS_ENDPOINT",
		DiscoveryCommandOptional: true,
		Markers: []string{
			"~/.hermes/plugins/hermes-otel-plugin",
		},
		ConfigFiles: []string{"~/.hermes/config.yaml"},
		RemovePaths: []string{
			"~/.hermes/plugins/hermes-otel-plugin",
		},
		Resolve:          resolveHermesPlugin,
		ResolveDiscovery: resolveHermesForDiscovery,
	}
}

func resolveHermesForDiscovery(p Definition) (Definition, bool) {
	p = resolveHermesPlugin(p)
	if _, err := exec.LookPath(p.AgentCommand); err == nil {
		return p, true
	}
	// Services, shell wrappers and virtual environments can expose a different
	// PATH from the connector. Require profile or plugin evidence, not just an
	// environment variable or an empty home directory.
	for _, name := range p.ConfigFiles {
		if info, err := os.Stat(name); err == nil && info.Mode().IsRegular() {
			return p, true
		}
	}
	if _, ok := InstalledMarker(p); ok {
		return p, true
	}
	return Definition{}, false
}

func resolveHermesPlugin(p Definition) Definition {
	home := os.Getenv("HERMES_HOME")
	if home == "" {
		home = "~/.hermes"
	}
	home = ExpandHome(home)
	pluginDir := filepath.Join(home, "plugins", p.PluginName)
	p.Markers = []string{pluginDir}
	p.ConfigFiles = []string{filepath.Join(home, "config.yaml")}
	p.RemovePaths = []string{pluginDir}
	p.Env = []string{"HERMES_HOME=" + home}
	return p
}
