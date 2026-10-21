package transport

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
)

type Config struct {
	Endpoint    string
	TracePath   string
	MetricsPath string
	TraceURL    string
	MetricsURL  string
	LogsPath    string
	LogsURL     string
	Headers     map[string]string
	PublicKey   string
	SecretKey   string
	Timeout     time.Duration
}

type Result struct {
	StatusCode         int
	Body               string
	RejectedLogRecords int64
}

// UploadError exposes retry policy without persisting server response bodies.
type UploadError struct {
	StatusCode         int
	RejectedLogRecords int64
	RetryAfter         time.Duration
}

func (e *UploadError) Error() string {
	if e.RejectedLogRecords > 0 {
		return fmt.Sprintf("OTLP logs partial success: %d records rejected", e.RejectedLogRecords)
	}
	return fmt.Sprintf("OTLP upload failed: HTTP %d", e.StatusCode)
}

func (e *UploadError) Retryable() bool {
	return e.StatusCode == 429 || e.StatusCode == 502 || e.StatusCode == 503 || e.StatusCode == 504
}

type Client struct {
	HTTPClient *http.Client
	Config     Config
}

func (c Client) Upload(signal string, body []byte) (Result, error) {
	return c.UploadContext(context.Background(), signal, body)
}

func (c Client) UploadContext(ctx context.Context, signal string, body []byte) (Result, error) {
	url := c.signalURL(signal)
	if strings.TrimSpace(url) == "" {
		return Result{}, fmt.Errorf("%s endpoint is empty", signal)
	}
	contentEncoding := requestContentEncoding(c.Config.Headers)
	payload, err := encodeRequestBody(body, contentEncoding)
	if err != nil {
		return Result{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return Result{}, err
	}
	for key, value := range c.Config.Headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			request.Header.Set(key, value)
		}
	}
	if request.Header.Get("Content-Encoding") == "" && contentEncoding == "gzip" {
		request.Header.Set("Content-Encoding", "gzip")
	}
	request.Header.Set("Content-Type", "application/x-protobuf")
	if request.Header.Get("Authorization") == "" && (c.Config.PublicKey != "" || c.Config.SecretKey != "") {
		raw := c.Config.PublicKey + ":" + c.Config.SecretKey
		request.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(raw)))
	}
	client := c.HTTPClient
	if client == nil {
		timeout := c.Config.Timeout
		if timeout <= 0 {
			timeout = 25 * time.Second
		}
		client = &http.Client{Timeout: timeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return Result{}, err
	}
	defer response.Body.Close()
	limit := int64(4096)
	if signal == "logs" {
		limit = 65536
	}
	responseBody, readErr := io.ReadAll(io.LimitReader(response.Body, limit+1))
	result := Result{StatusCode: response.StatusCode, Body: strings.TrimSpace(string(responseBody))}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		if signal != "logs" {
			return result, fmt.Errorf("OTLP %s upload failed: HTTP %d", signal, response.StatusCode)
		}
		return result, &UploadError{StatusCode: response.StatusCode, RetryAfter: retryAfter(response.Header.Get("Retry-After"))}
	}
	if signal == "logs" {
		if readErr != nil {
			return result, fmt.Errorf("read OTLP logs response: %w", readErr)
		}
		if int64(len(responseBody)) > limit {
			return result, fmt.Errorf("OTLP logs response exceeds limit")
		}
		var rejected int64
		if strings.Contains(strings.ToLower(response.Header.Get("Content-Type")), "json") {
			var value struct {
				PartialSuccess struct {
					Rejected json.RawMessage `json:"rejectedLogRecords"`
				} `json:"partialSuccess"`
			}
			if len(responseBody) > 0 {
				if err := json.Unmarshal(responseBody, &value); err != nil {
					return result, fmt.Errorf("decode OTLP logs response: %w", err)
				}
			}
			if len(value.PartialSuccess.Rejected) > 0 {
				text := strings.Trim(string(value.PartialSuccess.Rejected), "\"")
				if err := json.Unmarshal([]byte(text), &rejected); err != nil || rejected < 0 {
					return result, fmt.Errorf("invalid OTLP logs rejected count")
				}
			}
		} else {
			var err error
			rejected, err = proto.DecodeLogsRejected(responseBody)
			if err != nil {
				return result, fmt.Errorf("decode OTLP logs response: %w", err)
			}
		}
		result.RejectedLogRecords = rejected
		if rejected > 0 {
			return result, &UploadError{StatusCode: response.StatusCode, RejectedLogRecords: rejected}
		}
	}
	return result, nil
}

func retryAfter(value string) time.Duration {
	if seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64); err == nil && seconds > 0 {
		if seconds > 86400 {
			seconds = 86400
		}
		return time.Duration(seconds) * time.Second
	}
	if date, err := http.ParseTime(value); err == nil {
		delay := time.Until(date)
		if delay > 24*time.Hour {
			return 24 * time.Hour
		}
		if delay > 0 {
			return delay
		}
	}
	return 0
}

func requestContentEncoding(headers map[string]string) string {
	value := strings.ToLower(strings.TrimSpace(headerValue(headers, "Content-Encoding")))
	if value == "" {
		return "gzip"
	}
	return value
}

func encodeRequestBody(body []byte, contentEncoding string) ([]byte, error) {
	switch strings.ToLower(strings.TrimSpace(contentEncoding)) {
	case "identity":
		return body, nil
	case "gzip":
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		if _, err := writer.Write(body); err != nil {
			_ = writer.Close()
			return nil, err
		}
		if err := writer.Close(); err != nil {
			return nil, err
		}
		return compressed.Bytes(), nil
	case "":
		return body, nil
	default:
		return body, nil
	}
}

func headerValue(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(strings.TrimSpace(key), name) {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func (c Client) signalURL(signal string) string {
	switch signal {
	case "traces":
		if c.Config.TraceURL != "" {
			return c.Config.TraceURL
		}
		return JoinEndpoint(c.Config.Endpoint, c.Config.TracePath)
	case "metrics":
		if c.Config.MetricsURL != "" {
			return c.Config.MetricsURL
		}
		return JoinEndpoint(c.Config.Endpoint, c.Config.MetricsPath)
	case "logs":
		if c.Config.LogsURL != "" {
			return c.Config.LogsURL
		}
		// A logs route must be explicit: GTrace trace endpoints need not accept logs.
		if strings.TrimSpace(c.Config.LogsPath) == "" {
			return ""
		}
		return JoinEndpoint(c.Config.Endpoint, c.Config.LogsPath)
	default:
		return ""
	}
}

func JoinEndpoint(endpoint, signalPath string) string {
	endpoint = strings.TrimSpace(endpoint)
	path := strings.Trim(strings.TrimSpace(signalPath), "/")
	if endpoint == "" || path == "" {
		if path == "" {
			return endpoint
		}
		return ""
	}
	withoutQuery := strings.SplitN(endpoint, "?", 2)[0]
	withoutQuery = strings.SplitN(withoutQuery, "#", 2)[0]
	if strings.HasSuffix(strings.TrimRight(withoutQuery, "/"), "/"+path) {
		return endpoint
	}
	return strings.TrimRight(endpoint, "/") + "/" + path
}

func RedactedHeaders(headers map[string]string) string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	body, _ := json.Marshal(keys)
	return string(body)
}
