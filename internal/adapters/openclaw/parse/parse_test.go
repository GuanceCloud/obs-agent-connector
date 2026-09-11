package parse

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/openclaw/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

const fixture = `{"event":"agent_end","at":2000000,"startedAt":1999000,"sessionId":"s1","runId":"r1","success":true,"durationMs":1000,"prompt":"Read the guide","observations":[
{"kind":"llm_input","at":1999001,"event":{"prompt":"Read the guide"}},
{"kind":"llm_output","at":1999200,"event":{"provider":"test","model":"small","lastAssistant":{"content":[{"type":"text","text":"Reading"}],"usage":{"input":11,"output":2,"cacheRead":3}},"usage":{"input":11,"output":2,"cacheRead":3}}},
{"kind":"after_tool_call","at":1999300,"event":{"toolCallId":"t1","toolName":"read","durationMs":100,"params":{"path":"/skills/demo/SKILL.md","command":"cat /skills/demo/SKILL.md","token":"private-value"},"result":"password=secret-value"}},
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
	if turn.ToolCalls[0].Command != "cat /skills/demo/SKILL.md" || turn.ToolCalls[0].ResultStatus != "completed" {
		t.Fatalf("tool command or result status was not normalized: %#v", turn.ToolCalls[0])
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

func TestStandardMessageAttributesInPreviewMode(t *testing.T) {
	p, err := Decode([]byte(fixture))
	if err != nil {
		t.Fatal(err)
	}
	turn, ok := Normalize(p, config.Config{CaptureContent: "preview", MaxChars: 1000})
	if !ok || len(turn.LLMCalls) != 2 || len(turn.AssistantOutputs) != 1 {
		t.Fatalf("missing normalized content: %#v", turn)
	}
	assertTextMessages(t, turn.InputMessages, "user", "Read the guide")
	assertTextMessages(t, turn.OutputMessages, "assistant", "Done")
	assertTextMessages(t, turn.LLMCalls[0].InputMessages, "user", "Read the guide")
	assertTextMessages(t, turn.LLMCalls[0].OutputMessages, "assistant", "Reading")
	assertTextMessages(t, turn.LLMCalls[1].InputMessages, "user", "Read the guide")
	assertTextMessages(t, turn.LLMCalls[1].OutputMessages, "assistant", "Done")
	assertTextMessages(t, turn.AssistantOutputs[0].OutputMessages, "assistant", "Done")

	spans := (semantic.Builder{}).Build(turn)
	for _, span := range spans {
		if span.Name == "invoke_agent" || span.Name == "llm" {
			if span.Attributes["gen_ai.input.messages"] == nil || span.Attributes["gen_ai.output.messages"] == nil {
				t.Fatalf("%s is missing standard content attributes: %#v", span.Name, span.Attributes)
			}
		}
		if span.Name == "assistant" && span.Attributes["gen_ai.output.messages"] == nil {
			t.Fatalf("assistant is missing standard output messages: %#v", span.Attributes)
		}
		if (span.Name == "invoke_agent" || span.Name == "llm" || span.Name == "assistant") && span.Attributes["gen_ai.output.type"] != "text" {
			t.Fatalf("%s is missing standard output type: %#v", span.Name, span.Attributes)
		}
	}
}

func TestStructuredModelMessagesAndRequestMetadata(t *testing.T) {
	p, err := Decode([]byte(`{"event":"agent_end","at":2000000,"startedAt":1999000,"sessionId":"s1","runId":"r1","success":true,"prompt":"current","observations":[
{"kind":"llm_input","at":1999100,"event":{"provider":"test","model":"small","systemPrompt":"be helpful","prompt":"current","historyMessages":[{"role":"user","content":"previous"},{"role":"toolResult","toolCallId":"tool-1","content":"result"}],"tools":[{"name":"read","description":"Read a file","parameters":{"type":"object"}}]}},
{"kind":"llm_output","at":1999500,"event":{"provider":"test","model":"small","reasoningEffort":"high","lastAssistant":{"role":"assistant","stopReason":"toolUse","content":[{"type":"thinking","thinking":"checking"},{"type":"toolCall","id":"tool-2","name":"read","arguments":{"path":"README.md"}}]}}}],
"messages":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	turn, ok := Normalize(p, config.Config{CaptureContent: "full", MaxChars: 1000})
	if !ok || len(turn.LLMCalls) != 1 {
		t.Fatalf("missing structured call: %#v", turn)
	}
	call := turn.LLMCalls[0]
	inputs, _ := call.InputMessages.([]any)
	outputs, _ := call.OutputMessages.([]any)
	tools, _ := call.ToolDefinitions.([]any)
	if len(inputs) != 3 || len(outputs) != 1 || len(tools) != 1 || call.SystemInstructions == nil {
		t.Fatalf("request context was not preserved: %#v", call)
	}
	parts := outputs[0].(map[string]any)["parts"].([]any)
	if len(parts) != 2 || parts[0].(map[string]any)["type"] != "reasoning" || parts[1].(map[string]any)["type"] != "tool_call" {
		t.Fatalf("structured output was flattened: %#v", outputs)
	}
	if call.OutputKind != "tool_call" || len(call.FinishReasons) != 1 || call.FinishReasons[0] != "tool_call" || call.ExtraAttributes["openclaw.reasoning_effort"] != "high" {
		t.Fatalf("model metadata was not normalized: %#v", call)
	}
}

