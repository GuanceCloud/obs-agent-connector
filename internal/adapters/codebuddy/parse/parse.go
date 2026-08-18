package parse

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	codebuddyconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/codebuddy/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	previewcore "github.com/GuanceCloud/obs-agent-connector/internal/core/preview"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

type HookInput struct {
	Event          string `json:"hook_event_name"`
	SessionID      string `json:"session_id"`
	TranscriptPath string `json:"transcript_path"`
	Cwd            string `json:"cwd"`
	GenerationID   string `json:"generation_id"`
	Model          string `json:"model"`
	Client         string `json:"client"`
	Version        string `json:"version"`
	Reason         string `json:"reason"`
	LoopCount      int    `json:"loop_count"`
}

type Diagnostics struct {
	TotalRequests      int            `json:"total_requests"`
	MatchedRequests    int            `json:"matched_requests"`
	IndexedMessages    int            `json:"indexed_messages"`
	IncompleteMessages int            `json:"incomplete_messages"`
	TerminalTurns      int            `json:"terminal_turns"`
	RequestStates      map[string]int `json:"request_states"`
}

type conversationIndex struct {
	Messages []messageIndex `json:"messages"`
	Requests []requestIndex `json:"requests"`
}

type messageIndex struct {
	ID         string `json:"id"`
	IsComplete bool   `json:"isComplete"`
}

type requestIndex struct {
	ID        string         `json:"id"`
	Type      string         `json:"type"`
	Messages  []string       `json:"messages"`
	State     string         `json:"state"`
	Usage     map[string]any `json:"usage"`
	StartedAt any            `json:"startedAt"`
}

type storedMessage struct {
	Role       string          `json:"role"`
	Message    json.RawMessage `json:"message"`
	CreatedAt  any             `json:"createdAt"`
	CreateTime any             `json:"createTime"`
}

type messageBody struct {
	Role    string        `json:"role"`
	Content []contentPart `json:"content"`
}

type contentPart struct {
	Type       string          `json:"type"`
	Text       string          `json:"text"`
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
	Result     json.RawMessage `json:"result"`
	IsError    bool            `json:"isError"`
}

type rawTool struct {
	ID, Name      string
	Arguments     any
	Result        any
	IsError       bool
	HasResult     bool
	StartUnixNano int64
	EndUnixNano   int64
}

func Read(input HookInput, cfg codebuddyconfig.Config) ([]model.Turn, bool, Diagnostics, error) {
	if strings.TrimSpace(input.SessionID) == "" || strings.TrimSpace(input.TranscriptPath) == "" {
		return nil, false, Diagnostics{}, errors.New("hook payload is missing session_id or transcript_path")
	}
	if filepath.Base(input.TranscriptPath) != "index.json" {
		return nil, false, Diagnostics{}, fmt.Errorf("unsupported CodeBuddy transcript %q: expected index.json", filepath.Base(input.TranscriptPath))
	}
	body, err := os.ReadFile(input.TranscriptPath)
	if err != nil {
		return nil, true, Diagnostics{}, err
	}
	var index conversationIndex
	if err := json.Unmarshal(body, &index); err != nil {
		return nil, true, Diagnostics{}, fmt.Errorf("parse CodeBuddy index.json: %w", err)
	}
	diagnostics := Diagnostics{TotalRequests: len(index.Requests), IndexedMessages: len(index.Messages), RequestStates: map[string]int{}}
	messageSet := make(map[string]struct{}, len(index.Messages))
	for _, item := range index.Messages {
		messageSet[item.ID] = struct{}{}
		if !item.IsComplete {
			diagnostics.IncompleteMessages++
		}
	}
	selected := make([]requestIndex, 0, len(index.Requests))
	for _, request := range index.Requests {
		state := normalizedState(request.State)
		diagnostics.RequestStates[state]++
		if input.Event == "Stop" && request.ID == input.GenerationID {
			selected = append(selected, request)
		}
		if input.Event == "SessionEnd" {
			selected = append(selected, request)
		}
	}
	diagnostics.MatchedRequests = len(selected)
	if input.Event == "Stop" && len(selected) == 0 {
		return nil, true, diagnostics, nil
	}

	turns := make([]model.Turn, 0, len(selected))
	pending := false
	for index, request := range selected {
		status := normalizedState(request.State)
		if status == "unset" {
			if input.Event == "SessionEnd" && index == len(selected)-1 {
				status = "cancelled"
			} else {
				pending = pending || input.Event == "Stop"
				continue
			}
		}
		turn, complete, err := buildTurn(input, request, messageSet, cfg, status)
		if err != nil {
			return nil, true, diagnostics, err
		}
		if !complete {
			pending = pending || input.Event == "Stop"
			continue
		}
		turns = append(turns, turn)
	}
	diagnostics.TerminalTurns = len(turns)
	return turns, pending, diagnostics, nil
}

