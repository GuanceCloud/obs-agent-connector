package hook

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/buildinfo"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/metrics"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/state"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

func RunCLI(args []string) int {
	home, _ := os.UserHomeDir()
	fs := flag.NewFlagSet("hook omp", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	snapshotPath := fs.String("snapshot", "", "Terminal snapshot file")
	path := fs.String("config", agentfiles.ConfigPath(home, "omp"), "Runtime configuration")
	if fs.Parse(args) != nil || fs.NArg() != 0 || *snapshotPath == "" {
		return 1
	}
	cfg, err := config.LoadConfig(*path)
	if err != nil {
		return 1
	}
	if err := ProcessFile(*snapshotPath, cfg, nil); err != nil {
		appendLog(cfg.LogFile, "OMP telemetry error", map[string]any{"stage": "process", "code": "PROCESS_FAILED"})
		return 1
	}
	return 0
}

// ProcessFile handles one JS-owned handoff file, never scans for pending uploads.
func ProcessFile(path string, cfg config.Config, client *http.Client) error {
	// Refuse arbitrary paths: only regular JSON files in OMP's handoff directory.
	absolute, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	directory, err := filepath.Abs(filepath.Join(cfg.StateDir, "queue"))
	if err != nil {
		return err
	}
	if filepath.Dir(absolute) != directory || !strings.HasSuffix(absolute, ".json") {
		return errors.New("invalid OMP snapshot path")
	}
	info, err := os.Lstat(absolute)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("invalid OMP snapshot file")
	}
	cleanup(cfg, time.Now())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return processSnapshot(ctx, absolute, cfg, client)
}

// Cleanup runs for disabled capture, parse errors, and exhausted uploads alike.
func processSnapshot(ctx context.Context, path string, cfg config.Config, client *http.Client) (err error) {
	defer func() {
		if removeErr := os.Remove(path); removeErr != nil && !os.IsNotExist(removeErr) {
			appendLog(cfg.LogFile, "OMP telemetry error", map[string]any{"stage": "cleanup", "code": "REMOVE_FAILED"})
			err = errors.Join(err, removeErr)
		}
	}()
	if !cfg.Enabled {
		return nil
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() > 9*1024*1024 {
		return errors.New("OMP snapshot exceeds size limit")
	}
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	snapshot, err := parse.DecodeSnapshot(body)
	if err != nil {
		return err
	}
	return uploadSnapshotContext(ctx, snapshot, body, cfg, client)
}

func uploadWithRetry(ctx context.Context, uploader transport.Client, signal string, body []byte) (transport.Result, error) {
	for attempt := 0; ; attempt++ {
		if err := ctx.Err(); err != nil {
			return transport.Result{}, err
		}
		result, err := uploader.Upload(signal, body)
		if err == nil || attempt == 2 {
			return result, err
		}
		var networkErr *url.Error
		retryable := errors.As(err, &networkErr) || result.StatusCode == 429 ||
			result.StatusCode == 502 || result.StatusCode == 503 || result.StatusCode == 504
		if !retryable {
			return result, err
		}
		select {
		case <-ctx.Done():
			return result, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 500 * time.Millisecond):
		}
	}
}

var errClaimBusy = errors.New("OMP turn is being uploaded")

// Bind every HTTP attempt to the same per-snapshot deadline without changing other adapters.
type deadlineTransport struct {
	ctx  context.Context
	base http.RoundTripper
}

func (t deadlineTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancel(r.Context())
	stop := context.AfterFunc(t.ctx, cancel)
	release := func() { stop(); cancel() }
	response, err := t.base.RoundTrip(r.Clone(ctx))
	if err != nil {
		release()
		return nil, err
	}
	response.Body = &deadlineBody{ReadCloser: response.Body, release: release}
	return response, nil
}

type deadlineBody struct {
	io.ReadCloser
	release func()
}

func (b *deadlineBody) Close() error { defer b.release(); return b.ReadCloser.Close() }

func uploadSnapshotContext(ctx context.Context, snapshot parse.Snapshot, body []byte, cfg config.Config, client *http.Client) error {
	turn, err := parse.Normalize(snapshot, cfg)
	if err != nil {
		return err
	}
	if turn.SessionID == "" {
		return nil
	}
	manager := state.Manager{Root: filepath.Join(cfg.StateDir, "uploads"), StaleAfter: 5 * time.Minute}
	hash := sha256.Sum256(body)
	claim, err := manager.Claim(turn.SessionID, turn.TurnID, hex.EncodeToString(hash[:]))
	if errors.Is(err, state.ErrAlreadyCompleted) {
		return nil
	}
	if err != nil {
		return err
	}
	if claim == nil {
		return errClaimBusy
	}
	defer claim.Release()
	spans := linkedSpans(turn, snapshot)
	if len(spans) == 0 {
		return nil
	}
	items := buildMetrics(spans)
	// Session/run identifiers belong on traces, not default metric dimensions.
	for i := range items {
		for _, key := range []string{"session_id", "gen_ai.conversation.id", "run_id", "run_ids"} {
			delete(items[i].Attributes, key)
		}
	}
	httpClient := http.Client{Timeout: cfg.Transport.Timeout}
	if client != nil {
		httpClient = *client
	}
	base := httpClient.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	httpClient.Transport = deadlineTransport{ctx: ctx, base: base}
	uploader := transport.Client{Config: cfg.Transport, HTTPClient: &httpClient}
	signals := []struct {
		name string
		body []byte
	}{
		{"traces", proto.EncodeExportTraceServiceRequest(otlp.SpansToProtoRequest(spans))},
		{"metrics", proto.EncodeExportMetricsServiceRequest(otlp.MetricsToProtoRequest(items))},
	}
	for _, signal := range signals {
		if claim.SignalWasUploaded(signal.name) {
			continue
		}
		result, err := uploadWithRetry(ctx, uploader, signal.name, signal.body)
		if err != nil {
			appendLog(cfg.LogFile, "OMP telemetry error", map[string]any{"stage": "upload", "signal": signal.name, "http_status": result.StatusCode, "code": "UPLOAD_FAILED"})
			return err
		}
		if err := claim.MarkSignalUploaded(signal.name, map[string]any{"status": result.StatusCode}); err != nil {
			return err
		}
	}
	if err := claim.Complete("traces", "metrics"); err != nil {
		return err
	}
	appendLog(cfg.LogFile, "uploaded OMP turn", map[string]any{"spans": len(spans), "metrics": len(items)})
	return nil
}

// buildSpans keeps OMP on the published GenAI/GTrace semantic fields. The shared
// builder also emits legacy presentation aliases used by other adapters.
func buildSpans(turn model.Turn) []model.Span {
	spans := (semantic.Builder{ScopeName: "gtrace-omp-collector", ScopeVersion: buildinfo.Version}).Build(turn)
	for i := range spans {
		if strings.HasPrefix(spans[i].Name, "skill:") && spans[i].Attributes["status"] == "completed" {
			spans[i].Attributes["status"] = "ok"
		}
		for _, key := range []string{"gtrace.observation.input", "gtrace.observation.output", "gtrace.observation.type", "gtrace.usage", "gtrace.model.name"} {
			delete(spans[i].Attributes, key)
		}
	}
	return spans
}

// Preserve the request terminal state separately from the aggregate status.
func buildMetrics(spans []model.Span) []model.Metric {
	items := metrics.Build(spans)
	for _, span := range spans {
		if span.Name != "invoke_agent" {
			continue
		}
		for i := range items {
			if items[i].Name == "gen_ai.workflow.duration" {
				items[i].Attributes["final_status"] = span.Attributes["final_status"]
			}
		}
	}
	return items
}
