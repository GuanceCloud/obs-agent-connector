//go:build linux

package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/GuanceCloud/obs-agent-connector/internal/adapters/codex/config"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/agentfiles"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/hooklog"
	"github.com/GuanceCloud/obs-agent-connector/internal/core/journal"
)

type nativeProcess struct {
	UID                                          int
	PID, Parent                                  int
	Start, State, MountNS, PIDNS, UserNS, Thread string
	Args                                         []string
	Init, Restricted                             bool
}

func NativeSupported() bool { return true }

func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("native process metadata exceeds bound")
	}
	return b, nil
}

func processInfo(root string, pid int, details bool) (nativeProcess, error) {
	p := nativeProcess{PID: pid}
	dir := filepath.Join(root, strconv.Itoa(pid))
	stat, err := readBounded(filepath.Join(dir, "stat"), 8192)
	if err != nil {
		return p, err
	}
	end := strings.LastIndex(string(stat), ")")
	if end < 0 {
		return p, fmt.Errorf("invalid process stat")
	}
	parts := strings.Fields(string(stat[end+1:]))
	if len(parts) < 20 {
		return p, fmt.Errorf("incomplete process stat")
	}
	p.State, p.Start = parts[0], parts[19]
	p.Parent, _ = strconv.Atoi(parts[1])
	if !details {
		return p, nil
	}
	raw, err := readBounded(filepath.Join(dir, "cmdline"), 64<<10)
	if err != nil {
		return p, err
	}
	p.Args = strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
	status, err := readBounded(filepath.Join(dir, "status"), 16<<10)
	if err != nil {
		return p, err
	}
	fields := map[string]string{}
	for _, line := range strings.Split(string(status), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if ok {
			fields[key] = strings.TrimSpace(value)
		}
	}
	nspid := strings.Fields(fields["NSpid"])
	uids := strings.Fields(fields["Uid"])
	if len(uids) == 0 {
		return p, fmt.Errorf("missing process owner")
	}
	p.UID, err = strconv.Atoi(uids[0])
	if err != nil {
		return p, err
	}
	p.Init = len(nspid) > 1 && nspid[len(nspid)-1] == "1"
	p.Restricted = fields["NoNewPrivs"] == "1" && fields["Seccomp"] == "2"
	for name, dest := range map[string]*string{"mnt": &p.MountNS, "pid": &p.PIDNS, "user": &p.UserNS} {
		value, err := os.Readlink(filepath.Join(dir, "ns", name))
		if err != nil {
			return p, err
		}
		*dest = value
	}
	return p, nil
}

func nativeAncestor(root string, p nativeProcess) (nativeProcess, bool) {
	for depth := 0; depth < 12 && p.Parent > 1; depth++ {
		parent, err := processInfo(root, p.Parent, false)
		if err != nil {
			return nativeProcess{}, false
		}
		exe, err := os.Readlink(filepath.Join(root, strconv.Itoa(parent.PID), "exe"))
		if err == nil && filepath.Base(strings.TrimSuffix(exe, " (deleted)")) == "codex" {
			return parent, true
		}
		p = parent
	}
	return nativeProcess{}, false
}

func namespaceKey(p nativeProcess) string { return p.MountNS + "|" + p.PIDNS + "|" + p.UserNS }

type nativeContext struct {
	Home, CodexHome, Thread, Call string
}

func nativeCommandDigest(argv []string) string {
	// The exec-server's verified arg0 trampoline execs argv[3] with argv[2]
	// as its custom argv[0]. Match that command, not the bootstrap executable.
	if len(argv) >= 4 && filepath.Base(argv[0]) == "codex" && argv[1] == "--codex-run-as-arg0-exec-helper" {
		command := append([]string(nil), argv[3:]...)
		command[0] = argv[2]
		return argvHash(command)
	}
	return argvHash(argv)
}

func processContext(root string, pid int) nativeContext {
	var result nativeContext
	env, err := readBounded(filepath.Join(root, strconv.Itoa(pid), "environ"), 1<<20)
	if err != nil {
		return result
	}
	for _, item := range strings.Split(string(env), "\x00") {
		key, value, ok := strings.Cut(item, "=")
		if !ok || len(value) > 4096 || strings.ContainsAny(value, "\r\n\x00") {
			continue
		}
		switch key {
		case "HOME":
			result.Home = value
		case "CODEX_HOME":
			result.CodexHome = value
		case "CODEX_THREAD_ID":
			if len(value) <= 256 {
				result.Thread = value
			}
		case "CODEX_TOOL_CALL_ID":
			if len(value) <= 256 {
				result.Call = value
			}
		}
	}
	return result
}

