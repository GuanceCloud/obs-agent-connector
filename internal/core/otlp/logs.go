package otlp

import "github.com/GuanceCloud/obs-agent-connector/internal/core/model"

type ExportLogsServiceRequest struct {
	ResourceLogs []ResourceLogs
}

type ResourceLogs struct {
	Resource  Resource
	ScopeLogs []ScopeLogs
	SchemaURL string
}

type ScopeLogs struct {
	Scope      InstrumentationScope
	LogRecords []LogRecord
	SchemaURL  string
}

type LogRecord struct {
	TimeUnixNano           uint64
	ObservedTimeUnixNano   uint64
	SeverityNumber         uint32
	SeverityText           string
	Body                   AnyValue
	Attributes             []KeyValue
	DroppedAttributesCount uint32
	Flags                  uint32
	TraceID                []byte
	SpanID                 []byte
	EventName              string
}

func LogsToProtoRequest(records []model.LogRecord) ExportLogsServiceRequest {
	request := ExportLogsServiceRequest{}
	groups := map[string]int{}
	for _, record := range records {
		key := resourceKey(record.Resource, record.Scope)
		index, ok := groups[key]
		if !ok {
			index = len(request.ResourceLogs)
			groups[key] = index
			request.ResourceLogs = append(request.ResourceLogs, ResourceLogs{
				Resource: Resource{Attributes: attributesToOTLP(record.Resource)},
				ScopeLogs: []ScopeLogs{{Scope: InstrumentationScope{
					Name: record.Scope.Name, Version: record.Scope.Version,
					Attributes: attributesToOTLP(record.Scope.Attributes),
				}}},
			})
		}
		scope := &request.ResourceLogs[index].ScopeLogs[0]
		scope.LogRecords = append(scope.LogRecords, LogRecord{
			TimeUnixNano: record.TimeUnixNano, ObservedTimeUnixNano: record.ObservedTimeUnixNano,
			SeverityNumber: record.SeverityNumber, SeverityText: record.SeverityText,
			Body: anyValue(record.Body), Attributes: attributesToOTLP(record.Attributes),
			TraceID: bytesToOTLP(record.TraceID), SpanID: bytesToOTLP(record.SpanID),
			Flags: record.Flags, EventName: record.EventName,
		})
		if record.Body == nil {
			scope.LogRecords[len(scope.LogRecords)-1].Body = AnyValue{}
		}
	}
	return request
}
