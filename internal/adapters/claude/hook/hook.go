package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/buildinfo"
	claudeconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/config"
	claudeparse "github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/metrics"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/state"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

type RunOptions struct {
	Config     *claudeconfig.Config
	Payload    map[string]any
	ReadInput  func() (map[string]any, error)
	HTTPClient *http.Client
	SkipWait   bool
}

func RunCLI() int {
	cwd, _ := os.Getwd()
	cfg := claudeconfig.Resolve(claudeconfig.ResolveOptions{Cwd: cwd})
	if !cfg.Enabled {
		return 0
	}
	if err := RunWithOptions(RunOptions{Config: &cfg}); err != nil {
		appendLog(cfg, "failed", map[string]any{"error": err.Error()})
		if cfg.Debug {
			fmt.Fprintf(os.Stderr, "[gtrace-claude-hook] %v\n", err)
		}
	}
	return 0
}

func RunWithOptions(options RunOptions) error {
	cfg := claudeconfig.Config{}
	if options.Config != nil {
		cfg = *options.Config
	} else {
		cfg = claudeconfig.Resolve(claudeconfig.ResolveOptions{})
	}
	if !cfg.Enabled {
		return nil
	}
	payload, err := resolvePayload(options)
	if err != nil {
		return err
	}
	parsedPayload := parsePayload(payload)
	if parsedPayload.Cwd != "" && options.Config == nil {
		cfg = claudeconfig.Resolve(claudeconfig.ResolveOptions{Cwd: parsedPayload.Cwd})
		if !cfg.Enabled {
			return nil
		}
	}
	if parsedPayload.SessionID == "" || parsedPayload.TranscriptPath == "" {
		return errors.New("hook payload is missing session_id or transcript_path")
	}
	absoluteTranscript, err := filepath.Abs(parsedPayload.TranscriptPath)
	if err != nil {
		return err
	}
	parsedPayload.TranscriptPath = absoluteTranscript
	if !options.SkipWait {
		if err := waitForStableTranscript(absoluteTranscript, 250*time.Millisecond, 2, 2*time.Second); err != nil {
			return err
		}
	}
	messages, err := claudeparse.ReadTranscript(absoluteTranscript)
	if err != nil {
		return err
	}
	turns := claudeparse.Normalize(parsedPayload, cfg, messages)
	if len(turns) == 0 {
		return nil
	}

	builder := semantic.Builder{
		ScopeName:    "gtrace-claude-collector",
		ScopeVersion: buildinfo.Version,
	}
	manager := state.Manager{
		Root:       filepath.Join(cfg.StateDir, "uploads"),
		StaleAfter: 10 * time.Minute,
	}
	uploader := transport.Client{Config: cfg.Transport, HTTPClient: options.HTTPClient}
	hasMetricsEndpoint := cfg.Transport.MetricsURL != "" || cfg.Transport.Endpoint != ""

	for _, turn := range turns {
		fingerprint := turnFingerprint(turn)
		claim, claimErr := manager.Claim(turn.SessionID, turn.TurnID, fingerprint)
		if errors.Is(claimErr, state.ErrAlreadyCompleted) || (claimErr == nil && claim == nil) {
			continue
		}
		if claimErr != nil {
			appendLog(cfg, "claim failed", map[string]any{"turn_id_hash": shortHash(turn.TurnID), "error": claimErr.Error()})
			continue
		}
		completed := false
		func() {
			defer func() {
				if !completed {
					_ = claim.Release()
				}
			}()
			spans := builder.Build(turn)
			if len(spans) == 0 {
				return
			}
			builtMetrics := metrics.Build(spans)
			tracePayload := proto.EncodeExportTraceServiceRequest(otlp.SpansToProtoRequest(spans))
			if !claim.SignalWasUploaded("traces") {
				result, uploadErr := uploader.Upload("traces", tracePayload)
				if uploadErr != nil {
					appendLog(cfg, "trace upload failed", map[string]any{
						"turn_id_hash": shortHash(turn.TurnID),
						"error":        uploadErr.Error(),
						"status":       result.StatusCode,
					})
					return
				}
				if err := claim.MarkSignalUploaded("traces", map[string]any{
					"status": result.StatusCode,
					"bytes":  len(tracePayload),
				}); err != nil {
					appendLog(cfg, "trace state failed", map[string]any{"error": err.Error()})
					return
				}
			}

			required := []string{"traces"}
			if len(builtMetrics) > 0 && hasMetricsEndpoint {
				required = append(required, "metrics")
				if !claim.SignalWasUploaded("metrics") {
					metricPayload := proto.EncodeExportMetricsServiceRequest(otlp.MetricsToProtoRequest(builtMetrics))
					result, uploadErr := uploader.Upload("metrics", metricPayload)
					if uploadErr != nil {
						appendLog(cfg, "metric upload failed", map[string]any{
							"turn_id_hash": shortHash(turn.TurnID),
							"error":        uploadErr.Error(),
							"status":       result.StatusCode,
						})
						return
					}
					if err := claim.MarkSignalUploaded("metrics", map[string]any{
						"status": result.StatusCode,
						"bytes":  len(metricPayload),
					}); err != nil {
						appendLog(cfg, "metric state failed", map[string]any{"error": err.Error()})
						return
					}
				}
			}
			if err := claim.Complete(required...); err != nil {
				appendLog(cfg, "completion state failed", map[string]any{"error": err.Error()})
				return
			}
			completed = true
			appendLog(cfg, "turn uploaded", map[string]any{
				"turn_id_hash": shortHash(turn.TurnID),
				"spans":        len(spans),
				"metrics":      len(builtMetrics),
			})
		}()
	}
	return nil
}

