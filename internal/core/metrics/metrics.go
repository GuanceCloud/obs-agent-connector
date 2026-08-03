package metrics

import (
	"strings"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
)

var (
	workflowDuration = metricMeta{
		Name:        "gen_ai.workflow.duration",
		Type:        "histogram",
		Unit:        "s",
		Description: "GenAI workflow duration.",
	}
	tokenUsage = metricMeta{
		Name:        "gen_ai.client.token.usage",
		Type:        "histogram",
		Unit:        "{token}",
		Description: "Number of input and output tokens used.",
	}
	operationCount = metricMeta{
		Name:        "gen_ai.agent.operation.count",
		Type:        "sum",
		Unit:        "",
		Description: "Agent-side operation count.",
	}
	operationDuration = metricMeta{
		Name:        "gen_ai.agent.operation.duration",
		Type:        "histogram",
		Unit:        "ms",
		Description: "Agent-side operation duration.",
	}
)

type metricMeta struct {
	Name        string
	Type        string
	Unit        string
	Description string
}

func Build(spans []model.Span) []model.Metric {
	metrics := make([]model.Metric, 0)
	for _, span := range spans {
		switch {
		case span.Name == "invoke_agent":
			metrics = append(metrics, requestMetrics(span)...)
		case span.Name == "llm":
			metrics = append(metrics, llmMetrics(span)...)
		case strings.HasPrefix(span.Name, "tool:"):
			metrics = append(metrics, toolMetrics(span)...)
		case strings.HasPrefix(span.Name, "skill:"):
			metrics = append(metrics, skillMetrics(span)...)
		}
	}
	return metrics
}

func requestMetrics(span model.Span) []model.Metric {
	attributes := map[string]any{
		"agent_runtime":          span.Resource["agent_runtime"],
		"gen_ai.conversation.id": span.Attributes["gen_ai.conversation.id"],
		"session_id":             firstNonNil(span.Attributes["session_id"], span.Attributes["gen_ai.conversation.id"]),
		"error.type":             span.Attributes["error.type"],
	}
	setAttr(attributes, "final_status", requestStatus(span))
	setAttr(attributes, "status", requestStatus(span))
	duration := spanDurationSeconds(span)
	if duration <= 0 {
		return nil
	}
	return []model.Metric{metric(workflowDuration, span, duration, attributes)}
}

func llmMetrics(span model.Span) []model.Metric {
	operationAttributes := baseAttrs(span)
	if operationStatus(span) == "error" {
		setAttr(operationAttributes, "error.type", firstNonNil(span.Attributes["error.type"], "_OTHER"))
	}
	out := []model.Metric{metric(operationCount, span, 1, countAttrs(span))}
	if duration := positiveFloat(float64(span.DurationMs)); duration > 0 {
		out = append(out, metric(operationDuration, span, duration, operationAttributes))
	}

	for _, token := range []struct {
		Key  string
		Type string
	}{
		{Key: "gen_ai.usage.input_tokens", Type: "input"},
		{Key: "gen_ai.usage.output_tokens", Type: "output"},
	} {
		value := valueFloat(span.Attributes[token.Key])
		if value <= 0 {
			continue
		}
		attrs := baseAttrs(span)
		setAttr(attrs, "gen_ai.token.type", token.Type)
		out = append(out, metric(tokenUsage, span, value, attrs))
	}
	return out
}

func toolMetrics(span model.Span) []model.Metric {
	attributes := baseAttrs(span)
	if operationStatus(span) == "error" {
		setAttr(attributes, "error.type", firstNonNil(span.Attributes["error.type"], "_OTHER"))
	}
	setAttr(attributes, "tool_name", firstNonNil(span.Attributes["gen_ai.tool.name"], strings.TrimPrefix(span.Name, "tool:")))
	setAttr(attributes, "gen_ai.tool.name", firstNonNil(span.Attributes["gen_ai.tool.name"], strings.TrimPrefix(span.Name, "tool:")))
	setAttr(attributes, "skill_name", firstNonNil(span.Attributes["skill.name"], span.Attributes["gen_ai.skill.name"]))
	setAttr(attributes, "skill.name", span.Attributes["skill.name"])
	setAttr(attributes, "gen_ai.skill.name", span.Attributes["gen_ai.skill.name"])

	out := []model.Metric{metric(operationCount, span, 1, countAttrs(span))}
	if duration := positiveFloat(float64(span.DurationMs)); duration > 0 {
		out = append(out, metric(operationDuration, span, duration, attributes))
	}
	return out
}

func skillMetrics(span model.Span) []model.Metric {
	attributes := baseAttrs(span)
	setAttr(attributes, "skill_name", firstNonNil(span.Attributes["skill.name"], span.Attributes["gen_ai.skill.name"]))
	setAttr(attributes, "skill.name", span.Attributes["skill.name"])
	setAttr(attributes, "gen_ai.skill.name", span.Attributes["gen_ai.skill.name"])
	setAttr(attributes, "skill_source", firstNonNil(span.Attributes["skill.source.type"], span.Attributes["gen_ai.skill.source.type"]))
	setAttr(attributes, "skill.source.type", span.Attributes["skill.source.type"])
	setAttr(attributes, "gen_ai.skill.source.type", span.Attributes["gen_ai.skill.source.type"])
	setAttr(attributes, "skill.result_status", span.Attributes["skill.result_status"])
	setAttr(attributes, "gen_ai.skill.result.status", span.Attributes["gen_ai.skill.result.status"])
	setAttr(attributes, "gen_ai.skill.version", span.Attributes["gen_ai.skill.version"])
	if operationStatus(span) == "error" {
		setAttr(attributes, "error.type", firstNonNil(span.Attributes["error.type"], "_OTHER"))
	}

	out := []model.Metric{metric(operationCount, span, 1, countAttrs(span))}
	if duration := positiveFloat(float64(span.DurationMs)); duration > 0 {
		out = append(out, metric(operationDuration, span, duration, attributes))
	}
	return out
}

