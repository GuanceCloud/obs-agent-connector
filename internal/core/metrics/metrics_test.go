package metrics

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/collector"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

func TestBuildMetricsFromCollectedSpans(t *testing.T) {
	home := t.TempDir()
	userSkillDir := filepath.Join(home, ".codex", "skills", "dashboard")
	if err := os.MkdirAll(userSkillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	userSkillFile := filepath.Join(userSkillDir, "SKILL.md")
	if err := os.WriteFile(userSkillFile, []byte(`---
name: dashboard
description: Generate an observability dashboard.
version: 1.4.0
---

Generate an observability dashboard.
`), 0o644); err != nil {
		t.Fatal(err)
	}
	rollout := filepath.Join(home, "rollout.jsonl")
	body := []byte(joinJSONLines(
		row("2026-06-03T10:00:00.000Z", "session_meta", map[string]any{
			"id":             "sess-skill-order",
			"cli_version":    "0.140.0",
			"model_provider": "openai",
		}),
		row("2026-06-03T10:00:01.000Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-skill-order",
		}),
		row("2026-06-03T10:00:01.100Z", "turn_context", map[string]any{
			"model": "gpt-5.4",
		}),
		row("2026-06-03T10:00:01.200Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "Build a dashboard",
		}),
		row("2026-06-03T10:00:02.000Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "I will read the dashboard skill instructions first."},
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
				map[string]any{"type": "output_text", "text": "I finished reading the skill instructions."},
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
			"message": "I finished reading the skill instructions.",
		}),
		row("2026-06-03T10:00:03.300Z", "event_msg", map[string]any{
			"type": "task_complete",
		}),
	))
	if err := os.WriteFile(rollout, body, 0o644); err != nil {
		t.Fatal(err)
	}

	collected, err := collector.CollectRollout(rollout, config.Config{MaxChars: 4096}, nil)
	if err != nil {
		t.Fatal(err)
	}
	metrics := Build(collected.Spans)

	if findMetric(metrics, "gen_ai.workflow.duration", nil) == nil {
		t.Fatal("missing workflow duration metric")
	}
	chatCount := findMetric(metrics, "gen_ai.agent.operation.count", map[string]any{"gen_ai.operation.name": "chat"})
	toolCount := findMetric(metrics, "gen_ai.agent.operation.count", map[string]any{"gen_ai.operation.name": "execute_tool"})
	skillCount := findMetric(metrics, "gen_ai.agent.operation.count", map[string]any{"gen_ai.operation.name": "skill"})
	if chatCount == nil || toolCount == nil || skillCount == nil {
		t.Fatalf("missing operation count metrics: chat=%v tool=%v skill=%v", chatCount != nil, toolCount != nil, skillCount != nil)
	}
	if chatCount.Attributes["gen_ai.request.model"] != "gpt-5.4" {
		t.Fatalf("unexpected chat count attrs: %#v", chatCount.Attributes)
	}
	if toolCount.Attributes["gen_ai.tool.name"] != "exec_command" {
		t.Fatalf("unexpected tool count attrs: %#v", toolCount.Attributes)
	}
	if skillCount.Attributes["gen_ai.skill.name"] != "dashboard" {
		t.Fatalf("unexpected skill count attrs: %#v", skillCount.Attributes)
	}

	inputTokens := findMetric(metrics, "gen_ai.client.token.usage", map[string]any{"gen_ai.token.type": "input"})
	outputTokens := findMetric(metrics, "gen_ai.client.token.usage", map[string]any{"gen_ai.token.type": "output"})
	if inputTokens == nil || outputTokens == nil {
		t.Fatalf("missing token metrics: input=%v output=%v", inputTokens != nil, outputTokens != nil)
	}
	if inputTokens.Value != 20 || outputTokens.Value != 6 {
		t.Fatalf("unexpected token values: input=%v output=%v", inputTokens.Value, outputTokens.Value)
	}

	toolDuration := findMetric(metrics, "gen_ai.agent.operation.duration", map[string]any{"gen_ai.operation.name": "execute_tool"})
	skillDuration := findMetric(metrics, "gen_ai.agent.operation.duration", map[string]any{"gen_ai.operation.name": "skill"})
	if toolDuration == nil || skillDuration == nil {
		t.Fatalf("missing duration metrics: tool=%v skill=%v", toolDuration != nil, skillDuration != nil)
	}
	if toolDuration.Attributes["skill_name"] != "dashboard" || skillDuration.Attributes["skill_name"] != "dashboard" {
		t.Fatalf("unexpected skill attrs on duration metrics: tool=%#v skill=%#v", toolDuration.Attributes, skillDuration.Attributes)
	}
}

