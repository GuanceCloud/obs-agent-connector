package parse

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

const skillMetadataLimit = 64 << 10

// Only native Skill/Read argument shapes and the verified workspace Bash tool
// are recognized. Arbitrary tool arguments, lists and textual mentions are not.
func skillFor(name string, args any, callID string, cfg config.Config) *model.SkillUse {
	return skillForAt(name, args, callID, cfg, "")
}

func skillForAt(name string, args any, callID string, cfg config.Config, cwd string) *model.SkillUse {
	a := object(args)
	if name == "Skill" {
		n := strings.TrimSpace(str(a["skill"]))
		if !skillIdentifier(n) {
			return nil
		}
		return &model.SkillUse{Name: privacy.Text(n, cfg.MaxChars), CallID: callID}
	}
	var path string
	switch name {
	case "Read":
		path = str(a["file_path"])
	case "mcp__qw-builtin__mcp_workspace_bash":
		path = singleCatPath(str(a["command"]))
	default:
		return nil
	}
	path = resolveSkillPath(path, cwd)
	if path == "" || filepath.Base(path) != "SKILL.md" {
		return nil
	}
	path = filepath.Clean(path)
	n := filepath.Base(filepath.Dir(path))
	if !skillIdentifier(n) {
		return nil
	}
	skill := &model.SkillUse{Name: privacy.Text(n, cfg.MaxChars), CallID: callID, Path: privacy.Text(path, cfg.MaxChars)}
	meta := skillMetadata(path)
	if skillIdentifier(meta["name"]) {
		skill.Name = privacy.Text(meta["name"], cfg.MaxChars)
	}
	skill.Version = privacy.Text(meta["version"], cfg.MaxChars)
	if cfg.CaptureContent != "none" {
		skill.Description = privacy.Text(meta["description"], cfg.MaxChars)
	}
	skill.SourceType = skillSource(path, cfg.ProfileDir)
	return skill
}

// Cwd is evidence from this exact tool's native Hooks, never process state or
// the latest session event. Conflicting/invalid matching observations fail closed.
func toolCwd(in HookInput, events []HookInput, toolID, name string, args any, request string, start, end int64) string {
	if in.SessionID == "" || in.TranscriptPath == "" || toolID == "" {
		return ""
	}
	var cwd string
	for _, ev := range events {
		if ev.Event != "PreToolUse" && ev.Event != "PostToolUse" && ev.Event != "PostToolUseFailure" {
			continue
		}
		if ev.SessionID != in.SessionID || ev.TranscriptPath != in.TranscriptPath || ev.ToolUseID != toolID || ev.ObservedAt < start || ev.ObservedAt > end {
			continue
		}
		if !localSkillPath(ev.Cwd) || ev.ToolName != "" && ev.ToolName != name || ev.InputKey != "" && ev.InputKey != ToolKey(name, args) || ev.RequestSetID != "" && ev.RequestSetID != request {
			return ""
		}
		candidate := filepath.Clean(ev.Cwd)
		if cwd != "" && cwd != candidate {
			return ""
		}
		cwd = candidate
	}
	return cwd
}

// Relative file evidence may resolve only below an existing, trusted Hook cwd.
// Both lexical traversal and parent-symlink escapes are rejected. Missing files
// can still describe attempted reads, but an unresolved parent is not guessed.
func resolveSkillPath(path, cwd string) string {
	if localSkillPath(path) {
		return filepath.Clean(path)
	}
	if path == "" || len(path) > 4096 || filepath.IsAbs(path) || strings.ContainsAny(path, "\\:\x00\r\n") || strings.HasPrefix(path, "~") || !localSkillPath(cwd) {
		return ""
	}
	// Cleaning a/../ before resolving a symlink would change filesystem meaning.
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if part == ".." {
			return ""
		}
	}
	root, err := filepath.EvalSymlinks(cwd)
	if err != nil || !localSkillPath(root) {
		return ""
	}
	target := filepath.Join(root, path)
	if !pathWithin(root, target) {
		return ""
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(target))
	if err != nil || !localSkillPath(parent) {
		return ""
	}
	target = filepath.Join(parent, filepath.Base(target))
	if !localSkillPath(target) || !pathWithin(root, target) {
		return ""
	}
	if info, err := os.Lstat(target); err == nil && !info.Mode().IsRegular() {
		return ""
	}
	return target
}

