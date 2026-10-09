package parse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

func TestSkillEvidenceAndMetadata(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "demo")
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(path, []byte("---\nname: canonical-demo\nversion: '1.2.3'\ndescription: token=fixture-secret useful\n---\nbody-private"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{CaptureContent: "preview", MaxChars: 1000}
	for _, tc := range []struct {
		name string
		args any
	}{
		{"Read", map[string]any{"file_path": path}},
		{"mcp__qw-builtin__mcp_workspace_bash", map[string]any{"command": "cat -- '" + path + "'"}},
	} {
		skill := skillFor(tc.name, tc.args, "tool-1", cfg)
		if skill == nil || skill.Name != "canonical-demo" || skill.Version != "1.2.3" || skill.Path != path || skill.SourceType != "" || skill.CallID != "tool-1" {
			t.Fatalf("metadata: %+v", skill)
		}
		encoded, _ := json.Marshal(skill)
		if strings.Contains(string(encoded), "fixture-secret") || strings.Contains(string(encoded), "body-private") {
			t.Fatal("metadata leaked secret or document body")
		}
	}
	skill := skillFor("Skill", map[string]any{"skill": "plugin:demo"}, "native-1", cfg)
	if skill == nil || skill.Name != "plugin:demo" || skill.Path != "" || skill.SourceType != "" || skill.Version != "" {
		t.Fatalf("native skill fabricated metadata: %+v", skill)
	}
	cfg.CaptureContent = "none"
	if skillFor("Read", map[string]any{"file_path": path}, "none", cfg).Description != "" {
		t.Fatal("none leaked description")
	}
}

func TestSkillRejectsMentionsAmbiguousAndUnsupportedShapes(t *testing.T) {
	cfg := config.Config{CaptureContent: "preview"}
	for _, tc := range []struct {
		name string
		args any
	}{
		{"Skill", map[string]any{"name": "demo"}},
		{"Skill", map[string]any{"skill": "../demo"}},
		{"Skill", map[string]any{"skill": "demo one"}},
		{"Read", map[string]any{"file_path": "relative/SKILL.md"}},
		{"Read", map[string]any{"path": "/demo/SKILL.md"}},
		{"Read", map[string]any{"file_path": "/demo/readme.md", "description": "/demo/SKILL.md"}},
		{"List", map[string]any{"file_path": "/demo/SKILL.md"}},
		{"arbitrary", map[string]any{"command": "cat /demo/SKILL.md"}},
		{"mcp__qw-builtin__mcp_workspace_bash", map[string]any{"command": "echo /demo/SKILL.md"}},
		{"mcp__qw-builtin__mcp_workspace_bash", map[string]any{"command": "ls /demo/SKILL.md"}},
		{"mcp__qw-builtin__mcp_workspace_bash", map[string]any{"command": "cat /demo/SKILL.md /other/SKILL.md"}},
		{"mcp__qw-builtin__mcp_workspace_bash", map[string]any{"command": "cat /demo/SKILL.md && true"}},
		{"mcp__qw-builtin__mcp_workspace_bash", map[string]any{"command": "cat $(pwd)/SKILL.md"}},
		{"mcp__qw-builtin__mcp_workspace_bash", map[string]any{"command": "cat '/demo/SKILL.md'; echo no"}},
		{"mcp__qw-builtin__mcp_workspace_bash", map[string]any{"command": "cat /demo/SKILL.md > /tmp/out"}},
		{"mcp__qw-builtin__mcp_workspace_bash", map[string]any{"command": "cat /demo/SKILL.md\ncat /other/SKILL.md"}},
	} {
		if got := skillFor(tc.name, tc.args, "tool", cfg); got != nil {
			t.Errorf("false positive %s %v: %+v", tc.name, tc.args, got)
		}
	}
}

func TestSkillMetadataFailsOpen(t *testing.T) {
	for _, path := range []string{`\\server\share\SKILL.md`, `\\?\C:\skills\SKILL.md`, `\\.\pipe\SKILL.md`, "//server/share/SKILL.md", "relative/SKILL.md"} {
		if localSkillPath(path) || skillMetadata(path) != nil {
			t.Fatalf("nonlocal path accepted: %s", path)
		}
	}
	path := filepath.Join(t.TempDir(), "SKILL.md")
	for _, body := range []string{
		"no frontmatter", "---\nname: demo", "---\nname: first\nname: second\n---", "---\nversion: [1,2]\n---", "---\ndescription: |\n  body\n---", strings.Repeat("x", skillMetadataLimit+1),
	} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if meta := skillMetadata(path); len(meta) != 0 {
			t.Fatalf("accepted malformed metadata: %v", meta)
		}
	}
	if skillMetadata(filepath.Join(t.TempDir(), "missing")) != nil || skillMetadata(t.TempDir()) != nil {
		t.Fatal("nonregular/missing metadata accepted")
	}
	link := filepath.Join(t.TempDir(), "SKILL.md")
	if err := os.Symlink(path, link); err == nil && skillMetadata(link) != nil {
		t.Fatal("symlink metadata followed")
	}
	// Invocation remains observable when its file has disappeared at replay.
	missing := filepath.Join(t.TempDir(), "demo", "SKILL.md")
	skill := skillFor("Read", map[string]any{"file_path": missing}, "missing", config.Config{})
	if skill == nil || skill.Name != "demo" || skill.Version != "" {
		t.Fatal("missing metadata broke attempted read evidence")
	}
}

