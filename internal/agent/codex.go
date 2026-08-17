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
		ConfigFiles:     []string{"~/.obs-agent-connector/codex/gtrace.json", "~/.codex/gtrace.json"},
		EnabledJSONPath: []string{"enabled"},
		RemovePaths: []string{
			"~/.codex/plugin-sources/codex-otel-plugin",
			"~/.codex/plugins/cache/codex-otel-plugin",
		},
		RemoveCleanupDetails: []string{
			"~/.codex/config.toml (remove marketplace and plugin registration)",
			"~/.codex/hooks.json (remove managed Stop hooks)",
		},
		RemoveCleanup:    removeCodexRegistration,
		ResolveInstall:   resolveCodexInstall,
		ResolveRemove:    resolveCodexRemove,
		ResolveDiscovery: resolveCodexForDiscovery,
	}
}

func resolveCodexInstall(p Definition) (Definition, error) {
	if command, ok := resolveCodexCommandPath(); ok {
		p.AgentCommand = command
	}
	return p, nil
}

func resolveCodexForDiscovery(p Definition) (Definition, bool) {
	if command, ok := resolveCodexCommandPath(); ok {
		p.AgentCommand = command
		return p, true
	}
	return Definition{}, false
}
