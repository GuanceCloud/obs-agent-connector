package hook

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/collector"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/sidecar"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/hooklog"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/metrics"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/state"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/util"
)

type Input struct {
	TranscriptPath string `json:"transcript_path"`
}

type Summary struct {
	SessionID   string `json:"session_id"`
	Turns       int    `json:"turns"`
	Spans       int    `json:"spans"`
	Metrics     int    `json:"metrics"`
	TraceBytes  int    `json:"trace_bytes"`
	MetricBytes int    `json:"metric_bytes"`
}

type RunOptions struct {
	Config    *config.Config
	Input     *Input
	ReadInput func() (Input, error)
	Client    *http.Client
}

func RunCLI() int {
	return RunCLIWithOptions(RunOptions{})
}

func RunCLIWithOptions(options RunOptions) int {
	cfg, err := resolveRunConfig(options.Config)
	if err != nil {
		cfg = fallbackConfig()
	}
	options.Config = &cfg
	if err := RunWithOptions(options); err != nil {
		_ = appendLog(cfg, "failed", map[string]any{
			"error": err.Error(),
			"phase": "runHook",
		})
		if cfg.Debug {
			fmt.Fprintf(os.Stderr, "[gtrace-codex-hook] failed: %v\n", err)
		}
		if cfg.FailOnError {
			return 1
		}
	}
	return 0
}

func Run() error {
	return RunWithOptions(RunOptions{})
}

func RunWithOptions(options RunOptions) error {
	cfg, err := resolveRunConfig(options.Config)
	if err != nil {
		return err
	}
	if !cfg.Enabled {
		return appendLog(cfg, "gtrace disabled", nil)
	}

	input, err := resolveRunInput(options)
	if err != nil {
		return err
	}
	if err := appendLog(cfg, hooklog.HookInvoked, map[string]any{
		"pid":             os.Getpid(),
		"cwd":             mustGetwd(),
		"transcript_path": input.TranscriptPath,
	}); err != nil {
		return err
	}
	if input.TranscriptPath == "" {
		return appendLog(cfg, "hook payload missing transcript_path", nil)
	}
	rolloutLock, err := sidecar.AcquireRolloutLock(input.TranscriptPath, cfg.LockStaleMs)
	if err != nil {
		return err
	}
	if rolloutLock == nil {
		return appendLog(cfg, "skipped duplicate hook run", map[string]any{
			"transcript_path": input.TranscriptPath,
		})
	}
	defer func() { _ = sidecar.ReleaseRolloutLock(rolloutLock) }()

	if _, err := WaitForStableTranscript(input.TranscriptPath, 250*time.Millisecond, 2, 2*time.Second); err != nil {
		return err
	}
	collected, err := collector.CollectRollout(input.TranscriptPath, cfg, nil)
	if err != nil {
		return err
	}
	builtMetrics := metrics.Build(collected.Spans)
	if err := appendLog(cfg, hooklog.ParsedTranscript, map[string]any{
		"transcript_path": input.TranscriptPath,
		"turns":           len(collected.Turns),
		"spans":           len(collected.Spans),
		"metrics":         len(builtMetrics),
		"session_id":      collected.SessionMeta.SessionID,
	}); err != nil {
		return err
	}
	if len(collected.Spans) == 0 {
		return nil
	}

	stateRoot := cfg.StateDir
	if stateRoot == "" {
		home, _ := os.UserHomeDir()
		stateRoot = filepath.Join(home, ".codex", "state", "gtrace-agent")
	}
	manager := state.Manager{
		Root:       filepath.Join(stateRoot, "uploads"),
		StaleAfter: time.Duration(cfg.LockStaleMs) * time.Millisecond,
	}
	uploader := transport.Client{
		HTTPClient: options.Client,
		Config: transport.Config{
			Endpoint:    firstNonEmpty(cfg.Endpoint, cfg.BaseURL),
			TracePath:   cfg.TracePath,
			MetricsPath: cfg.MetricsPath,
			TraceURL:    cfg.OtelTracesURL,
			MetricsURL:  cfg.OtelMetricsURL,
			Headers:     cfg.Headers,
			PublicKey:   cfg.PublicKey,
			SecretKey:   cfg.SecretKey,
			Timeout:     time.Duration(cfg.TimeoutMs) * time.Millisecond,
		},
	}
	for _, batch := range collected.TurnBatches {
		if err := uploadTurn(cfg, input.TranscriptPath, collected.SessionMeta.SessionID, batch, manager, uploader); err != nil {
			return err
		}
	}
	return nil
}

