package install

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unsafe"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/claude/desktop"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
	"golang.org/x/sys/windows"
)

func TestManagedWindowsPermissions(t *testing.T) {
	for _, tc := range []struct {
		name, descriptor, errorText string
	}{
		{"administrators", "O:BAD:P(A;OICI;FA;;;BA)(A;OICI;FRFX;;;BU)", ""},
		{"trusted installer", "O:" + managedWindowsTrustedInstaller + "D:(A;;FA;;;" + managedWindowsTrustedInstaller + ")(A;;FA;;;SY)(A;;FRFX;;;BU)", ""},
		{"inherit-only creator owner", "O:BAD:(A;OICIIO;FA;;;CO)(A;OICI;FRFX;;;BU)", ""},
		{"untrusted owner", "O:BUD:(A;;FR;;;BU)", "owner"},
		{"unrestricted", "O:BAD:NO_ACCESS_CONTROL", "access list"},
		{"untrusted write", "O:BAD:(A;;FW;;;BU)", "write access"},
		{"untrusted delete child", "O:SYD:(A;;0x40;;;WD)", "write access"},
		{"untrusted change permissions", "O:SYD:(A;;WD;;;AU)", "write access"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(tc.descriptor)
			if err != nil {
				t.Fatalf("create fixture descriptor: %v", err)
			}
			err = checkManagedWindowsSecurity(sd, nil)
			if tc.errorText == "" && err != nil {
				t.Fatalf("expected protected path: %v", err)
			}
			if tc.errorText != "" && (err == nil || !strings.Contains(err.Error(), tc.errorText)) {
				t.Fatalf("expected %q, got %v", tc.errorText, err)
			}
		})
	}
}

func TestManagedWindowsStagingPermissions(t *testing.T) {
	account, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	sid := account.User.Sid.String()
	sddl := "O:" + sid + "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid + ")"
	dir := t.TempDir()
	if err := setManagedWindowsSecurity(dir, sddl); err != nil {
		t.Fatal(err)
	}
	if err := checkManagedWindowsPath(dir, account.User.Sid); err != nil {
		t.Fatal(err)
	}
}

func TestManagedWindowsCreatesAndReplacesPrivateUserConfig(t *testing.T) {
	home := t.TempDir()
	path := agentfiles.ConfigPath(home, desktop.Name)
	account, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	installed := 0
	apply := func(action, source string) error {
		if action != "install" || source != "" {
			t.Fatalf("unexpected privileged arguments: %q %q", action, source)
		}
		installed++
		return nil
	}
	for _, token := range []string{"first-fixture-token", "second-fixture-token"} {
		options := CodexOptions{Endpoint: "https://telemetry.invalid", XToken: token, CaptureContent: "none"}
		if err := installClaudeDesktopManaged(options, home, apply); err != nil {
			t.Fatal(err)
		}
		config, exists, err := ReadRuntimeConfig(path)
		if err != nil || !exists || config["enabled"] != true || stringObject(config["headers"])["X-Token"] != token {
			t.Fatalf("failed to write account telemetry configuration: %v", err)
		}
		sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
		if err != nil {
			t.Fatal(err)
		}
		owner, _, err := sd.Owner()
		if err != nil || owner == nil || !owner.Equals(account.User.Sid) {
			t.Fatalf("configuration owner is not the session account: %v", err)
		}
		dacl, _, err := sd.DACL()
		if err != nil || dacl == nil || dacl.AceCount != 2 {
			t.Fatalf("configuration should have exactly two explicit entries: %v", err)
		}
		for i := uint32(0); i < 2; i++ {
			var entry *windows.ACCESS_ALLOWED_ACE
			if err := windows.GetAce(dacl, i, &entry); err != nil {
				t.Fatal(err)
			}
			sid := (*windows.SID)(unsafe.Pointer(&entry.SidStart))
			if entry.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE || entry.Header.AceFlags != 0 ||
				(!sid.Equals(account.User.Sid) && !sid.IsWellKnown(windows.WinLocalSystemSid)) {
				t.Fatalf("unexpected Windows account config access entry %d", i)
			}
		}
	}
	if installed != 2 {
		t.Fatalf("machine installer was invoked %d times, want 2", installed)
	}
}

func TestManagedWindowsDirectoryLock(t *testing.T) {
	dir := t.TempDir()
	first, err := lockManagedDirectory(dir)
	if err != nil {
		t.Fatal(err)
	}
	var release sync.Once
	defer release.Do(first)

	done := make(chan error, 1)
	go func() {
		unlock, err := lockManagedDirectory(dir)
		if err == nil {
			unlock()
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("second lock completed while first lock was held: %v", err)
	case <-time.After(120 * time.Millisecond):
	}
	release.Do(first)
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(managedWindowsLockWait + time.Second):
		t.Fatal("second lock did not complete")
	}
	if _, err := os.Lstat(filepath.Join(dir, managedWindowsLockName)); !os.IsNotExist(err) {
		t.Fatalf("lock file remains after both handles closed: %v", err)
	}
}

func TestPinManagedWindowsExecutable(t *testing.T) {
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(t.TempDir(), "helper.exe")
	if err := copyExecutable(source, staged); err != nil {
		t.Fatal(err)
	}
	pinned, err := pinManagedWindowsExecutable(staged)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(pinned)

	name, err := windows.UTF16PtrFromString(staged)
	if err != nil {
		t.Fatal(err)
	}
	for _, access := range []uint32{windows.GENERIC_WRITE, windows.DELETE} {
		file, err := windows.CreateFile(name, access, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
			nil, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
		if err == nil {
			_ = windows.CloseHandle(file)
		}
		if !errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
			t.Fatalf("access %#x to protected helper returned %v", access, err)
		}
	}
	if out, err := exec.Command(staged, "-test.run=^$").CombinedOutput(); err != nil {
		t.Fatalf("start the protected helper: %v\n%s", err, out)
	}
}

func TestManagedWindowsAdministratorArguments(t *testing.T) {
	for _, action := range []string{"install", "update", "remove"} {
		got, err := managedWindowsAdminParameters(action)
		want := "internal " + ManagedDesktopAdminCommand + " " + action
		if err != nil || got != want {
			t.Fatalf("parameters for %s = %q, %v", action, got, err)
		}
	}
	if _, err := managedWindowsAdminParameters("install late-option"); err == nil {
		t.Fatal("unexpected administrator argument was accepted")
	}
	if unsafe.Sizeof(uintptr(0)) == 8 && unsafe.Sizeof(managedWindowsShellExecute{}) != 112 {
		t.Fatal("Win64 SHELLEXECUTEINFOW layout does not match the OS API")
	}
}
