package main

import (
	"testing"
	"time"
)

func TestParseAllowlist(t *testing.T) {
	set := parseAllowlist("# comment\nf9129044-e1f3-4ff4-862a-fe4dcb1cdbda  # %8\n\n  12444BC8-4325   \nshort\n")
	if len(set) != 2 || !allowed(set, "F9129044-e1f3-4ff4-862a-fe4dcb1cdbda") || !allowed(set, "12444bc8-4325") {
		t.Fatalf("parse: %+v", set)
	}
	if allowed(set, "") || allowed(set, "89444ffe-work-session") || allowed(map[string]bool{}, "f9129044-e1f3") {
		t.Fatal("fail-closed violated")
	}
}

func TestPickReroutesHonoursAllowlist(t *testing.T) {
	now := time.Date(2026, 11, 24, 19, 0, 0, 0, time.UTC)
	old := now.Add(-3 * time.Hour).Format(time.RFC3339)
	items := []workItem{
		{ID: 100, Text: "claim", Status: "done", By: "%6"},
		{ID: 101, Text: "[verify #100] falsify", Status: "todo", By: "auto", Assignee: "conductor", TS: old},
	}
	// a NAMED, IDLE work session that never opted in must never be picked
	minds := []mindView{
		{Name: "work", Pane: "%29", UUID: "89444ffe-608b", State: "idle"},
		{Name: "pmf", Pane: "%13", UUID: "e125a3b2-1b87", State: "idle"},
	}
	set := parseAllowlist("e125a3b2-1b87\n")
	got := pickReroutes(items, filterAllowed(minds, set), now, time.Hour, nil, nil)
	if len(got) != 1 || got[0].To != "%13" {
		t.Fatalf("only the opted-in mind may receive: %+v", got)
	}
	if r := pickReroutes(items, filterAllowed(minds, map[string]bool{}), now, time.Hour, nil, nil); len(r) != 0 {
		t.Fatalf("empty registry -> silence, got %+v", r)
	}
}
