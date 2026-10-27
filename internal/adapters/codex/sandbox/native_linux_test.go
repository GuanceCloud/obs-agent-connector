//go:build linux

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func fakeNativeProcess(t *testing.T, root string, pid, parent int, start, argv0, ns string) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(filepath.Join(dir, "ns"), 0700); err != nil {
		t.Fatal(err)
	}
	fields := make([]string, 20)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0], fields[1], fields[19] = "S", strconv.Itoa(parent), start
	files := map[string]string{"stat": fmt.Sprintf("%d (codex) %s", pid, strings.Join(fields, " ")), "comm": "codex\n", "status": "Uid:\t" + strconv.Itoa(os.Getuid()) + "\nNoNewPrivs:\t1\nSeccomp:\t2\nNSpid:\t" + strconv.Itoa(pid) + " 1\n", "cmdline": argv0 + "\x00--\x00/bin/sh\x00-c\x00sleep 2\x00", "environ": "X_TOKEN=never-persist-this\x00CODEX_THREAD_ID=session\x00HOME=/test-home\x00CODEX_HOME=/test-codex\x00"}
	for name, value := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"mnt", "pid", "user"} {
		if err := os.Symlink(name+":"+ns, filepath.Join(dir, "ns", name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("/opt/codex", filepath.Join(dir, "exe")); err != nil {
		t.Fatal(err)
	}
}

func TestLinuxNativeRequiresKernelIsolationAndRealCodexInit(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "self", "ns"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("mnt:host", filepath.Join(root, "self", "ns", "mnt")); err != nil {
		t.Fatal(err)
	}
	fakeNativeProcess(t, root, 100, 1, "10", "codex", "host")
	fakeNativeProcess(t, root, 101, 100, "20", "codex-linux-sandbox", "sandbox")
	fakeNativeProcess(t, root, 102, 100, "21", "/bin/bash", "sandbox")
	found, err := scanNative(root, "boot")
	if err != nil || len(found) != 1 {
		t.Fatalf("observations=%d err=%v", len(found), err)
	}
	instance := found[0]
	for _, tc := range []struct {
		home string
		want int
	}{{"/test-codex", 1}, {"/other-codex", 0}} {
		matched, err := scanNative(root, "boot", tc.home)
		if err != nil || len(matched) != tc.want {
			t.Fatalf("Codex home scope %q: count=%d err=%v", tc.home, len(matched), err)
		}
	}
	if instance.PID != 101 || instance.ThreadID != "session" || instance.CommandHash != digest("shell", "sleep 2") || len(instance.ID) != 64 {
		t.Fatal("native identity or command matching differs")
	}
	if ok, err := nativeAlive(root, instance); !ok || err != nil {
		t.Fatalf("alive=%v err=%v", ok, err)
	}
	// Changing a reused PID's start ticks closes only the old instance.
	p := filepath.Join(root, "101", "stat")
	body, _ := os.ReadFile(p)
	body = []byte(strings.TrimSuffix(string(body), "20") + "999")
	if err := os.WriteFile(p, body, 0600); err != nil {
		t.Fatal(err)
	}
	if ok, err := nativeAlive(root, instance); ok || err != nil {
		t.Fatal("PID reuse kept the original instance alive")
	}
	// A permission mode, thread env, argv label, or namespace alone is insufficient.
	if err := os.WriteFile(filepath.Join(root, "101", "status"), []byte("Uid:\t"+strconv.Itoa(os.Getuid())+"\nNSpid:\t101 1\nNoNewPrivs:\t0\nSeccomp:\t0\n"), 0600); err != nil {
		t.Fatal(err)
	}
	found, err = scanNative(root, "boot")
	if err != nil || len(found) != 0 {
		t.Fatal("observed an unenforced sandbox")
	}
}

func TestNativeCommandChildContextRequiresExactIsolationAndCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "self", "ns"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("mnt:host", filepath.Join(root, "self", "ns", "mnt")); err != nil {
		t.Fatal(err)
	}
	fakeNativeProcess(t, root, 100, 1, "10", "codex", "host")
	fakeNativeProcess(t, root, 101, 100, "20", "codex-linux-sandbox", "sandbox")
	fakeNativeProcess(t, root, 102, 101, "21", "/bin/sh", "sandbox")
	write := func(pid int, name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, strconv.Itoa(pid), name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(101, "environ", "HOME=/test-home\x00CODEX_HOME=/test-codex\x00")
	if err := os.MkdirAll(filepath.Join(root, "101", "task", "101"), 0700); err != nil {
		t.Fatal(err)
	}
	write(101, "task/101/children", "102")
	write(102, "cmdline", "/bin/sh\x00-c\x00sleep 2\x00")
	write(102, "environ", "CODEX_THREAD_ID=session\x00CODEX_TOOL_CALL_ID=call\x00SECRET=never-save\x00")
	get := func() nativeInstance {
		t.Helper()
		found, err := scanNative(root, "boot", "/test-codex")
		if err != nil || len(found) != 1 {
			t.Fatalf("scan count=%d err=%v", len(found), err)
		}
		return found[0]
	}
	if i := get(); i.ThreadID != "session" || i.ToolCallID != "call" || i.ContextConflict {
		t.Fatal("did not recover command child context")
	}
	write(101, "cmdline", "codex-linux-sandbox\x00--\x00/opt/codex\x00--codex-run-as-arg0-exec-helper\x00sh\x00/bin/sh\x00-c\x00sleep 2\x00")
	if i := get(); i.ThreadID != "session" || i.ToolCallID != "call" {
		t.Fatal("exec-server arg0 trampoline hid the command context")
	}
	write(102, "cmdline", "/bin/sh\x00-c\x00different\x00")
	if i := get(); i.ThreadID != "" || i.ToolCallID != "" {
		t.Fatal("unrelated command supplied a Tool identity")
	}
	write(102, "cmdline", "/bin/sh\x00-c\x00sleep 2\x00")
	if err := os.Remove(filepath.Join(root, "102", "ns", "pid")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("pid:other", filepath.Join(root, "102", "ns", "pid")); err != nil {
		t.Fatal(err)
	}
	if i := get(); i.ThreadID != "" {
		t.Fatal("another namespace supplied context")
	}
	if err := os.Remove(filepath.Join(root, "102", "ns", "pid")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("pid:sandbox", filepath.Join(root, "102", "ns", "pid")); err != nil {
		t.Fatal(err)
	}
	fakeNativeProcess(t, root, 103, 101, "22", "/bin/sh", "sandbox")
	write(103, "cmdline", "/bin/sh\x00-c\x00sleep 2\x00")
	write(103, "environ", "CODEX_THREAD_ID=other-session\x00CODEX_TOOL_CALL_ID=other-call\x00")
	write(101, "task/101/children", "102 103")
	if i := get(); !i.ContextConflict || i.ThreadID != "" {
		t.Fatal("conflicting command contexts were guessed")
	}
}
