package preview

import "testing"

func TestTextNormalizesWhitespaceAndRedactsSecrets(t *testing.T) {
	value := "hello\nworld sk-secret-value-123456"
	text := Text(value, 200)
	if text == "" {
		t.Fatal("expected preview text")
	}
	if text != "hello world [REDACTED]" {
		t.Fatalf("unexpected preview text %q", text)
	}
}

func TestAttrAndLengthSkipBlankValues(t *testing.T) {
	if value := Attr("   \n\t ", 10); value != nil {
		t.Fatalf("expected blank preview attr to be nil, got %#v", value)
	}
	if value := Length("   \n\t ", 10); value != nil {
		t.Fatalf("expected blank preview length to be nil, got %#v", value)
	}
}

func TestPairBuildsInputAndOutputPreview(t *testing.T) {
	input, output := Pair(" hello ", "world\nworld", 100)
	if input != "hello" || output != "world world" {
		t.Fatalf("unexpected preview pair input=%q output=%q", input, output)
	}
}
