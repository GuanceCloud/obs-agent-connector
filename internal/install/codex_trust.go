package install

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/buildinfo"
)

type hookTrustState struct {
	TrustedHash string `json:"trusted_hash"`
}

func TrustCodexHook(codexCommand, cwd string, timeout time.Duration) error {
	if strings.TrimSpace(codexCommand) == "" {
		return errors.New("codex command is required")
	}
	return trustCodexHookProcess(exec.Command(codexCommand, "app-server"), cwd, timeout)
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
					return errors.New("Codex did not discover the obs-agent-connector user hook")
				}
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
					return fmt.Errorf("trust Codex hook failed: %v", value)
				}
				return nil
			}
		}
	}
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
