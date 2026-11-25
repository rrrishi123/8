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
