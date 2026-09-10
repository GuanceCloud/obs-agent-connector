package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

type Config struct {
	Enabled        bool
	CaptureContent string
	MaxChars       int
	Resource       map[string]any
	Transport      transport.Config
	StateDir       string
	LogFile        string
}

func LoadConfig(path string) (Config, error) {
	cfg := Config{MaxChars: 20000, CaptureContent: "preview", StateDir: filepath.Join(filepath.Dir(path), "state"), LogFile: filepath.Join(filepath.Dir(path), "gtrace-hooks.json")}
	body, err := os.ReadFile(path)
	if err != nil {
		return cfg, err
	}
	var raw struct {
		Enabled         bool              `json:"enabled"`
		Endpoint        string            `json:"endpoint"`
		TracePath       string            `json:"tracePath"`
		MetricsPath     string            `json:"metricsPath"`
		Headers         map[string]string `json:"headers"`
		Resource        map[string]any    `json:"resourceAttributes"`
		CaptureContent  string            `json:"captureContent"`
		MaxChars        int               `json:"maxChars"`
		LegacyMaxChars  int               `json:"max_chars"`
		TimeoutMs       int               `json:"timeoutMs"`
		LegacyTimeoutMs int               `json:"timeout_ms"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return cfg, fmt.Errorf("parse OMP runtime config: %w", err)
	}
	cfg.Enabled, cfg.Resource = raw.Enabled, raw.Resource
	if raw.CaptureContent == "none" || raw.CaptureContent == "full" {
		cfg.CaptureContent = raw.CaptureContent
	}
	if raw.MaxChars == 0 {
		raw.MaxChars = raw.LegacyMaxChars
	}
	if raw.MaxChars > 0 {
		cfg.MaxChars = min(raw.MaxChars, 100000)
	}
	if raw.TimeoutMs == 0 {
		raw.TimeoutMs = raw.LegacyTimeoutMs
	}
	if raw.TimeoutMs <= 0 {
		raw.TimeoutMs = 10000
	}
	if raw.TracePath == "" {
		raw.TracePath = "v1/write/otel-llm"
	}
	if raw.MetricsPath == "" {
		raw.MetricsPath = "v1/write/otel-metrics"
	}
	cfg.Transport = transport.Config{Endpoint: strings.TrimRight(raw.Endpoint, "/"), TracePath: raw.TracePath, MetricsPath: raw.MetricsPath, Headers: raw.Headers, Timeout: time.Duration(min(raw.TimeoutMs, 30000)) * time.Millisecond}
	return cfg, nil
}
