// Package parse normalizes native terminal OpenClaw plugin events.
package parse

import (
	"encoding/json"
	"fmt"
	"math"
	"path"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/openclaw/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

type Observation struct {
	Kind  string         `json:"kind"`
	At    int64          `json:"at"`
	Event map[string]any `json:"event"`
}

type hookWindow struct {
	start, end         int64
	messageID          string
	provider           string
	model              string
	inputMessages      any
	outputMessages     any
	systemInstructions any
	toolDefinitions    any
	inputPreview       string
	outputPreview      string
	inputLength        int
	outputLength       int
	outputKind         string
	finishReasons      []string
	reasoningEffort    string
}

type Payload struct {
	Event        string           `json:"event"`
	At           int64            `json:"at"`
	StartedAt    int64            `json:"startedAt"`
	SessionID    string           `json:"sessionId"`
	RunID        string           `json:"runId"`
	Trigger      string           `json:"trigger"`
	Success      *bool            `json:"success"`
	Error        string           `json:"error"`
	DurationMs   float64          `json:"durationMs"`
	Prompt       string           `json:"prompt"`
	Messages     []map[string]any `json:"messages"`
	Observations []Observation    `json:"observations"`
}

func Normalize(p Payload, cfg config.Config) (model.Turn, bool) {
	t := model.Turn{}
	if p.Event != "agent_end" || p.Success == nil || p.SessionID == "" || p.RunID == "" || p.At <= 0 {
		return t, false
	}
	switch strings.ToLower(p.Trigger) {
	case "heartbeat", "cron", "system", "internal", "title":
		return t, false
	}
	end := p.At * int64(time.Millisecond)
	start := p.StartedAt * int64(time.Millisecond)
	if p.DurationMs > 0 && p.DurationMs <= 86400000 {
		start = end - int64(p.DurationMs*float64(time.Millisecond))
	}
	if start <= 0 || start >= end {
		start = end - int64(time.Millisecond)
	}
	t = model.Turn{SessionID: p.SessionID, TurnID: p.RunID, AgentRuntime: "openclaw", AgentName: "openclaw", StartUnixNano: start, EndUnixNano: end, FinalStatus: model.FinalStatusCompleted, Resource: cfg.ResourceAttributes, ExtraAttributes: map[string]any{"openclaw.run_id": p.RunID, "trace_completeness": "partial"}}
	if name, ok := cfg.ResourceAttributes["agent_name"].(string); ok && name != "" {
		t.AgentName = name
	}
	if version, ok := cfg.ResourceAttributes["agent_version"].(string); ok {
		t.AgentVersion = version
	}
	if !*p.Success {
		t.ErrorType = "agent_error"
		t.Reason = privacy.Text(p.Error, cfg.MaxChars)
	}
	// A session snapshot includes previous turns. Restrict replay to the latest user boundary.
	messages := p.Messages
	userIndex := -1
	for i, m := range messages {
		if str(m, "role") == "user" {
			userIndex = i
		}
	}
	input := p.Prompt
	if userIndex >= 0 {
		user := messages[userIndex]
		if input == "" {
			input = contentText(user["content"])
		}
		messages = messages[userIndex+1:]
		// Without live observations, require timestamps in this run's window.
		if len(p.Observations) == 0 && timestamp(user["timestamp"]) < start-int64(time.Second) {
			return model.Turn{}, false
		}
	} else if len(p.Observations) == 0 {
		return model.Turn{}, false
	}
	if strings.TrimSpace(input) == "" {
		return model.Turn{}, false
	}
	t.InputLength = len([]rune(input))
	t.InputPreview = preview(input, cfg)
	t.InputMessages = textMessages("user", input, cfg)
	// Per-message usage is authoritative. llm_output may summarize an entire
	// attempt, so its aggregate usage must never be assigned to one LLM call.
	windows := map[int]hookWindow{}
	var inputAt int64
	var inputEvent map[string]any
	ambiguous := false
	for i, o := range p.Observations {
		at := o.At * int64(time.Millisecond)
		if at < start || at > end {
			continue
		}
		if o.Kind == "llm_input" {
			if inputAt != 0 {
				ambiguous = true
			} else {
				inputAt = at
				inputEvent = o.Event
			}
		}
		if (o.Kind == "before_tool_call" || o.Kind == "after_tool_call") && inputAt != 0 {
			ambiguous = true
		}
		if o.Kind == "llm_output" {
			if inputAt > 0 && at > inputAt && !ambiguous {
				m := assistantMessage(o.Event)
				inputText := str(inputEvent, "prompt")
				outputText := contentText(m["content"])
				provider := str(o.Event, "provider")
				if provider == "" {
					provider = str(inputEvent, "provider")
				}
				modelName := str(o.Event, "model")
				if modelName == "" {
					modelName = str(inputEvent, "model")
				}
				normalizedOutput := normalizeMessages([]any{m}, cfg)
				windows[i] = hookWindow{
					start: inputAt, end: at, messageID: str(m, "id"), provider: provider, model: modelName,
					inputMessages: inputMessages(inputEvent, cfg), outputMessages: normalizedOutput,
					systemInstructions: systemInstructions(str(inputEvent, "systemPrompt"), cfg), toolDefinitions: toolDefinitions(inputEvent["tools"], cfg),
					inputPreview: preview(inputText, cfg), outputPreview: preview(outputText, cfg),
					inputLength: len([]rune(inputText)), outputLength: len([]rune(outputText)),
					outputKind: messagesOutputKind(normalizedOutput), finishReasons: finishReasons(m), reasoningEffort: str(o.Event, "reasoningEffort"),
				}
			}
			inputAt, inputEvent, ambiguous = 0, nil, false
		}
	}
	assistantCount := 0
	snapshotLLM := false
	for _, m := range messages {
		at := timestamp(m["timestamp"])
		if str(m, "role") == "assistant" && at >= start && at <= end {
			snapshotLLM = true
			assistantCount++
		}
	}
	seenCalls := map[string]bool{}
	var modelCalls []model.LLMCall
	seenTools := map[string]bool{}
	seenLLM := map[string]bool{}
	modelStarts := map[string]int64{}
	toolStarts := map[string]int64{}
	for _, o := range p.Observations {
		at := o.At * int64(time.Millisecond)
		if at < start || at > end {
			continue
		}
		id := str(o.Event, "callId")
		if o.Kind == "model_call_started" && id != "" && str(o.Event, "runId") == p.RunID {
			modelStarts[id] = at
		}
		id = str(o.Event, "toolCallId")
		if o.Kind == "before_tool_call" && id != "" {
			toolStarts[id] = at
		}
	}
	for i, o := range p.Observations {
		at := o.At * int64(time.Millisecond)
		if at < start || at > end {
			continue
		}
		switch o.Kind {
		case "model_call_ended":
			ev := o.Event
			id := str(ev, "callId")
			duration, validDuration := ev["durationMs"].(float64)
			outcome := str(ev, "outcome")
			observedStart := modelStarts[id]
			hasObservedWindow := observedStart > 0 && observedStart < at
			if id == "" || str(ev, "provider") == "" || seenCalls[id] || str(ev, "runId") != p.RunID || !validDuration || math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0 || duration > 86400000 || (duration == 0 && !hasObservedWindow) || (outcome != "completed" && outcome != "error") {
				continue
			}
			seenCalls[id] = true
			callStart := at - int64(duration*float64(time.Millisecond))
			if hasObservedWindow {
				callStart = observedStart
			}
			call := model.LLMCall{CallID: id, Provider: str(ev, "provider"), RequestModel: str(ev, "model"), ResponseModel: str(ev, "model"), StartUnixNano: callStart, EndUnixNano: at, Status: "ok", ExtraAttributes: map[string]any{"openclaw.model_call_id": id, "openclaw.timing_source": "native_model_call"}}
			if outcome == "error" {
				call.Status = "error"
				call.ErrorType = str(ev, "errorCategory")
				if call.ErrorType == "" {
					call.ErrorType = str(ev, "failureKind")
				}
				if call.ErrorType == "" {
					call.ErrorType = "_OTHER"
				}
				if str(ev, "failureKind") == "aborted" {
					call.FinishReasons = []string{"cancelled"}
				}
			}
			if latency, ok := ev["timeToFirstByteMs"].(float64); ok && !math.IsNaN(latency) && !math.IsInf(latency, 0) && latency >= 0 && latency <= duration {
				call.FirstChunkMs = &latency
				call.ExtraAttributes["openclaw.first_chunk.source"] = "openclaw.timeToFirstByteMs"
			}
			modelCalls = append(modelCalls, call)

		case "llm_output":
			m := assistantMessage(o.Event)
			if contentText(m["content"]) == "" && rawOutputKind(m) == "" {
				continue
			}
			id := str(m, "id")
			if id == "" {
				id = fmt.Sprintf("llm-%d", i)
			}
			if seenLLM[id] {
				continue
			}
			seenLLM[id] = true
			call := llm(m, id, at-int64(time.Millisecond), at, cfg)
			if window, ok := windows[i]; ok {
				applyWindow(&call, window)
				call.ExtraAttributes["openclaw.timing_source"] = "native_hook_boundary"
			}
			call.Provider = str(o.Event, "provider")
			call.RequestModel = str(o.Event, "model")
			call.ResponseModel = call.RequestModel
			t.LLMCalls = append(t.LLMCalls, call)
			t.OutputLength = len([]rune(contentText(m["content"])))
			t.OutputPreview = call.OutputPreview
			t.OutputMessages = call.OutputMessages
			if len(call.FinishReasons) > 0 && call.FinishReasons[0] == "cancelled" {
				t.FinalStatus = model.FinalStatusCancelled
				t.ErrorType = "cancelled"
			}
		case "after_tool_call":
			ev := o.Event
			id := str(ev, "toolCallId")
			if id == "" || seenTools[id] {
				continue
			}
			seenTools[id] = true
			duration, validDuration := ev["durationMs"].(float64)
			toolStart := int64(0)
			if validDuration && !math.IsNaN(duration) && !math.IsInf(duration, 0) && duration > 0 && duration <= 86400000 {
				toolStart = at - int64(duration*float64(time.Millisecond))
			} else {
				toolStart = toolStarts[id]
			}
			if toolStart < start || toolStart >= at {
				continue
			}
			call := model.ToolCall{CallID: id, Name: str(ev, "toolName"), StartUnixNano: toolStart, EndUnixNano: at, Command: commandValue(ev["params"], cfg), Status: "ok", ResultStatus: "completed"}
			if str(ev, "error") != "" {
				call.Status = "error"
				call.ErrorType = "tool_error"
				call.ResultStatus = "error"
				call.Reason = privacy.Text(ev["error"], cfg.MaxChars)
			}
			if cfg.CaptureContent != "none" {
				call.Arguments = privacy.Sanitize(ev["params"], cfg.MaxChars)
				call.Result = privacy.Sanitize(ev["result"], cfg.MaxChars)
			}
			attachSkill(&call, ev["params"], cfg)
			t.ToolCalls = append(t.ToolCalls, call)
		}
	}
	nativeLLM := len(t.LLMCalls) > 0
	for i, m := range messages {
		if nativeLLM || str(m, "role") != "assistant" {
			continue
		}
		at := timestamp(m["timestamp"])
		if at > 0 && (at < start-int64(time.Second) || at > end+int64(time.Second)) {
			continue
		}
		if normalized := normalizeMessages([]any{m}, cfg); normalized != nil {
			t.OutputMessages = normalized
			t.OutputKind = messagesOutputKind(normalized)
		}
		output := contentText(m["content"])
		if output != "" {
			t.OutputPreview = preview(output, cfg)
			t.OutputLength = len([]rune(output))
		}
		if !nativeLLM {
			if at <= 0 || at > end {
				at = end
			}
			callStart := at - int64(time.Millisecond)
			transcriptBoundary := assistantCount == 1 && at > start && str(m, "provider") != "" && str(m, "model") != ""
			if transcriptBoundary {
				callStart = start
			}
			call := llm(m, fmt.Sprintf("message-%d", i), callStart, at, cfg)
			if transcriptBoundary {
				call.InputPreview = t.InputPreview
				call.InputMessages = t.InputMessages
				call.InputLength = t.InputLength
				call.ExtraAttributes["openclaw.timing_source"] = "transcript_turn_boundary"
			}
			for _, window := range windows {
				if assistantCount == 1 && ((window.messageID != "" && window.messageID == str(m, "id")) || len(windows) == 1) {
					applyWindow(&call, window)
					call.ExtraAttributes["openclaw.timing_source"] = "native_hook_boundary"
					break
				}
			}
			t.LLMCalls = append(t.LLMCalls, call)
		}
		if str(m, "stopReason") == "aborted" {
			t.FinalStatus = model.FinalStatusCancelled
			t.ErrorType = "cancelled"
		}
	}
	// Recover tool calls from the current transcript when the harness omits native tool hooks.
	if snapshotLLM {
		for i, m := range messages {
			at := timestamp(m["timestamp"])
			if at < start || at > end {
				continue
			}
			if str(m, "role") != "assistant" {
				continue
			}
			blocks, _ := m["content"].([]any)
			for _, raw := range blocks {
				block, _ := raw.(map[string]any)
				if str(block, "type") != "toolCall" {
					continue
				}
				id := str(block, "id")
				if id == "" || seenTools[id] {
					continue
				}
				seenTools[id] = true
				tool := model.ToolCall{CallID: id, TriggeringLLMCall: fmt.Sprintf("message-%d", i), Name: str(block, "name"), StartUnixNano: at, Command: commandValue(block["arguments"], cfg), Status: "unset", ExtraAttributes: map[string]any{"openclaw.timing_source": "transcript_result_boundary"}}
				if cfg.CaptureContent != "none" {
					tool.Arguments = privacy.Sanitize(block["arguments"], cfg.MaxChars)
				}
				attachSkill(&tool, block["arguments"], cfg)
				resolved := false
				for _, result := range messages {
					if str(result, "role") == "toolResult" && str(result, "toolCallId") == id {
						tool.Status = "ok"
						tool.ResultStatus = "completed"
						if failed, _ := result["isError"].(bool); failed {
							tool.Status = "error"
							tool.ErrorType = "tool_error"
							tool.ResultStatus = "error"
						}
						if cfg.CaptureContent != "none" {
							tool.Result = privacy.Sanitize(result["content"], cfg.MaxChars)
						}
						if ts := timestamp(result["timestamp"]); ts > at && ts <= end {
							tool.EndUnixNano = ts
							resolved = true
						}
						break
					}
				}
				if !resolved {
					continue
				}
				if tool.Skill != nil {
					tool.Skill.Status = tool.Status
				}
				t.ToolCalls = append(t.ToolCalls, tool)
			}
		}
	}

	// Some harnesses omit the final snapshot but expose native assistant output.
	if t.OutputLength == 0 && len(t.LLMCalls) > 0 {
		last := t.LLMCalls[len(t.LLMCalls)-1]
		t.OutputPreview = last.OutputPreview
		t.OutputMessages = last.OutputMessages
	}
	if snapshotLLM {
		for _, message := range messages {
			at := timestamp(message["timestamp"])
			if str(message, "role") == "assistant" && at >= start && at <= end {
				addUsage(&t.Usage, usageMap(message["usage"]))
			}
		}
	} else {
		for _, call := range t.LLMCalls {
			addUsage(&t.Usage, call.Usage)
		}
	}
	if len(modelCalls) == 0 {
		measured := make([]model.LLMCall, 0, len(t.LLMCalls))
		for _, call := range t.LLMCalls {
			if source := call.ExtraAttributes["openclaw.timing_source"]; source == "native_hook_boundary" || source == "transcript_turn_boundary" {
				measured = append(measured, call)
			}
		}
		if len(measured) != len(t.LLMCalls) {
			t.AggregateUsageOnly = true
			t.ExtraAttributes["openclaw.llm_timing_unavailable"] = true
			for i := range measured {
				measured[i].Usage = model.Usage{}
			}
			for i := range t.ToolCalls {
				t.ToolCalls[i].TriggeringLLMCall = ""
			}
		}
		t.LLMCalls = measured
	}
	if len(modelCalls) > 0 {
		// Content hooks intentionally omit callId. Attach content only when one
		// provider call is fully contained by one unique input/output window.
		windowMatches := map[int][]int{}
		callMatches := map[int][]int{}
		windowKeys := make([]int, 0, len(windows))
		for key := range windows {
			windowKeys = append(windowKeys, key)
		}
		for callIndex, call := range modelCalls {
			for windowIndex, key := range windowKeys {
				window := windows[key]
				providerMatches := window.provider == "" || call.Provider == "" || window.provider == call.Provider
				modelMatches := window.model == "" || call.RequestModel == "" || window.model == call.RequestModel
				if providerMatches && modelMatches && call.StartUnixNano >= window.start && call.EndUnixNano <= window.end {
					callMatches[callIndex] = append(callMatches[callIndex], windowIndex)
					windowMatches[windowIndex] = append(windowMatches[windowIndex], callIndex)
				}
			}
		}
		for callIndex, matches := range callMatches {
			if len(matches) != 1 || len(windowMatches[matches[0]]) != 1 {
				continue
			}
			window := windows[windowKeys[matches[0]]]
			applyWindowContent(&modelCalls[callIndex], window)
			modelCalls[callIndex].ExtraAttributes["openclaw.content_source"] = "unique_hook_window"
		}
		// Transcript usage is a turn aggregate. Do not join it to native calls by
		// time proximity or array position, or count both sets as model calls.
		t.LLMCalls = modelCalls
		t.AggregateUsageOnly = true
		for i := range t.ToolCalls {
			t.ToolCalls[i].TriggeringLLMCall = ""
		}
		for _, call := range modelCalls {
			if call.StartUnixNano < t.StartUnixNano {
				t.StartUnixNano = call.StartUnixNano
			}
		}
	}
	applyTurnSummary(&t)
	if t.OutputPreview != "" || t.OutputLength > 0 || t.OutputMessages != nil {
		// Match the legacy collector's model-end -> run-completion output phase,
		// but only when an observed boundary exists; never pad for visibility.
		var outputStart, lastToolEnd int64
		for _, call := range t.LLMCalls {
			if call.EndUnixNano > outputStart {
				outputStart = call.EndUnixNano
			}
		}
		for _, o := range p.Observations {
			at := o.At * int64(time.Millisecond)
			if o.Kind == "llm_output" && at >= start && at <= end && at > outputStart {
				outputStart = at
			}
		}
		for _, call := range t.ToolCalls {
			if call.EndUnixNano > lastToolEnd {
				lastToolEnd = call.EndUnixNano
			}
		}
		source := "model_end_to_run_end"
		if outputStart <= 0 || outputStart > end || outputStart < lastToolEnd {
			outputStart, source = end, "terminal_output_event"
		}
		t.AssistantOutputs = []model.AssistantOutput{{StartUnixNano: outputStart, EndUnixNano: end, OutputPreview: t.OutputPreview, OutputMessages: t.OutputMessages, OutputLength: t.OutputLength, OutputKind: t.OutputKind, Provider: t.Provider, RequestModel: t.RequestModel, ResponseModel: t.ResponseModel, Status: statusValue(t.ErrorType), ErrorType: t.ErrorType, Reason: t.Reason, ExtraAttributes: map[string]any{"openclaw.timing_source": source}}}
	}
	if *p.Success && len(t.LLMCalls) == 0 && t.OutputLength == 0 && len(t.ToolCalls) == 0 && t.Usage == (model.Usage{}) {
		return model.Turn{}, false
	}
	return t, true
}

func llm(m map[string]any, id string, start, end int64, cfg config.Config) model.LLMCall {
	if start <= 0 || start >= end {
		start = end - int64(time.Millisecond)
	}
	output := contentText(m["content"])
	c := model.LLMCall{CallID: id, StartUnixNano: start, EndUnixNano: end, Provider: str(m, "provider"), RequestModel: str(m, "model"), ResponseModel: str(m, "model"), OutputPreview: preview(output, cfg), OutputLength: len([]rune(output)), OutputKind: rawOutputKind(m), Status: "ok", ExtraAttributes: map[string]any{"openclaw.timing_source": "estimated_message_boundary"}}
	if u, ok := m["usage"].(map[string]any); ok {
		c.Usage = usage(u)
	}
	if reasons := finishReasons(m); len(reasons) > 0 {
		c.FinishReasons = reasons
		if reasons[0] == "error" || reasons[0] == "cancelled" {
			c.Status = "error"
			c.ErrorType = reasons[0]
			c.Reason = privacy.Text(m["errorMessage"], cfg.MaxChars)
		}
	}
	c.OutputMessages = normalizeMessages([]any{m}, cfg)
	return c
}

func assistantMessage(event map[string]any) map[string]any {
	if message, _ := event["lastAssistant"].(map[string]any); message != nil {
		copy := make(map[string]any, len(message)+1)
		for key, value := range message {
			copy[key] = value
		}
		if str(copy, "role") == "" {
			copy["role"] = "assistant"
		}
		return copy
	}
	return map[string]any{"role": "assistant", "content": event["assistantTexts"]}
}

func applyWindow(call *model.LLMCall, window hookWindow) {
	call.StartUnixNano, call.EndUnixNano = window.start, window.end
	applyWindowContent(call, window)
}

func applyWindowContent(call *model.LLMCall, window hookWindow) {
	call.InputMessages, call.OutputMessages = window.inputMessages, window.outputMessages
	call.SystemInstructions, call.ToolDefinitions = window.systemInstructions, window.toolDefinitions
	call.InputPreview, call.OutputPreview = window.inputPreview, window.outputPreview
	call.InputLength, call.OutputLength = window.inputLength, window.outputLength
	call.OutputKind = window.outputKind
	if window.reasoningEffort != "" {
		call.ExtraAttributes["openclaw.reasoning_effort"] = window.reasoningEffort
	}
	if len(call.FinishReasons) == 0 {
		call.FinishReasons = window.finishReasons
	}
}

func applyTurnSummary(turn *model.Turn) {
	turn.OutputKind = messagesOutputKind(turn.OutputMessages)
	if len(turn.LLMCalls) == 0 {
		return
	}
	last := turn.LLMCalls[len(turn.LLMCalls)-1]
	turn.Provider = last.Provider
	turn.RequestModel = last.RequestModel
	turn.ResponseModel = last.ResponseModel
	turn.FinishReasons = append([]string(nil), last.FinishReasons...)
	if last.OutputKind != "" {
		turn.OutputKind = last.OutputKind
	}
}

func inputMessages(event map[string]any, cfg config.Config) any {
	if cfg.CaptureContent == "none" {
		return nil
	}
	messages, _ := normalizeMessages(event["historyMessages"], cfg).([]any)
	if prompt := str(event, "prompt"); strings.TrimSpace(prompt) != "" {
		if current, ok := textMessages("user", prompt, cfg).([]any); ok {
			messages = append(messages, current...)
		}
	}
	if len(messages) == 0 {
		return nil
	}
	return messages
}

func systemInstructions(text string, cfg config.Config) any {
	if cfg.CaptureContent == "none" || strings.TrimSpace(text) == "" {
		return nil
	}
	return []any{map[string]any{"type": "text", "content": privacy.Text(text, cfg.MaxChars)}}
}

func toolDefinitions(value any, cfg config.Config) any {
	if cfg.CaptureContent == "none" || value == nil {
		return nil
	}
	raw, _ := value.([]any)
	out := make([]any, 0, len(raw))
	for _, item := range raw {
		tool, _ := item.(map[string]any)
		name := firstString(tool, "name")
		if name == "" {
			continue
		}
		definition := map[string]any{"type": "function", "name": name}
		if description := firstString(tool, "description"); description != "" {
			definition["description"] = privacy.Text(description, cfg.MaxChars)
		}
		if parameters := firstNonNil(tool["parameters"], tool["inputSchema"], tool["input_schema"]); parameters != nil {
			definition["parameters"] = privacy.Sanitize(parameters, cfg.MaxChars)
		}
		out = append(out, definition)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeMessages(value any, cfg config.Config) any {
	if cfg.CaptureContent == "none" || value == nil {
		return nil
	}
	var raw []any
	switch typed := value.(type) {
	case []any:
		raw = typed
	case []map[string]any:
		for _, message := range typed {
			raw = append(raw, message)
		}
	case map[string]any:
		raw = []any{typed}
	default:
		return nil
	}
	out := make([]any, 0, len(raw))
	for _, item := range raw {
		message, _ := item.(map[string]any)
		if normalized := normalizeMessage(message, cfg); normalized != nil {
			out = append(out, normalized)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func normalizeMessage(message map[string]any, cfg config.Config) any {
	if message == nil {
		return nil
	}
	role := str(message, "role")
	if role == "toolResult" || role == "tool_result" || role == "tool" {
		response := privacy.Sanitize(message["content"], cfg.MaxChars)
		if response == nil {
			return nil
		}
		part := map[string]any{"type": "tool_call_response", "response": response}
		if id := firstString(message, "toolCallId", "tool_call_id", "id"); id != "" {
			part["id"] = id
		}
		return map[string]any{"role": "tool", "parts": []any{part}}
	}
	if role != "user" && role != "assistant" && role != "system" && role != "tool" {
		return nil
	}
	parts := normalizeParts(message["content"], cfg)
	if len(parts) == 0 {
		return nil
	}
	return map[string]any{"role": role, "parts": parts}
}

func normalizeParts(value any, cfg config.Config) []any {
	if text, ok := value.(string); ok {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []any{map[string]any{"type": "text", "content": privacy.Text(text, cfg.MaxChars)}}
	}
	raw, ok := value.([]any)
	if !ok {
		return nil
	}
	parts := make([]any, 0, len(raw))
	for _, item := range raw {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			parts = append(parts, map[string]any{"type": "text", "content": privacy.Text(text, cfg.MaxChars)})
			continue
		}
		block, _ := item.(map[string]any)
		typ := strings.ToLower(firstString(block, "type"))
		switch typ {
		case "text", "input_text", "output_text":
			if text := firstString(block, "text", "content"); strings.TrimSpace(text) != "" {
				parts = append(parts, map[string]any{"type": "text", "content": privacy.Text(text, cfg.MaxChars)})
			}
		case "thinking", "reasoning":
			if text := firstString(block, "thinking", "text", "content"); strings.TrimSpace(text) != "" {
				parts = append(parts, map[string]any{"type": "reasoning", "content": privacy.Text(text, cfg.MaxChars)})
			}
		case "toolcall", "tool_call", "tool_use":
			if name := firstString(block, "name"); name != "" {
				part := map[string]any{"type": "tool_call", "name": name}
				if id := firstString(block, "id", "toolCallId", "tool_call_id"); id != "" {
					part["id"] = id
				}
				if arguments := firstNonNil(block["arguments"], block["input"]); arguments != nil {
					part["arguments"] = privacy.Sanitize(arguments, cfg.MaxChars)
				}
				parts = append(parts, part)
			}
		case "toolresult", "tool_result", "tool_call_response":
			if response := firstNonNil(block["response"], block["result"], block["content"]); response != nil {
				part := map[string]any{"type": "tool_call_response", "response": privacy.Sanitize(response, cfg.MaxChars)}
				if id := firstString(block, "id", "toolCallId", "tool_call_id"); id != "" {
					part["id"] = id
				}
				parts = append(parts, part)
			}
		}
	}
	return parts
}

func rawOutputKind(message map[string]any) string {
	content := message["content"]
	if text, ok := content.(string); ok && strings.TrimSpace(text) != "" {
		return "text"
	}
	raw, _ := content.([]any)
	kind := ""
	for _, item := range raw {
		if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
			kind = "text"
			continue
		}
		block, _ := item.(map[string]any)
		switch strings.ToLower(firstString(block, "type")) {
		case "toolcall", "tool_call", "tool_use":
			return "tool_call"
		case "text", "input_text", "output_text":
			kind = "text"
		case "thinking", "reasoning":
			if kind == "" {
				kind = "reasoning"
			}
		}
	}
	return kind
}

func messagesOutputKind(value any) string {
	messages, _ := value.([]any)
	kind := ""
	for _, item := range messages {
		message, _ := item.(map[string]any)
		parts, _ := message["parts"].([]any)
		for _, raw := range parts {
			part, _ := raw.(map[string]any)
			switch str(part, "type") {
			case "tool_call":
				return "tool_call"
			case "text":
				kind = "text"
			case "reasoning":
				if kind == "" {
					kind = "reasoning"
				}
			}
		}
	}
	return kind
}

func finishReasons(message map[string]any) []string {
	raw := strings.ToLower(strings.TrimSpace(firstString(message, "stopReason", "stop_reason")))
	switch raw {
	case "stop", "end_turn", "stop_sequence":
		return []string{"stop"}
	case "toolcall", "tool_call", "tool_use", "tooluse":
		return []string{"tool_call"}
	case "aborted", "cancelled", "canceled":
		return []string{"cancelled"}
	case "error":
		return []string{"error"}
	case "":
		if rawOutputKind(message) == "tool_call" {
			return []string{"tool_call"}
		}
		return nil
	default:
		return nil
	}
}

func firstString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func statusValue(errorType string) string {
	if strings.TrimSpace(errorType) != "" {
		return "error"
	}
	return "ok"
}

func commandValue(value any, cfg config.Config) string {
	if cfg.CaptureContent == "none" {
		return ""
	}
	arguments, _ := value.(map[string]any)
	command := firstNonNil(arguments["cmd"], arguments["command"])
	if command == nil {
		return ""
	}
	return privacy.Text(command, cfg.MaxChars)
}

func textMessages(role, text string, cfg config.Config) any {
	if cfg.CaptureContent == "none" || strings.TrimSpace(text) == "" {
		return nil
	}
	return []any{map[string]any{
		"role":  role,
		"parts": []any{map[string]any{"type": "text", "content": privacy.Text(text, cfg.MaxChars)}},
	}}
}
func usage(u map[string]any) model.Usage {
	return model.Usage{InputTokens: int64(number(u["input"])), OutputTokens: int64(number(u["output"])), CacheReadTokens: int64(number(u["cacheRead"])), CacheCreateTokens: int64(number(u["cacheWrite"]))}
}
func usageMap(value any) model.Usage {
	current, _ := value.(map[string]any)
	return usage(current)
}
func addUsage(total *model.Usage, current model.Usage) {
	total.InputTokens += current.InputTokens
	total.OutputTokens += current.OutputTokens
	total.CacheReadTokens += current.CacheReadTokens
	total.CacheCreateTokens += current.CacheCreateTokens
	total.ReasoningTokens += current.ReasoningTokens
}
func number(v any) float64 {
	n, _ := v.(float64)
	if n < 0 {
		return 0
	}
	return n
}
func str(m map[string]any, k string) string { s, _ := m[k].(string); return s }
func preview(v string, c config.Config) string {
	if c.CaptureContent == "none" {
		return ""
	}
	return privacy.Preview(v, c.MaxChars)
}
func timestamp(v any) int64 {
	if n, ok := v.(float64); ok {
		return int64(n) * int64(time.Millisecond)
	}
	if s, ok := v.(string); ok {
		t, e := time.Parse(time.RFC3339Nano, s)
		if e == nil {
			return t.UnixNano()
		}
	}
	return 0
}
func contentText(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case []any:
		var parts []string
		for _, b := range x {
			if s, ok := b.(string); ok {
				parts = append(parts, s)
			}
			if m, ok := b.(map[string]any); ok && str(m, "type") == "text" {
				parts = append(parts, str(m, "text"))
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// Decode enforces JSON syntax before normalization.
func Decode(body []byte) (Payload, error) {
	var p Payload
	err := json.Unmarshal(body, &p)
	return p, err
}

func attachSkill(call *model.ToolCall, raw any, cfg config.Config) {
	params, _ := raw.(map[string]any)
	toolPath := str(params, "path")
	if toolPath == "" {
		toolPath = str(params, "file_path")
	}
	normalizedPath := strings.ReplaceAll(toolPath, `\`, "/")
	if (call.Name == "read" || call.Name == "read_file") && path.Base(normalizedPath) == "SKILL.md" {
		call.Skill = &model.SkillUse{Name: path.Base(path.Dir(normalizedPath)), CallID: call.CallID, SourceType: "tool", Status: call.Status}
		if cfg.CaptureContent != "none" {
			call.Skill.Path = privacy.Text(toolPath, cfg.MaxChars)
		}
	}
}
