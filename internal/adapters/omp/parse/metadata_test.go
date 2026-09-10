package parse

import (
	"github.com/GuanceCloud/obs-agent-connector/internal/core/semantic"
	"testing"
)

func TestMetadataSurvivesContentSuppression(t *testing.T) {
	cfg := testConfig(t)
	cfg.CaptureContent = "none"
	s := fixtureSnapshot(t)
	length := 1234
	for i := range s.Events {
		e := &s.Events[i]
		e.Message.HasContent = true
		e.Message.HasText = e.Type == "assistant"
		e.Message.TextLength = &length
		e.Message.Content = nil
		e.Args, e.Result = nil, nil
		if e.Type == "tool_start" {
			e.Skill = &SkillIdentity{Name: "example", Source: "workspace"}
		}
	}
	turn, err := Normalize(s, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if turn.ToolCalls[0].Skill == nil || turn.ToolCalls[0].Skill.Status != "completed" {
		t.Fatal("content suppression removed skill identity/status")
	}
	if turn.InputLength != length || turn.OutputLength != length {
		t.Fatal("raw lengths lost")
	}
	spans := (semantic.Builder{}).Build(turn)
	for _, sp := range spans {
		if sp.Name == "invoke_agent" && (sp.Attributes["gen_ai.provider.name"] != "test" || sp.Attributes["gen_ai.response.model"] != "test-model") {
			t.Fatal("root model summary absent")
		}
		if sp.Name == "llm" || sp.Name == "assistant" || sp.Name == "invoke_agent" {
			if sp.Attributes["output_length"] != length || sp.Attributes["gen_ai.output.type"] != "text" {
				t.Fatalf("length/type missing: %s", sp.Name)
			}
			if sp.Attributes["gen_ai.output.messages"] != nil {
				t.Fatal("content leaked")
			}
		}
	}
}

func TestSkillMetadataAndFinalOutput(t *testing.T) {
	d, v := skillMetadata("---\ndescription: 'A skill'\nversion: \"1.2\"\n---\nbody", 20000)
	if d != "A skill" || v != "1.2" {
		t.Fatalf("metadata %q %q", d, v)
	}
	d, v = skillMetadata("---\ndescription: |\n  unsupported\nversion: [bad]\n---", 20000)
	if d != "" || v != "" {
		t.Fatal("guessed unsupported YAML")
	}
	s := fixtureSnapshot(t)
	last := s.Events[len(s.Events)-1]
	last.Message.Timestamp++
	last.Message.Content = nil
	last.Message.StopReason = "error"
	s.Events = append(s.Events, last)
	turn, err := Normalize(s, testConfig(t))
	if err != nil {
		t.Fatal(err)
	}
	if turn.OutputMessages != nil || turn.OutputLength != 0 {
		t.Fatal("intermediate response exported as final output")
	}
}
