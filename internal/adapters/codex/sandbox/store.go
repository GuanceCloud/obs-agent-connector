package sandbox

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/hooklog"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/journal"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

var ErrFull = errors.New("sandbox journal capacity exhausted")
var ErrDeferred = errors.New("sandbox log retry is scheduled")

const retention = 7 * 24 * time.Hour

type Store struct {
	Root           string
	MaxPending     int
	Resource       map[string]any
	CaptureContent bool
	HookLogFile    string
	Now            func() time.Time
}

type receipt struct{ Key, Fingerprint string }
type pending struct {
	Key         string
	Fingerprint string
	Record      model.LogRecord
	Attempts    int
	RetryAt     time.Time
}
type binding struct {
	TraceID, SpanID string
	Observed        time.Time
}
type relation struct {
	Key, ToolKey string
	Record       model.LogRecord
	Bound        bool
	Observed     time.Time
}
type ledger struct {
	Queue     []pending
	Receipts  []receipt
	Bindings  map[string]binding
	Relations []relation
	Dropped   uint64
	Rejected  uint64
}

func (s Store) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s Store) limit() int {
	if s.MaxPending > 0 {
		return s.MaxPending
	}
	return 1024
}

func (s Store) update(fn func(*ledger) error) error {
	return s.access(fn, true)
}

