package hook

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
)

func TestDesktopSurfaceRecognizesObservedSources(t *testing.T) {
	for _, test := range []struct {
		name, entrypoint, platform, recordedEntrypoint, want string
	}{
		{name: "Cowork hook", entrypoint: "local-agent", want: "cowork"},
		{name: "local 3P Code hook", entrypoint: "claude-desktop-3p", want: "code"},
		{name: "local Code hook", entrypoint: "claude-desktop", want: "code"},
		{name: "local Code transcript", platform: "desktop_app", recordedEntrypoint: "claude-desktop-3p", want: "code"},
		{name: "terminal", entrypoint: "cli", platform: "cli", recordedEntrypoint: "claude-desktop-3p"},
		{name: "unknown Desktop profile", platform: "desktop_app", recordedEntrypoint: "chat"},
	} {
		t.Run(test.name, func(t *testing.T) {
			rows := []map[string]any{{"clientPlatform": test.platform, "entrypoint": test.recordedEntrypoint}}
			if got := desktopSurface(test.entrypoint, rows); got != test.want {
				t.Fatalf("surface = %q, want %q", got, test.want)
			}
		})
	}
}

func TestClaudeHooksLabelRuntimeAndDeduplicateDesktopTurns(t *testing.T) {
	wantUploads := []struct{ runtime, surface string }{
		{"claude-desktop", "cowork"},
		{"claude-desktop", "code"},
		{"claude-desktop", "code"},
		{"claude", ""},
	}
	checkResource := func(count int32, attributes []otlp.KeyValue) string {
		t.Helper()
		if int(count) > len(wantUploads) {
			t.Errorf("unexpected upload %d", count)
			return ""
		}
		want := wantUploads[count-1]
		if got := stringAttribute(attributes, "agent_runtime"); got != want.runtime {
			t.Errorf("upload %d runtime = %q, want %q", count, got, want.runtime)
		}
		if got := stringAttribute(attributes, "claude_surface"); got != want.surface {
			t.Errorf("upload %d surface = %q, want %q", count, got, want.surface)
		}
		return want.runtime
	}

	var traces atomic.Int32
	var metricRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, err := readMaybeGzipBody(request)
		if err != nil {
			t.Error(err)
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		switch request.URL.Path {
		case "/v1/traces":
			count := traces.Add(1)
			trace, err := proto.DecodeExportTraceServiceRequest(body)
			if err != nil || len(trace.ResourceSpans) != 1 {
				t.Errorf("decode Claude trace %d: %v", count, err)
				break
			}
			checkResource(count, trace.ResourceSpans[0].Resource.Attributes)
			if bytes.Contains(body, []byte("list files")) {
				t.Error("capture none leaked prompt in trace")
			}
		case "/v1/metrics":
			count := metricRequests.Add(1)
			request, err := proto.DecodeExportMetricsServiceRequest(body)
			if err != nil || len(request.ResourceMetrics) != 1 || len(request.ResourceMetrics[0].ScopeMetrics) != 1 {
				t.Errorf("decode Claude metrics %d: %v", count, err)
				break
			}
			resource := request.ResourceMetrics[0]
			wantRuntime := checkResource(count, resource.Resource.Attributes)
			foundDuration := false
			for _, metric := range resource.ScopeMetrics[0].Metrics {
				if metric.Name != "gen_ai.workflow.duration" || metric.Histogram == nil {
					continue
				}
				for _, point := range metric.Histogram.DataPoints {
					foundDuration = true
					if got := stringAttribute(point.Attributes, "agent_runtime"); got != wantRuntime {
						t.Errorf("upload %d workflow duration runtime = %q, want %q", count, got, wantRuntime)
					}
				}
			}
			if !foundDuration {
				t.Errorf("upload %d has no workflow duration metric", count)
			}
		default:
			t.Errorf("unexpected path %s", request.URL.Path)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	cfg := testHookConfig(t, server.URL)
	cfg.CaptureContent = "none"
	payload := hookPayload(writeHookTranscript(t))
	options := RunOptions{Config: &cfg, Payload: payload, SkipWait: true, Desktop: true}
	run := func(entrypoint, session, event string) {
		t.Helper()
		options.Entrypoint = entrypoint
		payload["session_id"] = session
		payload["hook_event_name"] = event
		if err := RunWithOptions(options); err != nil {
			t.Fatal(err)
		}
	}

	run("local-agent", "cowork-session", "Stop")
	run("local-agent", "cowork-session", "SessionEnd")
	run("claude-desktop-3p", "code-session", "Stop")
	options.Desktop = false
	run("claude-desktop-3p", "code-session", "SessionEnd")
	run("claude-desktop-3p", "legacy-first-code-session", "Stop")
	options.Desktop = true
	run("claude-desktop-3p", "legacy-first-code-session", "SessionEnd")
	run("chat", "chat-session", "Stop")
	run("cli", "terminal-session", "Stop")
	run("local-agent", "unfinished-session", "UserPromptSubmit")
	options.Desktop = false
	run("cli", "terminal-session", "Stop")
	if int(traces.Load()) != len(wantUploads) || int(metricRequests.Load()) != len(wantUploads) {
		t.Fatalf("uploads = traces %d metrics %d, want %d of each", traces.Load(), metricRequests.Load(), len(wantUploads))
	}
	logBody, err := os.ReadFile(cfg.HookLogFile)
	if err != nil || !strings.Contains(string(logBody), `"surface":"cowork"`) || !strings.Contains(string(logBody), `"surface":"code"`) {
		t.Fatalf("Desktop log is missing surface evidence: %v", err)
	}
}

func stringAttribute(attributes []otlp.KeyValue, key string) string {
	for _, attribute := range attributes {
		if attribute.Key == key && attribute.Value.StringValue != nil {
			return *attribute.Value.StringValue
		}
	}
	return ""
}

func TestDesktopHookIgnoresAnExplicitHomeArgument(t *testing.T) {
	root := t.TempDir()
	configDirectory := filepath.Join(root, ".obs-agent-connector", "claude-desktop")
	if err := os.MkdirAll(configDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDirectory, "gtrace.json"), []byte(`{"enabled":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	emptyInputPath := filepath.Join(t.TempDir(), "stdin")
	if err := os.WriteFile(emptyInputPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	emptyInput, err := os.Open(emptyInputPath)
	if err != nil {
		t.Fatal(err)
	}
	originalStdin := os.Stdin
	os.Stdin = emptyInput
	t.Cleanup(func() {
		os.Stdin = originalStdin
		_ = emptyInput.Close()
	})

	if code := RunDesktopCLI([]string{root}); code != 0 {
		t.Fatalf("hook did not fail open: exit %d", code)
	}
	if _, err := os.Stat(filepath.Join(configDirectory, "gtrace-hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("Hook used an explicit home argument: %v", err)
	}
}
