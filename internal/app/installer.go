package app

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
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
)

var currentGOOS = runtime.GOOS

func resolveInstallInput(defaults installInput, cfg connectorConfig, agent string) (installInput, error) {
	input, err := resolveCommonInstallInput(defaults, cfg)
	if err != nil {
		return input, err
	}
	if strings.TrimSpace(input.AgentID) == "" {
		agentID, err := generateAgentID()
		if err != nil {
			return input, err
		}
		input.AgentID = agentID
	}
	if strings.TrimSpace(input.AgentName) == "" {
		input.AgentName = defaultAgentName(agent, time.Now())
	}
	return input, nil
}

func resolveCommonInstallInput(defaults installInput, cfg connectorConfig) (installInput, error) {
	input := defaults
	if strings.TrimSpace(input.Endpoint) == "" {
		input.Endpoint = strings.TrimSpace(cfg.Endpoint)
	}
	if strings.TrimSpace(input.XToken) == "" {
		input.XToken = strings.TrimSpace(cfg.XToken)
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

func installOne(staticBase string, p agent.Definition, input installInput) error {
	fmt.Printf("\n==> Installing %s\n", p.Name)

	if usesPackageArchive(currentGOOS, p) {
		return runPackageInstaller(staticBase, p, "installation", func(extractDir string) []string {
			return buildPackageInstallArgs(extractDir, p, input)
		})
	}

	scriptPath := tempScriptPathForOS(currentGOOS, p)
	url, err := installerURLForOS(staticBase, p, currentGOOS)
	if err != nil {
		return err
	}
	fmt.Printf("Downloading installer: %s\n", url)

	if err := downloadFile(url, scriptPath); err != nil {
		return fmt.Errorf("failed to download %s installer: %w", p.Name, err)
	}

	if currentGOOS == "windows" {
		command := renderPowerShellInstallCommand(scriptPath, p, input)
		fmt.Printf("Running: %s\n", command)
		if err := runPowerShell(command); err != nil {
			return fmt.Errorf("%s installation failed: %w", p.Name, err)
		}
	} else {
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
	}

	fmt.Printf("%s installed.\n", p.Name)
	return nil
}

func updatePluginOne(staticBase string, p agent.Definition) error {
	fmt.Printf("\n==> Updating %s\n", p.Name)

	if usesPackageArchive(currentGOOS, p) {
		return runPackageInstaller(staticBase, p, "update", func(extractDir string) []string {
			return buildPackageUpdateArgs(extractDir, p)
		})
	}

	scriptPath := tempScriptPathForOS(currentGOOS, p)
	url, err := installerURLForOS(staticBase, p, currentGOOS)
	if err != nil {
		return err
	}
	fmt.Printf("Downloading installer: %s\n", url)

	if err := downloadFile(url, scriptPath); err != nil {
		return fmt.Errorf("failed to download %s installer: %w", p.Name, err)
	}

	if currentGOOS == "windows" {
		command := renderPowerShellUpdateCommand(scriptPath, p)
		fmt.Printf("Running: %s\n", command)
		if err := runPowerShell(command); err != nil {
			return fmt.Errorf("%s update failed: %w", p.Name, err)
		}
	} else {
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
	}

	fmt.Printf("%s updated.\n", p.Name)
	return nil
}

func removeOne(p agent.Definition, purgeConfig bool) error {
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
		expanded := agent.ExpandHome(path)
		if !agent.PathExists(expanded) {
			continue
		}
		fmt.Printf("Removing: %s\n", agent.DisplayPath(expanded))
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
			fmt.Printf("Removing config: %s\n", agent.DisplayPath(expanded))
			if err := os.Remove(expanded); err != nil {
				return err
			}
		}
	}

	fmt.Printf("%s removed.\n", p.Name)
	return nil
}

