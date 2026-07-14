package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	appName           = "obs-agent-connector"
	fixedType         = "gtrace"
	defaultStaticBase = "https://static.guance.com"
	configDirName     = ".obs-agent-connector"
	configFileName    = "config.json"
)

var version = "dev"

type plugin struct {
	Name         string
	PluginName   string
	AgentCommand string
	Env          []string
	InstallArgs  []string
	Markers      []string
	ConfigFiles  []string
	Dependencies []string
	RemoveCmds   [][]string
	RemovePaths  []string
}

type installInput struct {
	Endpoint  string
	XToken    string
	AgentID   string
	AgentName string
}

type checkResult struct {
	Level   string
	Target  string
	Check   string
	Message string
	Fix     string
}

type githubRelease struct {
	TagName string
	HTMLURL string
}

type connectorConfig struct {
	DownloadBaseURL string `json:"download_base_url"`
}

var plugins = map[string]plugin{
	"claude": {
		Name:         "claude",
		PluginName:   "claude-otel-plugin",
		AgentCommand: "claude",
		Markers: []string{
			"~/.claude/marketplaces/claude-otel-plugin-release",
			"~/.claude/plugins/cache/claude-otel-plugin",
		},
		ConfigFiles: []string{"~/.claude/gtrace.json"},
		Dependencies: []string{
			"python3",
		},
		RemoveCmds: [][]string{
			{"claude", "plugin", "uninstall", "claude-otel-plugin@claude-otel-plugin"},
			{"claude", "plugin", "marketplace", "remove", "claude-otel-plugin"},
		},
		RemovePaths: []string{
			"~/.claude/marketplaces/claude-otel-plugin-release",
			"~/.claude/plugins/cache/claude-otel-plugin",
		},
	},
	"codex": {
		Name:         "codex",
		PluginName:   "codex-otel-plugin",
		AgentCommand: "codex",
		Markers: []string{
			"~/.codex/plugin-sources/codex-otel-plugin/plugins/tracing",
			"~/.codex/plugins/cache/codex-otel-plugin",
		},
		ConfigFiles: []string{"~/.codex/gtrace.json"},
		Dependencies: []string{
			"node",
		},
		RemoveCmds: [][]string{
			{"codex", "plugin", "remove", "tracing@codex-otel-plugin"},
			{"codex", "plugin", "marketplace", "remove", "codex-otel-plugin"},
		},
		RemovePaths: []string{
			"~/.codex/plugin-sources/codex-otel-plugin",
			"~/.codex/plugins/cache/codex-otel-plugin",
		},
	},
	"hermes": {
		Name:         "hermes",
		PluginName:   "hermes-otel-plugin",
		AgentCommand: "hermes",
		Markers: []string{
			"~/.hermes/plugins/hermes-otel-plugin",
		},
		ConfigFiles: []string{"~/.hermes/config.yaml"},
		Dependencies: []string{
			"tar",
			"python3",
		},
		RemovePaths: []string{
			"~/.hermes/plugins/hermes-otel-plugin",
		},
	},
	"openclaw": {
		Name:         "openclaw",
		PluginName:   "openclaw-otel-plugin",
		AgentCommand: "openclaw",
		Markers: []string{
			"~/.openclaw/extensions/openclaw-otel-plugin",
			"~/.openclaw/plugins/openclaw-otel-plugin",
		},
		ConfigFiles: []string{"~/.openclaw/openclaw.json"},
		Dependencies: []string{
			"tar",
			"node",
			"npm",
		},
		RemovePaths: []string{
			"~/.openclaw/extensions/openclaw-otel-plugin",
			"~/.openclaw/plugins/openclaw-otel-plugin",
		},
	},
	"qoder": {
		Name:         "qoder",
		PluginName:   "qoder-otel-plugin",
		AgentCommand: "qoder",
		Markers: []string{
			"~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-probe",
			"~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-plugin",
		},
		ConfigFiles: []string{"~/.qoder/gtrace.json"},
		Dependencies: []string{
			"node",
		},
		RemovePaths: []string{
			"~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-probe",
			"~/.qoder/plugins/cache/qoder-marketplace/qoder-otel-plugin",
		},
	},
	"qoder-cn": {
		Name:         "qoder-cn",
		PluginName:   "qoder-otel-plugin",
		AgentCommand: "qoder-cn",
		Markers: []string{
			"~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-probe",
			"~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-plugin",
		},
		ConfigFiles: []string{"~/.qoder-cn/gtrace.json"},
		Dependencies: []string{
			"node",
		},
		RemovePaths: []string{
			"~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-probe",
			"~/.qoder-cn/plugins/cache/qoder-marketplace/qoder-otel-plugin",
		},
	},
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage()
		return nil
	}

	switch args[0] {
	case "list":
		return listPlugins()
	case "doctor":
		return doctor(args[1:])
	case "install":
		return install(args[1:])
	case "update":
		return update(args[1:])
	case "remove":
		return remove(args[1:])
	case "version":
		return showVersion(args[1:])
	case "-h", "--help", "help":
		printUsage()
		return nil
	default:
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func printUsage() {
	fmt.Printf(`%s

Usage:
  obs-agent-connector <command> [arguments]

Commands:
  list                  List installed Agent plugins
  doctor [agent]        Diagnose environment and plugin issues
  install <agent>       Install an Agent plugin
  update <agent>        Update one installed Agent plugin
  remove <agent>        Remove an Agent plugin
  version               Show version and check for updates
  version               Show current version and check for updates

Examples:
  obs-agent-connector install codex
  obs-agent-connector install qoder
  obs-agent-connector update codex
  obs-agent-connector remove codex
  obs-agent-connector version
  obs-agent-connector version

install prompts for:
  Endpoint
  X-Token
  Agent ID
  Agent Name

`, appName)
}

func listPlugins() error {
	rows := [][]string{}
	found := false
	for _, name := range pluginNames() {
		p := resolvedPlugin(plugins[name])
		installedAt, ok := installedMarker(p)
		if !ok {
			continue
		}
		found = true
		configPath := firstExistingPath(p.ConfigFiles)
		if configPath == "" {
			configPath = "-"
		}
		rows = append(rows, []string{p.Name, displayPath(configPath), displayPath(installedAt)})
	}
	if !found {
		fmt.Println("No installed plugins found.")
		return nil
	}

	printTable([]string{"AGENT", "CONFIG", "PATH"}, rows)
	return nil
}

func doctor(args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	verbose := fs.Bool("verbose", false, "Show all checks")
	online := fs.Bool("online", false, "Check whether remote installer scripts are reachable")
	staticBaseFlag := fs.String("static-base", "", "Installer script and plugin package base URL. Default: connector download source, then endpoint root domain")

	target := ""
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
		flagArgs = args[1:]
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized doctor arguments: %s", strings.Join(fs.Args(), " "))
	}

	selected, err := selectPlugins(target)
	if err != nil {
		return err
	}

	results := []checkResult{}
	results = append(results, checkCommand("system", "curl", "curl is required to download installer scripts", "Install curl and retry"))
	results = append(results, checkCommand("system", "bash", "bash is required to run plugin installer scripts", "Install bash and retry"))

	staticBase := staticBaseURL(*staticBaseFlag, "")
	explicitTarget := strings.TrimSpace(strings.ToLower(target)) != ""
	for _, p := range selected {
		p = resolvedPlugin(p)
		results = append(results, checkCommand(p.Name, p.AgentCommand, "Agent command was not found", "Install "+p.AgentCommand+" or check PATH"))
		results = append(results, checkPluginInstalled(p, explicitTarget))
		results = append(results, checkConfig(p))
		for _, dependency := range p.Dependencies {
			results = append(results, checkCommand(p.Name, dependency, "Plugin runtime dependency is missing", "Install "+dependency+" and retry"))
		}
		if *online {
			results = append(results, checkInstallerOnline(staticBase, p))
		}
	}

	rows := [][]string{}
	for _, result := range results {
		if !*verbose && result.Level == "OK" {
			continue
		}
		rows = append(rows, []string{result.Level, result.Target, result.Check, result.Message, result.Fix})
	}

	if len(rows) == 0 {
		fmt.Println("No issues found. Use --verbose to show all checks.")
		return nil
	}

	printTable([]string{"LEVEL", "TARGET", "CHECK", "MESSAGE", "FIX"}, rows)
	return nil
}

