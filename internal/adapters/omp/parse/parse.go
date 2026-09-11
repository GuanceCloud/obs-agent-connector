package parse

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

type Parent struct {
	SessionID  string `json:"session_id"`
	TurnID     string `json:"turn_id"`
	ToolCallID string `json:"tool_call_id"`
}

type Snapshot struct {
	TraceID        string  `json:"trace_id"`
	Parent         *Parent `json:"parent"`
	TerminalStatus string  `json:"terminal_status"`
	Schema         int     `json:"schema"`
	SessionID      string  `json:"session_id"`
	TurnID         string  `json:"turn_id"`
	Start          int64   `json:"started_at"`
	End            int64   `json:"ended_at"`
	Events         []Event `json:"events"`
}

type Event struct {
	Type     string         `json:"type"`
	At       int64          `json:"at"`
	Message  Message        `json:"message"`
	Messages []Message      `json:"messages"`
	CallID   string         `json:"call_id"`
	Name     string         `json:"name"`
	Args     any            `json:"args"`
	Result   any            `json:"result"`
	Skill    *SkillIdentity `json:"skill"`
	IsError  bool           `json:"is_error"`
}

type SkillIdentity struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Source string `json:"source"`
}

type Message struct {
	TextLength    *int    `json:"text_length"`
	Role          string  `json:"role"`
	Content       any     `json:"content"`
	Timestamp     int64   `json:"timestamp"`
	CompletedAt   int64   `json:"completedAt"`
	Duration      float64 `json:"duration"`
	TTFT          float64 `json:"ttft"`
	Provider      string  `json:"provider"`
	Model         string  `json:"model"`
	ResponseID    string  `json:"responseId"`
	StopReason    string  `json:"stopReason"`
	ErrorMessage  string  `json:"errorMessage"`
	Synthetic     bool    `json:"synthetic"`
	Attribution   string  `json:"attribution"`
	UserInitiated bool    `json:"userInitiated"`
	HasContent    bool    `json:"has_content"`
	HasText       bool    `json:"has_text"`
	ToolCallID    string  `json:"toolCallId"`
	Usage         struct {
		Input      int64 `json:"input"`
		Output     int64 `json:"output"`
		CacheRead  int64 `json:"cacheRead"`
		CacheWrite int64 `json:"cacheWrite"`
	} `json:"usage"`
}

