// Package sandbox ingests explicit runtime evidence. It never infers sandbox
// identity or lifecycle from tool calls, process exits, or turn completion.
package sandbox

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

const Source = "agent_sandbox"

// Event is the versioned runtime bridge contract, not a native Codex schema.
// The producer must own or observe the claimed isolation instance.
type Event struct {
	SchemaVersion     int        `json:"schema_version"`
	EventID           string     `json:"event_id"`
	EventName         string     `json:"event_name"`
	Timestamp         time.Time  `json:"timestamp"`
	SandboxID         string     `json:"sandbox_id"`
	ExecutorID        string     `json:"executor_id"`
	IdentitySource    string     `json:"identity_source"`
	RuntimeSource     string     `json:"runtime_source"`
	Name              string     `json:"name,omitempty"`
	Type              string     `json:"type,omitempty"`
	State             string     `json:"state,omitempty"`
	CreatedAt         *time.Time `json:"created_at,omitempty"`
	DestroyedAt       *time.Time `json:"destroyed_at,omitempty"`
	SessionID         string     `json:"session_id,omitempty"`
	TurnID            string     `json:"turn_id,omitempty"`
	ToolCallID        string     `json:"tool_call_id,omitempty"`
	ExecutionID       string     `json:"execution_id,omitempty"`
	Reason            string     `json:"reason,omitempty"`
	AssociationMethod string     `json:"association_method,omitempty"`
}

func (e Event) Validate() error {
	if e.SchemaVersion != 1 {
		return fmt.Errorf("unsupported sandbox event schema")
	}
	for _, id := range []string{e.EventID, e.SandboxID, e.ExecutorID, e.RuntimeSource} {
		if strings.TrimSpace(id) == "" || len(id) > 256 || strings.ContainsAny(id, "\r\n\x00") {
			return fmt.Errorf("sandbox event requires bounded identity and provenance")
		}
	}
	if e.IdentitySource != "native" && e.IdentitySource != "observer" {
		return fmt.Errorf("invalid sandbox identity source")
	}
	if !validTime(e.Timestamp) {
		return fmt.Errorf("sandbox event requires a valid occurrence timestamp")
	}
	switch e.EventName {
	case "obs.sandbox.discovered", "obs.sandbox.created", "obs.sandbox.ready", "obs.sandbox.snapshot", "obs.sandbox.destroyed", "obs.sandbox.failed", "obs.sandbox.observation.lost", "obs.sandbox.trace.bound":
	default:
		return fmt.Errorf("unsupported sandbox event name")
	}
	switch e.State {
	case "", "unknown", "creating", "created", "ready", "running", "destroying", "destroyed", "failed":
	default:
		return fmt.Errorf("invalid sandbox state")
	}
	for _, value := range []*time.Time{e.CreatedAt, e.DestroyedAt} {
		if value != nil && !validTime(*value) {
			return fmt.Errorf("invalid sandbox lifecycle timestamp")
		}
	}
	if e.CreatedAt != nil && e.DestroyedAt != nil && e.DestroyedAt.Before(*e.CreatedAt) {
		return fmt.Errorf("sandbox destruction precedes creation")
	}
	for _, value := range []*time.Time{e.CreatedAt, e.DestroyedAt} {
		if value != nil && value.After(e.Timestamp) {
			return fmt.Errorf("lifecycle timestamp follows evidence timestamp")
		}
	}
	expectedState := map[string]string{"obs.sandbox.created": "created", "obs.sandbox.ready": "ready", "obs.sandbox.destroyed": "destroyed", "obs.sandbox.failed": "failed"}[e.EventName]
	if expectedState != "" && e.State != "" && e.State != expectedState {
		return fmt.Errorf("event and sandbox state disagree")
	}
	if e.DestroyedAt != nil && e.EventName != "obs.sandbox.destroyed" && e.EventName != "obs.sandbox.snapshot" {
		return fmt.Errorf("event cannot establish destruction")
	}
	if e.EventName == "obs.sandbox.observation.lost" && (e.DestroyedAt != nil || (e.State != "" && e.State != "unknown")) {
		return fmt.Errorf("observation loss cannot establish terminal state")
	}
	if e.EventName == "obs.sandbox.discovered" && e.DestroyedAt != nil {
		return fmt.Errorf("discovery cannot establish destruction")
	}
	if e.EventName == "obs.sandbox.created" && e.CreatedAt != nil && !e.CreatedAt.Equal(e.Timestamp) {
		return fmt.Errorf("creation event timestamp mismatch")
	}
	if e.EventName == "obs.sandbox.destroyed" && e.DestroyedAt != nil && !e.DestroyedAt.Equal(e.Timestamp) {
		return fmt.Errorf("destruction event timestamp mismatch")
	}
	parts := 0
	for _, id := range []string{e.SessionID, e.TurnID, e.ToolCallID} {
		if id != "" {
			parts++
		}
		if len(id) > 256 {
			return fmt.Errorf("sandbox association identifier exceeds limit")
		}
	}
	if parts != 0 && parts != 3 {
		return fmt.Errorf("sandbox association requires session, turn, and tool call IDs")
	}
	if e.EventName == "obs.sandbox.trace.bound" && parts != 3 {
		return fmt.Errorf("trace association requires a tool identity")
	}
	if len(e.ExecutionID) > 256 {
		return fmt.Errorf("execution identifier exceeds limit")
	}
	if e.AssociationMethod != "" && e.AssociationMethod != "runtime_keys" && e.AssociationMethod != "native_thread_command_digest" {
		return fmt.Errorf("invalid sandbox association method")
	}
	return nil
}

