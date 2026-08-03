package transport

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Config struct {
	Endpoint    string
	TracePath   string
	MetricsPath string
	TraceURL    string
	MetricsURL  string
	Headers     map[string]string
	PublicKey   string
	SecretKey   string
	Timeout     time.Duration
}

type Result struct {
	StatusCode int
	Body       string
}

type Client struct {
	HTTPClient *http.Client
	Config     Config
}

func (c Client) Upload(signal string, body []byte) (Result, error) {
	url := c.signalURL(signal)
	if strings.TrimSpace(url) == "" {
		return Result{}, fmt.Errorf("%s endpoint is empty", signal)
	}
	request, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return Result{}, err
	}
	for key, value := range c.Config.Headers {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			request.Header.Set(key, value)
		}
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
	responseBody, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
	result := Result{StatusCode: response.StatusCode, Body: strings.TrimSpace(string(responseBody))}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return result, fmt.Errorf("OTLP %s upload failed: HTTP %d", signal, response.StatusCode)
	}
	return result, nil
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
