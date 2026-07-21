package agent

func claudePlugin() Definition {
	return Definition{
		Name:         "claude",
		PluginName:   "claude-otel-plugin",
		AgentCommand: "claude",
		Markers: []string{
			"~/.claude/marketplaces/claude-otel-plugin-release",
			"~/.claude/plugins/cache/claude-otel-plugin",
		},
		ConfigFiles:     []string{"~/.claude/gtrace.json"},
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
