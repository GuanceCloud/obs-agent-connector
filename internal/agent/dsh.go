package agent

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func dshPlugin() Definition {
	return Definition{
		Name:                     "dsh",
		PluginName:               "dsh-otel-plugin",
		AgentCommand:             "dsh",
		WindowsInstaller:         "install-release.ps1",
		InstallArgs:              []string{"--profile", "web"},
		WindowsArgs:              []string{"-Profile", "web"},
		ConnectorManagedConfig:   true,
		DiscoveryCommandOptional: true,
		Markers: []string{
			"~/.dsh/profiles/web/node_modules/dsh-otel-plugin",
			"~/.dsh/profiles/node_modules/dsh-otel-plugin",
		},
		ConfigFiles:     []string{"~/.dsh/gtrace.json"},
		EnabledJSONPath: []string{"enabled"},
		RemoveCmds: [][]string{
			{"dsh", "plugin", "--profile", "web", "remove", "dsh-otel-plugin"},
		},
		RemovePaths: []string{
			"~/.dsh/profiles/web/node_modules/dsh-otel-plugin",
			"~/.dsh/profiles/node_modules/dsh-otel-plugin",
		},
		Resolve:          resolveDshPlugin,
		ResolveInstall:   resolveDshForInstall,
		ResolveDiscovery: resolveDshForDiscovery,
	}
}

func resolveDshPlugin(p Definition) Definition { return withDshProfile(p, dshProfile()) }

func resolveDshForInstall(p Definition) (Definition, error) { return resolveDshPlugin(p), nil }

func resolveDshForDiscovery(p Definition) (Definition, bool) {
	resolved := resolveDshPlugin(p)
	if _, err := exec.LookPath(resolved.AgentCommand); err == nil || PathExists(ExpandHome(dshHome())) {
		return resolved, true
	}
	return Definition{}, false
}

func withDshProfile(p Definition, profile string) Definition {
	resolved := p
	home := dshHome()
	if profile = strings.TrimSpace(profile); profile == "" {
		profile = "web"
	}
	profileRoot := filepath.ToSlash(filepath.Join(home, "profiles", profile))
	resolved.Env = []string{"DSH_HOME=" + home}
	resolved.PackageArgs = []string{"--profile", profile}
	resolved.InstallArgs = []string{"--profile", profile}
	resolved.WindowsArgs = []string{"-Profile", profile}
	resolved.RemoveCmds = [][]string{{"dsh", "plugin", "--profile", profile, "remove", "dsh-otel-plugin"}}
	resolved.Markers = []string{profileRoot + "/node_modules/dsh-otel-plugin", filepath.ToSlash(filepath.Join(home, "profiles", "node_modules", "dsh-otel-plugin"))}
	resolved.RemovePaths = append([]string{}, resolved.Markers...)
	resolved.ConfigFiles = []string{home + "/gtrace.json"}
	resolved.EnabledJSONPath = []string{"enabled"}
	return resolved
}

func dshHome() string {
	if value := strings.TrimSpace(os.Getenv("DSH_HOME")); value != "" {
		return value
	}
	return "~/.dsh"
}

func dshProfile() string {
	if value := strings.TrimSpace(os.Getenv("DSH_PROFILE")); value != "" {
		return value
	}
	return "web"
}
