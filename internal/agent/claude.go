package agent

func claudePlugin() Definition {
	return Definition{
		Name:            "claude",
		Backend:         BackendBuiltin,
		BuiltinHookFile: "~/.claude/settings.json",
		PluginName:      "claude-otel-plugin",
		AgentCommand:    "claude",
		Markers: []string{
			"~/.claude/settings.json",
			"~/.claude/marketplaces/claude-otel-plugin-release",
			"~/.claude/plugins/cache/claude-otel-plugin",
		},
		ConfigFiles:     []string{"~/.obs-agent-connector/claude/gtrace.json", "~/.claude/gtrace.json"},
		EnabledJSONPath: []string{"enabled"},
		RemoveCmds: [][]string{
			{"claude", "plugin", "uninstall", "claude-otel-plugin@claude-otel-plugin"},
			{"claude", "plugin", "marketplace", "remove", "claude-otel-plugin"},
		},
		RemovePaths: []string{
			"~/.claude/marketplaces/claude-otel-plugin-release",
			"~/.claude/plugins/cache/claude-otel-plugin",
		},
	}
}