func install(args []string) error {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	endpoint := fs.String("endpoint", "", "GTrace endpoint")
	xToken := fs.String("x-token", "", "GTrace X-Token")
	agentID := fs.String("agent-id", "", "GTrace agent_id tag")
	agentName := fs.String("agent-name", "", "GTrace agent_name tag")
	staticBaseFlag := fs.String("static-base", "", "Installer script and plugin package base URL. Default: connector download source, then endpoint root domain")
	yes := fs.Bool("yes", false, "Skip confirmation")
	dryRun := fs.Bool("dry-run", false, "Print commands without installing")

	target := ""
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
		flagArgs = args[1:]
	}

	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized install arguments: %s", strings.Join(fs.Args(), " "))
	}
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("install requires a single agent, for example: install codex")
	}

	selected, err := selectPlugins(target)
	if err != nil {
		return err
	}

	input, err := promptInstallInput(installInput{
		Endpoint:  strings.TrimSpace(*endpoint),
		XToken:    strings.TrimSpace(*xToken),
		AgentID:   strings.TrimSpace(*agentID),
		AgentName: strings.TrimSpace(*agentName),
	})
	if err != nil {
		return err
	}

	staticBase := staticBaseURL(*staticBaseFlag, input.Endpoint)
	fmt.Println()
	fmt.Println("Install plan:")
	for _, p := range selected {
		p = resolvedPlugin(p)
		fmt.Printf("  - %s (%s)\n", p.Name, installerURL(staticBase, p))
	}
	fmt.Printf("OSS_ENDPOINT: %s\n", staticBase)
	fmt.Printf("Type: %s\n", fixedType)
	fmt.Printf("Endpoint: %s\n", input.Endpoint)
	fmt.Printf("X-Token: %s\n", input.XToken)
	fmt.Printf("Agent ID: %s\n", input.AgentID)
	fmt.Printf("Agent Name: %s\n", input.AgentName)

	if *dryRun {
		fmt.Println()
		fmt.Println("Command preview:")
		for _, p := range selected {
			p = resolvedPlugin(p)
			fmt.Println(renderInstallCommand(staticBase, p, input))
		}
		return nil
	}

	if !*yes {
		ok, err := confirm("Continue installation?", true)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Canceled.")
			return nil
		}
	}

	for _, p := range selected {
		p = resolvedPlugin(p)
		if err := installOne(staticBase, p, input); err != nil {
			return err
		}
	}

	return nil
}

