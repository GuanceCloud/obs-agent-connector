package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/hooklog"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/journal"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/state"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

const maxEventBytes = 64 << 10

func Configured(cfg config.Config) bool {
	return cfg.Enabled && cfg.SandboxEnabled && cfg.StateDir != "" && cfg.LogsPath != ""
}

func NewStore(cfg config.Config) Store {
	resource, _ := privacy.Sanitize(cfg.ResourceAttributes, 256).(map[string]any)
	// Reserve source for the log contract rather than conflicting user tags.
	delete(resource, "source")
	return Store{Root: filepath.Join(cfg.StateDir, "sandbox", Scope(cfg)), MaxPending: cfg.SandboxMaxPending, HookLogFile: cfg.HookLogFile, Resource: resource, CaptureContent: cfg.CaptureContent != "none"}
}

// Scope isolates queued records and recovered trace bindings when destinations
// or authentication change. Only its digest is persisted, never credentials.
func Scope(cfg config.Config) string {
	if !Configured(cfg) {
		return ""
	}
	// Keep the empty legacy LogsURL hash slot so existing endpoint/path queues
	// and confirmed bindings retain their destination scope. It is not a setting.
	body, _ := json.Marshal(struct {
		Endpoint, TracePath, TraceURL, LogsPath, LogsURL, PublicKey, SecretKey string
		Headers                                                                map[string]string
	}{cfg.Endpoint, cfg.TracePath, cfg.OtelTracesURL, cfg.LogsPath, "", cfg.PublicKey, cfg.SecretKey, cfg.Headers})
	return digest(string(body))
}

func NewClient(cfg config.Config, client *http.Client) transport.Client {
	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond
	if timeout <= 0 || timeout > 10*time.Second {
		timeout = 10 * time.Second
	}
	if client != nil {
		bounded := *client
		if bounded.Timeout <= 0 || bounded.Timeout > timeout {
			bounded.Timeout = timeout
		}
		client = &bounded
	}
	return transport.Client{HTTPClient: client, Config: transport.Config{
		Endpoint: cfg.Endpoint, LogsPath: cfg.LogsPath,
		Headers: cfg.Headers, PublicKey: cfg.PublicKey, SecretKey: cfg.SecretKey, Timeout: timeout,
	}}
}

// RunCLI is an internal producer bridge. No hooks or Codex configuration are
// automatically changed; native Codex 0.162.1 cannot supply this full contract.
func RunCLI(args []string) int {
	fs := flag.NewFlagSet("internal codex-sandbox", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	flushOnly := fs.Bool("flush", false, "attempt queued sandbox log delivery without reading stdin")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected sandbox bridge arguments")
		return 1
	}
	cfg, err := config.Resolve(config.ResolveOptions{})
	if err != nil {
		fmt.Fprintln(os.Stderr, "could not resolve Codex sandbox configuration")
		return 1
	}
	if !cfg.Enabled || !cfg.SandboxEnabled {
		return 0
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	var reader io.Reader = os.Stdin
	if *flushOnly {
		reader = bytes.NewReader(nil)
	}
	err = Run(ctx, reader, cfg, nil)
	if err != nil {
		_ = hooklog.Append(cfg.HookLogFile, "sandbox bridge stopped", map[string]any{"category": errorCategory(err)})
		fmt.Fprintln(os.Stderr, "sandbox bridge stopped; inspect the Codex hook log")
		return 1
	}
	return 0
}

// Run receives NDJSON continuously while delivery proceeds independently. The
// supplied reader should be closed by its owner when cancelling a blocked read.
func Run(ctx context.Context, reader io.Reader, cfg config.Config, httpClient *http.Client) error {
	if !cfg.Enabled || !cfg.SandboxEnabled {
		return nil
	}
	if !Configured(cfg) {
		return fmt.Errorf("sandbox collection requires state_dir and an explicit Logs endpoint")
	}
	endpoint := transport.JoinEndpoint(cfg.Endpoint, cfg.LogsPath)
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return fmt.Errorf("invalid sandbox Logs endpoint")
	}
	store, client := NewStore(cfg), NewClient(cfg, httpClient)
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	var workers sync.WaitGroup
	workers.Add(1)
	go func() {
		defer workers.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		var lastRecovery time.Time
		for {
			if time.Since(lastRecovery) >= 15*time.Second {
				if err := recoverBindings(cfg, store); err != nil {
					_ = hooklog.Append(cfg.HookLogFile, "sandbox binding recovery deferred", nil)
				}
				lastRecovery = time.Now()
			}
			if err := store.Flush(workerCtx, client); err != nil && !errors.Is(err, ErrDeferred) && workerCtx.Err() == nil {
				_ = hooklog.Append(cfg.HookLogFile, "sandbox export deferred", map[string]any{"category": errorCategory(err)})
			}
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	done := make(chan error, 1)
	go func() { done <- consume(ctx, reader, store, cfg.HookLogFile) }()
	select {
	case <-ctx.Done():
		err = ctx.Err()
	case err = <-done:
	}
	cancel()
	workers.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	// EOF gets one bounded final attempt. Remaining records survive for restart
	// or --flush; this never turns a producer pipe into an unbounded network wait.
	flushCtx, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	if recoveryErr := recoverBindings(cfg, store); recoveryErr != nil {
		err = errors.Join(err, recoveryErr)
	}
	flushErr := store.Flush(flushCtx, client)
	return errors.Join(err, flushErr)
}

func recoverBindings(cfg config.Config, store Store) error {
	return state.VisitUploadedSpans(filepath.Join(cfg.StateDir, "uploads"), store.now().Add(-retention), Scope(cfg), store.BindSpans)
}

func errorCategory(err error) string {
	var status *transport.UploadError
	switch {
	case errors.As(err, &status):
		if status.RejectedLogRecords > 0 {
			return "otlp_partial_rejection"
		}
		return fmt.Sprintf("otlp_http_%d", status.StatusCode)
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	case errors.Is(err, ErrFull):
		return "queue_full"
	case errors.Is(err, ErrDeferred):
		return "retry_scheduled"
	default:
		return "collection_or_transport_error"
	}
}

func consume(ctx context.Context, reader io.Reader, store Store, logPath string) error {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), maxEventBytes)
	var failures int
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
			continue
		}
		var event Event
		decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
		decoder.DisallowUnknownFields()
		err := decoder.Decode(&event)
		if err == nil {
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				err = fmt.Errorf("sandbox event contains trailing JSON")
			}
		}
		if err == nil {
			err = store.Apply(event)
		}
		if err != nil {
			failures++
			// Payloads and decoder diagnostics may contain credentials. Record a
			// bounded category only, and continue draining the producer stream.
			category := "invalid_event"
			if errors.Is(err, ErrFull) {
				category = "queue_full"
			}
			if errors.Is(err, journal.ErrBusy) {
				category = "journal_busy"
			}
			_ = hooklog.Append(logPath, "sandbox event not accepted", map[string]any{"category": category})
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("sandbox input read failed or event exceeds %d bytes", maxEventBytes)
	}
	if failures > 0 {
		return fmt.Errorf("%d sandbox input events were not accepted", failures)
	}
	return nil
}
