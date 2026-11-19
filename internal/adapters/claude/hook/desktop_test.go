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

func TestDesktopOnlyHooksUploadBothSurfacesAndSkipOthers(t *testing.T) {
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
			want := "cowork"
			if count == 2 {
				want = "code"
			}
			trace, err := proto.DecodeExportTraceServiceRequest(body)
			if err != nil || len(trace.ResourceSpans) != 1 {
				t.Errorf("decode Desktop trace %d: %v", count, err)
				break
			}
			attributes := map[string]string{}
			for _, item := range trace.ResourceSpans[0].Resource.Attributes {
				if item.Value.StringValue != nil {
					attributes[item.Key] = *item.Value.StringValue
				}
			}
			if attributes["claude_surface"] != want || attributes["agent_runtime"] != "claude" {
				t.Errorf("trace %d attributes = %#v, want claude/%s", count, attributes, want)
			}
			if bytes.Contains(body, []byte("list files")) {
				t.Error("capture none leaked prompt in trace")
			}
		case "/v1/metrics":
			metricRequests.Add(1)
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
	run("claude-desktop-3p", "code-session", "SessionEnd")
	run("chat", "chat-session", "Stop")
	run("cli", "terminal-session", "Stop")
	run("local-agent", "unfinished-session", "UserPromptSubmit")
	if traces.Load() != 2 || metricRequests.Load() != 2 {
		t.Fatalf("uploads = traces %d metrics %d, want one of each per Desktop turn", traces.Load(), metricRequests.Load())
	}
	logBody, err := os.ReadFile(cfg.HookLogFile)
	if err != nil || !strings.Contains(string(logBody), `"surface":"cowork"`) || !strings.Contains(string(logBody), `"surface":"code"`) {
		t.Fatalf("Desktop log is missing surface evidence: %v", err)
	}
}

func TestDesktopHookUsesInstallerHomeEvenWithDifferentProcessHome(t *testing.T) {
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
	body, err := os.ReadFile(filepath.Join(configDirectory, "gtrace-hooks.json"))
	if err != nil || !strings.Contains(string(body), "hook input is empty") {
		t.Fatalf("hook did not use the installer's per-user config: %v", err)
	}
}
