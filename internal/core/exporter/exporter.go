// Package exporter persists normalized terminal turns and retries each OTLP signal independently.
package exporter

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/hooklog"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/metrics"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/state"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

type Exporter struct {
	Root       string
	LogFile    string
	Transport  transport.Config
	HTTPClient *http.Client
	Builder    semantic.Builder
}

// Enqueue accepts only already sanitized turns. Credentials belong in Transport, never in the spool.
func (e Exporter) Enqueue(turn model.Turn) error {
	if len(e.Builder.Build(turn)) == 0 {
		return nil
	}
	sum := sha256.Sum256([]byte(turn.SessionID + "\x00" + turn.TurnID))
	dir := filepath.Join(e.Root, "pending")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	body, err := json.Marshal(turn)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".turn-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(body); err != nil {
		tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	path := filepath.Join(dir, hex.EncodeToString(sum[:])+".json")
	// A hard link publishes the fully written payload without replacing the first observation.
	if err = os.Link(tmp.Name(), path); errors.Is(err, os.ErrExist) {
		return nil
	}
	return err
}

func (e Exporter) Flush() error {
	entries, err := os.ReadDir(filepath.Join(e.Root, "pending"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	deadline := time.Now().Add(20 * time.Second)
	var failures []error
	for _, entry := range entries {
		if time.Now().After(deadline) {
			break
		}
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(e.Root, "pending", entry.Name())
		body, readErr := os.ReadFile(path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			failures = append(failures, readErr)
			continue
		}
		var turn model.Turn
		if err = json.Unmarshal(body, &turn); err != nil {
			failures = append(failures, fmt.Errorf("invalid pending turn"))
			continue
		}
		if err = e.upload(turn); err != nil {
			failures = append(failures, err)
			continue
		}
		// A concurrent owner may still be uploading. Only remove completed turns.
		manager := state.Manager{Root: filepath.Join(e.Root, "uploads")}
		complete, checkErr := manager.Completed(turn.SessionID, turn.TurnID)
		if checkErr != nil {
			failures = append(failures, checkErr)
		} else if complete {
			_ = os.Remove(path)
		}
	}
	return errors.Join(failures...)
}
func (e Exporter) upload(turn model.Turn) error {
	manager := state.Manager{Root: filepath.Join(e.Root, "uploads"), StaleAfter: time.Minute}
	claim, err := manager.Claim(turn.SessionID, turn.TurnID, "")
	if errors.Is(err, state.ErrAlreadyCompleted) {
		return nil
	}
	if err != nil {
		return err
	}
	if claim == nil {
		return nil
	}
	defer claim.Release()
	spans := e.Builder.Build(turn)
	if len(spans) == 0 {
		return fmt.Errorf("pending turn is not terminal")
	}
	ms := metrics.Build(spans)
	client := transport.Client{Config: e.Transport, HTTPClient: e.HTTPClient}
	signals := []string{"traces"}
	if len(ms) > 0 && (e.Transport.Endpoint != "" || e.Transport.MetricsURL != "") {
		signals = append(signals, "metrics")
	}
	for _, signal := range signals {
		if claim.SignalWasUploaded(signal) {
			continue
		}
		var body []byte
		if signal == "traces" {
			body = proto.EncodeExportTraceServiceRequest(otlp.SpansToProtoRequest(spans))
		} else {
			body = proto.EncodeExportMetricsServiceRequest(otlp.MetricsToProtoRequest(ms))
		}
		result, err := client.Upload(signal, body)
		if err != nil {
			_ = hooklog.Append(e.LogFile, "upload failed", map[string]any{"signal": signal, "status": result.StatusCode})
			return fmt.Errorf("%s upload failed (status %d)", signal, result.StatusCode)
		}
		message, countKey, count := hooklog.UploadedSpans, "spans", len(spans)
		if signal == "metrics" {
			message, countKey, count = hooklog.UploadedMetrics, "metrics", len(ms)
		}
		_ = hooklog.Append(e.LogFile, message, map[string]any{"status": result.StatusCode, countKey: count})
		if err = claim.MarkSignalUploaded(signal, nil); err != nil {
			return err
		}
	}
	return claim.Complete(signals...)
}
