package bridge

import (
	"strings"
	"testing"
)

func TestPiExtensionDoesNotPropagateSubagentContext(t *testing.T) {
	for _, unsupported := range []string{
		"OBS_AGENT_CONNECTOR_PI_TRACEPARENT",
		"OBS_AGENT_CONNECTOR_PI_PARENT_TOOL_CALL_ID",
		"obs-agent-connector.pi.subagent-context",
	} {
		if strings.Contains(Extension, unsupported) {
			t.Fatalf("unsupported subagent propagation remains: %s", unsupported)
		}
	}
}

func TestPiExtensionUsesOneBoundedQueueWorker(t *testing.T) {
	for _, expected := range []string{`openSync(workerLock, "wx"`, `"--drain"`, `"--worker-lock"`} {
		if !strings.Contains(Extension, expected) {
			t.Fatalf("queue worker guard is missing %q", expected)
		}
	}
	if strings.Contains(Extension, "readdirSync(queueDir)") {
		t.Fatal("extension still launches per-file retry workers")
	}
}
