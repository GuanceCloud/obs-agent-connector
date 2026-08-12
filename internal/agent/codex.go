package agent

func codexPlugin() Definition {
	return Definition{
		Name:            "codex",
		Backend:         BackendBuiltin,
		BuiltinHookFile: "~/.codex/hooks.json",
		PluginName:      "codex-otel-plugin",
		AgentCommand:    "codex",
		Markers: []string{
			"~/.codex/hooks.json",
			"~/.codex/plugin-sources/codex-otel-plugin/plugins/tracing",
			"~/.codex/plugins/cache/codex-otel-plugin",
		},
		ConfigFiles:     []string{"~/.codex/gtrace.json"},
		EnabledJSONPath: []string{"enabled"},
		RemovePaths: []string{
			"~/.codex/plugin-sources/codex-otel-plugin",
			"~/.codex/plugins/cache/codex-otel-plugin",
		},
		RemoveCleanupDetails: []string{
			"~/.codex/config.toml (remove marketplace and plugin registration)",
			"~/.codex/hooks.json (remove managed Stop hooks)",
		},
		RemoveCleanup: removeCodexRegistration,
		ResolveRemove: resolveCodexRemove,
	}
}
