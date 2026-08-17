package install

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
)

type CodexOptions struct {
	Home                  string
	SourceExecutable      string
	DestinationExecutable string
	HooksFile             string
	ConfigFile            string
	Endpoint              string
	TracePath             string
	MetricsPath           string
	InstallType           string
	XToken                string
	Headers               []string
	ResourceAttributes    []string
	CaptureContent        string
	MaxChars              int
	Enabled               *bool
	NoConfig              bool
	SkipTrust             bool
	CodexCommand          string
	TrustTimeout          time.Duration
}

type CodexResult struct {
	Executable   string
	HooksFile    string
	ConfigFile   string
	Configured   bool
	TrustSkipped bool
}

func InstallUsage() string {
	return `usage: obs-agent-connector install <claude|codex> [options]

Options:
  --type <gtrace|otlp>       upload protocol/path defaults (default: gtrace)
  --endpoint <url>           GTrace or OTLP HTTP endpoint
  --trace-path <path>        override the trace upload path
  --metrics-path <path>      override the metrics upload path
  --x-token <token>          add the GTrace X-Token header
  --header <KEY=VALUE>       add an HTTP header; may be repeated
  --tag <KEY=VALUE>          add a resource attribute; may be repeated
  --capture-content <mode>   none, preview, or full
  --max-chars <number>       maximum captured characters per value
  --enable                   enable telemetry
  --disable                  disable telemetry
  --no-config                install the hook without modifying gtrace.json
  --skip-trust               skip the Codex app-server trust handshake
  --home <directory>         override the user home used for Agent files
`
}

func CodexInstallUsage() string {
	return InstallUsage()
}

func InstallCodex(options CodexOptions) (CodexResult, error) {
	home := options.Home
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return CodexResult{}, err
		}
	}
	source := options.SourceExecutable
	if source == "" {
		var err error
		source, err = os.Executable()
		if err != nil {
			return CodexResult{}, err
		}
	}
	destination := options.DestinationExecutable
	if destination == "" {
		name := "obs-agent-connector"
		if runtime.GOOS == "windows" {
			name += ".exe"
		}
		destination = filepath.Join(home, ".local", "bin", name)
	}
	hooksFile := options.HooksFile
	if hooksFile == "" {
		hooksFile = filepath.Join(home, ".codex", "hooks.json")
	}
	configFile := options.ConfigFile
	if configFile == "" {
		configFile = agentfiles.ConfigPath(home, "codex")
	}
	codexCommand := strings.TrimSpace(options.CodexCommand)
	if !options.SkipTrust && codexCommand == "" {
		var resolveErr error
		codexCommand, resolveErr = exec.LookPath("codex")
		if resolveErr != nil {
			return CodexResult{}, fmt.Errorf("resolve Codex CLI for automatic hook trust: %w", resolveErr)
		}
	}

	hooks, err := readJSONObject(hooksFile)
	if err != nil {
		return CodexResult{}, fmt.Errorf("parse Codex hooks: %w", err)
	}
	configValue, configExists, err := readJSONObjectIfExists(configFile)
	if err != nil {
		return CodexResult{}, fmt.Errorf("parse Codex GTrace config: %w", err)
	}
	if !configExists && options.ConfigFile == "" {
		configValue, configExists, err = readJSONObjectIfExists(filepath.Join(home, ".codex", "gtrace.json"))
		if err != nil {
			return CodexResult{}, fmt.Errorf("parse legacy Codex GTrace config: %w", err)
		}
	}

	absoluteSource, err := filepath.Abs(source)
	if err != nil {
		return CodexResult{}, err
	}
	absoluteDestination, err := filepath.Abs(destination)
	if err != nil {
		return CodexResult{}, err
	}
	if absoluteSource != absoluteDestination {
		if err := copyExecutable(absoluteSource, absoluteDestination); err != nil {
			return CodexResult{}, err
		}
	}
	if err := writeCodexHooks(hooksFile, hooks, absoluteDestination); err != nil {
		return CodexResult{}, err
	}

	result := CodexResult{
		Executable: absoluteDestination,
		HooksFile:  hooksFile,
		ConfigFile: configFile,
	}
	if !options.SkipTrust {
		if err := TrustCodexHook(codexCommand, home, options.TrustTimeout); err != nil {
			return result, fmt.Errorf("automatically trust Codex hook: %w", err)
		}
	} else {
		result.TrustSkipped = true
	}

	if options.NoConfig {
		return result, nil
	}
	if !shouldConfigureGTrace(configExists, options) {
		return result, nil
	}
	nextConfig, err := mergeCodexGTraceConfig(configValue, options, configExists)
	if err != nil {
		return result, err
	}
	if err := writeJSONAtomic(configFile, nextConfig); err != nil {
		return result, err
	}
	result.Configured = true
	return result, nil
}