func buildInstallArgs(scriptPath string, p agent.Definition, input installInput) []string {
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

func buildPluginUpdateArgs(scriptPath string, p agent.Definition) []string {
	args := []string{
		scriptPath,
		"latest",
		"--no-config",
	}
	args = append(args, p.InstallArgs...)
	return args
}

func renderInstallCommand(staticBase string, p agent.Definition, input installInput) string {
	if usesPackageArchive(currentGOOS, p) {
		return renderPackageCommand(staticBase, p, buildPackageInstallArgs(packageExtractPath(p), p, input))
	}
	scriptPath := tempScriptPathForOS(currentGOOS, p)
	if currentGOOS == "windows" {
		return renderPowerShellInstallCommand(scriptPath, p, input)
	}
	return fmt.Sprintf(
		"curl -fsSL -o %s %s && \\\n%s \\\n%s",
		shellQuote(scriptPath),
		shellQuote(strings.TrimRight(staticBase, "/")+"/"+p.PluginName+"/install.sh"),
		renderEnvAssignments(staticBase, p),
		renderBashCommand(buildInstallArgs(scriptPath, p, input)),
	)
}

func renderPluginUpdateCommand(staticBase string, p agent.Definition) string {
	if usesPackageArchive(currentGOOS, p) {
		return renderPackageCommand(staticBase, p, buildPackageUpdateArgs(packageExtractPath(p), p))
	}
	scriptPath := tempScriptPathForOS(currentGOOS, p)
	if currentGOOS == "windows" {
		return renderPowerShellUpdateCommand(scriptPath, p)
	}
	return fmt.Sprintf(
		"curl -fsSL -o %s %s && \\\n%s \\\n%s",
		shellQuote(scriptPath),
		shellQuote(strings.TrimRight(staticBase, "/")+"/"+p.PluginName+"/install.sh"),
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

func pluginEnv(staticBase string, p agent.Definition) []string {
	env := []string{"OSS_ENDPOINT=" + staticBase}
	for _, item := range p.Env {
		key, value, ok := splitEnvAssignment(item)
		if !ok {
			continue
		}
		env = append(env, key+"="+agent.ExpandHome(value))
	}
	return env
}

func renderEnvAssignments(staticBase string, p agent.Definition) string {
	assignments := []string{"OSS_ENDPOINT=" + shellQuote(staticBase)}
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

func installerURLForOS(staticBase string, p agent.Definition, goos string) (string, error) {
	if strings.EqualFold(strings.TrimSpace(goos), "windows") {
		installer := strings.TrimSpace(p.WindowsInstaller)
		if installer == "" {
			return "", unsupportedPlatformError(p, goos)
		}
		if strings.Contains(installer, "://") {
			return installer, nil
		}
		return strings.TrimRight(staticBase, "/") + "/" + p.PluginName + "/" + strings.TrimLeft(installer, "/"), nil
	}
	return strings.TrimRight(staticBase, "/") + "/" + p.PluginName + "/install.sh", nil
}

func downloadSourceURL(staticBase string, p agent.Definition, goos string) (string, error) {
	if usesPackageArchive(goos, p) {
		return packageArchiveURL(staticBase, p), nil
	}
	return installerURLForOS(staticBase, p, goos)
}

func packageArchiveURL(staticBase string, p agent.Definition) string {
	return strings.TrimRight(staticBase, "/") + "/" + p.PluginName + "/" + p.PluginName + ".tar.gz"
}

func usesPackageArchive(goos string, p agent.Definition) bool {
	return !strings.EqualFold(strings.TrimSpace(goos), "windows") && strings.TrimSpace(p.PackageScript) != ""
}

func packageExtractPath(p agent.Definition) string {
	return filepath.Join(os.TempDir(), p.PluginName+"-package")
}

func buildPackageInstallArgs(extractDir string, p agent.Definition, input installInput) []string {
	args := make([]string, 0, len(p.PackageArgs)+8)
	if p.PackageRootArg {
		args = append(args, extractDir)
	}
	args = append(args, p.PackageArgs...)
	args = append(args,
		"--type", fixedType,
		"--endpoint", input.Endpoint,
		"--x-token", input.XToken,
		"--tag", "agent_id="+input.AgentID,
		"--tag", "agent_name="+input.AgentName,
	)
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

func renderPackageCommand(staticBase string, p agent.Definition, args []string) string {
	archivePath := tempPackageArchivePath(p)
	extractDir := packageExtractPath(p)
	scriptPath := filepath.Join(extractDir, filepath.FromSlash(p.PackageScript))
	return fmt.Sprintf(
		"curl -fsSL -o %s %s && \\\nmkdir -p %s && tar -xzf %s --strip-components=1 -C %s && \\\n%s \\\n%s",
		shellQuote(archivePath),
		shellQuote(packageArchiveURL(staticBase, p)),
		shellQuote(extractDir),
		shellQuote(archivePath),
		shellQuote(extractDir),
		renderEnvAssignments(staticBase, p),
		renderBashCommand(append([]string{scriptPath}, args...)),
	)
}

func tempPackageArchivePath(p agent.Definition) string {
	return filepath.Join(os.TempDir(), p.PluginName+".tar.gz")
}

func runPackageInstaller(staticBase string, p agent.Definition, action string, argsFn func(string) []string) error {
	extractDir, err := os.MkdirTemp("", p.PluginName+"-package-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(extractDir)
	archivePath := filepath.Join(extractDir, p.PluginName+".tar.gz")

	archiveURL := packageArchiveURL(staticBase, p)
	fmt.Printf("Downloading package: %s\n", archiveURL)
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
	fmt.Printf("Running: %s\n", renderBashCommand(append([]string{scriptPath}, args...)))

	cmd := exec.Command("bash", append([]string{scriptPath}, args...)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), pluginEnv(staticBase, p)...)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s failed: %w", p.Name, action, err)
	}
	return nil
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
	args := []string{
		"& " + powershellSingleQuote(scriptPath),
		"-Version " + powershellSingleQuote("latest"),
		"-Endpoint " + powershellSingleQuote(input.Endpoint),
		"-XToken " + powershellSingleQuote(input.XToken),
		"-Tag @(" + strings.Join([]string{
			powershellSingleQuote("agent_id=" + input.AgentID),
			powershellSingleQuote("agent_name=" + input.AgentName),
		}, ", ") + ")",
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
	return "agent_" + hex.EncodeToString(buf), nil
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
