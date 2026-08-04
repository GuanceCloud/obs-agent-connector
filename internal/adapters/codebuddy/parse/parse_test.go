package parse

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	codebuddyconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/codebuddy/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/metrics"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

func fixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("testdata", name, "index.json"))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func testConfig(capture string) codebuddyconfig.Config {
	return codebuddyconfig.Config{CaptureContent: capture, MaxChars: 4000, ResourceAttributes: map[string]any{}}
}

func TestReadCompletedTurnAndBuildSharedTelemetry(t *testing.T) {
	turns, pending, diagnostics, err := Read(HookInput{Event: "Stop", SessionID: "session-1", GenerationID: "generation-1", TranscriptPath: fixture(t, "normal"), Model: "synthetic-model", Version: "4.10.4"}, testConfig("preview"))
	if err != nil {
		t.Fatal(err)
	}
	if pending || len(turns) != 1 || diagnostics.TerminalTurns != 1 {
		t.Fatalf("turns=%d pending=%v diagnostics=%+v", len(turns), pending, diagnostics)
	}
	turn := turns[0]
	if turn.Usage.InputTokens != 120 || turn.Usage.OutputTokens != 32 || turn.Usage.CacheReadTokens != 20 || turn.Usage.ReasoningTokens != 4 {
		t.Fatalf("usage=%+v", turn.Usage)
	}
	if len(turn.LLMCalls) != 1 || len(turn.ToolCalls) != 1 || len(turn.AssistantOutputs) != 1 {
		t.Fatalf("unexpected turn shape: %+v", turn)
	}
	if turn.LLMCalls[0].ExtraAttributes["timing.source"] != "inferred" || turn.ToolCalls[0].ExtraAttributes["timing.source"] != "inferred" {
		t.Fatal("inferred timing marker missing")
	}
	spans := (semantic.Builder{ScopeName: "test", ScopeVersion: "test"}).Build(turn)
	if len(spans) != 4 || spans[0].Name != "invoke_agent" {
		t.Fatalf("spans=%+v", spans)
	}
	for _, span := range spans[1:] {
		if span.ParentID != spans[0].SpanID {
			t.Fatalf("%s parent=%s", span.Name, span.ParentID)
		}
		if span.StartTimeUnixNano < spans[0].StartTimeUnixNano || span.EndTimeUnixNano > spans[0].EndTimeUnixNano {
			t.Fatalf("%s outside root", span.Name)
		}
	}
	points := metrics.Build(spans)
	counts := map[string]int{}
	for _, point := range points {
		counts[point.Name]++
	}
	if counts["gen_ai.workflow.duration"] != 1 || counts["gen_ai.agent.operation.count"] != 2 || counts["gen_ai.agent.operation.duration"] != 2 || counts["gen_ai.client.token.usage"] != 2 {
		t.Fatalf("metric counts=%v", counts)
	}
	tracePayload := proto.EncodeExportTraceServiceRequest(otlp.SpansToProtoRequest(spans))
	decodedTraces, err := proto.DecodeExportTraceServiceRequest(tracePayload)
	if err != nil || len(decodedTraces.ResourceSpans) != 1 || len(decodedTraces.ResourceSpans[0].ScopeSpans[0].Spans) != len(spans) {
		t.Fatalf("trace protobuf round trip failed: %#v err=%v", decodedTraces, err)
	}
	metricPayload := proto.EncodeExportMetricsServiceRequest(otlp.MetricsToProtoRequest(points))
	decodedMetrics, err := proto.DecodeExportMetricsServiceRequest(metricPayload)
	if err != nil || len(decodedMetrics.ResourceMetrics) != 1 || len(decodedMetrics.ResourceMetrics[0].ScopeMetrics[0].Metrics) != 4 {
		t.Fatalf("metric protobuf round trip failed: %#v err=%v", decodedMetrics, err)
	}
	body, _ := json.Marshal(spans)
	if strings.Contains(string(body), "fixture-secret-value") || !strings.Contains(string(body), "REDACTED") {
		t.Fatalf("privacy failure: %s", body)
	}
}

