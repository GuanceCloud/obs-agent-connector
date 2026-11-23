package install

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

const (
	managedWindowsPolicyKey = `SOFTWARE\Policies\ClaudeCode`
	managedWindowsLockName  = ".obs-claude-desktop.lock"
	managedWindowsLockWait  = 5 * time.Second

	// The Windows Modules Installer service owns Program Files on stock Windows.
	managedWindowsTrustedInstaller = "S-1-5-80-956008885-3418522649-1831038044-1853292631-2271478464"
	managedWindowsFileDeleteChild  = 0x40
	managedWindowsWriteAccess      = windows.GENERIC_ALL | windows.GENERIC_WRITE |
		windows.MAXIMUM_ALLOWED | windows.ACCESS_SYSTEM_SECURITY |
		windows.DELETE | windows.WRITE_DAC | windows.WRITE_OWNER |
		windows.FILE_WRITE_DATA | windows.FILE_APPEND_DATA | windows.FILE_WRITE_EA |
		windows.FILE_WRITE_ATTRIBUTES | managedWindowsFileDeleteChild

	// A callback ACE has the same mask and SID prefix as an ordinary allow ACE.
	managedWindowsAllowedCallbackACE = 0x09
	managedWindowsDeniedObjectACE    = 0x06
	managedWindowsDeniedCallbackACE  = 0x0A
	managedWindowsDeniedCallbackObj  = 0x0C
)

func checkManagedDesktopPath(path string, info os.FileInfo, _ int, kind managedPathKind) error {
	if kind == managedDirectoryPath && !info.IsDir() || kind != managedDirectoryPath && !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe Claude Desktop machine path %s", path)
	}
	return checkManagedWindowsPath(path, nil)
}

func checkManagedWindowsPath(path string, account *windows.SID) error {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	file, err := windows.CreateFile(name, windows.READ_CONTROL|windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return fmt.Errorf("inspect Claude Desktop path %s: %w", path, err)
	}
	defer windows.CloseHandle(file)

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(file, &info); err != nil {
		return fmt.Errorf("inspect Claude Desktop file attributes for %s: %w", path, err)
	}
	if info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		return fmt.Errorf("unsafe Claude Desktop path %s. Reparse points are unsupported", path)
	}
	sd, err := windows.GetSecurityInfo(file, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION|windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		return fmt.Errorf("inspect Claude Desktop permissions for %s: %w", path, err)
	}
	if err := checkManagedWindowsSecurity(sd, account); err != nil {
		return fmt.Errorf("unsafe Claude Desktop path %s: %w", path, err)
	}
	return nil
}

func checkManagedWindowsSecurity(sd *windows.SECURITY_DESCRIPTOR, account *windows.SID) error {
	owner, _, err := sd.Owner()
	if err != nil || !trustedManagedWindowsSID(owner, account) {
		return errors.New("the owner is outside the trusted Windows accounts")
	}
	dacl, _, err := sd.DACL()
	if err != nil || dacl == nil {
		return errors.New("a discretionary access list must restrict write access")
	}
	for i := uint16(0); i < dacl.AceCount; i++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, uint32(i), &ace); err != nil {
			return fmt.Errorf("inspect Windows access entry: %w", err)
		}
		if err := checkManagedWindowsACE(ace, account); err != nil {
			return err
		}
	}
	return nil
}

func checkManagedWindowsACE(ace *windows.ACCESS_ALLOWED_ACE, account *windows.SID) error {
	if ace.Header.AceFlags&windows.INHERIT_ONLY_ACE != 0 {
		return nil
	}
	switch ace.Header.AceType {
	case windows.ACCESS_DENIED_ACE_TYPE, managedWindowsDeniedObjectACE,
		managedWindowsDeniedCallbackACE, managedWindowsDeniedCallbackObj:
		return nil
	}
	const sidOffset = unsafe.Offsetof(ace.SidStart)
	if uintptr(ace.Header.AceSize) < sidOffset {
		return errors.New("invalid Windows access entry")
	}
	if ace.Mask&managedWindowsWriteAccess == 0 {
		return nil
	}
	if ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE && ace.Header.AceType != managedWindowsAllowedCallbackACE {
		return errors.New("an unsupported Windows access entry grants write access")
	}
	if uintptr(ace.Header.AceSize) < sidOffset+8 {
		return errors.New("a Windows write access entry has an invalid SID")
	}
	sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
	if !sid.IsValid() || sidOffset+uintptr(sid.Len()) > uintptr(ace.Header.AceSize) {
		return errors.New("a Windows write access entry has an invalid SID")
	}
	if !trustedManagedWindowsSID(sid, account) {
		return errors.New("write access is granted outside the trusted Windows accounts")
	}
	return nil
}