func TestSnapshotKeepsStructuredToolCallOutput(t *testing.T) {
	p, err := Decode([]byte(`{"event":"agent_end","at":2000000,"startedAt":1999000,"sessionId":"s1","runId":"r1","success":true,"messages":[
{"role":"user","timestamp":1999000,"content":"read"},
{"role":"assistant","timestamp":1999100,"content":[{"type":"toolCall","id":"tool-1","name":"read","arguments":{"path":"README.md"}}],"usage":{"input":4,"output":1}},
{"role":"toolResult","timestamp":1999200,"toolCallId":"tool-1","content":"done"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	turn, ok := Normalize(p, config.Config{CaptureContent: "preview", MaxChars: 1000})
	if !ok || turn.OutputKind != "tool_call" || turn.OutputMessages == nil || len(turn.LLMCalls) != 0 || len(turn.ToolCalls) != 1 {
		t.Fatalf("structured snapshot output was lost or timing was fabricated: %#v", turn)
	}
	root := (semantic.Builder{}).Build(turn)[0]
	if root.Attributes["output_kind"] != "tool_call" || root.Attributes["gen_ai.output.messages"] == nil {
		t.Fatalf("root tool-call output is incomplete: %#v", root.Attributes)
	}
}

func TestSingleModelSnapshotRecoversLLMWhenTypedHooksAreMissing(t *testing.T) {
	p, err := Decode([]byte(`{"event":"agent_end","at":2000000,"startedAt":1999000,"sessionId":"s1","runId":"r1","success":true,"prompt":"current","messages":[
{"role":"user","timestamp":1999000,"content":"current"},
{"role":"assistant","timestamp":1999900,"provider":"openai","model":"gpt-5.5","stopReason":"stop","content":[{"type":"text","text":"done"}],"usage":{"input":12,"output":3,"cacheRead":4}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	turn, ok := Normalize(p, config.Config{CaptureContent: "preview", MaxChars: 1000})
	if !ok || len(turn.LLMCalls) != 1 {
		t.Fatalf("missing transcript-backed LLM call: %#v", turn)
	}
	call := turn.LLMCalls[0]
	if call.Provider != "openai" || call.RequestModel != "gpt-5.5" || call.EndUnixNano-call.StartUnixNano != 900*int64(time.Millisecond) {
		t.Fatalf("invalid transcript-backed LLM identity or timing: %#v", call)
	}
	if call.ExtraAttributes["openclaw.timing_source"] != "transcript_turn_boundary" || call.Usage.InputTokens != 12 || call.Usage.OutputTokens != 3 || call.Usage.CacheReadTokens != 4 || turn.AggregateUsageOnly {
		t.Fatalf("transcript-backed LLM metadata was lost: %#v", call)
	}
	assertTextMessages(t, call.InputMessages, "user", "current")
	assertTextMessages(t, call.OutputMessages, "assistant", "done")
	spans := (semantic.Builder{}).Build(turn)
	if len(spans) != 3 || spans[1].Name != "llm" {
		t.Fatalf("expected invoke_agent, llm, and assistant spans: %#v", spans)
	}
}

func TestNativeStartBoundariesAndMissingToolTiming(t *testing.T) {
	p, _ := Decode([]byte(fixture))
	p.Observations = append(p.Observations,
		Observation{Kind: "model_call_started", At: 1999410, Event: map[string]any{"runId": "r1", "callId": "call-1", "provider": "test", "model": "small"}},
		Observation{Kind: "model_call_ended", At: 1999900, Event: map[string]any{"runId": "r1", "callId": "call-1", "provider": "test", "model": "small", "durationMs": float64(400), "outcome": "completed"}},
	)
	delete(p.Observations[2].Event, "durationMs")
	turn, ok := Normalize(p, config.Config{CaptureContent: "none"})
	if !ok || len(turn.LLMCalls) != 1 || turn.LLMCalls[0].EndUnixNano-turn.LLMCalls[0].StartUnixNano != 490*int64(time.Millisecond) {
		t.Fatalf("native model boundaries were not used: %#v", turn.LLMCalls)
	}
	if len(turn.ToolCalls) != 0 {
		t.Fatalf("tool duration was fabricated: %#v", turn.ToolCalls)
	}

	p, _ = Decode([]byte(fixture))
	p.Observations = append(p.Observations, Observation{Kind: "before_tool_call", At: 1999210, Event: map[string]any{"toolCallId": "t1", "toolName": "read"}})
	delete(p.Observations[2].Event, "durationMs")
	turn, _ = Normalize(p, config.Config{CaptureContent: "none"})
	if len(turn.ToolCalls) != 1 || turn.ToolCalls[0].EndUnixNano-turn.ToolCalls[0].StartUnixNano != 90*int64(time.Millisecond) {
		t.Fatalf("native tool boundaries were not used: %#v", turn.ToolCalls)
	}

	p, _ = Decode([]byte(fixture))
	p.Observations = []Observation{{Kind: "model_call_ended", At: 1999900, Event: map[string]any{"runId": "r1", "callId": "zero", "provider": "test", "model": "small", "durationMs": float64(0), "outcome": "completed"}}}
	p.Messages = nil
	turn, _ = Normalize(p, config.Config{CaptureContent: "none"})
	if len(turn.LLMCalls) != 0 {
		t.Fatalf("zero model duration was padded into a call: %#v", turn.LLMCalls)
	}
}

func TestNativeCallUsesOnlyUniqueContentWindow(t *testing.T) {
	p, _ := Decode([]byte(fixture))
	event := map[string]any{"runId": "r1", "callId": "call-1", "provider": "test", "model": "small", "durationMs": float64(400), "outcome": "completed"}
	p.Observations = append(p.Observations, Observation{Kind: "model_call_ended", At: 1999900, Event: event})
	turn, ok := Normalize(p, config.Config{CaptureContent: "preview", MaxChars: 1000})
	if !ok || len(turn.LLMCalls) != 1 {
		t.Fatalf("missing native call: %#v", turn.LLMCalls)
	}
	assertTextMessages(t, turn.LLMCalls[0].InputMessages, "user", "Read the guide")
	assertTextMessages(t, turn.LLMCalls[0].OutputMessages, "assistant", "Done")
	if turn.LLMCalls[0].ExtraAttributes["openclaw.content_source"] != "unique_hook_window" {
		t.Fatal("native content was not joined through a unique window")
	}

	// A second native call inside the same content window makes attribution
	// ambiguous, so neither call may inherit the shared input/output content.
	second := map[string]any{"runId": "r1", "callId": "call-2", "provider": "test", "model": "small", "durationMs": float64(200), "outcome": "completed"}
	p.Observations = append(p.Observations, Observation{Kind: "model_call_ended", At: 1999800, Event: second})
	turn, _ = Normalize(p, config.Config{CaptureContent: "preview", MaxChars: 1000})
	if len(turn.LLMCalls) != 2 {
		t.Fatalf("missing ambiguous native calls: %#v", turn.LLMCalls)
	}
	for _, call := range turn.LLMCalls {
		if call.InputMessages != nil || call.OutputMessages != nil {
			t.Fatal("ambiguous content was assigned to a provider call")
		}
	}
}

func TestTimestampedSnapshotDoesNotSuppressHookCalls(t *testing.T) {
	p, _ := Decode([]byte(fixture))
	p.Messages[0]["timestamp"] = float64(1999000)
	p.Messages[1]["timestamp"] = float64(1999900)
	turn, ok := Normalize(p, config.Config{CaptureContent: "preview", MaxChars: 1000})
	if !ok || len(turn.LLMCalls) != 2 {
		t.Fatalf("timestamped snapshot suppressed observed hook calls: %#v", turn.LLMCalls)
	}
	if turn.LLMCalls[0].ExtraAttributes["openclaw.timing_source"] != "native_hook_boundary" || turn.LLMCalls[1].ExtraAttributes["openclaw.timing_source"] != "native_hook_boundary" {
		t.Fatalf("hook timing source was not retained: %#v", turn.LLMCalls)
	}
}

func assertTextMessages(t *testing.T, value any, role, content string) {
	t.Helper()
	messages, ok := value.([]any)
	if !ok || len(messages) != 1 {
		t.Fatalf("messages do not follow the GenAI schema: %#v", value)
	}
	message, ok := messages[0].(map[string]any)
	if !ok || message["role"] != role {
		t.Fatalf("invalid message role: %#v", value)
	}
	parts, ok := message["parts"].([]any)
	if !ok || len(parts) != 1 {
		t.Fatalf("invalid message parts: %#v", value)
	}
	part, ok := parts[0].(map[string]any)
	if !ok || part["type"] != "text" || part["content"] != content {
		t.Fatalf("invalid text part: %#v", value)
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
	if turn.InputPreview != "" || turn.OutputPreview != "" || turn.InputMessages != nil || turn.OutputMessages != nil ||
		turn.LLMCalls[0].InputMessages != nil || turn.LLMCalls[0].OutputMessages != nil || turn.AssistantOutputs[0].OutputMessages != nil || turn.ToolCalls[0].Arguments != nil || turn.ToolCalls[0].Command != "" {
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

func TestHookTimingAndOutputPhase(t *testing.T) {
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
		if span.Name == "assistant" && span.DurationMs != 100 {
			t.Fatal("assistant output phase does not use observed completion")
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

func TestLongNativeCallAndOutputPhase(t *testing.T) {
	p, _ := Decode([]byte(fixture))
	p.At = 2040470
	p.DurationMs = 41470
	p.Observations = []Observation{{Kind: "model_call_ended", At: 2040000, Event: map[string]any{"runId": "r1", "callId": "call1", "provider": "test", "durationMs": float64(40000), "outcome": "completed"}}}
	p.Messages = []map[string]any{{"role": "user", "timestamp": float64(1999000), "content": "hi"}, {"role": "assistant", "timestamp": float64(2040000), "content": "done", "usage": map[string]any{"input": float64(10)}}}
	turn, ok := Normalize(p, config.Config{CaptureContent: "preview", MaxChars: 1000})
	if !ok || len(turn.LLMCalls) != 1 || len(turn.AssistantOutputs) != 1 {
		t.Fatal("missing timed turn")
	}
	spans := (semantic.Builder{}).Build(turn)
	for _, span := range spans {
		want := map[string]int64{"invoke_agent": 41470, "llm": 40000, "assistant": 470}
		if duration, found := want[span.Name]; found && span.DurationMs != duration {
			t.Fatalf("%s duration %d, want %d", span.Name, span.DurationMs, duration)
		}
	}
	p.Observations = nil
	turn, ok = Normalize(p, config.Config{CaptureContent: "preview", MaxChars: 1000})
	if !ok || len(turn.LLMCalls) != 0 || turn.Usage.InputTokens != 10 {
		t.Fatal("untimed snapshot lost or fabricated timing")
	}
	for _, span := range (semantic.Builder{}).Build(turn) {
		if span.Name == "assistant" && (span.DurationMs != 0 || span.StartTimeUnixNano != span.EndTimeUnixNano) {
			t.Fatal("unknown output phase was padded")
		}
	}
}
