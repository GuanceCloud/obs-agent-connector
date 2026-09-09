package parse

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/workbuddy/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/util"
)

type HookInput struct {
	Event            string `json:"hook_event_name"`
	SessionID        string `json:"session_id"`
	TranscriptPath   string `json:"transcript_path"`
	Cwd              string `json:"cwd"`
	Reason           string `json:"reason,omitempty"`
	Version          string `json:"version,omitempty"`
	WorkBuddyVersion string `json:"workbuddy_version,omitempty"`
	CallID           string `json:"call_id,omitempty"`
	ToolUseID        string `json:"tool_use_id,omitempty"`
	ToolName         string `json:"tool_name,omitempty"`
	ToolInput        any    `json:"tool_input,omitempty"`
	AgentID          string `json:"agent_id,omitempty"`
	AgentType        string `json:"agent_type,omitempty"`
	ParentSessionID  string `json:"parent_session_id,omitempty"`
	ParentToolCallID string `json:"parent_tool_call_id,omitempty"`
	SessionChannel   string `json:"session_channel,omitempty"`
	Source           string `json:"source,omitempty"`
	Trigger          string `json:"trigger,omitempty"`
	ObservedAt       int64  `json:"observed_at_ns,omitempty"`
	InputKey         string `json:"input_key,omitempty"`
}

func Terminal(event string) bool {
	return event == "Stop" || event == "StopFailure" || event == "SessionEnd" || event == "SubagentStop"
}
func Supported(event string) bool {
	return Terminal(event) || event == "UserPromptSubmit" || event == "PreToolUse" || event == "PostToolUse" || event == "PostToolUseFailure" || event == "SubagentStart"
}
func Hash(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
func ToolKey(name string, input any) string {
	b, _ := json.Marshal(input)
	return Hash(name + ":" + string(b))
}

func Read(input HookInput, cfg config.Config, events []HookInput) ([]model.Turn, bool, error) {
	if input.SessionID == "" || input.TranscriptPath == "" {
		return nil, false, fmt.Errorf("WorkBuddy Hook requires session_id and transcript_path")
	}
	f, err := os.Open(input.TranscriptPath)
	if err != nil {
		return nil, true, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 65536), 8<<20)
	var items []map[string]any
	partial := false
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var item map[string]any
		if json.Unmarshal([]byte(line), &item) != nil {
			partial = true
			continue
		}
		if partial {
			return nil, true, fmt.Errorf("invalid WorkBuddy transcript record")
		}
		if item != nil {
			items = append(items, item)
		}
	}
	if err = scanner.Err(); err != nil {
		return nil, true, err
	}
	starts := []int{}
	for i, item := range items {
		if realUser(item) {
			starts = append(starts, i)
		}
	}
	var turns []model.Turn
	pending := partial
	for i, start := range starts {
		end := len(items)
		var nextTurnStart int64
		last := i == len(starts)-1
		if !last {
			end = starts[i+1]
			nextTurnStart = timestamp(items[end]["timestamp"])
		}
		turn := build(items[start:end], input, cfg, events, last, nextTurnStart)
		if turn.FinalStatus == model.FinalStatusUnset {
			pending = pending || last
		} else if !partial || !last {
			turns = append(turns, turn)
		}
	}
	return turns, pending, nil
}

