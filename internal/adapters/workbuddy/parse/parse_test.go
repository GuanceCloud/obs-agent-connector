package parse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/workbuddy/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

const transcript = `{"id":"u1","timestamp":1784000000000,"type":"message","role":"user","content":[{"type":"input_text","text":"Inspect the demo skill"}]}
{"id":"c1","timestamp":1784000001200,"type":"function_call","callId":"call-1","name":"Skill","arguments":"{\"skill\":\"demo\",\"api_token\":\"private-value\"}","providerData":{"model":"test-model","provider":"anthropic","usage":{"input_tokens":120,"output_tokens":18,"cache_read_input_tokens":40}}}
{"id":"r1","timestamp":1784000001800,"type":"function_call_result","callId":"call-1","status":"completed","output":{"type":"text","text":"done"}}
{"id":"a1","timestamp":1784000002600,"type":"message","role":"assistant","content":[{"type":"output_text","text":"Finished"}],"status":"completed","message":{"usage":{"input_tokens":150,"output_tokens":12,"cache_read_input_tokens":50}},"providerData":{"model":"test-model"}}
`

func fixture(t *testing.T, body string) (HookInput, config.Config) {
	t.Helper()
	home := t.TempDir()
	path := filepath.Join(home, "transcript.jsonl")
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return HookInput{SessionID: "s1", TranscriptPath: path, Event: "Stop", Cwd: home}, config.Config{Enabled: true, ProfileDir: home, CaptureContent: "preview", MaxChars: 20000}
}

func TestTranscriptAndHookTiming(t *testing.T) {
	in, cfg := fixture(t, transcript)
	skill := filepath.Join(cfg.ProfileDir, "skills", "demo", "SKILL.md")
	os.MkdirAll(filepath.Dir(skill), 0700)
	os.WriteFile(skill, []byte("---\nname: demo\ndescription: Demo skill\n---\n"), 0600)
	events := []HookInput{{Event: "PreToolUse", CallID: "call-1", ObservedAt: 1784000001300000000}, {Event: "PostToolUse", CallID: "call-1", ObservedAt: 1784000001700000000}, {Event: "SubagentStart", AgentID: "sub-1", ParentSessionID: "parent", ParentToolCallID: "delegate-1"}}
	turns, pending, err := Read(in, cfg, events)
	if err != nil || pending || len(turns) != 1 {
		t.Fatalf("turns=%d pending=%v err=%v", len(turns), pending, err)
	}
	turn := turns[0]
	if len(turn.LLMCalls) != 2 || turn.LLMCalls[0].Usage.InputTokens != 120 || turn.LLMCalls[1].Usage.OutputTokens != 12 {
		t.Fatalf("wrong calls: %+v", turn.LLMCalls)
	}
	tool := turn.ToolCalls[0]
	if tool.StartUnixNano != events[0].ObservedAt || tool.EndUnixNano != events[1].ObservedAt || tool.Skill == nil || tool.Skill.Name != "demo" {
		t.Fatalf("wrong tool: %+v", tool)
	}
	if turn.ExtraAttributes["parent_session_id"] != "parent" {
		t.Fatal("missing parent association")
	}
	spans := (semantic.Builder{}).Build(turn)
	body, _ := json.Marshal(spans)
	if strings.Contains(string(body), "private-value") {
		t.Fatal("secret leaked")
	}
	if len(spans) != 6 {
		t.Fatalf("expected root, 2 llm, tool, skill, assistant; got %d", len(spans))
	}
	for _, span := range spans {
		if span.Name == "invoke_agent" || span.Name == "assistant" {
			for k := range span.Attributes {
				if strings.HasPrefix(k, "gen_ai.usage.") {
					t.Fatalf("usage on %s", span.Name)
				}
			}
		}
	}
	if turn.LLMCalls[0].EndUnixNano >= tool.EndUnixNano {
		t.Fatal("LLM includes tool execution")
	}
}

func TestPrivacyNoneKeepsUsage(t *testing.T) {
	in, cfg := fixture(t, transcript)
	cfg.CaptureContent = "none"
	turns, _, err := Read(in, cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	turn := turns[0]
	if turn.InputMessages != nil || turn.InputPreview != "" || turn.ToolCalls[0].Arguments != nil {
		t.Fatal("content captured while disabled")
	}
	if turn.LLMCalls[0].Usage.InputTokens != 120 {
		t.Fatal("usage missing")
	}
}
func TestTerminalFiltering(t *testing.T) {
	user := strings.Split(transcript, "\n")[0] + "\n"
	for _, tc := range []struct {
		name, body, event, reason string
		count                     int
		pending                   bool
	}{
		{"unfinished", user, "Stop", "", 0, true},
		{"failure", user, "StopFailure", "", 1, false},
		{"cancelled", user, "SessionEnd", "interrupt", 1, false},
		{"partial", transcript + `{"id":`, "Stop", "", 0, true},
		{"hidden", `{"id":"meta","timestamp":1784000000000,"type":"message","role":"user","content":"internal","providerData":{"isMeta":true}}`, "Stop", "", 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in, cfg := fixture(t, tc.body)
			in.Event = tc.event
			in.Reason = tc.reason
			turns, pending, err := Read(in, cfg, nil)
			if err != nil || pending != tc.pending || len(turns) != tc.count {
				t.Fatalf("turns=%d pending=%v err=%v", len(turns), pending, err)
			}
			if tc.name == "failure" && (turns[0].ErrorType != "agent_error" || turns[0].FinalStatus != model.FinalStatusCancelled) {
				t.Fatal("failure not represented")
			}
		})
	}
}
func TestRepeatedToolArgumentsWithoutIDs(t *testing.T) {
	in, cfg := fixture(t, transcript)
	key := ToolKey("Skill", map[string]any{"skill": "demo", "api_token": "private-value"})
	turns, _, err := Read(in, cfg, []HookInput{{Event: "PreToolUse", ToolName: "Skill", InputKey: key, ObservedAt: 1784000001250000000}, {Event: "PostToolUseFailure", ToolName: "Skill", InputKey: key, ObservedAt: 1784000001750000000}})
	if err != nil {
		t.Fatal(err)
	}
	tool := turns[0].ToolCalls[0]
	if tool.ErrorType != "tool_error" || tool.ExtraAttributes["timing_source"] != "hook" {
		t.Fatalf("unmatched timing: %+v", tool)
	}
}

func TestUnfinishedOlderTurnDoesNotBlockCompletedTurn(t *testing.T) {
	old := `{"id":"old","timestamp":1783999999000,"type":"message","role":"user","content":"interrupted"}` + "\n"
	in, cfg := fixture(t, old+transcript)
	turns, pending, err := Read(in, cfg, nil)
	if err != nil || pending || len(turns) != 1 || turns[0].TurnID != "u1" {
		t.Fatalf("turns=%d pending=%v err=%v", len(turns), pending, err)
	}
}
