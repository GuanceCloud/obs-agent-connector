package install

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/buildinfo"
)

type hookTrustState struct {
	TrustedHash string `json:"trusted_hash"`
}

type legacyCodexPriorityTierError struct {
	messages []string
}

func (e *legacyCodexPriorityTierError) Error() string {
	return "Codex failed to load hooks with the legacy service tier schema: " + strings.Join(e.messages, "; ")
}

type legacyCodexConfigWriteError struct {
	entries map[string]hookTrustState
	cause   string
}

func (e *legacyCodexConfigWriteError) Error() string {
	return "legacy Codex CLI rejected the hook trust config write: " + e.cause
}

func TrustCodexHook(codexCommand, cwd string, timeout time.Duration) error {
	if strings.TrimSpace(codexCommand) == "" {
		return errors.New("codex command is required")
	}
	return trustCodexHookWithRunner(codexCommand, cwd, timeout, trustCodexHookProcess)
}

func trustCodexHookWithRunner(
	codexCommand string,
	cwd string,
	timeout time.Duration,
	run func(*exec.Cmd, string, time.Duration) error,
) error {
	err := run(exec.Command(codexCommand, "app-server"), cwd, timeout)
	var compatibilityErr *legacyCodexPriorityTierError
	if !errors.As(err, &compatibilityErr) {
		return err
	}

	retryErr := run(exec.Command(codexCommand, "app-server", "-c", `service_tier="fast"`), cwd, timeout)
	if retryErr == nil {
		return nil
	}
	var configWriteErr *legacyCodexConfigWriteError
	if !errors.As(retryErr, &configWriteErr) {
		return fmt.Errorf("trust Codex hook with legacy service tier compatibility: %w", retryErr)
	}
	if cwd == "" {
		cwd = "."
	}
	if writeErr := writeCodexTrustState(filepath.Join(cwd, ".codex", "config.toml"), configWriteErr.entries); writeErr != nil {
		return fmt.Errorf("write Codex hook trust state with legacy service tier compatibility: %w", writeErr)
	}
	return nil
}

func trustCodexHookProcess(cmd *exec.Cmd, cwd string, timeout time.Duration) error {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	if cwd == "" {
		cwd = "."
	}
	cmd.Dir = cwd
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	waited := false
	defer func() {
		_ = stdin.Close()
		if !waited && cmd.Process != nil {
			_ = cmd.Process.Kill()
			<-done
		}
	}()

	send := func(value any) error {
		body, err := json.Marshal(value)
		if err != nil {
			return err
		}
		body = append(body, '\n')
		_, err = stdin.Write(body)
		return err
	}
	if err := send(map[string]any{
		"id":     1,
		"method": "initialize",
		"params": map[string]any{
			"clientInfo":   map[string]any{"name": "obs-agent-connector", "version": buildinfo.Version},
			"capabilities": map[string]any{},
		},
	}); err != nil {
		return err
	}

	messages := make(chan map[string]any, 16)
	var pendingEntries map[string]hookTrustState
	readDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), 8*1024*1024)
		for scanner.Scan() {
			if len(bytes.TrimSpace(scanner.Bytes())) == 0 {
				continue
			}
			var message map[string]any
			if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
				readDone <- err
				return
			}
			select {
			case messages <- message:
			case <-ctx.Done():
				return
			}
		}
		readDone <- scanner.Err()
	}()

	readResult := (<-chan error)(readDone)
	for {
		select {
		case err := <-done:
			waited = true
			if err == nil {
				return errors.New("codex app-server exited before the hook was trusted")
			}
			return fmt.Errorf("codex app-server exited: %w: %s", err, strings.TrimSpace(stderr.String()))
		case <-deadline.C:
			return errors.New("timed out while trusting Codex hook")
		case err := <-readResult:
			readResult = nil
			if err != nil {
				return fmt.Errorf("read Codex app-server response: %w", err)
			}
		case message := <-messages:
			switch numberValue(message["id"]) {
			case 1:
				if err := send(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
					return err
				}
				if err := send(map[string]any{
					"id": 2, "method": "hooks/list", "params": map[string]any{"cwds": []string{cwd}},
				}); err != nil {
					return err
				}
			case 2:
				result, _ := message["result"].(map[string]any)
				entries := codexTrustEntries(result)
				if len(entries) == 0 {
					hookErrors := codexHookListErrors(result)
					if legacyPriorityTierError(hookErrors) {
						return &legacyCodexPriorityTierError{messages: hookErrors}
					}
					if len(hookErrors) > 0 {
						return fmt.Errorf("Codex failed to load hooks: %s", strings.Join(hookErrors, "; "))
					}
					return errors.New("Codex did not discover the obs-agent-connector user hook")
				}
				pendingEntries = entries
				serialized := map[string]any{}
				for key, state := range entries {
					serialized[key] = map[string]any{"trusted_hash": state.TrustedHash}
				}
				if err := send(map[string]any{
					"id":     3,
					"method": "config/batchWrite",
					"params": map[string]any{
						"edits": []any{
							map[string]any{
								"keyPath":       "hooks.state",
								"value":         serialized,
								"mergeStrategy": "upsert",
							},
						},
						"filePath":         nil,
						"expectedVersion":  nil,
						"reloadUserConfig": true,
					},
				}); err != nil {
					return err
				}
			case 3:
				if value, ok := message["error"]; ok && value != nil {
					cause := fmt.Sprint(value)
					if len(pendingEntries) > 0 && legacyPriorityTierError([]string{cause}) {
						return &legacyCodexConfigWriteError{entries: pendingEntries, cause: cause}
					}
					return fmt.Errorf("trust Codex hook failed: %v", value)
				}
				return nil
			}
		}
	}
}

