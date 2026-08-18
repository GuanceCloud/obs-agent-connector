package hook

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/cursor/buildinfo"
	cursorconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/cursor/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/metrics"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/model"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/otlp"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/preview"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/privacy"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/proto"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/state"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/transport"
)

type queuedEvent struct {
	Event   string         `json:"event"`
	Payload map[string]any `json:"payload"`
}

type storedEvent struct {
	Event        string         `json:"event"`
	RecordedNano int64          `json:"recorded_unix_nano"`
	Payload      map[string]any `json:"payload"`
}

type RunOptions struct {
	Config     *cursorconfig.Config
	HTTPClient *http.Client
}

func RunCLI(args []string) int {
	if len(args) == 3 && args[0] == "worker" {
		if err := runWorker(args[1], args[2], RunOptions{}); err != nil {
			return 1
		}
		return 0
	}
	if len(args) != 1 {
		writeEmptyResponse()
		return 0
	}
	payload, err := readPayload(os.Stdin)
	if err != nil {
		writeEmptyResponse()
		return 0
	}
	cfg := cursorconfig.Resolve(cursorconfig.ResolveOptions{Cwd: stringValue(payload, "cwd")})
	if !cfg.Enabled {
		writeEmptyResponse()
		return 0
	}
	payload = payloadForStorage(payload, cfg)
	executable, err := os.Executable()
	if err == nil {
		err = enqueue(executable, args[0], payload, cfg)
	}
	if err != nil {
		appendLog(cfg, "hook failed", map[string]any{"event": args[0], "error": err.Error()})
	}
	writeEmptyResponse()
	return 0
}

func ProcessEvent(event string, payload map[string]any, options RunOptions) error {
	cfg := cursorconfig.Resolve(cursorconfig.ResolveOptions{Cwd: stringValue(payload, "cwd")})
	if options.Config != nil {
		cfg = *options.Config
	}
	if !cfg.Enabled {
		return nil
	}
	event = firstNonEmpty(strings.TrimSpace(event), stringValue(payload, "hook_event_name", "hookEvent", "hookEventName"))
	if event == "" {
		return errors.New("Cursor Hook event is required")
	}
	sessionID := stringValue(payload, "conversation_id", "session_id")
	if sessionID == "" {
		appendLog(cfg, "event skipped", map[string]any{"event": event, "reason": "missing conversation_id"})
		return nil
	}
	journal, err := journalPath(cfg.StateDir, sessionID)
	if err != nil {
		return err
	}
	lock, err := acquireLock(journal + ".lock")
	if err != nil {
		return err
	}
	defer releaseLock(lock)

	current := storedEvent{Event: event, RecordedNano: time.Now().UnixNano(), Payload: payloadForStorage(payload, cfg)}
	if err := appendJournal(journal, current); err != nil {
		return err
	}
	if event != "stop" && event != "sessionEnd" {
		return nil
	}
	events, err := readJournal(journal)
	if err != nil {
		return err
	}
	turn := buildTurn(events, cfg)
	if turn.SessionID == "" {
		return nil
	}
	if err := exportTurn(cfg, turn, options.HTTPClient); err != nil {
		return err
	}
	if err := os.Remove(journal); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	appendLog(cfg, "turn uploaded", map[string]any{"session_id_hash": shortHash(turn.SessionID), "turn_id_hash": shortHash(turn.TurnID)})
	return nil
}

func runWorker(event, queuePath string, options RunOptions) error {
	body, err := os.ReadFile(queuePath)
	if err != nil {
		return err
	}
	var queued queuedEvent
	if err := json.Unmarshal(body, &queued); err != nil {
		return err
	}
	if queued.Event != "" {
		event = queued.Event
	}
	if err := ProcessEvent(event, queued.Payload, options); err != nil {
		return err
	}
	return os.Remove(queuePath)
}

