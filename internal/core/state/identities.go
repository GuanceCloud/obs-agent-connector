package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

type spanIdentity struct {
	Name         string
	TraceID      string
	SpanID       string
	ParentID     string
	ToolIdentity map[string]any `json:",omitempty"`
}

// StabilizeSpans pins IDs under the existing upload claim before its first
// request. Only identities are persisted, never messages or tool arguments.
func (c *Claim) StabilizeSpans(spans []model.Span) ([]model.Span, error) {
	if c == nil {
		return nil, fmt.Errorf("claim is nil")
	}
	path := filepath.Join(c.dir, "span-identities.json")
	var identities []spanIdentity
	body, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		// Legacy partial uploads have no recoverable IDs. Do not bind a newly
		// generated trace to an already uploaded legacy trace.
		if c.SignalWasUploaded("traces") {
			return nil, nil
		}
		for _, span := range spans {
			attrs := map[string]any{}
			for _, key := range []string{"gen_ai.conversation.id", "run_id", "gen_ai.tool.call.id"} {
				if value, ok := span.Attributes[key].(string); ok {
					attrs[key] = value
				}
			}
			identities = append(identities, spanIdentity{Name: span.Name, TraceID: span.TraceID, SpanID: span.SpanID, ParentID: span.ParentID, ToolIdentity: attrs})
		}
		if err := writeJSONAtomic(path, identities); err != nil {
			return nil, err
		}
		return spans, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(body, &identities); err != nil {
		return nil, err
	}
	if len(identities) != len(spans) {
		return nil, fmt.Errorf("persisted span identities no longer match batch")
	}
	remap := map[string]string{}
	for i, span := range spans {
		if identities[i].Name != span.Name {
			return nil, fmt.Errorf("persisted span identity order changed")
		}
		for key, value := range identities[i].ToolIdentity {
			if span.Attributes[key] != value {
				return nil, fmt.Errorf("persisted tool identity changed")
			}
		}
		remap[span.SpanID] = identities[i].SpanID
	}
	out := append([]model.Span(nil), spans...)
	for i := range out {
		out[i].TraceID, out[i].SpanID, out[i].ParentID = identities[i].TraceID, identities[i].SpanID, identities[i].ParentID
		attrs := make(map[string]any, len(out[i].Attributes))
		for key, value := range out[i].Attributes {
			if key == "triggered_by.llm_span_id" {
				if text, ok := value.(string); ok && remap[text] != "" {
					value = remap[text]
				}
			}
			attrs[key] = value
		}
		out[i].Attributes = attrs
	}
	return out, nil
}

// VisitUploadedSpans recovers confirmed identities after a crash between a
// successful Trace export and recording its sandbox associations. No transcript
// content is needed. Older identities follow the caller's retention window.
func VisitUploadedSpans(root string, since time.Time, exportScope string, visit func([]model.Span) error) error {
	dirs, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, dir := range dirs {
		if !dir.IsDir() {
			continue
		}
		path := filepath.Join(root, dir.Name())
		info, err := os.Stat(filepath.Join(path, "traces.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.ModTime().Before(since) {
			continue
		}
		if exportScope != "" {
			confirmed, err := os.ReadFile(filepath.Join(path, "traces.json"))
			if err != nil {
				return err
			}
			var details map[string]any
			if err := json.Unmarshal(confirmed, &details); err != nil {
				return err
			}
			if details["sandbox_scope"] != exportScope {
				continue
			}
		}
		body, err := os.ReadFile(filepath.Join(path, "span-identities.json"))
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		var ids []spanIdentity
		if err := json.Unmarshal(body, &ids); err != nil {
			return err
		}
		spans := make([]model.Span, 0, len(ids))
		for _, id := range ids {
			spans = append(spans, model.Span{Name: id.Name, TraceID: id.TraceID, SpanID: id.SpanID, ParentID: id.ParentID, Attributes: id.ToolIdentity})
		}
		if err := visit(spans); err != nil {
			return err
		}
	}
	return nil
}
