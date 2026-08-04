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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codebuddy/buildinfo"
	codebuddyconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/codebuddy/config"
	codebuddyparse "github.com/GuanceCloud/obs-agent-connector/internal/adapters/codebuddy/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/metrics"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/state"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

type RunOptions struct {
	Config     *codebuddyconfig.Config
	Input      *codebuddyparse.HookInput
	Executable string
	HTTPClient *http.Client
	SkipWait   bool
}

func RunCLI(args []string) int {
	if len(args) == 2 && args[0] == "worker" {
		if err := RunWorker(args[1], RunOptions{}); err != nil {
			return 1
		}
		return 0
	}
	input, err := readInput(os.Stdin)
	if err != nil {
		return 0
	}
	cfg := codebuddyconfig.Resolve(codebuddyconfig.ResolveOptions{Cwd: input.Cwd})
	if !cfg.Enabled {
		return 0
	}
	executable, err := os.Executable()
	if err != nil {
		appendLog(cfg, "hook failed", map[string]any{"phase": "resolve executable", "error": err.Error()})
		return 0
	}
	queuePath, err := enqueue(executable, input, cfg)
	if err != nil {
		appendLog(cfg, "hook failed", map[string]any{"phase": "enqueue worker", "error": err.Error()})
		return 0
	}
	appendLog(cfg, "worker queued", map[string]any{"event": input.Event, "queue_file": filepath.Base(queuePath)})
	return 0
}

func Run(options RunOptions) error {
	if options.Input == nil {
		return errors.New("CodeBuddy Hook input is required")
	}
	cfg := codebuddyconfig.Resolve(codebuddyconfig.ResolveOptions{Cwd: options.Input.Cwd})
	if options.Config != nil {
		cfg = *options.Config
	}
	if !cfg.Enabled {
		return nil
	}
	executable := options.Executable
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	_, err := enqueue(executable, *options.Input, cfg)
	return err
}

func RunWorker(queuePath string, options RunOptions) error {
	body, err := os.ReadFile(queuePath)
	if err != nil {
		return err
	}
	var input codebuddyparse.HookInput
	if err := json.Unmarshal(body, &input); err != nil {
		return err
	}
	cfg := codebuddyconfig.Resolve(codebuddyconfig.ResolveOptions{Cwd: input.Cwd})
	if options.Config != nil {
		cfg = *options.Config
	}
	if !cfg.Enabled {
		_ = os.Remove(queuePath)
		return nil
	}
	appendLog(cfg, "worker started", logFields(input, cfg.Debug))
	turns, diagnostics, err := waitForTurns(input, cfg, options.SkipWait)
	if err != nil {
		appendLog(cfg, "transcript replay failed", map[string]any{"event": input.Event, "error": err.Error()})
		return err
	}
	appendLog(cfg, "transcript replayed", map[string]any{"event": input.Event, "turns": len(turns), "diagnostics": diagnostics})
	for _, turn := range turns {
		if err := exportTurn(cfg, turn, options.HTTPClient); err != nil {
			appendLog(cfg, "turn export failed", map[string]any{"turn_id_hash": shortHash(turn.TurnID), "error": err.Error()})
			return err
		}
	}
	return os.Remove(queuePath)
}

func readInput(reader io.Reader) (codebuddyparse.HookInput, error) {
	var input codebuddyparse.HookInput
	if err := json.NewDecoder(io.LimitReader(reader, 1<<20)).Decode(&input); err != nil {
		return input, err
	}
	if input.Event != "Stop" && input.Event != "SessionEnd" {
		return input, fmt.Errorf("unsupported CodeBuddy Hook %q", input.Event)
	}
	return input, nil
}

func enqueue(executable string, input codebuddyparse.HookInput, cfg codebuddyconfig.Config) (string, error) {
	queueDir := filepath.Join(cfg.StateDir, "queue")
	if err := os.MkdirAll(queueDir, 0o700); err != nil {
		return "", err
	}
	file, err := os.CreateTemp(queueDir, "event-*.json")
	if err != nil {
		return "", err
	}
	path := file.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := json.NewEncoder(file).Encode(input); err != nil {
		_ = file.Close()
		return "", err
	}
	if err := file.Close(); err != nil {
		return "", err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return "", err
	}
	defer devNull.Close()
	command := exec.Command(executable, "hook", "codebuddy", "worker", path)
	command.Stdin, command.Stdout, command.Stderr = devNull, devNull, devNull
	if err := command.Start(); err != nil {
		return "", err
	}
	_ = command.Process.Release()
	remove = false
	return path, nil
}

