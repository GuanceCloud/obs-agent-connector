package parse

import (
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
)

// messageValue maps the OMP wire format to the GenAI input/output message
// schemas. Sanitize content before constructing the schema, so clipping cannot
// truncate role names, part types or other structural fields.
func messageValue(m Message, output bool, cfg config.Config) map[string]any {
	if cfg.CaptureContent == "none" {
		return nil
	}
	role := m.Role
	switch role {
	case "user", "assistant", "system", "developer":
	case "toolResult", "tool":
		role = "tool"
	case "custom":
		if m.Attribution != "user" {
			return nil
		}
		role = "user"
	default:
		return nil
	}
	parts := []any{}
	if role == "tool" {
		response := textContent(m.Content)
		if response != "" {
			part := map[string]any{"type": "tool_call_response", "response": privacy.Sanitize(response, cfg.MaxChars)}
			if m.ToolCallID != "" {
				part["id"] = m.ToolCallID
			}
			parts = append(parts, part)
		}
	} else if text, ok := m.Content.(string); ok {
		if strings.TrimSpace(text) != "" {
			parts = append(parts, map[string]any{"type": "text", "content": privacy.Sanitize(text, cfg.MaxChars)})
		}
	} else if content, ok := m.Content.([]any); ok {
		for _, raw := range content {
			p, _ := raw.(map[string]any)
			switch p["type"] {
			case "text", "thinking":
				kind, field := "text", "text"
				if p["type"] == "thinking" {
					kind, field = "reasoning", "thinking"
				}
				if text, ok := p[field].(string); ok && strings.TrimSpace(text) != "" {
					parts = append(parts, map[string]any{"type": kind, "content": privacy.Sanitize(text, cfg.MaxChars)})
				}
			case "toolCall":
				name, _ := p["name"].(string)
				if name == "" {
					continue
				}
				part := map[string]any{"type": "tool_call", "name": privacy.Sanitize(name, cfg.MaxChars)}
				if id, ok := p["id"].(string); ok && id != "" {
					part["id"] = id
				}
				if args := p["arguments"]; args != nil {
					part["arguments"] = privacy.Sanitize(args, cfg.MaxChars)
				}
				parts = append(parts, part)
			}
		}
	}
	if len(parts) == 0 {
		return nil
	}
	result := map[string]any{"role": role, "parts": parts}
	if output {
		result["finish_reason"] = finishReason(m.StopReason)
	}
	return result
}

func messageList(m Message, output bool, cfg config.Config) any {
	if value := messageValue(m, output, cfg); value != nil {
		return []any{value}
	}
	return nil
}

func finishReason(reason string) string {
	switch reason {
	case "toolUse":
		return "tool_call"
	case "aborted":
		return "cancelled"
	default:
		return reason
	}
}