func build(items []map[string]any, in HookInput, cfg config.Config, events []HookInput, last bool, nextTurnStart int64) model.Turn {
	first := items[0]
	prompt := messageText(first)
	start := timestamp(first["timestamp"])
	id := str(first["id"])
	if id == "" {
		id = Hash(in.SessionID + ":" + util.ToText(first["timestamp"]) + ":" + prompt)[:32]
	}
	turn := model.Turn{SessionID: in.SessionID, TurnID: id, AgentRuntime: "workbuddy", AgentName: "WorkBuddy", AgentVersion: firstText(in.WorkBuddyVersion, in.Version), StartUnixNano: start, EndUnixNano: start + 1, FinalStatus: model.FinalStatusUnset, Resource: cfg.ResourceAttributes, ExtraAttributes: map[string]any{"gen_ai.turn.id": id}}
	for _, e := range events {
		if e.Event == "SubagentStart" {
			turn.ExtraAttributes["parent_session_id"] = e.ParentSessionID
			turn.ExtraAttributes["parent_tool_call_id"] = e.ParentToolCallID
			turn.ExtraAttributes["subagent_id"] = e.AgentID
			turn.ExtraAttributes["subagent_type"] = e.AgentType
		}
		if channel := firstText(e.SessionChannel, e.Source, e.Trigger); channel != "" {
			turn.ExtraAttributes["session_channel"] = channel
		}
	}
	if in.Event == "SubagentStop" {
		turn.ExtraAttributes["is_subagent"] = true
	}
	turn.InputLength = len([]rune(prompt))
	turn.InputPreview = captureText(prompt, cfg)
	turn.InputMessages = capture(messages("user", prompt), cfg)
	conversation := []any{messages("user", prompt)[0]}
	boundary := start
	var batch *model.LLMCall
	var parts []any
	var batchToolIndexes []int
	finish := func() {
		if batch == nil {
			return
		}
		batch.CallID = fmt.Sprintf("%s:llm:%d", id, len(turn.LLMCalls))
		output := []any{map[string]any{"role": "assistant", "parts": parts}}
		batch.OutputMessages = capture(output, cfg)
		batch.OutputPreview = captureText(output, cfg)
		if batch.OutputKind == "tool_call" {
			batch.FinishReasons = []string{"tool_call"}
		} else {
			batch.FinishReasons = []string{"stop"}
		}
		for _, index := range batchToolIndexes {
			turn.ToolCalls[index].TriggeringLLMCall = batch.CallID
		}
		turn.LLMCalls = append(turn.LLMCalls, *batch)
		conversation = append(conversation, output...)
		batch = nil
		parts = nil
		batchToolIndexes = nil
	}
	tools := map[string]int{}
	lastOutput := ""
	terminalOutput := false
	for _, item := range items[1:] {
		at := timestamp(item["timestamp"])
		if at == 0 {
			at = boundary
		}
		if at > turn.EndUnixNano {
			turn.EndUnixNano = at
		}
		typ := str(item["type"])
		callID := firstText(str(item["callId"]), str(item["call_id"]), str(item["id"]))
		if typ == "function_call_result" || typ == "function_call_output" {
			finish()
			boundary = at
			result := toolResult(item)
			conversation = append(conversation, map[string]any{"role": "tool", "tool_call_id": callID, "parts": []any{map[string]any{"type": "tool_call_response", "content": result}}})
			if index, ok := tools[callID]; ok {
				tool := &turn.ToolCalls[index]
				tool.EndUnixNano = at
				tool.Result = capture(result, cfg)
				tool.OutputPreview = captureText(result, cfg)
				tool.ResultStatus = "success"
				if isError(item) {
					tool.ErrorType = "tool_error"
					tool.ResultStatus = "error"
				}
			}
			continue
		}
		if typ != "function_call" && !(typ == "message" && str(item["role"]) == "assistant") {
			continue
		}
		data := object(item["providerData"])
		if batch == nil {
			batch = &model.LLMCall{StartUnixNano: boundary, InputMessages: capture(append([]any{}, conversation...), cfg), InputPreview: captureText(conversation, cfg), OutputKind: "text", ExtraAttributes: map[string]any{"timing_source": "inferred"}}
		}
		batch.EndUnixNano = at
		if name := firstText(str(data["model"]), str(data["modelId"]), str(item["model"])); name != "" {
			batch.RequestModel = name
			batch.ResponseModel = name
		}
		if provider := firstText(str(data["provider"]), str(data["providerName"]), str(data["modelProvider"])); provider != "" {
			batch.Provider = provider
		}
		usage := object(object(item["message"])["usage"])
		if len(usage) == 0 {
			usage = object(data["usage"])
		}
		if len(usage) > 0 {
			batch.Usage = usageOf(usage)
		}
		if typ == "function_call" {
			args := decode(item["arguments"])
			name := firstText(str(item["name"]), "unknown")
			parts = append(parts, map[string]any{"type": "tool_call", "id": callID, "name": name, "arguments": args})
			batch.OutputKind = "tool_call"
			tool := model.ToolCall{CallID: callID, Name: name, StartUnixNano: at, EndUnixNano: at + 1, Arguments: capture(args, cfg), InputPreview: captureText(args, cfg), ExtraAttributes: map[string]any{"timing_source": "transcript"}}
			tool.Skill = skillFor(name, args, in.Cwd, cfg)
			tool.Command = captureText(object(args)["command"], cfg)
			tools[callID] = len(turn.ToolCalls)
			batchToolIndexes = append(batchToolIndexes, len(turn.ToolCalls))
			turn.ToolCalls = append(turn.ToolCalls, tool)
			terminalOutput = false
		} else {
			text := messageText(item)
			for _, p := range content(item) {
				switch str(p["type"]) {
				case "reasoning", "thinking":
					parts = append(parts, map[string]any{"type": "reasoning", "content": firstText(str(p["text"]), str(p["thinking"]))})
				}
			}
			if text != "" {
				parts = append(parts, map[string]any{"type": "text", "content": text})
				lastOutput = text
				terminalOutput = !isError(item)
				turn.AssistantOutputs = append(turn.AssistantOutputs, model.AssistantOutput{StartUnixNano: at, EndUnixNano: at + 1, OutputMessages: capture(messages("assistant", text), cfg), OutputPreview: captureText(text, cfg), OutputKind: "text"})
			}
		}
	}
	finish()
	// Match exact call IDs, or consume identical-argument pairs in order when IDs are absent.
	ordered := append([]HookInput{}, events...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].ObservedAt < ordered[j].ObservedAt })
	consumed := map[int]bool{}
	for index := range turn.ToolCalls {
		tool := &turn.ToolCalls[index]
		var before int64
		key := ""
		for _, item := range items {
			if str(item["type"]) == "function_call" && firstText(str(item["callId"]), str(item["call_id"]), str(item["id"])) == tool.CallID {
				key = ToolKey(tool.Name, decode(item["arguments"]))
				break
			}
		}
		for i, e := range ordered {
			if nextTurnStart > 0 && e.ObservedAt >= nextTurnStart {
				continue
			}
			eid := firstText(e.CallID, e.ToolUseID)
			match := eid != "" && eid == tool.CallID
			if eid == "" {
				match = e.InputKey == key && e.ToolName == tool.Name && e.ObservedAt >= start
			}
			if !match || consumed[i] {
				continue
			}
			if e.Event == "PreToolUse" {
				before = e.ObservedAt
				consumed[i] = true
			}
			if e.Event == "PostToolUse" || e.Event == "PostToolUseFailure" {
				consumed[i] = true
				if before > 0 {
					tool.StartUnixNano = before
					tool.ExtraAttributes["timing_source"] = "hook"
				}
				tool.EndUnixNano = e.ObservedAt
				if e.Event == "PostToolUseFailure" {
					tool.ErrorType = "tool_error"
					tool.ResultStatus = "error"
				}
				break
			}
		}
		if tool.Skill != nil {
			tool.Skill.Status = "ok"
			if tool.ErrorType != "" {
				tool.Skill.Status = "error"
				tool.Skill.ErrorType = tool.ErrorType
			}
		}
		if tool.EndUnixNano > turn.EndUnixNano {
			turn.EndUnixNano = tool.EndUnixNano
		}
	}
	if terminalOutput {
		turn.FinalStatus = model.FinalStatusCompleted
	}
	if last && in.Event == "StopFailure" {
		turn.FinalStatus = model.FinalStatusCancelled
		turn.ErrorType = "agent_error"
	}
	if last && in.Event == "SessionEnd" && in.Reason != "completed" && in.Reason != "clear" && turn.FinalStatus == model.FinalStatusUnset {
		turn.FinalStatus = model.FinalStatusCancelled
	}
	if start == 0 {
		turn.FinalStatus = model.FinalStatusUnset
	}
	turn.OutputLength = len([]rune(lastOutput))
	turn.OutputPreview = captureText(lastOutput, cfg)
	turn.OutputMessages = capture(messages("assistant", lastOutput), cfg)
	for _, out := range turn.AssistantOutputs {
		if out.EndUnixNano > turn.EndUnixNano {
			turn.EndUnixNano = out.EndUnixNano
		}
	}
	return turn
}

