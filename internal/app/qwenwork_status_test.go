package app

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type forbiddenStatusTransport struct{ t *testing.T }

func (r forbiddenStatusTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.t.Fatal("status made network request")
	return nil, nil
}

func TestQwenWorkStatusIsOfflineAndReadOnly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("QODERCN_EXPOSE_TOKEN_USAGE", "SECRET")
	t.Setenv("QWENWORK_CN_OTEL_STATE_DIR", filepath.Join(home, "must-not-use-cli-override"))
	profile := filepath.Join(home, ".qwenworkcn")
	if err := os.MkdirAll(profile, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(profile, ".env")
	body := []byte("QODERCN_EXPOSE_TOKEN_USAGE=1\nTOKEN=SECRET\n")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	old := http.DefaultTransport
	http.DefaultTransport = forbiddenStatusTransport{t}
	t.Cleanup(func() { http.DefaultTransport = old })
	out := captureStdout(t, func() {
		if err := status([]string{"qwenwork-cn"}); err != nil {
			t.Fatal(err)
		}
	})
	for _, want := range []string{"Token flag (profile)", "enabled", "Token runtime", "unknown", "Usage (last replay)", "Trace (last attempt)", "Metrics (last attempt)", "project .env"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %s", want, out)
		}
	}
	if strings.Contains(out, "SECRET") {
		t.Fatal("secret leaked")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != string(body) {
		t.Fatal("status changed dotenv")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 1 {
		t.Fatalf("status wrote to home: %v %v", entries, err)
	}
}
