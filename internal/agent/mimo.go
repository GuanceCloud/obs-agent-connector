package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

// MiMo shares the OpenCode plugin package, but has independent registration,
// runtime configuration and discovery roots.
func mimoPlugin() Definition {
	return Definition{
		Name: "mimo", PluginName: "opencode-otel-plugin", AgentCommand: "mimo",
		WindowsInstaller: "install-release.ps1", PackageScript: "scripts/install.sh",
		InstallArgs: []string{"--variant", "mimo"}, WindowsArgs: []string{"-Variant", "mimo"},
		DiscoveryCommandOptional: true, EnabledJSONPath: []string{"enabled"},
		Resolve: resolveMimoPlugin, ResolveInstall: resolveMimoForInstall,
		ResolveDiscovery:     resolveMimoForDiscovery,
		RemoveCleanup:        removeMimoRegistration,
		RemoveCleanupDetails: []string{"unregister MiMo telemetry plugin while preserving unrelated settings"},
	}
}

func mimoPaths() (config, data string, err error) {
	// Match MiMo's resolveMimocodeHome: relative overrides are invalid, even
	// when another valid XDG or OpenCode directory exists.
	if root := os.Getenv("MIMOCODE_HOME"); root != "" {
		if !filepath.IsAbs(root) {
			return "", "", fmt.Errorf("MIMOCODE_HOME must be an absolute path")
		}
		return filepath.Join(root, "config"), filepath.Join(root, "data"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}
	config = os.Getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(config) {
		config = filepath.Join(home, ".config")
	}
	data = os.Getenv("XDG_DATA_HOME")
	if !filepath.IsAbs(data) {
		data = filepath.Join(home, ".local", "share")
	}
	return filepath.Join(config, "mimocode"), filepath.Join(data, "mimocode"), nil
}

func resolveMimoForInstall(p Definition) (Definition, error) {
	config, _, err := mimoPaths()
	if err != nil {
		return Definition{}, err
	}
	p.Env = []string{"MIMOCODE_CONFIG_DIR=" + config}
	p.Markers = []string{filepath.Join(config, "plugins", "opencode-otel-plugin")}
	p.ConfigFiles = []string{filepath.Join(config, "gtrace.json")}
	p.RemovePaths = append([]string{}, p.Markers...)
	return p, nil
}

func resolveMimoPlugin(p Definition) Definition {
	resolved, err := resolveMimoForInstall(p)
	if err != nil {
		return p
	}
	return resolved
}

func resolveMimoForDiscovery(p Definition) (Definition, bool) {
	config, data, err := mimoPaths()
	if err != nil {
		return Definition{}, false
	}
	resolved := resolveMimoPlugin(p)
	if _, err := exec.LookPath(p.AgentCommand); err == nil {
		return resolved, true
	}
	for _, path := range []string{config, data} {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			return resolved, true
		}
	}
	return Definition{}, false
}

func removeMimoRegistration(p Definition) error {
	// The plugin owns JSON/JSONC editing; stop on failure before deleting files
	// so that a dangling registration can still be repaired on the next attempt.
	if len(p.Markers) == 0 {
		return fmt.Errorf("cannot resolve MiMo plugin directory; check MIMOCODE_HOME")
	}
	dir := p.Markers[0]
	if !PathExists(dir) {
		return nil
	}
	script := filepath.Join(dir, "scripts", "install-config.mjs")
	if !PathExists(script) {
		return fmt.Errorf("MiMo plugin unregistration helper is missing: %s; update to a host-aware plugin before removal", script)
	}
	cmd := exec.Command("node", script, "remove-mimo-config", filepath.Dir(p.ConfigFiles[0]))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("unregister MiMo plugin: %w", err)
	}
	return nil
}
