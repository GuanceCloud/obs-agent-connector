package agent

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

func claudeDesktopPlugin() Definition {
	return Definition{
		Name:                    desktop.Name,
		Backend:                 BackendBuiltin,
		PluginName:              desktop.PluginName,
		AgentCommand:            "Claude Desktop",
		BuiltinHookFile:         desktop.BundlePath("~"),
		SupportedPlatforms:      []string{"darwin"},
		ConfigFiles:             []string{agentfiles.ConfigPath("~", desktop.Name)},
		Markers:                 []string{desktop.ExecutablePath("~")},
		EnabledJSONPath:         []string{"enabled"},
		RequiresPluginImport:    true,
		ResolveDiscovery:        resolveClaudeDesktopDiscovery,
		ResolveInstalled:        claudeDesktopBundlePrepared,
		ResolveInstalledVersion: claudeDesktopBundleVersion,
	}
}

func resolveClaudeDesktopDiscovery(p Definition) (Definition, bool) {
	home, _ := os.UserHomeDir()
	bundle := filepath.Join("Claude.app", "Contents", "MacOS", "Claude")
	p.AgentCommand = FirstExistingPath([]string{
		filepath.Join(home, "Applications", bundle),
		filepath.Join("/Applications", bundle),
	})
	return p, p.AgentCommand != ""
}

func claudeDesktopBundlePrepared(p Definition) (string, bool) {
	path := ExpandHome(p.BuiltinHookFile)
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}
	return path, true
}

func claudeDesktopBundleVersion(p Definition) string {
	path, prepared := claudeDesktopBundlePrepared(p)
	if !prepared {
		return ""
	}
	bundle, err := zip.OpenReader(path)
	if err != nil {
		return ""
	}
	defer bundle.Close()
	for _, entry := range bundle.File {
		if entry.Name != ".claude-plugin/plugin.json" {
			continue
		}
		file, err := entry.Open()
		if err != nil {
			return ""
		}
		defer file.Close()
		body, err := io.ReadAll(io.LimitReader(file, 64*1024))
		if err != nil {
			return ""
		}
		version, _ := versionFromJSON(body)
		return version
	}
	return ""
}
