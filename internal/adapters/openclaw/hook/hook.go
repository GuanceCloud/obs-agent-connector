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
	_ = Run(cfg, os.Stdin, nil)
	return 0
}
func Run(cfg config.Config, input io.Reader, client *http.Client) (err error) {
	if !cfg.Enabled {
		return nil
	}
	defer func() {
		if err != nil {
			_ = hooklog.Append(cfg.LogFile, "hook failed", map[string]any{"error": err.Error()})
		}
	}()
	_ = hooklog.Append(cfg.LogFile, hooklog.HookInvoked, nil)
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
	e := exporter.Exporter{Root: cfg.StateDir, LogFile: cfg.LogFile, Transport: cfg.Transport, HTTPClient: client, Builder: semantic.Builder{ScopeName: "gtrace-openclaw-collector", ScopeVersion: buildinfo.Version}}
	if payload.Event == "flush" {
		return e.Flush()
	}
	turn, ok := parse.Normalize(payload, cfg)
	turns := 0
	if ok {
		turns = 1
	}
	_ = hooklog.Append(cfg.LogFile, hooklog.ParsedTranscript, map[string]any{"turns": turns})
	if ok {
		if err = e.Enqueue(turn); err != nil {
			return err
		}
	} else {
		_ = hooklog.Append(cfg.LogFile, "turn skipped", map[string]any{"reason": "no eligible terminal turn"})
	}
	return e.Flush()
}
