package app

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
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

func pluginDownloadSettings(overrideBase string, cfg connectorConfig, endpoint string) (pluginDownloadConfig, error) {
	source := normalizePluginSource(cfg.PluginSource)

	if envSource := normalizePluginSource(os.Getenv("OBS_AGENT_CONNECTOR_PLUGIN_SOURCE")); envSource != "" {
		source = envSource
	}

	baseURL := strings.TrimSpace(overrideBase)
	if baseURL == "" {
		baseURL = strings.TrimSpace(os.Getenv("OBS_AGENT_CONNECTOR_PLUGIN_BASE_URL"))
	}
	if baseURL == "" {
		baseURL = strings.TrimSpace(cfg.PluginBaseURL)
	}

	switch source {
	case "", pluginSourceOSS:
		source = pluginSourceOSS
		if baseURL == "" {
			baseURL = staticBaseURL("", endpoint)
		}
		baseURL = ossPluginBaseURL(baseURL)
	case pluginSourceGitHub:
		if baseURL == "" {
			return pluginDownloadConfig{}, fmt.Errorf("plugin_base_url is required when plugin_source=github")
		}
	default:
		return pluginDownloadConfig{}, fmt.Errorf("unsupported plugin_source %q", source)
	}

	return pluginDownloadConfig{
		Source:  source,
		BaseURL: strings.TrimRight(baseURL, "/"),
	}, nil
}

func ossPluginBaseURL(value string) string {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	if value == "" || strings.HasSuffix(value, "/"+pluginOSSDirectory) {
		return value
	}
	return value + "/" + pluginOSSDirectory
}

func normalizePluginSource(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "":
		return ""
	case pluginSourceOSS:
		return pluginSourceOSS
	case pluginSourceGitHub:
		return pluginSourceGitHub
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
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
			if err := mergeLegacyTelemetryDefaults(&cfg); err != nil {
				return cfg, path, err
			}
			return cfg, path, nil
		}
		return cfg, path, err
	}

	var disk connectorConfig
	data, err = normalizeJSONBytes(data)
	if err != nil {
		return cfg, path, err
	}
	if err := json.Unmarshal(data, &disk); err != nil {
		return cfg, path, err
	}

	if strings.TrimSpace(disk.DownloadBaseURL) != "" {
		cfg.DownloadBaseURL = strings.TrimRight(strings.TrimSpace(disk.DownloadBaseURL), "/")
	}
	if source := normalizePluginSource(disk.PluginSource); source != "" {
		cfg.PluginSource = source
	}
	if strings.TrimSpace(disk.PluginBaseURL) != "" {
		cfg.PluginBaseURL = strings.TrimRight(strings.TrimSpace(disk.PluginBaseURL), "/")
	}
	if strings.TrimSpace(disk.Endpoint) != "" {
		cfg.Endpoint = strings.TrimSpace(disk.Endpoint)
	}
	if strings.TrimSpace(disk.XToken) != "" {
		cfg.XToken = strings.TrimSpace(disk.XToken)
	}
	if len(disk.GlobalTags) > 0 {
		cfg.GlobalTags = make([]string, 0, len(disk.GlobalTags))
		for _, value := range disk.GlobalTags {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			cfg.GlobalTags = append(cfg.GlobalTags, value)
		}
	}
	if strings.TrimSpace(disk.TracePath) != "" {
		cfg.TracePath = strings.Trim(strings.TrimSpace(disk.TracePath), "/")
	}
	if strings.TrimSpace(disk.MetricsPath) != "" {
		cfg.MetricsPath = strings.Trim(strings.TrimSpace(disk.MetricsPath), "/")
	}
	if len(disk.Headers) > 0 {
		cfg.Headers = make(map[string]string, len(disk.Headers))
		for key, value := range disk.Headers {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				cfg.Headers[key] = value
			}
		}
	}
	if mode := strings.ToLower(strings.TrimSpace(disk.CaptureContent)); mode != "" {
		cfg.CaptureContent = mode
	}
	if disk.MaxChars > 0 {
		cfg.MaxChars = disk.MaxChars
	}
	if disk.Enabled != nil {
		value := *disk.Enabled
		cfg.Enabled = &value
	}
	if err := mergeLegacyTelemetryDefaults(&cfg); err != nil {
		return cfg, path, err
	}

	return cfg, path, nil
}

