package collector

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/metrics"
)

type normalizedSpan struct {
	Key        string         `json:"key"`
	Name       string         `json:"name"`
	ParentKey  string         `json:"parent_key,omitempty"`
	DurationMs int64          `json:"duration_ms"`
	StatusCode string         `json:"status_code,omitempty"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type normalizedMetric struct {
	Name       string         `json:"name"`
	Unit       string         `json:"unit"`
	Value      float64        `json:"value"`
	Attributes map[string]any `json:"attributes,omitempty"`
}

type goldenOutput struct {
	Spans   []normalizedSpan   `json:"spans"`
	Metrics []normalizedMetric `json:"metrics"`
}

func TestCollectorAndMetricsMatchLegacyGoldenForSkillFixture(t *testing.T) {
	home := t.TempDir()
	userSkillDir := filepath.Join(home, ".codex", "skills", "dashboard")
	if err := os.MkdirAll(userSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userSkillFile := filepath.Join(userSkillDir, "SKILL.md")
	if err := os.WriteFile(userSkillFile, []byte(`---
name: dashboard
description: Build dashboard assets.
version: 1.4.0
---

Build dashboard assets.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	rollout := filepath.Join(home, "rollout.jsonl")
	body := []byte(joinJSONLines(
		row("2026-06-03T10:00:00.000Z", "session_meta", map[string]any{
			"id":             "sess-skill-order",
			"cli_version":    "0.140.0",
			"model_provider": "openai",
			"timestamp":      "2026-06-03T09:59:58.000Z",
			"source":         "cli",
			"base_instructions": map[string]any{
				"text": "You are a file assistant.",
			},
		}),
		row("2026-06-03T10:00:01.000Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-skill-order",
		}),
		row("2026-06-03T10:00:01.100Z", "turn_context", map[string]any{
			"model":             "gpt-5.4",
			"response_format":   map[string]any{"type": "json_object"},
			"n":                 2,
			"seed":              7,
			"temperature":       0.2,
			"top_p":             0.9,
			"max_output_tokens": 512,
			"presence_penalty":  0.3,
			"frequency_penalty": 0.4,
			"stop_sequences":    []any{"DONE"},
			"tools": []any{
				map[string]any{
					"type":        "function",
					"name":        "exec_command",
					"description": "Run a shell command",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"command": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
					},
				},
			},
			"collaboration_mode": map[string]any{
				"settings": map[string]any{
					"developer_instructions": "Keep answers concise.",
				},
			},
		}),
		row("2026-06-03T10:00:01.200Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "Build a dashboard",
		}),
		row("2026-06-03T10:00:02.000Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "I will read the dashboard skill first."},
			},
		}),
		row("2026-06-03T10:00:02.050Z", "response_item", map[string]any{
			"type":    "function_call",
			"name":    "exec_command",
			"call_id": "call-skill-order",
			"arguments": mustJSON(map[string]any{
				"command": []string{"sed", "-n", "1,80p", userSkillFile},
			}),
		}),
		row("2026-06-03T10:00:02.200Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens":  20,
					"output_tokens": 6,
					"total_tokens":  26,
				},
			},
		}),
		row("2026-06-03T10:00:02.500Z", "event_msg", map[string]any{
			"type":    "exec_command_end",
			"call_id": "call-skill-order",
			"status":  "completed",
			"stdout":  stringsRepeat("x", 5000),
		}),
		row("2026-06-03T10:00:03.000Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "I have finished reading the skill."},
			},
		}),
		row("2026-06-03T10:00:03.100Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens":  30,
					"output_tokens": 10,
					"total_tokens":  40,
				},
			},
		}),
		row("2026-06-03T10:00:03.200Z", "event_msg", map[string]any{
			"type":    "agent_message",
			"message": "I have finished reading the skill.",
		}),
		row("2026-06-03T10:00:03.300Z", "event_msg", map[string]any{
			"type": "task_complete",
		}),
	))
	if err := os.WriteFile(rollout, body, 0o644); err != nil {
		t.Fatal(err)
	}

	assertGoldenMatch(t, "skill.golden.json", rollout)
}