func update(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("update requires a single agent, for example: update codex")
	}

	switch args[0] {
	case "cli", "self":
		return fmt.Errorf("update cli was removed and will move to the version command")
	case "plugin", "plugins", "agent", "agents":
		return updatePlugins(args[1:])
	default:
		return updatePlugins(args)
	}
}

func updatePlugins(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	staticBaseFlag := fs.String("static-base", "", "Installer script and plugin package base URL. Default: connector download source, then endpoint root domain")
	yes := fs.Bool("yes", false, "Skip confirmation")
	dryRun := fs.Bool("dry-run", false, "Print commands without updating")

	target := ""
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
		flagArgs = args[1:]
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized update arguments: %s", strings.Join(fs.Args(), " "))
	}

	selected, err := selectInstalledPlugins(target)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		fmt.Println("No installed plugin found to update.")
		return nil
	}

	staticBase := staticBaseURL(*staticBaseFlag, "")
	fmt.Println("Update plan. Configuration files will not be modified:")
	for _, p := range selected {
		p = resolvedPlugin(p)
		fmt.Printf("  - %s (%s)\n", p.Name, installerURL(staticBase, p))
	}

	if *dryRun {
		fmt.Println()
		fmt.Println("Command preview:")
		for _, p := range selected {
			p = resolvedPlugin(p)
			fmt.Println(renderPluginUpdateCommand(staticBase, p))
		}
		return nil
	}

	if !*yes {
		ok, err := confirm("Continue plugin update?", true)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Canceled.")
			return nil
		}
	}

	for _, p := range selected {
		p = resolvedPlugin(p)
		if err := updatePluginOne(staticBase, p); err != nil {
			return err
		}
	}
	return nil
}

func remove(args []string) error {
	fs := flag.NewFlagSet("remove", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	yes := fs.Bool("yes", false, "Skip confirmation")
	dryRun := fs.Bool("dry-run", false, "Print what would be removed")
	purgeConfig := fs.Bool("purge-config", false, "Also remove plugin configuration files")

	target := ""
	flagArgs := args
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
		flagArgs = args[1:]
	}
	if target == "" {
		return fmt.Errorf("remove requires an agent, for example: remove codex")
	}
	if err := fs.Parse(flagArgs); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized remove arguments: %s", strings.Join(fs.Args(), " "))
	}

	selected, err := selectInstalledPlugins(target)
	if err != nil {
		return err
	}
	if len(selected) == 0 {
		fmt.Println("No installed plugin found to remove.")
		return nil
	}

	fmt.Println("Remove plan:")
	for _, p := range selected {
		p = resolvedPlugin(p)
		fmt.Printf("  - %s\n", p.Name)
		for _, cmd := range p.RemoveCmds {
			fmt.Printf("    command: %s\n", strings.Join(cmd, " "))
		}
		for _, path := range p.RemovePaths {
			fmt.Printf("    path: %s\n", path)
		}
		if *purgeConfig {
			for _, path := range p.ConfigFiles {
				fmt.Printf("    config: %s\n", path)
			}
		}
	}
	if !*purgeConfig {
		fmt.Println("Configuration files will be kept. Use --purge-config to remove them.")
	}

	if *dryRun {
		return nil
	}

	if !*yes {
		ok, err := confirm("Continue removal?", false)
		if err != nil {
			return err
		}
		if !ok {
			fmt.Println("Canceled.")
			return nil
		}
	}

	for _, p := range selected {
		p = resolvedPlugin(p)
		if err := removeOne(p, *purgeConfig); err != nil {
			return err
		}
	}
	return nil
}

func showVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	offline := fs.Bool("offline", false, "Skip remote release check")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized version arguments: %s", strings.Join(fs.Args(), " "))
	}

	fmt.Printf("Version: %s\n", version)
	fmt.Printf("Platform: %s/%s\n", runtime.GOOS, runtime.GOARCH)

	cfg, cfgPath, cfgErr := loadConnectorConfig()
	if cfgErr == nil && cfgPath != "" && strings.TrimSpace(cfg.DownloadBaseURL) != "" {
		fmt.Printf("Download Base: %s\n", cfg.DownloadBaseURL)
	}

	if *offline {
		if cfgErr != nil {
			fmt.Printf("Config: unavailable (%v)\n", cfgErr)
		} else if cfgPath != "" {
			fmt.Printf("Config: %s\n", displayPath(cfgPath))
		}
		return nil
	}

	release, err := fetchLatestRelease(cfg)
	if err != nil {
		fmt.Printf("Latest release: unavailable (%v)\n", err)
		return nil
	}

	fmt.Printf("Latest release: %s\n", release.TagName)
	fmt.Printf("Release page: %s\n", release.HTMLURL)

	if version == release.TagName {
		fmt.Println("Status: up to date")
		return nil
	}

	comparison, ok := compareReleaseVersions(version, release.TagName)
	if ok && comparison >= 0 {
		fmt.Println("Status: up to date")
		return nil
	}

	fmt.Println("Status: update available")
	command, note, err := buildSelfUpdateCommand(release.TagName, cfg)
	if err != nil {
		fmt.Printf("Update command: unavailable (%v)\n", err)
		return nil
	}

	fmt.Println("Update command:")
	fmt.Println(command)
	if note != "" {
		fmt.Printf("Note: %s\n", note)
	}

	return nil
}

func selectPlugins(target string) ([]plugin, error) {
	target = strings.TrimSpace(strings.ToLower(target))
	if target == "" {
		names := pluginNames()
		out := make([]plugin, 0, len(names))
		for _, name := range names {
			out = append(out, plugins[name])
		}
		return out, nil
	}

	p, ok := plugins[target]
	if !ok {
		return nil, fmt.Errorf("unknown agent %q; available agents: %s", target, strings.Join(pluginNames(), ", "))
	}
	return []plugin{p}, nil
}

func selectInstalledPlugins(target string) ([]plugin, error) {
	normalizedTarget := strings.TrimSpace(strings.ToLower(target))
	selected, err := selectPlugins(target)
	if err != nil {
		return nil, err
	}

	out := make([]plugin, 0, len(selected))
	for _, p := range selected {
		p = resolvedPlugin(p)
		if _, ok := installedMarker(p); ok {
			out = append(out, p)
			continue
		}
		if normalizedTarget != "" {
			return nil, fmt.Errorf("%s plugin is not installed; cannot update it", p.Name)
		}
	}
	return out, nil
}

func checkCommand(target string, command string, missingMessage string, fix string) checkResult {
	if _, err := exec.LookPath(command); err == nil {
		return checkResult{
			Level:   "OK",
			Target:  target,
			Check:   command,
			Message: "found",
			Fix:     "-",
		}
	}
	return checkResult{
		Level:   "FAIL",
		Target:  target,
		Check:   command,
		Message: missingMessage,
		Fix:     fix,
	}
}

func checkPluginInstalled(p plugin, explicitTarget bool) checkResult {
	path, ok := installedMarker(p)
	if ok {
		return checkResult{
			Level:   "OK",
			Target:  p.Name,
			Check:   "plugin",
			Message: "installed: " + displayPath(path),
			Fix:     "-",
		}
	}

	level := "INFO"
	if explicitTarget {
		level = "WARN"
	}
	return checkResult{
		Level:   level,
		Target:  p.Name,
		Check:   "plugin",
		Message: "plugin is not installed",
		Fix:     "Run obs-agent-connector install " + p.Name,
	}
}

func checkConfig(p plugin) checkResult {
	path := firstExistingPath(p.ConfigFiles)
	if path != "" {
		return checkResult{
			Level:   "OK",
			Target:  p.Name,
			Check:   "config",
			Message: "found: " + displayPath(path),
			Fix:     "-",
		}
	}
	return checkResult{
		Level:   "WARN",
		Target:  p.Name,
		Check:   "config",
		Message: "configuration file was not found",
		Fix:     "Run obs-agent-connector install " + p.Name + " to generate configuration",
	}
}

func checkInstallerOnline(staticBase string, p plugin) checkResult {
	url := installerURL(staticBase, p)
	cmd := exec.Command("curl", "-fsSL", "--max-time", "8", "-o", os.DevNull, url)
	if err := cmd.Run(); err == nil {
		return checkResult{
			Level:   "OK",
			Target:  p.Name,
			Check:   "installer",
			Message: "reachable",
			Fix:     "-",
		}
	}
	return checkResult{
		Level:   "FAIL",
		Target:  p.Name,
		Check:   "installer",
		Message: "installer script is not reachable: " + url,
		Fix:     "Check network access or use --static-base",
	}
}

