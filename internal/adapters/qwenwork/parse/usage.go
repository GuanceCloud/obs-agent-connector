package parse

import (
	"math"
	"sort"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

var usageFields = []string{"input_tokens", "output_tokens", "cache_read_input_tokens", "cache_creation_input_tokens", "reasoning_output_tokens"}

func tokenCount(value any) (int64, bool) {
	n, ok := value.(float64)
	// JSON numbers are decoded as float64. Reject fractional, negative and
	// unsafe integers rather than truncating or overflowing a token count.
	if !ok || math.IsNaN(n) || n < 0 || n > 1<<53-1 || math.Trunc(n) != n {
		return 0, false
	}
	return int64(n), true
}

func mergeUsage(call *model.LLMCall, raw map[string]any) {
	dest := []*int64{&call.Usage.InputTokens, &call.Usage.OutputTokens, &call.Usage.CacheReadTokens, &call.Usage.CacheCreateTokens, &call.Usage.ReasoningTokens}
	for i, field := range usageFields {
		if count, valid := tokenCount(raw[field]); valid {
			// Snapshots replace only fields actually present; they are never deltas.
			*dest[i] = count
			call.ExtraAttributes["qwenwork.usage."+field+".observed"] = true
		}
	}
}

func observeUsage(rows []map[string]any, observation *UsageObservation) {
	calls := map[string]bool{}
	fields := map[string]bool{}
	for _, field := range observation.Fields {
		fields[field] = true
	}
	for _, row := range rows {
		if str(row["type"]) != "assistant" || timestamp(row["timestamp"]) == 0 {
			continue
		}
		m := object(row["message"])
		id := str(m["id"])
		if id == "" {
			id = str(row["uuid"])
		}
		if id == "" {
			continue
		}
		observed := calls[id]
		for _, field := range usageFields {
			if _, valid := tokenCount(object(m["usage"])[field]); valid {
				observed = true
				fields[field] = true
				if at := timestamp(row["timestamp"]); at > observation.ObservedAt {
					observation.ObservedAt = at
				}
			}
		}
		calls[id] = observed
	}
	observation.LLMCalls += len(calls)
	for _, observed := range calls {
		if observed {
			observation.ObservedCalls++
		}
	}
	observation.Fields = nil
	for field := range fields {
		observation.Fields = append(observation.Fields, field)
	}
	sort.Strings(observation.Fields)
}
