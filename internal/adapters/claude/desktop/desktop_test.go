package desktop

import (
	"errors"
	"os/user"
	"path/filepath"
	"strings"
	"testing"
)

func TestManagedDesktopUsesOperatingSystemAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	lookup := func(uid string) (*user.User, error) {
		if uid != "501" {
			t.Fatalf("lookup called with unexpected UID %q", uid)
		}
		return &user.User{Uid: uid, HomeDir: home}, nil
	}
	if got, err := homeForUID(501, 501, lookup); got != home || err != nil {
		t.Fatalf("managed Hook resolved %q, want the account home: %v", got, err)
	}
	if _, err := homeForUID(0, 0, lookup); err == nil {
		t.Fatal("managed Hook allowed root")
	}
	if _, err := homeForUID(501, 502, lookup); err == nil {
		t.Fatal("managed Hook allowed a mismatched effective account")
	}
	if got, err := homeForUID(501, 501, func(string) (*user.User, error) {
		return &user.User{Uid: "501", HomeDir: "relative"}, nil
	}); got != "" || err == nil {
		t.Fatalf("managed Hook resolved a relative account home: %q, %v", got, err)
	}
}

func TestManagedDesktopPaths(t *testing.T) {
	programFiles := filepath.Join(t.TempDir(), "Program Files")
	for _, tt := range []struct {
		goos, directory, runtimeSuffix string
	}{
		{"darwin", "/Library/Application Support/ClaudeCode", ""},
		{"linux", "/etc/claude-code", ""},
		{"windows", filepath.Join(programFiles, "ClaudeCode"), ".exe"},
	} {
		t.Run(tt.goos, func(t *testing.T) {
			got, err := managedPathsForOS(tt.goos, func() (string, error) {
				if tt.goos != "windows" {
					t.Fatal("Windows Known Folder lookup used for Unix")
				}
				return programFiles, nil
			})
			want := MachinePaths{
				Directory:   tt.directory,
				PolicyPath:  filepath.Join(tt.directory, "managed-settings.d", "50-obs-claude-desktop.json"),
				RuntimePath: filepath.Join(tt.directory, "obs-agent-connector-claude-desktop"+tt.runtimeSuffix),
			}
			if got != want || err != nil {
				t.Fatalf("machine paths = %#v, want %#v: %v", got, want, err)
			}
		})
	}
	if _, err := managedPathsForOS("freebsd", nil); err == nil {
		t.Fatal("accepted unsupported platform")
	}
	if _, err := managedPathsForOS("windows", func() (string, error) { return "Program Files", nil }); err == nil {
		t.Fatal("accepted relative Windows Program Files path")
	}
	lookupErr := errors.New("folder unavailable")
	if _, err := managedPathsForOS("windows", func() (string, error) { return "", lookupErr }); !errors.Is(err, lookupErr) {
		t.Fatalf("looked past failure to find Windows Program Files: %v", err)
	}
}

func TestManagedDesktopUsesWindowsProfile(t *testing.T) {
	profile := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	account := &user.User{Uid: "S-1-5-21-123-456-789-1001", HomeDir: profile}
	lookup := func() (*user.User, error) { return account, nil }
	if got, err := homeForWindowsAccount(lookup); got != profile || err != nil {
		t.Fatalf("profile = %q, want real Windows profile: %v", got, err)
	}
	for _, sid := range []string{"S-1-5-18", "S-1-5-19", "S-1-5-20"} {
		account.Uid = sid
		if got, err := homeForWindowsAccount(lookup); got != "" || err == nil || !strings.Contains(err.Error(), "account using Claude Desktop") {
			t.Errorf("accepted Windows service account %s: %q, %v", sid, got, err)
		}
	}
	account.Uid, account.HomeDir = "S-1-5-21-123-456-789-1001", "relative"
	if got, err := homeForWindowsAccount(lookup); got != "" || err == nil {
		t.Fatalf("accepted relative Windows profile: %q, %v", got, err)
	}
}