// Some launch paths initialize the namespace before injecting command metadata.
// Read only the direct command child, with matching UID, ancestry, argv digest
// and all namespace identities. Unrelated descendants cannot supply context.
func commandContext(root string, init nativeProcess, commandHash string, initial nativeContext) (nativeContext, bool) {
	if initial.Thread != "" && initial.Call != "" {
		return initial, false
	}
	raw, err := readBounded(filepath.Join(root, strconv.Itoa(init.PID), "task", strconv.Itoa(init.PID), "children"), 4096)
	if err != nil {
		return initial, false
	}
	children := strings.Fields(string(raw))
	if len(children) > 32 {
		return initial, true
	}
	result := initial
	for _, child := range children {
		pid, err := strconv.Atoi(child)
		if err != nil {
			continue
		}
		p, err := processInfo(root, pid, true)
		if err != nil || p.UID != init.UID || p.Parent != init.PID || namespaceKey(p) != namespaceKey(init) || argvHash(p.Args) != commandHash {
			continue
		}
		start, err := strconv.ParseUint(p.Start, 10, 64)
		parentStart, parentErr := strconv.ParseUint(init.Start, 10, 64)
		if err != nil || parentErr != nil || start < parentStart {
			continue
		}
		ctx := processContext(root, pid)
		if ctx.Thread == "" || ctx.Call == "" {
			continue
		}
		current, err := processInfo(root, pid, false)
		if err != nil || current.Start != p.Start || current.Parent != init.PID {
			continue
		}
		if result.Thread != "" && ctx.Thread != result.Thread || result.Call != "" && ctx.Call != result.Call {
			return nativeContext{Home: initial.Home, CodexHome: initial.CodexHome}, true
		}
		result.Thread, result.Call = ctx.Thread, ctx.Call
	}
	return result, false
}

// scanNative identifies the actual Codex sandbox namespace-init, not arbitrary
// descendants or a configured permission mode. No command/environment is saved.
func scanNative(root, boot string, codexHomes ...string) ([]nativeInstance, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	baseline, err := os.Readlink(filepath.Join(root, "self", "ns", "mnt"))
	if err != nil {
		return nil, err
	}
	var found []nativeInstance
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		comm, err := readBounded(filepath.Join(dir, "comm"), 128)
		if err != nil || strings.TrimSpace(string(comm)) != "codex" {
			continue
		}
		p, err := processInfo(root, pid, true)
		if err != nil || p.UID != os.Getuid() || len(p.Args) == 0 || p.Args[0] != "codex-linux-sandbox" || !p.Init || !p.Restricted || p.MountNS == baseline {
			continue
		}
		exe, err := os.Readlink(filepath.Join(dir, "exe"))
		if err != nil || filepath.Base(strings.TrimSuffix(exe, " (deleted)")) != "codex" {
			continue
		}
		ancestor, ok := nativeAncestor(root, p)
		if !ok {
			continue
		}
		split := -1
		for i, arg := range p.Args {
			if arg == "--" {
				split = i
				break
			}
		}
		if split < 0 || split+1 >= len(p.Args) {
			continue
		}
		ctx := processContext(root, pid)
		if len(codexHomes) > 0 {
			if ctx.CodexHome == "" && ctx.Home != "" {
				ctx.CodexHome = filepath.Join(ctx.Home, ".codex")
			}
			if ctx.CodexHome == "" || filepath.Clean(ctx.CodexHome) != filepath.Clean(codexHomes[0]) {
				continue
			}
		}
		commandHash := nativeCommandDigest(p.Args[split+1:])
		ctx, conflict := commandContext(root, p, commandHash, ctx)
		current, err := processInfo(root, pid, false)
		if err != nil || current.Start != p.Start {
			continue
		}
		id := digest("codex-native-linux", boot, strconv.Itoa(pid), p.Start, namespaceKey(p))
		found = append(found, nativeInstance{ID: id, ExecutorID: "linux:" + digest(boot, strconv.Itoa(ancestor.PID), ancestor.Start), PID: pid, StartTicks: p.Start, KernelKey: namespaceKey(p), ThreadID: ctx.Thread, ToolCallID: ctx.Call, ContextConflict: conflict, CommandHash: commandHash})
	}
	return found, nil
}