func writeCodexTrustState(path string, entries map[string]hookTrustState) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}

	managedKeys := make(map[string]struct{}, len(entries))
	for key := range entries {
		managedKeys[key] = struct{}{}
	}
	lines := strings.SplitAfter(string(body), "\n")
	next := make([]string, 0, len(lines))
	removing := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") {
			removing = false
			if key, ok := codexTrustSectionKey(trimmed); ok {
				_, removing = managedKeys[key]
			}
		}
		if !removing {
			next = append(next, line)
		}
	}

	newline := "\n"
	if strings.Contains(string(body), "\r\n") {
		newline = "\r\n"
	}
	current := strings.TrimRight(strings.Join(next, ""), "\r\n")
	keys := make([]string, 0, len(entries))
	for key := range entries {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var out strings.Builder
	if current != "" {
		out.WriteString(current)
		out.WriteString(newline)
		out.WriteString(newline)
	}
	for index, key := range keys {
		if index > 0 {
			out.WriteString(newline)
		}
		out.WriteString("[hooks.state.")
		out.WriteString(strconv.Quote(key))
		out.WriteString("]")
		out.WriteString(newline)
		out.WriteString("trusted_hash = ")
		out.WriteString(strconv.Quote(entries[key].TrustedHash))
		out.WriteString(newline)
	}
	return writeTextAtomic(path, []byte(out.String()), info.Mode().Perm())
}

func codexHookListErrors(response map[string]any) []string {
	messages := []string{}
	data, _ := response["data"].([]any)
	for _, entry := range data {
		entryMap, _ := entry.(map[string]any)
		items, _ := entryMap["errors"].([]any)
		for _, item := range items {
			itemMap, _ := item.(map[string]any)
			message := strings.TrimSpace(fmt.Sprint(itemMap["message"]))
			if message != "" && message != "<nil>" {
				messages = append(messages, message)
			}
		}
	}
	return messages
}

func legacyPriorityTierError(messages []string) bool {
	for _, message := range messages {
		normalized := strings.ToLower(message)
		if strings.Contains(normalized, "unknown variant `priority`") &&
			strings.Contains(normalized, "`fast`") &&
			strings.Contains(normalized, "`flex`") {
			return true
		}
	}
	return false
}

func codexTrustEntries(response map[string]any) map[string]hookTrustState {
	result := map[string]hookTrustState{}
	data, _ := response["data"].([]any)
	for _, entry := range data {
		entryMap, _ := entry.(map[string]any)
		hooks, _ := entryMap["hooks"].([]any)
		for _, hook := range hooks {
			hookMap, _ := hook.(map[string]any)
			source, _ := hookMap["source"].(string)
			command, _ := hookMap["command"].(string)
			key, _ := hookMap["key"].(string)
			currentHash, _ := hookMap["currentHash"].(string)
			if source != "user" || !isManagedCodexCommand(command) || key == "" || currentHash == "" {
				continue
			}
			result[key] = hookTrustState{TrustedHash: currentHash}
		}
	}
	return result
}

func isManagedCodexCommand(command string) bool {
	normalized := strings.ToLower(strings.ReplaceAll(strings.Trim(strings.TrimSpace(command), `"'`), `\`, "/"))
	return strings.Contains(normalized, "obs-agent-connector") ||
		strings.Contains(normalized, "agent-telemetry") ||
		strings.Contains(normalized, "gtrace-agent") ||
		strings.Contains(normalized, "codex-hook") ||
		strings.Contains(normalized, "codex-otel-plugin")
}

func numberValue(value any) int {
	switch current := value.(type) {
	case int:
		return current
	case int64:
		return int(current)
	case float64:
		return int(current)
	default:
		return 0
	}
}
