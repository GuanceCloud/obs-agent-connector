package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

func TestIdentitiesSurvivePartialUploadAndRecoverConfirmedTools(t *testing.T) {
	manager := Manager{Root: t.TempDir()}
	claim, err := manager.Claim("session", "turn", "")
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	original := []model.Span{
		{Name: "llm", TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("b", 16)},
		{Name: "tool:shell", TraceID: strings.Repeat("a", 32), SpanID: strings.Repeat("c", 16), ParentID: strings.Repeat("b", 16), Attributes: map[string]any{
			"gen_ai.conversation.id": "session", "run_id": "turn", "gen_ai.tool.call.id": "call", "triggered_by.llm_span_id": strings.Repeat("b", 16), "gen_ai.tool.call.arguments": "private command",
		}},
	}
	if _, err := claim.StabilizeSpans(original); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(claim.dir, "span-identities.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "private command") {
		t.Fatal("persisted tool content in identity journal")
	}
	if err := VisitUploadedSpans(manager.Root, time.Time{}, "", func([]model.Span) error { t.Fatal("recovered unconfirmed trace"); return nil }); err != nil {
		t.Fatal(err)
	}
	if err := claim.MarkSignalUploaded("traces", nil); err != nil {
		t.Fatal(err)
	}
	if err := claim.Release(); err != nil {
		t.Fatal(err)
	}
	claim, err = manager.Claim("session", "turn", "")
	if err != nil || claim == nil {
		t.Fatal(err)
	}
	defer claim.Release()
	rebuilt := append([]model.Span(nil), original...)
	rebuilt[0].TraceID = strings.Repeat("d", 32)
	rebuilt[0].SpanID = strings.Repeat("e", 16)
	rebuilt[1].SpanID = strings.Repeat("f", 16)
	rebuilt[1].Attributes = map[string]any{"gen_ai.conversation.id": "session", "run_id": "turn", "gen_ai.tool.call.id": "call", "triggered_by.llm_span_id": rebuilt[0].SpanID}
	pinned, err := claim.StabilizeSpans(rebuilt)
	if err != nil {
		t.Fatal(err)
	}
	if pinned[0].TraceID != original[0].TraceID || pinned[1].SpanID != original[1].SpanID || pinned[1].Attributes["triggered_by.llm_span_id"] != original[0].SpanID {
		t.Fatal("retry IDs changed")
	}
	count := 0
	if err := VisitUploadedSpans(manager.Root, time.Time{}, "", func(spans []model.Span) error {
		count += len(spans)
		if spans[1].SpanID != original[1].SpanID {
			t.Fatal("recovered wrong span")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("recovered %d identities", count)
	}
	rebuilt[1].Attributes["gen_ai.tool.call.id"] = "other-call"
	if _, err := claim.StabilizeSpans(rebuilt); err == nil {
		t.Fatal("silently rebound a different call")
	}
}
