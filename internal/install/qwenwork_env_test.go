package install

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestQwenWorkTokenUsageEnvironment(t *testing.T) {
	for _, tc := range []struct{ name, input, want string }{
		{"empty", "", "QODERCN_EXPOSE_TOKEN_USAGE=1\n"},
		{"append", "OTHER=value", "OTHER=value\nQODERCN_EXPOSE_TOKEN_USAGE=1\n"},
		{"duplicates", "QODERCN_EXPOSE_TOKEN_USAGE=0\nexport QODERCN_EXPOSE_TOKEN_USAGE = 'false' # keep\n", "QODERCN_EXPOSE_TOKEN_USAGE=1\nexport QODERCN_EXPOSE_TOKEN_USAGE = 1 # keep\n"},
		{"comments", "# QODERCN_EXPOSE_TOKEN_USAGE=0\nOTHER=x# comment\n", "# QODERCN_EXPOSE_TOKEN_USAGE=0\nOTHER=x# comment\nQODERCN_EXPOSE_TOKEN_USAGE=1\n"},
		{"bom crlf", "\xef\xbb\xbfOTHER=x\r\n  export QODERCN_EXPOSE_TOKEN_USAGE = \"0\" \t# keep\r\n", "\xef\xbb\xbfOTHER=x\r\n  export QODERCN_EXPOSE_TOKEN_USAGE = 1 \t# keep\r\n"},
		{"bom only", "\xef\xbb\xbf", "\xef\xbb\xbfQODERCN_EXPOSE_TOKEN_USAGE=1\n"},
		{"multiline unrelated", "OTHER='first\nQODERCN_EXPOSE_TOKEN_USAGE=0\nlast'\n", "OTHER='first\nQODERCN_EXPOSE_TOKEN_USAGE=0\nlast'\nQODERCN_EXPOSE_TOKEN_USAGE=1\n"},
		{"multiline target", "QODERCN_EXPOSE_TOKEN_USAGE=\"zero\nmore\" # keep\nOTHER=x\n", "QODERCN_EXPOSE_TOKEN_USAGE=1 # keep\nOTHER=x\n"},
		{"empty value", "QODERCN_EXPOSE_TOKEN_USAGE= # keep\n", "QODERCN_EXPOSE_TOKEN_USAGE= 1# keep\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := enableQwenWorkTokenUsage([]byte(tc.input))
			if err != nil || string(got) != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			again, err := enableQwenWorkTokenUsage(got)
			if err != nil || string(again) != string(got) {
				t.Fatalf("not idempotent: %q, %v", again, err)
			}
		})
	}
}

func qwenWorkEnvOptions(t *testing.T) QwenWorkOptions {
	t.Helper()
	home := t.TempDir()
	binary := filepath.Join(home, "obs-agent-connector")
	if err := os.WriteFile(binary, []byte("test"), 0700); err != nil {
		t.Fatal(err)
	}
	return QwenWorkOptions{CodeBuddyOptions: CodeBuddyOptions{Home: home, SourceExecutable: binary, DestinationExecutable: binary, Endpoint: "https://test.invalid", XToken: "secret"}}
}

func TestQwenWorkInstallTokenUsage(t *testing.T) {
	for _, custom := range []bool{false, true} {
		o := qwenWorkEnvOptions(t)
		profile := qwenWorkProfile(o.Home)
		if custom {
			o.ProfileDir = filepath.Join(o.Home, "custom-profile")
			profile = o.ProfileDir
		}
		if _, err := InstallQwenWork(o); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(profile, ".env")
		body, err := os.ReadFile(path)
		if err != nil || string(body) != qwenWorkTokenUsageKey+"=1\n" {
			t.Fatalf("environment: %q, %v", body, err)
		}
		info, err := os.Stat(path)
		if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0600) {
			t.Fatalf("environment permissions: %v, %v", info, err)
		}
		if custom {
			if _, err := os.Stat(filepath.Join(qwenWorkProfile(o.Home), ".env")); !os.IsNotExist(err) {
				t.Fatal("wrote default profile for custom install")
			}
		}
		original := "# keep\r\nexport QODERCN_EXPOSE_TOKEN_USAGE=0\r\nOTHER='secret\r\nQODERCN_EXPOSE_TOKEN_USAGE=0'\r\nQODERCN_EXPOSE_TOKEN_USAGE=0 # duplicate\r\n"
		want := "# keep\r\nexport QODERCN_EXPOSE_TOKEN_USAGE=1\r\nOTHER='secret\r\nQODERCN_EXPOSE_TOKEN_USAGE=0'\r\nQODERCN_EXPOSE_TOKEN_USAGE=1 # duplicate\r\n"
		if err := os.WriteFile(path, []byte(original), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0640); err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, err := InstallQwenWork(o); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(path)
			if err != nil || string(body) != want {
				t.Fatalf("reinstall environment: %q, %v", body, err)
			}
			info, err := os.Stat(path)
			if err != nil || (runtime.GOOS != "windows" && info.Mode().Perm() != 0640) {
				t.Fatalf("changed existing permissions: %v, %v", info, err)
			}
		}
	}
}

