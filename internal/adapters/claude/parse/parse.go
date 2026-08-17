package parse

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	claudeconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	previewcore "github.com/GuanceCloud/obs-agent-connector/internal/core/preview"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

type HookPayload struct {
	SessionID            string
	TranscriptPath       string
	Cwd                  string
	EventName            string
	Version              string
	LastAssistantMessage string
}

type rawTurn struct {
	user            map[string]any
	assistants      []map[string]any
	assistantIndex  map[string]int
	toolResults     map[string]toolResult
	injectedByTool  map[string]string
	durationMs      float64
	durationTime    int64
	hasDuration     bool
	closedByNext    bool
	completedByHook bool
}

type toolResult struct {
	Content         any
	IsError         bool
	Timestamp       int64
	DurationSeconds float64
}

func ReadTranscript(path string) ([]map[string]any, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	messages := make([]map[string]any, 0)
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var message map[string]any
		if json.Unmarshal([]byte(line), &message) == nil {
			messages = append(messages, message)
		}
	}
	if err := scanner.Err(); err != nil {
		return messages, err
	}
	return messages, nil
}

func Normalize(payload HookPayload, cfg claudeconfig.Config, messages []map[string]any) []model.Turn {
	includePending := payload.EventName == "Stop" && strings.TrimSpace(payload.LastAssistantMessage) != ""
	rawTurns := buildTurns(messages, includePending)
	applyStopFallback(payload, rawTurns)
	turns := make([]model.Turn, 0, len(rawTurns))
	agentVersion := payload.Version
	if agentVersion == "" {
		for _, message := range messages {
			if value := stringValue(message["version"]); value != "" {
				agentVersion = value
				break
			}
		}
	}
	host, _ := os.Hostname()
	for index, raw := range rawTurns {
		terminal := raw.closedByNext || raw.hasDuration || raw.completedByHook
		if !terminal && (payload.EventName == "Stop" || payload.EventName == "SessionEnd") {
			terminal = pendingComplete(raw)
		}
		if !terminal {
			continue
		}
		turn := normalizeTurn(payload, cfg, raw, index+1, agentVersion, host)
		if turn.FinalStatus != model.FinalStatusUnset {
			turns = append(turns, turn)
		}
	}
	return turns
}

// applyStopFallback uses the authoritative Stop payload when Claude has not
// flushed the current turn's final assistant message to the transcript yet.
// Claude documents transcript_path as asynchronously written at Stop time.
func applyStopFallback(payload HookPayload, turns []*rawTurn) {
	if payload.EventName != "Stop" || strings.TrimSpace(payload.LastAssistantMessage) == "" || len(turns) == 0 {
		return
	}
	turn := turns[len(turns)-1]
	if turn == nil || turn.user == nil || turn.hasDuration || pendingComplete(turn) {
		return
	}
	lastTimestamp := timestamp(turn.user)
	model := "claude"
	if len(turn.assistants) > 0 {
		last := turn.assistants[len(turn.assistants)-1]
		lastTimestamp = maxInt64(lastTimestamp, timestamp(last))
		model = modelName(last)
	}
	if lastTimestamp <= 0 {
		lastTimestamp = time.Now().UnixNano()
	} else {
		lastTimestamp++
	}
	sum := sha256.Sum256([]byte(payload.SessionID + "\x00" + messageID(turn.user) + "\x00" + payload.LastAssistantMessage))
	turn.assistants = append(turn.assistants, map[string]any{
		"type":      "assistant",
		"timestamp": time.Unix(0, lastTimestamp).UTC().Format(time.RFC3339Nano),
		"message": map[string]any{
			"id":          "hook-final-" + hex.EncodeToString(sum[:8]),
			"role":        "assistant",
			"model":       model,
			"stop_reason": "end_turn",
			"content": []any{
				map[string]any{"type": "text", "text": payload.LastAssistantMessage},
			},
		},
	})
	turn.completedByHook = true
}