func ParseCodexInstallArgs(args []string) (CodexOptions, error) {
	return ParseInstallArgs(args)
}

func ParseInstallArgs(args []string) (CodexOptions, error) {
	fs := flag.NewFlagSet("install", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var options CodexOptions
	var headers stringList
	var attributes stringList
	var enable bool
	var disable bool
	fs.StringVar(&options.Home, "home", "", "user home")
	fs.StringVar(&options.Home, "codex-home", "", "deprecated alias for --home")
	fs.StringVar(&options.Endpoint, "endpoint", "", "OTLP endpoint")
	fs.StringVar(&options.TracePath, "trace-path", "", "trace path")
	fs.StringVar(&options.MetricsPath, "metrics-path", "", "metrics path")
	fs.StringVar(&options.InstallType, "type", "gtrace", "gtrace or otlp")
	fs.StringVar(&options.XToken, "x-token", "", "GTrace X-Token")
	fs.StringVar(&options.CaptureContent, "capture-content", "", "none, preview, or full")
	fs.IntVar(&options.MaxChars, "max-chars", 0, "maximum captured characters")
	fs.BoolVar(&options.NoConfig, "no-config", false, "do not modify gtrace.json")
	fs.BoolVar(&options.SkipTrust, "skip-trust", false, "do not invoke codex app-server")
	fs.BoolVar(&enable, "enable", false, "enable telemetry")
	fs.BoolVar(&disable, "disable", false, "disable telemetry")
	fs.Var(&headers, "header", "HTTP header KEY=VALUE")
	fs.Var(&attributes, "tag", "resource attribute KEY=VALUE")
	if err := fs.Parse(args); err != nil {
		return CodexOptions{}, err
	}
	if fs.NArg() != 0 {
		return CodexOptions{}, fmt.Errorf("unexpected arguments: %s", strings.Join(fs.Args(), " "))
	}
	if enable && disable {
		return CodexOptions{}, errors.New("--enable and --disable cannot be used together")
	}
	installType := strings.ToLower(strings.TrimSpace(options.InstallType))
	if installType == "otel" {
		installType = "otlp"
	}
	if installType != "gtrace" && installType != "otlp" {
		return CodexOptions{}, fmt.Errorf("unsupported --type %q", options.InstallType)
	}
	options.InstallType = installType
	if options.CaptureContent != "" {
		options.CaptureContent = strings.ToLower(strings.TrimSpace(options.CaptureContent))
		if options.CaptureContent != "none" && options.CaptureContent != "preview" && options.CaptureContent != "full" {
			return CodexOptions{}, fmt.Errorf("unsupported --capture-content %q", options.CaptureContent)
		}
	}
	if options.MaxChars < 0 {
		return CodexOptions{}, errors.New("--max-chars must be positive")
	}
	options.Headers = headers
	options.ResourceAttributes = attributes
	if enable || disable {
		value := enable
		options.Enabled = &value
	}
	return options, nil
}

func writeCodexHooks(path string, settings map[string]any, command string) error {
	if settings == nil {
		settings = map[string]any{}
	}
	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		settings["hooks"] = hooks
	}
	groups, _ := hooks["Stop"].([]any)
	next := make([]any, 0, len(groups)+1)
	for _, group := range groups {
		if !managedCodexHook(group) {
			next = append(next, group)
		}
	}
	next = append(next, map[string]any{
		"hooks": []any{
			map[string]any{
				"type":          "command",
				"command":       codexHookCommand(command),
				"timeout":       60,
				"statusMessage": "Uploading Codex telemetry to GTrace",
			},
		},
	})
	hooks["Stop"] = next
	return writeJSONAtomic(path, settings)
}

func codexHookCommand(executable string) string {
	if strings.ContainsAny(executable, " \t") {
		return `"` + strings.ReplaceAll(executable, `"`, `\"`) + `" hook codex`
	}
	return executable + " hook codex"
}

