package desktop

import (
	"path/filepath"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

const (
	Name       = "claude-desktop"
	PluginName = "obs-claude-desktop"
)

func BundlePath(home string) string {
	return filepath.Join(agentfiles.Directory(home, Name), PluginName+".zip")
}

func ExecutablePath(home string) string {
	return filepath.Join(agentfiles.Directory(home, Name), "bin", "obs-agent-connector")
}
