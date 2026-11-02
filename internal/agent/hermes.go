package agent

import (
	"os"
	"path/filepath"
)

func hermesPlugin() Definition {
	return Definition{
		Name:                 "hermes",
		PluginName:           "hermes-otel-plugin",
		AgentCommand:         "hermes",
		GitHubUnixInstaller:  "install.sh",
		GitHubReleaseBaseEnv: "OSS_ENDPOINT",
		Markers: []string{
			"~/.hermes/plugins/hermes-otel-plugin",
		},
		ConfigFiles: []string{"~/.hermes/config.yaml"},
		RemovePaths: []string{
			"~/.hermes/plugins/hermes-otel-plugin",
		},
		Resolve: resolveHermesPlugin,
	}
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