func validTime(t time.Time) bool { return !t.IsZero() && t.Year() >= 1970 && t.Year() <= 2261 }

func digest(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (e Event) record(now time.Time, resource map[string]any, capture bool) model.LogRecord {
	r := map[string]any{}
	for key, value := range resource {
		r[key] = value
	}
	if r["service.name"] == nil {
		r["service.name"] = "codex-sandbox-runtime"
	}
	r["obs.sandbox.id"], r["obs.executor.id"] = e.SandboxID, e.ExecutorID
	attrs := map[string]any{
		"source": Source, "obs.schema.version": 1, "obs.event.id": e.EventID,
		"obs.sandbox.id": e.SandboxID, "obs.executor.id": e.ExecutorID,
		"obs.sandbox.identity_source": e.IdentitySource, "obs.collection.source": e.RuntimeSource,
	}
	body := map[string]any{}
	setLifecycleTime := func(key string, value time.Time) {
		formatted := value.UTC().Format(time.RFC3339Nano)
		attrs[key] = formatted
	}
	state := e.State
	switch e.EventName {
	case "obs.sandbox.created":
		state = "created"
		setLifecycleTime("sandbox_created_at", e.Timestamp)
	case "obs.sandbox.ready":
		state = "ready"
	case "obs.sandbox.destroyed":
		state = "destroyed"
		setLifecycleTime("sandbox_destroyed_at", e.Timestamp)
	case "obs.sandbox.failed":
		state = "failed"
	case "obs.sandbox.observation.lost":
		state = "unknown"
	}
	if state != "" {
		attrs["obs.sandbox.state"] = state
	}
	if e.Name != "" {
		attrs["obs.sandbox.name"] = privacy.Text(e.Name, 256)
	}
	if e.Type != "" {
		attrs["obs.sandbox.type"] = privacy.Text(e.Type, 128)
	}
	if e.CreatedAt != nil {
		setLifecycleTime("sandbox_created_at", *e.CreatedAt)
	}
	if e.DestroyedAt != nil {
		setLifecycleTime("sandbox_destroyed_at", *e.DestroyedAt)
	}
	if e.ExecutionID != "" {
		attrs["obs.execution.id"] = e.ExecutionID
	}
	if e.SessionID != "" {
		attrs["gen_ai.conversation.id"] = e.SessionID
		attrs["run_id"] = e.TurnID
		attrs["gen_ai.tool.call.id"] = e.ToolCallID
		method := e.AssociationMethod
		if method == "" {
			method = "runtime_keys"
		}
		attrs["obs.sandbox.association.method"] = method
		if method == "native_thread_command_digest" {
			attrs["obs.sandbox.association.confidence"] = "inferred"
		}
	}
	if capture && e.Reason != "" {
		body["reason"] = privacy.Text(e.Reason, 1024)
	}
	level, severity := "INFO", uint32(9)
	if e.EventName == "obs.sandbox.failed" {
		level, severity = "ERROR", 17
	}
	return exportRecord(model.LogRecord{EventName: e.EventName, TimeUnixNano: uint64(e.Timestamp.UnixNano()), ObservedTimeUnixNano: uint64(now.UnixNano()), SeverityNumber: severity, SeverityText: level, Attributes: attrs, Body: body, Resource: r, Scope: model.Scope{Name: "obs-codex-sandbox", Version: "1"}})
}
