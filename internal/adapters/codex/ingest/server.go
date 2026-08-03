package ingest

import (
	"bytes"
	"compress/gzip"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
)

const (
	DefaultPort    = 3030
	DefaultDataDir = "data"
)

type ServerOptions struct {
	Store     *FileStore
	DataDir   string
	PublicKey string
	SecretKey string
}

func NewHandler(options ServerOptions) http.Handler {
	store := options.Store
	if store == nil {
		dataDir := strings.TrimSpace(options.DataDir)
		if dataDir == "" {
			dataDir = DefaultDataDir
		}
		store = NewFileStore(dataDir)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		sendJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "gtrace"})
	})
	mux.HandleFunc("/api/public/health", func(w http.ResponseWriter, _ *http.Request) {
		sendJSON(w, http.StatusOK, map[string]any{"ok": true, "service": "gtrace-otlp"})
	})
	mux.HandleFunc("/traces", func(w http.ResponseWriter, r *http.Request) {
		limit := queryLimit(r)
		spans, err := store.ListSpans(limit)
		if err != nil {
			sendError(w, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"data": spans})
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		limit := queryLimit(r)
		metrics, err := store.ListMetrics(limit)
		if err != nil {
			sendError(w, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"data": metrics})
	})
	mux.HandleFunc("/api/public/otel/v1/traces", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			sendJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		auth := parseBasicAuth(r.Header.Get("Authorization"))
		if !isAuthorized(auth, options.PublicKey, options.SecretKey) {
			sendJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		body, err := readAndDecodeBody(r)
		if err != nil {
			sendBadRequest(w, err)
			return
		}
		request, err := decodeTraceRequest(body, r.Header.Get("Content-Type"))
		if err != nil {
			sendBadRequest(w, err)
			return
		}
		ingestMeta := buildIngestMeta(r, auth.username)
		spans := NormalizeExportTraceRequest(request, ingestMeta)
		saved, err := store.SaveBatch(traceRequestToMap(request), spans, nil, ingestMeta)
		if err != nil {
			sendError(w, err)
			return
		}
		sendTraceSuccess(w, r.Header.Get("Content-Type"), saved)
	})
	mux.HandleFunc("/api/public/otel/v1/metrics", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			sendJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		auth := parseBasicAuth(r.Header.Get("Authorization"))
		if !isAuthorized(auth, options.PublicKey, options.SecretKey) {
			sendJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		body, err := readAndDecodeBody(r)
		if err != nil {
			sendBadRequest(w, err)
			return
		}
		request, err := decodeMetricsRequest(body, r.Header.Get("Content-Type"))
		if err != nil {
			sendBadRequest(w, err)
			return
		}
		ingestMeta := buildIngestMeta(r, auth.username)
		metrics := NormalizeExportMetricsRequest(request, ingestMeta)
		saved, err := store.SaveBatch(metricRequestToMap(request), nil, metrics, ingestMeta)
		if err != nil {
			sendError(w, err)
			return
		}
		sendMetricsSuccess(w, r.Header.Get("Content-Type"), saved)
	})
	mux.HandleFunc("/api/gtrace/v1/codex-spans", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			sendJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
			return
		}
		auth := parseBasicAuth(r.Header.Get("Authorization"))
		if !isAuthorized(auth, options.PublicKey, options.SecretKey) {
			sendJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		body, err := readAndDecodeBody(r)
		if err != nil {
			sendBadRequest(w, err)
			return
		}
		var payload struct {
			Spans       []StoredSpan   `json:"spans"`
			RolloutFile string         `json:"rollout_file"`
			Session     map[string]any `json:"session"`
			TurnCount   any            `json:"turn_count"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			sendBadRequest(w, err)
			return
		}
		ingestMeta := buildIngestMeta(r, auth.username)
		ingestMeta["source"] = "gtrace-codex-native"
		ingestMeta["rollout_file"] = payload.RolloutFile
		if payload.Session != nil {
			ingestMeta["session_id"] = payload.Session["session_id"]
		}
		spans := make([]StoredSpan, 0, len(payload.Spans))
		for _, span := range payload.Spans {
			current := span
			current.Ingest = mergeMaps(current.Ingest, ingestMeta)
			spans = append(spans, current)
		}
		rawRequest := map[string]any{
			"type":         "gtrace.codex.spans",
			"rollout_file": payload.RolloutFile,
			"session":      payload.Session,
			"turn_count":   payload.TurnCount,
		}
		saved, err := store.SaveBatch(rawRequest, spans, nil, ingestMeta)
		if err != nil {
			sendError(w, err)
			return
		}
		sendJSON(w, http.StatusOK, map[string]any{"ok": true, "batch_id": saved.ID, "span_count": saved.SpanCount})
	})

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health", "/api/public/health", "/traces", "/metrics", "/api/public/otel/v1/traces", "/api/public/otel/v1/metrics", "/api/gtrace/v1/codex-spans":
			mux.ServeHTTP(w, r)
		default:
			sendJSON(w, http.StatusNotFound, map[string]any{"error": "not_found"})
		}
	})
}

func queryLimit(r *http.Request) int {
	limit, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	if err != nil || limit <= 0 {
		return 50
	}
	return limit
}

func buildIngestMeta(r *http.Request, publicKey string) map[string]any {
	return map[string]any{
		"public_key":   publicKey,
		"sdk_name":     r.Header.Get("x-gtrace-sdk-name"),
		"sdk_version":  r.Header.Get("x-gtrace-sdk-version"),
		"content_type": r.Header.Get("Content-Type"),
		"user_agent":   r.Header.Get("User-Agent"),
		"received_at":  time.Now().UTC().Format(time.RFC3339Nano),
	}
}

type basicAuth struct {
	username string
	password string
}

func parseBasicAuth(header string) basicAuth {
	if !strings.HasPrefix(header, "Basic ") {
		return basicAuth{}
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(strings.TrimPrefix(header, "Basic ")))
	if err != nil {
		return basicAuth{}
	}
	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return basicAuth{}
	}
	return basicAuth{username: parts[0], password: parts[1]}
}

func isAuthorized(auth basicAuth, publicKey, secretKey string) bool {
	if strings.TrimSpace(publicKey) == "" && strings.TrimSpace(secretKey) == "" {
		return true
	}
	return auth.username == publicKey && auth.password == secretKey
}

func readAndDecodeBody(r *http.Request) ([]byte, error) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Encoding"))) {
	case "", "identity":
		return body, nil
	case "gzip":
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	case "deflate":
		reader, err := zlib.NewReader(bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		defer reader.Close()
		return io.ReadAll(reader)
	default:
		return nil, fmt.Errorf("unsupported content-encoding: %s", r.Header.Get("Content-Encoding"))
	}
}

func decodeTraceRequest(body []byte, contentType string) (otlp.ExportTraceServiceRequest, error) {
	if strings.Contains(strings.ToLower(contentType), "application/json") {
		return DecodeJSONTraceRequest(body)
	}
	return proto.DecodeExportTraceServiceRequest(body)
}

func decodeMetricsRequest(body []byte, contentType string) (otlp.ExportMetricsServiceRequest, error) {
	if strings.Contains(strings.ToLower(contentType), "application/json") {
		return DecodeJSONMetricsRequest(body)
	}
	return proto.DecodeExportMetricsServiceRequest(body)
}

func sendTraceSuccess(w http.ResponseWriter, contentType string, saved SaveResult) {
	headers := w.Header()
	headers.Set("x-gtrace-batch-id", saved.ID)
	headers.Set("x-gtrace-span-count", strconv.Itoa(saved.SpanCount))
	if strings.Contains(strings.ToLower(contentType), "application/json") {
		headers.Set("content-type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}\n"))
		return
	}
	headers.Set("content-type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

func sendMetricsSuccess(w http.ResponseWriter, contentType string, saved SaveResult) {
	headers := w.Header()
	headers.Set("x-gtrace-batch-id", saved.ID)
	headers.Set("x-gtrace-metric-count", strconv.Itoa(saved.MetricCount))
	if strings.Contains(strings.ToLower(contentType), "application/json") {
		headers.Set("content-type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}\n"))
		return
	}
	headers.Set("content-type", "application/x-protobuf")
	w.WriteHeader(http.StatusOK)
}

func sendJSON(w http.ResponseWriter, status int, body map[string]any) {
	data, _ := json.Marshal(body)
	w.Header().Set("content-type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(append(data, '\n'))
}

func sendBadRequest(w http.ResponseWriter, err error) {
	sendJSON(w, http.StatusBadRequest, map[string]any{"error": "bad_request", "message": err.Error()})
}

func sendError(w http.ResponseWriter, err error) {
	sendJSON(w, http.StatusInternalServerError, map[string]any{"error": "internal_error", "message": err.Error()})
}

func mergeMaps(base, extra map[string]any) map[string]any {
	if len(base) == 0 && len(extra) == 0 {
		return nil
	}
	out := map[string]any{}
	for key, value := range base {
		out[key] = value
	}
	for key, value := range extra {
		out[key] = value
	}
	return out
}
