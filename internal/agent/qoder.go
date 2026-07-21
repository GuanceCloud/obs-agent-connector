package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func qoderPlugin() Definition {
	return Definition{
		Name:             "qoder",
		PluginName:       "qoder-otel-plugin",
		AgentCommand:     "qoder",
		WindowsInstaller: "https://github.com/GuanceCloud/qoder-otel-plugin/releases/latest/download/install-release.ps1",
		Markers: []string{
			"~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-probe",
			"~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-plugin",
		},
		ConfigFiles:     []string{"~/.qoder/gtrace.json"},
		EnabledJSONPath: []string{"enabled"},
		RemovePaths: []string{
			"~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-probe",
			"~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-plugin",
		},
		Resolve:          resolveQoderPlugin,
		ResolveInstall:   resolveQoderForInstall,
		ResolveDiscovery: resolveQoderForDiscovery,
	}
}

func qoderCNPlugin() Definition {
	return Definition{
		Name:             "qoder-cn",
		PluginName:       "qoder-otel-plugin",
		AgentCommand:     "qoder-cn",
		WindowsInstaller: "https://github.com/GuanceCloud/qoder-otel-plugin/releases/latest/download/install-release.ps1",
		Markers: []string{
			"~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-probe",
			"~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-plugin",
		},
		ConfigFiles:     []string{"~/.qoder-cn/gtrace.json"},
		EnabledJSONPath: []string{"enabled"},
		RemovePaths: []string{
			"~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-probe",
			"~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-plugin",
		},
		Hidden:           true,
		Resolve:          resolveQoderPlugin,
		ResolveInstall:   resolveQoderForInstall,
		ResolveDiscovery: resolveQoderForDiscovery,
	}
}

func resolveQoderPlugin(p Definition) Definition {
	switch p.Name {
	case "qoder":
		return withQoderVariant(p, detectQoderVariant("auto"))
	case "qoder-cn":
		return withQoderVariant(p, detectQoderVariant("cn"))
	default:
		return p
	}
}

func resolveQoderForInstall(p Definition) (Definition, error) {
	variant, ok := detectExistingQoderVariant()
	if !ok {
		return Definition{}, fmt.Errorf("qoder Agent data directory was not found; start Qoder before installing its plugin")
	}
	if p.Name == "qoder-cn" && variant != "cn" {
		return Definition{}, fmt.Errorf("qoder-cn Agent data directory was not found: ~/.qoder-cn")
	}
	return withQoderVariant(p, variant), nil
}

func resolveQoderForDiscovery(p Definition) (Definition, bool) {
	variant, ok := detectExistingQoderVariant()
	if !ok {
		return Definition{}, false
	}
	return withQoderVariant(p, variant), true
}

func withQoderVariant(p Definition, variant string) Definition {
	resolved := p
	home := "~/.qoder"
	if variant == "cn" {
		home = "~/.qoder-cn"
		resolved.AgentCommand = "qoder-cn"
	} else {
		resolved.AgentCommand = "qoder"
	}
	resolved.Env = []string{"QODER_HOME=" + home}
	resolved.InstallArgs = []string{"--variant", variant}
	resolved.Markers = []string{
		home + "/plugins/cache/qoder-marketplace/qoder-otel-probe",
		home + "/plugins/cache/qoder-marketplace/qoder-otel-plugin",
	}
	resolved.ConfigFiles = []string{home + "/gtrace.json"}
	resolved.EnabledJSONPath = []string{"enabled"}
	resolved.RemovePaths = []string{
		home + "/plugins/cache/qoder-marketplace/qoder-otel-probe",
		home + "/plugins/cache/qoder-marketplace/qoder-otel-plugin",
	}
	return resolved
}

func detectQoderVariant(fallback string) string {
	if variant, ok := detectExistingQoderVariant(); ok {
		return variant
	}

	if strings.EqualFold(fallback, "global") {
		return "global"
	}
	return "cn"
}

func detectExistingQoderVariant() (string, bool) {
	qoderHome := strings.TrimSpace(os.Getenv("QODER_HOME"))
	if qoderHome != "" && PathExists(ExpandHome(qoderHome)) {
		base := strings.ToLower(filepath.Base(qoderHome))
		switch base {
		case ".qoder-cn", "qoder-cn":
			return "cn", true
		case ".qoder", "qoder":
			return "global", true
		}
	}

	home, err := os.UserHomeDir()
	if err == nil {
		hasGlobal := PathExists(filepath.Join(home, ".qoder"))
		hasCN := PathExists(filepath.Join(home, ".qoder-cn"))
		switch {
		case hasGlobal && !hasCN:
			return "global", true
		case hasCN:
			return "cn", true
		}
	}
	return "", false
}