func buildTurns(messages []map[string]any, includePending bool) []*rawTurn {
	turns := make([]*rawTurn, 0)
	var current *rawTurn
	flush := func(closedByNext, allowPending bool) {
		if current == nil || current.user == nil || (len(current.assistants) == 0 && !allowPending) {
			return
		}
		current.closedByNext = closedByNext
		turns = append(turns, current)
	}
	for _, message := range messages {
		if boolValue(message["isMeta"]) {
			if current != nil {
				toolID := stringValue(message["sourceToolUseID"])
				if text := extractText(content(message)); toolID != "" && text != "" {
					current.injectedByTool[toolID] = text
				}
			}
			continue
		}
		if isToolResultMessage(message) {
			if current != nil {
				duration := toolDuration(message)
				for _, result := range blocks(content(message), "tool_result") {
					toolID := stringValue(result["tool_use_id"])
					if toolID == "" {
						continue
					}
					current.toolResults[toolID] = toolResult{
						Content:         result["content"],
						IsError:         boolValue(result["is_error"]),
						Timestamp:       timestamp(message),
						DurationSeconds: duration,
					}
				}
			}
			continue
		}
		if stringValue(message["type"]) == "system" && stringValue(message["subtype"]) == "turn_duration" {
			if current != nil {
				if value, ok := number(message["durationMs"]); ok && value >= 0 {
					current.durationMs = value
					current.durationTime = timestamp(message)
					current.hasDuration = true
				}
			}
			continue
		}
		switch role(message) {
		case "user":
			flush(true, false)
			current = &rawTurn{
				user:           message,
				assistantIndex: map[string]int{},
				toolResults:    map[string]toolResult{},
				injectedByTool: map[string]string{},
			}
		case "assistant":
			if current == nil {
				continue
			}
			messageID := messageID(message)
			if messageID == "" {
				messageID = fmt.Sprintf("noid:%d", len(current.assistants))
			}
			if existing, ok := current.assistantIndex[messageID]; ok {
				current.assistants[existing] = mergeAssistant(current.assistants[existing], message)
			} else {
				current.assistantIndex[messageID] = len(current.assistants)
				current.assistants = append(current.assistants, message)
			}
		}
	}
	flush(false, includePending)
	return turns
}