func managedCodexHook(value any) bool {
	group, ok := value.(map[string]any)
	if !ok {
		return false
	}
	handlers, _ := group["hooks"].([]any)
	for _, item := range handlers {
		handler, ok := item.(map[string]any)
		if !ok {
			continue
		}
		command := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(fmt.Sprint(handler["command"])), `\`, "/"))
		if strings.Contains(command, "obs-agent-connector") ||
			strings.Contains(command, "agent-telemetry") ||
			strings.Contains(command, "gtrace-agent") ||
			strings.Contains(command, "codex-hook") ||
			strings.Contains(command, "codex-otel-plugin") ||
			strings.Contains(command, "codex-observability-plugin") {
			return true
		}
		args, _ := handler["args"].([]any)
		if len(args) >= 2 && fmt.Sprint(args[0]) == "hook" && fmt.Sprint(args[1]) == "codex" {
			return true
		}
	}
	return false
}

func shouldConfigureGTrace(configExists bool, options CodexOptions) bool {
	return configExists || options.Endpoint != "" || options.Enabled != nil ||
		len(options.Headers) > 0 || len(options.ResourceAttributes) > 0 || options.XToken != "" ||
		options.CaptureContent != "" || options.MaxChars > 0
}

func mergeCodexGTraceConfig(current map[string]any, options CodexOptions, existed bool) (map[string]any, error) {
	next := copyJSONObject(current)
	if options.Enabled != nil {
		next["enabled"] = *options.Enabled
	} else if !existed {
		next["enabled"] = true
	}
	if endpoint := strings.TrimSpace(options.Endpoint); endpoint != "" {
		next["endpoint"] = strings.TrimRight(endpoint, "/")
	}
	currentTracePath := strings.Trim(strings.TrimSpace(firstJSONObjectString(next, "tracePath", "trace_path")), "/")
	currentMetricsPath := strings.Trim(strings.TrimSpace(firstJSONObjectString(next, "metricsPath", "metrics_path")), "/")
	tracePath := strings.Trim(strings.TrimSpace(options.TracePath), "/")
	metricsPath := strings.Trim(strings.TrimSpace(options.MetricsPath), "/")
	if tracePath == "" && options.Endpoint != "" && currentTracePath == "" {
		if options.InstallType == "otlp" {
			tracePath = "v1/traces"
		} else {
			tracePath = "v1/write/otel-llm"
		}
	}
	if metricsPath == "" && options.Endpoint != "" && currentMetricsPath == "" {
		if options.InstallType == "otlp" {
			metricsPath = "v1/metrics"
		} else {
			metricsPath = "v1/write/otel-metrics"
		}
	}
	if tracePath != "" {
		next["tracePath"] = tracePath
	}
	if metricsPath != "" {
		next["metricsPath"] = metricsPath
	}
	if options.CaptureContent != "" {
		next["captureContent"] = options.CaptureContent
	}
	if options.MaxChars > 0 {
		next["max_chars"] = options.MaxChars
	}

	headers := stringObject(next["headers"])
	if options.InstallType == "gtrace" && (options.Endpoint != "" || options.XToken != "") {
		if _, exists := headers["To-Headless"]; !exists {
			headers["To-Headless"] = "true"
		}
	}
	if options.XToken != "" {
		headers["X-Token"] = options.XToken
	}
	for _, entry := range options.Headers {
		key, value, ok := splitKeyValue(entry)
		if !ok {
			return nil, fmt.Errorf("invalid --header %q", key)
		}
		headers[key] = value
	}
	if len(headers) > 0 {
		next["headers"] = headers
	}

	resource := objectValue(next["resourceAttributes"])
	for _, entry := range options.ResourceAttributes {
		key, value, ok := splitKeyValue(entry)
		if !ok {
			return nil, fmt.Errorf("invalid --tag %q", key)
		}
		resource[key] = value
	}
	if len(resource) > 0 {
		next["resourceAttributes"] = resource
	}
	return next, nil
}

func readJSONObject(path string) (map[string]any, error) {
	value, _, err := readJSONObjectIfExists(path)
	return value, err
}

func readJSONObjectIfExists(path string) (map[string]any, bool, error) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if strings.TrimSpace(string(body)) == "" {
		return map[string]any{}, true, nil
	}
	var value map[string]any
	if err := json.Unmarshal(body, &value); err != nil {
		return nil, true, err
	}
	if value == nil {
		value = map[string]any{}
	}
	return value, true, nil
}

func copyJSONObject(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func objectValue(value any) map[string]any {
	out := map[string]any{}
	if current, ok := value.(map[string]any); ok {
		for key, item := range current {
			out[key] = item
		}
	}
	return out
}

func stringObject(value any) map[string]string {
	out := map[string]string{}
	for key, item := range objectValue(value) {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			out[key] = text
		}
	}
	return out
}

func firstJSONObjectString(values map[string]any, names ...string) string {
	for _, name := range names {
		value, ok := values[name].(string)
		if ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func splitKeyValue(value string) (string, string, bool) {
	key, item, found := strings.Cut(value, "=")
	key = strings.TrimSpace(key)
	item = strings.TrimSpace(item)
	return key, item, found && key != "" && item != ""
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}
