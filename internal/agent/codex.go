package agent

import (
	"fmt"
	"path/filepath"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

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
		Resolve:          resolveCodexPaths,
		ResolveInstall:   resolveCodexInstall,
		ResolveRemove:    resolveCodexRemove,
		ResolveDiscovery: resolveCodexForDiscovery,
	}
}

func resolveCodexPaths(p Definition) Definition {
	codexHome, configured := agentfiles.ConfiguredCodexHome()
	if !configured {
		return p
	}
	hooksFile := filepath.Join(codexHome, "hooks.json")
	p.BuiltinHookFile = hooksFile
	p.Markers = []string{
		hooksFile,
		filepath.Join(codexHome, "plugin-sources", "codex-otel-plugin", "plugins", "tracing"),
		filepath.Join(codexHome, "plugins", "cache", "codex-otel-plugin"),
	}
	p.ConfigFiles = []string{
		p.ConfigFiles[0],
		filepath.Join(codexHome, "gtrace.json"),
	}
	p.RemovePaths = []string{
		filepath.Join(codexHome, "plugin-sources", "codex-otel-plugin"),
		filepath.Join(codexHome, "plugins", "cache", "codex-otel-plugin"),
	}
	p.RemoveCleanupDetails = []string{
		fmt.Sprintf("%s (remove marketplace and plugin registration)", DisplayPath(filepath.Join(codexHome, "config.toml"))),
		fmt.Sprintf("%s (remove managed Stop hooks)", DisplayPath(hooksFile)),
	}
	return p
}

func resolveCodexInstall(p Definition) (Definition, error) {
	p = resolveCodexPaths(p)
	if command, ok := resolveCodexCommandPath(); ok {
		p.AgentCommand = command
	}
	return p, nil
}

func resolveCodexForDiscovery(p Definition) (Definition, bool) {
	p = resolveCodexPaths(p)
	if command, ok := resolveCodexCommandPath(); ok {
		p.AgentCommand = command
		return p, true
	}
	return Definition{}, false
}
