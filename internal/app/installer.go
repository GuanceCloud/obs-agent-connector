package app

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
	"unicode"

	telemetryinstall "github.com/GuanceCloud/obs-agent-connector/internal/install"
)

var currentGOOS = runtime.GOOS
var confirmInput io.Reader = os.Stdin
var confirmOutput io.Writer = os.Stdout

func resolveInstallInput(defaults installInput, cfg connectorConfig, agent string) (installInput, error) {
	input := mergeExistingRuntimeDefaults(defaults, agent)
	input, err := resolveCommonInstallInput(input, cfg)
	if err != nil {
		return input, err
	}
	existingID, existingName := existingAgentIdentity(agent)
	if strings.TrimSpace(input.AgentID) == "" {
		input.AgentID = existingID
	}
	if strings.TrimSpace(input.AgentID) == "" {
		agentID, err := generateAgentID()
		if err != nil {
			return input, err
		}
		input.AgentID = agentID
	}
	if strings.TrimSpace(input.AgentName) == "" {
		input.AgentName = existingName
	}
	if strings.TrimSpace(input.AgentName) == "" {
		input.AgentName = defaultAgentName(agent, time.Now())
	}
	return input, nil
}

func existingAgentIdentity(name string) (string, string) {
	value := existingAgentConfig(name)
	resource, _ := value["resourceAttributes"].(map[string]any)
	agentID, _ := resource["agent_id"].(string)
	agentName, _ := resource["agent_name"].(string)
	return strings.TrimSpace(agentID), strings.TrimSpace(agentName)
}

func mergeExistingRuntimeDefaults(input installInput, name string) installInput {
	value := existingAgentConfig(name)
	if len(value) == 0 {
		return input
	}
	if strings.TrimSpace(input.Endpoint) == "" {
		input.Endpoint, _ = value["endpoint"].(string)
	}
	if strings.TrimSpace(input.TracePath) == "" {
		input.TracePath, _ = value["tracePath"].(string)
	}
	if strings.TrimSpace(input.MetricsPath) == "" {
		input.MetricsPath, _ = value["metricsPath"].(string)
	}
	if len(input.Headers) == 0 {
		headers := stringMapFromJSON(value["headers"])
		for key, headerValue := range headers {
			if strings.EqualFold(strings.TrimSpace(key), "X-Token") {
				if strings.TrimSpace(input.XToken) == "" {
					input.XToken = strings.TrimSpace(headerValue)
				} else {
					delete(headers, key)
				}
			}
		}
		input.Headers = sortedMapEntries(headers)
	}
	if strings.TrimSpace(input.CaptureContent) == "" {
		input.CaptureContent, _ = value["captureContent"].(string)
		if input.CaptureContent == "" {
			input.CaptureContent, _ = value["capture_content"].(string)
		}
	}
	if input.MaxChars == 0 {
		if maxChars, ok := value["max_chars"].(float64); ok && maxChars > 0 {
			input.MaxChars = int(maxChars)
		}
	}
	if input.Enabled == nil {
		if enabled, ok := value["enabled"].(bool); ok {
			input.Enabled = &enabled
		}
	}
	if len(input.GlobalTags) == 0 {
		resource := stringMapFromJSON(value["resourceAttributes"])
		delete(resource, "agent_id")
		delete(resource, "agent_name")
		input.GlobalTags = sortedMapEntries(resource)
	}
	return input
}

func existingAgentConfig(name string) map[string]any {
	p := agent.Resolve(agent.Get(strings.ToLower(strings.TrimSpace(name))))
	if p.Name == "" {
		return nil
	}
	path := agent.FirstExistingPath(p.ConfigFiles)
	if path == "" {
		return nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	body, err = normalizeJSONBytes(body)
	if err != nil {
		return nil
	}
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return nil
	}
	return value
}

func stringMapFromJSON(value any) map[string]string {
	current, _ := value.(map[string]any)
	result := make(map[string]string, len(current))
	for key, raw := range current {
		text, ok := raw.(string)
		if ok && strings.TrimSpace(key) != "" && strings.TrimSpace(text) != "" {
			result[strings.TrimSpace(key)] = strings.TrimSpace(text)
		}
	}
	return result
}