func enqueue(executable, event string, payload map[string]any, cfg cursorconfig.Config) (err error) {
	queueDir := filepath.Join(cfg.StateDir, "queue")
	if err := os.MkdirAll(queueDir, 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(queueDir, "event-*.json")
	if err != nil {
		return err
	}
	queuePath := file.Name()
	remove := true
	defer func() {
		if remove {
			_ = os.Remove(queuePath)
		}
	}()
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if err := json.NewEncoder(file).Encode(queuedEvent{Event: event, Payload: payload}); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer devNull.Close()
	command := exec.Command(executable, "hook", "cursor", "worker", event, queuePath)
	command.Stdin, command.Stdout, command.Stderr = devNull, devNull, devNull
	if err := command.Start(); err != nil {
		return err
	}
	_ = command.Process.Release()
	remove = false
	return nil
}

func buildTurn(events []storedEvent, cfg cursorconfig.Config) model.Turn {
	if len(events) == 0 {
		return model.Turn{}
	}
	for index := len(events) - 1; index > 0; index-- {
		if events[index].Event == "beforeSubmitPrompt" {
			events = events[index:]
			break
		}
	}
	first := events[0]
	last := events[len(events)-1]
	sessionID := ""
	turnID := ""
	agentVersion := ""
	prompt := ""
	output := ""
	thoughtOutput := ""
	requestModel := ""
	composerMode := ""
	workspaceRoots := any(nil)
	var usage model.Usage
	var promptTime int64
	var responseTime int64
	var errorType string
	var reason string
	finalStatus := model.FinalStatusCompleted

	preTools := map[string]storedEvent{}
	toolOrder := []string{}
	toolResults := map[string]storedEvent{}
	for index, event := range events {
		payload := event.Payload
		sessionID = firstNonEmpty(sessionID, stringValue(payload, "conversation_id", "session_id"))
		agentVersion = firstNonEmpty(agentVersion, stringValue(payload, "cursor_version"))
		if roots := payload["workspace_roots"]; roots != nil {
			workspaceRoots = roots
		}
		switch event.Event {
		case "beforeSubmitPrompt":
			prompt = stringValue(payload, "prompt")
			promptTime = event.RecordedNano
			turnID = firstNonEmpty(stringValue(payload, "generation_id"), turnID)
			requestModel = firstNonEmpty(stringValue(payload, "model"), requestModel)
			composerMode = stringValue(payload, "composer_mode")
		case "afterAgentThought":
			requestModel = firstNonEmpty(stringValue(payload, "model"), requestModel)
			if text := stringValue(payload, "text"); text != "" {
				thoughtOutput = text
				responseTime = event.RecordedNano
			}
		case "afterAgentResponse":
			if text := stringValue(payload, "text"); text != "" {
				output = text
			}
			responseTime = event.RecordedNano
			requestModel = firstNonEmpty(stringValue(payload, "model"), requestModel)
			usage = usageFrom(payload, usage)
		case "preToolUse":
			id := toolID(payload, index)
			if _, exists := preTools[id]; !exists {
				toolOrder = append(toolOrder, id)
			}
			preTools[id] = event
		case "postToolUse", "postToolUseFailure":
			id := toolID(payload, index)
			if _, exists := preTools[id]; !exists {
				preTools[id] = event
				toolOrder = append(toolOrder, id)
			}
			toolResults[id] = event
		case "stop", "sessionEnd":
			usage = usageFrom(payload, usage)
			switch strings.ToLower(stringValue(payload, "status")) {
			case "aborted", "cancelled", "canceled", "interrupted":
				finalStatus = model.FinalStatusCancelled
			case "error", "failed":
				errorType = "cursor_agent_error"
				reason = firstNonEmpty(stringValue(payload, "error_message"), stringValue(payload, "status"))
			}
		}
	}
	if promptTime == 0 {
		promptTime = first.RecordedNano
	}
	endTime := last.RecordedNano
	if responseTime == 0 {
		responseTime = endTime
	}
	if output == "" {
		output = thoughtOutput
	}
	if turnID == "" {
		turnID = derivedID(sessionID, fmt.Sprintf("%d", promptTime))
	}
	turn := model.Turn{
		SessionID:     sessionID,
		TurnID:        turnID,
		AgentRuntime:  "cursor",
		AgentName:     "Cursor",
		AgentVersion:  agentVersion,
		StartUnixNano: promptTime,
		EndUnixNano:   endTime,
		FinalStatus:   finalStatus,
		InputLength:   len([]rune(prompt)),
		OutputLength:  len([]rune(output)),
		Usage:         usage,
		Resource:      copyMap(cfg.ResourceAttributes),
		ErrorType:     errorType,
		Reason:        reason,
		ExtraAttributes: map[string]any{
			"agent.cursor.composer_mode":   composerMode,
			"agent.cursor.workspace_roots": workspaceRoots,
		},
	}
	if cfg.CaptureContent != "none" {
		turn.InputMessages = textMessage("user", prompt, cfg.MaxChars)
		turn.OutputMessages = textMessage("assistant", output, cfg.MaxChars)
		turn.InputPreview = preview.Text(prompt, cfg.MaxChars)
		turn.OutputPreview = preview.Text(output, cfg.MaxChars)
	}
	if prompt != "" || output != "" || usage.InputTokens > 0 || usage.OutputTokens > 0 {
		call := model.LLMCall{
			CallID:        derivedID(turnID, "llm"),
			StartUnixNano: promptTime,
			EndUnixNano:   responseTime,
			Provider:      providerForModel(requestModel),
			RequestModel:  requestModel,
			ResponseModel: requestModel,
			FinishReasons: []string{"stop"},
			Usage:         usage,
			Status:        statusValue(errorType),
			ErrorType:     errorType,
			Reason:        reason,
		}
		if cfg.CaptureContent != "none" {
			call.InputMessages = turn.InputMessages
			call.OutputMessages = turn.OutputMessages
			call.InputPreview = turn.InputPreview
			call.OutputPreview = turn.OutputPreview
			call.OutputKind = "text"
		}
		turn.LLMCalls = append(turn.LLMCalls, call)
	}
	for _, id := range toolOrder {
		startEvent := preTools[id]
		resultEvent, hasResult := toolResults[id]
		end := startEvent.RecordedNano + 1
		if hasResult {
			end = resultEvent.RecordedNano
		}
		toolPayload := startEvent.Payload
		name := firstNonEmpty(stringValue(toolPayload, "tool_name"), stringValue(resultEvent.Payload, "tool_name"), "unknown")
		tool := model.ToolCall{
			CallID:        id,
			Name:          name,
			StartUnixNano: startEvent.RecordedNano,
			EndUnixNano:   end,
			Status:        "info",
			ResultStatus:  "completed",
			ExtraAttributes: map[string]any{
				"agent.cursor.hook_event_name": resultEvent.Event,
			},
		}
		if !hasResult {
			tool.Status = "unset"
			tool.ResultStatus = "unset"
		}
		if resultEvent.Event == "postToolUseFailure" {
			tool.Status = "error"
			tool.ResultStatus = "failed"
			tool.ErrorType = firstNonEmpty(stringValue(resultEvent.Payload, "failure_type"), "tool_use_failure")
			tool.Reason = stringValue(resultEvent.Payload, "error_message")
		}
		if cfg.CaptureContent != "none" {
			tool.Arguments = privacy.Sanitize(toolPayload["tool_input"], cfg.MaxChars)
			tool.Result = privacy.Sanitize(resultEvent.Payload["tool_output"], cfg.MaxChars)
			tool.Command = commandValue(toolPayload["tool_input"], cfg.MaxChars)
			tool.InputPreview = preview.Text(toolPayload["tool_input"], cfg.MaxChars)
			tool.OutputPreview = preview.Text(firstNonNil(resultEvent.Payload["tool_output"], resultEvent.Payload["error_message"]), cfg.MaxChars)
		}
		turn.ToolCalls = append(turn.ToolCalls, tool)
	}
	return turn
}

func exportTurn(cfg cursorconfig.Config, turn model.Turn, httpClient *http.Client) error {
	spans := (semantic.Builder{ScopeName: "gtrace-cursor-collector", ScopeVersion: buildinfo.Version}).Build(turn)
	if len(spans) == 0 {
		return nil
	}
	body, _ := json.Marshal(turn)
	fingerprint := sha256.Sum256(body)
	manager := state.Manager{Root: filepath.Join(cfg.StateDir, "uploads"), StaleAfter: 10 * time.Minute}
	claim, err := manager.Claim(turn.SessionID, turn.TurnID, hex.EncodeToString(fingerprint[:]))
	if errors.Is(err, state.ErrAlreadyCompleted) || (err == nil && claim == nil) {
		return nil
	}
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			_ = claim.Release()
		}
	}()
	uploader := transport.Client{Config: cfg.Transport, HTTPClient: httpClient}
	tracePayload := proto.EncodeExportTraceServiceRequest(otlp.SpansToProtoRequest(spans))
	if !claim.SignalWasUploaded("traces") {
		result, err := uploader.Upload("traces", tracePayload)
		if err != nil {
			return err
		}
		if err := claim.MarkSignalUploaded("traces", map[string]any{"status": result.StatusCode, "bytes": len(tracePayload)}); err != nil {
			return err
		}
	}
	required := []string{"traces"}
	builtMetrics := metrics.Build(spans)
	if len(builtMetrics) > 0 && (cfg.Transport.MetricsURL != "" || cfg.Transport.Endpoint != "") {
		required = append(required, "metrics")
		if !claim.SignalWasUploaded("metrics") {
			metricPayload := proto.EncodeExportMetricsServiceRequest(otlp.MetricsToProtoRequest(builtMetrics))
			result, err := uploader.Upload("metrics", metricPayload)
			if err != nil {
				return err
			}
			if err := claim.MarkSignalUploaded("metrics", map[string]any{"status": result.StatusCode, "bytes": len(metricPayload)}); err != nil {
				return err
			}
		}
	}
	if err := claim.Complete(required...); err != nil {
		return err
	}
	completed = true
	return nil
}