func TestSkillParentageAndFailureStatus(t *testing.T) {
	for _, failure := range []bool{false, true} {
		in, cfg, events := fixture(t)
		body, _ := os.ReadFile(in.TranscriptPath)
		text := strings.Replace(string(body), `"name":"mcp__qw-builtin__mcp_workspace_bash","input":{"command":"printf TOOL_OK"}`, `"name":"Skill","input":{"skill":"demo"}`, 1)
		if failure {
			text = strings.Replace(text, `"content":"TOOL_OK"`, `"content":"TOOL_OK","is_error":true`, 1)
		}
		in.TranscriptPath = filepath.Join(t.TempDir(), "skill.jsonl")
		if err := os.WriteFile(in.TranscriptPath, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
		turns, pending, err := Read(in, cfg, events)
		if err != nil || pending || len(turns) != 2 {
			t.Fatalf("read: %v %v", err, pending)
		}
		tool := turns[1].ToolCalls[0]
		if tool.Skill == nil || tool.Skill.Status != tool.ResultStatus || failure && tool.Skill.ErrorType != "tool_error" {
			t.Fatalf("skill result: %+v", tool)
		}
		spans := (semantic.Builder{}).Build(turns[1])
		var toolID string
		found := false
		for _, span := range spans {
			if span.Name == "tool:Skill" {
				toolID = span.SpanID
			}
			if span.Name == "skill:demo" {
				found = true
				if toolID == "" || span.ParentID != toolID || failure && span.Attributes["error.type"] != "tool_error" {
					t.Fatalf("skill parent/status: %+v", span)
				}
			}
		}
		if !found {
			t.Fatal("missing skill span")
		}
	}
}

func TestSkillUserSourceRequiresCanonicalContainment(t *testing.T) {
	profile := t.TempDir()
	root := filepath.Join(profile, "skills")
	paths := []string{filepath.Join(root, "demo", "SKILL.md"), filepath.Join(profile, "skills-other", "demo", "SKILL.md"), filepath.Join(t.TempDir(), "external", "SKILL.md")}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\nname: demo\n---\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Config{ProfileDir: profile, CaptureContent: "none"}
	for i, path := range paths {
		want := ""
		if i == 0 {
			want = "user"
		}
		for _, name := range []string{"Read", "mcp__qw-builtin__mcp_workspace_bash"} {
			args := map[string]any{"file_path": path, "command": "cat '" + path + "'"}
			skill := skillFor(name, args, "test", cfg)
			if skill == nil || skill.SourceType != want {
				t.Fatalf("source %s: %+v want %q", path, skill, want)
			}
		}
	}
	if skillFor("Skill", map[string]any{"skill": "demo"}, "native", cfg).SourceType != "" {
		t.Fatal("name-only native invocation inferred a source")
	}
	linkDir := filepath.Join(root, "escape")
	if err := os.Symlink(filepath.Dir(paths[2]), linkDir); err == nil {
		skill := skillFor("Read", map[string]any{"file_path": filepath.Join(linkDir, "SKILL.md")}, "escaped", cfg)
		if skill == nil || skill.SourceType != "" {
			t.Fatal("escaping parent symlink classified as user")
		}
	}
	fileLinkDir := filepath.Join(root, "file-escape")
	if err := os.MkdirAll(fileLinkDir, 0700); err != nil {
		t.Fatal(err)
	}
	fileLink := filepath.Join(fileLinkDir, "SKILL.md")
	if err := os.Symlink(paths[2], fileLink); err == nil && skillSource(fileLink, profile) != "" {
		t.Fatal("escaping file symlink classified as user")
	}
	if skillSource(filepath.Join(root, "missing", "SKILL.md"), profile) != "" {
		t.Fatal("missing file fabricated canonical source")
	}
}
