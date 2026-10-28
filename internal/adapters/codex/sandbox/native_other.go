//go:build !linux

package sandbox

import (
	"context"
	"fmt"
	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/config"
)

func NativeSupported() bool { return false }

func EnsureNative(cfg config.Config, executable string) error {
	if !Configured(cfg) {
		return nil
	}
	return fmt.Errorf("automatic native sandbox observation currently requires Linux")
}
func RunNative(ctx context.Context, cfg config.Config) error { return EnsureNative(cfg, "") }
