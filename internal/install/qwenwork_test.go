package install

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	qwconfig "github.com/GuanceCloud/obs-agent-connector/internal/adapters/qwenwork/config"
)

func TestQwenWorkLifecycle(t *testing.T) {
	home := t.TempDir()
	profile := filepath.Join(home, ".qwenworkcn")
	os.MkdirAll(profile, 0700)
	settings := filepath.Join(profile, "settings.json")
	os.WriteFile(settings, []byte(`{"theme":"dark","hooks":{"Stop":[{"hooks":[{"command":"echo keep"},{"command":"/old/obs-agent-connector hook qwenwork-cn"}]}]}}`), 0600)
	binary := filepath.Join(home, "obs-agent-connector")
	os.WriteFile(binary, []byte("test"), 0700)
	o := QwenWorkOptions{CodeBuddyOptions: CodeBuddyOptions{Home: home, SourceExecutable: binary, DestinationExecutable: binary, Endpoint: "https://test.invalid", XToken: "secret", CaptureContent: "none"}}
	r, e := InstallQwenWork(o)
	if e != nil {
		t.Fatal(e)
	}
	before, _ := os.ReadFile(r.ConfigFile)
	o.NoConfig = true
	o.Endpoint = "https://should-not-change.invalid"
	if _, e = InstallQwenWork(o); e != nil {
		t.Fatal(e)
	}
	after, _ := os.ReadFile(r.ConfigFile)
	if string(before) != string(after) {
		t.Fatal("update changed config")
	}
	body, _ := os.ReadFile(settings)
	if strings.Count(string(body), "hook qwenwork-cn") != len(qwenWorkEvents) || !strings.Contains(string(body), "echo keep") || !strings.Contains(string(body), "dark") {
		t.Fatal("settings lost or hooks duplicated")
	}
	if _, e = RemoveAdapter("qwenwork-cn", home, RemoveOptions{PurgeManaged: true}); e != nil {
		t.Fatal(e)
	}
	body, _ = os.ReadFile(settings)
	if strings.Contains(string(body), "hook qwenwork-cn") || !strings.Contains(string(body), "echo keep") {
		t.Fatal("remove lost user hooks")
	}
	if _, e = os.Stat(r.ConfigFile); !os.IsNotExist(e) {
		t.Fatal("managed config remains")
	}
}

func TestQwenWorkInstallPreservesThinkingChoice(t *testing.T) {
	for _, tt := range []struct {
		name string
		body string
		want bool
	}{
		{"unspecified", `{"future":{"setting":true}}`, false},
		{"disabled", `{"captureThinking":false,"future":{"setting":true}}`, false},
		{"enabled", `{"captureThinking":true,"future":{"setting":true}}`, true},
		{"legacy disabled", `{"capture_thinking":false,"future":{"setting":true}}`, false},
		{"legacy enabled", `{"capture_thinking":true,"future":{"setting":true}}`, true},
		{"canonical disabled", `{"captureThinking":false,"capture_thinking":true,"future":{"setting":true}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			t.Setenv("HOME", home)
			binary := filepath.Join(home, "obs-agent-connector")
			if err := os.WriteFile(binary, []byte("test"), 0700); err != nil {
				t.Fatal(err)
			}
			configPath := filepath.Join(home, ".obs-agent-connector", "qwenwork-cn", "gtrace.json")
			if err := os.MkdirAll(filepath.Dir(configPath), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(configPath, []byte(tt.body), 0600); err != nil {
				t.Fatal(err)
			}
			var initial map[string]any
			if err := json.Unmarshal([]byte(tt.body), &initial); err != nil {
				t.Fatal(err)
			}
			o := QwenWorkOptions{CodeBuddyOptions: CodeBuddyOptions{Home: home, SourceExecutable: binary, DestinationExecutable: binary, Endpoint: "https://example.invalid", CaptureContent: "preview"}}
			for _, noConfig := range []bool{false, false, true} {
				o.NoConfig = noConfig
				before, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := InstallQwenWork(o); err != nil {
					t.Fatal(err)
				}
				after, err := os.ReadFile(configPath)
				if err != nil {
					t.Fatal(err)
				}
				if noConfig && string(before) != string(after) {
					t.Fatal("no-config update changed configuration bytes")
				}
				var current map[string]any
				if err := json.Unmarshal(after, &current); err != nil {
					t.Fatal(err)
				}
				for _, key := range []string{"captureThinking", "capture_thinking", "future"} {
					oldValue, oldExists := initial[key]
					newValue, newExists := current[key]
					if oldExists != newExists || !reflect.DeepEqual(oldValue, newValue) {
						t.Fatalf("install changed preserved field %s", key)
					}
				}
				cfg := qwconfig.Resolve(qwconfig.ResolveOptions{Home: home, Env: map[string]string{}})
				if cfg.CaptureThinking != tt.want {
					t.Fatalf("CaptureThinking = %v, want %v", cfg.CaptureThinking, tt.want)
				}
			}
		})
	}
}
func TestQwenWorkRejectsMalformedSettings(t *testing.T) {
	for _, body := range []string{"{", `{"hooks":[]}`, `{"hooks":{"Stop":{}}}`} {
		home := t.TempDir()
		profile := filepath.Join(home, ".qwenworkcn")
		os.MkdirAll(profile, 0700)
		path := filepath.Join(profile, "settings.json")
		os.WriteFile(path, []byte(body), 0600)
		_, e := InstallQwenWork(QwenWorkOptions{CodeBuddyOptions: CodeBuddyOptions{Home: home, Endpoint: "https://test.invalid"}})
		if e == nil {
			t.Fatal("accepted malformed settings")
		}
		after, _ := os.ReadFile(path)
		if string(after) != body {
			t.Fatal("mutated invalid settings")
		}
	}
}

func TestQwenWorkManagedCommandsAreStandalone(t *testing.T) {
	for _, command := range []string{
		`echo "obs-agent-connector hook qwenwork-cn"`,
		`/bin/other obs-agent-connector hook qwenwork-cn`,
		`/bin/not-obs-agent-connector hook qwenwork-cn`,
		`obs-agent-connector hook qwenwork-cn-other`,
		`obs-agent-connector hook qwenwork-cn; echo keep`,
	} {
		if managedQwenWorkCommand.MatchString(command) {
			t.Fatalf("matched user command %q", command)
		}
	}
	for _, command := range []string{
		`/old/obs-agent-connector hook qwenwork-cn`,
		`"C:\Program Files\obs-agent-connector.exe" hook qwenwork-cn --profile "C:\Users\profile"`,
		quoteQwenWorkPath("/test user's/$data/obs-agent-connector") + " hook qwenwork-cn --profile " + quoteQwenWorkPath("/test user's/$data/profile"),
	} {
		if !managedQwenWorkCommand.MatchString(command) {
			t.Fatalf("missed managed command %q", command)
		}
	}
}