func normalizeTurn(
	payload HookPayload,
	cfg claudeconfig.Config,
	raw *rawTurn,
	turnNumber int,
	agentVersion string,
	host string,
) model.Turn {
	userText := extractText(content(raw.user))
	userTime := timestamp(raw.user)
	if userTime <= 0 {
		userTime = time.Now().UnixNano()
	}
	turnID := messageID(raw.user)
	if turnID == "" {
		sum := sha256.Sum256([]byte(fmt.Sprintf(
			"%s\x00%d\x00%d\x00%s",
			payload.SessionID, turnNumber, userTime, userText,
		)))
		turnID = "turn_" + hex.EncodeToString(sum[:8])
	}
	capture := cfg.CaptureContent != "none"
	inputPreview := ""
	var inputMessages any
	if capture {
		inputPreview = previewcore.Text(userText, cfg.MaxChars)
		inputMessages = []any{map[string]any{
			"role": "user",
			"parts": []any{map[string]any{
				"type": "text", "content": privacy.Text(userText, cfg.MaxChars),
			}},
		}}
	}

	resource := copyMap(cfg.ResourceAttributes)
	resource["agent_runtime"] = "claude"
	resource["telemetry.sdk.language"] = "go"
	resource["telemetry.sdk.name"] = "gtrace"
	if agentVersion != "" {
		resource["agent_version"] = agentVersion
		resource["gen_ai.agent.version"] = agentVersion
	}
	if host != "" {
		resource["host"] = host
		resource["host.name"] = host
	}

	turn := model.Turn{
		SessionID:     payload.SessionID,
		TurnID:        turnID,
		AgentRuntime:  "claude",
		AgentName:     "claude",
		AgentVersion:  agentVersion,
		StartUnixNano: userTime,
		FinalStatus:   model.FinalStatusCompleted,
		InputMessages: inputMessages,
		InputPreview:  inputPreview,
		InputLength:   len([]rune(userText)),
		Resource:      resource,
		ExtraAttributes: map[string]any{
			"claude.turn.number":  turnNumber,
			"request_type":        "user_request",
			"is_internal_request": false,
		},
	}
	if cfg.UserID != "" {
		turn.ExtraAttributes["user_id"] = cfg.UserID
	}

	cursor := userTime
	var finalText string
	var finalOutputMessages any
	var lastToolResult any
	var lastNonErrorAssistant int64
	var maxChildEnd int64
	var previousToolResults []any

	for assistantIndex, assistant := range raw.assistants {
		assistantTime := timestamp(assistant)
		if assistantTime <= 0 {
			assistantTime = cursor + 1
		}
		callID := messageID(assistant)
		if callID == "" {
			callID = fmt.Sprintf("%s:llm:%d", turnID, assistantIndex+1)
		}
		modelName := modelName(assistant)
		assistantText := extractText(content(assistant))
		toolUses := blocks(content(assistant), "tool_use")
		apiStatus, apiErrorType, apiReason := apiError(assistant)
		_ = apiStatus
		if apiErrorType == "" {
			lastNonErrorAssistant = maxInt64(lastNonErrorAssistant, assistantTime)
		} else {
			turn.ErrorType = apiErrorType
			turn.Reason = apiReason
		}
		usage := usage(assistant)
		turn.Usage = mergeUsage(turn.Usage, usage)

		var llmInput any
		var llmOutput any
		llmInputPreview := ""
		llmOutputPreview := ""
		if capture {
			if assistantIndex == 0 {
				llmInput = inputMessages
				llmInputPreview = previewcore.Text(userText, cfg.MaxChars)
			} else if len(previousToolResults) > 0 {
				llmInput = previousToolResults
				llmInputPreview = previewcore.Text(previousToolResults, cfg.MaxChars)
			}
			llmOutput = outputMessages(assistantText, toolUses, cfg.MaxChars)
			llmOutputPreview = previewcore.Text(map[string]any{
				"text":       assistantText,
				"tool_calls": toolUses,
			}, cfg.MaxChars)
		}
		call := model.LLMCall{
			CallID:         callID,
			StartUnixNano:  cursor,
			EndUnixNano:    maxInt64(cursor+1, assistantTime),
			Provider:       "anthropic",
			RequestModel:   modelName,
			ResponseModel:  modelName,
			InputMessages:  llmInput,
			OutputMessages: llmOutput,
			InputPreview:   llmInputPreview,
			OutputPreview:  llmOutputPreview,
			OutputKind:     ternary(len(toolUses) > 0, "tool_call", "text"),
			FinishReasons:  nonEmptyStrings(stopReason(assistant)),
			Usage:          usage,
			Status:         ternary(apiErrorType != "", "error", "ok"),
			ErrorType:      apiErrorType,
			Reason:         apiReason,
			ExtraAttributes: map[string]any{
				"timing.source": "inferred",
			},
		}
		turn.LLMCalls = append(turn.LLMCalls, call)
		maxChildEnd = maxInt64(maxChildEnd, call.EndUnixNano)

		toolResultMessages := make([]any, 0)
		for _, toolUse := range toolUses {
			toolID := stringValue(toolUse["id"])
			toolName := firstNonEmpty(stringValue(toolUse["name"]), "unknown")
			result, hasResult := raw.toolResults[toolID]
			if hasResult {
				lastToolResult = result.Content
			}
			toolEnd := result.Timestamp
			if result.DurationSeconds >= 0 && hasResult {
				toolEnd = assistantTime + int64(result.DurationSeconds*float64(time.Second))
			}
			if toolEnd <= assistantTime {
				toolEnd = assistantTime + 1
			}
			resultStatus := "completed"
			errorType := ""
			reason := ""
			if !hasResult {
				resultStatus = "unset"
			} else if result.IsError {
				resultStatus = "error"
				errorType = "_OTHER"
				reason = privacy.Text(result.Content, cfg.MaxChars)
			}
			var arguments any
			var resultValue any
			var toolInputPreview, toolOutputPreview string
			if capture {
				arguments = privacy.Sanitize(toolUse["input"], cfg.MaxChars)
				resultValue = privacy.Sanitize(result.Content, cfg.MaxChars)
				toolInputPreview = previewcore.Text(toolUse["input"], cfg.MaxChars)
				toolOutputPreview = previewcore.Text(result.Content, cfg.MaxChars)
			}
			tool := model.ToolCall{
				CallID:            toolID,
				TriggeringLLMCall: callID,
				Name:              toolName,
				StartUnixNano:     assistantTime,
				EndUnixNano:       toolEnd,
				Arguments:         arguments,
				Result:            resultValue,
				Command:           command(toolUse["input"], cfg.MaxChars),
				ResultStatus:      resultStatus,
				Status:            ternary(errorType != "", "error", "ok"),
				ErrorType:         errorType,
				Reason:            reason,
				InputPreview:      toolInputPreview,
				OutputPreview:     toolOutputPreview,
				ExtraAttributes:   map[string]any{"timing.source": ternary(result.DurationSeconds > 0, "reported_duration", "transcript")},
			}
			if injected := raw.injectedByTool[toolID]; capture && injected != "" {
				tool.ExtraAttributes["claude.injected_context.value"] = privacy.Text(injected, cfg.MaxChars)
			}
			tool.Skill = skillUse(toolUse, result.Content, resultStatus, payload, turnNumber, cfg.MaxChars)
			turn.ToolCalls = append(turn.ToolCalls, tool)
			maxChildEnd = maxInt64(maxChildEnd, toolEnd)
			cursor = maxInt64(cursor, toolEnd)
			toolResultMessages = append(toolResultMessages, map[string]any{
				"role": "tool",
				"parts": []any{map[string]any{
					"type":     "tool_call_response",
					"id":       toolID,
					"response": resultValue,
				}},
			})
		}
		previousToolResults = toolResultMessages

		if assistantText != "" {
			outputEnd := assistantTime + int64(time.Millisecond)
			assistantOutputMessages := outputMessages(assistantText, nil, cfg.MaxChars)
			preview := ""
			if capture {
				preview = previewcore.Text(assistantText, cfg.MaxChars)
			} else {
				assistantOutputMessages = nil
			}
			turn.AssistantOutputs = append(turn.AssistantOutputs, model.AssistantOutput{
				StartUnixNano:  assistantTime,
				EndUnixNano:    outputEnd,
				OutputMessages: assistantOutputMessages,
				OutputPreview:  preview,
				OutputKind:     "text",
				Provider:       "anthropic",
				RequestModel:   modelName,
				ResponseModel:  modelName,
				Status:         ternary(apiErrorType != "", "error", "ok"),
				ErrorType:      apiErrorType,
				Reason:         apiReason,
			})
			maxChildEnd = maxInt64(maxChildEnd, outputEnd)
			finalText = assistantText
			finalOutputMessages = outputMessages(assistantText, toolUses, cfg.MaxChars)
		} else if len(toolUses) > 0 {
			finalOutputMessages = outputMessages("", toolUses, cfg.MaxChars)
		}
		cursor = maxInt64(cursor, assistantTime)
	}

	recordedEnd := int64(0)
	if raw.hasDuration {
		recordedEnd = userTime + int64(raw.durationMs*float64(time.Millisecond))
	}
	if len(turn.AssistantOutputs) > 0 && raw.durationTime > 0 {
		last := &turn.AssistantOutputs[len(turn.AssistantOutputs)-1]
		if raw.durationTime > last.StartUnixNano {
			last.EndUnixNano = maxInt64(last.EndUnixNano, raw.durationTime)
			maxChildEnd = maxInt64(maxChildEnd, last.EndUnixNano)
		}
	}
	turn.EndUnixNano = maxInt64(userTime+1, recordedEnd, raw.durationTime, lastNonErrorAssistant, maxChildEnd)
	if capture {
		if strings.TrimSpace(finalText) == "" && lastToolResult != nil {
			finalText = privacy.Text(lastToolResult, cfg.MaxChars)
			finalOutputMessages = []any{map[string]any{
				"role": "tool",
				"parts": []any{map[string]any{
					"type":     "tool_call_response",
					"response": privacy.Sanitize(lastToolResult, cfg.MaxChars),
				}},
			}}
		}
		turn.OutputMessages = finalOutputMessages
		turn.OutputPreview = previewcore.Text(finalText, cfg.MaxChars)
	}
	turn.OutputLength = len([]rune(finalText))
	return turn
}

