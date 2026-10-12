package hook

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/parse"
)

func TestJournalCwdRoundtripAndRelativeSkillIsolation(t *testing.T) {
	for _, match := range []string{"exact", "other-tool", "other-session", "other-transcript"} {
		t.Run(match, func(t *testing.T) {
			cfg, in := setup(t)
			cwd := t.TempDir()
			path := filepath.Join(cwd, "demo", "SKILL.md")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte("---\nname: journal-demo\nversion: 3\n---\nprivate-skill-body"), 0600); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile("../parse/testdata/personal-1.2.5.jsonl")
			if err != nil {
				t.Fatal(err)
			}
			body = []byte(strings.Replace(string(body), `"name":"mcp__qw-builtin__mcp_workspace_bash","input":{"command":"printf TOOL_OK"}`, `"name":"Read","input":{"file_path":"demo/SKILL.md"}`, 1))
			if err := os.WriteFile(in.TranscriptPath, body, 0600); err != nil {
				t.Fatal(err)
			}
			if err := record(cfg, in); err != nil {
				t.Fatal(err)
			}
			in.ParentRequestSetID = "turn-2"
			in.ObservedAt = time.Date(2026, 10, 8, 6, 21, 35, 0, time.UTC).UnixNano()
			event := parse.HookInput{Event: "PreToolUse", SessionID: in.SessionID, TranscriptPath: in.TranscriptPath, ToolUseID: "tool-1", ToolName: "Read", Cwd: cwd, ObservedAt: in.ObservedAt - 7_000_000_000, ToolInput: map[string]any{"private": "raw-hook-secret"}, Reason: "raw-error-secret"}
			switch match {
			case "other-tool":
				event.ToolUseID = "other"
			case "other-session":
				event.SessionID = "other"
			case "other-transcript":
				event.TranscriptPath += ".other"
			}
			if err := record(cfg, event); err != nil {
				t.Fatal(err)
			}
			stored := readEvents(cfg, event)
			found := false
			for _, got := range stored {
				if got.Event != "PreToolUse" {
					continue
				}
				found = true
				if got.Cwd != cwd || got.ToolInput != nil || got.Reason != "" {
					t.Fatalf("journal metadata: %+v", got)
				}
			}
			if !found {
				t.Fatal("missing journal event")
			}
			files, err := filepath.Glob(filepath.Join(journalDir(cfg, event), "*.json"))
			if err != nil {
				t.Fatal(err)
			}
			for _, path := range files {
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(data), "raw-hook-secret") || strings.Contains(string(data), "raw-error-secret") || strings.Contains(string(data), "private-skill-body") {
					t.Fatal("journal leaked content")
				}
			}
			turns, pending, err := parse.Read(in, cfg, readEvents(cfg, in))
			if err != nil || pending || len(turns) != 2 {
				t.Fatalf("read: turns=%d pending=%v err=%v", len(turns), pending, err)
			}
			skill := turns[1].ToolCalls[0].Skill
			if match == "exact" {
				if skill == nil || skill.Name != "journal-demo" || skill.Version != "3" {
					t.Fatalf("lost cwd enrichment: %+v", skill)
				}
			} else if skill != nil {
				t.Fatalf("unmatched journal enriched skill: %+v", skill)
			}
		})
	}
}