func uploadTurn(
	cfg config.Config,
	rolloutFile string,
	sessionID string,
	batch collector.TurnBatch,
	manager state.Manager,
	uploader transport.Client,
) error {
	claim, err := manager.Claim(sessionID, batch.TurnID, batch.Fingerprint)
	if errors.Is(err, state.ErrAlreadyCompleted) || (err == nil && claim == nil) {
		return nil
	}
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			_ = claim.Release()
		}
	}()

	builtMetrics := metrics.Build(batch.Spans)
	tracePayload := proto.EncodeExportTraceServiceRequest(otlp.SpansToProtoRequest(batch.Spans))
	if !claim.SignalWasUploaded("traces") {
		result, err := uploader.Upload("traces", tracePayload)
		if err != nil {
			return err
		}
		if err := claim.MarkSignalUploaded("traces", map[string]any{
			"status": result.StatusCode,
			"bytes":  len(tracePayload),
		}); err != nil {
			return err
		}
		if err := appendLog(cfg, hooklog.UploadedSpans, map[string]any{
			"status": result.StatusCode,
			"spans":  len(batch.Spans),
		}); err != nil {
			return err
		}
	}

	required := []string{"traces"}
	if len(builtMetrics) > 0 {
		required = append(required, "metrics")
		if !claim.SignalWasUploaded("metrics") {
			metricPayload := proto.EncodeExportMetricsServiceRequest(otlp.MetricsToProtoRequest(builtMetrics))
			result, err := uploader.Upload("metrics", metricPayload)
			if err != nil {
				return err
			}
			if err := claim.MarkSignalUploaded("metrics", map[string]any{
				"status": result.StatusCode,
				"bytes":  len(metricPayload),
			}); err != nil {
				return err
			}
			if err := appendLog(cfg, hooklog.UploadedMetrics, map[string]any{
				"status":  result.StatusCode,
				"metrics": len(builtMetrics),
			}); err != nil {
				return err
			}
		}
	}
	if err := claim.Complete(required...); err != nil {
		return err
	}
	completed = true
	sidecar.MarkTurnUploaded(rolloutFile, batch.TurnID, batch.Fingerprint)
	return nil
}

func resolveRunConfig(explicit *config.Config) (config.Config, error) {
	if explicit != nil {
		return *explicit, nil
	}
	return safeResolveConfig()
}

func resolveRunInput(options RunOptions) (Input, error) {
	if options.Input != nil {
		return *options.Input, nil
	}
	if options.ReadInput != nil {
		return options.ReadInput()
	}
	var input Input
	if err := util.ReadStdinJSON(&input); err != nil {
		return Input{}, err
	}
	return input, nil
}

func safeResolveConfig() (config.Config, error) {
	home, _ := os.UserHomeDir()
	cwd, _ := os.Getwd()
	cfg, err := config.Resolve(config.ResolveOptions{Home: home, Cwd: cwd})
	if err == nil {
		return cfg, nil
	}
	return fallbackConfig(), nil
}

func fallbackConfig() config.Config {
	home, _ := os.UserHomeDir()
	return config.Config{
		Debug:       false,
		FailOnError: false,
		HookLogFile: filepath.Join(home, ".codex", "gtrace-hook.log"),
		StateDir:    filepath.Join(home, ".codex", "state", "gtrace-agent"),
	}
}

func appendLog(cfg config.Config, message string, extra map[string]any) error {
	// Hook logging is diagnostic and must never block telemetry collection.
	_ = hooklog.Append(cfg.HookLogFile, message, extra)
	return nil
}

func WaitForStableTranscript(file string, settle time.Duration, stableChecks int, maxWait time.Duration) (os.FileInfo, error) {
	deadline := time.Now().Add(maxWait)
	previous, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	stableCount := 0

	for time.Now().Before(deadline) {
		time.Sleep(settle)
		current, err := os.Stat(file)
		if err != nil {
			return nil, err
		}
		if current.Size() == previous.Size() && current.ModTime() == previous.ModTime() {
			stableCount++
			if stableCount >= stableChecks {
				return current, nil
			}
			continue
		}
		previous = current
		stableCount = 0
	}
	return previous, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func mustGetwd() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return wd
}

func FormatSummary(summary Summary) string {
	return fmt.Sprintf("%s:%d", summary.SessionID, summary.Turns)
}
