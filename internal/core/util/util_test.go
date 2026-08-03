package util

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncatePreservesUTF8Boundaries(t *testing.T) {
	got := Truncate("éüñø", 2)
	if !utf8.ValidString(got) {
		t.Fatalf("expected valid UTF-8, got %q", got)
	}
	if !strings.HasPrefix(got, "éü\n") || !strings.HasSuffix(got, "[truncated 2 chars]") {
		t.Fatalf("unexpected truncated text: %q", got)
	}
}

func TestClipValuePreservesStructuredToolResults(t *testing.T) {
	got := ClipValue(map[string]any{
		"stdout": "line one\nline two",
		"items":  []any{"first", map[string]any{"value": "second"}},
	}, 5)

	result, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %#v", got)
	}
	if _, ok := result["items"].([]any); !ok {
		t.Fatalf("expected array structure to be preserved, got %#v", result["items"])
	}
	if stdout, ok := result["stdout"].(string); !ok || !strings.HasPrefix(stdout, "line ") {
		t.Fatalf("unexpected clipped stdout: %#v", result["stdout"])
	}
}
