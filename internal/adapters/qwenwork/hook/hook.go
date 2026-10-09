package hook

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/buildinfo"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/diagnostics"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/hooklog"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/metrics"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/state"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

type RunOptions struct {
	Config     config.Config
	Input      parse.HookInput
	HTTPClient *http.Client
	SkipWait   bool
}

// RunCLI always fails open for product Hooks; worker failures remain visible in the log.
func RunCLI(args []string) int {
	profile := ""
	if len(args) >= 2 && args[0] == "--profile" {
		profile = args[1]
		args = args[2:]
	}
	cfg := config.Resolve(config.ResolveOptions{ProfileDir: profile})
	if !cfg.Enabled {
		return 0
	}
	if len(args) == 2 && args[0] == "worker" {
		if err := runWorker(args[1], cfg); err != nil {
			log(cfg, "worker failed", map[string]any{"error": err.Error()})
			return 1
		}
		return 0
	}
	if len(args) != 0 {
		return 0
	}
	var input parse.HookInput
	if err := json.NewDecoder(io.LimitReader(os.Stdin, 1<<20)).Decode(&input); err != nil {
		return 0
	}
	if !parse.Supported(input.Event) || input.SessionID == "" || input.TranscriptPath == "" {
		return 0
	}
	input.ObservedAt = time.Now().UnixNano()
	input.InputKey = parse.ToolKey(input.ToolName, input.ToolInput)
	input.ToolInput = nil // Keep only a hash for matching; never persist the raw Hook payload.
	log(cfg, hooklog.HookInvoked, map[string]any{"event": input.Event})
	if err := record(cfg, input); err != nil {
		log(cfg, "journal failed", map[string]any{"error": err.Error()})
		return 0
	}
	if !parse.Replay(input.Event) {
		return 0
	}
	if err := enqueue(cfg, input); err != nil {
		log(cfg, "queue failed", map[string]any{"error": err.Error()})
	}
	return 0
}

func journalDir(cfg config.Config, in parse.HookInput) string {
	return filepath.Join(cfg.StateDir, "events", parse.Hash(in.SessionID+"\x00"+in.TranscriptPath))
}
func record(cfg config.Config, in parse.HookInput) error {
	_, err := writeEvent(journalDir(cfg, in), in)
	return err
}
func writeEvent(dir string, in parse.HookInput) (string, error) {
	in.ToolInput = nil
	// Runtime reasons are not guaranteed to be enums; never journal free text.
	switch in.Reason {
	case "cancelled", "canceled", "aborted", "interrupted", "error", "failed":
	default:
		in.Reason = ""
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".event-*")
	if err != nil {
		return "", err
	}
	path := f.Name()
	defer os.Remove(path)
	if err = json.NewEncoder(f).Encode(in); err != nil {
		f.Close()
		return "", err
	}
	if err = f.Close(); err != nil {
		return "", err
	}
	final := path + ".json"
	if err = os.Rename(path, final); err != nil {
		return "", err
	}
	return final, nil
}
func readEvents(cfg config.Config, in parse.HookInput) []parse.HookInput {
	var events []parse.HookInput
	files, _ := filepath.Glob(filepath.Join(journalDir(cfg, in), "*.json"))
	for _, path := range files {
		body, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var event parse.HookInput
		if json.Unmarshal(body, &event) == nil && event.SessionID == in.SessionID && event.TranscriptPath == in.TranscriptPath {
			events = append(events, event)
		}
	}
	return events
}
func enqueue(cfg config.Config, in parse.HookInput) error {
	path, err := writeEvent(filepath.Join(cfg.StateDir, "queue"), in)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	command := exec.Command(executable, "hook", "qwenwork-cn", "--profile", cfg.ProfileDir, "worker", path)
	command.Stdin, command.Stdout, command.Stderr = null, null, null
	if err = command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}
func runWorker(path string, cfg config.Config) error {
	// A new terminal event also retries retained queues from an earlier upload failure.
	paths, _ := filepath.Glob(filepath.Join(cfg.StateDir, "queue", "*.json"))
	if len(paths) == 0 {
		paths = []string{path}
	}
	paths = coalesceQueues(cfg, paths)
	var firstErr error
	for _, p := range paths {
		body, err := os.ReadFile(p)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		var in parse.HookInput
		if err = json.Unmarshal(body, &in); err == nil {
			err = Run(RunOptions{Config: cfg, Input: in})
		}
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		_ = os.Remove(p)
	}
	return firstErr
}

// Replay always reads the entire transcript and journal. Keep one retry per
// session/transcript instead of accumulating another expensive retry per prompt.
// Only remove files from this snapshot, so newly enqueued events remain intact.
func coalesceQueues(cfg config.Config, paths []string) []string {
	kept := map[string]string{}
	var result []string
	for _, path := range paths {
		body, err := os.ReadFile(path)
		var in parse.HookInput
		if err != nil || json.Unmarshal(body, &in) != nil || in.SessionID == "" || in.TranscriptPath == "" {
			result = append(result, path)
			continue
		}
		key := journalDir(cfg, in)
		if _, ok := kept[key]; !ok {
			kept[key] = path
			result = append(result, path)
			continue
		}
		// Preserve terminal evidence even for queues restored independently.
		if record(cfg, in) == nil {
			_ = os.Remove(path)
		} else {
			result = append(result, path)
		}
	}
	return result
}