func pendingComplete(turn *rawTurn) bool {
	if turn == nil || turn.user == nil || len(turn.assistants) == 0 {
		return false
	}
	resolved := map[string]bool{}
	for toolID := range turn.toolResults {
		resolved[toolID] = true
	}
	last := turn.assistants[len(turn.assistants)-1]
	for _, assistant := range turn.assistants {
		for _, tool := range blocks(content(assistant), "tool_use") {
			if toolID := stringValue(tool["id"]); toolID != "" && !resolved[toolID] {
				return false
			}
		}
	}
	if _, apiType, _ := apiError(last); apiType != "" {
		return false
	}
	reason := stopReason(last)
	if reason == "tool_use" {
		return false
	}
	if reason == "end_turn" || reason == "stop_sequence" || reason == "max_tokens" {
		return true
	}
	return strings.TrimSpace(extractText(content(last))) != "" && len(blocks(content(last), "tool_use")) == 0
}

func outputMessages(text string, tools []map[string]any, maxChars int) any {
	parts := make([]any, 0)
	if strings.TrimSpace(text) != "" {
		parts = append(parts, map[string]any{"type": "text", "content": privacy.Text(text, maxChars)})
	}
	for _, tool := range tools {
		part := map[string]any{
			"type": "tool_call",
			"name": stringValue(tool["name"]),
		}
		if id := stringValue(tool["id"]); id != "" {
			part["id"] = id
		}
		if tool["input"] != nil {
			part["arguments"] = privacy.Sanitize(tool["input"], maxChars)
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return nil
	}
	return []any{map[string]any{"role": "assistant", "parts": parts}}
}

func skillUse(tool map[string]any, resultContent any, resultStatus string, payload HookPayload, turnNumber int, maxChars int) *model.SkillUse {
	if stringValue(tool["name"]) != "Skill" {
		return nil
	}
	input, ok := tool["input"].(map[string]any)
	if !ok {
		return nil
	}
	name := strings.TrimSpace(stringValue(input["skill"]))
	if name == "" {
		return nil
	}
	callID := stringValue(tool["id"])
	if callID == "" {
		callID = fmt.Sprintf("%s:%d:%s", payload.SessionID, turnNumber, name)
	}
	sum := sha256.Sum256([]byte(callID + "\x00" + name))
	status := resultStatus
	if status == "unset" {
		status = "completed"
	}
	inputPreview := previewcore.Text(input, maxChars)
	if inputPreview == "" {
		inputPreview = previewcore.Text(name, maxChars)
	}
	return &model.SkillUse{
		Name:          name,
		CallID:        "skillu_" + hex.EncodeToString(sum[:8]),
		SourceType:    "product_tool",
		InputPreview:  inputPreview,
		OutputPreview: previewcore.Text(resultContent, maxChars),
		Status:        status,
	}
}

func content(message map[string]any) any {
	if nested, ok := message["message"].(map[string]any); ok {
		return nested["content"]
	}
	return message["content"]
}

func role(message map[string]any) string {
	if direct := stringValue(message["type"]); direct == "user" || direct == "assistant" {
		return direct
	}
	if nested, ok := message["message"].(map[string]any); ok {
		if value := stringValue(nested["role"]); value == "user" || value == "assistant" {
			return value
		}
	}
	return ""
}

func blocks(value any, blockType string) []map[string]any {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0)
	for _, item := range items {
		if block, ok := item.(map[string]any); ok && stringValue(block["type"]) == blockType {
			out = append(out, block)
		}
	}
	return out
}