func TestBuildEmitsOneCountPointPerOperationSpan(t *testing.T) {
	spans := []model.Span{
		{
			Name:              "llm",
			DurationMs:        100,
			StartTimeUnixNano: "1",
			EndTimeUnixNano:   "2",
			Attributes: map[string]any{
				"gen_ai.operation.name":  "chat",
				"gen_ai.conversation.id": "sess-1",
				"session_id":             "sess-1",
				"gen_ai.request.model":   "gpt-5.4",
				"gen_ai.response.model":  "gpt-5.4",
			},
			Resource: map[string]any{"agent_runtime": "codex"},
		},
		{
			Name:              "tool:exec_command",
			DurationMs:        200,
			StartTimeUnixNano: "1",
			EndTimeUnixNano:   "2",
			Attributes: map[string]any{
				"gen_ai.operation.name":  "execute_tool",
				"gen_ai.conversation.id": "sess-1",
				"session_id":             "sess-1",
				"gen_ai.tool.name":       "exec_command",
			},
			Resource: map[string]any{"agent_runtime": "codex"},
		},
		{
			Name:              "skill:dashboard",
			DurationMs:        300,
			StartTimeUnixNano: "1",
			EndTimeUnixNano:   "2",
			Attributes: map[string]any{
				"gen_ai.operation.name":  "skill",
				"gen_ai.conversation.id": "sess-1",
				"session_id":             "sess-1",
				"gen_ai.skill.name":      "dashboard",
			},
			Resource: map[string]any{"agent_runtime": "codex"},
		},
	}

	metrics := Build(spans)
	count := 0
	for _, metric := range metrics {
		if metric.Name == "gen_ai.agent.operation.count" {
			count++
			if metric.Value != 1 {
				t.Fatalf("expected count metric value 1, got %v", metric.Value)
			}
		}
	}
	if count != 3 {
		t.Fatalf("expected 3 count metrics, got %d", count)
	}
}

func TestBuildNormalizesStatusAndKeepsToolResultOutOfMetrics(t *testing.T) {
	spans := []model.Span{
		{
			Name:              "invoke_agent",
			DurationMs:        1000,
			StartTimeUnixNano: "1",
			EndTimeUnixNano:   "2",
			Attributes: map[string]any{
				"gen_ai.conversation.id": "sess-1",
				"session_id":             "sess-1",
				"final_status":           "completed",
				"status":                 "ok",
			},
		},
		{
			Name:              "tool:Bash",
			DurationMs:        100,
			StartTimeUnixNano: "1",
			EndTimeUnixNano:   "2",
			Attributes: map[string]any{
				"gen_ai.operation.name":  "execute_tool",
				"gen_ai.conversation.id": "sess-1",
				"session_id":             "sess-1",
				"gen_ai.tool.name":       "Bash",
				"tool_result_status":     "completed",
				"status":                 "ok",
			},
		},
	}
	built := Build(spans)
	workflow := findMetric(built, "gen_ai.workflow.duration", nil)
	toolDuration := findMetric(built, "gen_ai.agent.operation.duration", map[string]any{
		"gen_ai.operation.name": "execute_tool",
	})
	if workflow == nil || workflow.Attributes["status"] != "completed" {
		t.Fatalf("unexpected workflow status: %#v", workflow)
	}
	if toolDuration == nil || toolDuration.Attributes["status"] != "ok" {
		t.Fatalf("unexpected operation status: %#v", toolDuration)
	}
	if _, exists := toolDuration.Attributes["tool_result_status"]; exists {
		t.Fatalf("tool_result_status must remain trace-only: %#v", toolDuration.Attributes)
	}
}

func findMetric(metrics []model.Metric, name string, attrs map[string]any) *model.Metric {
	for i := range metrics {
		if metrics[i].Name != name {
			continue
		}
		if attrs == nil {
			return &metrics[i]
		}
		matched := true
		for key, value := range attrs {
			if metrics[i].Attributes[key] != value {
				matched = false
				break
			}
		}
		if matched {
			return &metrics[i]
		}
	}
	return nil
}

func row(ts, typ string, payload map[string]any) map[string]any {
	return map[string]any{
		"timestamp": ts,
		"type":      typ,
		"payload":   payload,
	}
}

func joinJSONLines(rows ...map[string]any) string {
	lines := make([]string, 0, len(rows))
	for _, entry := range rows {
		lines = append(lines, mustJSON(entry))
	}
	return strings.Join(lines, "\n") + "\n"
}

func mustJSON(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(data)
}

func stringsRepeat(s string, count int) string {
	return strings.Repeat(s, count)
}