func buildTurn(input HookInput, request requestIndex, messageSet map[string]struct{}, cfg codebuddyconfig.Config, status string) (model.Turn, bool, error) {
	start := parseTime(request.StartedAt)
	end := int64(0)
	var userText, assistantText string
	tools := make([]rawTool, 0)
	toolByID := map[string]int{}
	baseDir := filepath.Dir(input.TranscriptPath)
	messagesDir := filepath.Join(baseDir, "messages")
	for _, messageID := range request.Messages {
		if _, ok := messageSet[messageID]; !ok || !safeID(messageID) {
			return model.Turn{}, false, nil
		}
		path := filepath.Join(messagesDir, messageID+".json")
		stored, err := readMessage(path)
		if err != nil {
			if os.IsNotExist(err) {
				return model.Turn{}, false, nil
			}
			return model.Turn{}, false, err
		}
		at := parseTime(stored.CreatedAt)
		if at == 0 {
			at = parseTime(stored.CreateTime)
		}
		if start == 0 || (at > 0 && at < start) {
			start = at
		}
		if at > end {
			end = at
		}
		decoded, err := decodeBody(stored.Message)
		if err != nil {
			return model.Turn{}, false, fmt.Errorf("parse message %s: %w", messageID, err)
		}
		role := firstNonEmpty(stored.Role, decoded.Role)
		for _, part := range decoded.Content {
			switch part.Type {
			case "text":
				if role == "user" {
					userText += part.Text
				} else if role == "assistant" {
					assistantText += part.Text
				}
			case "tool-call":
				arguments := decodeAny(part.Args)
				toolByID[part.ToolCallID] = len(tools)
				tools = append(tools, rawTool{ID: part.ToolCallID, Name: part.ToolName, Arguments: arguments, StartUnixNano: at, EndUnixNano: at})
			case "tool-result":
				result := decodeAny(part.Result)
				if position, ok := toolByID[part.ToolCallID]; ok {
					tools[position].Result = result
					tools[position].IsError = part.IsError
					tools[position].HasResult = true
					tools[position].EndUnixNano = at
				} else {
					tools = append(tools, rawTool{ID: part.ToolCallID, Name: part.ToolName, Result: result, IsError: part.IsError, HasResult: true, StartUnixNano: at, EndUnixNano: at})
				}
			}
		}
	}
	usage := model.Usage{
		InputTokens:     usageNumber(request.Usage, "inputTokens", "promptTokens", "input_tokens", "prompt_tokens"),
		OutputTokens:    usageNumber(request.Usage, "outputTokens", "completionTokens", "output_tokens", "completion_tokens"),
		CacheReadTokens: usageNumber(request.Usage, "cacheTokens", "cachedInputTokens", "cacheReadInputTokens", "cached_input_tokens"),
		ReasoningTokens: usageNumber(request.Usage, "reasoningTokens", "reasoning_tokens"),
	}
	if strings.TrimSpace(userText) == "" && strings.TrimSpace(assistantText) == "" && len(tools) == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return model.Turn{}, false, nil
	}
	for _, tool := range tools {
		if !tool.HasResult {
			return model.Turn{}, false, nil
		}
	}
	if start == 0 {
		start = time.Now().UnixNano()
	}
	if end <= start {
		end = start + 1
	}
	capture := cfg.CaptureContent != "none"
	inputMessages, outputMessages, inputPreview, outputPreview := content(userText, assistantText, cfg, status)
	if !capture {
		inputMessages, outputMessages, inputPreview, outputPreview = nil, nil, "", ""
	}
	resource := copyMap(cfg.ResourceAttributes)
	resource["agent_runtime"] = "codebuddy"
	resource["telemetry.sdk.language"] = "go"
	resource["telemetry.sdk.name"] = "gtrace"
	if input.Version != "" {
		resource["agent_version"] = input.Version
	}
	finalStatus := model.FinalStatusCompleted
	finishReason := "stop"
	if status == "cancelled" {
		finalStatus = model.FinalStatusCancelled
		finishReason = "cancelled"
	}
	turn := model.Turn{
		SessionID: input.SessionID, TurnID: request.ID, AgentRuntime: "codebuddy", AgentName: "codebuddy", AgentVersion: input.Version,
		StartUnixNano: start, EndUnixNano: end, FinalStatus: finalStatus, InputMessages: inputMessages, OutputMessages: outputMessages,
		InputPreview: inputPreview, OutputPreview: outputPreview, InputLength: len([]rune(userText)), OutputLength: len([]rune(assistantText)),
		Usage: usage, Resource: resource,
		ExtraAttributes: map[string]any{"request_type": firstNonEmpty(request.Type, "user_request"), "timing.source": "inferred"},
	}
	llmOutputKind := "text"
	if len(tools) > 0 {
		llmOutputKind = "tool_call"
	}
	turn.LLMCalls = append(turn.LLMCalls, model.LLMCall{
		CallID: request.ID, StartUnixNano: start, EndUnixNano: end, RequestModel: input.Model, ResponseModel: input.Model,
		InputMessages: inputMessages, OutputMessages: outputMessages, InputPreview: inputPreview, OutputPreview: outputPreview,
		OutputKind: llmOutputKind, FinishReasons: []string{finishReason}, Usage: usage, Status: "info",
		ExtraAttributes: map[string]any{"timing.source": "inferred"},
	})
	for _, raw := range tools {
		tool := model.ToolCall{CallID: raw.ID, TriggeringLLMCall: request.ID, Name: firstNonEmpty(raw.Name, "unknown"), StartUnixNano: raw.StartUnixNano, EndUnixNano: raw.EndUnixNano, Status: "info", ResultStatus: "completed", ExtraAttributes: map[string]any{"timing.source": "inferred"}}
		if tool.StartUnixNano == 0 {
			tool.StartUnixNano = start
		}
		if tool.EndUnixNano <= tool.StartUnixNano {
			tool.EndUnixNano = tool.StartUnixNano + 1
		}
		if capture {
			tool.Arguments = privacy.Sanitize(raw.Arguments, cfg.MaxChars)
			tool.Result = privacy.Sanitize(raw.Result, cfg.MaxChars)
			tool.InputPreview = previewcore.Text(raw.Arguments, cfg.MaxChars)
			tool.OutputPreview = previewcore.Text(raw.Result, cfg.MaxChars)
			tool.Command = command(raw.Arguments, cfg.MaxChars)
		}
		if raw.IsError {
			tool.Status, tool.ResultStatus, tool.ErrorType = "error", "error", "_OTHER"
		}
		turn.ToolCalls = append(turn.ToolCalls, tool)
	}
	if strings.TrimSpace(assistantText) != "" {
		turn.AssistantOutputs = append(turn.AssistantOutputs, model.AssistantOutput{
			StartUnixNano: end - 1, EndUnixNano: end, OutputMessages: outputMessages, OutputPreview: outputPreview,
			OutputKind: "text", RequestModel: input.Model, ResponseModel: input.Model, Status: "info",
			ExtraAttributes: map[string]any{"timing.source": "inferred"},
		})
	}
	return turn, true, nil
}