func resolveCommonInstallInput(defaults installInput, cfg connectorConfig) (installInput, error) {
	input := defaults
	if strings.TrimSpace(input.Endpoint) == "" {
		input.Endpoint = strings.TrimSpace(cfg.Endpoint)
	}
	if strings.TrimSpace(input.XToken) == "" {
		input.XToken = strings.TrimSpace(cfg.XToken)
	}
	if len(input.GlobalTags) == 0 && len(cfg.GlobalTags) > 0 {
		input.GlobalTags = append([]string{}, cfg.GlobalTags...)
	}
	if strings.TrimSpace(input.TracePath) == "" {
		input.TracePath = strings.Trim(strings.TrimSpace(cfg.TracePath), "/")
	}
	if strings.TrimSpace(input.MetricsPath) == "" {
		input.MetricsPath = strings.Trim(strings.TrimSpace(cfg.MetricsPath), "/")
	}
	if len(input.Headers) == 0 && len(cfg.Headers) > 0 {
		input.Headers = sortedMapEntries(cfg.Headers)
	}
	if strings.TrimSpace(input.CaptureContent) == "" {
		input.CaptureContent = strings.ToLower(strings.TrimSpace(cfg.CaptureContent))
	}
	if input.MaxChars == 0 {
		input.MaxChars = cfg.MaxChars
	}
	if input.Enabled == nil && cfg.Enabled != nil {
		value := *cfg.Enabled
		input.Enabled = &value
	}
	if strings.TrimSpace(input.Endpoint) == "" {
		return input, fmt.Errorf("endpoint is required; pass --endpoint or configure it in %s", configFileName)
	}
	if strings.TrimSpace(input.XToken) == "" {
		return input, fmt.Errorf("x-token is required; pass --x-token or configure it in %s", configFileName)
	}
	return input, nil
}

