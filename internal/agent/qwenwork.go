package agent

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/diagnostics"
)

func qwenWorkPlugin() Definition {
	return Definition{
		Name: "qwenwork-cn", Backend: BackendBuiltin, PluginName: "obs-agent-connector",
		AgentCommand: "qwenworkcn", SupportedPlatforms: []string{"darwin", "linux", "windows"},
		DiscoveryCommandOptional: true,
		BuiltinHookFile:          "~/.qwenworkcn/settings.json",
		Markers:                  []string{"~/.qwenworkcn/settings.json"},
		ConfigFiles:              []string{"~/.obs-agent-connector/qwenwork-cn/gtrace.json"},
		EnabledJSONPath:          []string{"enabled"},
		StatusDetails: func(p Definition) [][2]string {
			// Deliberately ignore the CLI environment: it is not the desktop's.
			cfg := config.Resolve(config.ResolveOptions{Env: map[string]string{}})
			return diagnostics.Rows(filepath.Dir(ExpandHome(p.BuiltinHookFile)), cfg.StateDir, time.Now())
		},
		ResolveInstall: func(p Definition) (Definition, error) {
			if !PathExists(ExpandHome("~/.qwenworkcn")) {
				return Definition{}, fmt.Errorf("QwenWork CN profile not found; start the domestic desktop client first")
			}
			return p, nil
		},
		ResolveDiscovery: func(p Definition) (Definition, bool) { return p, PathExists(ExpandHome("~/.qwenworkcn")) },
	}
}