func journalPath(stateDir, sessionID string) (string, error) {
	dir := filepath.Join(stateDir, "journal")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return filepath.Join(dir, derivedID("cursor", sessionID)+".jsonl"), nil
}

func appendJournal(path string, event storedEvent) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewEncoder(file).Encode(event)
}

func readJournal(path string) ([]storedEvent, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var events []storedEvent
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event storedEvent
		if json.Unmarshal(scanner.Bytes(), &event) == nil {
			events = append(events, event)
		}
	}
	return events, scanner.Err()
}

func acquireLock(path string) (*os.File, error) {
	deadline := time.Now().Add(2 * time.Second)
	for {
		file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err == nil {
			return file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return nil, err
		}
		if info, statErr := os.Stat(path); statErr == nil && time.Since(info.ModTime()) > 15*time.Second {
			_ = os.Remove(path)
			continue
		}
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("timed out waiting for Cursor journal lock")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func releaseLock(file *os.File) {
	if file == nil {
		return
	}
	path := file.Name()
	_ = file.Close()
	_ = os.Remove(path)
}

func readPayload(reader io.Reader) (map[string]any, error) {
	var payload map[string]any
	if err := json.NewDecoder(io.LimitReader(reader, 4*1024*1024)).Decode(&payload); err != nil {
		return nil, err
	}
	return payload, nil
}

func writeEmptyResponse() {
	_, _ = fmt.Fprintln(os.Stdout, "{}")
}

func appendLog(cfg cursorconfig.Config, message string, fields map[string]any) {
	if cfg.LogFile == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o700); err != nil {
		return
	}
	file, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_ = json.NewEncoder(file).Encode(map[string]any{"ts": time.Now().UTC().Format(time.RFC3339Nano), "message": message, "fields": fields})
}

func usageFrom(payload map[string]any, fallback model.Usage) model.Usage {
	set := func(current int64, keys ...string) int64 {
		for _, key := range keys {
			if value, ok := numberValue(payload[key]); ok {
				return value
			}
		}
		return current
	}
	fallback.InputTokens = set(fallback.InputTokens, "input_tokens")
	fallback.OutputTokens = set(fallback.OutputTokens, "output_tokens")
	fallback.CacheReadTokens = set(fallback.CacheReadTokens, "cache_read_tokens")
	fallback.CacheCreateTokens = set(fallback.CacheCreateTokens, "cache_write_tokens")
	return fallback
}

func payloadForStorage(payload map[string]any, cfg cursorconfig.Config) map[string]any {
	allowed := map[string]struct{}{
		"conversation_id": {}, "session_id": {}, "generation_id": {}, "model": {},
		"status": {}, "input_tokens": {}, "output_tokens": {}, "cache_read_tokens": {},
		"cache_write_tokens": {}, "cursor_version": {}, "composer_mode": {}, "tool_name": {},
		"tool_use_id": {}, "failure_type": {}, "duration_ms": {}, "duration": {},
		"loop_count": {}, "cwd": {}, "hook_event_name": {}, "hookEvent": {}, "hookEventName": {},
		"subagent_id": {}, "subagent_type": {}, "parent_conversation_id": {}, "tool_call_id": {},
		"subagent_model": {}, "is_parallel_worker": {}, "is_interrupt": {}, "message_count": {},
		"tool_call_count": {},
	}
	out := make(map[string]any, len(allowed)+8)
	for key := range allowed {
		if value, exists := payload[key]; exists {
			out[key] = value
		}
	}
	if cfg.CaptureContent != "none" {
		for _, key := range []string{"prompt", "text", "tool_input", "tool_output", "error_message", "attachments", "task", "description", "workspace_roots"} {
			if value, exists := payload[key]; exists {
				out[key] = privacy.Sanitize(value, cfg.MaxChars)
			}
		}
	}
	return out
}

func numberValue(value any) (int64, bool) {
	switch current := value.(type) {
	case float64:
		return int64(current), true
	case int:
		return int64(current), true
	case int64:
		return current, true
	case json.Number:
		parsed, err := current.Int64()
		return parsed, err == nil
	}
	return 0, false
}

func toolID(payload map[string]any, index int) string {
	id := stringValue(payload, "tool_use_id")
	if line, _, ok := strings.Cut(id, "\n"); ok {
		id = line
	}
	if id != "" {
		return id
	}
	return derivedID(stringValue(payload, "tool_name"), fmt.Sprintf("%d", index))
}

func textMessage(role, text string, maxChars int) any {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return []any{map[string]any{"role": role, "parts": []any{map[string]any{"type": "text", "content": privacy.Text(text, maxChars)}}}}
}

func commandValue(value any, maxChars int) string {
	if object, ok := value.(map[string]any); ok {
		return privacy.Text(firstNonNil(object["command"], object["cmd"]), maxChars)
	}
	return ""
}

func providerForModel(value string) string {
	value = strings.ToLower(value)
	switch {
	case strings.Contains(value, "claude"):
		return "anthropic"
	case strings.Contains(value, "gpt"), strings.Contains(value, "o3"), strings.Contains(value, "o4"):
		return "openai"
	case strings.Contains(value, "gemini"):
		return "google"
	case strings.Contains(value, "grok"):
		return "xai"
	default:
		return "cursor"
	}
}

func statusValue(errorType string) string {
	if errorType != "" {
		return "error"
	}
	return "info"
}

func stringValue(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := value[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func derivedID(values ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return hex.EncodeToString(sum[:16])
}

func shortHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:6])
}

func copyMap(value map[string]any) map[string]any {
	out := map[string]any{}
	for key, item := range value {
		out[key] = item
	}
	return out
}
