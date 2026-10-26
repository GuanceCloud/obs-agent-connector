package sandbox

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/core/journal"
)

// NativeHook contains only the Hook fields needed for causal association.
// ToolInput is inspected in memory; only a command digest is persisted.
type NativeHook struct {
	Event      string          `json:"hook_event_name"`
	SessionID  string          `json:"session_id"`
	TurnID     string          `json:"turn_id"`
	ToolCallID string          `json:"tool_use_id"`
	ToolName   string          `json:"tool_name"`
	ToolInput  json.RawMessage `json:"tool_input"`
}

type nativeTool struct {
	SessionID, TurnID, CallID, CommandHash string
	Started, Ended                         time.Time
}

type nativeInstance struct {
	ID, ExecutorID, ThreadID, CommandHash string
	PID                                   int
	StartTicks                            string
	KernelKey                             string
	FirstObserved, LastSnapshot           time.Time
	Terminal                              bool
	Lost                                  bool
	Relations                             map[string]bool
}

type nativeState struct {
	Tools     []nativeTool
	Instances map[string]nativeInstance
	Pending   []Event
}

func nativeFile(store Store) journal.File[nativeState] {
	return journal.File[nativeState]{Root: store.Root + "/native", MaxBytes: 4 << 20}
}

func updateNative(store Store, update func(*nativeState) error) error {
	deadline := time.Now().Add(100 * time.Millisecond)
	for {
		err := nativeFile(store).Update(update)
		if !errors.Is(err, journal.ErrBusy) || time.Now().After(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// RecordNativeHook persists identifiers and exact command hashes, never commands
// or tool results. Ambiguous simultaneous identical commands remain unlinked.
func RecordNativeHook(store Store, input NativeHook) error {
	if input.Event == "Stop" || input.Event == "Interrupt" {
		if input.SessionID == "" || input.TurnID == "" {
			return nil
		}
		now := store.now()
		return updateNative(store, func(s *nativeState) error {
			for i := range s.Tools {
				tool := &s.Tools[i]
				if tool.SessionID == input.SessionID && tool.TurnID == input.TurnID && tool.Ended.IsZero() {
					tool.Ended = now
				}
			}
			return nil
		})
	}
	if input.Event != "PreToolUse" && input.Event != "PostToolUse" && input.Event != "PostToolUseFailure" {
		return nil
	}
	for _, value := range []string{input.SessionID, input.TurnID, input.ToolCallID} {
		if value == "" || len(value) > 256 || strings.ContainsAny(value, "\r\n\x00") {
			return fmt.Errorf("native Hook requires bounded session, turn and tool identities")
		}
	}
	commandHash := nativeCommandHash(input.ToolName, input.ToolInput)
	if commandHash == "" {
		return nil
	}
	now := store.now()
	return updateNative(store, func(s *nativeState) error {
		kept := s.Tools[:0]
		for _, tool := range s.Tools {
			if now.Sub(tool.Started) < 24*time.Hour && (tool.Ended.IsZero() || now.Sub(tool.Ended) < time.Minute) {
				kept = append(kept, tool)
			}
		}
		s.Tools = kept
		for i := range s.Tools {
			t := &s.Tools[i]
			if t.SessionID == input.SessionID && t.TurnID == input.TurnID && t.CallID == input.ToolCallID {
				if input.Event != "PreToolUse" {
					t.Ended = now
				}
				return nil
			}
		}
		if input.Event != "PreToolUse" {
			return nil
		}
		if len(s.Tools) >= 1024 {
			return ErrFull
		}
		s.Tools = append(s.Tools, nativeTool{SessionID: input.SessionID, TurnID: input.TurnID, CallID: input.ToolCallID, CommandHash: commandHash, Started: now})
		return nil
	})
}

func nativeCommandHash(tool string, raw json.RawMessage) string {
	if len(raw) > 64<<10 {
		return ""
	}
	switch strings.ToLower(tool) {
	case "bash", "shell", "shell_command", "exec_command", "functions.exec_command", "functions.shell", "functions.shell_command":
	default:
		return ""
	}
	var input struct {
		Command json.RawMessage `json:"command"`
		Cmd     string          `json:"cmd"`
	}
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	var command string
	if len(input.Command) > 0 && string(input.Command) != "null" && json.Unmarshal(input.Command, &command) == nil {
		return digest("shell", command)
	}
	var argv []string
	if json.Unmarshal(input.Command, &argv) == nil && len(argv) > 0 {
		return argvHash(argv)
	}
	if input.Cmd != "" {
		return digest("shell", input.Cmd)
	}
	return ""
}

func argvHash(argv []string) string {
	if len(argv) > 0 {
		parts := strings.Split(argv[0], "/")
		switch parts[len(parts)-1] {
		case "bash", "sh", "zsh", "dash", "ksh":
			for i, value := range argv {
				if (value == "-c" || value == "-lc") && i+2 == len(argv) {
					return digest("shell", argv[i+1])
				}
			}
		}
	}
	encoded, _ := json.Marshal(argv)
	return digest("argv", string(encoded))
}

func matchingTool(tools []nativeTool, instance nativeInstance) *nativeTool {
	var match *nativeTool
	for _, tool := range tools {
		if tool.SessionID != instance.ThreadID || tool.CommandHash != instance.CommandHash || instance.FirstObserved.Before(tool.Started) || (!tool.Ended.IsZero() && instance.FirstObserved.After(tool.Ended)) {
			continue
		}
		if match != nil {
			return nil
		}
		copy := tool
		match = &copy
	}
	return match
}

// reconcileNative is shared by the observer and deterministic evidence tests.
// A missing inaccessible PID is observation loss; only confirmed death of the
// previously observed namespace-init establishes a terminal snapshot. Its exact
// creation/destruction clocks remain unknown, so neither date is manufactured.
func reconcileNative(store Store, observed []nativeInstance, alive func(nativeInstance) (bool, error)) error {
	if err := drainNative(store); err != nil {
		return err
	}
	now := store.now()
	err := updateNative(store, func(s *nativeState) error {
		if s.Instances == nil {
			s.Instances = map[string]nativeInstance{}
		}
		for id, instance := range s.Instances {
			if instance.Terminal && now.Sub(instance.LastSnapshot) > retention {
				delete(s.Instances, id)
			}
		}
		for _, instance := range observed {
			if _, exists := s.Instances[instance.ID]; !exists {
				if len(s.Instances) >= 1024 {
					oldest := ""
					for id, candidate := range s.Instances {
						if candidate.Terminal && (oldest == "" || candidate.LastSnapshot.Before(s.Instances[oldest].LastSnapshot)) {
							oldest = id
						}
					}
					if oldest == "" {
						return ErrFull
					}
					delete(s.Instances, oldest)
				}
				instance.FirstObserved, instance.Relations = now, map[string]bool{}
				s.Instances[instance.ID] = instance
			}
		}
		for id, instance := range s.Instances {
			if instance.Relations == nil {
				instance.Relations = map[string]bool{}
			}
			if instance.Terminal {
				if now.Sub(instance.LastSnapshot) > retention {
					delete(s.Instances, id)
				}
				continue
			}
			live, err := alive(instance)
			event := Event{SchemaVersion: 1, SandboxID: id, ExecutorID: instance.ExecutorID, IdentitySource: "observer", RuntimeSource: "codex.native.linux.proc", Name: "codex-linux-" + id[:min(12, len(id))], Type: "linux-pid-namespace", Timestamp: now, ExecutionID: id, ObservedSessionID: instance.ThreadID}
			if err != nil {
				// A temporary permission/read failure is not destruction.
				if !instance.Lost {
					event.EventID, event.EventName, event.State = digest(id, "lost", now.Format(time.RFC3339Nano)), "obs.sandbox.observation.lost", "unknown"
					s.Pending = append(s.Pending, event)
					instance.Lost = true
					s.Instances[id] = instance
				}
				continue
			}
			if !live {
				event.EventID, event.EventName, event.State = digest(id, "init-ended"), "obs.sandbox.snapshot", "destroyed"
				s.Pending = append(s.Pending, event)
				instance.Terminal, instance.LastSnapshot = true, now
			} else {
				if instance.LastSnapshot.IsZero() || instance.Lost || now.Sub(instance.LastSnapshot) >= time.Minute {
					event.EventName, event.State = "obs.sandbox.snapshot", "running"
					if instance.LastSnapshot.IsZero() {
						event.EventName = "obs.sandbox.discovered"
					}
					event.EventID = digest(id, event.EventName, now.Format(time.RFC3339Nano))
					s.Pending = append(s.Pending, event)
					instance.LastSnapshot, instance.Lost = now, false
				}
				if tool := matchingTool(s.Tools, instance); tool != nil {
					key := digest(tool.SessionID, tool.TurnID, tool.CallID)
					if !instance.Relations[key] {
						event.EventID, event.EventName, event.State = digest(id, key), "obs.sandbox.trace.bound", ""
						event.SessionID, event.TurnID, event.ToolCallID = tool.SessionID, tool.TurnID, tool.CallID
						event.AssociationMethod = "native_thread_command_digest"
						s.Pending = append(s.Pending, event)
						instance.Relations[key] = true
					}
				}
			}
			s.Instances[id] = instance
		}
		if len(s.Pending) > 1024 {
			return ErrFull
		}
		return nil
	})
	if err != nil {
		return err
	}
	return drainNative(store)
}

// A durable producer outbox closes the crash window between two journals.
func drainNative(store Store) error {
	for i := 0; i < 32; i++ {
		var next *Event
		if err := nativeFile(store).View(func(s *nativeState) error {
			if len(s.Pending) > 0 {
				copy := s.Pending[0]
				next = &copy
			}
			return nil
		}); err != nil {
			return err
		}
		if next == nil {
			return nil
		}
		if err := store.Apply(*next); err != nil {
			return err
		}
		if err := updateNative(store, func(s *nativeState) error {
			if len(s.Pending) > 0 && s.Pending[0].EventID == next.EventID {
				s.Pending = s.Pending[1:]
			}
			return nil
		}); err != nil {
			return err
		}
	}
	return nil
}
