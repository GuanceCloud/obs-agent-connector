package bridge

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

//go:embed extension.js
var Extension string

const ExtensionMarker = "// obs-agent-connector: omp extension v1"

// AgentDirectory follows OMP's active agent-directory override. Named profiles
// can be targeted by setting PI_CODING_AGENT_DIR to their agent directory.
func AgentDirectory(home string) string {
	if value := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); value != "" {
		if strings.HasPrefix(value, "~/") {
			return filepath.Join(home, value[2:])
		}
		return value
	}
	return filepath.Join(home, ".omp", "agent")
}

func ExtensionPath(home string) string {
	return filepath.Join(AgentDirectory(home), "extensions", "obs-agent-connector.js")
}