func (s Store) access(fn func(*ledger) error, commit bool) error {
	f := journal.File[ledger]{Root: s.Root, MaxBytes: 16 << 20}
	transaction := f.Update
	if !commit {
		transaction = f.View
	}
	deadline := time.Now().Add(250 * time.Millisecond)
	for {
		err := transaction(func(l *ledger) error {
			if l.Bindings == nil {
				l.Bindings = map[string]binding{}
			}
			return fn(l)
		})
		if !errors.Is(err, journal.ErrBusy) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (s Store) prune(l *ledger, now time.Time) {
	for key, b := range l.Bindings {
		if now.Sub(b.Observed) > retention {
			delete(l.Bindings, key)
		}
	}
	kept := l.Relations[:0]
	for _, r := range l.Relations {
		// Unresolved relations are bounded and never silently expired.
		if !r.Bound || now.Sub(r.Observed) <= retention {
			kept = append(kept, r)
		}
	}
	l.Relations = kept
	if n := len(l.Receipts) - 2*s.limit(); n > 0 {
		l.Receipts = append([]receipt(nil), l.Receipts[n:]...)
	}
}

// Apply records only validated runtime evidence. Queue saturation is reported,
// and existing unsent lifecycle records are never evicted to admit a new one.
func (s Store) Apply(event Event) error {
	if err := event.Validate(); err != nil {
		return err
	}
	now := s.now()
	body, err := json.Marshal(event)
	if err != nil {
		return err
	}
	key := digest(event.ExecutorID, event.RuntimeSource, event.EventID)
	fingerprint := digest(string(body))
	full := false
	err = s.update(func(l *ledger) error {
		for _, queued := range l.Queue {
			if queued.Key != key {
				continue
			}
			if queued.Fingerprint != fingerprint {
				return fmt.Errorf("pending sandbox event ID reused with different contents")
			}
			return nil
		}
		for _, seen := range l.Receipts {
			if seen.Key != key {
				continue
			}
			if seen.Fingerprint != fingerprint {
				return fmt.Errorf("sandbox event ID reused with different contents")
			}
			return nil
		}
		s.prune(l, now)
		toolKey, relationKey := "", ""
		relationIndex := -1
		if event.SessionID != "" {
			toolKey = digest(event.SessionID, event.TurnID, event.ToolCallID)
			relationKey = digest(event.ExecutorID, event.SandboxID, event.ExecutionID, toolKey)
			for i := range l.Relations {
				if l.Relations[i].Key == relationKey {
					relationIndex = i
					break
				}
			}
		}
		needsRecord := event.EventName != "obs.sandbox.trace.bound"
		if (needsRecord && len(l.Queue) >= s.limit()) || (event.SessionID != "" && relationIndex < 0 && len(l.Relations) >= 2*s.limit()) {
			l.Dropped++
			full = true
			return nil
		}
		record := event.record(now, s.Resource, s.CaptureContent)
		record.Attributes["obs.event.id"] = key
		if needsRecord {
			l.Queue = append(l.Queue, pending{Key: key, Fingerprint: fingerprint, Record: record})
		}
		if event.SessionID != "" {
			if relationIndex >= 0 {
				l.Relations[relationIndex].Observed = now
			} else {
				attrs := make(map[string]any, len(record.Attributes))
				for key, value := range record.Attributes {
					attrs[key] = value
				}
				record.Attributes = attrs
				record.EventName = "obs.sandbox.trace.bound"
				record.Attributes["obs.event.id"] = digest("relation", relationKey)
				// Binding records carry identity, not a second lifecycle update.
				delete(record.Attributes, "obs.sandbox.state")
				record.Body = nil
				l.Relations = append(l.Relations, relation{Key: relationKey, ToolKey: toolKey, Record: record, Observed: now})
			}
		}
		l.Receipts = append(l.Receipts, receipt{key, fingerprint})
		return s.resolve(l, now)
	})
	if err == nil && full {
		return ErrFull
	}
	return err
}

func validHexID(value string, size int) bool {
	if len(value) != size*2 {
		return false
	}
	data, err := hex.DecodeString(value)
	if err != nil {
		return false
	}
	for _, b := range data {
		if b != 0 {
			return true
		}
	}
	return false
}

// BindSpans accepts the identities from successfully exported OBS spans only.
// Native Codex launch.trace_id is intentionally not used as an OBS trace ID.
func (s Store) BindSpans(spans []model.Span) error {
	now := s.now()
	return s.update(func(l *ledger) error {
		s.prune(l, now)
		for _, span := range spans {
			if !strings.HasPrefix(span.Name, "tool:") || !validHexID(span.TraceID, 16) || !validHexID(span.SpanID, 8) {
				continue
			}
			session, _ := span.Attributes["gen_ai.conversation.id"].(string)
			turn, _ := span.Attributes["run_id"].(string)
			call, _ := span.Attributes["gen_ai.tool.call.id"].(string)
			if session == "" || turn == "" || call == "" {
				continue
			}
			key := digest(session, turn, call)
			if old, ok := l.Bindings[key]; ok {
				if old.TraceID != span.TraceID || old.SpanID != span.SpanID {
					return fmt.Errorf("conflicting OBS trace binding")
				}
			} else if len(l.Bindings) >= 2*s.limit() {
				return ErrFull
			}
			l.Bindings[key] = binding{span.TraceID, span.SpanID, now}
		}
		return s.resolve(l, now)
	})
}

func (s Store) resolve(l *ledger, now time.Time) error {
	for i := range l.Relations {
		r := &l.Relations[i]
		if r.Bound {
			continue
		}
		b, ok := l.Bindings[r.ToolKey]
		if !ok {
			continue
		}
		if len(l.Queue) >= s.limit() {
			break
		}
		r.Record.TraceID, r.Record.SpanID = b.TraceID, b.SpanID
		r.Record.ObservedTimeUnixNano = uint64(now.UnixNano())
		l.Queue = append(l.Queue, pending{Key: digest("relation", r.Key), Record: r.Record})
		r.Bound = true
	}
	return nil
}

// Flush sends individual records so that a partial-success response has an
// unambiguous outcome. Failed requests retain their original event identities.
func (s Store) Flush(ctx context.Context, client transport.Client) error {
	for count := 0; count < 16; count++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		var next *pending
		if err := s.access(func(l *ledger) error {
			if len(l.Queue) > 0 {
				item := l.Queue[0]
				next = &item
			}
			return nil
		}, false); err != nil {
			return err
		}
		if next == nil {
			return nil
		}
		if s.now().Before(next.RetryAt) {
			return ErrDeferred
		}
		payload := proto.EncodeExportLogsServiceRequest(otlp.LogsToProtoRequest([]model.LogRecord{next.Record}))
		result, uploadErr := client.UploadContext(ctx, "logs", payload)
		var statusErr *transport.UploadError
		permanent := errors.As(uploadErr, &statusErr) && !statusErr.Retryable()
		extra := map[string]any{"signal": "logs", "source": Source, "log_count": 1, "status": result.StatusCode, "rejected_log_records": result.RejectedLogRecords}
		message := hooklog.UploadedLogs
		if uploadErr != nil {
			message = "logs upload failed"
			extra["category"] = errorCategory(uploadErr)
			extra["retryable"] = !permanent && ctx.Err() == nil
		}
		// Record only delivery metadata. Payloads, URLs, credentials, and server
		// response bodies must never enter the local diagnostic file.
		_ = hooklog.Append(s.HookLogFile, message, extra)
		if uploadErr != nil && !permanent {
			if ctx.Err() != nil {
				return uploadErr
			}
			delay := time.Second << min(max(next.Attempts, 0), 5)
			delay += time.Duration(rand.Int63n(int64(250 * time.Millisecond)))
			if statusErr != nil && statusErr.RetryAfter > delay {
				delay = statusErr.RetryAfter
			}
			retryAt := s.now().Add(delay)
			if err := s.update(func(l *ledger) error {
				for i := range l.Queue {
					if l.Queue[i].Key == next.Key {
						l.Queue[i].Attempts = min(l.Queue[i].Attempts+1, 6)
						l.Queue[i].RetryAt = retryAt
						break
					}
				}
				return nil
			}); err != nil {
				return errors.Join(uploadErr, err)
			}
			_ = hooklog.Append(s.HookLogFile, "logs retry scheduled", map[string]any{"signal": "logs", "source": Source, "retry_delay_ms": delay.Milliseconds(), "retry_at": retryAt.UTC().Format(time.RFC3339Nano)})
			return uploadErr
		}
		if err := s.update(func(l *ledger) error {
			for i, item := range l.Queue {
				if item.Key == next.Key {
					l.Queue = append(l.Queue[:i], l.Queue[i+1:]...)
					if permanent {
						l.Rejected++
					}
					break
				}
			}
			return s.resolve(l, s.now())
		}); err != nil {
			return err
		}
		if permanent {
			return uploadErr
		}
	}
	return nil
}
