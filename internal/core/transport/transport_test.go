package transport

import (
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
			if request.Header.Get("Authorization") != "Bearer configured" {
				t.Fatal("configured authorization was overwritten")
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
