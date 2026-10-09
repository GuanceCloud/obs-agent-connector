// Package diagnostics stores bounded, content-free observations. It is optional:
// neither read nor write failures may affect transcript processing or delivery.
package diagnostics

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const maxEntries = 128
const maxBytes = 4096

// Observation contains only allowlisted metadata, never IDs, paths or errors.
type Observation struct {
	Version       int      `json:"version"`
	At            int64    `json:"at"`
	SourceAt      int64    `json:"source_at,omitempty"`
	Kind          string   `json:"kind"`
	Outcome       string   `json:"outcome,omitempty"`
	Calls         int      `json:"calls,omitempty"`
	ObservedCalls int      `json:"observed_calls,omitempty"`
	Fields        []string `json:"fields,omitempty"`
}

func valid(o Observation) bool {
	if o.Version != 1 || o.At <= 0 || o.SourceAt < 0 || o.Calls < 0 || o.ObservedCalls < 0 || o.ObservedCalls > o.Calls {
		return false
	}
	switch o.Kind {
	case "usage":
		if o.Outcome != "" {
			return false
		}
	case "traces", "metrics":
		if o.Outcome != "accepted" && o.Outcome != "failed" && o.Outcome != "checkpoint_failed" {
			return false
		}
	case "replay":
		switch o.Outcome {
		case "complete", "incomplete", "parse_failed", "export_failed":
		default:
			return false
		}
	default:
		return false
	}
	for _, f := range o.Fields {
		switch f {
		case "input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "reasoning_output_tokens":
		default:
			return false
		}
	}
	return len(o.Fields) <= 5
}

func directory(root string) string { return filepath.Join(root, "diagnostics-v1") }

// Write publishes immutable atomic snapshots. Concurrent writers cannot replace
// newer evidence with older evidence. Pruning only touches the enumerated snapshot
// and retains two latest records per kind; racing publications remain untouched.
func Write(root string, o Observation) error {
	o.Version = 1
	if !valid(o) {
		return fmt.Errorf("invalid diagnostic observation")
	}
	dir := directory(root)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".pending-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = json.NewEncoder(f).Encode(o); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	name := fmt.Sprintf("%s-%019d-%s.json", o.Kind, o.At, filepath.Base(f.Name()))
	if err = os.Rename(f.Name(), filepath.Join(dir, name)); err != nil {
		return err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasPrefix(e.Name(), o.Kind+"-") && strings.HasSuffix(e.Name(), ".json") {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	for i := 2; i < len(names); i++ {
		_ = os.Remove(filepath.Join(dir, names[i]))
	}
	return nil
}

func readFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxBytes {
		return nil, fmt.Errorf("invalid diagnostic file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxBytes+1))
}

// Read is bounded and read-only. Unknown versions, malformed and future records
// invalidate their kind instead of falling back to misleading older evidence.
func Read(root string, now time.Time) map[string]Observation {
	out := map[string]Observation{}
	dir, err := os.Open(directory(root))
	if err != nil {
		return out
	}
	defer dir.Close()
	entries, err := dir.ReadDir(maxEntries + 1)
	if (err != nil && err != io.EOF) || len(entries) > maxEntries {
		return out
	}
	latest := map[string]string{}
	for _, e := range entries {
		if !e.Type().IsRegular() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		kind, _, _ := strings.Cut(e.Name(), "-")
		if e.Name() > latest[kind] {
			latest[kind] = e.Name()
		}
	}
	for kind, name := range latest {
		body, err := readFile(filepath.Join(directory(root), name))
		var o Observation
		if err == nil && len(body) <= maxBytes && json.Unmarshal(body, &o) == nil && valid(o) && o.Kind == kind && o.At <= now.Add(time.Minute).UnixNano() && o.SourceAt <= now.Add(time.Minute).UnixNano() {
			out[kind] = o
		}
	}
	return out
}

func describe(o Observation, now time.Time, value string) string {
	if o.At == 0 {
		return "unknown"
	}
	suffix := ""
	if now.Sub(time.Unix(0, o.At)) > 24*time.Hour {
		suffix = "; stale (>24h)"
	}
	return value + " at " + time.Unix(0, o.At).UTC().Format(time.RFC3339) + suffix
}

// Rows never opens transcripts, logs or project dotenv files and never sends data.
func Rows(profile, root string, now time.Time) [][2]string {
	all := Read(root, now)
	u := all["usage"]
	usage := "unknown"
	if u.At != 0 {
		usage = fmt.Sprintf("%d/%d LLM calls with numeric usage", u.ObservedCalls, u.Calls)
		if len(u.Fields) > 0 {
			usage += "; fields: " + strings.Join(u.Fields, ", ")
		}
		if u.Calls == 0 {
			usage = "unknown (no inspected LLM calls)"
		}
	}
	source := "unknown"
	if u.SourceAt > 0 {
		source = describe(Observation{At: u.SourceAt}, now, "numeric transcript record")
	}
	return [][2]string{
		{"Token flag (profile)", Flag(filepath.Join(profile, ".env"))},
		{"Token runtime", "unknown; process environment/project .env may override profile; restart desktop after changes"},
		{"Usage (last replay)", describe(u, now, usage)},
		{"Usage source", source + "; replay time is not model-call time"},
		{"Trace (last attempt)", describe(all["traces"], now, all["traces"].Outcome)},
		{"Metrics (last attempt)", describe(all["metrics"], now, all["metrics"].Outcome)},
		{"Replay (last run)", describe(all["replay"], now, all["replay"].Outcome)},
		{"Pending queue files", pending(root)},
		{"Delivery evidence", "accepted = HTTP acceptance, not backend storage verification"},
	}
}

func pending(root string) string {
	f, err := os.Open(filepath.Join(root, "queue"))
	if os.IsNotExist(err) {
		return "0 (no queue directory)"
	}
	if err != nil {
		return "unknown"
	}
	defer f.Close()
	entries, err := f.ReadDir(1025)
	if err != nil && err != io.EOF {
		return "unknown"
	}
	if len(entries) > 1024 {
		return "unknown (scan limit reached)"
	}
	n := 0
	for _, e := range entries {
		if e.Type().IsRegular() && strings.HasSuffix(e.Name(), ".json") {
			n++
		}
	}
	return fmt.Sprintf("%d (queue files, not turn count)", n)
}
