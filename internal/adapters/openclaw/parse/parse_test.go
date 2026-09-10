package parse

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/openclaw/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

const fixture = `{"event":"agent_end","at":2000000,"startedAt":1999000,"sessionId":"s1","runId":"r1","success":true,"durationMs":1000,"prompt":"Read the guide","observations":[
{"kind":"llm_input","at":1999001,"event":{"prompt":"Read the guide"}},
{"kind":"llm_output","at":1999200,"event":{"provider":"test","model":"small","lastAssistant":{"content":[{"type":"text","text":"Reading"}],"usage":{"input":11,"output":2,"cacheRead":3}},"usage":{"input":11,"output":2,"cacheRead":3}}},
{"kind":"after_tool_call","at":1999300,"event":{"toolCallId":"t1","toolName":"read","durationMs":100,"params":{"path":"/skills/demo/SKILL.md","token":"private-value"},"result":"password=secret-value"}},
{"kind":"llm_input","at":1999400,"event":{"prompt":"Read the guide"}},
{"kind":"llm_output","at":1999900,"event":{"provider":"test","model":"small","lastAssistant":{"content":[{"type":"text","text":"Done"}],"stopReason":"stop","usage":{"input":5,"output":4}},"usage":{"input":5,"output":4}}}],
"messages":[{"role":"user","timestamp":1000,"content":"old user"},{"role":"assistant","timestamp":1001,"content":[{"type":"text","text":"stale output"}],"usage":{"input":999}}]}`