func Run(o RunOptions) error {
	cfg, in := o.Config, o.Input
	if !cfg.Enabled {
		return nil
	}
	if !parse.Replay(in.Event) {
		return nil
	}
	// Wait for a stable snapshot before parsing, then retry incomplete terminal data.
	if !o.SkipWait {
		var previous string
		for i := 0; i < 8; i++ {
			info, err := os.Stat(in.TranscriptPath)
			signature := ""
			if err == nil {
				signature = fmt.Sprintf("%d:%d", info.Size(), info.ModTime().UnixNano())
			}
			if signature != "" && signature == previous {
				break
			}
			previous = signature
			time.Sleep(250 * time.Millisecond)
		}
	}
	deadline := time.Now().Add(2 * time.Second)
	observedAt := time.Now().UnixNano()
	var turns []model.Turn
	incomplete := false
	for {
		observedAt++ // Keep successive snapshots ordered within this replay.
		current, pending, usage, err := parse.ReadWithUsage(in, cfg, readEvents(cfg, in))
		if err == nil {
			_ = diagnostics.Write(cfg.StateDir, diagnostics.Observation{Kind: "usage", At: observedAt, SourceAt: usage.ObservedAt, Calls: usage.LLMCalls, ObservedCalls: usage.ObservedCalls, Fields: usage.Fields})
		}
		turns = current
		if err == nil && !pending {
			break
		}
		if o.SkipWait || time.Now().After(deadline) {
			if err != nil {
				replayDiagnostic(cfg, observedAt, "parse_failed")
				return err
			}
			if pending {
				incomplete = true
			}
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	for _, turn := range turns {
		if err := export(cfg, turn, o.HTTPClient); err != nil {
			replayDiagnostic(cfg, observedAt, "export_failed")
			return err
		}
	}
	log(cfg, hooklog.ParsedTranscript, map[string]any{"turns": len(turns)})
	if incomplete {
		replayDiagnostic(cfg, observedAt, "incomplete")
		return fmt.Errorf("QwenWorkCN transcript is incomplete; retained for retry")
	}
	replayDiagnostic(cfg, observedAt, "complete")
	return nil
}

func replayDiagnostic(cfg config.Config, at int64, outcome string) {
	_ = diagnostics.Write(cfg.StateDir, diagnostics.Observation{Kind: "replay", At: at, Outcome: outcome})
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }
func export(cfg config.Config, turn model.Turn, httpClient *http.Client) error {
	manager := state.Manager{Root: filepath.Join(cfg.StateDir, "uploads")}
	claim, err := manager.Claim(turn.SessionID, turn.TurnID, parse.Hash(turn.TurnID))
	if errors.Is(err, state.ErrAlreadyCompleted) {
		return nil
	}
	if err != nil {
		return err
	}
	if claim == nil {
		return fmt.Errorf("QwenWorkCN turn upload is already active")
	}
	defer claim.Release()
	spans := (semantic.Builder{ScopeName: "gtrace-qwenwork-cn-collector", ScopeVersion: buildinfo.Version}).Build(turn)
	stableSpanIDs(spans, turn)
	if len(spans) == 0 {
		return nil
	}
	client := transport.Client{Config: cfg.Transport, HTTPClient: httpClient}
	signals := []string{"traces"}
	if cfg.Transport.Endpoint != "" || cfg.Transport.MetricsURL != "" {
		signals = append(signals, "metrics")
	}
	for _, signal := range signals {
		if claim.SignalWasUploaded(signal) {
			continue
		}
		var payload []byte
		if signal == "traces" {
			payload = proto.EncodeExportTraceServiceRequest(otlp.SpansToProtoRequest(spans))
		} else {
			payload = proto.EncodeExportMetricsServiceRequest(otlp.MetricsToProtoRequest(metrics.Build(spans)))
		}
		result, err := client.Upload(signal, payload)
		if err != nil {
			_ = diagnostics.Write(cfg.StateDir, diagnostics.Observation{Kind: signal, At: time.Now().UnixNano(), Outcome: "failed"})
			return err
		}
		_ = diagnostics.Write(cfg.StateDir, diagnostics.Observation{Kind: signal, At: time.Now().UnixNano(), Outcome: "accepted"})
		if err = claim.MarkSignalUploaded(signal, map[string]any{"status": result.StatusCode}); err != nil {
			_ = diagnostics.Write(cfg.StateDir, diagnostics.Observation{Kind: signal, At: time.Now().UnixNano(), Outcome: "checkpoint_failed"})
			return err
		}
		message := hooklog.UploadedSpans
		if signal == "metrics" {
			message = hooklog.UploadedMetrics
		}
		log(cfg, message, map[string]any{"status": result.StatusCode})
	}
	if err = claim.Complete(signals...); err != nil {
		return err
	}
	log(cfg, "turn uploaded", nil)
	return nil
}
func log(cfg config.Config, message string, fields map[string]any) {
	_ = hooklog.Append(cfg.LogFile, strings.TrimSpace(message), fields)
}

// Retries retain the same trace and span identities, including LLM links.
func stableSpanIDs(spans []model.Span, turn model.Turn) {
	seed := "qwenwork-cn:" + turn.SessionID + ":" + turn.TurnID
	ids := map[string]string{}
	for i, s := range spans {
		ids[s.SpanID] = parse.Hash(fmt.Sprintf("%s:span:%d", seed, i))[:16]
	}
	for i := range spans {
		s := &spans[i]
		s.TraceID = parse.Hash(seed)[:32]
		s.SpanID = ids[s.SpanID]
		if s.ParentID != "" {
			s.ParentID = ids[s.ParentID]
		}
		if old, ok := s.Attributes["triggered_by.llm_span_id"].(string); ok {
			s.Attributes["triggered_by.llm_span_id"] = ids[old]
		}
	}
}