func nativeAlive(root string, instance nativeInstance) (bool, error) {
	p, err := processInfo(root, instance.PID, false)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if p.Start != instance.StartTicks || p.State == "Z" || p.State == "X" {
		return false, nil
	}
	parts := strings.Split(instance.KernelKey, "|")
	if len(parts) != 3 {
		return false, fmt.Errorf("invalid native namespace identity")
	}
	for name, want := range map[string]string{"mnt": parts[0], "pid": parts[1], "user": parts[2]} {
		value, err := os.Readlink(filepath.Join(root, strconv.Itoa(instance.PID), "ns", name))
		if err != nil {
			return false, err
		}
		if value != want {
			return false, fmt.Errorf("native namespace identity changed")
		}
	}
	return true, nil
}

type observerStatus struct {
	PID        int
	StartTicks string
	Updated    time.Time
	Scope      string
}

func observerStatusPath(store Store) string {
	return filepath.Join(store.Root, "native", "observer.json")
}

func observerReady(store Store) bool {
	body, err := readBounded(observerStatusPath(store), 4096)
	if err != nil {
		return false
	}
	var status observerStatus
	if json.Unmarshal(body, &status) != nil || time.Since(status.Updated) > 5*time.Second {
		return false
	}
	p, err := processInfo("/proc", status.PID, false)
	return err == nil && p.Start == status.StartTicks && p.State != "Z"
}

