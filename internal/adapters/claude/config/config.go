package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

const (
	DefaultMaxChars  = 20_000
	DefaultTimeoutMs = 10_000
)

type Config struct {
	Enabled            bool
	Transport          transport.Config
	ResourceAttributes map[string]any
	CaptureContent     string
	MaxChars           int
	Debug              bool
	UserID             string
	HookLogFile        string
	StateDir           string
}

type ResolveOptions struct {
	Env  map[string]string
	Home string
	Cwd  string
}

func Resolve(options ResolveOptions) Config {
	env := options.Env
	if env == nil {
		env = environment()
	}
	home := options.Home
	if home == "" {
		home, _ = os.UserHomeDir()
	}
	cwd := options.Cwd
	if cwd == "" {
		cwd, _ = os.Getwd()
	}

	merged := map[string]any{
		"tracePath":      "v1/traces",
		"metricsPath":    "v1/metrics",
		"maxChars":       DefaultMaxChars,
		"timeoutMs":      DefaultTimeoutMs,
		"debug":          false,
		"captureContent": "preview",
		"headers":        map[string]any{},
		"resourceAttributes": map[string]any{
			"service.name":           "gtrace-claude-code",
			"telemetry.sdk.name":     "gtrace",
			"telemetry.sdk.language": "go",
			"agent_runtime":          "claude",
		},
	}
	sources := []map[string]any{
		pluginEnvironment(env),
		readJSON(filepath.Join(home, ".claude", "gtrace.json")),
		readJSON(filepath.Join(cwd, ".claude", "gtrace.json")),
		readJSON(agentfiles.ConfigPath(home, "claude")),
		ordinaryEnvironment(env),
	}
	for _, source := range sources {
		merge(merged, source)
	}

	endpoint := firstString(merged, "endpoint", "base_url")
	tracePath := normalizedPath(firstString(merged, "tracePath", "trace_path"), "v1/traces")
	metricsPath := normalizedPath(firstString(merged, "metricsPath", "metrics_path"), "v1/metrics")
	if metricsPath == "v1/metrics" && tracePath == "v1/write/otel-llm" {
		metricsPath = "v1/write/otel-metrics"
	}
	traceURL := firstString(merged, "otel_traces_url", "tracesEndpoint", "traceEndpoint")
	metricsURL := firstString(merged, "otel_metrics_url", "metricsEndpoint", "metricEndpoint")
	enabled, hasEnabled := boolean(merged["enabled"])
	if !hasEnabled {
		enabled = endpoint != "" || traceURL != "" || metricsURL != ""
	}
	timeoutMs := integer(merged["timeoutMs"], integer(merged["timeout_ms"], DefaultTimeoutMs))
	maxChars := integer(merged["maxChars"], integer(merged["max_chars"], DefaultMaxChars))
	captureContent := strings.ToLower(firstString(merged, "captureContent", "capture_content"))
	if captureContent != "none" && captureContent != "full" {
		captureContent = "preview"
	}
	headers := stringMap(merged["headers"])
	resources := primitiveMap(merged["resourceAttributes"])
	return Config{
		Enabled: enabled,
		Transport: transport.Config{
			Endpoint:    strings.TrimRight(endpoint, "/"),
			TracePath:   tracePath,
			MetricsPath: metricsPath,
			TraceURL:    strings.TrimRight(traceURL, "/"),
			MetricsURL:  strings.TrimRight(metricsURL, "/"),
			Headers:     headers,
			PublicKey:   firstString(merged, "public_key", "publicKey"),
			SecretKey:   firstString(merged, "secret_key", "secretKey"),
			Timeout:     time.Duration(timeoutMs) * time.Millisecond,
		},
		ResourceAttributes: resources,
		CaptureContent:     captureContent,
		MaxChars:           maxChars,
		Debug:              boolValue(merged["debug"]),
		UserID:             firstString(merged, "user_id", "userId"),
		HookLogFile:        agentfiles.HookLogPath(home, "claude"),
		StateDir:           firstNonEmpty(firstString(merged, "state_dir"), filepath.Join(home, ".claude", "state", "gtrace-agent")),
	}
}

func ordinaryEnvironment(env map[string]string) map[string]any {
	return clean(map[string]any{
		"enabled":            firstEnv(env, "CLAUDE_OTEL_ENABLED", "TRACE_TO_GTRACE"),
		"endpoint":           firstEnv(env, "OTEL_EXPORTER_OTLP_ENDPOINT", "CLAUDE_OTEL_ENDPOINT", "GTRACE_ENDPOINT"),
		"otel_traces_url":    firstEnv(env, "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", "CLAUDE_OTEL_TRACES_ENDPOINT"),
		"otel_metrics_url":   firstEnv(env, "OTEL_EXPORTER_OTLP_METRICS_ENDPOINT", "CLAUDE_OTEL_METRICS_ENDPOINT"),
		"tracePath":          firstEnv(env, "CLAUDE_OTEL_TRACE_PATH", "GTRACE_TRACE_PATH"),
		"metricsPath":        firstEnv(env, "CLAUDE_OTEL_METRICS_PATH", "GTRACE_METRICS_PATH"),
		"headers":            parseObject(firstEnv(env, "OTEL_EXPORTER_OTLP_HEADERS", "CLAUDE_OTEL_HEADERS")),
		"resourceAttributes": parseObject(firstEnv(env, "OTEL_RESOURCE_ATTRIBUTES", "CLAUDE_OTEL_RESOURCE_ATTRIBUTES")),
		"debug":              firstEnv(env, "CLAUDE_OTEL_DEBUG", "GTRACE_DEBUG"),
		"maxChars":           firstEnv(env, "CLAUDE_OTEL_MAX_CHARS", "GTRACE_MAX_CHARS"),
		"timeoutMs":          firstEnv(env, "CLAUDE_OTEL_TIMEOUT_MS", "GTRACE_TIMEOUT_MS"),
		"user_id":            firstEnv(env, "CLAUDE_OTEL_USER_ID", "GTRACE_USER_ID"),
		"captureContent":     firstEnv(env, "CLAUDE_OTEL_CAPTURE_CONTENT", "GTRACE_CAPTURE_CONTENT"),
	})
}

