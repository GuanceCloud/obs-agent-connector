package install

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// Runs on native Windows too; a cross-build alone does not execute this test.
func TestQwenWorkRCUnicodeUpgradeAndRemoval(t *testing.T) {
	home := filepath.Join(t.TempDir(), "user space '\u6d4b\u8bd5")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	profile := filepath.Join(home, ".qwenworkcn")
	write := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	read := func(path string) string {
		t.Helper()
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	settingsPath := filepath.Join(profile, "settings.json")
	settingsBody := `{"theme":"dark","hooks":{"Stop":[{"matcher":"user-filter","hooks":[{"type":"command","command":"echo user-hook"},{"type":"command","command":"/old/obs-agent-connector hook qwenwork-cn"}]}],"CustomEvent":[{"hooks":[{"command":"echo custom"}]}]}}`
	write(settingsPath, settingsBody)
	write(filepath.Join(profile, ".env"), "OTHER=preserve\r\n")
	otherConfig := filepath.Join(home, ".obs-agent-connector", "other-agent", "gtrace.json")
	write(otherConfig, "{\"enabled\":false}\n")
	source := filepath.Join(home, "downloads", "candidate")
	name := "obs-agent-connector"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	destination := filepath.Join(home, ".obs-agent-connector", "qwenwork-cn", "bin", name)
	write(source, "candidate-one")
	o := QwenWorkOptions{CodeBuddyOptions: CodeBuddyOptions{Home: home, SourceExecutable: source, DestinationExecutable: destination, Endpoint: "https://example.invalid", CaptureContent: "none"}}
	r, err := InstallQwenWork(o)
	if err != nil {
		t.Fatal(err)
	}
	if read(destination) != "candidate-one" {
		t.Fatal("fresh binary not copied")
	}
	beforeConfig, beforeEnv := read(r.ConfigFile), read(filepath.Join(profile, ".env"))
	checkpoint := filepath.Join(home, ".obs-agent-connector", "qwenwork-cn", "state", "completed", "synthetic")
	write(checkpoint, "completed")
	write(source, "candidate-two")
	o.NoConfig, o.Endpoint = true, "https://must-not-replace.invalid"
	for i := 0; i < 2; i++ {
		if _, err := InstallQwenWork(o); err != nil {
			t.Fatal(err)
		}
		if read(destination) != "candidate-two" {
			t.Fatal("upgrade binary not copied")
		}
		if read(r.ConfigFile) != beforeConfig || read(filepath.Join(profile, ".env")) != beforeEnv || read(checkpoint) != "completed" {
			t.Fatal("upgrade changed preserved config or checkpoint")
		}
		settings, err := readJSONObject(settingsPath)
		if err != nil {
			t.Fatal(err)
		}
		hooks := settings["hooks"].(map[string]any)
		command := quoteQwenWorkPath(destination) + " hook qwenwork-cn --profile " + quoteQwenWorkPath(profile)
		for _, event := range qwenWorkEvents {
			count := 0
			for _, group := range hooks[event].([]any) {
				for _, handler := range group.(map[string]any)["hooks"].([]any) {
					if handler.(map[string]any)["command"] == command {
						count++
					}
				}
			}
			if count != 1 {
				t.Fatalf("%s has %d current handlers", event, count)
			}
		}
	}
	if _, err := RemoveAdapter("qwenwork-cn", home, RemoveOptions{PurgeManaged: true}); err != nil {
		t.Fatal(err)
	}
	settings, err := readJSONObject(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"matcher": "user-filter", "hooks": []any{map[string]any{"type": "command", "command": "echo user-hook"}}}
	hooks := settings["hooks"].(map[string]any)
	if settings["theme"] != "dark" || !reflect.DeepEqual(hooks["Stop"], []any{want}) || len(hooks["CustomEvent"].([]any)) != 1 {
		t.Fatal("removal changed unrelated settings or shared Hook group")
	}
	if read(filepath.Join(profile, ".env")) != beforeEnv || read(otherConfig) != "{\"enabled\":false}\n" {
		t.Fatal("removal changed user environment or other Agent")
	}
	if _, err := os.Stat(filepath.Dir(r.ConfigFile)); !os.IsNotExist(err) {
		t.Fatal("managed directory remains")
	}
}

func TestQwenWorkWindowsQuotedCommandRecognition(t *testing.T) {
	executable := "C:\\Users\\user space \u6d4b\u8bd5\\bin\\obs-agent-connector.exe"
	profile := "C:\\Users\\user space \u6d4b\u8bd5\\.qwenworkcn"
	command := quoteHookCommand(executable) + " hook qwenwork-cn --profile " + quoteHookCommand(profile)
	if !managedQwenWorkCommand.MatchString(command) {
		t.Fatal("Windows Unicode command is not recognized")
	}
	if runtime.GOOS == "windows" && (quoteQwenWorkPath(executable) != quoteHookCommand(executable) || quoteQwenWorkPath(profile) != quoteHookCommand(profile)) {
		t.Fatal("Windows registration quoting changed")
	}
	for _, suffix := range []string{" & echo keep", " | more", "-other"} {
		if managedQwenWorkCommand.MatchString(command + suffix) {
			t.Fatal("matched a user-owned command")
		}
	}
}