func fetchLatestRelease(cfg connectorConfig) (githubRelease, error) {
	metadataURL := latestMetadataURL(cfg)
	if metadataURL == "" {
		return githubRelease{}, fmt.Errorf("download_base_url is not configured")
	}

	client := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequest(http.MethodGet, metadataURL, nil)
	if err != nil {
		return githubRelease{}, err
	}
	req.Header.Set("User-Agent", appName)

	resp, err := client.Do(req)
	if err != nil {
		return githubRelease{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return githubRelease{}, fmt.Errorf("latest metadata is not reachable: %s", metadataURL)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return githubRelease{}, err
	}
	tag := strings.TrimSpace(string(body))
	release := githubRelease{TagName: tag}
	if release.TagName == "" {
		return githubRelease{}, fmt.Errorf("latest metadata is empty")
	}
	if release.HTMLURL == "" {
		release.HTMLURL = strings.TrimRight(cfg.DownloadBaseURL, "/") + "/"
	}
	return release, nil
}

func compareReleaseVersions(current string, latest string) (int, bool) {
	currentParts, ok := parseVersionParts(current)
	if !ok {
		return 0, false
	}
	latestParts, ok := parseVersionParts(latest)
	if !ok {
		return 0, false
	}

	maxLen := len(currentParts)
	if len(latestParts) > maxLen {
		maxLen = len(latestParts)
	}

	for i := 0; i < maxLen; i++ {
		currentValue := 0
		latestValue := 0
		if i < len(currentParts) {
			currentValue = currentParts[i]
		}
		if i < len(latestParts) {
			latestValue = latestParts[i]
		}
		switch {
		case currentValue < latestValue:
			return -1, true
		case currentValue > latestValue:
			return 1, true
		}
	}

	return 0, true
}

func parseVersionParts(value string) ([]int, bool) {
	trimmed := strings.TrimSpace(strings.TrimPrefix(value, "v"))
	if trimmed == "" || trimmed == "dev" {
		return nil, false
	}

	segments := strings.Split(trimmed, ".")
	parts := make([]int, 0, len(segments))
	for _, segment := range segments {
		if segment == "" {
			return nil, false
		}
		digits := takeLeadingDigits(segment)
		if digits == "" {
			return nil, false
		}
		number, err := strconv.Atoi(digits)
		if err != nil {
			return nil, false
		}
		parts = append(parts, number)
	}
	return parts, true
}

func takeLeadingDigits(value string) string {
	var builder strings.Builder
	for _, r := range value {
		if r < '0' || r > '9' {
			break
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func buildSelfUpdateCommand(tag string, cfg connectorConfig) (string, string, error) {
	if strings.TrimSpace(cfg.DownloadBaseURL) == "" {
		return "", "", fmt.Errorf("download_base_url is not configured")
	}

	executablePath, err := os.Executable()
	if err != nil {
		return "", "", err
	}
	executablePath, err = filepath.EvalSymlinks(executablePath)
	if err != nil {
		return "", "", err
	}

	packageName, binaryName, _, err := releaseAssetNames()
	if err != nil {
		return "", "", err
	}

	downloadURL := strings.TrimRight(cfg.DownloadBaseURL, "/") + "/" + packageName

	if runtime.GOOS == "windows" {
		command := fmt.Sprintf(
			"powershell -Command \"Invoke-WebRequest -Uri %s -OutFile $env:TEMP\\%s; Expand-Archive -Path $env:TEMP\\%s -DestinationPath $env:TEMP\\obs-agent-connector-update -Force; Copy-Item $env:TEMP\\obs-agent-connector-update\\%s %s -Force\"",
			windowsDoubleQuote(downloadURL),
			windowsDoubleQuote(packageName),
			windowsDoubleQuote(packageName),
			windowsDoubleQuote(binaryName),
			windowsDoubleQuote(executablePath),
		)
		return command, "", nil
	}

	archivePath := "/tmp/" + packageName
	extractedPath := "/tmp/" + binaryName
	command := fmt.Sprintf(
		"curl -fsSL -o %s %s && tar -xzf %s -C /tmp && install -m 0755 %s %s",
		shellQuote(archivePath),
		shellQuote(downloadURL),
		shellQuote(archivePath),
		shellQuote(extractedPath),
		shellQuote(executablePath),
	)

	note := "If the current executable path requires elevated permissions, prefix the final install step with sudo."
	return command, note, nil
}

func releaseAssetNames() (packageName string, binaryName string, extension string, err error) {
	switch runtime.GOOS {
	case "darwin", "linux":
		binaryName = fmt.Sprintf("%s-%s-%s", appName, runtime.GOOS, runtime.GOARCH)
		extension = ".tar.gz"
		return binaryName + extension, binaryName, extension, nil
	case "windows":
		binaryName = fmt.Sprintf("%s-%s-%s.exe", appName, runtime.GOOS, runtime.GOARCH)
		extension = ".zip"
		return strings.TrimSuffix(binaryName, ".exe") + extension, binaryName, extension, nil
	default:
		return "", "", "", fmt.Errorf("unsupported platform %s/%s", runtime.GOOS, runtime.GOARCH)
	}
}

func promptInstallInput(defaults installInput) (installInput, error) {
	reader := bufio.NewReader(os.Stdin)
	var err error
	input := defaults

	if input.Endpoint == "" {
		input.Endpoint, err = promptRequired(reader, "Endpoint")
		if err != nil {
			return input, err
		}
	}
	if input.XToken == "" {
		input.XToken, err = promptRequired(reader, "X-Token")
		if err != nil {
			return input, err
		}
	}
	if input.AgentID == "" {
		input.AgentID, err = promptRequired(reader, "Agent ID")
		if err != nil {
			return input, err
		}
	}
	if input.AgentName == "" {
		input.AgentName, err = promptRequired(reader, "Agent Name")
		if err != nil {
			return input, err
		}
	}

	return input, nil
}

func promptRequired(reader *bufio.Reader, label string) (string, error) {
	for {
		fmt.Printf("%s: ", label)
		value, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		value = strings.TrimSpace(value)
		if value != "" {
			return value, nil
		}
		fmt.Printf("%s cannot be empty.\n", label)
	}
}

func confirm(label string, defaultYes bool) (bool, error) {
	suffix := "y/N"
	if defaultYes {
		suffix = "Y/n"
	}
	fmt.Printf("%s [%s]: ", label, suffix)
	reader := bufio.NewReader(os.Stdin)
	value, err := reader.ReadString('\n')
	if err != nil {
		return false, err
	}
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return defaultYes, nil
	}
	switch value {
	case "y", "yes":
		return true, nil
	case "n", "no":
		return false, nil
	default:
		return false, fmt.Errorf("invalid confirmation input %q", value)
	}
}

func installOne(staticBase string, p plugin, input installInput) error {
	fmt.Printf("\n==> Installing %s\n", p.Name)

	scriptPath := tempScriptPath(p)
	url := installerURL(staticBase, p)
	fmt.Printf("Downloading installer: %s\n", url)

	if err := downloadFile(url, scriptPath); err != nil {
		return fmt.Errorf("failed to download %s installer: %w", p.Name, err)
	}

	args := buildInstallArgs(scriptPath, p, input)
	fmt.Printf("Running: %s\n", renderBashCommand(args))

	cmd := exec.Command("bash", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), pluginEnv(staticBase, p)...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s installation failed: %w", p.Name, err)
	}

	fmt.Printf("%s installed.\n", p.Name)
	return nil
}

func updatePluginOne(staticBase string, p plugin) error {
	fmt.Printf("\n==> Updating %s\n", p.Name)

	scriptPath := tempScriptPath(p)
	url := installerURL(staticBase, p)
	fmt.Printf("Downloading installer: %s\n", url)

	if err := downloadFile(url, scriptPath); err != nil {
		return fmt.Errorf("failed to download %s installer: %w", p.Name, err)
	}

	args := buildPluginUpdateArgs(scriptPath, p)
	fmt.Printf("Running: %s\n", renderBashCommand(args))

	cmd := exec.Command("bash", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), pluginEnv(staticBase, p)...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s update failed: %w", p.Name, err)
	}

	fmt.Printf("%s updated.\n", p.Name)
	return nil
}

func removeOne(p plugin, purgeConfig bool) error {
	fmt.Printf("\n==> Removing %s\n", p.Name)

	for _, command := range p.RemoveCmds {
		if len(command) == 0 {
			continue
		}
		if _, err := exec.LookPath(command[0]); err != nil {
			fmt.Printf("Skipping command; %s was not found: %s\n", command[0], strings.Join(command, " "))
			continue
		}
		fmt.Printf("Running: %s\n", strings.Join(command, " "))
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			fmt.Printf("Command failed; continuing local cleanup: %v\n", err)
		}
	}

	for _, path := range p.RemovePaths {
		expanded := expandHome(path)
		if !pathExists(expanded) {
			continue
		}
		fmt.Printf("Removing: %s\n", displayPath(expanded))
		if err := os.RemoveAll(expanded); err != nil {
			return err
		}
	}

	if purgeConfig {
		for _, path := range p.ConfigFiles {
			expanded := expandHome(path)
			if !pathExists(expanded) {
				continue
			}
			fmt.Printf("Removing config: %s\n", displayPath(expanded))
			if err := os.Remove(expanded); err != nil {
				return err
			}
		}
	}

	fmt.Printf("%s removed.\n", p.Name)
	return nil
}

