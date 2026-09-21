package parse

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/pi/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/metrics"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

func fixtureSnapshot(t *testing.T) Snapshot {
	t.Helper()
	body, err := os.ReadFile("testdata/terminal.json")
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := DecodeSnapshot(body)
	if err != nil {
		t.Fatal(err)
	}
	return snapshot
}

func testConfig(t *testing.T) config.Config {
	return config.Config{Enabled: true, MaxChars: 20000, CaptureContent: "preview", StateDir: t.TempDir(), Resource: map[string]any{"agent_name": "test-pi"}}
}

func TestNormalizePiNativeTimingsUsageSkillAndMetrics(t *testing.T) {
	turn, err := Normalize(fixtureSnapshot(t), testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if turn.AgentRuntime != "pi" || len(turn.LLMCalls) != 2 || len(turn.ToolCalls) != 1 {
		t.Fatalf("unexpected normalized turn: %#v", turn)
	}
	if turn.LLMCalls[0].TTFTMs != 15 || turn.LLMCalls[1].TTFTMs != 40 {
		t.Fatalf("TTFT must use native first-token times: %#v", turn.LLMCalls)
	}
	if turn.LLMCalls[0].EndUnixNano >= turn.ToolCalls[0].EndUnixNano {
		t.Fatal("LLM duration includes tool execution")
	}
	if turn.Usage.InputTokens != 35 || turn.Usage.OutputTokens != 12 || turn.Usage.CacheReadTokens != 3 || turn.Usage.CacheCreateTokens != 2 {
		t.Fatalf("unexpected usage: %#v", turn.Usage)
	}
	skill := turn.ToolCalls[0].Skill
	if skill == nil || skill.Name != "example" || skill.SourceType != "workspace" || skill.Description != "A synthetic skill" || skill.Version != "1.2.3" {
		t.Fatalf("unexpected skill: %#v", skill)
	}
	body, _ := json.Marshal(turn)
	if strings.Contains(string(body), "private-value") {
		t.Fatal("secret leaked")
	}
	spans := (semantic.Builder{}).Build(turn)
	kinds := map[string]bool{}
	for _, item := range metrics.Build(spans) {
		kinds[item.Name] = true
	}
	if len(kinds) != 4 {
		t.Fatalf("expected four metrics, got %v", kinds)
	}
}

func TestNormalizePiPrivacyAndTerminalRules(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	snapshot.End = 0
	if _, err := Normalize(snapshot, testConfig(t)); err == nil {
		t.Fatal("accepted nonterminal snapshot")
	}

	snapshot = fixtureSnapshot(t)
	for index := range snapshot.Events {
		if snapshot.Events[index].Type == "tool_end" {
			snapshot.Events = append(snapshot.Events[:index], snapshot.Events[index+1:]...)
			break
		}
	}
	turn, err := Normalize(snapshot, testConfig(t))
	if err != nil || turn.SessionID != "" {
		t.Fatal("accepted incomplete successful tool")
	}

	snapshot = fixtureSnapshot(t)
	for index := range snapshot.Events {
		if snapshot.Events[index].Type == "assistant" {
			snapshot.Events[index].Message.StopReason = "aborted"
		}
	}
	turn, err = Normalize(snapshot, testConfig(t))
	if err != nil || turn.FinalStatus != model.FinalStatusCancelled {
		t.Fatalf("cancelled request not normalized: %#v", turn)
	}

	cfg := testConfig(t)
	cfg.CaptureContent = "none"
	turn, err = Normalize(fixtureSnapshot(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(turn)
	for _, value := range []string{"Read the skill", "Hello", "private-value", "/synthetic"} {
		if strings.Contains(string(body), value) {
			t.Fatalf("capture none leaked %q", value)
		}
	}
	if turn.ToolCalls[0].Skill == nil || turn.ToolCalls[0].Skill.Name != "example" {
		t.Fatal("capture none discarded skill identity")
	}
}

func TestNormalizePiSubagentResultSummary(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	snapshot.TraceID = "11111111111111111111111111111111"
	snapshot.RootSpanID = "2222222222222222"
	start := Event{Type: "tool_start", At: snapshot.Start + 901, CallID: "sub-1", SpanID: "3333333333333333", Name: "subagent", Args: map[string]any{"agent": "worker"}}
	end := Event{Type: "tool_end", At: snapshot.Start + 950, CallID: "sub-1", Name: "subagent", Result: map[string]any{"details": map[string]any{"results": []any{
		map[string]any{"exitCode": float64(0), "model": "test/fast"},
		map[string]any{"exitCode": float64(1), "model": "test/deep"},
	}}}}
	snapshot.Events = append(snapshot.Events, start, end)
	turn, err := Normalize(snapshot, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	tool := turn.ToolCalls[len(turn.ToolCalls)-1]
	if turn.TraceID != snapshot.TraceID || turn.RootSpanID != snapshot.RootSpanID || tool.SpanID != start.SpanID {
		t.Fatalf("explicit Pi span IDs missing: turn=%#v tool=%#v", turn, tool)
	}
	if tool.ExtraAttributes["subagent.count"] != 2 || tool.ExtraAttributes["subagent.failed_count"] != 1 {
		t.Fatalf("subagent summary missing: %#v", tool.ExtraAttributes)
	}
}

func TestNormalizePiOmitsTTFTWithoutNativeFirstTokenEvent(t *testing.T) {
	snapshot := fixtureSnapshot(t)
	events := snapshot.Events[:0]
	for _, event := range snapshot.Events {
		if event.Type != "first_token" {
			events = append(events, event)
		}
	}
	snapshot.Events = events
	turn, err := Normalize(snapshot, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, call := range turn.LLMCalls {
		if call.TTFTMs != 0 {
			t.Fatalf("estimated TTFT was emitted without a native event: %#v", call)
		}
	}
}
