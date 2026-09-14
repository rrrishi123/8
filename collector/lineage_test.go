package main

import "testing"

// Replays 2026-09-14: tmux restored, every seat re-minted. Old boot A had
// %7=conductor, %9=higgs, %11=pmf, %0=host shell (no mind). New boot B seats
// conductor at %9 and higgs at %11; pmf hasn't resumed yet.
func TestRemintItems(t *testing.T) {
	l := &lineageFile{Boots: map[string]map[string]string{
		"A": {"%7": "u-conductor", "%9": "u-higgs", "%11": "u-pmf"},
	}, Seen: map[string]string{}}
	live := map[string]string{"u-conductor": "%9", "u-higgs": "%11"}
	items := []workItem{
		{ID: 1, Boot: "A", By: "%7", Assignee: "%9", FlippedBy: "%7", DeliveredTo: "%9"}, // conductor->higgs
		{ID: 2, Boot: "A", By: "%0", Assignee: "%7"},                                     // host seat + conductor
		{ID: 3, Boot: "A", By: "%11", Assignee: "%11"},                                   // pmf, not back -> waits
		{ID: 4, Boot: "", By: "%9"},                                                      // legacy/fresh -> current
		{ID: 5, Boot: "B", By: "%9", Assignee: "%10"},                                    // already current
		{ID: 6, Boot: "Z", By: "%3"},                                                     // boot never witnessed -> waits
	}
	ch, pending := remintItems(items, "B", l, live)
	if pending != 2 {
		t.Fatalf("pending=%d want 2 (pmf not back, unknown boot Z)", pending)
	}
	got := map[int64]workItem{}
	for _, it := range items {
		got[it.ID] = it
	}
	if g := got[1]; g.By != "%9" || g.Assignee != "%11" || g.FlippedBy != "%9" || g.DeliveredTo != "%11" || g.Boot != "B" {
		t.Fatalf("item1 mistranslated: %+v", g)
	}
	if g := got[2]; g.By != "ex-%0" || g.Assignee != "%9" || g.Boot != "B" {
		t.Fatalf("item2: host seat should be ex-%%0, conductor -> %%9: %+v", g)
	}
	if g := got[3]; g.By != "%11" || g.Assignee != "%11" || g.Boot != "A" {
		t.Fatalf("item3 must stay quarantined untouched: %+v", g)
	}
	if g := got[4]; g.Boot != "B" || g.By != "%9" {
		t.Fatalf("item4 blank boot should be stamped current, fields untouched: %+v", g)
	}
	if g := got[5]; g.Boot != "B" || g.Assignee != "%10" {
		t.Fatalf("item5 current boot must be untouched: %+v", g)
	}
	if g := got[6]; g.Boot != "Z" {
		t.Fatalf("item6 unknown boot must wait: %+v", g)
	}
	if len(ch) != 6 { // 1: by,assignee,flipped_by,delivered_to (4); 2: by(ex), assignee (2)
		t.Fatalf("changes=%d want 6: %+v", len(ch), ch)
	}
	// second pass once pmf is back at %13: item3 translates, nothing else moves
	live["u-pmf"] = "%13"
	ch, pending = remintItems(items, "B", l, live)
	if pending != 1 || len(ch) != 2 || items[2].By != "%13" || items[2].Assignee != "%13" || items[2].Boot != "B" {
		t.Fatalf("pmf pass: pending=%d ch=%+v item3=%+v", pending, ch, items[2])
	}
	// staleBoot: quarantine only when both boots known and differ
	setCurrentBoot("B")
	if !staleBoot(workItem{Boot: "A"}) || staleBoot(workItem{Boot: "B"}) || staleBoot(workItem{Boot: ""}) {
		t.Fatal("staleBoot wrong")
	}
	setCurrentBoot("")
	if staleBoot(workItem{Boot: "A"}) {
		t.Fatal("no tmux -> never quarantine")
	}
}
