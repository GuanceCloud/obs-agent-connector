package bridge

import (
	"os/exec"
	"testing"
)

func TestNativeBridge(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node.js is required to exercise the native OpenClaw bridge")
	}
	if out, err := exec.Command(node, "--test", "bridge.test.mjs").CombinedOutput(); err != nil {
		t.Fatalf("native bridge: %v\n%s", err, out)
	}
}