func metric(meta metricMeta, span model.Span, value float64, attributes map[string]any) model.Metric {
	return model.Metric{
		Name:              meta.Name,
		Type:              meta.Type,
		Unit:              meta.Unit,
		Description:       meta.Description,
		Value:             value,
		Attributes:        attributes,
		Resource:          span.Resource,
		Scope:             span.Scope,
		StartTimeUnixNano: span.StartTimeUnixNano,
		TimeUnixNano:      firstNonEmptyString(span.EndTimeUnixNano, span.StartTimeUnixNano),
	}
}

func baseAttrs(span model.Span) map[string]any {
	attributes := map[string]any{}
	modelName := firstNonNil(span.Attributes["gen_ai.response.model"], span.Attributes["gen_ai.request.model"])
	setAttr(attributes, "agent_runtime", span.Resource["agent_runtime"])
	setAttr(attributes, "gen_ai.conversation.id", span.Attributes["gen_ai.conversation.id"])
	setAttr(attributes, "session_id", firstNonNil(span.Attributes["session_id"], span.Attributes["gen_ai.conversation.id"]))
	setAttr(attributes, "operation_name", legacyOperationName(span))
	setAttr(attributes, "gen_ai.operation.name", span.Attributes["gen_ai.operation.name"])
	setAttr(attributes, "status", operationStatus(span))
	setAttr(attributes, "provider_name", span.Attributes["gen_ai.provider.name"])
	setAttr(attributes, "gen_ai.provider.name", span.Attributes["gen_ai.provider.name"])
	setAttr(attributes, "request_model", span.Attributes["gen_ai.request.model"])
	setAttr(attributes, "gen_ai.request.model", span.Attributes["gen_ai.request.model"])
	setAttr(attributes, "response_model", span.Attributes["gen_ai.response.model"])
	setAttr(attributes, "gen_ai.response.model", span.Attributes["gen_ai.response.model"])
	setAttr(attributes, "model_name", modelName)
	setAttr(attributes, "error.type", span.Attributes["error.type"])
	return attributes
}

func countAttrs(span model.Span) map[string]any {
	attributes := map[string]any{
		"agent_runtime":          span.Resource["agent_runtime"],
		"gen_ai.conversation.id": span.Attributes["gen_ai.conversation.id"],
		"session_id":             firstNonNil(span.Attributes["session_id"], span.Attributes["gen_ai.conversation.id"]),
		"gen_ai.operation.name":  span.Attributes["gen_ai.operation.name"],
		"status":                 operationStatus(span),
	}
	if operationStatus(span) == "error" {
		setAttr(attributes, "error.type", firstNonNil(span.Attributes["error.type"], "_OTHER"))
	}
	if span.Name == "llm" {
		setAttr(attributes, "gen_ai.provider.name", span.Attributes["gen_ai.provider.name"])
		setAttr(attributes, "gen_ai.request.model", span.Attributes["gen_ai.request.model"])
		setAttr(attributes, "gen_ai.response.model", span.Attributes["gen_ai.response.model"])
		return attributes
	}
	if strings.HasPrefix(span.Name, "tool:") {
		setAttr(attributes, "gen_ai.tool.name", firstNonNil(span.Attributes["gen_ai.tool.name"], strings.TrimPrefix(span.Name, "tool:")))
		return attributes
	}
	if strings.HasPrefix(span.Name, "skill:") {
		setAttr(attributes, "gen_ai.skill.name", firstNonNil(span.Attributes["gen_ai.skill.name"], span.Attributes["skill.name"]))
		return attributes
	}
	return attributes
}

func requestStatus(span model.Span) string {
	if asString(span.Attributes["status"]) == "error" || strings.Contains(strings.ToUpper(span.Status.Code), "ERROR") {
		return "error"
	}
	finalStatus := asString(span.Attributes["final_status"])
	if finalStatus == "completed" || finalStatus == "cancelled" {
		return "completed"
	}
	return "completed"
}

func operationStatus(span model.Span) string {
	if asString(span.Attributes["tool_result_status"]) == "error" {
		return "error"
	}
	if asString(span.Attributes["status"]) == "error" || strings.Contains(strings.ToUpper(span.Status.Code), "ERROR") {
		return "error"
	}
	return "ok"
}

func legacyOperationName(span model.Span) any {
	switch {
	case span.Name == "llm":
		return "model"
	case strings.HasPrefix(span.Name, "tool:"):
		return "tool"
	case strings.HasPrefix(span.Name, "skill:"):
		return "skill"
	default:
		return nil
	}
}

func spanDurationSeconds(span model.Span) float64 {
	if value := positiveFloat(float64(span.DurationMs)); value > 0 {
		return value / 1000
	}
	return 0
}

func positiveFloat(value float64) float64 {
	if value > 0 {
		return value
	}
	return 0
}

func valueFloat(value any) float64 {
	switch current := value.(type) {
	case float64:
		return current
	case int:
		return float64(current)
	case int64:
		return float64(current)
	default:
		return 0
	}
}

func setAttr(attributes map[string]any, key string, value any) {
	if key == "" || value == nil {
		return
	}
	if text, ok := value.(string); ok && text == "" {
		return
	}
	attributes[key] = value
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value == nil {
			continue
		}
		if text, ok := value.(string); ok && strings.TrimSpace(text) == "" {
			continue
		}
		return value
	}
	return nil
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func asString(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	return ""
}