func TestNativeTurn(t *testing.T) {
	p, err := Decode([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	turn, ok := Normalize(p, config.Config{CaptureContent: "full", MaxChars: 1000})
	if !ok {
		t.Fatal("missing turn")
	}
	if len(turn.LLMCalls) != 2 || len(turn.ToolCalls) != 1 || turn.Usage.InputTokens != 16 || turn.Usage.OutputTokens != 6 || turn.OutputPreview != "Done" {
		t.Fatalf("bad native turn: %#v", turn)
	}
	if turn.ToolCalls[0].Skill == nil || turn.ToolCalls[0].Skill.Name != "demo" {
		t.Fatal("missing explicit skill read")
	}
	body, _ := json.Marshal(turn)
	for _, secret := range []string{"private-value", "secret-value", "stale output"} {
		if strings.Contains(string(body), secret) {
			t.Fatalf("retained %s", secret)
		}
	}
	spans := (semantic.Builder{}).Build(turn)
	if len(spans) != 6 {
		t.Fatalf("expected root + 2 llm + tool + skill + assistant, got %d", len(spans))
	}
	for _, span := range spans[1:] {
		if span.Name != "skill:demo" && span.ParentID != spans[0].SpanID {
			t.Fatal("incorrect span parent")
		}
	}
}
func TestTerminalFilteringAndPrivacy(t *testing.T) {
	p, _ := Decode([]byte(fixture))
	for _, trigger := range []string{"heartbeat", "cron", "internal", "title"} {
		p.Trigger = trigger
		if _, ok := Normalize(p, config.Config{}); ok {
			t.Fatal("internal run exported")
		}
	}
	p.Trigger = ""
	p.Event = "llm_output"
	if _, ok := Normalize(p, config.Config{}); ok {
		t.Fatal("nonterminal exported")
	}
	p.Event = "agent_end"
	turn, ok := Normalize(p, config.Config{CaptureContent: "none"})
	if !ok {
		t.Fatal("metadata missing")
	}
	if turn.InputPreview != "" || turn.OutputPreview != "" || turn.LLMCalls[0].OutputMessages != nil || turn.ToolCalls[0].Arguments != nil {
		t.Fatal("content-none retained content")
	}
	failed := false
	p.Success = &failed
	p.Error = "Authorization: Bearer private-value"
	turn, ok = Normalize(p, config.Config{CaptureContent: "preview", MaxChars: 1000})
	if !ok || turn.ErrorType != "agent_error" || strings.Contains(turn.Reason, "private-value") {
		t.Fatal("error not sanitized")
	}
	p.Observations[len(p.Observations)-1].Event["lastAssistant"].(map[string]any)["stopReason"] = "aborted"
	turn, ok = Normalize(p, config.Config{})
	if !ok || turn.FinalStatus != model.FinalStatusCancelled {
		t.Fatal("cancelled not recognized")
	}
	p.RunID = ""
	if _, ok = Normalize(p, config.Config{}); ok {
		t.Fatal("unstable identity accepted")
	}
}
func TestSnapshotDoesNotReplayOldTurn(t *testing.T) {
	p, _ := Decode([]byte(fixture))
	p.Observations = nil
	p.Prompt = ""
	if _, ok := Normalize(p, config.Config{}); ok {
		t.Fatal("stale snapshot exported")
	}
	p.Messages[0]["timestamp"] = float64(1999000)
	p.Messages[1]["timestamp"] = float64(1999900)
	if turn, ok := Normalize(p, config.Config{}); !ok || len(turn.LLMCalls) != 0 || !turn.AggregateUsageOnly {
		t.Fatal("snapshot should retain usage without inventing call timing")
	}
}

func TestSnapshotPerCallUsageWinsOverAttemptAggregate(t *testing.T) {
	p, err := Decode([]byte(`{"event":"agent_end","at":2000000,"durationMs":1000,"sessionId":"s","runId":"r","success":true,
 "observations":[{"kind":"llm_output","at":1999900,"event":{"usage":{"input":999},"lastAssistant":{"content":"done","usage":{"input":5}}}}],
 "messages":[{"role":"user","timestamp":1999000,"content":"read"},
 {"role":"assistant","timestamp":1999100,"content":[{"type":"toolCall","id":"tool1","name":"read","arguments":{"path":"/skills/demo/SKILL.md"}}],"usage":{"input":10,"output":2}},
 {"role":"toolResult","timestamp":1999200,"toolCallId":"tool1","content":"guide","isError":true},
 {"role":"assistant","timestamp":1999900,"content":"done","usage":{"input":5,"output":1}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	turn, ok := Normalize(p, config.Config{CaptureContent: "preview", MaxChars: 1000})
	if !ok || len(turn.LLMCalls) != 0 || !turn.AggregateUsageOnly || turn.Usage.InputTokens != 15 || turn.Usage.OutputTokens != 3 {
		t.Fatalf("aggregate miscounted: %#v", turn)
	}
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].ErrorType != "tool_error" || turn.ToolCalls[0].Skill == nil {
		t.Fatal("tool fallback missing")
	}

}

func TestFirstChunkNativeTiming(t *testing.T) {
	p, _ := Decode([]byte(fixture))
	event := map[string]any{"runId": "r1", "callId": "call-1", "provider": "test", "model": "small", "durationMs": float64(400), "timeToFirstByteMs": float64(125), "outcome": "completed"}
	p.Observations = append(p.Observations, Observation{Kind: "model_call_ended", At: 1999900, Event: event}, Observation{Kind: "model_call_ended", At: 1999900, Event: event})
	turn, ok := Normalize(p, config.Config{CaptureContent: "none"})
	if !ok || len(turn.LLMCalls) != 1 {
		t.Fatalf("missing or duplicate native call: %#v", turn.LLMCalls)
	}
	call := turn.LLMCalls[0]
	if call.CallID != "call-1" || call.FirstChunkMs == nil || *call.FirstChunkMs != 125 || call.EndUnixNano-call.StartUnixNano != 400000000 {
		t.Fatalf("bad timing: %#v", call)
	}
	if !turn.AggregateUsageOnly || turn.Usage.InputTokens != 16 || call.Usage.InputTokens != 0 {
		t.Fatal("usage duplicated or incorrectly joined")
	}
	for _, bad := range []any{nil, "100", float64(-1), float64(401), math.NaN(), math.Inf(1)} {
		event["timeToFirstByteMs"] = bad
		turn, _ = Normalize(p, config.Config{})
		if len(turn.LLMCalls) != 1 || turn.LLMCalls[0].FirstChunkMs != nil {
			t.Fatalf("accepted invalid latency %v", bad)
		}
	}
	event["timeToFirstByteMs"] = float64(0)
	event["outcome"] = "error"
	event["errorCategory"] = "timeout"
	turn, _ = Normalize(p, config.Config{})
	if turn.LLMCalls[0].FirstChunkMs == nil || *turn.LLMCalls[0].FirstChunkMs != 0 || turn.LLMCalls[0].ErrorType != "timeout" {
		t.Fatal("observed zero/error dropped")
	}
	event["runId"] = "other-run"
	turn, _ = Normalize(p, config.Config{})
	for _, c := range turn.LLMCalls {
		if c.FirstChunkMs != nil {
			t.Fatal("cross-run timing accepted")
		}
	}
}

func TestHookTimingAndPointOutput(t *testing.T) {
	p, _ := Decode([]byte(fixture))
	turn, ok := Normalize(p, config.Config{CaptureContent: "preview", MaxChars: 1000})
	if !ok || len(turn.LLMCalls) != 2 {
		t.Fatal("missing timed calls")
	}
	for i, want := range []int64{199000000, 500000000} {
		if got := turn.LLMCalls[i].EndUnixNano - turn.LLMCalls[i].StartUnixNano; got != want {
			t.Fatalf("call %d duration %d, want %d", i, got, want)
		}
	}
	spans := (semantic.Builder{}).Build(turn)
	for _, span := range spans {
		if span.Name == "assistant" && (span.EndTimeUnixNano != span.StartTimeUnixNano || span.DurationMs != 0) {
			t.Fatal("assistant event has fabricated duration")
		}
	}
}

func TestSnapshotUsesSingleObservedHookWindow(t *testing.T) {
	p, _ := Decode([]byte(fixture))
	p.Observations = p.Observations[3:]
	p.Messages = []map[string]any{
		{"role": "user", "timestamp": float64(1999000), "content": "hello"},
		{"role": "assistant", "timestamp": float64(1999400), "content": "Done", "usage": map[string]any{"input": float64(5)}},
	}
	turn, ok := Normalize(p, config.Config{})
	if !ok || len(turn.LLMCalls) != 1 || turn.LLMCalls[0].EndUnixNano-turn.LLMCalls[0].StartUnixNano != 500000000 {
		t.Fatal("snapshot replaced observed timing")
	}
	// Multiple unmatched inputs do not provide a unique call boundary.
	p.Observations = append([]Observation{p.Observations[0]}, p.Observations...)
	turn, ok = Normalize(p, config.Config{})
	if !ok || len(turn.LLMCalls) != 0 || !turn.AggregateUsageOnly {
		t.Fatal("ambiguous hook timing fabricated")
	}
}