func realUser(m map[string]any) bool {
	d := object(m["providerData"])
	for _, k := range []string{"skipRun", "isMeta", "isCompactInternal", "isCompact", "isTeammateMessage"} {
		if d[k] == true {
			return false
		}
	}
	return str(m["type"]) == "message" && str(m["role"]) == "user" && !strings.Contains(strings.ToLower(str(d["agentPurpose"])), "compact") && strings.TrimSpace(messageText(m)) != ""
}
func content(m map[string]any) []map[string]any {
	out := []map[string]any{}
	if values, ok := m["content"].([]any); ok {
		for _, v := range values {
			if p, ok := v.(map[string]any); ok {
				out = append(out, p)
			}
		}
	}
	return out
}
func messageText(m map[string]any) string {
	if s, ok := m["content"].(string); ok {
		return s
	}
	var parts []string
	for _, p := range content(m) {
		switch str(p["type"]) {
		case "text", "input_text", "output_text":
			parts = append(parts, str(p["text"]))
		}
	}
	return strings.Join(parts, "\n")
}
func messages(role, text string) []any {
	return []any{map[string]any{"role": role, "parts": []any{map[string]any{"type": "text", "content": text}}}}
}
func usageOf(u map[string]any) model.Usage {
	return model.Usage{InputTokens: number(u, "input_tokens", "inputTokens", "prompt_tokens"), OutputTokens: number(u, "output_tokens", "outputTokens", "completion_tokens"), CacheReadTokens: number(u, "cache_read_input_tokens", "cached_tokens", "cacheReadInputTokens"), CacheCreateTokens: number(u, "cache_creation_input_tokens", "cacheCreationInputTokens"), ReasoningTokens: number(u, "reasoning_output_tokens", "reasoning_tokens")}
}
func number(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			f, _ := strconv.ParseFloat(util.ToText(v), 64)
			if f > 0 {
				return int64(f)
			}
			return 0
		}
	}
	return 0
}
func timestamp(v any) int64 {
	if f, ok := v.(float64); ok {
		return int64(f * 1e6)
	}
	if s, ok := v.(string); ok {
		if t, e := time.Parse(time.RFC3339Nano, s); e == nil {
			return t.UnixNano()
		}
		if f, e := strconv.ParseFloat(s, 64); e == nil {
			return int64(f * 1e6)
		}
	}
	return 0
}
func isError(m map[string]any) bool {
	s := str(m["status"])
	d := object(m["providerData"])
	return s == "incomplete" || s == "failed" || s == "cancelled" || m["is_error"] == true || d["isError"] == true || d["error"] != nil
}
func toolResult(m map[string]any) any {
	out := m["output"]
	if p, ok := out.(map[string]any); ok && p["type"] == "text" {
		return decode(p["text"])
	}
	if out != nil {
		return out
	}
	if out = object(m["providerData"])["toolResult"]; out != nil {
		return out
	}
	return m["result"]
}
func decode(v any) any {
	if s, ok := v.(string); ok {
		var out any
		if json.Unmarshal([]byte(s), &out) == nil {
			return out
		}
	}
	return v
}
func object(v any) map[string]any { m, _ := v.(map[string]any); return m }
func str(v any) string            { s, _ := v.(string); return s }
func firstText(values ...string) string {
	for _, s := range values {
		if s != "" {
			return s
		}
	}
	return ""
}
func capture(v any, c config.Config) any {
	if c.CaptureContent == "none" {
		return nil
	}
	return privacy.Sanitize(v, c.MaxChars)
}
func captureText(v any, c config.Config) string {
	if c.CaptureContent == "none" || v == nil {
		return ""
	}
	return privacy.Preview(v, c.MaxChars)
}

