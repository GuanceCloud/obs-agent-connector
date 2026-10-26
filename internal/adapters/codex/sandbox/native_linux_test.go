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
	files := map[string]string{"stat": fmt.Sprintf("%d (codex) %s", pid, strings.Join(fields, " ")), "comm": "codex\n", "status": "Uid:\t" + strconv.Itoa(os.Getuid()) + "\nNoNewPrivs:\t1\nSeccomp:\t2\nNSpid:\t" + strconv.Itoa(pid) + " 1\n", "cmdline": argv0 + "\x00--\x00/bin/sh\x00-c\x00sleep 2\x00", "environ": "X_TOKEN=never-persist-this\x00CODEX_THREAD_ID=session\x00"}
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
