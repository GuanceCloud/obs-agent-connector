package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	Enabled            bool
	Endpoint           string
	BaseURL            string
	TracePath          string
	MetricsPath        string
	OtelTracesURL      string
	OtelMetricsURL     string
	PublicKey          string
	SecretKey          string
	Headers            map[string]string
	Environment        string
	UserID             string
	Tags               []string
	Metadata           map[string]any
	ResourceAttributes map[string]any
	TimeoutMs          int
	MaxChars           int
	CaptureContent     string
	Debug              bool
	FailOnError        bool
	HookLogFile        string
	StateDir           string
	LockStaleMs        int
}

type ResolveOptions struct {
	Home string
	Cwd  string
}

func Resolve(options ResolveOptions) (Config, error) {
	home := options.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	cwd := options.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	globalConfig, err := readJSONIfExists(filepath.Join(home, ".codex", "gtrace.json"))
	if err != nil {
		return Config{}, err
	}
	localConfig, err := readJSONIfExists(filepath.Join(cwd, ".codex", "gtrace.json"))
	if err != nil {
		return Config{}, err
	}

	merged := map[string]any{
		"enabled":        false,
		"tracePath":      "api/public/otel/v1/traces",
		"metricsPath":    "api/public/otel/v1/metrics",
		"max_chars":      20_000,
		"captureContent": "preview",
		"debug":          false,
		"fail_on_error":  false,
	}
	for key, value := range globalConfig {
		merged[key] = value
	}
	for key, value := range localConfig {
		merged[key] = value
	}

	tags := parseTags(merged["tags"])
	configuredResourceAttributes := parseResourceAttributes(merged["resourceAttributes"])
	resourceAttributes := map[string]any{}
	for key, value := range tagsToResourceAttributes(tags) {
		resourceAttributes[key] = value
	}
	for key, value := range configuredResourceAttributes {
		resourceAttributes[key] = value
	}

	captureContent := strings.ToLower(firstNonEmptyString(
		asString(merged["captureContent"]),
		asString(merged["capture_content"]),
		"preview",
	))
	if captureContent != "none" && captureContent != "full" {
		captureContent = "preview"
	}
	return Config{
		Enabled:            parseBoolean(merged["enabled"], false),
		Endpoint:           normalizeEndpoint(asString(merged["endpoint"]), asString(merged["base_url"])),
		BaseURL:            normalizeEndpoint(asString(merged["base_url"]), ""),
		TracePath:          normalizeSignalPath(asString(merged["tracePath"]), "api/public/otel/v1/traces"),
		MetricsPath:        normalizeSignalPath(asString(merged["metricsPath"]), "api/public/otel/v1/metrics"),
		OtelTracesURL:      asString(merged["otel_traces_url"]),
		OtelMetricsURL:     asString(merged["otel_metrics_url"]),
		PublicKey:          asString(merged["public_key"]),
		SecretKey:          asString(merged["secret_key"]),
		Headers:            parseHeaders(merged["headers"]),
		Environment:        asString(merged["environment"]),
		UserID:             asString(merged["user_id"]),
		Tags:               tags,
		Metadata:           parseMetadata(merged["metadata"]),
		ResourceAttributes: resourceAttributes,
		TimeoutMs:          parseInteger(merged["timeout_ms"], 25_000),
		MaxChars:           parseInteger(merged["max_chars"], 20_000),
		CaptureContent:     captureContent,
		Debug:              parseBoolean(merged["debug"], false),
		FailOnError:        parseBoolean(merged["fail_on_error"], false),
		HookLogFile:        firstNonEmptyString(asString(merged["hook_log_file"]), filepath.Join(home, ".codex", "gtrace-hook.log")),
		StateDir:           firstNonEmptyString(asString(merged["state_dir"]), filepath.Join(home, ".codex", "state", "gtrace-agent")),
		LockStaleMs:        parseInteger(merged["lock_stale_ms"], 120_000),
	}, nil
}

func readJSONIfExists(file string) (map[string]any, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	trimmed := strings.TrimPrefix(string(data), "\uFEFF")
	if strings.TrimSpace(trimmed) == "" {
		return nil, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(trimmed), &out); err != nil {
		return nil, nil
	}
	return out, nil
}

func parseBoolean(value any, fallback bool) bool {
	switch current := value.(type) {
	case bool:
		return current
	case string:
		normalized := strings.ToLower(strings.TrimSpace(current))
		switch normalized {
		case "1", "true", "yes", "on":
			return true
		case "0", "false", "no", "off":
			return false
		}
	}
	return fallback
}

func parseInteger(value any, fallback int) int {
	switch current := value.(type) {
	case float64:
		return int(current)
	case int:
		return current
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(current))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func parseTags(value any) []string {
	switch current := value.(type) {
	case []any:
		out := make([]string, 0, len(current))
		for _, item := range current {
			text := strings.TrimSpace(asString(item))
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	case string:
		trimmed := strings.TrimSpace(current)
		if trimmed == "" {
			return nil
		}
		if strings.HasPrefix(trimmed, "[") {
			var parsed []string
			if err := json.Unmarshal([]byte(trimmed), &parsed); err == nil {
				return parsed
			}
		}
		parts := strings.Split(trimmed, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			item := strings.TrimSpace(part)
			if item != "" {
				out = append(out, item)
			}
		}
		return out
	default:
		return nil
	}
}

func parseMetadata(value any) map[string]any {
	if value == nil {
		return nil
	}
	if current, ok := value.(map[string]any); ok {
		return current
	}
	if text, ok := value.(string); ok {
		var parsed map[string]any
		if err := json.Unmarshal([]byte(text), &parsed); err == nil {
			return parsed
		}
	}
	return nil
}

func parseResourceAttributes(value any) map[string]any {
	metadata := parseMetadata(value)
	if len(metadata) == 0 {
		return nil
	}
	out := map[string]any{}
	for key, item := range metadata {
		switch item.(type) {
		case string, float64, bool, int:
			out[key] = item
		}
	}
	return out
}

func tagsToResourceAttributes(tags []string) map[string]any {
	if len(tags) == 0 {
		return nil
	}
	out := map[string]any{}
	for _, tag := range tags {
		parts := strings.Split(tag, "=")
		if len(parts) < 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(strings.Join(parts[1:], "="))
		if key != "" && value != "" {
			out[key] = value
		}
	}
	return out
}

func normalizeEndpoint(primary, secondary string) string {
	value := firstNonEmptyString(primary, secondary)
	value = strings.TrimSpace(value)
	if value == "" {
		return "http://localhost:3030"
	}
	return strings.TrimRight(value, "/")
}

func normalizeSignalPath(value, fallback string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	return strings.Trim(strings.TrimLeft(trimmed, "/"), "/")
}

func parseHeaders(value any) map[string]string {
	current, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	headers := map[string]string{}
	for key, item := range current {
		text := strings.TrimSpace(asString(item))
		if text != "" {
			headers[key] = text
		}
	}
	if len(headers) == 0 {
		return nil
	}
	return headers
}

func asString(value any) string {
	switch current := value.(type) {
	case string:
		return current
	default:
		return ""
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