func buildInstallArgs(scriptPath string, p plugin, input installInput) []string {
	args := []string{
		scriptPath,
		"latest",
		"--type", fixedType,
		"--endpoint", input.Endpoint,
		"--x-token", input.XToken,
		"--tag", "agent_id=" + input.AgentID,
		"--tag", "agent_name=" + input.AgentName,
	}
	args = append(args, p.InstallArgs...)
	return args
}

func buildPluginUpdateArgs(scriptPath string, p plugin) []string {
	args := []string{
		scriptPath,
		"latest",
		"--no-config",
	}
	args = append(args, p.InstallArgs...)
	return args
}

func renderInstallCommand(staticBase string, p plugin, input installInput) string {
	scriptPath := tempScriptPath(p)
	return fmt.Sprintf(
		"curl -fsSL -o %s %s && \\\n%s \\\n%s",
		shellQuote(scriptPath),
		shellQuote(installerURL(staticBase, p)),
		renderEnvAssignments(staticBase, p),
		renderBashCommand(buildInstallArgs(scriptPath, p, input)),
	)
}

func renderPluginUpdateCommand(staticBase string, p plugin) string {
	scriptPath := tempScriptPath(p)
	return fmt.Sprintf(
		"curl -fsSL -o %s %s && \\\n%s \\\n%s",
		shellQuote(scriptPath),
		shellQuote(installerURL(staticBase, p)),
		renderEnvAssignments(staticBase, p),
		renderBashCommand(buildPluginUpdateArgs(scriptPath, p)),
	)
}

