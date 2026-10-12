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

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

type HookInput struct {
	Event              string `json:"hook_event_name"`
	SessionID          string `json:"session_id"`
	TranscriptPath     string `json:"transcript_path"`
	Cwd                string `json:"cwd,omitempty"`
	RequestSetID       string `json:"request_set_id,omitempty"`
	ParentRequestSetID string `json:"parent_request_set_id,omitempty"`
	ToolUseID          string `json:"tool_use_id,omitempty"`
	ToolName           string `json:"tool_name,omitempty"`
	ToolInput          any    `json:"tool_input,omitempty"`
	InputKey           string `json:"input_key,omitempty"`
	ObservedAt         int64  `json:"observed_at_ns,omitempty"`
	Reason             string `json:"reason,omitempty"`
}

func Terminal(e string) bool { return e == "Stop" }

// Replay also runs at the next prompt so durable cancellation can be recovered
// when the desktop did not emit any terminal Hook.
func Replay(e string) bool { return Terminal(e) || e == "UserPromptSubmit" || e == "SessionEnd" }
func Supported(e string) bool {
	return Replay(e) || e == "SessionStart" || e == "PreToolUse" || e == "PostToolUse" || e == "PostToolUseFailure"
}
func Hash(s string) string { h := sha256.Sum256([]byte(s)); return hex.EncodeToString(h[:]) }
func ToolKey(name string, input any) string {
	b, _ := json.Marshal(input)
	return Hash(name + ":" + string(b))
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func str(v any) string            { s, _ := v.(string); return s }
func timestamp(v any) int64 {
	if s, ok := v.(string); ok {
		t, e := time.Parse(time.RFC3339Nano, s)
		if e == nil {
			return t.UnixNano()
		}
	}
	if n, ok := v.(float64); ok {
		return int64(n * 1e6)
	}
	return 0
}
func capture(v any, c config.Config) any {
	if c.CaptureContent == "none" {
		return nil
	}
	return privacy.Sanitize(v, c.MaxChars)
}
func preview(v any, c config.Config) string {
	if v == nil || c.CaptureContent == "none" {
		return ""
	}
	return privacy.Preview(v, c.MaxChars)
}
func message(role string, parts []any) []any {
	return []any{map[string]any{"role": role, "parts": parts}}
}
func textParts(s string) []any      { return []any{map[string]any{"type": "text", "content": s}} }
func blocks(m map[string]any) []any { b, _ := object(m["message"])["content"].([]any); return b }
func realUser(m map[string]any) bool {
	if kind := str(object(m["origin"])["kind"]); kind != "" && kind != "human" {
		return false
	}
	return str(m["type"]) == "user" && m["isSidechain"] != true && m["isMeta"] != true && str(object(m["humanInput"])["text"]) != ""
}

// Read only explicit human turns; system reminders, attachments and token anchors are not inputs.
// A Stop is matched by request_set_id, or by its observation window when that ID is absent.
func Read(in HookInput, c config.Config, events []HookInput) ([]model.Turn, bool, error) {
	return read(in, c, events, nil)
}

// UsageObservation contains only allowlisted numeric-presence evidence from
// Hook-authorized turns, including turns still awaiting terminal evidence.
type UsageObservation struct {
	LLMCalls      int
	ObservedCalls int
	Fields        []string
	// ObservedAt is the latest transcript timestamp carrying valid usage, not
	// the replay time. Replaying old evidence must not make it appear fresh.
	ObservedAt int64
}

func ReadWithUsage(in HookInput, c config.Config, events []HookInput) ([]model.Turn, bool, UsageObservation, error) {
	var observation UsageObservation
	turns, pending, err := read(in, c, events, &observation)
	return turns, pending, observation, err
}

func read(in HookInput, c config.Config, events []HookInput, observation *UsageObservation) ([]model.Turn, bool, error) {
	f, e := os.Open(in.TranscriptPath)
	if e != nil {
		return nil, true, e
	}
	defer f.Close()
	var rows []map[string]any
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 65536), 8<<20)
	partial := false
	seen := map[string]int{}
	turnStart := 0
	for scan.Scan() {
		if strings.TrimSpace(scan.Text()) == "" {
			continue
		}
		var r map[string]any
		if json.Unmarshal(scan.Bytes(), &r) != nil {
			partial = true
			continue
		}
		if partial {
			return nil, true, fmt.Errorf("invalid QwenWork CN transcript record")
		}
		delete(r, "_qwenwork_usage_only") // Reserved internal marker, never source data.
		if r["isSidechain"] == true {
			continue
		}
		for _, key := range []string{"sessionId", "session_id"} {
			if session := str(r[key]); session != "" && session != in.SessionID {
				return nil, true, fmt.Errorf("QwenWork CN transcript session mismatch")
			}
		}
		id := str(r["uuid"])
		if id != "" {
			if index, exists := seen[id]; exists {
				// A repeated record may enrich usage without adding content. Only
				// accept it for the same assistant identity, never a different call.
				// Cross-turn repeats are ambiguous and may refer to checkpointed
				// history; discard them instead of assigning them to the new turn.
				original := rows[index]
				if index >= turnStart && timestamp(original["timestamp"]) > 0 && str(r["type"]) == "assistant" && str(original["type"]) == "assistant" && str(object(original["message"])["id"]) == str(object(r["message"])["id"]) {
					// Keep evidence at its transcript position so the most recent
					// valid value wins, including corrections that decrease counts.
					m := object(r["message"])
					r["message"] = map[string]any{"id": m["id"], "usage": m["usage"]}
					r["_qwenwork_usage_only"] = true
					rows = append(rows, r)
				}
				continue
			}
			seen[id] = len(rows)
		}
		if realUser(r) {
			turnStart = len(rows)
		}
		rows = append(rows, r)
	}
	if e = scan.Err(); e != nil {
		return nil, true, e
	}
	starts := []int{}
	for i, r := range rows {
		if realUser(r) {
			starts = append(starts, i)
		}
	}
	events = append(append([]HookInput{}, events...), in)
	sort.SliceStable(events, func(i, j int) bool { return events[i].ObservedAt < events[j].ObservedAt })
	out := []model.Turn{}
	pending := false
	for n, a := range starts {
		z := len(rows)
		next := int64(0)
		if n+1 < len(starts) {
			z = starts[n+1]
			next = timestamp(rows[z]["timestamp"])
		}
		first := rows[a]
		request := str(first["requestSetId"])
		start := timestamp(first["timestamp"])
		observed := false
		cancelledAt := cancellationTime(rows[a:z])
		for _, ev := range events {
			if ev.Event != "UserPromptSubmit" || (cancelledAt > 0 && ev.ObservedAt > cancelledAt) {
				continue
			}
			if ev.RequestSetID != "" || request != "" {
				observed = ev.RequestSetID != "" && ev.RequestSetID == request
			} else {
				// Without an ID, require the event inside an explicit turn window.
				end := next
				if end == 0 {
					end = cancelledAt
				}
				observed = start > 0 && end > start && ev.ObservedAt >= start && ev.ObservedAt < end
			}
			if observed {
				break
			}
		}
		var stop *HookInput
		for i := range events {
			ev := &events[i]
			if ev.Event != "Stop" {
				continue
			}
			if ev.ParentRequestSetID != "" {
				if request == "" || ev.ParentRequestSetID != request {
					continue
				}
			} else if ev.ObservedAt < start || (next > 0 && ev.ObservedAt >= next) {
				continue
			}
			stop = ev
			break
		}
		if cancelledAt > 0 && observed {
			stop = &HookInput{Event: "Stop", Reason: "cancelled", ObservedAt: cancelledAt}
		}
		// A transcript can contain history from before Hook registration. Only
		// recorded prompts or correlated Stops authorize replay of a turn.
		if !observed && stop == nil {
			continue
		}
		if observation != nil {
			observeUsage(rows[a:z], observation)
		}
		if stop == nil || start == 0 || (partial && n == len(starts)-1) {
			pending = true
			continue
		}
		t, complete := build(rows[a:z], in, c, events, *stop)
		if !complete {
			pending = true
			continue
		}
		out = append(out, t)
	}
	if len(starts) == 0 {
		for _, ev := range events {
			if ev.Event == "UserPromptSubmit" || ev.Event == "Stop" {
				pending = true
				break
			}
		}
	}
	return out, pending, nil
}
func cancellationTime(rows []map[string]any) int64 {
	var structured int64
	for _, r := range rows {
		status := object(r["toolUseResult"])
		if str(r["type"]) == "user" && status["kind"] == "tool-status" && status["status"] == "cancelled" && r["toolDenialKind"] == "interrupted" {
			structured = timestamp(r["timestamp"])
		}
		if structured > 0 && str(r["type"]) == "user" && r["humanInput"] == nil {
			for _, v := range blocks(r) {
				p := object(v)
				if p["type"] == "text" && p["text"] == "[Request interrupted by user for tool use]" {
					return timestamp(r["timestamp"])
				}
			}
		}
	}
	return 0
}
func build(rows []map[string]any, in HookInput, c config.Config, events []HookInput, stop HookInput) (model.Turn, bool) {
	first := rows[0]
	id := str(first["requestSetId"])
	if id == "" {
		id = str(first["uuid"])
	}
	prompt := str(object(first["humanInput"])["text"])
	start := timestamp(first["timestamp"])
	t := model.Turn{SessionID: in.SessionID, TurnID: id, AgentRuntime: "qwenwork-cn", AgentName: "QwenWork CN", AgentVersion: str(first["version"]), StartUnixNano: start, EndUnixNano: start + 1, FinalStatus: model.FinalStatusUnset, Resource: c.ResourceAttributes, ExtraAttributes: map[string]any{"gen_ai.turn.id": id}, InputLength: len([]rune(prompt)), InputPreview: preview(prompt, c), InputMessages: capture(message("user", textParts(prompt)), c)}
	conv := message("user", textParts(prompt))
	boundary := start
	calls := map[string]int{}
	parts := map[string][]any{}
	// Visible conversation parts deliberately exclude opt-in reasoning.
	visibleParts := map[string][]any{}
	reasoning := map[string]*reasoningCapture{}
	texts := map[string][]string{}
	textAt := map[string]int64{}
	partSeen := map[string]bool{}
	tools := map[string]int{}
	flushed := map[string]bool{}
	lastText := ""
	lastKind := ""
	for _, r := range rows[1:] {
		at := timestamp(r["timestamp"])
		if at == 0 {
			continue
		}
		usageOnly := r["_qwenwork_usage_only"] == true
		if !usageOnly && at > t.EndUnixNano {
			t.EndUnixNano = at
		}
		m := object(r["message"])
		typ := str(r["type"])
		if typ == "assistant" {
			cid := str(m["id"])
			if cid == "" {
				cid = str(r["uuid"])
			}
			if cid == "" {
				continue
			}
			ix, ok := calls[cid]
			if usageOnly {
				if ok {
					mergeUsage(&t.LLMCalls[ix], object(m["usage"]))
				}
				continue
			}
			if !ok {
				lastKind = ""
				ix = len(t.LLMCalls)
				calls[cid] = ix
				t.LLMCalls = append(t.LLMCalls, model.LLMCall{CallID: id + ":" + cid, StartUnixNano: boundary, EndUnixNano: at, RequestModel: str(m["model"]), ResponseModel: str(m["model"]), InputMessages: capture(conv, c), InputPreview: preview(conv, c), ExtraAttributes: map[string]any{"timing_source": "inferred"}})
			}
			llm := &t.LLMCalls[ix]
			if at > llm.EndUnixNano {
				llm.EndUnixNano = at
			}
			mergeUsage(llm, object(m["usage"]))
			if reason := str(m["stop_reason"]); reason != "" {
				llm.FinishReasons = []string{reason}
			}
			for _, v := range blocks(r) {
				p := object(v)
				raw, _ := json.Marshal(p)
				key := cid + ":" + string(raw)
				if partSeen[key] {
					continue
				}
				partSeen[key] = true
				switch str(p["type"]) {
				case "text":
					s := str(p["text"])
					parts[cid] = append(parts[cid], map[string]any{"type": "text", "content": s})
					visibleParts[cid] = append(visibleParts[cid], map[string]any{"type": "text", "content": s})
					if s != "" {
						texts[cid] = append(texts[cid], s)
						lastText = strings.Join(texts[cid], "\n")
						lastKind = "text"
						textAt[cid] = at
					}
					if llm.OutputKind == "" {
						llm.OutputKind = "text"
					}
				case "thinking":
					if c.CaptureThinking && c.CaptureContent != "none" {
						if reasoning[cid] == nil {
							reasoning[cid] = &reasoningCapture{}
						}
						if part := reasoning[cid].add(str(p["thinking"]), c.MaxChars); part != nil {
							parts[cid] = append(parts[cid], part)
						}
					}
				case "tool_use":
					tid := str(p["id"])
					if tid == "" {
						continue
					}
					if _, ok := tools[tid]; ok {
						continue
					}
					name := str(p["name"])
					args := p["input"]
					parts[cid] = append(parts[cid], map[string]any{"type": "tool_call", "id": tid, "name": name, "arguments": args})
					visibleParts[cid] = append(visibleParts[cid], map[string]any{"type": "tool_call", "id": tid, "name": name, "arguments": args})
					tools[tid] = len(t.ToolCalls)
					t.ToolCalls = append(t.ToolCalls, model.ToolCall{CallID: tid, TriggeringLLMCall: llm.CallID, Name: name, StartUnixNano: at, EndUnixNano: at + 1, Arguments: capture(args, c), InputPreview: preview(args, c), Command: preview(object(args)["command"], c), ExtraAttributes: map[string]any{"timing_source": "transcript"}})
					cwd := toolCwd(in, events, tid, name, args, id, start, stop.ObservedAt)
					t.ToolCalls[len(t.ToolCalls)-1].Skill = skillForAt(name, args, tid, c, cwd)
					llm.OutputKind = "tool_call"
					lastKind = "tool_call"
				}
			}
			llm.OutputMessages = capture(message("assistant", parts[cid]), c)
			llm.OutputPreview = preview(visibleParts[cid], c)
			if len(visibleParts[cid]) == 0 {
				llm.OutputPreview = ""
			}
			if llm.OutputKind == "text" {
				llm.OutputPreview = preview(strings.Join(texts[cid], "\n"), c)
			}
			boundary = at
		} else if typ == "user" {
			for _, call := range t.LLMCalls {
				for cid, ix := range calls {
					if t.LLMCalls[ix].CallID == call.CallID && !flushed[cid] {
						conv = append(conv, message("assistant", visibleParts[cid])...)
						flushed[cid] = true
					}
				}
			}
			for _, v := range blocks(r) {
				p := object(v)
				if str(p["type"]) != "tool_result" {
					continue
				}
				tid := str(p["tool_use_id"])
				if ix, ok := tools[tid]; ok {
					tool := &t.ToolCalls[ix]
					tool.EndUnixNano = at
					tool.Result = capture(p["content"], c)
					tool.OutputPreview = preview(p["content"], c)
					tool.ResultStatus = "success"
					if p["is_error"] == true {
						tool.ResultStatus = "error"
						tool.ErrorType = "tool_error"
					}
					status := object(r["toolUseResult"])
					if status["kind"] == "tool-status" && status["status"] == "cancelled" && r["toolDenialKind"] == "interrupted" {
						tool.ResultStatus = "cancelled"
						tool.ErrorType = "cancelled"
					}
					conv = append(conv, map[string]any{"role": "tool", "tool_call_id": tid, "parts": []any{map[string]any{"type": "tool_call_response", "content": p["content"]}}})
				}
				boundary = at
			}
		}
	}
	// Hook tool IDs provide measured tool boundaries. LLM timing remains inferred.
	for i := range t.ToolCalls {
		tool := &t.ToolCalls[i]
		for _, ev := range events {
			if ev.ToolUseID != tool.CallID || ev.ObservedAt < start {
				continue
			}
			if ev.Event == "PreToolUse" {
				tool.StartUnixNano = ev.ObservedAt
				tool.ExtraAttributes["timing_source"] = "hook"
			}
			if ev.Event == "PostToolUse" || ev.Event == "PostToolUseFailure" {
				tool.EndUnixNano = ev.ObservedAt
				if ev.Event == "PostToolUseFailure" && tool.ResultStatus != "cancelled" {
					tool.ResultStatus = "error"
					tool.ErrorType = "tool_error"
				}
			}
		}
		if tool.EndUnixNano < tool.StartUnixNano {
			tool.EndUnixNano = tool.StartUnixNano + 1
		}
		if tool.Skill != nil {
			tool.Skill.Status = tool.ResultStatus
			tool.Skill.ErrorType = tool.ErrorType
		}
		for j := range t.LLMCalls {
			llm := &t.LLMCalls[j]
			if llm.CallID == tool.TriggeringLLMCall && llm.EndUnixNano > tool.StartUnixNano {
				llm.EndUnixNano = tool.StartUnixNano
				if llm.StartUnixNano > llm.EndUnixNano {
					llm.StartUnixNano = llm.EndUnixNano
				}
			}
		}
		if tool.EndUnixNano > t.EndUnixNano {
			t.EndUnixNano = tool.EndUnixNano
		}
	}
	cancelled := stop.Reason == "cancelled" || stop.Reason == "canceled" || stop.Reason == "aborted" || stop.Reason == "interrupted"
	failed := stop.Reason == "error" || stop.Reason == "failed"
	if cancelled || failed {
		t.FinalStatus = model.FinalStatusCancelled
		if failed {
			t.ErrorType = "agent_error"
		}
	} else {
		if lastKind != "text" || lastText == "" {
			return t, false
		}
		for _, tool := range t.ToolCalls {
			if tool.ResultStatus == "" {
				return t, false
			}
		}
		t.FinalStatus = model.FinalStatusCompleted
	}
	if stop.ObservedAt > t.EndUnixNano {
		t.EndUnixNano = stop.ObservedAt
	}
	t.OutputLength = len([]rune(lastText))
	t.OutputPreview = preview(lastText, c)
	t.OutputMessages = capture(message("assistant", textParts(lastText)), c)
	// LLM slice order, rather than map iteration or late usage records, defines
	// the stable output sequence. Mixed text/tool calls still have visible output.
	for _, llm := range t.LLMCalls {
		cid := strings.TrimPrefix(llm.CallID, id+":")
		text := strings.Join(texts[cid], "\n")
		if text == "" {
			continue
		}
		at := textAt[cid]
		end := at + int64(time.Millisecond)
		t.AssistantOutputs = append(t.AssistantOutputs, model.AssistantOutput{TriggeringLLMCall: llm.CallID, StartUnixNano: at, EndUnixNano: end, OutputMessages: capture(message("assistant", textParts(text)), c), OutputPreview: preview(text, c), OutputKind: "text", ResponseModel: llm.ResponseModel, ExtraAttributes: map[string]any{"timing_source": "transcript", "duration_source": "synthetic"}})
		if end > t.EndUnixNano {
			t.EndUnixNano = end
		}
	}
	return t, id != ""
}