func TestCollectorAndMetricsMatchLegacyGoldenForUsageFixture(t *testing.T) {
	home := t.TempDir()
	systemSkillDir := filepath.Join(home, ".codex", "skills", ".system", "plugin-creator")
	if err := os.MkdirAll(systemSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	systemSkillFile := filepath.Join(systemSkillDir, "SKILL.md")
	if err := os.WriteFile(systemSkillFile, []byte(`---
name: plugin-creator
description: Create and scaffold plugin directories for Codex.
version: 2.1.0
---

Create and scaffold plugin directories for Codex.
`), 0o644); err != nil {
		t.Fatal(err)
	}

	rollout := filepath.Join(home, "rollout-basic-main.jsonl")
	body := []byte(joinJSONLines(
		row("2026-06-03T10:00:00.000Z", "session_meta", map[string]any{
			"id":             "sess-basic",
			"cli_version":    "0.123.0",
			"model_provider": "openai",
			"timestamp":      "2026-06-03T09:59:58.000Z",
			"source":         "cli",
			"base_instructions": map[string]any{
				"text": "You are a file assistant.",
			},
		}),
		row("2026-06-03T10:00:01.000Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-1",
		}),
		row("2026-06-03T10:00:01.100Z", "turn_context", map[string]any{
			"model":             "gpt-5.4",
			"response_format":   map[string]any{"type": "json_object"},
			"n":                 2,
			"seed":              7,
			"temperature":       0.2,
			"top_p":             0.9,
			"max_output_tokens": 512,
			"presence_penalty":  0.3,
			"frequency_penalty": 0.4,
			"stop_sequences":    []any{"DONE"},
			"tools": []any{
				map[string]any{
					"type":        "function",
					"name":        "exec_command",
					"description": "Run a shell command",
					"parameters": map[string]any{
						"type": "object",
						"properties": map[string]any{
							"command": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
						},
					},
				},
			},
			"collaboration_mode": map[string]any{
				"settings": map[string]any{
					"developer_instructions": "Keep answers concise.",
				},
			},
		}),
		row("2026-06-03T10:00:01.200Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "List the files in the repo",
		}),
		row("2026-06-03T10:00:02.000Z", "response_item", map[string]any{
			"type":    "reasoning",
			"summary": []any{map[string]any{"text": "I'll list files with ls."}},
		}),
		row("2026-06-03T10:00:02.100Z", "response_item", map[string]any{
			"type":    "function_call",
			"name":    "exec_command",
			"call_id": "call-1",
			"arguments": mustJSON(map[string]any{
				"command": []string{"sed", "-n", "1,120p", systemSkillFile},
			}),
		}),
		row("2026-06-03T10:00:02.600Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"total_token_usage": map[string]any{
					"input_tokens":            100,
					"output_tokens":           20,
					"total_tokens":            120,
					"cached_input_tokens":     0,
					"reasoning_output_tokens": 5,
				},
				"last_token_usage": map[string]any{
					"input_tokens":            100,
					"output_tokens":           20,
					"total_tokens":            120,
					"cached_input_tokens":     0,
					"reasoning_output_tokens": 5,
				},
			},
		}),
		row("2026-06-03T10:00:03.100Z", "event_msg", map[string]any{
			"type":    "exec_command_end",
			"call_id": "call-1",
			"status":  "completed",
			"stdout":  "file1.txt\nfile2.txt",
		}),
		row("2026-06-03T10:00:04.000Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "There are two files: file1.txt and file2.txt."},
			},
		}),
		row("2026-06-03T10:00:04.200Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"total_token_usage": map[string]any{
					"input_tokens":            250,
					"output_tokens":           50,
					"total_tokens":            300,
					"cached_input_tokens":     50,
					"reasoning_output_tokens": 5,
				},
				"last_token_usage": map[string]any{
					"input_tokens":            150,
					"output_tokens":           30,
					"total_tokens":            180,
					"cached_input_tokens":     50,
					"reasoning_output_tokens": 0,
				},
			},
		}),
		row("2026-06-03T10:00:04.300Z", "event_msg", map[string]any{
			"type":    "agent_message",
			"message": "There are two files: file1.txt and file2.txt.",
		}),
		row("2026-06-03T10:00:04.400Z", "event_msg", map[string]any{
			"type":    "task_complete",
			"turn_id": "turn-1",
		}),
	))
	if err := os.WriteFile(rollout, body, 0o644); err != nil {
		t.Fatal(err)
	}

	assertGoldenMatch(t, "usage.golden.json", rollout)
}

