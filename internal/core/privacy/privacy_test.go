package privacy

import (
	"strings"
	"testing"
)

func TestSanitizeRedactsSecretKeysAndBearerValues(t *testing.T) {
	value := Sanitize(map[string]any{
		"authorization": "Bearer secret-value",
		"nested": map[string]any{
			"message": "use Bearer abcdefghijklmno",
			"token":   "top-secret",
		},
	}, 100)
	text := Text(value, 1000)
	if strings.Contains(text, "secret-value") || strings.Contains(text, "abcdefghijklmno") || strings.Contains(text, "top-secret") {
		t.Fatalf("secret leaked: %s", text)
	}
}

func TestTextClipsByRunes(t *testing.T) {
	if got := Text("Καλημέρα", 4); got != "Καλη" {
		t.Fatalf("unexpected clipped text %q", got)
	}
}