// Normalize maps OMP's per-model turns into one user-request Turn. Only the
// extension's terminal agent_end snapshot reaches this function.
func Normalize(s Snapshot, cfg config.Config) (model.Turn, error) {
	if s.Schema != 1 || s.SessionID == "" || s.TurnID == "" || s.Start <= 0 || s.End < s.Start || s.End > time.Now().Add(24*time.Hour).UnixMilli() {
		return model.Turn{}, fmt.Errorf("invalid OMP terminal snapshot")
	}
	if s.Parent != nil {
		decoded, err := hex.DecodeString(s.TraceID)
		if err != nil || len(decoded) != 16 || s.TraceID == "00000000000000000000000000000000" {
			return model.Turn{}, fmt.Errorf("invalid OMP child trace ID")
		}
		if s.Parent.SessionID == "" || s.Parent.TurnID == "" || s.Parent.ToolCallID == "" || (s.TerminalStatus != "completed" && s.TerminalStatus != "failed" && s.TerminalStatus != "aborted") {
			return model.Turn{}, fmt.Errorf("invalid OMP child relationship or terminal state")
		}
	}
	end := max(s.End, s.Start+1)
	t := model.Turn{SessionID: s.SessionID, TurnID: s.TurnID, AgentRuntime: "omp", AgentName: "omp", StartUnixNano: s.Start * 1e6, EndUnixNano: end * 1e6, FinalStatus: model.FinalStatusCompleted, Resource: map[string]any{}, ExtraAttributes: map[string]any{"run_id": s.TurnID}}
	for k, v := range cfg.Resource {
		t.Resource[k] = v
	}
	if name, ok := cfg.Resource["agent_name"].(string); ok {
		t.AgentName = name
	}
	if version, ok := cfg.Resource["agent_version"].(string); ok {
		t.AgentVersion = version
	}
	capture := func(value any) any {
		if cfg.CaptureContent == "none" || value == nil {
			return nil
		}
		return privacy.Sanitize(value, cfg.MaxChars)
	}
	preview := func(value any) string {
		if value == nil || cfg.CaptureContent == "none" {
			return ""
		}
		return privacy.Preview(value, cfg.MaxChars)
	}
	bounds := func(start, finish int64) (int64, int64) {
		start = max(s.Start, min(start, end-1))
		finish = max(start+1, min(finish, end))
		return start * 1e6, finish * 1e6
	}
	hasUser := false
	llmStart := s.Start
	tools := map[string]*model.ToolCall{}
	toolOrder := []string{}
	trigger := map[string]string{}
	seen := map[string]bool{}
	var last Message
	var requestMessages []any
	requestLength := 0
	for _, e := range s.Events {
		switch e.Type {
		case "context":
			requestMessages = nil
			requestLength = 0
			for _, m := range e.Messages {
				requestLength += messageLength(m)
				if value := messageValue(m, false, cfg); value != nil {
					requestMessages = append(requestMessages, value)
				}
			}
		case "input":
			m := e.Message
			user := (m.Role == "user" && !m.Synthetic && m.Attribution != "agent") || (m.Role == "developer" && m.UserInitiated) || (m.Role == "custom" && m.Attribution == "user")
			if s.Parent != nil && m.Role == "user" {
				user = true
			}
			if user && (m.HasContent || textContent(m.Content) != "") {
				hasUser = true
				text := textContent(m.Content)
				if t.InputMessages == nil && t.InputLength == 0 {
					t.InputMessages = messageList(m, false, cfg)
					t.InputPreview = preview(text)
					t.InputLength = messageLength(m)
				}
			}
		case "turn_start":
			llmStart = e.At
		case "assistant":
			m := e.Message
			key := fmt.Sprintf("%d:%s:%s", m.Timestamp, m.ResponseID, m.StopReason)
			if seen[key] {
				continue
			}
			seen[key] = true
			last = m
			start := m.Timestamp
			if start <= 0 {
				start = llmStart
			}
			finish := e.At
			if m.Duration > 0 {
				finish = start + int64(m.Duration)
			} else if m.CompletedAt > start {
				finish = m.CompletedAt
			}
			a, b := bounds(start, finish)
			usage := model.Usage{InputTokens: max(0, m.Usage.Input) + max(0, m.Usage.CacheRead) + max(0, m.Usage.CacheWrite), OutputTokens: max(0, m.Usage.Output), CacheReadTokens: max(0, m.Usage.CacheRead), CacheCreateTokens: max(0, m.Usage.CacheWrite)}
			callID := fmt.Sprintf("llm-%d", len(t.LLMCalls))
			call := model.LLMCall{CallID: callID, StartUnixNano: a, EndUnixNano: b, Provider: m.Provider, RequestModel: m.Model, ResponseModel: m.Model, OutputMessages: messageList(m, true, cfg), OutputPreview: preview(textContent(m.Content)), Usage: usage, TTFTMs: max(0, m.TTFT), FinishReasons: []string{finishReason(m.StopReason)}, Status: "ok"}
			call.ExtraAttributes = map[string]any{"output_length": messageLength(m)}
			call.OutputKind = outputKind(m)
			if m.HasText || textContent(m.Content) != "" {
				call.ExtraAttributes["gen_ai.output.type"] = "text"
			}
			if requestLength > 0 {
				call.ExtraAttributes["input_length"] = requestLength
			} else if len(t.LLMCalls) == 0 {
				call.ExtraAttributes["input_length"] = t.InputLength
			}
			if len(requestMessages) > 0 {
				call.InputMessages = requestMessages
				call.InputPreview = preview(requestMessages)
			} else if len(t.LLMCalls) == 0 {
				call.InputMessages = t.InputMessages
				call.InputPreview = t.InputPreview
			}
			if m.StopReason == "error" || m.StopReason == "aborted" {
				call.Status = "error"
				call.ErrorType = "omp." + m.StopReason
				call.Reason = preview(m.ErrorMessage)
			}
			t.LLMCalls = append(t.LLMCalls, call)
			t.Usage.InputTokens += usage.InputTokens
			t.Usage.OutputTokens += usage.OutputTokens
			t.Usage.CacheReadTokens += usage.CacheReadTokens
			t.Usage.CacheCreateTokens += usage.CacheCreateTokens
			parts, _ := m.Content.([]any)
			for _, part := range parts {
				item, _ := part.(map[string]any)
				if item["type"] == "toolCall" {
					id, _ := item["id"].(string)
					trigger[id] = callID
				}
			}
			text := textContent(m.Content)
			t.OutputMessages, t.OutputPreview, t.OutputLength = nil, "", 0
			delete(t.ExtraAttributes, "gen_ai.output.type")
			if m.HasText || text != "" {
				x, y := bounds(e.At-1, e.At)
				t.AssistantOutputs = append(t.AssistantOutputs, model.AssistantOutput{StartUnixNano: x, EndUnixNano: y, OutputMessages: messageList(Message{Role: "assistant", Content: text, StopReason: m.StopReason}, true, cfg), OutputPreview: preview(text), OutputKind: "text", Provider: m.Provider, RequestModel: m.Model, ResponseModel: m.Model, Status: call.Status, ErrorType: call.ErrorType, ExtraAttributes: map[string]any{"output_length": messageLength(m), "gen_ai.output.type": "text"}})
				t.OutputMessages = messageList(Message{Role: "assistant", Content: text, StopReason: m.StopReason}, true, cfg)
				t.OutputPreview = preview(text)
				t.OutputLength = messageLength(m)
				t.ExtraAttributes["gen_ai.output.type"] = "text"
			}
		case "tool_start":
			if e.CallID == "" || e.Name == "" || tools[e.CallID] != nil {
				continue
			}
			a, _ := bounds(e.At, e.At+1)
			tool := &model.ToolCall{CallID: e.CallID, Name: e.Name, StartUnixNano: a, Arguments: capture(e.Args), InputPreview: preview(e.Args), TriggeringLLMCall: trigger[e.CallID]}
			if args, ok := e.Args.(map[string]any); ok {
				if command, ok := args["command"].(string); ok {
					tool.Command = preview(command)
				}
				if path, ok := args["path"].(string); ok && filepath.Base(path) == "SKILL.md" && e.Name == "read" {
					tool.Skill = &model.SkillUse{Name: filepath.Base(filepath.Dir(path)), Path: preview(path), CallID: e.CallID}
				}
			}
			if e.Skill != nil && e.Skill.Name != "" && e.Name == "read" {
				tool.Skill = &model.SkillUse{Name: privacy.Preview(e.Skill.Name, 256), Path: preview(e.Skill.Path), SourceType: e.Skill.Source, CallID: e.CallID}
			}
			tools[e.CallID] = tool
			toolOrder = append(toolOrder, e.CallID)
		case "tool_end":
			tool := tools[e.CallID]
			if tool == nil {
				continue
			}
			_, b := bounds(tool.StartUnixNano/1e6, e.At)
			tool.EndUnixNano = b
			tool.Result = capture(e.Result)
			tool.OutputPreview = preview(e.Result)
			tool.Status = "ok"
			tool.ResultStatus = "success"
			if e.IsError {
				tool.Status = "error"
				tool.ResultStatus = "error"
				tool.ErrorType = "omp.tool_error"
			}
			if tool.Skill != nil {
				tool.Skill.Status = skillStatus(tool.Status)
				tool.Skill.ErrorType = tool.ErrorType
				if cfg.CaptureContent != "none" {
					tool.Skill.Description, tool.Skill.Version = skillMetadata(textContent(e.Result), cfg.MaxChars)
				}
			}
		}
	}
	for key, value := range map[string]string{"gen_ai.provider.name": last.Provider, "gen_ai.request.model": last.Model, "gen_ai.response.model": last.Model} {
		if value != "" {
			t.ExtraAttributes[key] = value
		}
	}
	if last.StopReason != "" {
		t.ExtraAttributes["gen_ai.response.finish_reasons"] = []string{finishReason(last.StopReason)}
	}
	if !hasUser {
		return model.Turn{}, nil
	}
	if last.StopReason == "aborted" || s.TerminalStatus == "aborted" {
		t.FinalStatus = model.FinalStatusCancelled
		t.ErrorType = "omp.aborted"
	}
	if last.StopReason == "error" || s.TerminalStatus == "failed" {
		t.ErrorType = "omp.model_error"
	}
	t.Reason = preview(last.ErrorMessage)
	for _, id := range toolOrder {
		tool := tools[id]
		if tool.EndUnixNano == 0 {
			if t.ErrorType == "" {
				return model.Turn{}, nil
			}
			tool.EndUnixNano = t.EndUnixNano
			tool.Status = "error"
			tool.ResultStatus = "cancelled"
			tool.ErrorType = "omp.incomplete_tool"
			if tool.Skill != nil {
				tool.Skill.Status = skillStatus(tool.Status)
				tool.Skill.ErrorType = tool.ErrorType
			}
		}
		t.ToolCalls = append(t.ToolCalls, *tool)
	}
	return t, nil
}

func textContent(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	parts, _ := value.([]any)
	texts := []string{}
	for _, part := range parts {
		item, _ := part.(map[string]any)
		if item["type"] == "text" {
			if text, ok := item["text"].(string); ok {
				texts = append(texts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(texts, "\n"))
}

func DecodeSnapshot(body []byte) (Snapshot, error) {
	var value Snapshot
	err := json.Unmarshal(body, &value)
	return value, err
}
