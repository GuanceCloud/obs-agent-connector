package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/GuanceCloud/obs-agent-connector/internal/agent"
)

func TestMimoRemovalSafety(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("Node.js required by external plugin cleanup")
	}
	for _, tc := range []struct {
		name        string
		fail, purge bool
	}{
		{name: "failed helper preserves files", fail: true, purge: true},
		{name: "default preserves config"},
		{name: "purge removes telemetry only", purge: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("MIMOCODE_HOME", root)
			p := agent.Resolve(agent.Get("mimo"))
			plugin := p.Markers[0]
			if err := os.MkdirAll(filepath.Join(plugin, "scripts"), 0755); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(root, "config")
			script := `import fs from "node:fs"; import path from "node:path";
if (process.argv[2] !== "remove-mimo-config" || !fs.existsSync(path.join(process.argv[3], "gtrace.json"))) process.exit(2);
`
			if tc.fail {
				script += "process.exit(3);"
			}
			if err := os.WriteFile(filepath.Join(plugin, "scripts", "install-config.mjs"), []byte(script), 0644); err != nil {
				t.Fatal(err)
			}
			for _, file := range []string{"gtrace.json", "mimocode.json", "auth.json"} {
				if err := os.WriteFile(filepath.Join(config, file), []byte("{}"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			err := removeOne(p, tc.purge)
			if (err != nil) != tc.fail {
				t.Fatalf("remove error = %v, want failure %v", err, tc.fail)
			}
			if agent.PathExists(plugin) != tc.fail {
				t.Fatal("plugin deletion did not respect helper result")
			}
			if agent.PathExists(p.ConfigFiles[0]) != (tc.fail || !tc.purge) {
				t.Fatal("telemetry config retention mismatch")
			}
			for _, file := range []string{"mimocode.json", "auth.json"} {
				data, err := os.ReadFile(filepath.Join(config, file))
				if err != nil || string(data) != "{}" {
					t.Fatalf("unrelated file %s changed: %v", file, err)
				}
			}
		})
	}
}
