package transport

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestUploadUsesProtobufAndDoesNotOverrideAuthorization(t *testing.T) {
	client := Client{
		Config: Config{
			Endpoint:  "https://collector.example",
			TracePath: "v1/traces",
			Headers:   map[string]string{"Authorization": "Bearer configured"},
			PublicKey: "public",
			SecretKey: "secret",
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.URL.String() != "https://collector.example/v1/traces" {
				t.Fatalf("unexpected URL %s", request.URL)
			}
			if request.Header.Get("Content-Type") != "application/x-protobuf" {
				t.Fatal("missing protobuf content type")
			}
			if request.Header.Get("Content-Encoding") != "gzip" {
				t.Fatalf("unexpected content-encoding %q", request.Header.Get("Content-Encoding"))
			}
			if request.Header.Get("Authorization") != "Bearer configured" {
				t.Fatal("configured authorization was overwritten")
			}
			reader, err := gzip.NewReader(request.Body)
			if err != nil {
				t.Fatalf("new gzip reader: %v", err)
			}
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("read gzip body: %v", err)
			}
			if err := reader.Close(); err != nil {
				t.Fatalf("close gzip reader: %v", err)
			}
			if len(body) != 3 || body[0] != 1 || body[1] != 2 || body[2] != 3 {
				t.Fatalf("unexpected decoded body %v", body)
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		})},
	}
	if _, err := client.Upload("traces", []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
}

func TestJoinEndpointAvoidsDuplicateSignalPath(t *testing.T) {
	value := JoinEndpoint("https://collector.example/v1/traces", "v1/traces")
	if value != "https://collector.example/v1/traces" {
		t.Fatalf("unexpected URL %s", value)
	}
}

func TestUploadCompressesBodyByDefault(t *testing.T) {
	client := Client{
		Config: Config{
			Endpoint:  "https://collector.example",
			TracePath: "v1/traces",
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Content-Encoding") != "gzip" {
				t.Fatalf("unexpected content-encoding %q", request.Header.Get("Content-Encoding"))
			}
			reader, err := gzip.NewReader(request.Body)
			if err != nil {
				t.Fatalf("new gzip reader: %v", err)
			}
			body, err := io.ReadAll(reader)
			if err != nil {
				t.Fatalf("read gzip body: %v", err)
			}
			if err := reader.Close(); err != nil {
				t.Fatalf("close gzip reader: %v", err)
			}
			if string(body) != "payload" {
				t.Fatalf("unexpected decoded body %q", string(body))
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		})},
	}
	if _, err := client.Upload("traces", []byte("payload")); err != nil {
		t.Fatal(err)
	}
}

func TestUploadAllowsIdentityOverride(t *testing.T) {
	client := Client{
		Config: Config{
			Endpoint:  "https://collector.example",
			TracePath: "v1/traces",
			Headers:   map[string]string{"Content-Encoding": "identity"},
		},
		HTTPClient: &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
			if request.Header.Get("Content-Encoding") != "identity" {
				t.Fatalf("unexpected content-encoding %q", request.Header.Get("Content-Encoding"))
			}
			body, err := io.ReadAll(request.Body)
			if err != nil {
				t.Fatalf("read body: %v", err)
			}
			if string(body) != "payload" {
				t.Fatalf("unexpected decoded body %q", string(body))
			}
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("")),
				Header:     make(http.Header),
			}, nil
		})},
	}
	if _, err := client.Upload("traces", []byte("payload")); err != nil {
		t.Fatal(err)
	}
}