func pathWithin(root, target string) bool {
	rel, err := filepath.Rel(root, target)
	return err == nil && !filepath.IsAbs(rel) && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// This describes user-profile location, not authorship or original distribution.
// Resolve both sides before containment so sibling prefixes and escaping links
// cannot turn arbitrary resources into user-profile skills. No directory scan.
func skillSource(path, profile string) string {
	if !localSkillPath(path) || !localSkillPath(profile) {
		return ""
	}
	root, err := filepath.EvalSymlinks(filepath.Join(profile, "skills"))
	if err != nil || !localSkillPath(root) {
		return ""
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || !localSkillPath(canonical) {
		return ""
	}
	rel, err := filepath.Rel(root, canonical)
	if err != nil || filepath.IsAbs(rel) || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return "user"
}

func skillIdentifier(s string) bool {
	if s == "" || len(s) > 256 || s == "." || s == ".." {
		return false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune("-_.:", r) {
			return false
		}
	}
	return true
}

// This is deliberately not a shell interpreter: a single cat command reading
// one path is sufficient evidence when absolute or resolved with trusted cwd.
// Reject options, expansions, shell
// operators and multiple targets. Never execute or expand transcript commands.
func singleCatPath(command string) string {
	command = strings.TrimSpace(command)
	if !strings.HasPrefix(command, "cat ") && !strings.HasPrefix(command, "cat\t") {
		return ""
	}
	arg := strings.TrimSpace(command[3:])
	optionsEnded := false
	if strings.HasPrefix(arg, "-- ") || strings.HasPrefix(arg, "--\t") {
		optionsEnded = true
		arg = strings.TrimSpace(arg[2:])
	}
	if len(arg) >= 2 && (arg[0] == '\'' || arg[0] == '"') && arg[len(arg)-1] == arg[0] {
		quote := arg[0]
		arg = arg[1 : len(arg)-1]
		if strings.ContainsRune(arg, rune(quote)) {
			return ""
		}
	} else if strings.ContainsAny(arg, " \t\"'") {
		return ""
	}
	if strings.ContainsAny(arg, "\r\n;$`|&<>\\*?[]{}()") {
		return ""
	}
	if !optionsEnded && strings.HasPrefix(arg, "-") {
		return ""
	}
	return arg
}

func skillMetadata(path string) map[string]string {
	if !localSkillPath(path) {
		return nil
	}
	// Canonicalize parent links (for example macOS /var -> /private/var), but
	// never follow a final SKILL.md link or a Windows network/device target.
	parent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil || !localSkillPath(parent) {
		return nil
	}
	path = filepath.Join(parent, filepath.Base(path))
	// Reject links and devices before opening, then recheck file identity. The
	// platform opener also prevents FIFO replacement from blocking on POSIX.
	before, err := os.Lstat(path)
	if err != nil || !before.Mode().IsRegular() || before.Size() > skillMetadataLimit {
		return nil
	}
	f, err := openSkillMetadata(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(f, skillMetadataLimit+1))
	if err != nil || len(data) > skillMetadataLimit {
		return nil
	}
	return skillFrontmatter(string(data))
}

func localSkillPath(path string) bool {
	return len(path) <= 4096 && filepath.IsAbs(path) && !strings.HasPrefix(path, "//") && !strings.HasPrefix(path, `\`) && !strings.ContainsAny(path, "\x00\r\n")
}

// Accept a small flat scalar subset, not arbitrary YAML or embedded content.
// Unsupported/malformed frontmatter is omitted without affecting collection.
func skillFrontmatter(body string) map[string]string {
	lines := strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n")
	if len(lines) == 0 || lines[0] != "---" {
		return nil
	}
	meta := map[string]string{}
	for i, line := range lines[1:] {
		if i >= 100 {
			return nil
		}
		if line == "---" {
			return meta
		}
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || key != strings.TrimSpace(key) {
			return nil
		}
		if key != "name" && key != "description" && key != "version" {
			continue
		}
		if _, exists := meta[key]; exists {
			return nil
		}
		value = strings.TrimSpace(value)
		if len(value) >= 2 && (value[0] == '\'' || value[0] == '"') && value[len(value)-1] == value[0] {
			value = value[1 : len(value)-1]
		} else if strings.ContainsAny(value, "\"'[]{}|>&*!#") {
			return nil
		}
		meta[key] = value
	}
	return nil
}
