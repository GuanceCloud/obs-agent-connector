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
	"github.com/GuanceCloud/obs-agent-connector/internal/core/util"
)

type Config struct {
	Enabled                       bool
	Transport                     transport.Config
	ResourceAttributes            map[string]any
	CaptureContent                string
	CaptureThinking               bool
	MaxChars                      int
	Debug                         bool
	ProfileDir, StateDir, LogFile string
}
type ResolveOptions struct {
	Home, ProfileDir string
	Env              map[string]string
}

func ProfileDir(home string) string {
	return filepath.Join(home, ".qwenworkcn")
}

func Resolve(o ResolveOptions) Config {
	if o.Home == "" {
		o.Home, _ = os.UserHomeDir()
	}
	if o.Env == nil {
		o.Env = map[string]string{}
		for _, e := range os.Environ() {
			k, v, ok := strings.Cut(e, "=")
			if ok {
				o.Env[k] = v
			}
		}
	}
	profile := o.ProfileDir
	if profile == "" {
		profile = ProfileDir(o.Home)
	}
	values := map[string]any{}
	for _, path := range []string{agentfiles.ConfigPath(o.Home, "qwenwork-cn")} {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var v map[string]any
		if json.Unmarshal(body, &v) == nil {
			merge(values, normalize(v))
		}
	}
	env := o.Env
	for key, names := range map[string][]string{
		"enabled": {"QWENWORK_CN_OTEL_ENABLED"}, "endpoint": {"OTEL_EXPORTER_OTLP_ENDPOINT", "QWENWORK_CN_OTEL_ENDPOINT", "GTRACE_ENDPOINT"},
		"otel_traces_url": {"OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"}, "otel_metrics_url": {"OTEL_EXPORTER_OTLP_METRICS_ENDPOINT"},
		"tracePath": {"QWENWORK_CN_OTEL_TRACE_PATH", "GTRACE_TRACE_PATH"}, "metricsPath": {"QWENWORK_CN_OTEL_METRICS_PATH", "GTRACE_METRICS_PATH"},
		"captureContent": {"QWENWORK_CN_OTEL_CAPTURE_CONTENT"}, "maxChars": {"QWENWORK_CN_OTEL_MAX_CHARS"},
		"timeoutMs": {"QWENWORK_CN_OTEL_TIMEOUT_MS", "OTEL_EXPORTER_OTLP_TIMEOUT"}, "debug": {"QWENWORK_CN_OTEL_DEBUG"},
		"stateDir": {"QWENWORK_CN_OTEL_STATE_DIR"},
	} {
		for _, name := range names {
			if env[name] != "" {
				values[key] = env[name]
				break
			}
		}
	}
	// Presence matters for this opt-in: even an empty or malformed environment
	// override must disable capture instead of inheriting an enabled file value.
	if value, ok := env["QWENWORK_CN_OTEL_CAPTURE_THINKING"]; ok {
		values["captureThinking"] = value
	}
	headers := object(values["headers"])
	for k, v := range assignments(env["OTEL_EXPORTER_OTLP_HEADERS"]) {
		headers[k] = v
	}
	if token := first(env["GTRACE_X_TOKEN"], env["X_TOKEN"]); token != "" {
		headers["X-Token"] = token
	}
	h := map[string]string{}
	for k, v := range headers {
		h[k] = text(v)
	}
	attrs := map[string]any{"service.name": "gtrace-qwenwork-cn", "agent_runtime": "qwenwork-cn", "telemetry.sdk.language": "go"}
	if host, err := os.Hostname(); err == nil {
		attrs["host"] = host
	}
	for k, v := range object(values["resourceAttributes"]) {
		if util.IsPrimitive(v) {
			attrs[k] = v
		}
	}
	for k, v := range assignments(env["OTEL_RESOURCE_ATTRIBUTES"]) {
		if util.IsPrimitive(v) {
			attrs[k] = v
		}
	}
	endpoint := text(values["endpoint"])
	tracePath := first(text(values["tracePath"]), "v1/write/otel-llm")
	metricPath := first(text(values["metricsPath"]), "v1/write/otel-metrics")
	if env["OTEL_EXPORTER_OTLP_ENDPOINT"] != "" {
		if values["tracePath"] == nil {
			tracePath = "v1/traces"
		}
		if values["metricsPath"] == nil {
			metricPath = "v1/metrics"
		}
	}
	capture := strings.ToLower(text(values["captureContent"]))
	switch capture {
	case "false", "0", "none":
		capture = "none"
	case "full":
	default:
		capture = "preview"
	}
	stateDir := first(text(values["stateDir"]), filepath.Join(agentfiles.Directory(o.Home, "qwenwork-cn"), "state"))
	if strings.HasPrefix(stateDir, "~/") {
		stateDir = filepath.Join(o.Home, stateDir[2:])
	}
	return Config{
		Enabled:            boolean(values["enabled"], endpoint != "" || text(values["otel_traces_url"]) != ""),
		Transport:          transport.Config{Endpoint: endpoint, TracePath: tracePath, MetricsPath: metricPath, TraceURL: text(values["otel_traces_url"]), MetricsURL: text(values["otel_metrics_url"]), Headers: h, PublicKey: text(values["public_key"]), SecretKey: text(values["secret_key"]), Timeout: time.Duration(integer(values["timeoutMs"], 25000, 120000)) * time.Millisecond},
		ResourceAttributes: attrs, CaptureContent: capture, MaxChars: integer(values["maxChars"], 20000, 100000), Debug: boolean(values["debug"], false),
		CaptureThinking: capture != "none" && boolean(values["captureThinking"], false),
		ProfileDir:      profile, StateDir: stateDir, LogFile: agentfiles.HookLogPath(o.Home, "qwenwork-cn"),
	}
}

func normalize(v map[string]any) map[string]any {
	// A present canonical value wins, including false or malformed/null values.
	if _, exists := v["captureThinking"]; !exists {
		v["captureThinking"] = v["capture_thinking"]
	}
	for old, key := range map[string]string{"base_url": "endpoint", "capture_content": "captureContent", "max_chars": "maxChars", "timeout_ms": "timeoutMs", "trace_path": "tracePath", "metrics_path": "metricsPath"} {
		if v[key] == nil && v[old] != nil {
			v[key] = v[old]
		}
	}
	return v
}
func merge(a, b map[string]any) {
	for k, v := range b {
		if k == "headers" || k == "resourceAttributes" {
			m := object(a[k])
			for x, y := range object(v) {
				m[x] = y
			}
			a[k] = m
		} else {
			a[k] = v
		}
	}
}
func object(v any) map[string]any {
	if m, ok := v.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}
func assignments(s string) map[string]any {
	m := map[string]any{}
	if json.Unmarshal([]byte(s), &m) == nil && m != nil {
		return m
	}
	m = map[string]any{}
	for _, e := range strings.Split(s, ",") {
		k, v, ok := strings.Cut(e, "=")
		if ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}
func text(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	}
	return ""
}
func first(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
func boolean(v any, fallback bool) bool {
	switch strings.ToLower(text(v)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return fallback
}
func integer(v any, fallback, limit int) int {
	i, e := strconv.Atoi(text(v))
	if e != nil || i <= 0 || i > limit {
		return fallback
	}
	return i
}
