package util

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

func IsPrimitive(value any) bool {
	switch value.(type) {
	case string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return true
	default:
		return false
	}
}

func ToText(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	if IsPrimitive(value) {
		return fmt.Sprint(value)
	}
	encoded, err := json.Marshal(value)
	if err == nil {
		return string(encoded)
	}
	return fmt.Sprint(value)
}

func Truncate(value string, maxChars int) string {
	if maxChars < 0 {
		maxChars = 0
	}
	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}
	return fmt.Sprintf("%s\n...[truncated %d chars]", string(runes[:maxChars]), len(runes)-maxChars)
}

func ClipValue(value any, maxChars int) any {
	switch current := value.(type) {
	case string:
		return Truncate(current, maxChars)
	case []any:
		out := make([]any, len(current))
		for i, item := range current {
			out[i] = ClipValue(item, maxChars)
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(current))
		for key, item := range current {
			out[key] = ClipValue(item, maxChars)
		}
		return out
	default:
		return value
	}
}

func ReadStdinJSON(target any) error {
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return fmt.Errorf("empty hook stdin")
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("failed to parse hook stdin: %w", err)
	}
	return nil
}

func RealpathEqual(left, right string) bool {
	leftResolved, err := filepath.EvalSymlinks(left)
	if err != nil {
		return false
	}
	rightResolved, err := filepath.EvalSymlinks(right)
	if err != nil {
		return false
	}
	return leftResolved == rightResolved
}