func TestReadDoesNotUploadRunningStopAndCancelsOnSessionEnd(t *testing.T) {
	input := HookInput{Event: "Stop", SessionID: "session-2", GenerationID: "generation-2", TranscriptPath: fixture(t, "cancelled")}
	turns, pending, _, err := Read(input, testConfig("none"))
	if err != nil || !pending || len(turns) != 0 {
		t.Fatalf("stop turns=%+v pending=%v err=%v", turns, pending, err)
	}
	input.Event = "SessionEnd"
	turns, pending, _, err = Read(input, testConfig("none"))
	if err != nil || pending || len(turns) != 1 || turns[0].FinalStatus != "cancelled" {
		t.Fatalf("session end turns=%+v pending=%v err=%v", turns, pending, err)
	}
}

func TestReadRejectsUnknownTranscriptAndMissingGeneration(t *testing.T) {
	_, _, _, err := Read(HookInput{Event: "Stop", SessionID: "session", GenerationID: "missing", TranscriptPath: fixture(t, "normal")}, testConfig("none"))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, err = Read(HookInput{Event: "Stop", SessionID: "session", TranscriptPath: filepath.Join(t.TempDir(), "transcript.txt")}, testConfig("none"))
	if err == nil || !strings.Contains(err.Error(), "expected index.json") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestCaptureNoneOmitsContent(t *testing.T) {
	turns, _, _, err := Read(HookInput{Event: "Stop", SessionID: "session-1", GenerationID: "generation-1", TranscriptPath: fixture(t, "normal")}, testConfig("none"))
	if err != nil {
		t.Fatal(err)
	}
	spans := (semantic.Builder{}).Build(turns[0])
	body, _ := json.Marshal(spans)
	for _, text := range []string{"Inspect the synthetic project", "npm test", "synthetic fixture passed"} {
		if strings.Contains(string(body), text) {
			t.Fatalf("content leaked with capture none: %s", body)
		}
	}
}

func TestToolFailureMapsErrorStatus(t *testing.T) {
	root := t.TempDir()
	messages := filepath.Join(root, "messages")
	if err := os.MkdirAll(messages, 0o700); err != nil {
		t.Fatal(err)
	}
	index := `{"messages":[{"id":"u","isComplete":true},{"id":"a","isComplete":true},{"id":"t","isComplete":true}],"requests":[{"id":"failed-tool","type":"craft","messages":["u","a","t"],"state":"complete","startedAt":1767225600000}]}`
	fixtures := map[string]string{
		"index.json":      index,
		"messages/u.json": `{"role":"user","createdAt":"2026-01-01T00:00:00Z","message":{"role":"user","content":[{"type":"text","text":"Run a failing synthetic tool"}]}}`,
		"messages/a.json": `{"role":"assistant","createdAt":"2026-01-01T00:00:01Z","message":{"role":"assistant","content":[{"type":"tool-call","toolCallId":"call-fail","toolName":"synthetic_tool","args":{"command":"fail"}}]}}`,
		"messages/t.json": `{"role":"tool","createdAt":"2026-01-01T00:00:02Z","message":{"role":"tool","content":[{"type":"tool-result","toolCallId":"call-fail","toolName":"synthetic_tool","result":{"stderr":"synthetic failure"},"isError":true}]}}`,
	}
	for name, body := range fixtures {
		if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	turns, pending, _, err := Read(HookInput{Event: "Stop", SessionID: "error-session", GenerationID: "failed-tool", TranscriptPath: filepath.Join(root, "index.json")}, testConfig("preview"))
	if err != nil || pending || len(turns) != 1 {
		t.Fatalf("turns=%+v pending=%v err=%v", turns, pending, err)
	}
	tool := turns[0].ToolCalls[0]
	if tool.Status != "error" || tool.ResultStatus != "error" || tool.ErrorType != "_OTHER" {
		t.Fatalf("tool=%+v", tool)
	}
}
