package journal

import (
	"errors"
	"testing"
)

func TestFailedOrOversizedTransactionsDoNotReplaceCommittedData(t *testing.T) {
	type payload struct{ Value string }
	file := File[payload]{Root: t.TempDir(), MaxBytes: 64}
	if err := file.Update(func(p *payload) error { p.Value = "committed"; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := file.Update(func(p *payload) error { p.Value = "uncommitted"; return errors.New("abort") }); err == nil {
		t.Fatal("transaction unexpectedly committed")
	}
	if err := file.Update(func(p *payload) error { p.Value = string(make([]byte, 128)); return nil }); err == nil {
		t.Fatal("byte limit not enforced")
	}
	if err := file.View(func(p *payload) error {
		if p.Value != "committed" {
			t.Fatal("lost last committed value")
		}
		p.Value = "read-only"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := file.View(func(p *payload) error {
		if p.Value != "committed" {
			t.Fatal("view mutated durable state")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