type legacyTelemetryConfig struct {
	Endpoint           string            `json:"endpoint"`
	TracePath          string            `json:"trace_path"`
	MetricsPath        string            `json:"metrics_path"`
	XToken             string            `json:"x_token"`
	Headers            map[string]string `json:"headers"`
	ResourceAttributes map[string]string `json:"resource_attributes"`
	CaptureContent     string            `json:"capture_content"`
	MaxChars           int               `json:"max_chars"`
	Enabled            *bool             `json:"enabled"`
}

func mergeLegacyTelemetryDefaults(cfg *connectorConfig) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	path := filepath.Join(home, ".agent-telemetry", "config.json")
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var legacy legacyTelemetryConfig
	if err := json.Unmarshal(body, &legacy); err != nil {
		// The legacy file is only a best-effort bootstrap source and must not
		// make an otherwise valid connector installation unusable.
		return nil
	}
	if strings.TrimSpace(cfg.Endpoint) == "" {
		cfg.Endpoint = strings.TrimSpace(legacy.Endpoint)
	}
	if strings.TrimSpace(cfg.XToken) == "" {
		cfg.XToken = strings.TrimSpace(legacy.XToken)
	}
	if strings.TrimSpace(cfg.TracePath) == "" {
		cfg.TracePath = strings.Trim(strings.TrimSpace(legacy.TracePath), "/")
	}
	if strings.TrimSpace(cfg.MetricsPath) == "" {
		cfg.MetricsPath = strings.Trim(strings.TrimSpace(legacy.MetricsPath), "/")
	}
	if len(cfg.Headers) == 0 && len(legacy.Headers) > 0 {
		cfg.Headers = copyStringMap(legacy.Headers)
	}
	if len(cfg.GlobalTags) == 0 && len(legacy.ResourceAttributes) > 0 {
		cfg.GlobalTags = sortedMapEntries(legacy.ResourceAttributes)
	}
	if strings.TrimSpace(cfg.CaptureContent) == "" {
		cfg.CaptureContent = strings.ToLower(strings.TrimSpace(legacy.CaptureContent))
	}
	if cfg.MaxChars == 0 {
		cfg.MaxChars = legacy.MaxChars
	}
	if cfg.Enabled == nil && legacy.Enabled != nil {
		value := *legacy.Enabled
		cfg.Enabled = &value
	}
	return nil
}

func copyStringMap(values map[string]string) map[string]string {
	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			result[key] = value
		}
	}
	return result
}

func sortedMapEntries(values map[string]string) []string {
	entries := make([]string, 0, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key != "" && value != "" {
			entries = append(entries, key+"="+value)
		}
	}
	sort.Strings(entries)
	return entries
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

func normalizeJSONBytes(data []byte) ([]byte, error) {
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	if len(data) < 2 {
		return data, nil
	}

	switch {
	case bytes.HasPrefix(data, []byte{0xFF, 0xFE}):
		return decodeUTF16JSON(data[2:], binary.LittleEndian)
	case bytes.HasPrefix(data, []byte{0xFE, 0xFF}):
		return decodeUTF16JSON(data[2:], binary.BigEndian)
	default:
		return data, nil
	}
}

func decodeUTF16JSON(data []byte, order binary.ByteOrder) ([]byte, error) {
	if len(data)%2 != 0 {
		return nil, fmt.Errorf("invalid UTF-16 JSON payload length %d", len(data))
	}
	words := make([]uint16, 0, len(data)/2)
	for i := 0; i < len(data); i += 2 {
		words = append(words, order.Uint16(data[i:i+2]))
	}
	text := string(utf16.Decode(words))
	return []byte(text), nil
}
