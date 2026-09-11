package install

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	ompbridge "github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/bridge"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

// OMP uses a native JS extension as a bridge to the built-in Go collector.
// CodexOptions is the existing shared runtime configuration merge contract.
func InstallOMP(options CodexOptions) (CodexResult, error) {
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
	extension := ompbridge.ExtensionPath(home)
	configFile := agentfiles.ConfigPath(home, "omp")
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
	if body, err := os.ReadFile(extension); err == nil && !strings.HasPrefix(string(body), ompbridge.ExtensionMarker) {
		return CodexResult{}, fmt.Errorf("OMP extension path already contains an unmanaged file: %s", extension)
	} else if err != nil && !os.IsNotExist(err) {
		return CodexResult{}, err
	}
	exeJSON, _ := json.Marshal(executable)
	cfgJSON, _ := json.Marshal(configFile)
	body := strings.ReplaceAll(ompbridge.Extension, "__CONNECTOR_EXECUTABLE__", string(exeJSON))
	body = strings.ReplaceAll(body, "__CONNECTOR_CONFIG__", string(cfgJSON))
	if err := os.MkdirAll(filepath.Dir(extension), 0o700); err != nil {
		return CodexResult{}, err
	}
	file, err := os.CreateTemp(filepath.Dir(extension), ".obs-agent-connector-*")
	if err != nil {
		return CodexResult{}, err
	}
	temp := file.Name()
	defer os.Remove(temp)
	if _, err = file.WriteString(body); err != nil {
		file.Close()
		return CodexResult{}, err
	}
	if err = file.Close(); err != nil {
		return CodexResult{}, err
	}
	if err = os.Rename(temp, extension); err != nil {
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

func removeOMP(home string, options RemoveOptions) (RemoveResult, error) {
	result := RemoveResult{Adapter: "omp", HookFile: ompbridge.ExtensionPath(home), ConfigFile: agentfiles.ConfigPath(home, "omp")}
	body, err := os.ReadFile(result.HookFile)
	if err == nil {
		if !strings.HasPrefix(string(body), ompbridge.ExtensionMarker) {
			return result, fmt.Errorf("refusing to remove unmanaged OMP extension: %s", result.HookFile)
		}
		if err := os.Remove(result.HookFile); err != nil {
			return result, err
		}
		result.HookRemoved = true
	} else if !os.IsNotExist(err) {
		return result, err
	}
	if options.PurgeConfig {
		if err := removeConfigFiles(result.ConfigFile); err != nil {
			return result, err
		}
		result.ConfigRemoved = true
	}
	if options.PurgeState {
		if err := os.RemoveAll(filepath.Join(agentfiles.Directory(home, "omp"), "state")); err != nil {
			return result, err
		}
		if err := removeFileIfExists(agentfiles.HookLogPath(home, "omp")); err != nil {
			return result, err
		}
		result.StatePurged = true
	}
	return result, nil
}
