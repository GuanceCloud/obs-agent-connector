package parse

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/config"
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
	s, err := DecodeSnapshot(body)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func testConfig(t *testing.T) config.Config {
	return config.Config{Enabled: true, MaxChars: 20000, CaptureContent: "preview", StateDir: t.TempDir(), Resource: map[string]any{"agent_name": "test-omp"}}
}

func TestNormalizeOMPCallsUsagePrivacyAndMetrics(t *testing.T) {
	cfg := testConfig(t)
	turn, err := Normalize(fixtureSnapshot(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.LLMCalls) != 2 || len(turn.ToolCalls) != 1 || turn.Usage.InputTokens != 35 || turn.Usage.OutputTokens != 12 {
		t.Fatalf("unexpected normalized turn: %#v", turn)
	}
	if turn.LLMCalls[0].EndUnixNano >= turn.ToolCalls[0].EndUnixNano {
		t.Fatal("LLM duration includes tool execution")
	}
	if turn.ToolCalls[0].TriggeringLLMCall != turn.LLMCalls[0].CallID || turn.ToolCalls[0].Skill == nil {
		t.Fatal("tool/skill association missing")
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
	for _, span := range spans {
		if span.Name == "assistant" {
			if _, ok := span.Attributes["gen_ai.usage.output_tokens"]; ok {
				t.Fatal("assistant carries tokens")
			}
		}
	}
}

func TestNormalizeOMPInternalIncompleteCancelledAndNone(t *testing.T) {
	cfg := testConfig(t)
	s := fixtureSnapshot(t)
	s.Events[0].Message.Synthetic = true
	turn, err := Normalize(s, cfg)
	if err != nil || turn.SessionID != "" {
		t.Fatal("synthetic-only request exported")
	}
	s = fixtureSnapshot(t)
	s.End = 0
	if _, err := Normalize(s, cfg); err == nil {
		t.Fatal("accepted nonterminal snapshot")
	}
	s = fixtureSnapshot(t)
	s.Events = append(s.Events[:4], s.Events[5:]...)
	turn, err = Normalize(s, cfg)
	if err != nil || turn.SessionID != "" {
		t.Fatal("accepted incomplete successful tool")
	}
	s.Events[len(s.Events)-1].Message.StopReason = "aborted"
	turn, err = Normalize(s, cfg)
	if err != nil || turn.FinalStatus != model.FinalStatusCancelled || turn.ToolCalls[0].ErrorType == "" {
		t.Fatal("cancelled request not normalized")
	}
	s = fixtureSnapshot(t)
	s.Events[len(s.Events)-1].Message.StopReason = "error"
	turn, _ = Normalize(s, cfg)
	if turn.ErrorType != "omp.model_error" {
		t.Fatal("model error missing")
	}
	cfg.CaptureContent = "none"
	turn, err = Normalize(fixtureSnapshot(t), cfg)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(turn)
	for _, text := range []string{"Read the skill", "Hello", "private-value", "/synthetic"} {
		if strings.Contains(string(body), text) {
			t.Fatalf("capture none leaked %q", text)
		}
	}
	if len((semantic.Builder{}).Build(turn)) == 0 {
		t.Fatal("capture none discarded telemetry")
	}
}

// Use the content-free wire shape produced by the extension, not a raw
// transcript that still contains text at normalization time.
func TestContentFreeAssistantPreservesSpan(t *testing.T) {
	s := fixtureSnapshot(t)
	s.Events = []Event{s.Events[0], s.Events[len(s.Events)-1]}
	s.Events[0].Message.HasContent = true
	s.Events[0].Message.Content = nil
	s.Events[1].Message.HasText = true
	s.Events[1].Message.Content = []any{map[string]any{"type": "text"}}
	cfg := testConfig(t)
	cfg.CaptureContent = "none"
	turn, err := Normalize(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(turn.AssistantOutputs) != 1 || len(turn.LLMCalls) != 1 {
		t.Fatalf("missing content-free events: %#v", turn)
	}
	spans := (semantic.Builder{}).Build(turn)
	if len(spans) != 3 {
		t.Fatalf("expected root, llm and assistant; got %d", len(spans))
	}
	for _, span := range spans {
		for _, key := range []string{"input_preview", "output_preview", "gen_ai.input.messages", "gen_ai.output.messages"} {
			if _, exists := span.Attributes[key]; exists {
				t.Fatalf("content-free span retained %s", key)
			}
		}
	}
}

func TestImageOnlyUserRequestPreservesTelemetry(t *testing.T) {
	s := fixtureSnapshot(t)
	// The extension records image presence but never persists image bytes.
	s.Events[0].Message.Content = []any{map[string]any{"type": "omitted"}}
	s.Events[0].Message.HasContent = true
	for _, mode := range []string{"preview", "none"} {
		cfg := testConfig(t)
		cfg.CaptureContent = mode
		turn, err := Normalize(s, cfg)
		if err != nil || turn.SessionID == "" || len(turn.LLMCalls) != 2 || turn.Usage.InputTokens != 35 {
			t.Fatalf("image-only request lost for %s: %#v, %v", mode, turn, err)
		}
	}
	// Empty input is still excluded even when a model event exists.
	s.Events[0].Message.HasContent = false
	s.Events[0].Message.Content = ""
	turn, err := Normalize(s, testConfig(t))
	if err != nil || turn.SessionID != "" {
		t.Fatal("empty request exported")
	}
}

func TestCancelledIncompleteSkillInheritsToolError(t *testing.T) {
	s := fixtureSnapshot(t)
	s.Events = append(s.Events[:4], s.Events[5:]...)
	s.Events[len(s.Events)-1].Message.StopReason = "aborted"
	turn, err := Normalize(s, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, span := range (semantic.Builder{}).Build(turn) {
		if strings.HasPrefix(span.Name, "skill:") {
			found = true
			if span.Attributes["status"] != "error" || span.Attributes["error.type"] != "omp.incomplete_tool" || span.Status.Code != "STATUS_CODE_ERROR" {
				t.Fatalf("cancelled skill reported success: %#v", span)
			}
		}
	}
	if !found {
		t.Fatal("missing skill span")
	}
}