func resolvePayload(options RunOptions) (map[string]any, error) {
	if options.Payload != nil {
		return options.Payload, nil
	}
	if options.ReadInput != nil {
		return options.ReadInput()
	}
	body, err := io.ReadAll(io.LimitReader(os.Stdin, 4*1024*1024))
	if err != nil {
		return nil, err
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		return nil, errors.New("hook input is empty")
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func parsePayload(value map[string]any) claudeparse.HookPayload {
	sessionID := firstString(value, "session_id", "sessionId")
	if sessionID == "" {
		if session, ok := value["session"].(map[string]any); ok {
			sessionID = firstString(session, "id")
		}
	}
	transcript := firstString(value, "transcript_path", "transcriptPath")
	if transcript == "" {
		if item, ok := value["transcript"].(map[string]any); ok {
			transcript = firstString(item, "path")
		}
	}
	return claudeparse.HookPayload{
		SessionID:      sessionID,
		TranscriptPath: transcript,
		Cwd:            firstString(value, "cwd"),
		EventName:      firstString(value, "hook_event_name", "hookEventName", "event", "event_name"),
		Version:        firstString(value, "version"),
	}
}

func waitForStableTranscript(path string, settle time.Duration, stableChecks int, maxWait time.Duration) error {
	previous, err := os.Stat(path)
	if err != nil {
		return err
	}
	deadline := time.Now().Add(maxWait)
	stable := 0
	for time.Now().Before(deadline) {
		time.Sleep(settle)
		current, err := os.Stat(path)
		if err != nil {
			return err
		}
		if current.Size() == previous.Size() && current.ModTime() == previous.ModTime() {
			stable++
			if stable >= stableChecks {
				return nil
			}
		} else {
			stable = 0
			previous = current
		}
	}
	return nil
}

func appendLog(cfg claudeconfig.Config, message string, extra map[string]any) {
	payload := map[string]any{
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
		"message": message,
	}
	if extra != nil {
		payload["extra"] = extra
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return
	}
	if os.MkdirAll(filepath.Dir(cfg.LogFile), 0o700) != nil {
		return
	}
	file, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(body, '\n'))
}

func turnFingerprint(value any) string {
	body, _ := json.Marshal(value)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return text
		}
	}
	return ""
}
