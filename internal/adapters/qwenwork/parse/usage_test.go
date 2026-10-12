package parse

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/metrics"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

func TestUsageSnapshotMergeAndMetricTotals(t *testing.T) {
	in, cfg, events := fixture(t)
	body, err := os.ReadFile(in.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	snapshots := map[int]string{
		1: `{"input_tokens":100,"output_tokens":2,"cache_read_input_tokens":20}`,
		2: `{"output_tokens":5,"cache_creation_input_tokens":4,"reasoning_output_tokens":3}`,
		4: `{"input_tokens":200,"output_tokens":8}`,
		5: `{"output_tokens":7,"input_tokens":null}`,
		7: `{"output_tokens":0,"input_tokens":-1,"reasoning_output_tokens":1.5}`,
		8: `{"input_tokens":300,"output_tokens":9}`,
	}
	for i, usage := range snapshots {
		lines[i] = strings.Replace(lines[i], `"model":"flash"`, `"model":"flash","usage":`+usage, 1)
	}
	// Late enrichment of an existing UUID must update usage in replay order,
	// without adding another LLM call or another copy of its content.
	late := strings.Replace(lines[7], snapshots[7], `{"output_tokens":6}`, 1)
	lines = append(lines, late, late)
	in.TranscriptPath = filepath.Join(t.TempDir(), "usage.jsonl")
	if err := os.WriteFile(in.TranscriptPath, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	turns, pending, observation, err := ReadWithUsage(in, cfg, events)
	if err != nil || pending || len(turns) != 2 {
		t.Fatalf("read: %v %v %#v", err, pending, turns)
	}
	wants := []model.Usage{
		{InputTokens: 100, OutputTokens: 5, CacheReadTokens: 20, CacheCreateTokens: 4, ReasoningTokens: 3},
		{InputTokens: 200, OutputTokens: 7},
		{InputTokens: 300, OutputTokens: 6},
	}
	call := 0
	totals := map[string]float64{}
	for _, turn := range turns {
		if turn.Usage != (model.Usage{}) {
			t.Fatal("root usage populated")
		}
		for _, llm := range turn.LLMCalls {
			if llm.Usage != wants[call] {
				t.Fatalf("call %d: got %+v want %+v", call, llm.Usage, wants[call])
			}
			call++
		}
		spans := (semantic.Builder{}).Build(turn)
		for _, span := range spans {
			if span.Name == "invoke_agent" || span.Name == "assistant" {
				for key := range span.Attributes {
					if strings.HasPrefix(key, "gen_ai.usage.") {
						t.Fatalf("%s carries tokens", span.Name)
					}
				}
			}
		}
		for _, metric := range metrics.Build(spans) {
			if metric.Name == "gen_ai.client.token.usage" {
				kind := metric.Attributes["gen_ai.token.type"].(string)
				if kind != "input" && kind != "output" {
					t.Fatalf("unexpected metric: %s", kind)
				}
				totals[kind] += metric.Value
			}
		}
	}
	if call != 3 || totals["input"] != 600 || totals["output"] != 18 {
		t.Fatalf("calls=%d totals=%v", call, totals)
	}
	if observation.LLMCalls != 3 || observation.ObservedCalls != 3 || len(observation.Fields) != 5 {
		t.Fatalf("observation: %+v", observation)
	}
}

func TestUsageMissingZeroAndInvalid(t *testing.T) {
	for _, value := range []any{nil, "12", true, float64(-1), 1.5, math.Inf(1), math.NaN(), float64(1 << 53)} {
		if _, valid := tokenCount(value); valid {
			t.Fatalf("accepted invalid count %#v", value)
		}
	}
	for _, value := range []float64{0, 12, 1<<53 - 1} {
		if count, valid := tokenCount(value); !valid || count != int64(value) {
			t.Fatalf("rejected count %v", value)
		}
	}
	llm := model.LLMCall{CallID: "zero-call", StartUnixNano: 1, EndUnixNano: 2, ExtraAttributes: map[string]any{}}
	mergeUsage(&llm, map[string]any{"input_tokens": float64(0), "output_tokens": "0"})
	if llm.ExtraAttributes["qwenwork.usage.input_tokens.observed"] != true || llm.ExtraAttributes["qwenwork.usage.output_tokens.observed"] != nil {
		t.Fatal("missing and zero conflated")
	}
	spans := (semantic.Builder{}).Build(model.Turn{TurnID: "zero", SessionID: "zero", StartUnixNano: 1, EndUnixNano: 2, FinalStatus: model.FinalStatusCompleted, LLMCalls: []model.LLMCall{llm}})
	found := false
	for _, span := range spans {
		if span.Name == "llm" {
			found = true
			if span.Attributes["qwenwork.usage.input_tokens.observed"] != true || span.Attributes["qwenwork.usage.output_tokens.observed"] != nil {
				t.Fatal("semantic spans lost zero-presence evidence")
			}
			if _, exists := span.Attributes["gen_ai.usage.output_tokens"]; exists {
				t.Fatal("missing output presented as measured count")
			}
		}
	}
	if !found {
		t.Fatal("LLM span missing")
	}
	for _, metric := range metrics.Build(spans) {
		if metric.Name == "gen_ai.client.token.usage" {
			t.Fatal("zero generated token metric")
		}
	}
}

func TestUsageObservationIncludesOnlyAuthorizedPendingTurns(t *testing.T) {
	in, cfg, events := fixture(t)
	body, err := os.ReadFile(in.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	lines[4] = strings.Replace(lines[4], `"model":"flash"`, `"model":"flash","usage":{"input_tokens":0,"output_tokens":null,"secret":"excluded"}`, 1)
	in.TranscriptPath = filepath.Join(t.TempDir(), "pending.jsonl")
	if err := os.WriteFile(in.TranscriptPath, []byte(strings.Join(lines[:6], "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	in.Event = "SessionEnd"
	in.ParentRequestSetID = ""
	_, pending, observation, err := ReadWithUsage(in, cfg, events)
	if err != nil || !pending || observation.LLMCalls != 2 || observation.ObservedCalls != 1 || !reflect.DeepEqual(observation.Fields, []string{"input_tokens"}) {
		t.Fatalf("%v %v %+v", err, pending, observation)
	}
	_, _, historical, err := ReadWithUsage(in, cfg, nil)
	if err != nil || historical.LLMCalls != 0 || historical.ObservedCalls != 0 {
		t.Fatalf("history observed: %+v %v", historical, err)
	}
	b, _ := json.Marshal(observation)
	if strings.Contains(string(b), "excluded") || strings.Contains(string(b), "secret") {
		t.Fatal("unsafe observation")
	}
}

func TestDuplicateUsageCannotCrossTurnOrExtendTiming(t *testing.T) {
	in, cfg, events := fixture(t)
	baseline, _, err := Read(in, cfg, events)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(in.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	// A repeated UUID from the first turn arrives after the second turn's
	// assistant. Ignore this ambiguous enrichment rather than creating a call
	// owned by the second turn or rewriting already-checkpointed history.
	foreign := strings.Replace(lines[2], `"model":"flash"`, `"model":"flash","usage":{"input_tokens":999}`, 1)
	foreign = strings.Replace(foreign, "06:21:09.521Z", "06:21:40Z", 1)
	// A same-turn repeat is useful usage evidence but not a model output event.
	local := strings.Replace(lines[8], `"model":"flash"`, `"model":"flash","usage":{"input_tokens":12}`, 1)
	local = strings.Replace(local, "06:21:34.630Z", "06:21:41Z", 1)
	lines = append(lines, foreign, local)
	in.TranscriptPath = filepath.Join(t.TempDir(), "late.jsonl")
	if err := os.WriteFile(in.TranscriptPath, []byte(strings.Join(lines, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	turns, pending, observation, err := ReadWithUsage(in, cfg, events)
	if err != nil || pending || len(turns) != 2 {
		t.Fatalf("read: %v %v %d", err, pending, len(turns))
	}
	if observation.LLMCalls != 3 || observation.ObservedCalls != 1 || observation.ObservedAt != timestamp("2026-10-08T06:21:41Z") {
		t.Fatalf("observation %+v", observation)
	}
	for i, turn := range turns {
		if len(turn.LLMCalls) != len(baseline[i].LLMCalls) || turn.EndUnixNano != baseline[i].EndUnixNano {
			t.Fatalf("turn %d changed timing/count", i)
		}
		for j, llm := range turn.LLMCalls {
			if llm.StartUnixNano != baseline[i].LLMCalls[j].StartUnixNano || llm.EndUnixNano != baseline[i].LLMCalls[j].EndUnixNano {
				t.Fatalf("call %d/%d extended", i, j)
			}
			want := int64(0)
			if i == 1 && j == 1 {
				want = 12
			}
			if llm.Usage.InputTokens != want {
				t.Fatalf("call %d/%d usage %d", i, j, llm.Usage.InputTokens)
			}
		}
	}
}
