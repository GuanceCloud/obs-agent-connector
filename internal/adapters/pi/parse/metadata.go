package parse

import (
	"strconv"
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

// Length counts Unicode text characters before clipping when the extension
// supplied that fact. Older snapshots fall back to the available text.
func messageLength(m Message) int {
	if m.TextLength != nil {
		return max(0, *m.TextLength)
	}
	return len([]rune(textContent(m.Content)))
}

func outputKind(m Message) string {
	if parts, ok := m.Content.([]any); ok {
		for _, raw := range parts {
			if p, ok := raw.(map[string]any); ok && p["type"] == "toolCall" {
				return "tool_call"
			}
		}
	}
	if m.HasText || textContent(m.Content) != "" {
		return "text"
	}
	return ""
}

func skillStatus(status string) string {
	if status == "ok" {
		return "completed"
	}
	return status
}

// Read only unambiguous scalar frontmatter from the actual read-tool result.
// Unsupported YAML (including block scalars) is omitted rather than guessed.
// Do not open arbitrary paths in the collector or add a YAML dependency.
func skillMetadata(text string, limit int) (description, version string) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return
	}
	end := strings.Index(text[4:], "\n---")
	if end < 0 {
		return
	}
	for _, line := range strings.Split(text[4:4+end], "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok || (key != "description" && key != "version") {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" || strings.ContainsAny(value[:1], "|>!&*[{#") {
			continue
		}
		if strings.HasPrefix(value, "\"") {
			decoded, err := strconv.Unquote(value)
			if err != nil {
				continue
			}
			value = decoded
		} else if strings.HasPrefix(value, "'") {
			if len(value) < 2 || !strings.HasSuffix(value, "'") {
				continue
			}
			value = strings.ReplaceAll(value[1:len(value)-1], "''", "'")
		} else if strings.Contains(value, " #") {
			value = strings.SplitN(value, " #", 2)[0]
		}
		value = privacy.Preview(value, limit)
		if key == "description" {
			description = value
		} else {
			version = value
		}
	}
	return
}
