package app

import (
	"context"
	"os/exec"
	"time"
)

var runOpenClawRestart = func(ctx context.Context) error {
	return exec.CommandContext(ctx, "openclaw", "gateway", "restart").Run()
}

func restartOpenClawAfterInstall() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	printSingleDetail("Gateway", "restarting OpenClaw")
	if err := runOpenClawRestart(ctx); err != nil {
		reason := "restart failed or OpenClaw CLI unavailable"
		if ctx.Err() != nil {
			reason = "restart timed out"
		}
		printSingleDetail("Warning", "OpenClaw Gateway "+reason+"; installation succeeded. Restart the OpenClaw Gateway manually with: openclaw gateway restart")
		return
	}
	printSingleDetail("Gateway", "OpenClaw restarted")
}