func extractText(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	items, ok := value.([]any)
	if !ok {
		return ""
	}
	out := make([]string, 0)
	for _, item := range items {
		switch current := item.(type) {
		case string:
			out = append(out, current)
		case map[string]any:
			switch stringValue(current["type"]) {
			case "text", "input_text", "output_text":
				if text := stringValue(current["text"]); text != "" {
					out = append(out, text)
				}
			}
		}
	}
	return strings.Join(out, "\n")
}

func isToolResultMessage(message map[string]any) bool {
	return role(message) == "user" && len(blocks(content(message), "tool_result")) > 0
}

func messageID(message map[string]any) string {
	if nested, ok := message["message"].(map[string]any); ok {
		if id := stringValue(nested["id"]); id != "" {
			return id
		}
	}
	return stringValue(message["uuid"])
}

func modelName(message map[string]any) string {
	if nested, ok := message["message"].(map[string]any); ok {
		if value := stringValue(nested["model"]); value != "" {
			return value
		}
	}
	return firstNonEmpty(stringValue(message["model"]), "claude")
}

func stopReason(message map[string]any) string {
	if nested, ok := message["message"].(map[string]any); ok {
		if value := stringValue(nested["stop_reason"]); value != "" {
			return value
		}
	}
	return stringValue(message["stop_reason"])
}

func usage(message map[string]any) model.Usage {
	var values map[string]any
	if nested, ok := message["message"].(map[string]any); ok {
		values, _ = nested["usage"].(map[string]any)
	}
	if values == nil {
		values, _ = message["usage"].(map[string]any)
	}
	input, _ := number(values["input_tokens"])
	output, _ := number(values["output_tokens"])
	cacheRead, _ := number(values["cache_read_input_tokens"])
	cacheCreate, _ := number(values["cache_creation_input_tokens"])
	return model.Usage{
		InputTokens:       int64(input + cacheRead + cacheCreate),
		OutputTokens:      int64(output),
		CacheReadTokens:   int64(cacheRead),
		CacheCreateTokens: int64(cacheCreate),
	}
}

