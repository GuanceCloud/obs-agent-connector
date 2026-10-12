package parse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnprovenTerminalEvidenceRemainsPending(t *testing.T) {
	in, cfg, events := fixture(t)
	body, err := os.ReadFile(in.TranscriptPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	structured := `{"type":"user","uuid":"cancel-result","timestamp":"2026-10-08T06:21:28Z","toolUseResult":{"kind":"tool-status","status":"cancelled"},"toolDenialKind":"interrupted","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"tool-1","content":"Cancelled","is_error":true}]}}`
	marker := `{"type":"user","uuid":"marker","timestamp":"2026-10-08T06:21:29Z","message":{"role":"user","content":[{"type":"text","text":"[Request interrupted by user for tool use]"}]}}`
	for _, tc := range []struct{ name, suffix, reason string }{
		{"client-exit", "", ""},
		{"session-end-error-reason", "", "error"},
		{"session-end-cancel-reason", "", "cancelled"},
		{"marker-only", marker, ""},
		{"structured-only", structured, ""},
		{"wrong-kind", strings.Replace(structured, "tool-status", "other", 1) + "\n" + marker, ""},
		{"wrong-status", strings.Replace(structured, "cancelled", "failed", 1) + "\n" + marker, ""},
		{"missing-denial", strings.Replace(structured, `,"toolDenialKind":"interrupted"`, "", 1) + "\n" + marker, ""},
		{"marker-before-structure", marker + "\n" + structured, ""},
		{"provider-error-text", `{"type":"assistant","uuid":"error-text","timestamp":"2026-10-08T06:21:29Z","message":{"id":"error-call","content":[{"type":"text","text":"Request failed: provider timeout, cancelled"}]}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := in
			input.Event = "SessionEnd"
			input.ParentRequestSetID = ""
			input.Reason = tc.reason
			input.TranscriptPath = filepath.Join(t.TempDir(), "terminal.jsonl")
			if err := os.WriteFile(input.TranscriptPath, []byte(strings.Join(lines[:6], "\n")+"\n"+tc.suffix), 0600); err != nil {
				t.Fatal(err)
			}
			turns, pending, err := Read(input, cfg, events)
			if err != nil || !pending || len(turns) != 1 {
				t.Fatalf("unproven turn finalized: turns=%d pending=%v err=%v", len(turns), pending, err)
			}
		})
	}
}