func renderBashCommand(args []string) string {
	out := make([]string, len(args))
	for i, arg := range args {
		out[i] = shellQuote(arg)
	}
	return "bash " + strings.Join(out, " ")
}

func pluginEnv(staticBase string, p plugin) []string {
	env := []string{"OSS_ENDPOINT=" + staticBase}
	for _, item := range p.Env {
		key, value, ok := splitEnvAssignment(item)
		if !ok {
			continue
		}
		env = append(env, key+"="+expandHome(value))
	}
	return env
}

func renderEnvAssignments(staticBase string, p plugin) string {
	assignments := []string{"OSS_ENDPOINT=" + shellQuote(staticBase)}
	for _, item := range p.Env {
		key, value, ok := splitEnvAssignment(item)
		if !ok {
			continue
		}
		assignments = append(assignments, key+"="+shellQuote(expandHome(value)))
	}
	return strings.Join(assignments, " ")
}

func splitEnvAssignment(value string) (string, string, bool) {
	key, val, ok := strings.Cut(value, "=")
	if !ok || key == "" {
		return "", "", false
	}
	return key, val, true
}

func installerURL(staticBase string, p plugin) string {
	return strings.TrimRight(staticBase, "/") + "/" + p.PluginName + "/install.sh"
}

func tempScriptPath(p plugin) string {
	return "/tmp/" + p.PluginName + "-install.sh"
}

func downloadFile(url string, target string) error {
	cmd := exec.Command("curl", "-fsSL", "-o", target, url)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func staticBaseURL(value string, endpoint string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		value = strings.TrimSpace(os.Getenv("GTRACE_AGENT_STATIC_BASE"))
	}
	if value == "" {
		value = staticBaseFromDownloadBase(os.Getenv("DOWNLOAD_BASE_URL"))
	}
	if value == "" {
		value = staticBaseFromDownloadBase(os.Getenv("OBS_AGENT_CONNECTOR_OSS_ENDPOINT"))
	}
	if value == "" {
		value = connectorPluginStaticBase()
	}
	if value == "" {
		value = derivedStaticBaseFromEndpoint(endpoint)
	}
	if value == "" {
		value = defaultStaticBase
	}
	return strings.TrimRight(value, "/")
}

func connectorPluginStaticBase() string {
	cfg, _, err := loadConnectorConfig()
	if err != nil {
		return ""
	}
	return staticBaseFromDownloadBase(cfg.DownloadBaseURL)
}

func defaultConnectorConfig() connectorConfig {
	return connectorConfig{}
}

func connectorConfigPath() (string, error) {
	value := strings.TrimSpace(os.Getenv("OBS_AGENT_CONNECTOR_CONFIG"))
	if value != "" {
		return value, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, configDirName, configFileName), nil
}

func loadConnectorConfig() (connectorConfig, string, error) {
	cfg := defaultConnectorConfig()
	path, err := connectorConfigPath()
	if err != nil {
		return cfg, "", err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, path, nil
		}
		return cfg, path, err
	}

	var disk connectorConfig
	if err := json.Unmarshal(data, &disk); err != nil {
		return cfg, path, err
	}

	if strings.TrimSpace(disk.DownloadBaseURL) != "" {
		cfg.DownloadBaseURL = strings.TrimRight(strings.TrimSpace(disk.DownloadBaseURL), "/")
	}

	return cfg, path, nil
}

func latestMetadataURL(cfg connectorConfig) string {
	if strings.TrimSpace(cfg.DownloadBaseURL) == "" {
		return ""
	}
	return strings.TrimRight(cfg.DownloadBaseURL, "/") + "/latest.txt"
}

