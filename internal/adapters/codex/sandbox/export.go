package sandbox

import (
	"encoding/json"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

// exportRecord applies the sandbox receiver contract, including to records
// queued by earlier releases. Event identity and both native clocks are kept.
// This belongs in the adapter: other OTLP producers may use structured bodies.
func exportRecord(record model.LogRecord) model.LogRecord {
	clone := func(src map[string]any) map[string]any {
		dst := make(map[string]any, len(src)+2)
		for key, value := range src {
			dst[key] = value
		}
		return dst
	}
	record.Resource, record.Attributes = clone(record.Resource), clone(record.Attributes)
	// Guance selects the log source from Resource log.source before service.name.
	record.Resource["log.source"] = Source
	record.Attributes["source"] = Source
	// Some receivers do not expose the newer native OTLP EventName field.
	record.Attributes["event_name"] = record.EventName
	text := func(key string) string {
		value, _ := record.Attributes[key].(string)
		return value
	}
	body := struct {
		EventName      string `json:"event_name"`
		SandboxID      string `json:"sandbox_id"`
		ExecutorID     string `json:"executor_id"`
		State          string `json:"state,omitempty"`
		CreatedAt      string `json:"sandbox_created_at,omitempty"`
		DestroyedAt    string `json:"sandbox_destroyed_at,omitempty"`
		FirstSeenAt    string `json:"first_seen_at,omitempty"`
		LastObservedAt string `json:"last_observed_at,omitempty"`
		Reason         string `json:"reason,omitempty"`
	}{EventName: record.EventName, SandboxID: text("obs.sandbox.id"), ExecutorID: text("obs.executor.id")}
	if record.EventName == "obs.sandbox.trace.bound" {
		// Relations never make a second lifecycle assertion, even after replay.
		record.SeverityText, record.SeverityNumber = "INFO", 9
		delete(record.Attributes, "obs.sandbox.state")
		delete(record.Attributes, "sandbox_created_at")
		delete(record.Attributes, "sandbox_destroyed_at")
	} else {
		var previous map[string]any
		switch value := record.Body.(type) {
		case map[string]any:
			previous = value
		case string:
			_ = json.Unmarshal([]byte(value), &previous)
		}
		// RC1-RC4 kept lifecycle dates only in the structured body. Export them
		// under the same prefixed, queryable fields used for new evidence.
		for _, pair := range [][2]string{{"sandbox_created_at", "created_at"}, {"sandbox_destroyed_at", "destroyed_at"}} {
			if text(pair[0]) == "" {
				value, _ := previous[pair[0]].(string)
				if value == "" {
					value, _ = previous[pair[1]].(string)
				}
				if value != "" {
					record.Attributes[pair[0]] = value
				}
			}
		}
		body.State, body.CreatedAt, body.DestroyedAt = text("obs.sandbox.state"), text("sandbox_created_at"), text("sandbox_destroyed_at")
		if record.ObservedTimeUnixNano != 0 {
			body.LastObservedAt = time.Unix(0, int64(record.ObservedTimeUnixNano)).UTC().Format(time.RFC3339Nano)
			if record.EventName == "obs.sandbox.discovered" {
				body.FirstSeenAt = body.LastObservedAt
			}
		}
		if reason, ok := previous["reason"].(string); ok {
			body.Reason = privacy.Text(reason, 1024)
		}
	}
	// All fields are strings, so encoding cannot fail. A string Body avoids
	// receivers rendering AnyValue.KVList as protobuf diagnostic text.
	encoded, _ := json.Marshal(body)
	record.Body = string(encoded)
	return record
}