func trustedManagedWindowsSID(sid, account *windows.SID) bool {
	return sid != nil && (sid.IsWellKnown(windows.WinBuiltinAdministratorsSid) ||
		sid.IsWellKnown(windows.WinLocalSystemSid) || sid.String() == managedWindowsTrustedInstaller ||
		(account != nil && sid.Equals(account)))
}

func prepareManagedDesktopPath(path string, kind managedPathKind, _ os.FileMode) error {
	var sddl string
	switch kind {
	case managedDirectoryPath:
		sddl = "O:BAD:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FRFX;;;BU)"
	case managedRuntimeFile:
		sddl = "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FRFX;;;BU)"
	case managedPolicyFile:
		sddl = "O:BAD:P(A;;FA;;;SY)(A;;FA;;;BA)(A;;FR;;;BU)"
	default:
		return errors.New("unknown Claude Desktop machine path kind")
	}
	return setManagedWindowsSecurity(path, sddl)
}

func protectClaudeDesktopUserConfig(path string) error {
	account, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("resolve the Windows account for telemetry configuration: %w", err)
	}
	sid := account.User.Sid.String()
	return setManagedWindowsSecurity(path, "O:"+sid+"D:P(A;;FA;;;SY)(A;;FA;;;"+sid+")")
}

func setManagedWindowsSecurity(path, sddl string) error {
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("build Windows permissions for %s: %w", path, err)
	}
	owner, _, err := sd.Owner()
	if err != nil {
		return fmt.Errorf("resolve Windows owner for %s: %w", path, err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		return fmt.Errorf("resolve Windows access list for %s: %w", path, err)
	}
	info := windows.OWNER_SECURITY_INFORMATION | windows.DACL_SECURITY_INFORMATION | windows.PROTECTED_DACL_SECURITY_INFORMATION
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.SECURITY_INFORMATION(info), owner, nil, dacl, nil); err != nil {
		return fmt.Errorf("set protected Windows permissions on %s: %w", path, err)
	}
	return nil
}

