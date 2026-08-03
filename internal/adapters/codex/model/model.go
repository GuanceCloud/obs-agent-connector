package model

import coremodel "github.com/GuanceCloud/obs-agent-connector/internal/core/model"

type SessionMeta struct {
	SessionID        string
	CLIVersion       string
	ModelProvider    string
	BaseInstructions string
	CreatedAt        string
	Channel          string
}

type AssistantMessage struct {
	Text              string
	StartTime         int64
	EndTime           int64
	EventTime         int64
	HasEventTime      bool
	ResponseItemCount int
}

type ToolCall struct {
	CallID    string
	Name      string
	Args      any
	Output    any
	Error     string
	StartTime int64
	EndTime   int64
	HasEnd    bool
}

type Step struct {
	StartTime         int64
	EndTime           int64
	ToolCalls         []*ToolCall
	AssistantMessages []*AssistantMessage
	Text              string
	Reasoning         string
	Usage             map[string]any
	ModelEndTime      int64
	HasModelEndTime   bool
}

type Turn struct {
	TurnID            string
	StartTime         int64
	EndTime           int64
	Steps             []*Step
	SubagentThreadIDs []string
	Completed         bool
	Aborted           bool
	Model             string
	InvocationParams  map[string]any
	UserInput         string
	UserInputFallback string
	FinalOutput       string
	LastAgentMessage  string
	TotalUsage        map[string]any
}

type ParsedSession struct {
	SessionMeta SessionMeta
	Turns       []*Turn
}

type SpanStatus = coremodel.SpanStatus
type Scope = coremodel.Scope
type Span = coremodel.Span
type Metric = coremodel.Metric
