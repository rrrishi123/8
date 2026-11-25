package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDurableWriteRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "x.json")
	if err := durableWrite(dir, p, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "hello" {
		t.Fatalf("got %q", b)
	}
	if err := durableWrite(dir, p, []byte("world")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "world" {
		t.Fatalf("overwrite got %q", b)
	}
}

func TestLedgerCorruptStateReadsDisk(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
	if lc, _ := ledgerCorruptState(); lc {
		t.Fatal("no file => not corrupt")
	}
	os.WriteFile(workFile(), []byte(`[{"id":1,`), 0o644) // torn
	if lc, _ := ledgerCorruptState(); !lc {
		t.Fatal("torn file => corrupt")
	}
	os.WriteFile(workFile(), []byte(`[]`), 0o644) // repaired
	if lc, _ := ledgerCorruptState(); lc {
		t.Fatal("repaired => self-resets to clean")
	}
}

func TestWriteWorkRefusesOverCorrupt(t *testing.T) { // the probe the pack ran against e6d83c0
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
	torn := []byte(`[{"id":1,"text":"keep"},{"id":2,`)
	os.WriteFile(workFile(), torn, 0o644)
	writeWork([]workItem{{ID: 3, Text: "new add", Kind: "task"}}) // reader saw [] and appended
	b, _ := os.ReadFile(workFile())
	if string(b) != string(torn) {
		t.Fatalf("non-parsing ledger was OVERWRITTEN (#18): %q", b)
	}
	if lc, _ := ledgerCorruptState(); !lc {
		t.Fatal("corrupt not reported")
	}
}

func TestWriteWorkRefusesEmptyOverNonEmpty(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
	writeWork([]workItem{{ID: 1, Text: "keep", Kind: "task"}, {ID: 2, Text: "me", Kind: "task"}})
	writeWork(nil)
	b, _ := os.ReadFile(workFile())
	var items []workItem
	if json.Unmarshal(b, &items) != nil || len(items) != 2 {
		t.Fatalf("empty write not refused: %d items", len(items))
	}
}
