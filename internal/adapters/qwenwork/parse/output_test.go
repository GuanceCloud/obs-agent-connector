package parse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

func TestOutputMessagesAndPreviews(t *testing.T) {
	in, cfg, events := fixture(t)
	body, err := os.ReadFile(in.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	// Synthetic mixed output exercises text plus tool arguments and redaction.
	text := strings.Replace(string(body), `"type":"tool_use","id":"tool-1"`, `"type":"text","text":"Preparing command"},{"type":"tool_use","id":"tool-1"`, 1)
	text = strings.Replace(text, `"command":"printf TOOL_OK"`, `"command":"printf TOOL_OK","credentials":{"api_key":"fixture-secret"}`, 1)
	in.TranscriptPath = filepath.Join(t.TempDir(), "output.jsonl")
	if err := os.WriteFile(in.TranscriptPath, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	turns, pending, err := Read(in, cfg, events)
	if err != nil || pending || len(turns) != 2 {
		t.Fatalf("read: %v pending=%v turns=%d", err, pending, len(turns))
	}
	if turns[0].LLMCalls[0].OutputPreview != "PROBE_OK" || turns[1].LLMCalls[1].OutputPreview != "TOOL_OK" {
		t.Fatal("pure-text output preview is not plain text")
	}
	if !strings.Contains(turns[1].LLMCalls[0].OutputPreview, "Preparing command") || !strings.Contains(turns[1].LLMCalls[0].OutputPreview, "tool_call") {
		t.Fatal("mixed output lost text or tool request")
	}
	if turns[1].ToolCalls[0].Result != "TOOL_OK" {
		t.Fatal("tool stdout changed")
	}
	messageCount := 0
	for _, turn := range turns {
		request := otlp.SpansToProtoRequest((semantic.Builder{}).Build(turn))
		wire := proto.EncodeExportTraceServiceRequest(request)
		if strings.Contains(string(wire), "fixture-secret") {
			t.Fatal("nested credential leaked")
		}
		decoded, err := proto.DecodeExportTraceServiceRequest(wire)
		if err != nil {
			t.Fatal(err)
		}
		for _, span := range decoded.ResourceSpans[0].ScopeSpans[0].Spans {
			for _, attr := range span.Attributes {
				if attr.Key != "gen_ai.input.messages" && attr.Key != "gen_ai.output.messages" {
					continue
				}
				messageCount++
				var messages []map[string]any
				if attr.Value.StringValue == nil || json.Unmarshal([]byte(*attr.Value.StringValue), &messages) != nil || len(messages) == 0 || messages[0]["role"] == nil || messages[0]["parts"] == nil {
					t.Fatalf("%s message wire format invalid", span.Name)
				}
			}
		}
	}
	// Each root and LLM has input/output; each assistant has output.
	if messageCount != 13 {
		t.Fatalf("expected 13 message attributes across both turns, got %d", messageCount)
	}
	cfg.MaxChars = 4
	clipped, _, err := Read(in, cfg, events)
	if err != nil || len(clipped) != 2 || clipped[0].LLMCalls[0].OutputPreview != "PROB" {
		t.Fatalf("preview clipping failed: %v", err)
	}
	messageBytes, err := json.Marshal(clipped[0].LLMCalls[0].OutputMessages)
	if err != nil || strings.Contains(string(messageBytes), "PROBE_OK") {
		t.Fatal("structured message content bypassed clipping")
	}
	cfg.CaptureContent = "none"
	turns, pending, err = Read(in, cfg, events)
	if err != nil || pending || len(turns) != 2 {
		t.Fatalf("capture none read: %v pending=%v turns=%d", err, pending, len(turns))
	}
	spanCount := 0
	for _, turn := range turns {
		for _, span := range (semantic.Builder{}).Build(turn) {
			spanCount++
			for _, key := range []string{"gen_ai.input.messages", "gen_ai.output.messages", "gtrace.observation.output", "output_preview", "gen_ai.tool.call.result"} {
				if span.Attributes[key] != nil {
					t.Fatalf("capture none retained %s", key)
				}
			}
		}
	}
	if spanCount != 9 {
		t.Fatalf("capture none dropped spans: got %d, want 9", spanCount)
	}
}