func waitForTurns(input codebuddyparse.HookInput, cfg codebuddyconfig.Config, skipWait bool) ([]model.Turn, codebuddyparse.Diagnostics, error) {
	deadline := time.Now().Add(cfg.TerminalWait)
	var lastDiagnostics codebuddyparse.Diagnostics
	var lastError error
	for {
		turns, pending, diagnostics, err := codebuddyparse.Read(input, cfg)
		lastDiagnostics = diagnostics
		if err == nil && !pending {
			return turns, diagnostics, nil
		}
		if err != nil {
			lastError = err
		}
		if skipWait || time.Now().After(deadline) {
			if lastError != nil {
				return nil, lastDiagnostics, lastError
			}
			return turns, lastDiagnostics, nil
		}
		time.Sleep(125 * time.Millisecond)
	}
}

func exportTurn(cfg codebuddyconfig.Config, turn model.Turn, httpClient *http.Client) error {
	manager := state.Manager{Root: filepath.Join(cfg.StateDir, "uploads"), StaleAfter: 10 * time.Minute}
	claim, err := manager.Claim(turn.SessionID, turn.TurnID, codebuddyparse.Fingerprint(turn))
	if errors.Is(err, state.ErrAlreadyCompleted) || (err == nil && claim == nil) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("claim turn: %w", err)
	}
	completed := false
	defer func() {
		if !completed {
			_ = claim.Release()
		}
	}()

	spans := (semantic.Builder{ScopeName: "gtrace-codebuddy-collector", ScopeVersion: buildinfo.Version}).Build(turn)
	if len(spans) == 0 {
		return nil
	}
	builtMetrics := metrics.Build(spans)
	uploader := transport.Client{Config: cfg.Transport, HTTPClient: httpClient}
	if !claim.SignalWasUploaded("traces") {
		payload := proto.EncodeExportTraceServiceRequest(otlp.SpansToProtoRequest(spans))
		result, err := uploader.Upload("traces", payload)
		if err != nil {
			return err
		}
		if err := claim.MarkSignalUploaded("traces", map[string]any{"status": result.StatusCode, "bytes": len(payload)}); err != nil {
			return err
		}
	}
	required := []string{"traces"}
	hasMetricsEndpoint := cfg.Transport.MetricsURL != "" || cfg.Transport.Endpoint != ""
	if len(builtMetrics) > 0 && hasMetricsEndpoint {
		required = append(required, "metrics")
		if !claim.SignalWasUploaded("metrics") {
			payload := proto.EncodeExportMetricsServiceRequest(otlp.MetricsToProtoRequest(builtMetrics))
			result, err := uploader.Upload("metrics", payload)
			if err != nil {
				return err
			}
			if err := claim.MarkSignalUploaded("metrics", map[string]any{"status": result.StatusCode, "bytes": len(payload)}); err != nil {
				return err
			}
		}
	}
	if err := claim.Complete(required...); err != nil {
		return err
	}
	completed = true
	appendLog(cfg, "turn uploaded", map[string]any{"turn_id_hash": shortHash(turn.TurnID), "spans": len(spans), "metrics": len(builtMetrics)})
	return nil
}

func appendLog(cfg codebuddyconfig.Config, message string, extra map[string]any) {
	if strings.TrimSpace(cfg.LogFile) == "" {
		return
	}
	payload := map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano), "message": message}
	if extra != nil {
		payload["extra"] = extra
	}
	body, err := json.Marshal(payload)
	if err != nil || os.MkdirAll(filepath.Dir(cfg.LogFile), 0o700) != nil {
		return
	}
	file, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = file.Write(append(body, '\n'))
}

func logFields(input codebuddyparse.HookInput, debug bool) map[string]any {
	fields := map[string]any{"event": input.Event, "session_id_hash": shortHash(input.SessionID), "generation_id_hash": shortHash(input.GenerationID), "loop_count": input.LoopCount}
	if debug {
		fields["transcript_file"] = filepath.Base(input.TranscriptPath)
		fields["client"] = input.Client
		fields["version"] = input.Version
	}
	return fields
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}
