package hook

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/omp/parse"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

// Parent roots and delegate tools must have stable IDs: a background child can
// finish after its parent's upload, in a different collector process.
func identityID(session, turn, kind, call string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s\x00%s\x00%s\x00%s", session, turn, kind, call)))
	return hex.EncodeToString(sum[:8])
}

func linkedSpans(turn model.Turn, snapshot parse.Snapshot) []model.Span {
	spans := buildSpans(turn)
	if len(spans) == 0 {
		return spans
	}
	ids := map[string]string{spans[0].SpanID: identityID(turn.SessionID, turn.TurnID, "root", "")}
	for _, span := range spans {
		if span.Attributes["gen_ai.operation.name"] == "execute_tool" {
			if call, ok := span.Attributes["gen_ai.tool.call.id"].(string); ok && call != "" {
				ids[span.SpanID] = identityID(turn.SessionID, turn.TurnID, "tool", call)
			}
		}
	}
	traceID := snapshot.TraceID
	if decoded, err := hex.DecodeString(traceID); err != nil || len(decoded) != 16 || traceID == "00000000000000000000000000000000" {
		traceID = spans[0].TraceID
	}
	for i := range spans {
		spans[i].TraceID = traceID
		if id, ok := ids[spans[i].SpanID]; ok {
			spans[i].SpanID = id
		}
		if id, ok := ids[spans[i].ParentID]; ok {
			spans[i].ParentID = id
		}
	}
	if parent := snapshot.Parent; parent != nil {
		spans[0].ParentID = identityID(parent.SessionID, parent.TurnID, "tool", parent.ToolCallID)
	}
	return spans
}
