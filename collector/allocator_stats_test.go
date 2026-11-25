package main

import (
	"fmt"
	"testing"
)

// reroute record exactly as allocatorPass writes it, so the parser is tested
// against the real literal, not a paraphrase.
func rerouteRec(id, item, verifies int64, from, to string) workItem {
	return workItem{ID: id, Kind: "record", By: "allocator", Status: "done",
		Text: fmt.Sprintf("[allocator] re-routed #%d (verify of #%d) %s -> %s: untaken 90m0s; receiver idle, named, not the author", item, verifies, from, to)}
}

func TestAllocatorStats(t *testing.T) {
	items := []workItem{
		// #10 re-routed to %8, then TAKEN by %8 (un-stuck, by the target)
		{ID: 10, Kind: "task", Status: "doing", Text: "[verify #5] check the thing", FlippedBy: "%8", AckedBy: "%8"},
		// #11 re-routed to %9, still sitting in the pool (miss)
		{ID: 11, Kind: "task", Status: "todo", Text: "[verify #6] check another", FlippedBy: "allocator:reroute", Assignee: "%9"},
		// #12 re-routed to %12, who DECLINED it
		{ID: 12, Kind: "task", Status: "todo", Text: "[verify #7] a third", DeclinedBy: "%12"},
		// #13 re-routed to %9, taken by SOMEONE ELSE (%7) — un-stuck, but not by the target
		{ID: 13, Kind: "task", Status: "done", Text: "[verify #8] a fourth", FlippedBy: "%7", AckedBy: "%7"},
		// the immutable reroute records (note #10 re-routed TWICE → events>distinct)
		rerouteRec(100, 10, 5, "%6", "%8"),
		rerouteRec(101, 10, 5, "%6", "%8"),
		rerouteRec(102, 11, 6, "%6", "%9"),
		rerouteRec(103, 12, 7, "%6", "%12"),
		rerouteRec(104, 13, 8, "%6", "%9"),
		// noise that must NOT be counted
		{ID: 200, Kind: "record", By: "sibling-1", Text: "[allocator] re-routed #10 ... (a quote in someone's note, not a real record)"},
		{ID: 201, Kind: "record", By: "allocator", Text: "[allocator] held — budget: conserve"},
	}

	st := allocatorStats(items)

	if st.RerouteEvents != 5 {
		t.Errorf("reroute_events: got %d want 5", st.RerouteEvents)
	}
	if st.Distinct != 4 {
		t.Errorf("distinct_items: got %d want 4 (ids 10,11,12,13)", st.Distinct)
	}
	if st.Taken != 2 { // #10 and #13 are un-stuck
		t.Errorf("taken: got %d want 2", st.Taken)
	}
	if st.TakenByTarget != 1 { // only #10 was taken by the aimed-at %8 (#13 taken by %7, not the %9 target)
		t.Errorf("taken_by_target: got %d want 1", st.TakenByTarget)
	}
	if st.Declined != 1 { // #12
		t.Errorf("declined: got %d want 1", st.Declined)
	}
	if st.StillStuck != 1 { // #11
		t.Errorf("still_stuck: got %d want 1", st.StillStuck)
	}
	if st.HitRate != 0.5 { // 2/4
		t.Errorf("hit_rate: got %v want 0.5", st.HitRate)
	}
	// %8: routed twice (events), its one distinct id taken
	if st.ByReceiver["%8"].Routed != 2 || st.ByReceiver["%8"].Taken != 1 {
		t.Errorf("%%8 receiver: got %+v want {2 1}", st.ByReceiver["%8"])
	}
	// %9: routed twice (ids 11 and 13), zero taken-by-%9 (11 stuck, 13 taken by %7)
	if st.ByReceiver["%9"].Routed != 2 || st.ByReceiver["%9"].Taken != 0 {
		t.Errorf("%%9 receiver: got %+v want {2 0}", st.ByReceiver["%9"])
	}
}

func TestAllocatorStatsEmpty(t *testing.T) {
	st := allocatorStats(nil)
	if st.RerouteEvents != 0 || st.Distinct != 0 || st.HitRate != 0 {
		t.Errorf("empty ledger must be all-zero, got %+v", st)
	}
}