func TestCollectorAndMetricsMatchLegacyGoldenForSubagentFixture(t *testing.T) {
	base := t.TempDir()
	parentDir := filepath.Join(base, "sessions", "2026", "07")
	childDir := filepath.Join(base, "sessions", "subagents")
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(childDir, 0o755); err != nil {
		t.Fatal(err)
	}

	parentRollout := filepath.Join(parentDir, "rollout-parent.jsonl")
	childRollout := filepath.Join(childDir, "rollout-child-thread-123.jsonl")

	parentBody := []byte(joinJSONLines(
		row("2026-07-24T08:00:00.000Z", "session_meta", map[string]any{
			"id":             "sess-parent",
			"cli_version":    "0.145.0",
			"model_provider": "openai",
			"timestamp":      "2026-07-24T07:59:58.000Z",
			"source":         "cli",
		}),
		row("2026-07-24T08:00:01.000Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-parent",
		}),
		row("2026-07-24T08:00:01.100Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "delegate this task",
		}),
		row("2026-07-24T08:00:02.000Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "spawning subagent"},
			},
		}),
		row("2026-07-24T08:00:02.100Z", "event_msg", map[string]any{
			"type":          "collab_agent_spawn_end",
			"new_thread_id": "thread-123",
		}),
		row("2026-07-24T08:00:02.200Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{"input_tokens": 10, "output_tokens": 4},
			},
		}),
		row("2026-07-24T08:00:02.300Z", "event_msg", map[string]any{
			"type":    "agent_message",
			"message": "parent complete",
		}),
		row("2026-07-24T08:00:02.400Z", "event_msg", map[string]any{
			"type":               "task_complete",
			"last_agent_message": "parent complete",
		}),
	))
	if err := os.WriteFile(parentRollout, parentBody, 0o644); err != nil {
		t.Fatal(err)
	}

	childBody := []byte(joinJSONLines(
		row("2026-07-24T08:00:03.000Z", "session_meta", map[string]any{
			"id":             "sess-child",
			"cli_version":    "0.145.0",
			"model_provider": "openai",
			"timestamp":      "2026-07-24T08:00:02.900Z",
			"source":         "cli",
		}),
		row("2026-07-24T08:00:03.100Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-child",
		}),
		row("2026-07-24T08:00:03.200Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "child work",
		}),
		row("2026-07-24T08:00:04.000Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "child result"},
			},
		}),
		row("2026-07-24T08:00:04.100Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{"input_tokens": 8, "output_tokens": 3},
			},
		}),
		row("2026-07-24T08:00:04.200Z", "event_msg", map[string]any{
			"type":               "task_complete",
			"last_agent_message": "child result",
		}),
	))
	if err := os.WriteFile(childRollout, childBody, 0o644); err != nil {
		t.Fatal(err)
	}

	assertGoldenMatch(t, "subagent.golden.json", parentRollout)
}

