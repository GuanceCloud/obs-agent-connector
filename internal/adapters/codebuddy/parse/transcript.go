package parse

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Both storage formats feed the same turn builder, privacy filters and upload state.
// JSONL records are normalized in memory; the user's transcript is never rewritten.
func readTranscript(input HookInput) (conversationIndex, func(string) (storedMessage, error), error) {
	var index conversationIndex
	if filepath.Base(input.TranscriptPath) == "index.json" {
		body, err := os.ReadFile(input.TranscriptPath)
		if err != nil {
			return index, nil, err
		}
		if err := json.Unmarshal(body, &index); err != nil {
			return index, nil, fmt.Errorf("parse CodeBuddy index.json: %w", err)
		}
		return index, func(id string) (storedMessage, error) {
			return readMessage(filepath.Join(filepath.Dir(input.TranscriptPath), "messages", id+".json"))
		}, nil
	}
	if filepath.Ext(input.TranscriptPath) != ".jsonl" {
		return index, nil, fmt.Errorf("unsupported CodeBuddy transcript %q: expected index.json or .jsonl", filepath.Base(input.TranscriptPath))
	}
	return readJSONL(input)
}

type jsonlEvent struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"sessionId"`
	Timestamp    any             `json:"timestamp"`
	Type         string          `json:"type"`
	Role         string          `json:"role"`
	Status       string          `json:"status"`
	Content      []contentPart   `json:"content"`
	CallID       string          `json:"callId"`
	Name         string          `json:"name"`
	Arguments    json.RawMessage `json:"arguments"`
	Output       json.RawMessage `json:"output"`
	ProviderData struct {
		RequestID string         `json:"conversationRequestId"`
		MessageID string         `json:"messageId"`
		Agent     string         `json:"agent"`
		RawUsage  map[string]any `json:"rawUsage"`
	} `json:"providerData"`
}

func readJSONL(input HookInput) (conversationIndex, func(string) (storedMessage, error), error) {
	var index conversationIndex
	file, err := os.Open(input.TranscriptPath)
	if err != nil {
		return index, nil, err
	}
	defer file.Close()
	reader := bufio.NewReader(file)
	var events []jsonlEvent
	positions := map[string]int{}
	for line := 1; ; line++ {
		body, readErr := reader.ReadBytes('\n')
		if readErr != nil && readErr != io.EOF {
			return index, nil, readErr
		}
		if len(bytes.TrimSpace(body)) > 0 {
			var envelope struct {
				Type string `json:"type"`
			}
			// Fail the snapshot on partial writes too: the worker retries it. Do not put
			// raw records or JSON decoder errors (which can contain content) in logs.
			if !json.Valid(body) || json.Unmarshal(body, &envelope) != nil {
				return index, nil, fmt.Errorf("parse CodeBuddy JSONL line %d: invalid record", line)
			}
			switch envelope.Type {
			case "message", "function_call", "function_call_result":
				var event jsonlEvent
				decoder := json.NewDecoder(bytes.NewReader(body))
				decoder.UseNumber()
				if decoder.Decode(&event) != nil {
					return index, nil, fmt.Errorf("parse CodeBuddy JSONL line %d: invalid message record", line)
				}
				if !safeID(event.ID) || event.ProviderData.RequestID == "" || parseTime(event.Timestamp) == 0 {
					return index, nil, fmt.Errorf("parse CodeBuddy JSONL line %d: missing or invalid message identity, request identity or timestamp", line)
				}
				if event.SessionID != "" && event.SessionID != input.SessionID {
					return index, nil, fmt.Errorf("parse CodeBuddy JSONL line %d: session mismatch", line)
				}
				// A later record with the same ID is an updated snapshot, not another message.
				if position, ok := positions[event.ID]; ok {
					events[position] = event
				} else {
					positions[event.ID] = len(events)
					events = append(events, event)
				}
			}
		}
		if readErr == io.EOF {
			break
		}
	}
	messages := map[string]storedMessage{}
	requests := map[string]int{}
	usageByRequest := map[string]map[string]map[string]any{}
	for _, event := range events {
		id := event.ProviderData.RequestID
		position, ok := requests[id]
		if !ok {
			position = len(index.Requests)
			requests[id] = position
			index.Requests = append(index.Requests, requestIndex{ID: id, Type: firstNonEmpty(event.ProviderData.Agent, "cli"), StartedAt: event.Timestamp})
			usageByRequest[id] = map[string]map[string]any{}
		}
		request := &index.Requests[position]
		if parseTime(event.Timestamp) < parseTime(request.StartedAt) {
			request.StartedAt = event.Timestamp
		}
		role := event.Role
		complete := true
		var parts []contentPart
		switch event.Type {
		case "message":
			for _, part := range event.Content {
				if part.Type == "input_text" || part.Type == "output_text" || part.Type == "text" {
					parts = append(parts, contentPart{Type: "text", Text: part.Text})
				}
			}
			if role == "assistant" {
				request.State = normalizedState(event.Status)
				complete = request.State != "unset"
			}
		case "function_call":
			if event.CallID == "" || event.Name == "" {
				return index, nil, fmt.Errorf("parse CodeBuddy JSONL: tool call is missing identity or name")
			}
			role = "assistant"
			args := event.Arguments
			if len(args) > 0 && args[0] == '"' {
				var text string
				if json.Unmarshal(args, &text) != nil || !json.Valid([]byte(text)) {
					return index, nil, fmt.Errorf("parse CodeBuddy JSONL: invalid tool arguments")
				}
				args = json.RawMessage(text)
			}
			parts = []contentPart{{Type: "tool-call", ToolCallID: event.CallID, ToolName: event.Name, Args: args}}
			request.State = "unset"
		case "function_call_result":
			if event.CallID == "" {
				return index, nil, fmt.Errorf("parse CodeBuddy JSONL: tool result is missing identity")
			}
			role = "tool"
			complete = false
			// Non-terminal results must not make a pending call eligible for upload.
			switch event.Status {
			case "completed", "success", "failed", "error", "cancelled", "canceled":
				complete = true
				parts = []contentPart{{Type: "tool-result", ToolCallID: event.CallID, ToolName: event.Name, Result: event.Output, IsError: event.Status != "completed" && event.Status != "success"}}
			}
		}
		if role == "assistant" && len(event.ProviderData.RawUsage) > 0 {
			if event.ProviderData.MessageID == "" {
				return index, nil, fmt.Errorf("parse CodeBuddy JSONL: usage is missing provider message identity")
			}
			// Parallel function calls can repeat one provider response's usage. The last
			// snapshot wins; sum once per provider message within each user request.
			usageByRequest[id][event.ProviderData.MessageID] = event.ProviderData.RawUsage
		}
		body, err := json.Marshal(messageBody{Role: role, Content: parts})
		if err != nil {
			return index, nil, err
		}
		messages[event.ID] = storedMessage{Role: role, CreatedAt: event.Timestamp, Message: body}
		request.Messages = append(request.Messages, event.ID)
		index.Messages = append(index.Messages, messageIndex{ID: event.ID, IsComplete: complete})
	}
	for i := range index.Requests {
		request := &index.Requests[i]
		var inputTokens, outputTokens, cacheTokens, reasoningTokens int64
		for _, usage := range usageByRequest[request.ID] {
			inputTokens += usageNumber(usage, "prompt_tokens", "input_tokens")
			outputTokens += usageNumber(usage, "completion_tokens", "output_tokens")
			promptDetails, _ := usage["prompt_tokens_details"].(map[string]any)
			completionDetails, _ := usage["completion_tokens_details"].(map[string]any)
			cacheTokens += usageNumber(promptDetails, "cached_tokens")
			reasoningTokens += usageNumber(completionDetails, "reasoning_tokens")
		}
		request.Usage = map[string]any{"inputTokens": fmt.Sprint(inputTokens), "outputTokens": fmt.Sprint(outputTokens), "cacheTokens": fmt.Sprint(cacheTokens), "reasoningTokens": fmt.Sprint(reasoningTokens)}
	}
	return index, func(id string) (storedMessage, error) {
		value, ok := messages[id]
		if !ok {
			return storedMessage{}, os.ErrNotExist
		}
		return value, nil
	}, nil
}
