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

	pibridge "github.com/GuanceCloud/obs-agent-connector/internal/adapters/pi/bridge"
)

const MinimumPiVersion = "0.85.1"

func piPlugin() Definition {
	return Definition{
		Name:             "pi",
		Backend:          BackendBuiltin,
		PluginName:       "obs-agent-connector",
		AgentCommand:     "pi",
		BuiltinHookFile:  "~/.obs-agent-connector/pi/extension.js",
		ConfigFiles:      []string{"~/.obs-agent-connector/pi/gtrace.json"},
		EnabledJSONPath:  []string{"enabled"},
		Resolve:          resolvePi,
		ResolveInstall:   resolvePiInstall,
		ResolveDiscovery: resolvePiDiscovery,
		ResolveInstalled: installedPi,
	}
}

func resolvePi(p Definition) Definition {
	home, _ := os.UserHomeDir()
	p.BuiltinHookFile = pibridge.ExtensionPath(home)
	p.Markers = []string{p.BuiltinHookFile}
	if command, err := exec.LookPath("pi"); err == nil {
		p.AgentCommand = command
	}
	return p
}

func resolvePiInstall(p Definition) (Definition, error) {
	p = resolvePi(p)
	if _, err := exec.LookPath(p.AgentCommand); err != nil {
		return Definition{}, fmt.Errorf("Pi was not found; install Pi Coding Agent before installing its adapter")
	}
	return p, nil
}

func resolvePiDiscovery(p Definition) (Definition, bool) {
	p = resolvePi(p)
	_, err := exec.LookPath(p.AgentCommand)
	return p, err == nil
}

func installedPi(p Definition) (string, bool) {
	p = resolvePi(p)
	body, err := os.ReadFile(p.BuiltinHookFile)
	if err != nil || !strings.HasPrefix(string(body), pibridge.ExtensionMarker) {
		return "", false
	}
	home, _ := os.UserHomeDir()
	registered, err := pibridge.IsRegistered(home)
	return p.BuiltinHookFile, err == nil && registered
}

func PiVersion(command string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	body, err := exec.CommandContext(ctx, command, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("read Pi version: %w", err)
	}
	match := regexp.MustCompile(`(?:^|\s)(?:pi/)?v?([0-9]+\.[0-9]+\.[0-9]+)(?:\s|$)`).FindStringSubmatch(strings.TrimSpace(string(body)))
	if len(match) != 2 {
		return "", fmt.Errorf("could not determine Pi version; Pi %s or later is required", MinimumPiVersion)
	}
	if comparePiVersions(match[1], MinimumPiVersion) < 0 {
		return "", fmt.Errorf("Pi %s is unsupported; Pi %s or later is required", match[1], MinimumPiVersion)
	}
	return match[1], nil
}

func comparePiVersions(left, right string) int {
	parse := func(value string) [3]int {
		var parts [3]int
		for index, item := range strings.SplitN(value, ".", 3) {
			parts[index], _ = strconv.Atoi(item)
		}
		return parts
	}
	a, b := parse(left), parse(right)
	for index := range a {
		if a[index] < b[index] {
			return -1
		}
		if a[index] > b[index] {
			return 1
		}
	}
	return 0
}
