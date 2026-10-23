package install

import (
	"flag"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type CodexSandboxOptions struct {
	LogsPath   *string
	Enabled    *bool
	MaxPending *int
}

func (o CodexSandboxOptions) Specified() bool {
	return o.LogsPath != nil || o.Enabled != nil || o.MaxPending != nil
}

// RegisterCodexSandboxFlags parses explicit edits, including empty route resets.
// Runtime config generation remains owned by this installer.
func RegisterCodexSandboxFlags(fs *flag.FlagSet) func() (CodexSandboxOptions, error) {
	path := fs.String("logs-path", "", "Codex sandbox Logs path relative to endpoint")
	enabled := fs.String("sandbox-enabled", "", "Codex sandbox bridge true or false; an external runtime producer is required")
	maxPending := fs.String("sandbox-max-pending", "", "Codex sandbox pending Logs limit, from 1 to 4096")
	return func() (CodexSandboxOptions, error) {
		var out CodexSandboxOptions
		var parseErr error
		fs.Visit(func(f *flag.Flag) {
			switch f.Name {
			case "logs-path":
				value := strings.Trim(strings.TrimSpace(*path), "/")
				out.LogsPath = &value
			case "sandbox-enabled":
				value, err := strconv.ParseBool(*enabled)
				if err != nil {
					parseErr = fmt.Errorf("--sandbox-enabled must be true or false")
				} else {
					out.Enabled = &value
				}
			case "sandbox-max-pending":
				value, err := strconv.Atoi(*maxPending)
				if err != nil || value < 1 || value > 4096 {
					parseErr = fmt.Errorf("--sandbox-max-pending must be between 1 and 4096")
				} else {
					out.MaxPending = &value
				}
			}
		})
		return out, parseErr
	}
}

func MergeCodexRuntimeConfig(current map[string]any, options CodexOptions, existed bool) (map[string]any, error) {
	next, err := mergeCodexGTraceConfig(current, options, existed)
	if err != nil {
		return nil, err
	}
	delete(next, "otel_logs_url")
	if _, ok := next["logsPath"]; !ok {
		next["logsPath"] = ""
	}
	// Seed only recognized upload profiles. Preserve custom routes, and apply
	// explicit edits below so an empty route reset is still honored.
	if firstJSONObjectString(next, "logsPath") == "" {
		tracePath := strings.Trim(strings.TrimSpace(firstJSONObjectString(next, "tracePath", "trace_path")), "/")
		metricsPath := strings.Trim(strings.TrimSpace(firstJSONObjectString(next, "metricsPath", "metrics_path")), "/")
		switch {
		case tracePath == "v1/write/otel-llm" && metricsPath == "v1/write/otel-metrics":
			next["logsPath"] = "v1/write/otel-logs"
		case tracePath == "v1/traces" && metricsPath == "v1/metrics":
			next["logsPath"] = "v1/logs"
		}
	}
	if value, ok := next["sandbox"]; ok {
		if _, valid := value.(map[string]any); !valid {
			return nil, fmt.Errorf("Codex sandbox config must be an object")
		}
	}
	sandbox := objectValue(next["sandbox"])
	if _, ok := sandbox["enabled"]; !ok {
		sandbox["enabled"] = false
	}
	if _, ok := sandbox["max_pending"]; !ok {
		sandbox["max_pending"] = 1024
	}
	o := options.Sandbox
	if o.LogsPath != nil {
		value := *o.LogsPath
		if !validCodexLogsPath(value) {
			return nil, fmt.Errorf("Logs path must be a relative path without whitespace, query, or fragment")
		}
		next["logsPath"] = value
	}
	if o.Enabled != nil {
		sandbox["enabled"] = *o.Enabled
	}
	if o.MaxPending != nil {
		if *o.MaxPending < 1 || *o.MaxPending > 4096 {
			return nil, fmt.Errorf("sandbox pending limit must be between 1 and 4096")
		}
		sandbox["max_pending"] = *o.MaxPending
	}
	if o.Specified() && sandbox["enabled"] == true {
		path, _ := next["logsPath"].(string)
		base := firstJSONObjectString(next, "endpoint", "base_url")
		if path == "" || !validCodexLogsPath(path) || !validCodexLogsEndpoint(base) {
			return nil, fmt.Errorf("sandbox bridge requires a Logs path with a valid endpoint")
		}
	}
	next["sandbox"] = sandbox
	return next, nil
}

func validCodexLogsPath(value string) bool {
	u, err := url.Parse(value)
	return err == nil && !u.IsAbs() && u.Host == "" && u.RawQuery == "" && u.Fragment == "" && !strings.ContainsAny(value, "\r\n\t ")
}

func validCodexLogsEndpoint(value string) bool {
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil && u.Fragment == ""
}
