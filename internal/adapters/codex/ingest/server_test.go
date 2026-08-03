package ingest

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
)

func TestServerAcceptsTraceProtobufAndStoresNormalizedSpans(t *testing.T) {
	dataDir := t.TempDir()
	server := httptest.NewServer(NewHandler(ServerOptions{
		DataDir:   dataDir,
		PublicKey: "pk-test",
		SecretKey: "sk-test",
	}))
	defer server.Close()

	request := otlp.ExportTraceServiceRequest{
		ResourceSpans: []otlp.ResourceSpans{
			{
				Resource: otlp.Resource{
					Attributes: []otlp.KeyValue{
						{Key: "service.name", Value: stringValue("codex")},
						{Key: "telemetry.sdk.language", Value: stringValue("nodejs")},
					},
				},
				ScopeSpans: []otlp.ScopeSpans{
					{
						Scope: otlp.InstrumentationScope{Name: "gtrace-otel-test"},
						Spans: []otlp.Span{
							{
								TraceID:           bytes.Repeat([]byte{0xaa}, 16),
								SpanID:            bytes.Repeat([]byte{0x11}, 8),
								Name:              "invoke_agent",
								Kind:              1,
								StartTimeUnixNano: 1800000000000000000,
								EndTimeUnixNano:   1800000000100000000,
								Attributes: []otlp.KeyValue{
									{Key: "trace_name", Value: stringValue("Codex Turn")},
									{Key: "gen_ai.conversation.id", Value: stringValue("codex-session-1")},
									{Key: "span_type", Value: stringValue("agent")},
									{Key: "input_preview", Value: stringValue("hello")},
									{Key: "output_preview", Value: stringValue("done")},
								},
							},
							{
								TraceID:           bytes.Repeat([]byte{0xaa}, 16),
								SpanID:            bytes.Repeat([]byte{0x22}, 8),
								ParentSpanID:      bytes.Repeat([]byte{0x11}, 8),
								Name:              "llm",
								Kind:              1,
								StartTimeUnixNano: 1800000000010000000,
								EndTimeUnixNano:   1800000000050000000,
								Attributes: []otlp.KeyValue{
									{Key: "span_type", Value: stringValue("llm")},
									{Key: "gen_ai.request.model", Value: stringValue("gpt-test")},
									{Key: "gen_ai.response.model", Value: stringValue("gpt-test")},
									{Key: "gen_ai.usage.input_tokens", Value: intValue(10)},
									{Key: "gen_ai.usage.output_tokens", Value: intValue(20)},
									{Key: "gen_ai.usage.cache_read.input_tokens", Value: intValue(2)},
									{Key: "gen_ai.usage.reasoning.output_tokens", Value: intValue(3)},
								},
							},
							{
								TraceID:           bytes.Repeat([]byte{0xaa}, 16),
								SpanID:            bytes.Repeat([]byte{0x33}, 8),
								ParentSpanID:      bytes.Repeat([]byte{0x11}, 8),
								Name:              "tool:exec_command",
								Kind:              1,
								StartTimeUnixNano: 1800000000060000000,
								EndTimeUnixNano:   1800000000080000000,
								Attributes: []otlp.KeyValue{
									{Key: "span_type", Value: stringValue("tool")},
									{Key: "reason", Value: stringValue("command failed")},
								},
							},
						},
					},
				},
			},
		},
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/public/otel/v1/traces", bytes.NewReader(proto.EncodeExportTraceServiceRequest(request)))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("pk-test:sk-test")))
	req.Header.Set("Content-Type", "application/x-protobuf")
	req.Header.Set("x-gtrace-sdk-name", "otel-test")
	req.Header.Set("x-gtrace-sdk-version", "1.0.0")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	if resp.Header.Get("x-gtrace-span-count") != "3" {
		t.Fatalf("unexpected span count header: %s", resp.Header.Get("x-gtrace-span-count"))
	}

	store := NewFileStore(dataDir)
	spans, err := store.ListSpans(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(spans) != 3 {
		t.Fatalf("expected 3 spans, got %d", len(spans))
	}
	if spans[0].GTrace.Observation.Type != "agent" || spans[1].GTrace.Observation.Type != "llm" || spans[2].GTrace.Observation.Type != "tool" {
		t.Fatalf("unexpected observation types: %#v", spans)
	}
	if spans[0].GTrace.Trace.SessionID != "codex-session-1" {
		t.Fatalf("unexpected session id: %#v", spans[0].GTrace.Trace)
	}
	if spans[1].GTrace.Observation.ModelName != "gpt-test" {
		t.Fatalf("unexpected model name: %#v", spans[1].GTrace.Observation)
	}
	usage := spans[1].GTrace.Observation.Usage
	if asInt(usage["input"]) != 10 || asInt(usage["output"]) != 20 || asInt(usage["total"]) != 30 || asInt(usage["cache_read_input_tokens"]) != 2 || asInt(usage["reasoning_tokens"]) != 3 {
		t.Fatalf("unexpected usage: %#v", usage)
	}
	if spans[2].Attributes["reason"] != "command failed" {
		t.Fatalf("unexpected tool reason: %#v", spans[2].Attributes)
	}

	batches, err := os.ReadDir(filepath.Join(dataDir, "batches"))
	if err != nil {
		t.Fatal(err)
	}
	if len(batches) != 1 {
		t.Fatalf("expected 1 batch file, got %d", len(batches))
	}
}

func TestServerAcceptsTraceJSONUsedByOtelExporter(t *testing.T) {
	dataDir := t.TempDir()
	server := httptest.NewServer(NewHandler(ServerOptions{
		DataDir:   dataDir,
		PublicKey: "pk-test",
		SecretKey: "sk-test",
	}))
	defer server.Close()

	payload := map[string]any{
		"resourceSpans": []any{
			map[string]any{
				"resource": map[string]any{
					"attributes": []any{jsonAttr("service.name", "codex")},
				},
				"scopeSpans": []any{
					map[string]any{
						"scope": map[string]any{"name": "gtrace-otel-test"},
						"spans": []any{
							map[string]any{
								"traceId":           "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
								"spanId":            "bbbbbbbbbbbbbbbb",
								"parentSpanId":      "",
								"name":              "Codex Turn",
								"kind":              1,
								"startTimeUnixNano": "1800000000000000000",
								"endTimeUnixNano":   "1800000000100000000",
								"attributes": []any{
									jsonAttr("gen_ai.conversation.id", "codex-json-session"),
									jsonAttr("gen_ai.request.model", "gpt-json"),
									jsonAttr("gen_ai.response.model", "gpt-json"),
								},
								"status": map[string]any{"code": 0},
							},
						},
					},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	req, err := http.NewRequest(http.MethodPost, server.URL+"/api/public/otel/v1/traces", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("pk-test:sk-test")))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-gtrace-sdk-name", "otel-json-test")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	if contentType := resp.Header.Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("unexpected content type: %s", contentType)
	}

	listResp, err := http.Get(server.URL + "/traces?limit=1")
	if err != nil {
		t.Fatal(err)
	}
	defer listResp.Body.Close()
	var listed struct {
		Data []StoredSpan `json:"data"`
	}
	if err := json.NewDecoder(listResp.Body).Decode(&listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 1 {
		t.Fatalf("expected 1 listed span, got %d", len(listed.Data))
	}
	if listed.Data[0].TraceID != "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || listed.Data[0].SpanID != "bbbbbbbbbbbbbbbb" {
		t.Fatalf("unexpected listed span ids: %#v", listed.Data[0])
	}
	if listed.Data[0].GTrace.Trace.SessionID != "codex-json-session" {
		t.Fatalf("unexpected listed session id: %#v", listed.Data[0].GTrace.Trace)
	}
}

func stringValue(value string) otlp.AnyValue {
	return otlp.AnyValue{StringValue: &value}
}

func intValue(value int64) otlp.AnyValue {
	return otlp.AnyValue{IntValue: &value}
}

func jsonAttr(key, value string) map[string]any {
	return map[string]any{
		"key":   key,
		"value": map[string]any{"stringValue": value},
	}
}

func asInt(value any) int64 {
	switch current := value.(type) {
	case float64:
		return int64(current)
	case int64:
		return current
	case int:
		return int64(current)
	default:
		return 0
	}
}
