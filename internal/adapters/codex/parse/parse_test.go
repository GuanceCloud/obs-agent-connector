package parse

import (
	"testing"
	"time"
)

func TestParseSessionBuildsAssistantAndUsage(t *testing.T) {
	lines := []map[string]any{
		{
			"type":      "session_meta",
			"timestamp": "2026-07-24T08:00:00Z",
			"payload": map[string]any{
				"id":             "session-1",
				"cli_version":    "0.145.0",
				"model_provider": "openai",
			},
		},
		{
			"type":      "event_msg",
			"timestamp": "2026-07-24T08:00:01Z",
			"payload": map[string]any{
				"type":    "task_started",
				"turn_id": "turn-1",
			},
		},
		{
			"type":      "event_msg",
			"timestamp": "2026-07-24T08:00:02Z",
			"payload": map[string]any{
				"type":    "user_message",
				"message": "hello",
			},
		},
		{
			"type":      "response_item",
			"timestamp": "2026-07-24T08:00:03Z",
			"payload": map[string]any{
				"type": "message",
				"role": "assistant",
				"content": []any{
					map[string]any{"type": "output_text", "text": "world"},
				},
			},
		},
		{
			"type":      "event_msg",
			"timestamp": "2026-07-24T08:00:04Z",
			"payload": map[string]any{
				"type": "token_count",
				"info": map[string]any{
					"last_token_usage": map[string]any{
						"input_tokens":  float64(11),
						"output_tokens": float64(7),
					},
				},
			},
		},
		{
			"type":      "event_msg",
			"timestamp": "2026-07-24T08:00:05Z",
			"payload": map[string]any{
				"type":               "task_complete",
				"last_agent_message": "world",
			},
		},
	}

	parsed := ParseSession(lines)
	if parsed.SessionMeta.SessionID != "session-1" {
		t.Fatalf("unexpected session id: %#v", parsed.SessionMeta)
	}
	if len(parsed.Turns) != 1 {
		t.Fatalf("expected one turn, got %d", len(parsed.Turns))
	}
	turn := parsed.Turns[0]
	if turn.TurnID != "turn-1" || !turn.Completed || turn.Aborted {
		t.Fatalf("unexpected turn state: %#v", turn)
	}
	if turn.UserInput != "hello" {
		t.Fatalf("unexpected user input: %q", turn.UserInput)
	}
	if turn.FinalOutput != "world" {
		t.Fatalf("unexpected final output: %q", turn.FinalOutput)
	}
	if len(turn.Steps) != 1 {
		t.Fatalf("expected one step, got %d", len(turn.Steps))
	}
	step := turn.Steps[0]
	if step.Text != "world" {
		t.Fatalf("unexpected step text: %q", step.Text)
	}
	if step.Usage["input_tokens"] != float64(11) || step.Usage["output_tokens"] != float64(7) {
		t.Fatalf("unexpected usage: %#v", step.Usage)
	}
}

func TestParseSessionDeduplicatesToolCalls(t *testing.T) {
	lines := []map[string]any{
		{
			"type":      "event_msg",
			"timestamp": "2026-07-24T08:10:00Z",
			"payload": map[string]any{
				"type":    "task_started",
				"turn_id": "turn-2",
			},
		},
		{
			"type":      "response_item",
			"timestamp": "2026-07-24T08:10:01Z",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "call-1",
				"name":      "exec_command",
				"arguments": `{"cmd":"date"}`,
			},
		},
		{
			"type":      "response_item",
			"timestamp": "2026-07-24T08:10:02Z",
			"payload": map[string]any{
				"type":      "function_call",
				"call_id":   "call-1",
				"name":      "exec_command",
				"arguments": `{"cmd":"date"}`,
			},
		},
		{
			"type":      "event_msg",
			"timestamp": "2026-07-24T08:10:03Z",
			"payload": map[string]any{
				"type":              "exec_command_end",
				"call_id":           "call-1",
				"aggregated_output": "Thu Jul 24",
			},
		},
		{
			"type":      "event_msg",
			"timestamp": "2026-07-24T08:10:04Z",
			"payload": map[string]any{
				"type": "turn_aborted",
			},
		},
	}

	parsed := ParseSession(lines)
	if len(parsed.Turns) != 1 {
		t.Fatalf("expected one turn, got %d", len(parsed.Turns))
	}
	if len(parsed.Turns[0].Steps) != 1 {
		t.Fatalf("expected one step, got %d", len(parsed.Turns[0].Steps))
	}
	toolCalls := parsed.Turns[0].Steps[0].ToolCalls
	if len(toolCalls) != 1 {
		t.Fatalf("expected one deduplicated tool call, got %d", len(toolCalls))
	}
	if toolCalls[0].Output != "Thu Jul 24" {
		t.Fatalf("unexpected tool output: %#v", toolCalls[0].Output)
	}
}

