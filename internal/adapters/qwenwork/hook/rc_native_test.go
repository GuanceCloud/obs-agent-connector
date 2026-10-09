package hook

import (
	"bytes"
	"compress/gzip"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
)

// TestRCNativeSynthetic is opt-in: supply only the approved RC synthetic transcript
// and its exact metadata journal directory. It never reads installed config, writes
// installed state, discovers other transcripts, or sends traffic off loopback.
func TestRCNativeSynthetic(t *testing.T) {
	transcript, journal := os.Getenv("QWEN_RC_TRANSCRIPT"), os.Getenv("QWEN_RC_JOURNAL")
	if transcript == "" || journal == "" {
		t.Skip("explicit synthetic transcript and journal required")
	}
	entries, err := os.ReadDir(journal)
	if err != nil {
		t.Fatal(err)
	}
	var events []parse.HookInput
	var stop parse.HookInput
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		body, err := os.ReadFile(filepath.Join(journal, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		var event parse.HookInput
		if err := json.Unmarshal(body, &event); err != nil {
			t.Fatal(err)
		}
		if event.TranscriptPath != transcript {
			t.Fatal("journal transcript mismatch")
		}
		events = append(events, event)
		if event.Event == "Stop" && event.ObservedAt > stop.ObservedAt {
			stop = event
		}
	}
	if stop.Event == "" {
		t.Fatal("native Stop missing")
	}
	for _, mode := range []string{"off", "on", "none"} {
		t.Run(mode, func(t *testing.T) {
			cfg, _ := setup(t)
			cfg.CaptureThinking = mode == "on" || mode == "none"
			cfg.CaptureContent = "full"
			if mode == "none" {
				cfg.CaptureContent = "none"
			}
			for _, event := range events {
				if event.SessionID != stop.SessionID {
					t.Fatal("journal session mismatch")
				}
				if err := record(cfg, event); err != nil {
					t.Fatal(err)
				}
			}
			var spans []otlp.Span
			metricTokens := map[string]float64{}
			traceRequests, metricRequests := 0, 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reader := io.Reader(r.Body)
				if r.Header.Get("Content-Encoding") == "gzip" {
					gz, err := gzip.NewReader(reader)
					if err != nil {
						t.Error(err)
						return
					}
					defer gz.Close()
					reader = gz
				}
				body, err := io.ReadAll(reader)
				if err != nil {
					t.Error(err)
					return
				}
				if strings.Contains(r.URL.Path, "metrics") {
					metricRequests++
					request, err := proto.DecodeExportMetricsServiceRequest(body)
					if err != nil {
						t.Error(err)
						return
					}
					for _, resource := range request.ResourceMetrics {
						for _, scope := range resource.ScopeMetrics {
							for _, metric := range scope.Metrics {
								if metric.Histogram != nil && metric.Name == "gen_ai.client.token.usage" {
									for _, point := range metric.Histogram.DataPoints {
										for _, a := range point.Attributes {
											if a.Key == "gen_ai.token.type" && a.Value.StringValue != nil {
												metricTokens[*a.Value.StringValue] += point.Sum
											}
										}
									}
								}
								if metric.Sum == nil {
									continue
								}
								for _, point := range metric.Sum.DataPoints {
									for _, a := range point.Attributes {
										if a.Key == "gen_ai.operation.name" && a.Value.StringValue != nil && *a.Value.StringValue == "assistant" {
											t.Error("assistant metric emitted")
										}
										if metric.Name == "gen_ai.client.token.usage" && a.Key == "gen_ai.token.type" && a.Value.StringValue != nil {
											if point.AsDouble != nil {
												metricTokens[*a.Value.StringValue] += *point.AsDouble
											}
											if point.AsInt != nil {
												metricTokens[*a.Value.StringValue] += float64(*point.AsInt)
											}
										}
									}
								}
							}
						}
					}
				} else {
					traceRequests++
					request, err := proto.DecodeExportTraceServiceRequest(body)
					if err != nil {
						t.Error(err)
						return
					}
					for _, resource := range request.ResourceSpans {
						for _, scope := range resource.ScopeSpans {
							spans = append(spans, scope.Spans...)
						}
					}
				}
			}))
			defer server.Close()
			cfg.Transport.Endpoint = server.URL
			for i := 0; i < 2; i++ {
				if err := Run(RunOptions{Config: cfg, Input: stop, SkipWait: true}); err != nil {
					t.Fatal(err)
				}
			}
			server.Close()
			if traceRequests != 1 || metricRequests != 1 {
				t.Fatalf("signal counts %d/%d", traceRequests, metricRequests)
			}
			attr := func(s otlp.Span, key string) otlp.AnyValue {
				for _, a := range s.Attributes {
					if a.Key == key {
						return a.Value
					}
				}
				return otlp.AnyValue{}
			}
			str := func(s otlp.Span, key string) string {
				v := attr(s, key)
				if v.StringValue != nil {
					return *v.StringValue
				}
				return ""
			}
			var root otlp.Span
			llms := map[string]otlp.Span{}
			tools := map[string]otlp.Span{}
			for _, s := range spans {
				switch str(s, "gen_ai.operation.name") {
				case "invoke_agent":
					root = s
				case "chat":
					llms[hex.EncodeToString(s.SpanID)] = s
				case "execute_tool":
					tools[str(s, "gen_ai.tool.name")] = s
				}
			}
			assistantCount, skillCount, reasoningCount := 0, 0, 0
			var input, output, cache int64
			for _, s := range spans {
				operation := str(s, "gen_ai.operation.name")
				if (operation == "chat" || operation == "execute_tool") && !bytes.Equal(s.ParentSpanID, root.SpanID) {
					t.Fatal("LLM/tool parent mismatch")
				}
				if mode == "none" && (str(s, "gen_ai.input.messages") != "" || str(s, "gen_ai.output.messages") != "") {
					t.Fatal("content-none emitted messages")
				}
				if !bytes.Equal(s.SpanID, root.SpanID) && (s.StartTimeUnixNano < root.StartTimeUnixNano || s.EndTimeUnixNano > root.EndTimeUnixNano) {
					t.Fatal("root containment")
				}
				if s.Name == "assistant" {
					assistantCount++
					if s.EndTimeUnixNano-s.StartTimeUnixNano != uint64(time.Millisecond) || !bytes.Equal(s.ParentSpanID, root.SpanID) {
						t.Fatal("assistant timing/parent")
					}
					if _, ok := llms[str(s, "triggered_by.llm_span_id")]; !ok {
						t.Fatal("assistant LLM association")
					}
				}
				if str(s, "gen_ai.operation.name") == "skill" {
					skillCount++
					if !bytes.Equal(s.ParentSpanID, tools["Read"].SpanID) || str(s, "gen_ai.skill.name") != "rc-synthetic-check" || str(s, "gen_ai.skill.version") != "0.0.1" {
						t.Fatal("skill metadata/parent")
					}
				}
				for _, pair := range []struct {
					key string
					sum *int64
				}{{"gen_ai.usage.input_tokens", &input}, {"gen_ai.usage.output_tokens", &output}, {"gen_ai.usage.cache_read.input_tokens", &cache}} {
					v := attr(s, pair.key)
					if v.IntValue != nil {
						if s.Name != "llm" {
							t.Fatal("tokens outside LLM")
						}
						*pair.sum += *v.IntValue
					}
				}
				var messages []struct {
					Parts []struct {
						Type string `json:"type"`
					} `json:"parts"`
				}
				if raw := str(s, "gen_ai.output.messages"); raw != "" {
					if err := json.Unmarshal([]byte(raw), &messages); err != nil {
						t.Fatal(err)
					}
					for _, message := range messages {
						for _, part := range message.Parts {
							if part.Type == "reasoning" {
								if s.Name != "llm" {
									t.Fatal("reasoning outside LLM")
								}
								reasoningCount++
							}
						}
					}
				}
			}
			if len(spans) != 12 || len(llms) != 4 || len(tools) != 3 || assistantCount != 3 || skillCount != 1 {
				t.Fatalf("span counts %d/%d/%d/%d/%d", len(spans), len(llms), len(tools), assistantCount, skillCount)
			}
			if input != 143146 || output != 1283 {
				t.Fatalf("tokens %d/%d", input, output)
			}
			if cache != 107968 || metricTokens["input"] != 143146 || metricTokens["output"] != 1283 || len(metricTokens) != 2 {
				t.Fatal("cache or metric token totals")
			}
			wantReasoning := 0
			if mode == "on" {
				wantReasoning = 3
			}
			if reasoningCount != wantReasoning {
				t.Fatalf("reasoning parts %d", reasoningCount)
			}
			if mode != "none" && (str(tools["Agent"], "gen_ai.tool.call.result") != "63" || str(tools["Agent"], "output_preview") != "63") {
				t.Fatal("Agent result missing on wire")
			}
			if mode == "none" && (str(tools["Agent"], "gen_ai.tool.call.result") != "" || str(tools["Agent"], "output_preview") != "") {
				t.Fatal("content-none retained Agent result")
			}
			t.Logf("mode=%s spans=%d llm=4 assistant=3 tools=3 skill=1 reasoning=%d input=%d output=%d cache=%d uploads=1/1 duplicate=0 Agent-result-present=%v", mode, len(spans), reasoningCount, input, output, cache, mode != "none")
		})
	}
}
