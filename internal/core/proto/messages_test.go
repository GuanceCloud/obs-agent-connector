package proto

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
)

func TestMessageAttributesJSONAndProtobufRoundTrip(t *testing.T) {
	messages := []any{map[string]any{"role": "assistant", "parts": []any{
		map[string]any{"type": "text", "content": "Running a command"},
		map[string]any{"type": "tool_call", "id": "call-1", "name": "shell", "arguments": map[string]any{"command": "printf OK"}},
	}}}
	encoded, err := json.Marshal(messages)
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{messages, string(encoded)} {
		request := otlp.SpansToProtoRequest([]model.Span{{Attributes: map[string]any{
			"gen_ai.input.messages": value, "gen_ai.output.messages": value,
			"gen_ai.tool.call.result": "OK", "custom.map": map[string]any{"value": "kept"},
			"custom.array": []any{"one", "two"},
		}}})
		decoded, err := DecodeExportTraceServiceRequest(EncodeExportTraceServiceRequest(request))
		if err != nil {
			t.Fatal(err)
		}
		attrs := decoded.ResourceSpans[0].ScopeSpans[0].Spans[0].Attributes
		if len(attrs) != 5 {
			t.Fatalf("expected all five attributes, got %d", len(attrs))
		}
		seen := make(map[string]bool)
		for _, attr := range attrs {
			if seen[attr.Key] {
				t.Fatalf("duplicate attribute %s", attr.Key)
			}
			seen[attr.Key] = true
			switch attr.Key {
			case "gen_ai.input.messages", "gen_ai.output.messages":
				if attr.Value.StringValue == nil || *attr.Value.StringValue != string(encoded) || attr.Value.ArrayValue != nil || attr.Value.KVListValue != nil {
					t.Fatalf("%s is not canonical JSON text: %+v", attr.Key, attr.Value)
				}
				var got any
				if err := json.Unmarshal([]byte(*attr.Value.StringValue), &got); err != nil || !reflect.DeepEqual(got, messages) {
					t.Fatalf("role/parts JSON round trip failed: %v", err)
				}
			case "gen_ai.tool.call.result":
				if attr.Value.StringValue == nil || *attr.Value.StringValue != "OK" {
					t.Fatal("tool stdout changed")
				}
			case "custom.map":
				if attr.Value.KVListValue == nil {
					t.Fatal("unrelated map was stringified")
				}
			case "custom.array":
				if attr.Value.ArrayValue == nil || len(attr.Value.ArrayValue.Values) != 2 {
					t.Fatal("unrelated array was stringified")
				}
			default:
				t.Fatalf("unexpected attribute %s", attr.Key)
			}
		}
	}
}
