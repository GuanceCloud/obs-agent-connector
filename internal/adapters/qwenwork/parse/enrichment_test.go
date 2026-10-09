package parse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

func TestAssistantOutputOrderAssociationAndSyntheticDuration(t *testing.T) {
	in, cfg, events := fixture(t)
	body, _ := os.ReadFile(in.TranscriptPath)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	lines[4] = strings.Replace(lines[4], `"type":"thinking","thinking":"excluded reasoning"`, `"type":"text","text":"Preparing"`, 1)
	lines[5] = strings.Replace(lines[5], `"type":"tool_use"`, `"type":"text","text":"Preparing"},{"type":"text","text":"command"},{"type":"tool_use"`, 1)
	// A repeated block under a distinct record and an identical UUID replay
	// cannot produce another output or extend its timestamp.
	duplicate := strings.Replace(lines[5], `"uuid":"a4"`, `"uuid":"a4-repeat"`, 1)
	lines = append(lines, lines[5], duplicate)
	in.TranscriptPath = filepath.Join(t.TempDir(), "output.jsonl")
	if err := os.WriteFile(in.TranscriptPath, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	in.ObservedAt = timestamp("2026-10-08T06:21:34.630Z")
	turns, pending, err := Read(in, cfg, events)
	if err != nil || pending || len(turns) != 2 {
		t.Fatalf("read: %v %v %d", err, pending, len(turns))
	}
	turn := turns[1]
	if len(turn.AssistantOutputs) != 2 || turn.OutputPreview != "TOOL_OK" {
		t.Fatalf("output granularity/root: %+v", turn.AssistantOutputs)
	}
	for i, want := range []string{"Preparing command", "TOOL_OK"} {
		output := turn.AssistantOutputs[i]
		if output.OutputPreview != want || output.TriggeringLLMCall != turn.LLMCalls[i].CallID || output.EndUnixNano-output.StartUnixNano != int64(time.Millisecond) || output.ExtraAttributes["duration_source"] != "synthetic" {
			t.Fatalf("output %d: %+v", i, output)
		}
		if output.EndUnixNano > turn.EndUnixNano {
			t.Fatal("assistant outside root")
		}
	}
	if turn.EndUnixNano != in.ObservedAt+int64(time.Millisecond) || turn.LLMCalls[1].EndUnixNano != in.ObservedAt {
		t.Fatal("synthetic duration altered LLM or failed root containment")
	}
	spans := (semantic.Builder{}).Build(turn)
	llmIDs := []string{}
	for _, span := range spans {
		if span.Name == "llm" {
			llmIDs = append(llmIDs, span.SpanID)
		}
	}
	i := 0
	for _, span := range spans {
		if span.Name != "assistant" {
			continue
		}
		if span.ParentID != spans[0].SpanID || span.Attributes["triggered_by.llm_span_id"] != llmIDs[i] || span.DurationMs != 1 {
			t.Fatalf("assistant graph/duration: %+v", span)
		}
		i++
	}
	if i != 2 {
		t.Fatal("duplicate assistant span")
	}
}

func TestThinkingPrivacyAndCumulativeBudget(t *testing.T) {
	in, cfg, events := fixture(t)
	body, _ := os.ReadFile(in.TranscriptPath)
	text := strings.ReplaceAll(string(body), `"thinking":"excluded reasoning"`, `"thinking":"native-only password=fixture-secret safe","signature":"never-signature"`)
	text = strings.Replace(text, `"type":"tool_use"`, `"type":"thinking","thinking":"second-fragment `+strings.Repeat("\u754c", 100)+`"},{"type":"redacted_thinking","data":"never-redacted"},{"type":"tool_use"`, 1)
	in.TranscriptPath = filepath.Join(t.TempDir(), "thinking.jsonl")
	if err := os.WriteFile(in.TranscriptPath, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"preview", "full", "none"} {
		for _, enabled := range []bool{false, true} {
			cfg.CaptureContent, cfg.CaptureThinking, cfg.MaxChars = mode, enabled, 64
			turns, pending, err := Read(in, cfg, events)
			if err != nil || pending || len(turns) != 2 {
				t.Fatalf("read: %v %v", err, pending)
			}
			all, _ := json.Marshal(turns)
			for _, forbidden := range []string{"fixture-secret", "never-signature", "never-redacted"} {
				if strings.Contains(string(all), forbidden) {
					t.Fatalf("leaked %s", forbidden)
				}
			}
			want := enabled && mode != "none"
			if strings.Contains(string(all), "native-only") != want {
				t.Fatalf("thinking gate mode=%s enabled=%v", mode, enabled)
			}
			for _, turn := range turns {
				for _, call := range turn.LLMCalls {
					b, _ := json.Marshal([]any{call.InputMessages, call.InputPreview, call.OutputPreview, turn.OutputMessages, turn.AssistantOutputs})
					if strings.Contains(string(b), "native-only") || strings.Contains(string(b), "second-fragment") {
						t.Fatal("reasoning leaked outside matching LLM output")
					}
					n := 0
					if messages, ok := call.OutputMessages.([]any); ok {
						for _, part := range object(messages[0])["parts"].([]any) {
							p := object(part)
							if p["type"] == "reasoning" {
								n += utf8.RuneCountInString(str(p["content"]))
							}
						}
					}
					if n > cfg.MaxChars || want && n == 0 || !want && n != 0 {
						t.Fatalf("reasoning budget/gate %d", n)
					}
				}
			}
		}
	}
}

func TestThinkingOnlyAndToolOnlyDoNotCreateAssistant(t *testing.T) {
	in, cfg, events := fixture(t)
	cfg.CaptureThinking = true
	body, _ := os.ReadFile(in.TranscriptPath)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	// No final text; an explicit cancelled Stop authorizes a partial turn.
	in.TranscriptPath = filepath.Join(t.TempDir(), "partial.jsonl")
	in.Reason = "cancelled"
	if err := os.WriteFile(in.TranscriptPath, []byte(strings.Join(lines[:8], "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	turns, pending, err := Read(in, cfg, events)
	if err != nil || pending || len(turns) != 2 || len(turns[1].AssistantOutputs) != 0 || turns[1].FinalStatus != model.FinalStatusCancelled {
		t.Fatalf("partial output: %v %v %+v", err, pending, turns)
	}
	// Numeric usage and timing are independent of reasoning capture.
	cfg.CaptureThinking = false
	without, _, _ := Read(in, cfg, events)
	for i, call := range turns[1].LLMCalls {
		other := without[1].LLMCalls[i]
		if !reflect.DeepEqual(call.Usage, other.Usage) || call.StartUnixNano != other.StartUnixNano || call.EndUnixNano != other.EndUnixNano {
			t.Fatal("thinking changed usage or timing")
		}
	}
}