func assertGoldenMatch(t *testing.T, goldenFile, rollout string) {
	t.Helper()

	goResult, err := CollectRollout(rollout, config.Config{MaxChars: 4096, ResourceAttributes: map[string]any{}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	goGolden := goldenOutput{
		Spans:   normalizeGoSpans(goResult.Spans),
		Metrics: normalizeGoMetrics(metrics.Build(goResult.Spans)),
	}
	goGolden = normalizeGoldenPaths(goGolden, filepath.Dir(rollout))

	if shouldUpdateGolden() {
		if err := writeGoldenFixture(goldenFile, goGolden); err != nil {
			t.Fatal(err)
		}
		return
	}

	expected, err := loadGoldenFixture(goldenFile)
	if err != nil {
		t.Fatal(err)
	}

	if diff := diffJSON(expected.Spans, goGolden.Spans); diff != "" {
		t.Fatalf("span golden mismatch\n%s", diff)
	}
	if diff := diffJSON(expected.Metrics, goGolden.Metrics); diff != "" {
		t.Fatalf("metric golden mismatch\n%s", diff)
	}
}

func shouldUpdateGolden() bool {
	return os.Getenv("UPDATE_GOLDEN") == "1"
}

func loadGoldenFixture(name string) (goldenOutput, error) {
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		return goldenOutput{}, err
	}
	var result goldenOutput
	if err := json.Unmarshal(data, &result); err != nil {
		return goldenOutput{}, err
	}
	return result, nil
}

func writeGoldenFixture(name string, fixture goldenOutput) error {
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(fixture, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(filepath.Join("testdata", name), data, 0o644)
}

func normalizeGoldenPaths(fixture goldenOutput, roots ...string) goldenOutput {
	fixture.Spans = normalizeFixtureItems(fixture.Spans, roots...)
	fixture.Metrics = normalizeFixtureItems(fixture.Metrics, roots...)
	return fixture
}

func normalizeFixtureItems[T any](items []T, roots ...string) []T {
	normalized := make([]T, len(items))
	for index, item := range items {
		data, err := json.Marshal(item)
		if err != nil {
			normalized[index] = item
			continue
		}
		var generic any
		if err := json.Unmarshal(data, &generic); err != nil {
			normalized[index] = item
			continue
		}
		generic = normalizeFixtureValue(generic, roots...)
		data, err = json.Marshal(generic)
		if err != nil {
			normalized[index] = item
			continue
		}
		if err := json.Unmarshal(data, &normalized[index]); err != nil {
			normalized[index] = item
			continue
		}
	}
	return normalized
}

func normalizeFixtureValue(value any, roots ...string) any {
	switch current := value.(type) {
	case map[string]any:
		for key, item := range current {
			current[key] = normalizeFixtureValue(item, roots...)
		}
		return current
	case []any:
		for index, item := range current {
			current[index] = normalizeFixtureValue(item, roots...)
		}
		return current
	case string:
		return normalizeFixtureString(current, roots...)
	default:
		return value
	}
}

func normalizeFixtureString(value string, roots ...string) string {
	normalized := filepath.ToSlash(value)
	for _, root := range roots {
		root = filepath.ToSlash(root)
		if root == "" || root == "." {
			continue
		}
		normalized = strings.ReplaceAll(normalized, root, "<TMPDIR>")
	}
	return normalized
}

func normalizeGoSpans(spans []model.Span) []normalizedSpan {
	keyByID := map[string]string{}
	for _, span := range spans {
		keyByID[span.SpanID] = goSpanKey(span)
	}
	out := make([]normalizedSpan, 0, len(spans))
	for _, span := range spans {
		item := normalizedSpan{
			Key:        goSpanKey(span),
			Name:       span.Name,
			ParentKey:  keyByID[span.ParentID],
			DurationMs: span.DurationMs,
			StatusCode: span.Status.Code,
			Attributes: pickMap(span.Attributes, []string{
				"gen_ai.conversation.id",
				"session_id",
				"gen_ai.operation.name",
				"gen_ai.provider.name",
				"gen_ai.request.model",
				"gen_ai.response.model",
				"gen_ai.output.type",
				"gen_ai.request.choice.count",
				"gen_ai.request.seed",
				"gen_ai.request.temperature",
				"gen_ai.request.top_p",
				"gen_ai.request.max_tokens",
				"gen_ai.request.presence_penalty",
				"gen_ai.request.frequency_penalty",
				"gen_ai.request.stop_sequences",
				"gen_ai.response.finish_reasons",
				"gen_ai.system_instructions",
				"gen_ai.tool.definitions",
				"gen_ai.usage.input_tokens",
				"gen_ai.usage.output_tokens",
				"gen_ai.usage.cache_read.input_tokens",
				"gen_ai.usage.reasoning.output_tokens",
				"final_status",
				"ttft",
				"tool_count",
				"tool_command",
				"gen_ai.tool.name",
				"gen_ai.tool.call.id",
				"skill.name",
				"gen_ai.skill.name",
				"skill.source.type",
				"gen_ai.skill.source.type",
				"skill.result_status",
				"gen_ai.skill.result.status",
				"gen_ai.skill.version",
				"assistant_message_start_time",
				"assistant_message_event_time",
			}),
		}
		if trigger := asString(span.Attributes["triggered_by.llm_span_id"]); trigger != "" {
			item.Attributes["triggered_by.key"] = keyByID[trigger]
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		return stableJSON(out[i]) < stableJSON(out[j])
	})
	return out
}

func normalizeGoMetrics(items []model.Metric) []normalizedMetric {
	out := make([]normalizedMetric, 0, len(items))
	for _, item := range items {
		out = append(out, normalizedMetric{
			Name:  item.Name,
			Unit:  item.Unit,
			Value: item.Value,
			Attributes: pickMap(item.Attributes, []string{
				"agent_runtime",
				"gen_ai.conversation.id",
				"session_id",
				"final_status",
				"operation_name",
				"gen_ai.operation.name",
				"status",
				"provider_name",
				"gen_ai.provider.name",
				"request_model",
				"gen_ai.request.model",
				"response_model",
				"gen_ai.response.model",
				"gen_ai.output.type",
				"gen_ai.token.type",
				"tool_name",
				"gen_ai.tool.name",
				"skill_name",
				"skill.name",
				"gen_ai.skill.name",
				"skill_source",
				"skill.source.type",
				"gen_ai.skill.source.type",
				"skill.result_status",
				"gen_ai.skill.result.status",
				"gen_ai.skill.version",
				"tool_result_status",
			}),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return stableJSON(out[i]) < stableJSON(out[j])
	})
	return out
}

func goSpanKey(span model.Span) string {
	parts := []string{span.Name}
	if value, ok := span.Attributes["step_index"]; ok {
		parts = append(parts, "step="+toStableText(value))
	}
	if value, ok := span.Attributes["message_index"]; ok {
		parts = append(parts, "message="+toStableText(value))
	}
	if value, ok := span.Attributes["gen_ai.tool.call.id"]; ok {
		parts = append(parts, "call="+toStableText(value))
	} else if value, ok := span.Attributes["skill_call_id"]; ok {
		parts = append(parts, "call="+toStableText(value))
	} else if value, ok := span.Attributes["run_id"]; ok {
		parts = append(parts, "run="+toStableText(value))
	}
	return joinKey(parts)
}

func pickMap(source map[string]any, keys []string) map[string]any {
	out := map[string]any{}
	for _, key := range keys {
		if value, ok := source[key]; ok {
			out[key] = value
		}
	}
	return out
}

func diffJSON(left, right any) string {
	leftJSON := stableJSON(left)
	rightJSON := stableJSON(right)
	if leftJSON == rightJSON {
		return ""
	}
	return "expected:\n" + leftJSON + "\nactual:\n" + rightJSON
}

func stableJSON(value any) string {
	data, _ := json.Marshal(value)
	var normalized any
	_ = json.Unmarshal(data, &normalized)
	buffer := &bytes.Buffer{}
	writeStableJSON(buffer, normalized)
	return buffer.String()
}

func writeStableJSON(buffer *bytes.Buffer, value any) {
	switch current := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(current))
		for key := range current {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		buffer.WriteByte('{')
		for index, key := range keys {
			if index > 0 {
				buffer.WriteByte(',')
			}
			keyJSON, _ := json.Marshal(key)
			buffer.Write(keyJSON)
			buffer.WriteByte(':')
			writeStableJSON(buffer, current[key])
		}
		buffer.WriteByte('}')
	case []any:
		buffer.WriteByte('[')
		for index, item := range current {
			if index > 0 {
				buffer.WriteByte(',')
			}
			writeStableJSON(buffer, item)
		}
		buffer.WriteByte(']')
	default:
		encoded, _ := json.Marshal(current)
		buffer.Write(encoded)
	}
}

func joinKey(parts []string) string {
	result := ""
	for index, part := range parts {
		if index > 0 {
			result += "|"
		}
		result += part
	}
	return result
}

func toStableText(value any) string {
	data, _ := json.Marshal(value)
	if string(data) == "null" {
		return ""
	}
	var plain any
	if err := json.Unmarshal(data, &plain); err == nil {
		switch current := plain.(type) {
		case string:
			return current
		default:
			return stableJSON(current)
		}
	}
	return ""
}
