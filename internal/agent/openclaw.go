package agent

func openClawPlugin() Definition {
	return Definition{
		Name:             "openclaw",
		PluginName:       "openclaw-otel-plugin",
		AgentCommand:     "openclaw",
		WindowsInstaller: "install-release.ps1",
		WindowsArgs:      []string{"-Type", "gtrace"},
		Markers: []string{
			"~/.openclaw/extensions/openclaw-otel-plugin",
			"~/.openclaw/plugins/openclaw-otel-plugin",
		},
		ConfigFiles:     []string{"~/.openclaw/openclaw.json"},
		EnabledJSONPath: []string{"plugins", "entries", "openclaw-otel-plugin", "enabled"},
		RemovePaths: []string{
			"~/.openclaw/extensions/openclaw-otel-plugin",
			"~/.openclaw/plugins/openclaw-otel-plugin",
		},
	}
}