func confirm(label string, defaultYes bool) (bool, error) {
	suffix := "y/N"
	if defaultYes {
		suffix = "Y/n"
	}
	fmt.Fprintf(confirmOutput, "%s [%s]: ", label, suffix)
	reader := bufio.NewReader(confirmInput)
	value, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
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

func installOne(download pluginDownloadConfig, p agent.Definition, input installInput) error {
	fmt.Println()
	printDetails([][2]string{
		{"Action", "install"},
		{"Agent", p.Name},
	})
	if p.IsBuiltin() {
		if err := installBuiltinAdapter(p, input, false); err != nil {
			return err
		}
		printSingleDetail("Result", "installed")
		return nil
	}

	if usesPackageArchive(currentGOOS, p) {
		return runPackageInstaller(download, p, "installation", func(extractDir string) []string {
			return buildPackageInstallArgs(extractDir, p, input)
		})
	}

	scriptPath := tempScriptPathForOS(currentGOOS, p)
	url, err := installerURLForOS(download, p, currentGOOS)
	if err != nil {
		return err
	}
	printSingleDetail("Download", url)

	if err := downloadFile(url, scriptPath); err != nil {
		return fmt.Errorf("failed to download %s installer: %w", p.Name, err)
	}

	if currentGOOS == "windows" {
		command := renderPowerShellInstallCommand(scriptPath, p, input)
		printSingleDetail("Command", redactSecret(command, input.XToken))
		if err := runPowerShell(command); err != nil {
			return fmt.Errorf("%s installation failed: %w", p.Name, err)
		}
	} else {
		args := buildInstallArgs(scriptPath, p, input)
		printSingleDetail("Command", renderBashCommand(redactInstallerArgs(args)))

		cmd := exec.Command("bash", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Env = append(os.Environ(), pluginEnv(download, p)...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s installation failed: %w", p.Name, err)
		}
	}

	printSingleDetail("Result", "installed")
	return nil
}

func updatePluginOne(download pluginDownloadConfig, p agent.Definition) error {
	fmt.Println()
	printDetails([][2]string{
		{"Action", "update"},
		{"Agent", p.Name},
	})
	if p.IsBuiltin() {
		if err := installBuiltinAdapter(p, installInput{}, true); err != nil {
			return err
		}
		printSingleDetail("Result", "reconciled")
		return nil
	}

	if usesPackageArchive(currentGOOS, p) {
		return runPackageInstaller(download, p, "update", func(extractDir string) []string {
			return buildPackageUpdateArgs(extractDir, p)
		})
	}

	scriptPath := tempScriptPathForOS(currentGOOS, p)
	url, err := installerURLForOS(download, p, currentGOOS)
	if err != nil {
		return err
	}
	printSingleDetail("Download", url)

	if err := downloadFile(url, scriptPath); err != nil {
		return fmt.Errorf("failed to download %s installer: %w", p.Name, err)
	}

	if currentGOOS == "windows" {
		command := renderPowerShellUpdateCommand(scriptPath, p)
		printSingleDetail("Command", command)
		if err := runPowerShell(command); err != nil {
			return fmt.Errorf("%s update failed: %w", p.Name, err)
		}
	} else {
		args := buildPluginUpdateArgs(scriptPath, p)
		printSingleDetail("Command", renderBashCommand(args))

		cmd := exec.Command("bash", args...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		cmd.Env = append(os.Environ(), pluginEnv(download, p)...)
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("%s update failed: %w", p.Name, err)
		}
	}

	printSingleDetail("Result", "updated")
	return nil
}

func removeOne(p agent.Definition, purgeConfig bool) error {
	fmt.Println()
	printDetails([][2]string{
		{"Action", "remove"},
		{"Agent", p.Name},
	})
	if p.IsBuiltin() {
		return removeBuiltinAdapter(p, telemetryinstall.RemoveOptions{
			PurgeConfig:  purgeConfig,
			PurgeState:   purgeConfig,
			PurgeManaged: true,
		})
	}

	for _, command := range p.RemoveCmds {
		if len(command) == 0 {
			continue
		}
		if _, err := exec.LookPath(command[0]); err != nil {
			printSingleDetail("Skip", fmt.Sprintf("%s was not found: %s", command[0], strings.Join(command, " ")))
			continue
		}
		printSingleDetail("Command", strings.Join(command, " "))
		cmd := exec.Command(command[0], command[1:]...)
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		cmd.Stdin = os.Stdin
		if err := cmd.Run(); err != nil {
			printSingleDetail("Warning", fmt.Sprintf("command failed; continuing local cleanup: %v", err))
		}
	}

	if p.RemoveCleanup != nil {
		for _, item := range p.RemoveCleanupDetails {
			printSingleDetail("Cleanup", item)
		}
		if err := p.RemoveCleanup(p); err != nil {
			return err
		}
	}

	for _, path := range p.RemovePaths {
		expanded := agent.ExpandHome(path)
		if !agent.PathExists(expanded) {
			continue
		}
		printSingleDetail("Cleanup", agent.DisplayPath(expanded))
		if err := os.RemoveAll(expanded); err != nil {
			return err
		}
	}

	if purgeConfig {
		for _, path := range p.ConfigFiles {
			expanded := agent.ExpandHome(path)
			if !agent.PathExists(expanded) {
				continue
			}
			printSingleDetail("Config", agent.DisplayPath(expanded))
			if err := os.Remove(expanded); err != nil {
				return err
			}
		}
	}

	printSingleDetail("Result", "removed")
	return nil
}

func buildInstallArgs(scriptPath string, p agent.Definition, input installInput) []string {
	args := []string{
		scriptPath,
		"latest",
		"--type", fixedType,
		"--endpoint", input.Endpoint,
		"--x-token", input.XToken,
	}
	args = appendInstallTags(args, input)
	args = append(args, p.InstallArgs...)
	return args
}

func buildPluginUpdateArgs(scriptPath string, p agent.Definition) []string {
	args := []string{
		scriptPath,
		"latest",
		"--no-config",
	}
	args = append(args, p.InstallArgs...)
	return args
}

func renderInstallCommand(download pluginDownloadConfig, p agent.Definition, input installInput) string {
	if usesPackageArchive(currentGOOS, p) {
		return renderPackageCommand(download, p, buildPackageInstallArgs(packageExtractPath(p), p, input))
	}
	scriptPath := tempScriptPathForOS(currentGOOS, p)
	if currentGOOS == "windows" {
		return renderPowerShellInstallCommand(scriptPath, p, input)
	}
	envAssignments := renderEnvAssignments(download, p)
	envLine := ""
	if envAssignments != "" {
		envLine = envAssignments + " \\\n"
	}
	return fmt.Sprintf(
		"curl -fsSL -o %s %s && \\\n%s%s",
		shellQuote(scriptPath),
		shellQuote(mustInstallerURL(download, p, currentGOOS)),
		envLine,
		renderBashCommand(buildInstallArgs(scriptPath, p, input)),
	)
}

func renderPluginUpdateCommand(download pluginDownloadConfig, p agent.Definition) string {
	if usesPackageArchive(currentGOOS, p) {
		return renderPackageCommand(download, p, buildPackageUpdateArgs(packageExtractPath(p), p))
	}
	scriptPath := tempScriptPathForOS(currentGOOS, p)
	if currentGOOS == "windows" {
		return renderPowerShellUpdateCommand(scriptPath, p)
	}
	envAssignments := renderEnvAssignments(download, p)
	envLine := ""
	if envAssignments != "" {
		envLine = envAssignments + " \\\n"
	}
	return fmt.Sprintf(
		"curl -fsSL -o %s %s && \\\n%s%s",
		shellQuote(scriptPath),
		shellQuote(mustInstallerURL(download, p, currentGOOS)),
		envLine,
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

func pluginEnv(download pluginDownloadConfig, p agent.Definition) []string {
	env := []string{}
	if download.Source == pluginSourceOSS {
		env = append(env, "OSS_ENDPOINT="+download.BaseURL)
	}
	for _, item := range p.Env {
		key, value, ok := splitEnvAssignment(item)
		if !ok {
			continue
		}
		env = append(env, key+"="+agent.ExpandHome(value))
	}
	return env
}

func renderEnvAssignments(download pluginDownloadConfig, p agent.Definition) string {
	assignments := []string{}
	if download.Source == pluginSourceOSS {
		assignments = append(assignments, "OSS_ENDPOINT="+shellQuote(download.BaseURL))
	}
	for _, item := range p.Env {
		key, value, ok := splitEnvAssignment(item)
		if !ok {
			continue
		}
		assignments = append(assignments, key+"="+shellQuote(agent.ExpandHome(value)))
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

func installerURLForOS(download pluginDownloadConfig, p agent.Definition, goos string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(goos), "windows") {
		if strings.TrimSpace(p.WindowsInstaller) == "" {
			return "", unsupportedPlatformError(p, goos)
		}
		switch download.Source {
		case pluginSourceGitHub:
			return strings.TrimRight(download.BaseURL, "/") + "/" + p.PluginName + "/releases/latest/download/install-release.ps1", nil
		case pluginSourceOSS:
			return strings.TrimRight(download.BaseURL, "/") + "/" + p.PluginName + "/" + strings.TrimLeft(strings.TrimSpace(p.WindowsInstaller), "/"), nil
		default:
			return "", fmt.Errorf("unsupported plugin source %q", download.Source)
		}
	}
	switch download.Source {
	case pluginSourceGitHub:
		return strings.TrimRight(download.BaseURL, "/") + "/" + p.PluginName + "/releases/latest/download/install-release.sh", nil
	case pluginSourceOSS:
		return strings.TrimRight(download.BaseURL, "/") + "/" + p.PluginName + "/install.sh", nil
	default:
		return "", fmt.Errorf("unsupported plugin source %q", download.Source)
	}
}

func downloadSourceURL(download pluginDownloadConfig, p agent.Definition, goos string) (string, error) {
	if usesPackageArchive(goos, p) {
		return packageArchiveURL(download, p), nil
	}
	return installerURLForOS(download, p, goos)
}

func packageArchiveURL(download pluginDownloadConfig, p agent.Definition) string {
	switch download.Source {
	case pluginSourceGitHub:
		return strings.TrimRight(download.BaseURL, "/") + "/" + p.PluginName + "/releases/latest/download/" + p.PluginName + ".tar.gz"
	default:
		return strings.TrimRight(download.BaseURL, "/") + "/" + p.PluginName + "/" + p.PluginName + ".tar.gz"
	}
}

func usesPackageArchive(goos string, p agent.Definition) bool {
	return !strings.EqualFold(strings.TrimSpace(goos), "windows") && strings.TrimSpace(p.PackageScript) != ""
}

func packageExtractPath(p agent.Definition) string {
	return filepath.Join(os.TempDir(), p.PluginName+"-package")
}

func buildPackageInstallArgs(extractDir string, p agent.Definition, input installInput) []string {
	args := make([]string, 0, len(p.PackageArgs)+8+(len(input.GlobalTags)*2))
	if p.PackageRootArg {
		args = append(args, extractDir)
	}
	args = append(args, p.PackageArgs...)
	args = append(args,
		"--type", fixedType,
		"--endpoint", input.Endpoint,
		"--x-token", input.XToken,
	)
	args = appendInstallTags(args, input)
	args = append(args, p.InstallArgs...)
	return args
}

func buildPackageUpdateArgs(extractDir string, p agent.Definition) []string {
	args := make([]string, 0, len(p.PackageArgs)+2)
	if p.PackageRootArg {
		args = append(args, extractDir)
	}
	args = append(args, p.PackageArgs...)
	args = append(args, "--no-config")
	args = append(args, p.InstallArgs...)
	return args
}

func renderPackageCommand(download pluginDownloadConfig, p agent.Definition, args []string) string {
	archivePath := tempPackageArchivePath(p)
	extractDir := packageExtractPath(p)
	scriptPath := filepath.Join(extractDir, filepath.FromSlash(p.PackageScript))
	envAssignments := renderEnvAssignments(download, p)
	envLine := ""
	if envAssignments != "" {
		envLine = envAssignments + " \\\n"
	}
	return fmt.Sprintf(
		"curl -fsSL -o %s %s && \\\nmkdir -p %s && tar -xzf %s --strip-components=1 -C %s && \\\n%s%s",
		shellQuote(archivePath),
		shellQuote(packageArchiveURL(download, p)),
		shellQuote(extractDir),
		shellQuote(archivePath),
		shellQuote(extractDir),
		envLine,
		renderBashCommand(append([]string{scriptPath}, args...)),
	)
}

func tempPackageArchivePath(p agent.Definition) string {
	return filepath.Join(os.TempDir(), p.PluginName+".tar.gz")
}

func runPackageInstaller(download pluginDownloadConfig, p agent.Definition, action string, argsFn func(string) []string) error {
	extractDir, err := os.MkdirTemp("", p.PluginName+"-package-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(extractDir)
	archivePath := filepath.Join(extractDir, p.PluginName+".tar.gz")

	archiveURL := packageArchiveURL(download, p)
	printSingleDetail("Download", archiveURL)
	if err := downloadFile(archiveURL, archivePath); err != nil {
		return fmt.Errorf("failed to download %s package: %w", p.Name, err)
	}

	if err := extractTarGzStripOne(archivePath, extractDir); err != nil {
		return fmt.Errorf("failed to extract %s package: %w", p.Name, err)
	}

	scriptPath := filepath.Join(extractDir, filepath.FromSlash(p.PackageScript))
	if !agent.PathExists(scriptPath) {
		return fmt.Errorf("package installer was not found: %s", scriptPath)
	}

	args := argsFn(extractDir)
	printSingleDetail("Command", renderBashCommand(redactInstallerArgs(append([]string{scriptPath}, args...))))

	cmd := exec.Command("bash", append([]string{scriptPath}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), pluginEnv(download, p)...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s failed: %w", p.Name, action, err)
	}
	return nil
}

func mustInstallerURL(download pluginDownloadConfig, p agent.Definition, goos string) string {
	url, err := installerURLForOS(download, p, goos)
	if err != nil {
		return "<invalid-installer-url>"
	}
	return url
}

func extractTarGzStripOne(archivePath string, extractDir string) error {
	cmd := exec.Command("tar", "-xzf", archivePath, "--strip-components=1", "-C", extractDir)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func tempScriptPathForOS(goos string, p agent.Definition) string {
	extension := ".sh"
	name := p.PluginName + "-install"
	if strings.EqualFold(strings.TrimSpace(goos), "windows") {
		extension = ".ps1"
		name += "-release"
	}
	return filepath.Join(os.TempDir(), name+extension)
}

func downloadFile(url string, target string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected HTTP status %s", resp.Status)
	}

	file, err := os.Create(target)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err := io.Copy(file, resp.Body); err != nil {
		return err
	}
	return nil
}

func renderPowerShellInstallCommand(scriptPath string, p agent.Definition, input installInput) string {
	tagValues := make([]string, 0, len(input.GlobalTags)+2)
	for _, value := range input.GlobalTags {
		tagValues = append(tagValues, powershellSingleQuote(value))
	}
	tagValues = append(tagValues,
		powershellSingleQuote("agent_id="+input.AgentID),
		powershellSingleQuote("agent_name="+input.AgentName),
	)
	args := []string{
		"& " + powershellSingleQuote(scriptPath),
		"-Version " + powershellSingleQuote("latest"),
		"-Endpoint " + powershellSingleQuote(input.Endpoint),
		"-XToken " + powershellSingleQuote(input.XToken),
		"-Tag @(" + strings.Join(tagValues, ", ") + ")",
	}
	for _, arg := range renderPowerShellOptionArgs(p.WindowsArgs) {
		args = append(args, arg)
	}
	return "& { " + strings.Join(args, " ") + " }"
}

func renderPowerShellUpdateCommand(scriptPath string, p agent.Definition) string {
	args := []string{
		"& " + powershellSingleQuote(scriptPath),
		"-Version " + powershellSingleQuote("latest"),
		"-NoConfig",
	}
	for _, arg := range renderPowerShellOptionArgs(p.WindowsArgs) {
		args = append(args, arg)
	}
	return "& { " + strings.Join(args, " ") + " }"
}

func renderPowerShellOptionArgs(args []string) []string {
	if len(args) == 0 {
		return nil
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		value := strings.TrimSpace(args[i])
		if value == "" {
			continue
		}
		if strings.HasPrefix(value, "-") {
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				out = append(out, value+" "+powershellSingleQuote(args[i+1]))
				i++
				continue
			}
			out = append(out, value)
			continue
		}
		out = append(out, powershellSingleQuote(value))
	}
	return out
}

func runPowerShell(command string) error {
	executable, err := powerShellExecutable()
	if err != nil {
		return err
	}
	cmd := exec.Command(executable, "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func powerShellExecutable() (string, error) {
	for _, name := range []string{"powershell", "pwsh"} {
		if path, err := exec.LookPath(name); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("PowerShell was not found in PATH")
}

func unsupportedPlatformError(p agent.Definition, goos string) error {
	if !strings.EqualFold(strings.TrimSpace(goos), "windows") {
		return fmt.Errorf("%s is not supported on %s", p.Name, goos)
	}
	return fmt.Errorf(
		"%s is not supported on Windows; supported Windows Agents: %s",
		p.Name,
		strings.Join(agent.SupportedNames("windows"), ", "),
	)
}

func generateAgentID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate agent_id: %w", err)
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	return fmt.Sprintf("agid_%x", buf), nil
}

func appendInstallTags(args []string, input installInput) []string {
	for _, value := range input.GlobalTags {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		args = append(args, "--tag", value)
	}
	args = append(args,
		"--tag", "agent_id="+input.AgentID,
		"--tag", "agent_name="+input.AgentName,
	)
	return args
}

func defaultAgentName(agent string, now time.Time) string {
	host, err := os.Hostname()
	if err != nil {
		host = ""
	}
	host = normalizeAgentNameHost(host)
	if host == "" {
		host = "agent"
	}
	agent = normalizeAgentNameHost(agent)
	if agent == "" {
		agent = "unknown"
	}
	return host + "_" + agent + "_" + now.Format("20060102")
}

func normalizeAgentNameHost(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return ""
	}

	var builder strings.Builder
	lastUnderscore := false
	for _, r := range value {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			builder.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '.' || r == ' ' {
			if !lastUnderscore {
				builder.WriteByte('_')
				lastUnderscore = true
			}
			continue
		}
		if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}

	normalized := strings.Trim(builder.String(), "_")
	return normalized
}

func redactInstallerArgs(args []string) []string {
	redacted := append([]string{}, args...)
	for i := 0; i < len(redacted)-1; i++ {
		if redacted[i] == "--x-token" || strings.EqualFold(redacted[i], "-XToken") {
			redacted[i+1] = "<redacted>"
			i++
		}
	}
	return redacted
}

func redactSecret(value, secret string) string {
	if strings.TrimSpace(secret) == "" {
		return value
	}
	return strings.ReplaceAll(value, secret, "<redacted>")
}