// EnsureNative starts a singleton detached observer and waits only for its local
// ready marker. It never waits for network I/O and never starts when disabled.
func EnsureNative(cfg config.Config, executable string) error {
	if !Configured(cfg) {
		return nil
	}
	store := NewStore(cfg)
	if observerReady(store) {
		return nil
	}
	if executable == "" {
		var err error
		executable, err = os.Executable()
		if err != nil {
			return err
		}
	}
	null, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	defer null.Close()
	command := exec.Command(executable, "internal", "codex-sandbox", "--observe")
	command.Stdin, command.Stdout, command.Stderr = null, null, null
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err
	}
	_ = command.Process.Release()
	deadline := time.Now().Add(400 * time.Millisecond)
	for time.Now().Before(deadline) {
		if observerReady(store) {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("native observer startup deferred")
}

// RunNative observes Linux PID-namespace sandbox instances while delivery runs
// independently. flock is released by the kernel on crashes, allowing recovery.
func RunNative(ctx context.Context, cfg config.Config) error {
	if !Configured(cfg) {
		return nil
	}
	store := NewStore(cfg)
	dir := filepath.Join(store.Root, "native")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "observer.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil
		}
		return err
	}
	defer syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
	boot, err := readBounded("/proc/sys/kernel/random/boot_id", 128)
	if err != nil {
		return err
	}
	own, err := processInfo("/proc", os.Getpid(), false)
	if err != nil {
		return err
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	image, err := os.Stat("/proc/self/exe")
	if err != nil {
		return err
	}
	publish := func() error {
		b, _ := json.Marshal(observerStatus{PID: own.PID, StartTicks: own.Start, Updated: time.Now().UTC(), Scope: Scope(cfg)})
		return journal.WriteAtomic(observerStatusPath(store), b)
	}
	if err := publish(); err != nil {
		return err
	}
	defer os.Remove(observerStatusPath(store))
	_ = hooklog.Append(cfg.HookLogFile, "native sandbox observer started", map[string]any{"platform": "linux", "source": Source})
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	deliveryStore, deliveryConfig := store, cfg
	go func() {
		defer close(done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		lastRecovery := time.Time{}
		for {
			current, err := config.Resolve(config.ResolveOptions{})
			if err != nil || !Configured(current) || Scope(current) != Scope(deliveryConfig) || current.StateDir != deliveryConfig.StateDir {
				return
			}
			deliveryConfig, deliveryStore = current, NewStore(current)
			if time.Since(lastRecovery) >= 15*time.Second {
				if err := recoverBindings(deliveryConfig, deliveryStore); err != nil {
					if detail := CollectionDiagnostic(err, "binding_recovery", deliveryConfig.Debug); detail != nil {
						_ = hooklog.Append(deliveryConfig.HookLogFile, "native sandbox binding recovery deferred", detail)
					}
				}
				lastRecovery = time.Now()
			}
			_ = deliveryStore.Flush(workerCtx, NewClient(deliveryConfig, nil))
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	defer func() { cancel(); <-done }()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	lastCheck := time.Now()
	lastWarning := time.Time{}
	retryAfter := time.Time{}
	idleSince := time.Now()
	lastReconcile := time.Time{}
	lastScan := time.Time{}
	lastSignature := ""
	var nativeModified time.Time
	fastUntil := time.Now().Add(2 * time.Second)
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	codexHome := agentfiles.CodexHome(home)
	lastHookCheck := time.Time{}
	lastHookHealth := ""
	for {
		if time.Since(lastHookCheck) >= time.Minute {
			missing, err := nativeMissingHooks(codexHome)
			health := strings.Join(missing, ",")
			if err != nil {
				health = "unreadable"
			}
			if health != lastHookHealth {
				if err != nil {
					_ = hooklog.Append(cfg.HookLogFile, "native sandbox Hook registration unavailable", map[string]any{"category": errorCategory(err), "phase": "hook_health"})
				} else if len(missing) > 0 {
					_ = hooklog.Append(cfg.HookLogFile, "native sandbox Hook registration missing", map[string]any{"category": "hook_registration_missing", "missing_events": missing, "recovery": "obs-agent-connector update codex"})
				} else {
					_ = hooklog.Append(cfg.HookLogFile, "native sandbox Hook registration restored", nil)
				}
				lastHookHealth = health
			}
			lastHookCheck = time.Now()
		}
		if time.Since(lastCheck) >= time.Second {
			currentImage, err := os.Stat(executable)
			if err != nil || !os.SameFile(image, currentImage) {
				return nil
			}
			current, err := config.Resolve(config.ResolveOptions{})
			if err != nil || !Configured(current) || Scope(current) != Scope(cfg) || current.StateDir != cfg.StateDir {
				return nil
			}
			cfg, store = current, NewStore(current)
			_ = publish()
			lastCheck = time.Now()
		}
		modified := time.Time{}
		if info, err := os.Stat(filepath.Join(dir, "journal.json")); err == nil {
			modified = info.ModTime()
		}
		hookChanged := !modified.Equal(nativeModified)
		if hookChanged {
			fastUntil = time.Now().Add(2 * time.Second)
			idleSince = time.Now()
		}
		interval := 100 * time.Millisecond
		if time.Now().Before(fastUntil) {
			interval = 20 * time.Millisecond
		}
		if time.Since(lastScan) < interval {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
			}
			continue
		}
		lastScan = time.Now()
		phase := "proc_scan"
		observed, scanErr := scanNative("/proc", strings.TrimSpace(string(boot)), codexHome)
		if len(observed) > 0 {
			idleSince = time.Now()
		}
		if time.Since(idleSince) > 30*time.Minute {
			return nil
		}
		ids := make([]string, 0, len(observed))
		for _, instance := range observed {
			ids = append(ids, digest(instance.ID, instance.ThreadID, instance.ToolCallID, strconv.FormatBool(instance.ContextConflict)))
		}
		sort.Strings(ids)
		signature := digest(ids...)
		if scanErr == nil && time.Now().After(retryAfter) && (signature != lastSignature || hookChanged || time.Since(lastReconcile) >= time.Minute) {
			phase = "journal_reconcile"
			scanErr = reconcileNative(store, observed, func(i nativeInstance) (bool, error) { return nativeAlive("/proc", i) })
			if scanErr == nil {
				lastSignature, lastReconcile = signature, time.Now()
			} else {
				retryAfter = time.Now().Add(time.Second)
			}
		}
		if info, err := os.Stat(filepath.Join(dir, "journal.json")); err == nil {
			nativeModified = info.ModTime()
		}
		if scanErr != nil && time.Since(lastWarning) >= time.Minute {
			if detail := CollectionDiagnostic(scanErr, phase, cfg.Debug); detail != nil {
				_ = hooklog.Append(cfg.HookLogFile, "native sandbox observation deferred", detail)
				lastWarning = time.Now()
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}