func TestQwenWorkRejectsMalformedEnvironmentBeforeWrites(t *testing.T) {
	for _, body := range []string{"OTHER='unfinished", "OTHER=\"ok\" trailing", "not an assignment", "OTHER=\x00"} {
		o := qwenWorkEnvOptions(t)
		profile := qwenWorkProfile(o.Home)
		if err := os.MkdirAll(profile, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(profile, ".env")
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		o.DestinationExecutable = filepath.Join(o.Home, "new-binary")
		o.ConfigFile = filepath.Join(o.Home, "new-config.json")
		if _, err := InstallQwenWork(o); err == nil {
			t.Fatal("accepted malformed environment")
		}
		for _, absent := range []string{o.DestinationExecutable, o.ConfigFile, filepath.Join(profile, "settings.json")} {
			if _, err := os.Stat(absent); !os.IsNotExist(err) {
				t.Fatalf("wrote %s before validation", absent)
			}
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != body {
			t.Fatalf("changed malformed environment: %q, %v", after, err)
		}
	}
}

func TestQwenWorkRejectsEnvironmentSymlinkBeforeWrites(t *testing.T) {
	for _, dangling := range []bool{false, true} {
		o := qwenWorkEnvOptions(t)
		profile := qwenWorkProfile(o.Home)
		if err := os.MkdirAll(profile, 0700); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(o.Home, "shared.env")
		original := "OTHER=keep\nQODERCN_EXPOSE_TOKEN_USAGE=0\n"
		if !dangling {
			if err := os.WriteFile(target, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
		}
		path := filepath.Join(profile, ".env")
		if err := os.Symlink(target, path); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
		o.DestinationExecutable = filepath.Join(o.Home, "new-binary")
		o.ConfigFile = filepath.Join(o.Home, "new-config.json")
		if _, err := InstallQwenWork(o); err == nil {
			t.Fatal("accepted environment symlink")
		}
		if got, err := os.Readlink(path); err != nil || got != target {
			t.Fatalf("changed symlink: %q, %v", got, err)
		}
		for _, absent := range []string{o.DestinationExecutable, o.ConfigFile, filepath.Join(profile, "settings.json")} {
			if _, err := os.Stat(absent); !os.IsNotExist(err) {
				t.Fatalf("wrote %s before validation", absent)
			}
		}
		body, err := os.ReadFile(target)
		if dangling {
			if !os.IsNotExist(err) {
				t.Fatalf("created symlink target: %v", err)
			}
		} else if err != nil || string(body) != original {
			t.Fatalf("changed symlink target: %q, %v", body, err)
		}
	}
}

func TestQwenWorkInstallPreservesEnvironmentWhenSkipped(t *testing.T) {
	for _, noConfig := range []bool{false, true} {
		for _, exists := range []bool{false, true} {
			o := qwenWorkEnvOptions(t)
			o.NoConfig = noConfig
			if !noConfig {
				disabled := false
				o.Enabled = &disabled
			}
			path := filepath.Join(qwenWorkProfile(o.Home), ".env")
			original := "QODERCN_EXPOSE_TOKEN_USAGE=0\nOTHER='unfinished"
			if exists {
				if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(original), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := InstallQwenWork(o); err != nil {
				t.Fatal(err)
			}
			body, err := os.ReadFile(path)
			if exists && (err != nil || string(body) != original) {
				t.Fatalf("changed skipped env: %q, %v", body, err)
			}
			if !exists && !os.IsNotExist(err) {
				t.Fatalf("created skipped env: %v", err)
			}
		}
	}
}

func TestQwenWorkInstallEnvironmentValidationAndRollback(t *testing.T) {
	for _, original := range []string{"OTHER='unfinished", "OTHER=\"ok\" trailing", "not an assignment", "OTHER=\x00", "QODERCN_EXPOSE_TOKEN_USAGE=0\nOTHER=keep\n", ""} {
		t.Run(original, func(t *testing.T) {
			o := qwenWorkEnvOptions(t)
			profile := qwenWorkProfile(o.Home)
			if err := os.MkdirAll(profile, 0700); err != nil {
				t.Fatal(err)
			}
			settingsPath := filepath.Join(profile, "settings.json")
			settings := []byte("{\"theme\":\"dark\"}\n")
			if err := os.WriteFile(settingsPath, settings, 0600); err != nil {
				t.Fatal(err)
			}
			envPath := filepath.Join(profile, ".env")
			if original != "" {
				if err := os.WriteFile(envPath, []byte(original), 0640); err != nil {
					t.Fatal(err)
				}
			}
			o.ConfigFile = filepath.Join(o.Home, "config.json")
			config := []byte("{\"enabled\":true}\n")
			if err := os.WriteFile(o.ConfigFile, config, 0600); err != nil {
				t.Fatal(err)
			}
			// A deterministic late failure, after settings and environment writes.
			if err := os.Mkdir(o.ConfigFile+".tmp", 0700); err != nil {
				t.Fatal(err)
			}
			if _, err := InstallQwenWork(o); err == nil {
				t.Fatal("expected failure")
			}
			for path, want := range map[string]string{settingsPath: string(settings), o.ConfigFile: string(config), envPath: original} {
				body, err := os.ReadFile(path)
				if path == envPath && original == "" {
					if !os.IsNotExist(err) {
						t.Fatal("rollback left created environment")
					}
				} else if err != nil || string(body) != want {
					t.Fatalf("changed %s: %q, %v", path, body, err)
				}
			}
		})
	}
}
