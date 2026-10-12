package parse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/config"
)

func TestToolCwdExactEvidence(t *testing.T) {
	cwd := t.TempDir()
	in := HookInput{SessionID: "session", TranscriptPath: filepath.Join(cwd, "transcript.jsonl")}
	args := map[string]any{"file_path": "demo/SKILL.md"}
	base := HookInput{Event: "PreToolUse", SessionID: in.SessionID, TranscriptPath: in.TranscriptPath, ToolUseID: "tool", ToolName: "Read", InputKey: ToolKey("Read", args), Cwd: cwd, ObservedAt: 15}
	// The same struct is decoded by the Hook and serialized in its journal.
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip HookInput
	if err := json.Unmarshal(encoded, &roundtrip); err != nil {
		t.Fatal(err)
	}
	if got := toolCwd(in, []HookInput{roundtrip}, "tool", "Read", args, "request", 10, 20); got != cwd {
		t.Fatalf("roundtrip cwd = %q", got)
	}
	for _, tc := range []struct {
		name     string
		change   func(*HookInput)
		conflict bool
	}{
		{"missing", func(e *HookInput) { e.Cwd = "" }, true},
		{"relative", func(e *HookInput) { e.Cwd = "relative" }, true},
		{"network", func(e *HookInput) { e.Cwd = "//server/share" }, true},
		{"conflicting", func(e *HookInput) { e.Cwd = filepath.Join(cwd, "other") }, true},
		{"name", func(e *HookInput) { e.ToolName = "Other" }, true},
		{"input", func(e *HookInput) { e.InputKey = "other" }, true},
		{"request", func(e *HookInput) { e.RequestSetID = "other" }, true},
		{"session", func(e *HookInput) { e.SessionID = "other" }, false},
		{"transcript", func(e *HookInput) { e.TranscriptPath += ".other" }, false},
		{"tool", func(e *HookInput) { e.ToolUseID = "other" }, false},
		{"before", func(e *HookInput) { e.ObservedAt = 9 }, false},
		{"after", func(e *HookInput) { e.ObservedAt = 21 }, false},
		{"latest-session", func(e *HookInput) { e.Event = "SessionStart" }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := base
			tc.change(&e)
			if tc.name != "conflicting" && toolCwd(in, []HookInput{e}, "tool", "Read", args, "request", 10, 20) != "" {
				t.Fatal("unmatched or invalid evidence accepted")
			}
			want := cwd
			if tc.conflict {
				want = ""
			}
			for _, events := range [][]HookInput{{base, e}, {e, base}} {
				if got := toolCwd(in, events, "tool", "Read", args, "request", 10, 20); got != want {
					t.Fatalf("cwd = %q want %q", got, want)
				}
			}
		})
	}
	post := base
	post.Event = "PostToolUseFailure"
	post.ObservedAt = 16
	if got := toolCwd(in, []HookInput{base, post, base}, "tool", "Read", args, "request", 10, 20); got != cwd {
		t.Fatal("consistent repeated evidence rejected")
	}
}

func TestRelativeSkillContainment(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(cwd, "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: demo\nversion: 2\n---\nprivate body"), 0600); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{CaptureContent: "none", MaxChars: 1000}
	for _, name := range []string{"Read", "mcp__qw-builtin__mcp_workspace_bash"} {
		args := map[string]any{"file_path": "demo/SKILL.md", "command": "cat -- 'demo/SKILL.md'"}
		got := skillForAt(name, args, "tool", cfg, cwd)
		if got == nil || got.Path != canonical || got.Version != "2" || got.Name != "demo" || got.SourceType != "" {
			t.Fatalf("relative %s: %+v", name, got)
		}
		if skillForAt(name, args, "tool", cfg, "") != nil {
			t.Fatal("missing cwd accepted")
		}
	}
	for _, path := range []string{"../outside/SKILL.md", "demo/../demo/SKILL.md", "missing/demo/SKILL.md", `C:demo/SKILL.md`, `\server\share\SKILL.md`, "//server/share/SKILL.md", "~/demo/SKILL.md", "demo/\x00SKILL.md"} {
		if got := resolveSkillPath(path, cwd); got != "" {
			t.Fatalf("unsafe %q => %q", path, got)
		}
	}
	// A foreign drive-qualified path must not become a POSIX relative target.
	if path := `C:\demo\SKILL.md`; !filepath.IsAbs(path) && resolveSkillPath(path, cwd) != "" {
		t.Fatal("foreign drive path resolved against cwd")
	}
	if singleCatPath("cat '-demo/SKILL.md'") != "" || singleCatPath("cat -- '-demo/SKILL.md'") != "-demo/SKILL.md" {
		t.Fatal("cat option boundary lost")
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(cwd, "escape")); err == nil {
		if resolveSkillPath("escape/SKILL.md", cwd) != "" {
			t.Fatal("parent symlink escape accepted")
		}
	}
	if err := os.Symlink(path, filepath.Join(cwd, "SKILL.md")); err == nil {
		if resolveSkillPath("SKILL.md", cwd) != "" {
			t.Fatal("final symlink accepted")
		}
	}
	if got := skillForAt("Skill", map[string]any{"skill": "demo"}, "native", cfg, cwd); got == nil || got.Path != "" || got.Version != "" {
		t.Fatal("name-only invocation guessed a file")
	}
}

func TestReadRelativeSkillUsesOnlyMatchingHookCwd(t *testing.T) {
	in, cfg, events := fixture(t)
	cwd := t.TempDir()
	path := filepath.Join(cwd, "demo", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("---\nname: relative-demo\nversion: 7\n---"), 0600); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(in.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	body = []byte(strings.Replace(string(body), `"name":"mcp__qw-builtin__mcp_workspace_bash","input":{"command":"printf TOOL_OK"}`, `"name":"Read","input":{"file_path":"demo/SKILL.md"}`, 1))
	in.TranscriptPath = filepath.Join(t.TempDir(), "relative.jsonl")
	if err := os.WriteFile(in.TranscriptPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	hook := HookInput{Event: "PreToolUse", SessionID: in.SessionID, TranscriptPath: in.TranscriptPath, ToolUseID: "tool-1", ToolName: "Read", Cwd: cwd, ObservedAt: events[1].ObservedAt + 4_000_000_000}
	for _, matched := range []bool{true, false} {
		e := hook
		if !matched {
			e.ToolUseID = "unrelated"
		}
		turns, pending, err := Read(in, cfg, append(events, e))
		if err != nil || pending || len(turns) != 2 {
			t.Fatalf("read: %v %v", err, pending)
		}
		skill := turns[1].ToolCalls[0].Skill
		if matched && (skill == nil || skill.Name != "relative-demo" || skill.Version != "7") {
			t.Fatalf("matching skill: %+v", skill)
		}
		if !matched && skill != nil {
			t.Fatalf("unrelated cwd used: %+v", skill)
		}
	}
}
