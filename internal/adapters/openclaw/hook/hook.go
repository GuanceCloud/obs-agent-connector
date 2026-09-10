package hook

import (
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/openclaw/buildinfo"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/openclaw/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/openclaw/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/exporter"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/hooklog"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
)

func RunCLI() int {
	cfg := config.Resolve(config.ResolveOptions{})
	if err := Run(cfg, os.Stdin, nil); err != nil {
		_ = hooklog.Append(cfg.LogFile, "OpenClaw telemetry failed", map[string]any{"error": err.Error()})
	}
	return 0
}
func Run(cfg config.Config, input io.Reader, client *http.Client) error {
	if !cfg.Enabled {
		return nil
	}
	body, err := io.ReadAll(io.LimitReader(input, 8*1024*1024+1))
	if err != nil {
		return fmt.Errorf("read OpenClaw event")
	}
	if len(body) > 8*1024*1024 {
		return fmt.Errorf("OpenClaw event exceeds 8 MiB")
	}
	payload, err := parse.Decode(body)
	if err != nil {
		return fmt.Errorf("invalid OpenClaw event JSON")
	}
	e := exporter.Exporter{Root: cfg.StateDir, Transport: cfg.Transport, HTTPClient: client, Builder: semantic.Builder{ScopeName: "gtrace-openclaw-collector", ScopeVersion: buildinfo.Version}}
	if turn, ok := parse.Normalize(payload, cfg); ok {
		if err = e.Enqueue(turn); err != nil {
			return err
		}
	}
	return e.Flush()
}
