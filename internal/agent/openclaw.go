package agent

import (
	"os"
	"os/exec"
	"path/filepath"
)

func openClawPlugin() Definition {
	return Definition{
		Name: "openclaw", Backend: BackendBuiltin, PluginName: "obs-agent-connector", AgentCommand: "openclaw",
		SupportedPlatforms: []string{"darwin", "linux", "windows"}, DiscoveryCommandOptional: true,
		ConfigFiles: []string{"~/.obs-agent-connector/openclaw/gtrace.json"}, EnabledJSONPath: []string{"enabled"},
		Markers: []string{"~/.obs-agent-connector/openclaw/plugin/runtime.json"},
		Resolve: resolveOpenClaw,
		ResolveRemove: func(d Definition) Definition {
			d = resolveOpenClaw(d)
			root := os.Getenv("OPENCLAW_STATE_DIR")
			if root == "" {
				root = ExpandHome("~/.openclaw")
			}
			d.RemovePaths = []string{filepath.Join(root, "extensions", "openclaw-otel-plugin"), filepath.Join(root, "plugins", "openclaw-otel-plugin")}
			d.Markers = append(d.Markers, d.RemovePaths...)
			return d
		},
		ResolveDiscovery: func(d Definition) (Definition, bool) {
			d = resolveOpenClaw(d)
			if PathExists(filepath.Dir(d.BuiltinHookFile)) {
				return d, true
			}
			_, err := exec.LookPath(d.AgentCommand)
			return d, err == nil
		},
	}
}
func resolveOpenClaw(d Definition) Definition {
	root := os.Getenv("OPENCLAW_STATE_DIR")
	if root == "" {
		root = ExpandHome("~/.openclaw")
	}
	config := os.Getenv("OPENCLAW_CONFIG_PATH")
	if config == "" {
		config = filepath.Join(root, "openclaw.json")
	}
	d.BuiltinHookFile = config
	d.Markers = []string{ExpandHome("~/.obs-agent-connector/openclaw/plugin/runtime.json")}
	return d
}
