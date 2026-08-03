package install

import (
	"bufio"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInstallCodexIsIdempotentAndPreservesConfiguration(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	source := filepath.Join(root, "source", "agent-telemetry")
	destination := filepath.Join(home, ".local", "bin", "agent-telemetry")
	hooksFile := filepath.Join(home, ".codex", "hooks.json")
	configFile := filepath.Join(home, ".codex", "gtrace.json")
	if err := os.MkdirAll(filepath.Dir(source), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(hooksFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeTestJSON(t, hooksFile, map[string]any{
		"hooks": map[string]any{
			"Stop": []any{
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "echo keep"}}},
				map[string]any{"hooks": []any{map[string]any{"type": "command", "command": "node codex-hook-wrapper.js"}}},
			},
		},
	})
	writeTestJSON(t, configFile, map[string]any{
		"enabled":  false,
		"endpoint": "https://old.example",
		"headers": map[string]any{
			"X-Custom": "keep",
		},
		"resourceAttributes": map[string]any{
			"app_id": "keep",
		},
		"unknown": "keep",
	})

	options := CodexOptions{
		Home:                  home,
		SourceExecutable:      source,
		DestinationExecutable: destination,
		HooksFile:             hooksFile,
		ConfigFile:            configFile,
		Endpoint:              "https://new.example/",
		InstallType:           "gtrace",
		XToken:                "secret-placeholder",
		Headers:               []string{"X-Extra=value"},
		ResourceAttributes:    []string{"env=test"},
		SkipTrust:             true,
	}
	for range 2 {
		result, err := InstallCodex(options)
		if err != nil {
			t.Fatal(err)
		}
		if !result.Configured || !result.TrustSkipped {
			t.Fatalf("unexpected result: %#v", result)
		}
	}

	var hooks map[string]any
	readTestJSON(t, hooksFile, &hooks)
	stop := hooks["hooks"].(map[string]any)["Stop"].([]any)
	if len(stop) != 2 {
		t.Fatalf("hook install is not idempotent: %#v", stop)
	}
	managed := stop[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if managed["command"] != destination+" hook codex" {
		t.Fatalf("hook command = %#v", managed["command"])
	}
	if _, exists := managed["args"]; exists {
		t.Fatalf("Codex hook must use its proven command-string schema: %#v", managed)
	}

	var cfg map[string]any
	readTestJSON(t, configFile, &cfg)
	if cfg["enabled"] != false || cfg["endpoint"] != "https://new.example" || cfg["unknown"] != "keep" {
		t.Fatalf("configuration was not preserved: %#v", cfg)
	}
	headers := cfg["headers"].(map[string]any)
	if headers["X-Custom"] != "keep" || headers["X-Extra"] != "value" ||
		headers["X-Token"] != "secret-placeholder" || headers["To-Headless"] != "true" {
		t.Fatalf("unexpected headers: %#v", headers)
	}
	resource := cfg["resourceAttributes"].(map[string]any)
	if resource["app_id"] != "keep" || resource["env"] != "test" {
		t.Fatalf("unexpected resource attributes: %#v", resource)
	}
}

func TestInstallCodexNoConfigAndInvalidHooksSafety(t *testing.T) {
	root := t.TempDir()
	source := filepath.Join(root, "agent-telemetry")
	destination := filepath.Join(root, "installed", "agent-telemetry")
	hooksFile := filepath.Join(root, "hooks.json")
	configFile := filepath.Join(root, "gtrace.json")
	if err := os.WriteFile(source, []byte("binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(configFile, []byte("{\"enabled\":false}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := InstallCodex(CodexOptions{
		SourceExecutable:      source,
		DestinationExecutable: destination,
		HooksFile:             hooksFile,
		ConfigFile:            configFile,
		NoConfig:              true,
		SkipTrust:             true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Configured {
		t.Fatal("--no-config modified config")
	}
	body, err := os.ReadFile(configFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "{\"enabled\":false}\n" {
		t.Fatalf("config changed: %q", body)
	}

	invalidHooks := filepath.Join(root, "invalid-hooks.json")
	invalidDestination := filepath.Join(root, "invalid-install", "agent-telemetry")
	if err := os.WriteFile(invalidHooks, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = InstallCodex(CodexOptions{
		SourceExecutable:      source,
		DestinationExecutable: invalidDestination,
		HooksFile:             invalidHooks,
		ConfigFile:            configFile,
		SkipTrust:             true,
	})
	if err == nil {
		t.Fatal("expected invalid hooks error")
	}
	if _, statErr := os.Stat(invalidDestination); !os.IsNotExist(statErr) {
		t.Fatalf("binary copied before hooks validation: %v", statErr)
	}
}

func TestParseCodexInstallArgs(t *testing.T) {
	options, err := ParseCodexInstallArgs([]string{
		"--type", "otlp",
		"--endpoint", "http://127.0.0.1:4318",
		"--header", "Authorization=placeholder",
		"--tag", "env=test",
		"--capture-content", "none",
		"--max-chars", "4096",
		"--enable",
		"--skip-trust",
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.InstallType != "otlp" || options.Endpoint == "" || options.Enabled == nil || !*options.Enabled ||
		len(options.Headers) != 1 || len(options.ResourceAttributes) != 1 || !options.SkipTrust ||
		options.CaptureContent != "none" || options.MaxChars != 4096 {
		t.Fatalf("unexpected parsed options: %#v", options)
	}
	if _, err := ParseCodexInstallArgs([]string{"--enable", "--disable"}); err == nil {
		t.Fatal("expected conflicting enable flags error")
	}
	usage := CodexInstallUsage()
	for _, option := range []string{"--endpoint", "--type", "--capture-content", "--no-config", "--skip-trust"} {
		if !strings.Contains(usage, option) {
			t.Fatalf("install usage is missing %s", option)
		}
	}
}

func TestTrustCodexHookProcessCompletesHandshake(t *testing.T) {
	cmd := codexTrustHelperCommand(t, "success")
	if err := trustCodexHookProcess(cmd, t.TempDir(), 2*time.Second); err != nil {
		t.Fatal(err)
	}
}

func TestTrustCodexHookProcessHonorsTimeoutAndExit(t *testing.T) {
	err := trustCodexHookProcess(codexTrustHelperCommand(t, "exit"), t.TempDir(), 2*time.Second)
	if err == nil || !strings.Contains(err.Error(), "app-server exited") {
		t.Fatalf("unexpected exit error: %v", err)
	}
	err = trustCodexHookProcess(codexTrustHelperCommand(t, "hang"), t.TempDir(), 100*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("unexpected timeout error: %v", err)
	}
}

func codexTrustHelperCommand(t *testing.T, scenario string) *exec.Cmd {
	t.Helper()
	command := exec.Command(os.Args[0], "-test.run=^TestCodexTrustHelperProcess$")
	command.Env = append(os.Environ(), "GO_WANT_CODEX_TRUST_HELPER=1", "CODEX_TRUST_SCENARIO="+scenario)
	return command
}

func TestCodexTrustHelperProcess(t *testing.T) {
	if os.Getenv("GO_WANT_CODEX_TRUST_HELPER") != "1" {
		return
	}
	switch os.Getenv("CODEX_TRUST_SCENARIO") {
	case "exit":
		os.Exit(7)
	case "hang":
		time.Sleep(10 * time.Second)
		os.Exit(0)
	}
	scanner := bufio.NewScanner(os.Stdin)
	encoder := json.NewEncoder(os.Stdout)
	for scanner.Scan() {
		var request map[string]any
		if json.Unmarshal(scanner.Bytes(), &request) != nil {
			os.Exit(8)
		}
		switch numberValue(request["id"]) {
		case 1:
			_ = encoder.Encode(map[string]any{"id": 1, "result": map[string]any{}})
		case 2:
			_ = encoder.Encode(map[string]any{
				"id": 2,
				"result": map[string]any{
					"data": []any{
						map[string]any{
							"hooks": []any{
								map[string]any{
									"source":      "user",
									"command":     filepath.Join(os.TempDir(), "bin", "agent-telemetry"),
									"key":         "gtrace-hook",
									"currentHash": "trusted-hash",
								},
							},
						},
					},
				},
			})
		case 3:
			_ = encoder.Encode(map[string]any{"id": 3, "result": map[string]any{}})
			time.Sleep(10 * time.Second)
			os.Exit(0)
		}
	}
	os.Exit(9)
}

func readTestJSON(t *testing.T, path string, target any) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, target); err != nil {
		t.Fatal(err)
	}
}
