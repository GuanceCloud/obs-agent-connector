package checkpoint

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnrichWithoutDatabase(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("DCODE_OTEL_PYTHON", "/missing/python")
	if got := Enrich("session", "original.jsonl"); got != "original.jsonl" {
		t.Fatal(got)
	}
}

// Run with DCODE_TEST_PYTHON pointing at DCode's virtualenv interpreter.
func TestEnrichWithDcodePython(t *testing.T) {
	python := os.Getenv("DCODE_TEST_PYTHON")
	if python == "" {
		t.Skip("DCode Python not configured")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("DCODE_OTEL_PYTHON", python)
	fixture := `import sqlite3,pathlib
from langchain_core.messages import HumanMessage,AIMessage,ToolMessage
from langgraph.checkpoint.serde.jsonplus import JsonPlusSerializer
p=pathlib.Path.home()/'.deepagents/.state'
p.mkdir(parents=True)
with sqlite3.connect(p/'sessions.db') as db:
 db.execute('CREATE TABLE writes(thread_id TEXT, checkpoint_ns TEXT, channel TEXT, checkpoint_id TEXT, task_id TEXT, idx INTEGER, type TEXT, value BLOB)')
 messages=[HumanMessage(id='u',content='read'),AIMessage(id='a',content='',tool_calls=[{'id':'t','name':'Read','args':{'path':'/file','API_KEY':'secret-value'}}],usage_metadata={'input_tokens':10,'output_tokens':2,'total_tokens':12}),ToolMessage(id='r',content='result',tool_call_id='t',name='Read')]
 for i,msg in enumerate(messages+[messages[-1]]):
  kind,body=JsonPlusSerializer().dumps_typed([msg])
  db.execute('INSERT INTO writes VALUES(?,?,?,?,?,?,?,?)',('s','','messages',str(i),'task',0,kind,body))
`
	if out, err := exec.Command(python, "-c", fixture).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	original := filepath.Join(home, "transcript.jsonl")
	if err := os.WriteFile(original, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	enriched := Enrich("s", original)
	if enriched == original {
		t.Fatal("checkpoint enrichment did not run")
	}
	content, err := os.ReadFile(enriched)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 3 || strings.Contains(string(content), "secret-value") {
		t.Fatalf("dedup/privacy failure")
	}
	var assistant, tool map[string]any
	if json.Unmarshal([]byte(lines[1]), &assistant) != nil || json.Unmarshal([]byte(lines[2]), &tool) != nil {
		t.Fatal("invalid JSON")
	}
	if assistant["usage_metadata"].(map[string]any)["input_tokens"] != float64(10) || tool["tool_call_id"] != "t" {
		t.Fatal("metadata lost")
	}
	if Enrich("s", enriched) != enriched {
		t.Fatal("path is not idempotent")
	}
	after, _ := os.ReadFile(enriched)
	if string(after) != string(content) {
		t.Fatal("replay changed content")
	}
	info, _ := os.Stat(enriched)
	if info.Mode().Perm() != 0600 {
		t.Fatal("unsafe permissions")
	}
	body, _ := os.ReadFile(original)
	if string(body) != "original" {
		t.Fatal("overwrote original")
	}
	t.Setenv("DCODE_OTEL_PYTHON", filepath.Join(home, "missing-python"))
	if Enrich("s", original) != original {
		t.Fatal("must fail open")
	}
}