func mergeUsage(left, right model.Usage) model.Usage {
	left.InputTokens += right.InputTokens
	left.OutputTokens += right.OutputTokens
	left.CacheReadTokens += right.CacheReadTokens
	left.CacheCreateTokens += right.CacheCreateTokens
	left.ReasoningTokens += right.ReasoningTokens
	return left
}

func mergeAssistant(existing, incoming map[string]any) map[string]any {
	out := copyMap(existing)
	existingTime := timestamp(existing)
	incomingTime := timestamp(incoming)
	if incomingTime > 0 && (existingTime == 0 || incomingTime < existingTime) {
		out["timestamp"] = incoming["timestamp"]
	}
	for key, value := range incoming {
		if key == "timestamp" || value == nil || value == "" {
			continue
		}
		if key != "message" {
			out[key] = value
			continue
		}
		incomingMessage, ok := value.(map[string]any)
		if !ok {
			continue
		}
		currentMessage, _ := out["message"].(map[string]any)
		currentMessage = copyMap(currentMessage)
		for nestedKey, nestedValue := range incomingMessage {
			if nestedKey == "content" {
				currentMessage[nestedKey] = mergeContent(currentMessage[nestedKey], nestedValue)
			} else if nestedValue != nil && nestedValue != "" {
				currentMessage[nestedKey] = nestedValue
			}
		}
		out["message"] = currentMessage
	}
	return out
}

func mergeContent(existing, incoming any) any {
	left, leftOK := existing.([]any)
	right, rightOK := incoming.([]any)
	if !leftOK || !rightOK {
		if incoming != nil && incoming != "" {
			return incoming
		}
		return existing
	}
	out := make([]any, 0, len(left)+len(right))
	seen := map[string]bool{}
	for _, value := range append(left, right...) {
		body, _ := json.Marshal(value)
		key := string(body)
		if object, ok := value.(map[string]any); ok {
			if stringValue(object["type"]) == "tool_use" {
				key = "tool_use:" + stringValue(object["id"])
			}
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, value)
		}
	}
	return out
}

func toolDuration(message map[string]any) float64 {
	result, _ := message["toolUseResult"].(map[string]any)
	if value, ok := number(result["durationSeconds"]); ok && value >= 0 {
		return value
	}
	if value, ok := number(result["durationMs"]); ok && value >= 0 {
		return value / 1000
	}
	return -1
}

func command(value any, maxChars int) string {
	input, ok := value.(map[string]any)
	if !ok {
		return ""
	}
	current := input["cmd"]
	if current == nil {
		current = input["command"]
	}
	if list, ok := current.([]any); ok {
		parts := make([]string, 0, len(list))
		for _, item := range list {
			parts = append(parts, fmt.Sprint(item))
		}
		return previewcore.Text(strings.Join(parts, " "), maxChars)
	}
	return previewcore.Text(current, maxChars)
}

func apiError(message map[string]any) (int, string, string) {
	text := extractText(content(message))
	status := int(numberOrZero(message["apiErrorStatus"]))
	errorType := ""
	if marker := strings.Index(text, "API Error:"); marker >= 0 {
		var parsedStatus int
		var parsedType string
		if _, err := fmt.Sscanf(text[marker:], "API Error: %d %s", &parsedStatus, &parsedType); err == nil {
			status = parsedStatus
			errorType = strings.TrimSuffix(parsedType, ":")
		}
	}
	if errorType == "" && boolValue(message["isApiErrorMessage"]) {
		errorType = "api_error"
	}
	return status, errorType, text
}

func timestamp(value map[string]any) int64 {
	text := stringValue(value["timestamp"])
	if text == "" {
		return 0
	}
	parsed, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		return 0
	}
	return parsed.UnixNano()
}

func number(value any) (float64, bool) {
	switch current := value.(type) {
	case float64:
		return current, true
	case float32:
		return float64(current), true
	case int:
		return float64(current), true
	case int64:
		return float64(current), true
	case json.Number:
		parsed, err := current.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}

func numberOrZero(value any) float64 {
	result, _ := number(value)
	return result
}

func stringValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}

func boolValue(value any) bool {
	result, _ := value.(bool)
	return result
}

func copyMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func ternary(condition bool, truthy, falsy string) string {
	if condition {
		return truthy
	}
	return falsy
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0)
	for _, value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func maxInt64(values ...int64) int64 {
	var maximum int64
	for _, value := range values {
		if value > maximum {
			maximum = value
		}
	}
	return maximum
}

func SortedMessageKeys(value map[string]any) []string {
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
