package agent

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	ompbridge "github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/bridge"
)

func ompPlugin() Definition {
	return Definition{
		Name:             "omp",
		Backend:          BackendBuiltin,
		PluginName:       "obs-agent-connector",
		AgentCommand:     "omp",
		BuiltinHookFile:  "~/.omp/agent/extensions/obs-agent-connector.js",
		ConfigFiles:      []string{"~/.obs-agent-connector/omp/gtrace.json"},
		EnabledJSONPath:  []string{"enabled"},
		Resolve:          resolveOMP,
		ResolveInstall:   resolveOMPInstall,
		ResolveDiscovery: resolveOMPDiscovery,
		ResolveInstalled: installedOMP,
	}
}

func resolveOMP(p Definition) Definition {
	home, _ := os.UserHomeDir()
	p.BuiltinHookFile = ompbridge.ExtensionPath(home)
	p.Markers = []string{p.BuiltinHookFile}
	if command, err := exec.LookPath("omp"); err == nil {
		p.AgentCommand = command
	}
	return p
}

func resolveOMPInstall(p Definition) (Definition, error) {
	p = resolveOMP(p)
	if _, err := exec.LookPath(p.AgentCommand); err != nil {
		return Definition{}, fmt.Errorf("OMP was not found; install Oh My Pi before installing its adapter")
	}
	return p, nil
}

func resolveOMPDiscovery(p Definition) (Definition, bool) {
	p = resolveOMP(p)
	_, err := exec.LookPath(p.AgentCommand)
	return p, err == nil
}

func installedOMP(p Definition) (string, bool) {
	p = resolveOMP(p)
	body, err := os.ReadFile(p.BuiltinHookFile)
	if err != nil || !strings.HasPrefix(string(body), ompbridge.ExtensionMarker) {
		return "", false
	}
	return p.BuiltinHookFile, true
}

// OMPVersion enforces the first version used to verify the event contract.
func OMPVersion(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := exec.CommandContext(ctx, command, "--version").Output()
	if err != nil {
		return "", fmt.Errorf("read OMP version: %w", err)
	}
	match := regexp.MustCompile(`(?:^|\s)omp/([0-9]+)\.([0-9]+)\.([0-9]+)(?:\s|$)`).FindStringSubmatch(strings.TrimSpace(string(body)))
	if len(match) != 4 {
		return "", fmt.Errorf("could not determine OMP version; OMP 18.1.15 or later is required")
	}
	major, _ := strconv.Atoi(match[1])
	minor, _ := strconv.Atoi(match[2])
	patch, _ := strconv.Atoi(match[3])
	if major < 18 || (major == 18 && (minor < 1 || minor == 1 && patch < 15)) {
		return "", fmt.Errorf("OMP 18.1.15 or later is required")
	}
	return strings.Join(match[1:], "."), nil
}
