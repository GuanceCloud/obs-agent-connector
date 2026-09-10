package install

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/openclaw/bridge"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/openclaw/buildinfo"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

// OpenClawOptions shares the connector's common runtime configuration options.
type OpenClawOptions = CodexOptions

func OpenClawConfigPath(home string) string {
	if p := os.Getenv("OPENCLAW_CONFIG_PATH"); p != "" {
		return p
	}
	root := os.Getenv("OPENCLAW_STATE_DIR")
	if root == "" {
		root = filepath.Join(home, ".openclaw")
	}
	return filepath.Join(root, "openclaw.json")
}

func InstallOpenClaw(options OpenClawOptions) (CodexResult, error) {
	home := options.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return CodexResult{}, err
		}
	}
	hostFile := firstInstallPath(options.HooksFile, OpenClawConfigPath(home))
	cfgFile := firstInstallPath(options.ConfigFile, agentfiles.ConfigPath(home, "openclaw"))
	host, err := readJSONObject(hostFile)
	if err != nil {
		return CodexResult{}, fmt.Errorf("parse OpenClaw config (strict JSON required): %w", err)
	}
	plugins, err := openClawObject(host, "plugins")
	if err != nil {
		return CodexResult{}, err
	}
	entries, err := openClawObject(plugins, "entries")
	if err != nil {
		return CodexResult{}, err
	}
	load, err := openClawObject(plugins, "load")
	if err != nil {
		return CodexResult{}, err
	}
	paths, err := openClawArray(load, "paths")
	if err != nil {
		return CodexResult{}, err
	}
	allow, err := openClawArray(plugins, "allow")
	if err != nil {
		return CodexResult{}, err
	}
	current, exists, err := readJSONObjectIfExists(cfgFile)
	if err != nil {
		return CodexResult{}, err
	}
	legacy, _ := entries["openclaw-otel-plugin"].(map[string]any)
	migrated := false
	if !exists {
		if old, ok := legacy["config"].(map[string]any); ok {
			current = map[string]any{}
			for _, k := range []string{"endpoint", "tracePath", "metricsPath", "headers", "resourceAttributes", "enabled", "captureContent", "maxChars", "timeoutMs"} {
				if v, ok := old[k]; ok {
					current[k] = v
				}
			}
			attributes := objectValue(old["globalTags"])
			for key, value := range objectValue(current["resourceAttributes"]) {
				attributes[key] = value
			}
			if len(attributes) > 0 {
				current["resourceAttributes"] = attributes
			}
			if name, ok := old["serviceName"].(string); ok {
				attrs := objectValue(current["resourceAttributes"])
				if attrs == nil {
					attrs = map[string]any{}
				}
				attrs["service.name"] = name
				current["resourceAttributes"] = attrs
			}
			if enabled, ok := legacy["enabled"].(bool); ok && !enabled {
				current["enabled"] = false
			}
			migrated = true
		}
	}
	next := current
	writeConfig := migrated
	if !options.NoConfig && shouldConfigureGTrace(exists || migrated, options) {
		next, err = mergeCodexGTraceConfig(current, options, exists || migrated)
		if err != nil {
			return CodexResult{}, err
		}
		writeConfig = true
	}
	executable, err := InstallRuntime(home, options.SourceExecutable, options.DestinationExecutable)
	if err != nil {
		return CodexResult{}, err
	}
	dir := filepath.Join(agentfiles.Directory(home, "openclaw"), "plugin")
	if err = os.MkdirAll(dir, 0700); err != nil {
		return CodexResult{}, err
	}
	bridgeFile := filepath.Join(dir, "index.mjs")
	if _, statErr := os.Stat(bridgeFile); os.IsNotExist(statErr) {
		err = os.WriteFile(bridgeFile, bridge.Source, 0600)
	} else {
		err = writeTextAtomic(bridgeFile, bridge.Source, 0600)
	}
	if err != nil {
		return CodexResult{}, err
	}
	for name, value := range map[string]any{
		"runtime.json":         map[string]any{"command": executable, "args": []string{"hook", "openclaw"}, "configFile": cfgFile},
		"package.json":         map[string]any{"name": "obs-agent-connector-openclaw", "version": "0.0.0", "type": "module", "openclaw": map[string]any{"extensions": []string{"./index.mjs"}}},
		"openclaw.plugin.json": map[string]any{"id": bridge.PluginID, "name": "OBS Agent Connector", "version": buildinfo.Version, "configSchema": map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false}},
	} {
		if err = writeJSONAtomic(filepath.Join(dir, name), value); err != nil {
			return CodexResult{}, err
		}
	}
	found := false
	for _, p := range paths {
		if p == dir {
			found = true
		}
	}
	if !found {
		paths = append(paths, dir)
	}
	load["paths"] = paths
	// Extend an explicit allowlist. Leaving an absent allowlist absent preserves unrelated auto-discovered plugins.
	if _, ok := plugins["allow"]; ok {
		found = false
		for _, p := range allow {
			if p == bridge.PluginID {
				found = true
			}
		}
		if !found {
			plugins["allow"] = append(allow, bridge.PluginID)
		}
	}
	entry, err := openClawObject(entries, bridge.PluginID)
	if err != nil {
		return CodexResult{}, err
	}
	hooks, err := openClawObject(entry, "hooks")
	if err != nil {
		return CodexResult{}, err
	}
	hooks["allowConversationAccess"] = true
	entry["enabled"] = true
	if legacy != nil {
		legacy["enabled"] = false
	}
	if native, ok := entries["diagnostics-otel"].(map[string]any); ok {
		native["enabled"] = false
	}
	if err = writeJSONAtomic(hostFile, host); err != nil {
		return CodexResult{}, err
	}
	if writeConfig {
		if err = writeJSONAtomic(cfgFile, next); err != nil {
			return CodexResult{}, err
		}
	}
	return CodexResult{Executable: executable, HooksFile: hostFile, ConfigFile: cfgFile, Configured: writeConfig}, nil
}

