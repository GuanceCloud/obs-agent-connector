package parse

import (
	"unicode/utf8"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

// The budget is per call, not per fragment. Only native thinking text enters
// this projection; signatures and opaque/redacted blocks never do.
type reasoningCapture struct{ chars int }

func (r *reasoningCapture) add(text string, maxChars int) any {
	if maxChars <= 0 {
		maxChars = 20_000
	}
	remaining := maxChars - r.chars
	if text == "" || remaining <= 0 {
		return nil
	}
	text = privacy.Text(text, remaining)
	r.chars += utf8.RuneCountInString(text)
	return map[string]any{"type": "reasoning", "content": text}
}