func lockManagedDirectory(path string) (func(), error) {
	name, err := windows.UTF16PtrFromString(filepath.Join(path, managedWindowsLockName))
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(managedWindowsLockWait)
	for {
		lock, err := windows.CreateFile(name, windows.GENERIC_READ|windows.GENERIC_WRITE|windows.DELETE, 0, nil,
			windows.CREATE_NEW, windows.FILE_ATTRIBUTE_HIDDEN|windows.FILE_FLAG_DELETE_ON_CLOSE|windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
		if err == nil {
			return func() { _ = windows.CloseHandle(lock) }, nil
		}
		if !managedWindowsLockBusy(name, err) {
			return nil, err
		}
		if !time.Now().Before(deadline) {
			return nil, errors.New("the Claude Desktop machine directory is busy. Retry after the other operation finishes")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func managedWindowsLockBusy(name *uint16, err error) bool {
	if errors.Is(err, windows.ERROR_FILE_EXISTS) || errors.Is(err, windows.ERROR_ALREADY_EXISTS) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) {
		return true
	}
	if !errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		return false
	}
	// An exclusively opened delete-on-close file may appear as access denied
	// to concurrent opens. A missing lock still leaves the real access error.
	_, attributesErr := windows.GetFileAttributes(name)
	return attributesErr == nil || errors.Is(attributesErr, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(attributesErr, windows.ERROR_SHARING_VIOLATION)
}

func managedDesktopAdminPrivileges() (bool, error) {
	admins, err := windows.CreateWellKnownSid(windows.WinBuiltinAdministratorsSid)
	if err != nil {
		return false, fmt.Errorf("resolve the Windows Administrators group: %w", err)
	}
	// A null token requests the effective token. The filtered UAC token has
	// Administrators marked deny-only, which CheckTokenMembership excludes.
	admin, err := windows.Token(0).IsMember(admins)
	if err != nil {
		return false, fmt.Errorf("inspect Windows administrator permissions: %w", err)
	}
	return admin, nil
}

func checkManagedDesktopPriority() error {
	key, err := registry.OpenKey(registry.LOCAL_MACHINE, managedWindowsPolicyKey, registry.QUERY_VALUE|registry.WOW64_64KEY)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect the Windows Claude Code device policy: %w", err)
	}
	defer key.Close()
	// Query the presence and size only. Existing device policy may contain secrets.
	_, _, err = key.GetValue("Settings", nil)
	if errors.Is(err, registry.ErrNotExist) {
		return nil
	}
	if err == nil || errors.Is(err, registry.ErrShortBuffer) {
		return fmt.Errorf(`Claude Code has a higher-priority device policy at HKLM\%s. Ask the device administrator to deploy the OBS Hook there`, managedWindowsPolicyKey)
	}
	return fmt.Errorf("inspect the Windows Claude Code Settings value: %w", err)
}

func elevateManagedDesktop(action, source string) error {
	parameters, err := managedWindowsAdminParameters(action)
	if err != nil {
		return err
	}
	account, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return fmt.Errorf("resolve the Windows account for administrator staging: %w", err)
	}
	return withManagedWindowsStaging(account.User.Sid, windows.KnownFolderPath, func(dir string) error {
		staged := filepath.Join(dir, "obs-agent-connector.exe")
		if err := copyExecutable(source, staged); err != nil {
			return fmt.Errorf("stage the Windows administrator helper: %w", err)
		}
		pinned, err := pinManagedWindowsExecutable(staged)
		if err != nil {
			return fmt.Errorf("protect the Windows administrator helper during authorization: %w", err)
		}
		defer windows.CloseHandle(pinned)
		if err := verifyOBSExecutable(staged); err != nil {
			return err
		}
		return runManagedWindowsAdministrator(staged, parameters, action)
	})
}

func withManagedWindowsStaging(account *windows.SID, knownFolderPath func(*windows.KNOWNFOLDERID, uint32) (string, error), run func(string) error) error {
	sid := account.String()
	sddl := "O:" + sid + "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;BA)(A;OICI;FA;;;" + sid + ")"
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		return fmt.Errorf("build private Windows administrator staging permissions: %w", err)
	}
	sa := windows.SecurityAttributes{
		Length:             uint32(unsafe.Sizeof(windows.SecurityAttributes{})),
		SecurityDescriptor: sd,
	}
	var failures []error
	for _, candidate := range []struct {
		name string
		id   *windows.KNOWNFOLDERID
	}{
		{"Local AppData", windows.FOLDERID_LocalAppData},
		{"user profile", windows.FOLDERID_Profile},
	} {
		root, err := knownFolderPath(candidate.id, 0)
		if err != nil {
			failures = append(failures, fmt.Errorf("resolve Windows %s folder: %w", candidate.name, err))
			continue
		}
		dir, err := createManagedWindowsStaging(root, account, &sa)
		if err != nil {
			failures = append(failures, fmt.Errorf("Windows %s folder %s: %w", candidate.name, root, err))
			continue
		}
		defer os.RemoveAll(dir)
		return run(dir)
	}
	return fmt.Errorf("no safe staging directory for the Windows administrator helper: %w", errors.Join(failures...))
}

