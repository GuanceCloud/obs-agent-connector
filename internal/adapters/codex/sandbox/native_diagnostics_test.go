package sandbox

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/journal"
)

func TestCollectionDiagnosticsClassifyWithoutLeakingErrorContents(t *testing.T) {
	busy := fmt.Errorf("private journal path: %w", journal.ErrBusy)
	if CollectionDiagnostic(busy, "journal_reconcile", false) != nil {
		t.Fatal("routine contention became a production warning")
	}
	if d := CollectionDiagnostic(busy, "journal_reconcile", true); d["category"] != "journal_busy" || d["phase"] != "journal_reconcile" {
		t.Fatal("debug contention diagnostic missing")
	}
	for _, tc := range []struct {
		err      error
		category string
	}{
		{&url.Error{Op: "Post", URL: "https://user:secret@example.test", Err: &net.DNSError{Name: "secret.example", IsTimeout: true}}, "transport_timeout"},
		{&url.Error{Op: "Post", URL: "https://user:secret@example.test", Err: &net.DNSError{Name: "secret.example"}}, "transport_network_error"},
		{fmt.Errorf("private path: %w", os.ErrPermission), "filesystem_permission_denied"},
		{io.ErrUnexpectedEOF, "connection_closed"},
	} {
		d := CollectionDiagnostic(tc.err, "test_operation", false)
		if d["category"] != tc.category || strings.Contains(fmt.Sprint(d), "secret") {
			t.Fatalf("unsafe or inaccurate diagnostic: %v", d)
		}
	}
	if CollectionDiagnostic(nil, "none", false) != nil {
		t.Fatal("nil error generated a diagnostic")
	}
	if !errors.Is(busy, journal.ErrBusy) {
		t.Fatal("wrapped contention lost identity")
	}
}

func TestNativeHealthDetectsStopReplacedByAnotherIntegration(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "hooks.json")
	body := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"/other/loongsuite-pilot.sh stop"}]}],"PreToolUse":[{"hooks":[{"type":"command","command":"/bin/obs-agent-connector hook codex"}]}],"PostToolUse":[{"hooks":[{"type":"command","command":"/bin/obs-agent-connector hook codex"}]}]}}`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	missing, err := nativeMissingHooks(home)
	if err != nil || len(missing) != 1 || missing[0] != "Stop" {
		t.Fatalf("missing=%v err=%v", missing, err)
	}
	body = strings.Replace(body, `"Stop":[`, `"Stop":[{"hooks":[{"type":"command","command":"\"/my tools/obs-agent-connector\" hook codex"}]},`, 1)
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if missing, err := nativeMissingHooks(home); err != nil || len(missing) != 0 {
		t.Fatalf("repaired missing=%v err=%v", missing, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != body {
		t.Fatal("health check rewrote another integration's configuration")
	}
}
