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
	t.InputLength = len(input)
	t.InputPreview = preview(input, cfg)
	if cfg.CaptureContent == "full" {
		t.InputMessages = privacy.Sanitize([]any{map[string]any{"role": "user", "content": input}}, cfg.MaxChars)
	}
	// Per-message usage is authoritative. llm_output may summarize an entire
	// attempt, so its aggregate usage must never be assigned to one LLM call.
	snapshotLLM := false
	for _, m := range messages {
		at := timestamp(m["timestamp"])
		if str(m, "role") == "assistant" && at >= start && at <= end {
			snapshotLLM = true
		}
	}
	seenCalls := map[string]bool{}
	var modelCalls []model.LLMCall
	seenTools := map[string]bool{}
	seenLLM := map[string]bool{}
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
			if id == "" || str(ev, "provider") == "" || seenCalls[id] || str(ev, "runId") != p.RunID || !validDuration || math.IsNaN(duration) || math.IsInf(duration, 0) || duration < 0 || duration > 86400000 || (outcome != "completed" && outcome != "error") {
				continue
			}
			seenCalls[id] = true
			call := model.LLMCall{CallID: id, Provider: str(ev, "provider"), RequestModel: str(ev, "model"), StartUnixNano: at - int64(duration*float64(time.Millisecond)), EndUnixNano: at, Status: "ok", ExtraAttributes: map[string]any{"openclaw.model_call_id": id, "openclaw.timing_source": "native_model_call"}}
			if outcome == "error" {
				call.Status = "error"
				call.ErrorType = str(ev, "errorCategory")
				if call.ErrorType == "" {
					call.ErrorType = "_OTHER"
				}
			}
			if latency, ok := ev["timeToFirstByteMs"].(float64); ok && !math.IsNaN(latency) && !math.IsInf(latency, 0) && latency >= 0 && latency <= duration {
				call.FirstChunkMs = &latency
				call.ExtraAttributes["openclaw.first_chunk.source"] = "openclaw.timeToFirstByteMs"
			}
			modelCalls = append(modelCalls, call)

		case "llm_output":
			if snapshotLLM {
				continue
			}
			m, _ := o.Event["lastAssistant"].(map[string]any)
			if m == nil {
				if contentText(o.Event["assistantTexts"]) == "" {
					continue
				}
				m = map[string]any{"content": o.Event["assistantTexts"]}
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
			call.Provider = str(o.Event, "provider")
			call.RequestModel = str(o.Event, "model")
			call.ResponseModel = call.RequestModel
			t.LLMCalls = append(t.LLMCalls, call)
			t.OutputLength = len(contentText(m["content"]))
			t.OutputPreview = call.OutputPreview
			t.OutputMessages = call.OutputMessages
			if len(call.FinishReasons) > 0 && call.FinishReasons[0] == "aborted" {
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
			duration := number(ev["durationMs"])
			toolStart := at - int64(duration*float64(time.Millisecond))
			if toolStart < start || toolStart >= at {
				toolStart = at - int64(time.Millisecond)
			}
			call := model.ToolCall{CallID: id, Name: str(ev, "toolName"), StartUnixNano: toolStart, EndUnixNano: at, Status: "ok", ResultStatus: "success"}
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
		output := contentText(m["content"])
		if output != "" {
			t.OutputPreview = preview(output, cfg)
			t.OutputLength = len(output)
			if cfg.CaptureContent == "full" {
				t.OutputMessages = privacy.Sanitize([]any{map[string]any{"role": "assistant", "content": output}}, cfg.MaxChars)
			}
		}
		if !nativeLLM {
			if at <= 0 || at > end {
				at = end
			}
			t.LLMCalls = append(t.LLMCalls, llm(m, fmt.Sprintf("message-%d", i), at-int64(time.Millisecond), at, cfg))
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
				tool := model.ToolCall{CallID: id, TriggeringLLMCall: fmt.Sprintf("message-%d", i), Name: str(block, "name"), StartUnixNano: at, EndUnixNano: at + int64(time.Millisecond), Status: "unset", ExtraAttributes: map[string]any{"openclaw.timing_source": "estimated_message_boundary"}}
				if cfg.CaptureContent != "none" {
					tool.Arguments = privacy.Sanitize(block["arguments"], cfg.MaxChars)
				}
				attachSkill(&tool, block["arguments"], cfg)
				for _, result := range messages {
					if str(result, "role") == "toolResult" && str(result, "toolCallId") == id {
						tool.Status = "ok"
						tool.ResultStatus = "success"
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
						}
						break
					}
				}
				if tool.EndUnixNano > end {
					tool.EndUnixNano = end
					tool.StartUnixNano = end - int64(time.Millisecond)
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
	for _, call := range t.LLMCalls {
		t.Usage.InputTokens += call.Usage.InputTokens
		t.Usage.OutputTokens += call.Usage.OutputTokens
		t.Usage.CacheReadTokens += call.Usage.CacheReadTokens
		t.Usage.CacheCreateTokens += call.Usage.CacheCreateTokens
	}
	if len(modelCalls) > 0 {
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
	if t.OutputPreview != "" || t.OutputLength > 0 || t.OutputMessages != nil {
		t.AssistantOutputs = []model.AssistantOutput{{StartUnixNano: end - int64(time.Millisecond), EndUnixNano: end, OutputPreview: t.OutputPreview, OutputMessages: t.OutputMessages}}
	}
	if *p.Success && len(t.LLMCalls) == 0 && t.OutputLength == 0 {
		return model.Turn{}, false
	}
	return t, true
}

func llm(m map[string]any, id string, start, end int64, cfg config.Config) model.LLMCall {
	if start <= 0 || start >= end {
		start = end - int64(time.Millisecond)
	}
	output := contentText(m["content"])
	c := model.LLMCall{CallID: id, StartUnixNano: start, EndUnixNano: end, Provider: str(m, "provider"), RequestModel: str(m, "model"), ResponseModel: str(m, "model"), OutputPreview: preview(output, cfg), Status: "ok", ExtraAttributes: map[string]any{"openclaw.timing_source": "estimated_message_boundary"}}
	if u, ok := m["usage"].(map[string]any); ok {
		c.Usage = usage(u)
	}
	if finish := str(m, "stopReason"); finish != "" {
		c.FinishReasons = []string{finish}
		if finish == "error" || finish == "aborted" {
			c.Status = "error"
			c.ErrorType = finish
			c.Reason = privacy.Text(m["errorMessage"], cfg.MaxChars)
		}
	}
	if cfg.CaptureContent == "full" {
		c.OutputMessages = privacy.Sanitize([]any{map[string]any{"role": "assistant", "content": output}}, cfg.MaxChars)
	}
	return c
}
func usage(u map[string]any) model.Usage {
	return model.Usage{InputTokens: int64(number(u["input"])), OutputTokens: int64(number(u["output"])), CacheReadTokens: int64(number(u["cacheRead"])), CacheCreateTokens: int64(number(u["cacheWrite"]))}
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