func createManagedWindowsStaging(root string, account *windows.SID, sa *windows.SecurityAttributes) (string, error) {
	volume := filepath.VolumeName(root)
	if !filepath.IsAbs(root) || len(volume) != 2 || volume[1] != ':' {
		return "", errors.New("administrator staging requires a local drive")
	}
	drive, err := windows.UTF16PtrFromString(volume + `\`)
	if err != nil {
		return "", err
	}
	switch windows.GetDriveType(drive) {
	case windows.DRIVE_FIXED, windows.DRIVE_REMOVABLE, windows.DRIVE_RAMDISK:
	default:
		return "", errors.New("administrator staging requires a local drive")
	}
	if err := checkManagedWindowsPath(root, account); err != nil {
		return "", err
	}
	var suffix [16]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return "", fmt.Errorf("generate a private Windows staging name: %w", err)
	}
	dir := filepath.Join(root, "obs-claude-desktop-admin-"+hex.EncodeToString(suffix[:]))
	name, err := windows.UTF16PtrFromString(dir)
	if err != nil {
		return "", err
	}
	// Apply the protected DACL before the staging directory can inherit any access entries.
	if err := windows.CreateDirectory(name, sa); err != nil {
		return "", fmt.Errorf("create a private Windows staging directory: %w", err)
	}
	if err := checkManagedWindowsPath(dir, account); err != nil {
		_ = os.Remove(dir)
		return "", err
	}
	return dir, nil
}

func pinManagedWindowsExecutable(path string) (windows.Handle, error) {
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	// Keep the bytes and filename stable through verification, the UAC prompt,
	// and the helper's read of its own executable. The loader can still read it.
	file, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil,
		windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT, 0)
	if err != nil {
		return 0, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(file, &info); err != nil {
		_ = windows.CloseHandle(file)
		return 0, err
	}
	if info.FileAttributes&(windows.FILE_ATTRIBUTE_DIRECTORY|windows.FILE_ATTRIBUTE_REPARSE_POINT) != 0 {
		_ = windows.CloseHandle(file)
		return 0, errors.New("the Windows administrator helper must be a regular executable file")
	}
	return file, nil
}

func managedWindowsAdminParameters(action string) (string, error) {
	switch action {
	case "install", "update", "remove":
		// Every argument is a fixed word. The executable goes into lpFile separately.
		return "internal " + ManagedDesktopAdminCommand + " " + action, nil
	default:
		return "", errors.New("unknown Claude Desktop administrator action")
	}
}

// managedWindowsShellExecute matches the Win32 SHELLEXECUTEINFOW layout.
type managedWindowsShellExecute struct {
	size       uint32
	mask       uint32
	window     windows.Handle
	verb       *uint16
	file       *uint16
	parameters *uint16
	directory  *uint16
	show       int32
	instance   windows.Handle
	idList     uintptr
	class      *uint16
	classKey   windows.Handle
	hotKey     uint32
	icon       windows.Handle
	process    windows.Handle
}

var managedWindowsShellExecuteEx = windows.NewLazySystemDLL("shell32.dll").NewProc("ShellExecuteExW")

func runManagedWindowsAdministrator(path, parameters, action string) error {
	file, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return err
	}
	arguments, err := windows.UTF16PtrFromString(parameters)
	if err != nil {
		return err
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return fmt.Errorf("resolve the Windows system directory: %w", err)
	}
	directory, err := windows.UTF16PtrFromString(system)
	if err != nil {
		return err
	}
	if err := managedWindowsShellExecuteEx.Find(); err != nil {
		return fmt.Errorf("load Windows administrator authorization: %w", err)
	}
	const (
		seeMaskNoCloseProcess = 0x00000040
		seeMaskNoAsync        = 0x00000100
		seeMaskNoConsole      = 0x00008000
	)
	info := managedWindowsShellExecute{
		size: uint32(unsafe.Sizeof(managedWindowsShellExecute{})),
		mask: seeMaskNoCloseProcess | seeMaskNoAsync | seeMaskNoConsole,
		verb: windows.StringToUTF16Ptr("runas"), file: file, parameters: arguments,
		directory: directory, show: windows.SW_SHOWNORMAL,
	}
	if err := launchManagedWindowsAdministrator(&info); err != nil {
		if errors.Is(err, windows.ERROR_CANCELLED) {
			return fmt.Errorf("Windows administrator authorization was canceled for Claude Desktop %s", action)
		}
		return fmt.Errorf("start the Windows administrator helper for Claude Desktop %s: %w", action, err)
	}
	if info.process == 0 || info.process == windows.InvalidHandle {
		return errors.New("Windows did not return the administrator helper process handle")
	}
	defer windows.CloseHandle(info.process)

	state, err := windows.WaitForSingleObject(info.process, windows.INFINITE)
	if err != nil {
		return fmt.Errorf("wait for the Windows administrator helper: %w", err)
	}
	if state != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("unexpected Windows administrator helper wait result %d", state)
	}
	var code uint32
	if err := windows.GetExitCodeProcess(info.process, &code); err != nil {
		return fmt.Errorf("read the Windows administrator helper result: %w", err)
	}
	if code != 0 {
		return fmt.Errorf("Claude Desktop machine %s failed in the Windows administrator helper (exit status %d)", action, code)
	}
	return nil
}

func launchManagedWindowsAdministrator(info *managedWindowsShellExecute) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	const comAlreadyInitialized syscall.Errno = 1 // S_FALSE
	if err := windows.CoInitializeEx(0, windows.COINIT_APARTMENTTHREADED|windows.COINIT_DISABLE_OLE1DDE); err != nil && !errors.Is(err, comAlreadyInitialized) {
		return err
	}
	defer windows.CoUninitialize()
	result, _, err := managedWindowsShellExecuteEx.Call(uintptr(unsafe.Pointer(info)))
	runtime.KeepAlive(info)
	if result == 0 {
		if err == nil || errors.Is(err, windows.ERROR_SUCCESS) {
			return errors.New("ShellExecuteEx failed without a Windows error code")
		}
		return err
	}
	return nil
}