func pluginEnvironment(env map[string]string) map[string]any {
	value := func(name string) string {
		return env["CLAUDE_PLUGIN_OPTION_"+name]
	}
	return clean(map[string]any{
		"enabled":            value("CLAUDE_OTEL_ENABLED"),
		"endpoint":           value("OTEL_EXPORTER_OTLP_ENDPOINT"),
		"otel_traces_url":    value("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
		"otel_metrics_url":   value("OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"),
		"tracePath":          value("CLAUDE_OTEL_TRACE_PATH"),
		"metricsPath":        value("CLAUDE_OTEL_METRICS_PATH"),
		"headers":            parseObject(value("OTEL_EXPORTER_OTLP_HEADERS")),
		"resourceAttributes": parseObject(value("CLAUDE_OTEL_RESOURCE_ATTRIBUTES")),
		"debug":              value("CLAUDE_OTEL_DEBUG"),
		"maxChars":           value("CLAUDE_OTEL_MAX_CHARS"),
		"timeoutMs":          value("CLAUDE_OTEL_TIMEOUT_MS"),
		"user_id":            value("CLAUDE_OTEL_USER_ID"),
		"captureContent":     value("CLAUDE_OTEL_CAPTURE_CONTENT"),
	})
}

func merge(target, source map[string]any) {
	for key, value := range source {
		if value == nil || value == "" {
			continue
		}
		if key == "headers" || key == "resourceAttributes" {
			existing := objectMap(target[key])
			for childKey, childValue := range objectMap(value) {
				existing[childKey] = childValue
			}
			target[key] = existing
			continue
		}
		target[key] = value
	}
}

func readJSON(path string) map[string]any {
	body, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var parsed map[string]any
	if json.Unmarshal(body, &parsed) != nil {
		return map[string]any{}
	}
	return parsed
}

func parseObject(value string) map[string]any {
	out := map[string]any{}
	value = strings.TrimSpace(value)
	if value == "" {
		return out
	}
	if strings.HasPrefix(value, "{") {
		if json.Unmarshal([]byte(value), &out) == nil {
			return out
		}
		return map[string]any{}
	}
	for _, entry := range strings.Split(value, ",") {
		key, raw, found := strings.Cut(entry, "=")
		if found && strings.TrimSpace(key) != "" && strings.TrimSpace(raw) != "" {
			out[strings.TrimSpace(key)] = strings.TrimSpace(raw)
		}
	}
	return out
}

func objectMap(value any) map[string]any {
	if current, ok := value.(map[string]any); ok {
		out := make(map[string]any, len(current))
		for key, item := range current {
			out[key] = item
		}
		return out
	}
	return map[string]any{}
}

func stringMap(value any) map[string]string {
	out := map[string]string{}
	for key, item := range objectMap(value) {
		if text := strings.TrimSpace(toString(item)); text != "" {
			out[key] = text
		}
	}
	return out
}

func primitiveMap(value any) map[string]any {
	out := map[string]any{}
	for key, item := range objectMap(value) {
		switch item.(type) {
		case string, bool, float64, int, int64:
			out[key] = item
		default:
			body, _ := json.Marshal(item)
			out[key] = string(body)
		}
	}
	return out
}

func clean(value map[string]any) map[string]any {
	out := map[string]any{}
	for key, item := range value {
		if item == nil || item == "" {
			continue
		}
		if object, ok := item.(map[string]any); ok && len(object) == 0 {
			continue
		}
		out[key] = item
	}
	return out
}

func environment() map[string]string {
	out := map[string]string{}
	for _, entry := range os.Environ() {
		if key, value, found := strings.Cut(entry, "="); found {
			out[key] = value
		}
	}
	return out
}

func firstEnv(env map[string]string, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(env[name]); value != "" {
			return value
		}
	}
	return ""
}

func firstString(values map[string]any, names ...string) string {
	for _, name := range names {
		if value := strings.TrimSpace(toString(values[name])); value != "" {
			return value
		}
	}
	return ""
}

func toString(value any) string {
	switch current := value.(type) {
	case string:
		return current
	case json.Number:
		return current.String()
	case float64:
		return strconv.FormatFloat(current, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(current)
	default:
		return ""
	}
}

func normalizedPath(value, fallback string) string {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if value == "" {
		return fallback
	}
	return value
}

func integer(value any, fallback int) int {
	switch current := value.(type) {
	case int:
		return current
	case int64:
		return int(current)
	case float64:
		return int(current)
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(current))
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func boolean(value any) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(toString(value))) {
	case "1", "true", "yes", "on":
		return true, true
	case "0", "false", "no", "off":
		return false, true
	}
	if current, ok := value.(bool); ok {
		return current, true
	}
	return false, false
}

func boolValue(value any) bool {
	result, _ := boolean(value)
	return result
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
