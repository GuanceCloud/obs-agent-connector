package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestLogsUseExplicitRouteAndRespectPartialSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, contentType, body string
		rejected                int64
		invalid                 bool
	}{
		{"empty", "application/x-protobuf", "", 0, false},
		{"rejected", "application/x-protobuf", string([]byte{0x0a, 0x02, 0x08, 0x01}), 1, false},
		{"json", "application/json", `{"partialSuccess":{"rejectedLogRecords":"1"}}`, 1, false},
		{"warning", "application/json", `{"partialSuccess":{"errorMessage":"warning"}}`, 0, false},
		{"malformed", "application/x-protobuf", string([]byte{0x0a, 0x08}), 0, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := Client{Config: Config{Endpoint: "https://collector.example", LogsPath: "otel/v1/logs"}, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.URL.Path != "/otel/v1/logs" {
					t.Fatalf("wrong logs route %s", r.URL.Path)
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{"Content-Type": []string{tc.contentType}}}, nil
			})}}
			result, err := client.Upload("logs", []byte{1})
			if result.RejectedLogRecords != tc.rejected {
				t.Fatal("rejected count lost")
			}
			if tc.rejected > 0 {
				var e *UploadError
				if !errors.As(err, &e) || e.Retryable() {
					t.Fatalf("partial success should not be replayed: %v", err)
				}
			} else if (err != nil) != tc.invalid {
				t.Fatalf("unexpected error %v", err)
			}
		})
	}
	client := Client{Config: Config{Endpoint: "https://collector.example", TracePath: "v1/traces"}}
	if _, err := client.Upload("logs", nil); err == nil {
		t.Fatal("invented a Logs endpoint")
	}
	client.Config.LogsURL = "https://logs.example/v1/logs"
	if client.signalURL("logs") != client.Config.LogsURL {
		t.Fatal("explicit URL not honored")
	}
}

func TestLogsRetryAfterSurvivesTheTransportBoundary(t *testing.T) {
	client := Client{Config: Config{LogsURL: "https://collector.example/v1/logs"}, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader("")), Header: http.Header{"Retry-After": []string{"120"}}}, nil
	})}}
	_, err := client.Upload("logs", nil)
	var response *UploadError
	if !errors.As(err, &response) || !response.Retryable() || response.RetryAfter != 120*time.Second {
		t.Fatalf("server retry delay lost: %v", err)
	}
}

func TestLogsCancellationAndRetryPolicy(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := Client{Config: Config{LogsURL: "https://collector.example/v1/logs"}}
	if _, err := client.UploadContext(ctx, "logs", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("lost cancellation: %v", err)
	}
	for _, status := range []int{429, 502, 503, 504} {
		if !(&UploadError{StatusCode: status}).Retryable() {
			t.Fatal("retryable status lost")
		}
	}
	for _, status := range []int{400, 401, 403, 404, 500, 200} {
		if (&UploadError{StatusCode: status}).Retryable() {
			t.Fatal("permanent response retried")
		}
	}
}
