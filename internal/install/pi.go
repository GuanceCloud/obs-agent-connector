package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	pibridge "github.com/GuanceCloud/obs-agent-connector/internal/adapters/pi/bridge"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

// InstallPi writes the connector-managed extension and registers its absolute
// path in Pi's global settings without replacing unrelated settings.
func InstallPi(options CodexOptions) (CodexResult, error) {
	home := options.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return CodexResult{}, err
		}
	}
	executable := options.SourceExecutable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return CodexResult{}, err
		}
	}
	executable, err := filepath.Abs(executable)
	if err != nil {
		return CodexResult{}, err
	}

	extension := pibridge.ExtensionPath(home)
	settingsFile := pibridge.SettingsPath(home)
	configFile := agentfiles.ConfigPath(home, "pi")
	current, exists, err := readJSONObjectIfExists(configFile)
	if err != nil {
		return CodexResult{}, err
	}
	var next map[string]any
	if !options.NoConfig {
		next, err = mergeCodexGTraceConfig(current, options, exists)
		if err != nil {
			return CodexResult{}, err
		}
	}
	if body, readErr := os.ReadFile(extension); readErr == nil && !strings.HasPrefix(string(body), pibridge.ExtensionMarker) {
		return CodexResult{}, fmt.Errorf("Pi extension path already contains an unmanaged file: %s", extension)
	} else if readErr != nil && !os.IsNotExist(readErr) {
		return CodexResult{}, readErr
	}
	settings, _, err := readJSONObjectIfExists(settingsFile)
	if err != nil {
		return CodexResult{}, fmt.Errorf("read Pi settings: %w", err)
	}
	extensions, err := piExtensions(settings)
	if err != nil {
		return CodexResult{}, err
	}
	if !containsPath(extensions, extension) {
		extensions = append(extensions, extension)
	}
	settings["extensions"] = extensions

	exeJSON, _ := json.Marshal(executable)
	cfgJSON, _ := json.Marshal(configFile)
	body := strings.ReplaceAll(pibridge.Extension, "__CONNECTOR_EXECUTABLE__", string(exeJSON))
	body = strings.ReplaceAll(body, "__CONNECTOR_CONFIG__", string(cfgJSON))
	if err := writeFileAtomic(extension, []byte(body)); err != nil {
		return CodexResult{}, err
	}
	if err := writeJSONAtomic(settingsFile, settings); err != nil {
		return CodexResult{}, err
	}
	result := CodexResult{Executable: executable, HooksFile: extension, ConfigFile: configFile}
	if !options.NoConfig {
		if err := writeJSONAtomic(configFile, next); err != nil {
			return result, err
		}
		result.Configured = true
	}
	return result, nil
}

func removePi(home string, options RemoveOptions) (RemoveResult, error) {
	result := RemoveResult{Adapter: "pi", HookFile: pibridge.ExtensionPath(home), ConfigFile: agentfiles.ConfigPath(home, "pi")}
	hookExists := false
	if body, readErr := os.ReadFile(result.HookFile); readErr == nil {
		if !strings.HasPrefix(string(body), pibridge.ExtensionMarker) {
			return result, fmt.Errorf("refusing to remove unmanaged Pi extension: %s", result.HookFile)
		}
		hookExists = true
	} else if !os.IsNotExist(readErr) {
		return result, readErr
	}

	settingsFile := pibridge.SettingsPath(home)
	settings, exists, err := readJSONObjectIfExists(settingsFile)
	if err != nil {
		return result, err
	}
	if exists {
		extensions, err := piExtensions(settings)
		if err != nil {
			return result, err
		}
		next := make([]any, 0, len(extensions))
		for _, value := range extensions {
			path, ok := value.(string)
			if ok && filepath.Clean(path) == filepath.Clean(result.HookFile) {
				result.HookRemoved = true
				continue
			}
			next = append(next, value)
		}
		if result.HookRemoved {
			settings["extensions"] = next
			if err := writeJSONAtomic(settingsFile, settings); err != nil {
				return result, err
			}
		}
	}
	if hookExists {
		if err := os.Remove(result.HookFile); err != nil {
			return result, err
		}
		result.HookRemoved = true
	}
	if options.PurgeConfig {
		if err := removeConfigFiles(result.ConfigFile); err != nil {
			return result, err
		}
		result.ConfigRemoved = true
	}
	if options.PurgeState {
		if err := os.RemoveAll(filepath.Join(agentfiles.Directory(home, "pi"), "state")); err != nil {
			return result, err
		}
		if err := removeFileIfExists(agentfiles.HookLogPath(home, "pi")); err != nil {
			return result, err
		}
		result.StatePurged = true
	}
	return result, nil
}

func piExtensions(settings map[string]any) ([]any, error) {
	value, exists := settings["extensions"]
	if !exists {
		return []any{}, nil
	}
	extensions, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("Pi settings extensions must be an array")
	}
	return extensions, nil
}

func containsPath(values []any, want string) bool {
	for _, value := range values {
		if path, ok := value.(string); ok && filepath.Clean(path) == filepath.Clean(want) {
			return true
		}
	}
	return false
}

func writeFileAtomic(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".obs-agent-connector-*")
	if err != nil {
		return err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(body); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(temp, path)
}
