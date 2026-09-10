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
	"regexp"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/workbuddy/buildinfo"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/workbuddy/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/workbuddy/parse"
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
	if !parse.Terminal(input.Event) {
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
	command := exec.Command(executable, "hook", "workbuddy", "--profile", cfg.ProfileDir, "worker", path)
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

func Run(o RunOptions) error {
	cfg, in := o.Config, o.Input
	if !cfg.Enabled {
		return nil
	}
	if !parse.Terminal(in.Event) {
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
	var turns []model.Turn
	incomplete := false
	for {
		current, pending, err := parse.Read(in, cfg, readEvents(cfg, in))
		turns = current
		if err == nil && !pending {
			break
		}
		if o.SkipWait || time.Now().After(deadline) {
			if err != nil {
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
			return err
		}
	}
	log(cfg, hooklog.ParsedTranscript, map[string]any{"turns": len(turns)})
	if incomplete {
		return fmt.Errorf("WorkBuddy transcript is incomplete; retained for retry")
	}
	return nil
}

var unsafeFilename = regexp.MustCompile(`[^A-Za-z0-9_.-]`)

func legacyTurnDir(cfg config.Config, turn model.Turn) string {
	safe := func(s string) string {
		s = unsafeFilename.ReplaceAllString(s, "_")
		if len(s) > 180 {
			s = s[:180]
		}
		if s == "" || s == "." || s == ".." {
			s = "unknown"
		}
		return s
	}
	return filepath.Join(cfg.ProfileDir, "plugins", "data", "workbuddy-otel-plugin", "uploads", safe(turn.SessionID), safe(turn.TurnID))
}
func exists(path string) bool { _, err := os.Stat(path); return err == nil }
func export(cfg config.Config, turn model.Turn, httpClient *http.Client) error {
	legacy := legacyTurnDir(cfg, turn)
	if exists(filepath.Join(legacy, "completed.json")) {
		return nil
	}
	if info, err := os.Stat(filepath.Join(legacy, "upload.lock")); err == nil && time.Since(info.ModTime()) < 10*time.Minute {
		return fmt.Errorf("legacy WorkBuddy upload is still active; restart WorkBuddy before retrying")
	}
	manager := state.Manager{Root: filepath.Join(cfg.StateDir, "uploads")}
	claim, err := manager.Claim(turn.SessionID, turn.TurnID, parse.Hash(turn.TurnID))
	if errors.Is(err, state.ErrAlreadyCompleted) {
		return nil
	}
	if err != nil {
		return err
	}
	if claim == nil {
		return fmt.Errorf("WorkBuddy turn upload is already active")
	}
	defer claim.Release()
	spans := (semantic.Builder{ScopeName: "gtrace-workbuddy-collector", ScopeVersion: buildinfo.Version}).Build(turn)
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
		if exists(filepath.Join(legacy, signal+".json")) {
			if err = claim.MarkSignalUploaded(signal, nil); err != nil {
				return err
			}
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
			return err
		}
		if err = claim.MarkSignalUploaded(signal, map[string]any{"status": result.StatusCode}); err != nil {
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
