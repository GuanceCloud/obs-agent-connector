package agent

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

func isolateMimo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	for _, key := range []string{"MIMOCODE_HOME", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "OPENCODE_HOME", "MIMOCODE_CONFIG_DIR"} {
		t.Setenv(key, "")
	}
	return home
}

func TestMimoPaths(t *testing.T) {
	home := isolateMimo(t)
	for _, tc := range []struct {
		name, root, config, data, wantConfig, wantData string
		invalid                                        bool
	}{
		{name: "default", wantConfig: filepath.Join(home, ".config", "mimocode"), wantData: filepath.Join(home, ".local", "share", "mimocode")},
		{name: "xdg", config: filepath.Join(home, "xdg-config"), data: filepath.Join(home, "xdg-data"), wantConfig: filepath.Join(home, "xdg-config", "mimocode"), wantData: filepath.Join(home, "xdg-data", "mimocode")},
		{name: "root precedence", root: filepath.Join(home, "custom"), config: filepath.Join(home, "ignored"), wantConfig: filepath.Join(home, "custom", "config"), wantData: filepath.Join(home, "custom", "data")},
		{name: "invalid root", root: "relative", invalid: true},
		{name: "relative XDG ignored", config: "relative", data: "relative", wantConfig: filepath.Join(home, ".config", "mimocode"), wantData: filepath.Join(home, ".local", "share", "mimocode")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MIMOCODE_HOME", tc.root)
			t.Setenv("XDG_CONFIG_HOME", tc.config)
			t.Setenv("XDG_DATA_HOME", tc.data)
			c, d, err := mimoPaths()
			if tc.invalid {
				if err == nil {
					t.Fatal("relative home accepted")
				}
				if _, err := resolveMimoForInstall(Get("mimo")); err == nil {
					t.Fatal("install accepted invalid home")
				}
				return
			}
			if err != nil || c != tc.wantConfig || d != tc.wantData {
				t.Fatalf("paths = %q %q %v", c, d, err)
			}
		})
	}
}

func TestMimoDiscoveryIsolation(t *testing.T) {
	home := isolateMimo(t)
	t.Setenv("PATH", t.TempDir())
	open := filepath.Join(home, ".config", "opencode")
	os.MkdirAll(open, 0755)
	t.Setenv("OPENCODE_HOME", open)
	root := filepath.Join(home, "custom")
	os.MkdirAll(root, 0755)
	t.Setenv("MIMOCODE_HOME", root)
	if _, ok := resolveMimoForDiscovery(Get("mimo")); ok {
		t.Fatal("empty custom root or OpenCode detected as MiMo")
	}
	// A data directory is enough even when config does not yet exist.
	os.MkdirAll(filepath.Join(root, "data"), 0755)
	p, ok := resolveMimoForDiscovery(Get("mimo"))
	if !ok {
		t.Fatal("data directory not discovered")
	}
	if p.ConfigFiles[0] != filepath.Join(root, "config", "gtrace.json") {
		t.Fatal(p.ConfigFiles)
	}
	t.Setenv("MIMOCODE_HOME", "relative")
	if _, ok := resolveMimoForDiscovery(Get("mimo")); ok {
		t.Fatal("invalid root discovered")
	}
}

func TestMimoDefinitionContract(t *testing.T) {
	home := isolateMimo(t)
	t.Setenv("MIMOCODE_CONFIG_DIR", filepath.Join(home, "ambient-ignored"))
	p, err := resolveMimoForInstall(Get("mimo"))
	if err != nil {
		t.Fatal(err)
	}
	if p.PluginName != "opencode-otel-plugin" || !reflect.DeepEqual(p.InstallArgs, []string{"--variant", "mimo"}) || !reflect.DeepEqual(p.WindowsArgs, []string{"-Variant", "mimo"}) {
		t.Fatalf("wrong contract: %+v", p)
	}
	if !reflect.DeepEqual(p.Env, []string{"MIMOCODE_CONFIG_DIR=" + filepath.Join(home, ".config", "mimocode")}) {
		t.Fatal(p.Env)
	}
	for _, platform := range []string{"darwin", "linux", "windows"} {
		if !SupportsPlatform(p, platform) {
			t.Fatal(platform)
		}
	}
	if err := removeMimoRegistration(p); err != nil {
		t.Fatal(err)
	}
	os.MkdirAll(p.Markers[0], 0755)
	if err := removeMimoRegistration(p); err == nil {
		t.Fatal("missing helper must block removal")
	}
	if !PathExists(p.Markers[0]) {
		t.Fatal("plugin removed before unregister")
	}
}

func TestMimoDiscoveryByCommand(t *testing.T) {
	home := isolateMimo(t)
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0755); err != nil {
		t.Fatal(err)
	}
	name := "mimo"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bin, name), []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	if _, ok := resolveMimoForDiscovery(Get("mimo")); !ok {
		t.Fatal("MiMo executable not discovered without config/data directories")
	}
}
