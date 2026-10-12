package diagnostics

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConcurrentSnapshotsKeepNewestAndBounded(t *testing.T) {
	root := t.TempDir()
	base := time.Now().Add(-time.Hour).UnixNano()
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := Write(root, Observation{Kind: "traces", At: base + int64(i), Outcome: "accepted"}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	if got := Read(root, time.Now())["traces"].At; got != base+39 {
		t.Fatalf("newest regressed: %d", got)
	}
	entries, err := os.ReadDir(directory(root))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) > 2 {
		t.Fatalf("unbounded records: %d", len(entries))
	}
}

func TestUnknownMalformedFutureStaleAndPrivacy(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	if len(Read(root, now)) != 0 {
		t.Fatal("missing should be unknown")
	}
	old := now.Add(-25 * time.Hour).UnixNano()
	if err := Write(root, Observation{Kind: "usage", At: old, Calls: 1, ObservedCalls: 1, Fields: []string{"input_tokens"}}); err != nil {
		t.Fatal(err)
	}
	rows := fmt.Sprint(Rows(root, root, now))
	if !strings.Contains(rows, "stale (>24h)") || !strings.Contains(rows, "1/1 LLM calls") {
		t.Fatal(rows)
	}
	if err := Write(root, Observation{Kind: "usage", At: now.UnixNano(), Fields: []string{"SECRET"}}); err == nil {
		t.Fatal("unallowlisted field accepted")
	}
	path := filepath.Join(directory(root), fmt.Sprintf("usage-%019d-invalid.json", now.UnixNano()))
	for _, body := range []string{`invalid SECRET`, `{"version":2,"kind":"usage","at":1}`, `{"version":1,"kind":"usage","at":1,"fields":["SECRET"]}`} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if Read(root, now)["usage"].At != 0 {
			t.Fatal("malformed latest must not fall back")
		}
		if strings.Contains(fmt.Sprint(Rows(root, root, now)), "SECRET") {
			t.Fatal("leaked data")
		}
	}
	if err := Write(root, Observation{Kind: "metrics", At: now.Add(time.Hour).UnixNano(), Outcome: "accepted"}); err != nil {
		t.Fatal(err)
	}
	if Read(root, now)["metrics"].At != 0 {
		t.Fatal("future evidence trusted")
	}
}

func TestFlagAllowlist(t *testing.T) {
	for _, tc := range []struct{ body, want string }{
		{"", "missing"}, {"OTHER=SECRET\n", "missing"}, {"QODERCN_EXPOSE_TOKEN_USAGE=1\n", "enabled"},
		{"QODERCN_EXPOSE_TOKEN_USAGE=0\n", "disabled"}, {"QODERCN_EXPOSE_TOKEN_USAGE=true\n", "enabled"},
		{"QODERCN_EXPOSE_TOKEN_USAGE=YES\n", "enabled"}, {"QODERCN_EXPOSE_TOKEN_USAGE=' True '\n", "enabled"},
		{"QODERCN_EXPOSE_TOKEN_USAGE=on\n", "disabled"}, {"QODERCN_EXPOSE_TOKEN_USAGE=false\n", "disabled"},
		{"\xef\xbb\xbfexport QODERCN_EXPOSE_TOKEN_USAGE = '1' # SECRET\r\n", "enabled"},
		{"QODERCN_EXPOSE_TOKEN_USAGE=1\nQODERCN_EXPOSE_TOKEN_USAGE=0", "disabled"},
		{"OTHER='first\nQODERCN_EXPOSE_TOKEN_USAGE=1\nlast'\n", "missing"},
		{"QODERCN_EXPOSE_TOKEN_USAGE=1\nOTHER='broken", "invalid"},
		{"QODERCN_EXPOSE_TOKEN_USAGE='1' trailing SECRET", "invalid"},
	} {
		t.Run(tc.want+tc.body, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".env")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			if got := Flag(path); got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
	if got := Flag(filepath.Join(t.TempDir(), "missing")); got != "missing" {
		t.Fatal(got)
	}
}

func TestRowsHaveNoWriteSideEffects(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	Rows(root, root, time.Now())
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("status created state")
	}
}

func TestReplayFreshnessDoesNotHideStaleUsageSource(t *testing.T) {
	root := t.TempDir()
	now := time.Now()
	if err := Write(root, Observation{Kind: "usage", At: now.UnixNano(), SourceAt: now.Add(-48 * time.Hour).UnixNano(), Calls: 1, ObservedCalls: 1, Fields: []string{"input_tokens"}}); err != nil {
		t.Fatal(err)
	}
	rows := Rows(root, root, now)
	for _, row := range rows {
		if row[0] == "Usage (last replay)" && strings.Contains(row[1], "stale") {
			t.Fatal("fresh inspection marked stale")
		}
		if row[0] == "Usage source" && !strings.Contains(row[1], "stale") {
			t.Fatal("old source hidden by recent replay")
		}
	}
}

func TestRejectImpossibleAndExcessiveSnapshots(t *testing.T) {
	root := t.TempDir()
	for _, o := range []Observation{
		{Kind: "usage", At: 1, Calls: -1},
		{Kind: "usage", At: 1, Calls: 1, ObservedCalls: 2},
		{Kind: "usage", At: 1, SourceAt: -1},
		{Kind: "traces", At: 1, Outcome: "SECRET"},
	} {
		if Write(root, o) == nil {
			t.Fatal("accepted invalid observation")
		}
	}
	if err := os.MkdirAll(directory(root), 0700); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxEntries+1; i++ {
		if err := os.WriteFile(filepath.Join(directory(root), fmt.Sprintf("unknown-%d.json", i)), []byte("{}"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if len(Read(root, time.Now())) != 0 {
		t.Fatal("unbounded read")
	}
}