func openClawObject(parent map[string]any, key string) (map[string]any, error) {
	if v, ok := parent[key]; ok {
		if m, ok := v.(map[string]any); ok {
			return m, nil
		}
		return nil, fmt.Errorf("OpenClaw %s must be an object", key)
	}
	m := map[string]any{}
	parent[key] = m
	return m, nil
}
func openClawArray(parent map[string]any, key string) ([]any, error) {
	v, exists := parent[key]
	if !exists {
		return nil, nil
	}
	a, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("OpenClaw %s must be an array", key)
	}
	for _, item := range a {
		if _, ok := item.(string); !ok {
			return nil, fmt.Errorf("OpenClaw %s must contain strings", key)
		}
	}
	return a, nil
}

func removeOpenClaw(home string, options RemoveOptions) (RemoveResult, error) {
	result := RemoveResult{Adapter: "openclaw", HookFile: OpenClawConfigPath(home), ConfigFile: agentfiles.ConfigPath(home, "openclaw")}
	host, exists, err := readJSONObjectIfExists(result.HookFile)
	if err != nil {
		return result, err
	}
	if exists {
		plugins, err := openClawObject(host, "plugins")
		if err != nil {
			return result, err
		}
		entries, err := openClawObject(plugins, "entries")
		if err != nil {
			return result, err
		}
		if _, ok := entries[bridge.PluginID]; ok {
			delete(entries, bridge.PluginID)
			result.HookRemoved = true
		}
		dir := filepath.Join(agentfiles.Directory(home, "openclaw"), "plugin")
		if load, ok := plugins["load"].(map[string]any); ok {
			if err = removeOpenClawItem(load, "paths", dir); err != nil {
				return result, err
			}
		}
		if err = removeOpenClawItem(plugins, "allow", bridge.PluginID); err != nil {
			return result, err
		}
		if options.PurgeConfig {
			if entry, ok := entries["openclaw-otel-plugin"].(map[string]any); ok {
				delete(entry, "config")
			}
		}
		if err = writeJSONAtomic(result.HookFile, host); err != nil {
			return result, err
		}
	}
	if err = os.RemoveAll(filepath.Join(agentfiles.Directory(home, "openclaw"), "plugin")); err != nil {
		return result, err
	}
	if options.PurgeConfig {
		if err = removeConfigFiles(result.ConfigFile, filepath.Join(home, ".openclaw", "gtrace.json")); err != nil {
			return result, err
		}
		result.ConfigRemoved = true
	}
	if options.PurgeState {
		if err = os.RemoveAll(filepath.Join(agentfiles.Directory(home, "openclaw"), "state")); err != nil {
			return result, err
		}
		if err = removeFileIfExists(agentfiles.HookLogPath(home, "openclaw")); err != nil {
			return result, err
		}
		result.StatePurged = true
	}
	return result, nil
}
func removeOpenClawItem(parent map[string]any, key, value string) error {
	items, err := openClawArray(parent, key)
	if err != nil {
		return err
	}
	if _, exists := parent[key]; !exists {
		return nil
	}
	next := []any{}
	for _, item := range items {
		if item != value {
			next = append(next, item)
		}
	}
	parent[key] = next
	return nil
}