func content(userText, assistantText string, cfg codebuddyconfig.Config, status string) (any, any, string, string) {
	input := []any{map[string]any{"role": "user", "parts": []any{map[string]any{"type": "text", "content": privacy.Text(userText, cfg.MaxChars)}}}}
	output := []any{map[string]any{"role": "assistant", "finish_reason": map[string]string{"completed": "stop", "cancelled": "cancelled"}[status], "parts": []any{map[string]any{"type": "text", "content": privacy.Text(assistantText, cfg.MaxChars)}}}}
	inputPreview, outputPreview := previewcore.Pair(userText, assistantText, cfg.MaxChars)
	return input, output, inputPreview, outputPreview
}

func readMessage(path string) (storedMessage, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return storedMessage{}, err
	}
	var value storedMessage
	err = json.Unmarshal(body, &value)
	return value, err
}

func decodeBody(raw json.RawMessage) (messageBody, error) {
	if len(raw) == 0 {
		return messageBody{}, errors.New("empty message")
	}
	if raw[0] == '"' {
		var encoded string
		if err := json.Unmarshal(raw, &encoded); err != nil {
			return messageBody{}, err
		}
		raw = []byte(encoded)
	}
	var value messageBody
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	err := decoder.Decode(&value)
	return value, err
}

func decodeAny(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil
	}
	return value
}

func normalizedState(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "complete", "completed", "success", "succeeded":
		return "completed"
	case "cancelled", "canceled", "aborted", "interrupted":
		return "cancelled"
	default:
		return "unset"
	}
}

func parseTime(value any) int64 {
	switch current := value.(type) {
	case float64:
		if current > 1e15 {
			return int64(current)
		}
		if current > 1e12 {
			return int64(current) * int64(time.Millisecond)
		}
		return int64(current) * int64(time.Second)
	case json.Number:
		number, _ := current.Int64()
		return parseTime(float64(number))
	case string:
		if number, err := strconv.ParseInt(current, 10, 64); err == nil {
			return parseTime(float64(number))
		}
		if parsed, err := time.Parse(time.RFC3339Nano, current); err == nil {
			return parsed.UnixNano()
		}
	}
	return 0
}

func usageNumber(usage map[string]any, names ...string) int64 {
	for _, name := range names {
		if value, ok := usage[name]; ok {
			number, err := strconv.ParseInt(strings.TrimSpace(toString(value)), 10, 64)
			if err == nil {
				return number
			}
		}
	}
	return 0
}

func toString(value any) string {
	switch current := value.(type) {
	case string:
		return current
	case float64:
		return strconv.FormatInt(int64(current), 10)
	case json.Number:
		return current.String()
	}
	return ""
}

func command(arguments any, maxChars int) string {
	if values, ok := arguments.(map[string]any); ok {
		for _, key := range []string{"cmd", "command"} {
			if value, exists := values[key]; exists {
				return privacy.Text(value, maxChars)
			}
		}
	}
	return ""
}

func safeID(value string) bool {
	return value != "" && filepath.Base(value) == value && value != "." && value != ".."
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
func copyMap(value map[string]any) map[string]any {
	out := map[string]any{}
	for key, item := range value {
		out[key] = item
	}
	return out
}
func Fingerprint(turn model.Turn) string {
	body, _ := json.Marshal(turn)
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}
