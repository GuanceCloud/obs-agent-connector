package hook

import (
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/parse"
	"testing"
)

func TestLinkedChildHasUniqueIDsAndStableDelegateParent(t *testing.T) {
	s, err := parse.DecodeSnapshot(fixtureBody(t))
	if err != nil {
		t.Fatal(err)
	}
	s.TraceID = "12345678901234567890123456789012"
	turn, err := parse.Normalize(s, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	parent := linkedSpans(turn, s)
	child := s
	child.SessionID = "child-session"
	child.TurnID = "child-turn"
	child.Parent = &parse.Parent{SessionID: s.SessionID, TurnID: s.TurnID, ToolCallID: "call-1"}
	child.TerminalStatus = "aborted"
	ct, err := parse.Normalize(child, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	spans := linkedSpans(ct, child)
	want := identityID(s.SessionID, s.TurnID, "tool", "call-1")
	found := false
	ids := map[string]bool{}
	for _, sp := range parent {
		ids[sp.SpanID] = true
		if sp.SpanID == want {
			found = true
		}
	}
	if !found || spans[0].ParentID != want {
		t.Fatal("child not attached to actual delegate tool")
	}
	for _, sp := range spans {
		if ids[sp.SpanID] || sp.SpanID == sp.ParentID || sp.TraceID != s.TraceID {
			t.Fatal("invalid child span identity")
		}
		ids[sp.SpanID] = true
	}
	if spans[0].Attributes["final_status"] != "cancelled" {
		t.Fatal("native cancellation lost")
	}
	again := linkedSpans(turn, s)
	if again[0].SpanID != parent[0].SpanID {
		t.Fatal("parent identity changed between worker invocations")
	}
}
