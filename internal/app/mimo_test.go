package app

import (
	"github.com/GuanceCloud/obs-agent-connector/internal/agent"
	"strings"
	"testing"
)

func TestMimoInstallerContract(t *testing.T) {
	p := agent.Resolve(agent.Get("mimo"))
	input := installInput{Endpoint: "https://example.test", XToken: "test-token"}
	for name, args := range map[string][]string{
		"install":         buildInstallArgs("install.sh", p, input),
		"package install": buildPackageInstallArgs("/tmp/package", p, input),
		"update":          buildPluginUpdateArgs("install.sh", p),
		"package update":  buildPackageUpdateArgs("/tmp/package", p),
	} {
		if !strings.Contains(strings.Join(args, " "), "--variant mimo") {
			t.Fatalf("%s: %v", name, args)
		}
	}
	for _, command := range []string{renderPowerShellInstallCommand("install.ps1", p, input), renderPowerShellUpdateCommand("install.ps1", p)} {
		if !strings.Contains(command, "-Variant 'mimo'") {
			t.Fatal(command)
		}
	}
	if !supportsEditableRuntimeConfig(p) {
		t.Fatal("MiMo runtime config must be editable")
	}
}