func skillFor(name string, args any, cwd string, cfg config.Config) *model.SkillUse {
	var paths []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case string:
			if strings.HasSuffix(x, "SKILL.md") {
				if !filepath.IsAbs(x) {
					x = filepath.Join(cwd, x)
				}
				paths = append(paths, x)
			}
		case map[string]any:
			for _, v := range x {
				walk(v)
			}
		case []any:
			for _, v := range x {
				walk(v)
			}
		}
	}
	walk(args)
	if strings.EqualFold(name, "skill") {
		n := firstText(str(object(args)["skill"]), str(object(args)["name"]))
		if n != "" && filepath.Base(n) == n {
			paths = append(paths, filepath.Join(cfg.ProfileDir, "skills", n, "SKILL.md"), filepath.Join(cwd, ".workbuddy", "skills", n, "SKILL.md"), filepath.Join(cwd, ".codebuddy", "skills", n, "SKILL.md"))
			for _, env := range []string{"WORKBUDDY_BUILTIN_SKILLS_DIR", "CODEBUDDY_BUILTIN_SKILLS_DIR"} {
				if root := os.Getenv(env); root != "" {
					paths = append(paths, filepath.Join(root, n, "SKILL.md"))
				}
			}
			matches, _ := filepath.Glob(filepath.Join(cfg.ProfileDir, "plugins", "marketplaces", "*", "plugins", "*", "skills", n, "SKILL.md"))
			paths = append(paths, matches...)
		}
	}
	for _, p := range paths {
		f, e := os.Open(p)
		if e != nil {
			continue
		}
		info, e := f.Stat()
		if e != nil || !info.Mode().IsRegular() {
			f.Close()
			continue
		}
		scanner := bufio.NewScanner(f)
		meta := map[string]string{}
		if scanner.Scan() && scanner.Text() == "---" {
			for i := 0; i < 100 && scanner.Scan(); i++ {
				line := scanner.Text()
				if line == "---" {
					break
				}
				k, v, ok := strings.Cut(line, ":")
				if ok {
					meta[k] = strings.Trim(strings.TrimSpace(v), "\"'")
				}
			}
		}
		f.Close()
		source := "project"
		if strings.Contains(p, string(filepath.Separator)+"plugins"+string(filepath.Separator)) {
			source = "plugin"
		} else if strings.Contains(p, "builtin-skills") {
			source = "builtin"
		} else if strings.HasPrefix(p, filepath.Join(cfg.ProfileDir, "skills")+string(filepath.Separator)) {
			source = "user"
		}
		return &model.SkillUse{Name: firstText(meta["name"], filepath.Base(filepath.Dir(p))), Path: p, SourceType: source, Description: privacy.Text(meta["description"], cfg.MaxChars), Version: meta["version"]}
	}
	return nil
}
