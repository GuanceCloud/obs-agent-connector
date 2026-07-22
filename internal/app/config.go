package app

import (
	"bytes"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

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
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if err := json.Unmarshal(data, &disk); err != nil {
		return cfg, path, err
	}

	if strings.TrimSpace(disk.DownloadBaseURL) != "" {
		cfg.DownloadBaseURL = strings.TrimRight(strings.TrimSpace(disk.DownloadBaseURL), "/")
	}
	if strings.TrimSpace(disk.Endpoint) != "" {
		cfg.Endpoint = strings.TrimSpace(disk.Endpoint)
	}
	if strings.TrimSpace(disk.XToken) != "" {
		cfg.XToken = strings.TrimSpace(disk.XToken)
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
