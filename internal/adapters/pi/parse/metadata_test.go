package parse

import "testing"

func TestPiSkillMetadataUsesOnlyScalarFrontmatter(t *testing.T) {
	description, version := skillMetadata("---\ndescription: Example skill\nversion: '1.2.3'\n---\nBody", 100)
	if description != "Example skill" || version != "1.2.3" {
		t.Fatalf("unexpected metadata %q %q", description, version)
	}
	description, version = skillMetadata("---\ndescription: |\n  multiline\nversion: [1, 2]\n---", 100)
	if description != "" || version != "" {
		t.Fatal("ambiguous frontmatter was accepted")
	}
}
