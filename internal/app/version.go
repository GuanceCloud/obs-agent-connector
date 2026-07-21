package app

import (
	"flag"
	"fmt"
	agent "github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func showVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	offline := fs.Bool("offline", false, "Skip remote release check")
	updateNow := fs.Bool("u", false, "Update obs-agent-connector to the latest release")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return fmt.Errorf("unrecognized version arguments: %s", strings.Join(fs.Args(), " "))
	}
	if *offline && *updateNow {
		return fmt.Errorf("version -u cannot be combined with --offline")
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
			fmt.Printf("Config: %s\n", agent.DisplayPath(cfgPath))
		}
		return nil
	}

	release, err := fetchLatestRelease(cfg)
	if err != nil {
		if *updateNow {
			return fmt.Errorf("self-update failed to check latest release: %w", err)
		}
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
		if *updateNow {
			return fmt.Errorf("self-update is unavailable: %w", err)
		}
		fmt.Printf("Update command: unavailable (%v)\n", err)
		return nil
	}

	if *updateNow {
		fmt.Println("Running self-update...")
		if err := runSelfUpdateCommand(command); err != nil {
			return fmt.Errorf("self-update failed: %w", err)
		}
		fmt.Println("Self-update finished.")
		return nil
	}

	fmt.Println("Update command:")
	fmt.Println(command)
	if note != "" {
		fmt.Printf("Note: %s\n", note)
	}

	return nil
}

func runSelfUpdateCommand(command string) error {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", command)
	} else {
		cmd = exec.Command("sh", "-c", command)
	}
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
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

	installDir := filepath.Dir(executablePath)
	downloadBase := strings.TrimRight(cfg.DownloadBaseURL, "/")

	if runtime.GOOS == "windows" {
		command := fmt.Sprintf(
			"powershell -ExecutionPolicy Bypass -Command \"$installer = Join-Path $env:TEMP 'obs-agent-connector-install.ps1'; Invoke-WebRequest -Uri %s -OutFile $installer; & $installer -BinaryOnly -Version %s -InstallDir %s -DownloadBaseUrl %s\"",
			powershellSingleQuote(downloadBase+"/install.ps1?v="+url.QueryEscape(tag)),
			powershellSingleQuote(tag),
			powershellSingleQuote(installDir),
			powershellSingleQuote(downloadBase),
		)
		return command, "", nil
	}

	installerPath := "/tmp/obs-agent-connector-install.sh"
	command := fmt.Sprintf(
		"curl -fsSL -o %s %s && sh %s --binary-only --version %s --install-dir %s --download-base-url %s",
		shellQuote(installerPath),
		shellQuote(downloadBase+"/install.sh?v="+url.QueryEscape(tag)),
		shellQuote(installerPath),
		shellQuote(tag),
		shellQuote(installDir),
		shellQuote(downloadBase),
	)
	return command, "If the current install directory requires elevated permissions, run the command with suitable privileges.", nil
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
