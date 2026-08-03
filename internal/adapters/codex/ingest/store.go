package ingest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Batch struct {
	ID          string         `json:"id"`
	ReceivedAt  string         `json:"received_at"`
	Ingest      map[string]any `json:"ingest,omitempty"`
	SpanCount   int            `json:"span_count"`
	MetricCount int            `json:"metric_count"`
	RawRequest  any            `json:"raw_request,omitempty"`
	Spans       []StoredSpan   `json:"spans,omitempty"`
	Metrics     []StoredMetric `json:"metrics,omitempty"`
}

type FileStore struct {
	DataDir     string
	BatchDir    string
	SpansFile   string
	MetricsFile string

	mu sync.Mutex
}

func NewFileStore(dataDir string) *FileStore {
	return &FileStore{
		DataDir:     dataDir,
		BatchDir:    filepath.Join(dataDir, "batches"),
		SpansFile:   filepath.Join(dataDir, "spans.ndjson"),
		MetricsFile: filepath.Join(dataDir, "metrics.ndjson"),
	}
}

type SaveResult struct {
	ID          string
	BatchFile   string
	SpanCount   int
	MetricCount int
}

func (s *FileStore) SaveBatch(rawRequest any, spans []StoredSpan, metrics []StoredMetric, ingest map[string]any) (SaveResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.BatchDir, 0o755); err != nil {
		return SaveResult{}, err
	}

	id := fmt.Sprintf("%d-%d", time.Now().UnixMilli(), time.Now().UnixNano())
	batchFile := filepath.Join(s.BatchDir, id+".json")
	batch := Batch{
		ID:          id,
		ReceivedAt:  time.Now().UTC().Format(time.RFC3339Nano),
		Ingest:      ingest,
		SpanCount:   len(spans),
		MetricCount: len(metrics),
		RawRequest:  rawRequest,
		Spans:       spans,
		Metrics:     metrics,
	}

	data, err := json.MarshalIndent(batch, "", "  ")
	if err != nil {
		return SaveResult{}, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(batchFile, data, 0o644); err != nil {
		return SaveResult{}, err
	}
	if err := appendNDJSON(s.SpansFile, spans); err != nil {
		return SaveResult{}, err
	}
	if err := appendNDJSON(s.MetricsFile, metrics); err != nil {
		return SaveResult{}, err
	}

	return SaveResult{
		ID:          id,
		BatchFile:   batchFile,
		SpanCount:   len(spans),
		MetricCount: len(metrics),
	}, nil
}

func appendNDJSON[T any](file string, values []T) error {
	if len(values) == 0 {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return err
	}
	handle, err := os.OpenFile(file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer handle.Close()
	writer := bufio.NewWriter(handle)
	for _, value := range values {
		line, err := json.Marshal(value)
		if err != nil {
			return err
		}
		if _, err := writer.Write(line); err != nil {
			return err
		}
		if err := writer.WriteByte('\n'); err != nil {
			return err
		}
	}
	return writer.Flush()
}

func (s *FileStore) ListSpans(limit int) ([]StoredSpan, error) {
	if limit <= 0 {
		limit = 50
	}
	lines, err := tailNDJSON(s.SpansFile, limit)
	if err != nil {
		return nil, err
	}
	out := make([]StoredSpan, 0, len(lines))
	for _, line := range lines {
		var item StoredSpan
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *FileStore) ListMetrics(limit int) ([]StoredMetric, error) {
	if limit <= 0 {
		limit = 50
	}
	lines, err := tailNDJSON(s.MetricsFile, limit)
	if err != nil {
		return nil, err
	}
	out := make([]StoredMetric, 0, len(lines))
	for _, line := range lines {
		var item StoredMetric
		if err := json.Unmarshal([]byte(line), &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func tailNDJSON(file string, limit int) ([]string, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	if len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}
