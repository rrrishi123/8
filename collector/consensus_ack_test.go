package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// #53: "does everyone agree" made mechanical. A Need=N item self-closes when N
// distinct minds agree, de-dupes repeat acks, and a standing disagree holds it.
func TestConsensusAckCloses(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeWork([]workItem{{ID: 1, Text: "cut decision", Kind: "task", Status: "todo", Need: 2}})
	c := &collector{}
	post := func(body string) {
		c.handleWork(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/work", strings.NewReader(body)))
	}
	read := func() workItem {
		var items []workItem
		b, _ := os.ReadFile(workFile())
		json.Unmarshal(b, &items)
		for _, it := range items {
			if it.ID == 1 {
				return it
			}
		}
		t.Fatal("item 1 vanished")
		return workItem{}
	}

	post(`{"id":1,"ack":"agree","by":"%7"}`) // one agree: still open
	if it := read(); it.Status != "todo" || len(it.Acks) != 1 {
		t.Fatalf("1 agree: status=%q acks=%d, want todo/1", it.Status, len(it.Acks))
	}
	post(`{"id":1,"ack":"agree","by":"%7"}`) // same mind again: de-duped, no double-count
	if it := read(); it.Status != "todo" || len(it.Acks) != 1 {
		t.Fatalf("dup agree must not count twice: status=%q acks=%d", it.Status, len(it.Acks))
	}
	post(`{"id":1,"ack":"agree","by":"%9"}`) // second distinct agree reaches Need=2 -> self-close
	if it := read(); it.Status != "done" || it.FlippedBy != "consensus:2-ack" {
		t.Fatalf("consensus must self-close: status=%q flipped=%q", it.Status, it.FlippedBy)
	}
}

// A disagree HOLDS (blocked) even past the agree count; withdrawing it (the mind
// changes its verdict to agree) lets the item reach consensus and close.
func TestConsensusDisagreeHoldsThenClears(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeWork([]workItem{{ID: 1, Text: "d", Kind: "task", Status: "todo", Need: 2}})
	c := &collector{}
	post := func(body string) {
		c.handleWork(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/work", strings.NewReader(body)))
	}
	read := func() workItem {
		var items []workItem
		b, _ := os.ReadFile(workFile())
		json.Unmarshal(b, &items)
		return items[0]
	}
	post(`{"id":1,"ack":"agree","by":"%7"}`)
	post(`{"id":1,"ack":"disagree","by":"%9"}`) // standing disagree holds it
	if it := read(); it.Status != "blocked" {
		t.Fatalf("disagree must hold: status=%q", it.Status)
	}
	post(`{"id":1,"ack":"agree","by":"%9"}`) // %9 switches to agree -> 2 agrees, 0 disagree -> close
	if it := read(); it.Status != "done" {
		t.Fatalf("withdraw+agree must close: status=%q", it.Status)
	}
}

// #9713 (%8): one mind signing two ways must count ONCE. 'sibling-1' (bare) and
// 'sibling-1 (77777777)' are the same voter; keyed on the raw string they'd be
// two agrees and self-close a Need=2 item on one mind. ackKey resolves both to
// the uuid's first-8-hex (bare name via roles.json), so the item stays open.
func TestConsensusAckDedupesSpellings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(home+"/.8", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(home+"/.8/roles.json", []byte(`{"sibling-1":"77777777-0000-4000-8000-000000000077"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeWork([]workItem{{ID: 1, Text: "decision", Kind: "task", Status: "todo", Need: 2}})
	c := &collector{}
	post := func(body string) {
		c.handleWork(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/work", strings.NewReader(body)))
	}
	read := func() workItem {
		var items []workItem
		b, _ := os.ReadFile(workFile())
		json.Unmarshal(b, &items)
		return items[0]
	}
	// sanity: ackKey folds both spellings to the same key
	if ackKey("sibling-1") != ackKey("sibling-1 (77777777)") {
		t.Fatalf("ackKey must fold spellings: %q vs %q", ackKey("sibling-1"), ackKey("sibling-1 (77777777)"))
	}
	post(`{"id":1,"ack":"agree","by":"sibling-1"}`)
	post(`{"id":1,"ack":"agree","by":"sibling-1 (77777777)"}`) // SAME mind, 2nd spelling
	if it := read(); it.Status != "todo" || len(it.Acks) != 1 {
		t.Fatalf("one mind two spellings must stay 1 ack / todo: status=%q acks=%d", it.Status, len(it.Acks))
	}
	post(`{"id":1,"ack":"agree","by":"%9 (99999999)"}`) // a genuinely different mind -> now 2 -> closes
	if it := read(); it.Status != "done" {
		t.Fatalf("second distinct mind must close: status=%q", it.Status)
	}
}
