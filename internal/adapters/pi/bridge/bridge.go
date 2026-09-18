package bridge

import (
	_ "embed"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

//go:embed extension.js
var Extension string

const ExtensionMarker = "// obs-agent-connector: pi extension v1"

func AgentDirectory(home string) string {
	if value := strings.TrimSpace(os.Getenv("PI_CODING_AGENT_DIR")); value != "" {
		if strings.HasPrefix(value, "~/") {
			return filepath.Join(home, value[2:])
		}
		return value
	}
	return filepath.Join(home, ".pi", "agent")
}

func SettingsPath(home string) string { return filepath.Join(AgentDirectory(home), "settings.json") }

func ExtensionPath(home string) string {
	return filepath.Join(agentfiles.Directory(home, "pi"), "extension.js")
}

func IsRegistered(home string) (bool, error) {
	body, err := os.ReadFile(SettingsPath(home))
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var settings map[string]any
	if err := json.Unmarshal(body, &settings); err != nil {
		return false, err
	}
	extensions, _ := settings["extensions"].([]any)
	want := filepath.Clean(ExtensionPath(home))
	for _, value := range extensions {
		path, ok := value.(string)
		if ok && filepath.Clean(path) == want {
			return true, nil
		}
	}
	return false, nil
}