func TestParseSessionInfersCompletedWhenStopPrecedesTaskComplete(t *testing.T) {
	parsed := ParseSession([]map[string]any{
		testLine("2026-06-03T10:00:00.000Z", "session_meta", map[string]any{
			"id":             "sess-stop-before-complete",
			"cli_version":    "0.139.0",
			"model_provider": "openai",
		}),
		testLine("2026-06-03T10:00:01.000Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-stop-before-complete",
		}),
		testLine("2026-06-03T10:00:01.100Z", "turn_context", map[string]any{
			"model": "gpt-5.5",
		}),
		testLine("2026-06-03T10:00:02.000Z", "event_msg", map[string]any{
			"type":    "user_message",
			"message": "hello",
		}),
		testLine("2026-06-03T10:00:03.000Z", "event_msg", map[string]any{
			"type":    "agent_message",
			"message": "done",
		}),
	})

	if len(parsed.Turns) != 1 {
		t.Fatalf("expected one turn, got %d", len(parsed.Turns))
	}
	turn := parsed.Turns[0]
	if !turn.Completed || turn.FinalOutput != "done" {
		t.Fatalf("unexpected inferred turn: %#v", turn)
	}
	message := turn.Steps[0].AssistantMessages[0]
	want := testTimeMillis(t, "2026-06-03T10:00:03.000Z")
	if message.StartTime != want || message.EventTime != want || !message.HasEventTime {
		t.Fatalf("unexpected inferred assistant timing: %#v", message)
	}
}

func TestParseSessionExtendsAssistantToStepEndWithoutAgentMessage(t *testing.T) {
	parsed := ParseSession([]map[string]any{
		testLine("2026-06-03T10:00:01.000Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-assistant-time",
		}),
		testLine("2026-06-03T10:00:02.000Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "partial answer"},
			},
		}),
		testLine("2026-06-03T10:00:02.400Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens":  10,
					"output_tokens": 3,
				},
			},
		}),
	})

	message := parsed.Turns[0].Steps[0].AssistantMessages[0]
	if message.StartTime != testTimeMillis(t, "2026-06-03T10:00:02.000Z") ||
		message.EndTime != testTimeMillis(t, "2026-06-03T10:00:02.400Z") {
		t.Fatalf("unexpected assistant timing: %#v", message)
	}
}

func TestParseSessionUsesAgentMessageWithoutDuplicateAssistant(t *testing.T) {
	parsed := ParseSession([]map[string]any{
		testLine("2026-06-03T10:00:01.000Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-assistant-dedupe",
		}),
		testLine("2026-06-03T10:00:02.000Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "partial answer"},
			},
		}),
		testLine("2026-06-03T10:00:02.250Z", "event_msg", map[string]any{
			"type":    "agent_message",
			"message": "final answer",
		}),
		testLine("2026-06-03T10:00:02.400Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens":  10,
					"output_tokens": 3,
				},
			},
		}),
	})

	step := parsed.Turns[0].Steps[0]
	if len(step.AssistantMessages) != 1 {
		t.Fatalf("expected one assistant message, got %d", len(step.AssistantMessages))
	}
	message := step.AssistantMessages[0]
	if message.Text != "final answer" ||
		message.StartTime != testTimeMillis(t, "2026-06-03T10:00:02.000Z") ||
		message.EventTime != testTimeMillis(t, "2026-06-03T10:00:02.250Z") ||
		message.EndTime != testTimeMillis(t, "2026-06-03T10:00:02.250Z") {
		t.Fatalf("unexpected assistant message: %#v", message)
	}
}

func TestParseSessionCollapsesAssistantResponseItems(t *testing.T) {
	parsed := ParseSession([]map[string]any{
		testLine("2026-06-03T10:00:01.000Z", "event_msg", map[string]any{
			"type":    "task_started",
			"turn_id": "turn-assistant-collapse",
		}),
		testLine("2026-06-03T10:00:02.000Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "first part"},
			},
		}),
		testLine("2026-06-03T10:00:02.100Z", "response_item", map[string]any{
			"type": "message",
			"role": "assistant",
			"content": []any{
				map[string]any{"type": "output_text", "text": "second part"},
			},
		}),
		testLine("2026-06-03T10:00:02.300Z", "event_msg", map[string]any{
			"type":    "agent_message",
			"message": "final combined answer",
		}),
		testLine("2026-06-03T10:00:02.500Z", "event_msg", map[string]any{
			"type": "token_count",
			"info": map[string]any{
				"last_token_usage": map[string]any{
					"input_tokens":  10,
					"output_tokens": 3,
				},
			},
		}),
	})

	message := parsed.Turns[0].Steps[0].AssistantMessages[0]
	if message.Text != "final combined answer" || message.ResponseItemCount != 2 {
		t.Fatalf("unexpected collapsed assistant message: %#v", message)
	}
	if message.StartTime != testTimeMillis(t, "2026-06-03T10:00:02.000Z") ||
		message.EventTime != testTimeMillis(t, "2026-06-03T10:00:02.300Z") ||
		message.EndTime != testTimeMillis(t, "2026-06-03T10:00:02.300Z") {
		t.Fatalf("unexpected collapsed assistant timing: %#v", message)
	}
}

func testLine(timestamp, lineType string, payload map[string]any) map[string]any {
	return map[string]any{
		"timestamp": timestamp,
		"type":      lineType,
		"payload":   payload,
	}
}

func testTimeMillis(t *testing.T, value string) int64 {
	t.Helper()
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed.UnixMilli()
}