func staticBaseFromDownloadBase(downloadBase string) string {
	downloadBase = strings.TrimSpace(downloadBase)
	if downloadBase == "" {
		return ""
	}

	parsed, err := url.Parse(downloadBase)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}

	parsed.RawQuery = ""
	parsed.Fragment = ""

	cleanedPath := strings.TrimRight(parsed.Path, "/")
	if cleanedPath == "" {
		parsed.Path = ""
		return strings.TrimRight(parsed.String(), "/")
	}

	lastSlash := strings.LastIndex(cleanedPath, "/")
	if lastSlash <= 0 {
		parsed.Path = ""
		return strings.TrimRight(parsed.String(), "/")
	}

	parsed.Path = cleanedPath[:lastSlash]
	return strings.TrimRight(parsed.String(), "/")
}

func derivedStaticBaseFromEndpoint(endpoint string) string {
	host := endpointHost(endpoint)
	if host == "" {
		return ""
	}

	rootDomain := registeredDomain(host)
	if rootDomain == "" {
		return ""
	}

	return "https://static." + rootDomain
}

func endpointHost(endpoint string) string {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}

	parsed, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}

	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return ""
	}

	return strings.ToLower(host)
}

func registeredDomain(host string) string {
	if host == "" {
		return ""
	}
	if net.ParseIP(host) != nil {
		return ""
	}

	parts := strings.Split(host, ".")
	if len(parts) < 2 {
		return ""
	}

	return parts[len(parts)-2] + "." + parts[len(parts)-1]
}

func pluginNames() []string {
	names := make([]string, 0, len(plugins))
	for name := range plugins {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func resolvedPlugin(p plugin) plugin {
	switch p.Name {
	case "qoder":
		return withQoderVariant(p, detectQoderVariant("auto"))
	case "qoder-cn":
		return withQoderVariant(p, detectQoderVariant("cn"))
	default:
		return p
	}
}

func withQoderVariant(p plugin, variant string) plugin {
	resolved := p
	home := "~/.qoder"
	if variant == "cn" {
		home = "~/.qoder-cn"
	}
	resolved.Env = []string{"QODER_HOME=" + home}
	resolved.InstallArgs = []string{"--variant", variant}
	resolved.Markers = []string{
		home + "/plugins/cache/qoder-marketplace/qoder-otel-probe",
		home + "/plugins/cache/qoder-marketplace/qoder-otel-plugin",
	}
	resolved.ConfigFiles = []string{home + "/gtrace.json"}
	resolved.RemovePaths = []string{
		home + "/plugins/cache/qoder-marketplace/qoder-otel-probe",
		home + "/plugins/cache/qoder-marketplace/qoder-otel-plugin",
	}
	return resolved
}

func detectQoderVariant(fallback string) string {
	qoderHome := strings.TrimSpace(os.Getenv("QODER_HOME"))
	if qoderHome != "" {
		base := strings.ToLower(filepath.Base(qoderHome))
		switch base {
		case ".qoder-cn", "qoder-cn":
			return "cn"
		case ".qoder", "qoder":
			return "global"
		}
	}

	home, err := os.UserHomeDir()
	if err == nil {
		hasGlobal := pathExists(filepath.Join(home, ".qoder"))
		hasCN := pathExists(filepath.Join(home, ".qoder-cn"))
		switch {
		case hasGlobal && !hasCN:
			return "global"
		case hasCN:
			return "cn"
		}
	}

	if strings.EqualFold(fallback, "global") {
		return "global"
	}
	return "cn"
}

func installedMarker(p plugin) (string, bool) {
	path := firstExistingPath(p.Markers)
	return path, path != ""
}

func firstExistingPath(paths []string) string {
	for _, path := range paths {
		expanded := expandHome(path)
		if pathExists(expanded) {
			return expanded
		}
	}
	return ""
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func expandHome(path string) string {
	if path == "~" {
		home, err := os.UserHomeDir()
		if err == nil {
			return home
		}
		return path
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			return home + path[1:]
		}
	}
	return path
}

func displayPath(path string) string {
	home, err := os.UserHomeDir()
	if err == nil && strings.HasPrefix(path, home+"/") {
		return "~" + strings.TrimPrefix(path, home)
	}
	return path
}

func printTable(headers []string, rows [][]string) {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = len(header)
	}
	for _, row := range rows {
		for i, value := range row {
			if i < len(widths) && len(value) > widths[i] {
				widths[i] = len(value)
			}
		}
	}

	printTableRow(headers, widths)
	separators := make([]string, len(headers))
	for i, width := range widths {
		separators[i] = strings.Repeat("-", width)
	}
	printTableRow(separators, widths)
	for _, row := range rows {
		printTableRow(row, widths)
	}
}

func printTableRow(values []string, widths []int) {
	for i, width := range widths {
		value := ""
		if i < len(values) {
			value = values[i]
		}
		if i > 0 {
			fmt.Print("  ")
		}
		fmt.Printf("%-*s", width, value)
	}
	fmt.Println()
}

func shellQuote(value string) string {
	if value == "" {
		return "''"
	}
	if strings.IndexFunc(value, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			!strings.ContainsRune("-_./:=@", r)
	}) == -1 {
		return value
	}
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

func windowsDoubleQuote(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}
