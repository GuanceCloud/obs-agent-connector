package preview

import (
	"strings"
	"unicode/utf8"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

func Text(value any, maxChars int) string {
	if value == nil || maxChars < 0 {
		return ""
	}
	return strings.TrimSpace(privacy.Preview(value, maxChars))
}

func Attr(value any, maxChars int) any {
	text := Text(value, maxChars)
	if text == "" {
		return nil
	}
	return text
}

func Length(value any, maxChars int) any {
	text := Text(value, maxChars)
	if text == "" {
		return nil
	}
	return utf8.RuneCountInString(text)
}

func Pair(input, output any, maxChars int) (string, string) {
	return Text(input, maxChars), Text(output, maxChars)
}

func Present(value any, maxChars int) bool {
	return Attr(value, maxChars) != nil
}
