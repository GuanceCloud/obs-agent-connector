package parse

import (
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/util"
)

func LoadRollout(file string) ([]map[string]any, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	lines := make([]map[string]any, 0)
	for _, raw := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			continue
		}
		var line map[string]any
		if err := json.Unmarshal([]byte(trimmed), &line); err == nil {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func ParseSession(lines []map[string]any) model.ParsedSession {
	sessionMeta := model.SessionMeta{SessionID: "unknown"}
	turns := make([]*model.Turn, 0)
	var turn *model.Turn
	var step *model.Step
	toolCallsByID := map[string]*model.ToolCall{}
	lastTimestamp := time.Now().UnixMilli()

	newStep := func(startTime int64) *model.Step {
		return &model.Step{
			StartTime:         startTime,
			EndTime:           startTime,
			ToolCalls:         make([]*model.ToolCall, 0),
			AssistantMessages: make([]*model.AssistantMessage, 0),
		}
	}

	ensureTurn := func(ts int64) *model.Turn {
		if turn == nil {
			turn = &model.Turn{
				StartTime:         ts,
				EndTime:           ts,
				Steps:             make([]*model.Step, 0),
				SubagentThreadIDs: make([]string, 0),
				InvocationParams:  map[string]any{},
			}
		}
		return turn
	}

	ensureStep := func(ts int64) *model.Step {
		if step == nil {
			step = newStep(ts)
		}
		return step
	}

	finalizeAssistantMessageTimes := func(targetStep *model.Step, fallbackEndTime int64) {
		for _, message := range targetStep.AssistantMessages {
			if message.EndTime > message.StartTime {
				continue
			}
			endTime := fallbackEndTime
			if message.HasEventTime {
				endTime = message.EventTime
			}
			if endTime < message.StartTime {
				endTime = message.StartTime
			}
			message.EndTime = endTime
		}
	}

	refreshStepText := func(targetStep *model.Step) {
		parts := make([]string, 0, len(targetStep.AssistantMessages))
		for _, message := range targetStep.AssistantMessages {
			if strings.TrimSpace(message.Text) != "" {
				parts = append(parts, message.Text)
			}
		}
		targetStep.Text = strings.Join(parts, "\n")
	}

	recordAssistantMessage := func(targetStep *model.Step, text string, ts int64, eventTime *int64) {
		if len(targetStep.AssistantMessages) > 0 {
			message := targetStep.AssistantMessages[0]
			if message.Text != "" {
				message.Text = message.Text + "\n" + text
			} else {
				message.Text = text
			}
			endTime := ts
			if eventTime != nil {
				endTime = *eventTime
				message.HasEventTime = true
				message.EventTime = *eventTime
			}
			if endTime > message.EndTime {
				message.EndTime = endTime
			}
			message.ResponseItemCount++
		} else {
			message := &model.AssistantMessage{
				Text:              text,
				StartTime:         ts,
				EndTime:           ts,
				ResponseItemCount: 1,
			}
			if eventTime != nil {
				message.HasEventTime = true
				message.EventTime = *eventTime
				if *eventTime > message.EndTime {
					message.EndTime = *eventTime
				}
			}
			targetStep.AssistantMessages = append(targetStep.AssistantMessages, message)
		}
		refreshStepText(targetStep)
		if eventTime != nil && *eventTime > targetStep.EndTime {
			targetStep.EndTime = *eventTime
		} else if ts > targetStep.EndTime {
			targetStep.EndTime = ts
		}
	}

	attachAgentMessage := func(text string, ts int64) {
		var targetStep *model.Step
		if step != nil {
			targetStep = step
		} else if turn != nil && len(turn.Steps) > 0 {
			targetStep = turn.Steps[len(turn.Steps)-1]
		}
		if targetStep != nil && len(targetStep.AssistantMessages) > 0 {
			message := targetStep.AssistantMessages[0]
			message.Text = text
			message.HasEventTime = true
			message.EventTime = ts
			if ts > message.EndTime {
				message.EndTime = ts
			}
			targetStep.AssistantMessages = []*model.AssistantMessage{message}
			refreshStepText(targetStep)
			if ts > targetStep.EndTime {
				targetStep.EndTime = ts
			}
			return
		}
		recordAssistantMessage(ensureStep(ts), text, ts, &ts)
	}

	closeStep := func(ts int64, usage map[string]any) {
		if step == nil {
			return
		}
		if ts > step.EndTime {
			step.EndTime = ts
		}
		finalizeAssistantMessageTimes(step, step.EndTime)
		if usage != nil {
			step.Usage = usage
			step.ModelEndTime = ts
			step.HasModelEndTime = true
		}
		turn.Steps = append(turn.Steps, step)
		step = nil
	}

	recordToolCall := func(ts int64, payload map[string]any, rawArgs any) *model.ToolCall {
		s := ensureStep(ts)
		callID := asString(payload["call_id"])
		if existing, ok := toolCallsByID[callID]; ok && callID != "" {
			if ts < existing.StartTime {
				existing.StartTime = ts
			}
			if existing.Name == "" {
				existing.Name = asString(payload["name"])
			}
			if existing.Args == nil {
				existing.Args = parseArgs(rawArgs)
			}
			return existing
		}
		tc := &model.ToolCall{
			CallID:    callID,
			Name:      asString(payload["name"]),
			Args:      parseArgs(rawArgs),
			StartTime: ts,
		}
		s.ToolCalls = append(s.ToolCalls, tc)
		if callID != "" {
			toolCallsByID[callID] = tc
		}
		return tc
	}

	inferCompleted := func(currentTurn *model.Turn) bool {
		if currentTurn == nil {
			return false
		}
		if strings.TrimSpace(currentTurn.LastAgentMessage) != "" || strings.TrimSpace(currentTurn.FinalOutput) != "" {
			return true
		}
		for _, currentStep := range currentTurn.Steps {
			if strings.TrimSpace(currentStep.Text) != "" {
				return true
			}
		}
		return false
	}

	finishTurn := func(ts int64, completed, aborted bool) {
		if turn == nil {
			return
		}
		closeStep(ts, nil)
		if ts > turn.EndTime {
			turn.EndTime = ts
		}
		turn.Completed = completed
		turn.Aborted = aborted
		if turn.UserInput == "" {
			turn.UserInput = turn.UserInputFallback
		}
		if turn.LastAgentMessage != "" {
			turn.FinalOutput = turn.LastAgentMessage
		} else {
			for i := len(turn.Steps) - 1; i >= 0; i-- {
				if strings.TrimSpace(turn.Steps[i].Text) != "" {
					turn.FinalOutput = turn.Steps[i].Text
					break
				}
			}
		}
		turn.UserInputFallback = ""
		turn.LastAgentMessage = ""
		turns = append(turns, turn)
		turn = nil
		step = nil
		toolCallsByID = map[string]*model.ToolCall{}
	}

	finishCurrentTurn := func(ts int64) {
		finishTurn(ts, inferCompleted(turn), false)
	}

	for _, line := range lines {
		ts := parseLineTimestamp(line, lastTimestamp)
		lastTimestamp = ts
		lineType := asString(line["type"])
		payload := asMap(line["payload"])

		switch lineType {
		case "session_meta":
			sessionMeta = model.SessionMeta{
				SessionID:        firstNonEmptyString(asString(payload["id"]), sessionMeta.SessionID),
				CLIVersion:       asString(payload["cli_version"]),
				ModelProvider:    asString(payload["model_provider"]),
				BaseInstructions: nestedText(payload, "base_instructions", "text"),
				CreatedAt:        normalizeISOTimestamp(asString(payload["timestamp"]), ts),
				Channel: firstNonEmptyString(
					asString(payload["source"]),
					asString(payload["originator"]),
					asString(payload["thread_source"]),
				),
			}
		case "turn_context":
			currentTurn := ensureTurn(ts)
			currentTurn.Model = firstNonEmptyString(asString(payload["model"]), currentTurn.Model)
			currentTurn.InvocationParams = payload
		case "response_item":
			ensureTurn(ts)
			responseType := asString(payload["type"])
			switch responseType {
			case "message":
				text := extractMessageText(payload["content"])
				role := asString(payload["role"])
				if role == "assistant" && text != "" {
					recordAssistantMessage(ensureStep(ts), text, ts, nil)
				} else if role == "user" && text != "" && turn.UserInputFallback == "" && !isSyntheticUserContext(text) {
					turn.UserInputFallback = text
				}
			case "function_call":
				recordToolCall(ts, payload, payload["arguments"])
			case "custom_tool_call":
				recordToolCall(ts, payload, payload["input"])
			case "function_call_output", "custom_tool_call_output":
				callID := asString(payload["call_id"])
				if tc, ok := toolCallsByID[callID]; ok {
					if tc.Output == nil {
						tc.Output = payload["output"]
					}
					tc.EndTime = maxInt64(tc.EndTime, ts)
					tc.HasEnd = true
				}
			case "reasoning":
				reasoning := extractReasoning(payload)
				if reasoning != "" {
					s := ensureStep(ts)
					if s.Reasoning != "" {
						s.Reasoning += "\n" + reasoning
					} else {
						s.Reasoning = reasoning
					}
				}
			}
		case "event_msg":
			eventType := asString(payload["type"])
			if eventType == "task_started" {
				if turn != nil {
					finishCurrentTurn(ts)
				}
				turn = &model.Turn{
					TurnID:            asString(payload["turn_id"]),
					StartTime:         ts,
					EndTime:           ts,
					Steps:             make([]*model.Step, 0),
					SubagentThreadIDs: make([]string, 0),
					InvocationParams:  map[string]any{},
				}
				step = nil
				toolCallsByID = map[string]*model.ToolCall{}
				continue
			}

			ensureTurn(ts)
			switch eventType {
			case "user_message":
				if turn.UserInput == "" {
					turn.UserInput = asString(payload["message"])
				}
			case "agent_message":
				message := asString(payload["message"])
				if message != "" {
					turn.LastAgentMessage = message
					attachAgentMessage(message, ts)
				}
			case "token_count":
				if info := asMap(payload["info"]); len(info) > 0 {
					if totalUsage := asMap(info["total_token_usage"]); len(totalUsage) > 0 {
						turn.TotalUsage = totalUsage
					}
					closeStep(ts, asMap(info["last_token_usage"]))
				} else {
					closeStep(ts, nil)
				}
			case "task_complete":
				lastMessage := asString(payload["last_agent_message"])
				if lastMessage != "" {
					turn.LastAgentMessage = lastMessage
				}
				finishTurn(ts, true, false)
			case "turn_aborted":
				finishTurn(ts, true, true)
			default:
				if eventType == "collab_agent_spawn_end" {
					if threadID := asString(payload["new_thread_id"]); threadID != "" {
						turn.SubagentThreadIDs = append(turn.SubagentThreadIDs, threadID)
					}
				}
				if callID := asString(payload["call_id"]); callID != "" && strings.HasSuffix(eventType, "_end") {
					if tc, ok := toolCallsByID[callID]; ok {
						tc.EndTime = maxInt64(tc.EndTime, ts)
						tc.HasEnd = true
						status := asString(payload["status"])
						if status == "failed" || status == "declined" {
							tc.Error = extractToolError(payload)
						}
						if tc.Output == nil {
							tc.Output = firstNonNil(payload["aggregated_output"], payload["stdout"], payload["result"])
						}
					}
				}
			}
		}
	}

	if turn != nil {
		finishCurrentTurn(lastTimestamp)
	}

	return model.ParsedSession{
		SessionMeta: sessionMeta,
		Turns:       turns,
	}
}

func extractMessageText(content any) string {
	items, ok := content.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0)
	for _, item := range items {
		entry := asMap(item)
		if len(entry) == 0 {
			continue
		}
		partType := asString(entry["type"])
		if partType == "input_text" || partType == "output_text" || partType == "text" {
			text := asString(entry["text"])
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func isSyntheticUserContext(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(trimmed, "<environment_context") ||
		strings.HasPrefix(trimmed, "<user_instructions") ||
		strings.HasPrefix(trimmed, "# AGENTS.md instructions") ||
		strings.Contains(trimmed, "<environment_context>") ||
		strings.Contains(trimmed, "<permissions instructions>")
}

func extractReasoning(item map[string]any) string {
	if content, ok := item["content"].(string); ok {
		return content
	}
	if content, ok := item["content"].([]any); ok {
		parts := make([]string, 0, len(content))
		for _, entry := range content {
			parts = append(parts, entryText(entry))
		}
		return strings.TrimSpace(strings.Join(filterNonEmpty(parts), "\n"))
	}
	if summary, ok := item["summary"].([]any); ok && len(summary) > 0 {
		parts := make([]string, 0, len(summary))
		for _, entry := range summary {
			parts = append(parts, entryText(entry))
		}
		return strings.TrimSpace(strings.Join(filterNonEmpty(parts), "\n"))
	}
	return ""
}

func entryText(value any) string {
	if entry := asMap(value); len(entry) > 0 {
		if text, ok := entry["text"]; ok {
			return util.ToText(text)
		}
	}
	return util.ToText(value)
}

func parseArgs(raw any) any {
	text, ok := raw.(string)
	if !ok {
		return raw
	}
	var parsed any
	if err := json.Unmarshal([]byte(text), &parsed); err == nil {
		return parsed
	}
	return raw
}

func extractToolError(payload map[string]any) string {
	if payload["error"] != nil {
		return util.ToText(payload["error"])
	}
	if payload["codex_error_info"] != nil {
		return util.ToText(payload["codex_error_info"])
	}
	streams := make([]string, 0, 2)
	if stdout := strings.TrimSpace(asString(payload["stdout"])); stdout != "" {
		streams = append(streams, stdout)
	}
	if stderr := strings.TrimSpace(asString(payload["stderr"])); stderr != "" {
		streams = append(streams, stderr)
	}
	if aggregated := asString(payload["aggregated_output"]); aggregated != "" {
		return aggregated
	}
	if len(streams) > 0 {
		return strings.Join(streams, "\n")
	}
	if exitCode, ok := payload["exit_code"].(float64); ok {
		return "Exit code: " + strings.TrimSuffix(strings.TrimSuffix(util.ToText(exitCode), ".0"), ".")
	}
	return ""
}

func normalizeISOTimestamp(value string, fallbackTs int64) string {
	if value != "" {
		if parsed, err := time.Parse(time.RFC3339, value); err == nil {
			return parsed.UTC().Format(time.RFC3339)
		}
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed.UTC().Format(time.RFC3339Nano)
		}
	}
	if fallbackTs > 0 {
		return time.UnixMilli(fallbackTs).UTC().Format(time.RFC3339Nano)
	}
	return ""
}

func parseLineTimestamp(line map[string]any, fallback int64) int64 {
	timestamp := asString(line["timestamp"])
	if timestamp == "" {
		return fallback
	}
	if parsed, err := time.Parse(time.RFC3339Nano, timestamp); err == nil {
		return parsed.UnixMilli()
	}
	if parsed, err := time.Parse(time.RFC3339, timestamp); err == nil {
		return parsed.UnixMilli()
	}
	return fallback
}

func nestedText(value map[string]any, keys ...string) string {
	current := value
	for index, key := range keys {
		item := current[key]
		if index == len(keys)-1 {
			return asString(item)
		}
		current = asMap(item)
		if len(current) == 0 {
			return ""
		}
	}
	return ""
}

func asString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func asMap(value any) map[string]any {
	if current, ok := value.(map[string]any); ok {
		return current
	}
	return map[string]any{}
}

func filterNonEmpty(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func maxInt64(left, right int64) int64 {
	if right > left {
		return right
	}
	return left
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}
