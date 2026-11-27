package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func rfc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

// #9923: the stale-sweep must re-queue the DEAD, not the SLOW, and must enroll
// only opted-in deliverables. These are the pure decision cases (no ledger).
func TestDeliverableStale(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	grace := 15 * time.Minute
	ago := func(d time.Duration) string { return rfc(now.Add(-d)) }
	ahead := func(d time.Duration) string { return rfc(now.Add(d)) }

	cases := []struct {
		name string
		it   workItem
		want bool
	}{
		{"dropped: doing, deadline, no touch for 1h, no receipt", workItem{Kind: "task", Status: "doing", Deadline: ago(30 * time.Minute), TS: ago(time.Hour)}, true},
		{"slow-but-ALIVE: beating 1m ago, long past deadline", workItem{Kind: "task", Status: "doing", Deadline: ago(2 * time.Hour), TS: ago(time.Hour), BeatAt: ago(1 * time.Minute)}, false},
		{"beat went stale: last beat 20m ago > grace", workItem{Kind: "task", Status: "doing", Deadline: ago(time.Hour), TS: ago(time.Hour), BeatAt: ago(20 * time.Minute)}, true},
		{"not opted in: no deadline", workItem{Kind: "task", Status: "doing", TS: ago(time.Hour)}, false},
		{"not doing: still todo", workItem{Kind: "task", Status: "todo", Deadline: ago(time.Hour), TS: ago(time.Hour)}, false},
		{"already has a receipt", workItem{Kind: "task", Status: "doing", Deadline: ago(time.Hour), TS: ago(time.Hour), Receipt: "done, verified"}, false},
		{"a record is never swept", workItem{Kind: "record", Status: "doing", Deadline: ago(time.Hour), TS: ago(time.Hour)}, false},
		{"fresh: delivered 1m ago, deadline ahead", workItem{Kind: "task", Status: "doing", Deadline: ahead(time.Hour), TS: ago(2 * time.Hour), DeliveredAt: ago(time.Minute)}, false},
	}
	for _, c := range cases {
		if got := deliverableStale(c.it, now, grace); got != c.want {
			t.Errorf("%s: deliverableStale = %v, want %v", c.name, got, c.want)
		}
	}
}

// the sweep pass: a dropped deliverable is re-queued (todo, assignee + stamps
// cleared, FlippedBy records the sweep) and a witnessed record is written; an
// alive (beating) deliverable is left alone; firing twice is a no-op (the first
// pass left it as todo, which the sweep never touches).
func TestRedeliverPass(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	now := time.Now().UTC()
	ago := func(d time.Duration) string { return rfc(now.Add(-d)) }
	writeWork([]workItem{
		{ID: 1, Kind: "task", Status: "doing", By: "%6", Assignee: "%29", Deadline: ago(30 * time.Minute), TS: ago(time.Hour), DeliveredAt: ago(time.Hour)}, // dropped
		{ID: 2, Kind: "task", Status: "doing", By: "%6", Assignee: "%29", Deadline: ago(2 * time.Hour), TS: ago(time.Hour), BeatAt: ago(time.Minute)},       // alive
		{ID: 3, Kind: "task", Status: "doing", By: "%6", TS: ago(time.Hour)},                                                                                // not opted in
	})
	c := &collector{}
	read := func(id int64) workItem {
		var items []workItem
		b, _ := os.ReadFile(workFile())
		json.Unmarshal(b, &items)
		for _, it := range items {
			if it.ID == id {
				return it
			}
		}
		t.Fatalf("item %d vanished", id)
		return workItem{}
	}

	if n := c.redeliverPass(15 * time.Minute); n != 1 {
		t.Fatalf("first sweep must re-queue exactly the 1 dropped item, got %d", n)
	}
	if it := read(1); it.Status != "todo" || it.Assignee != "" || it.FlippedBy != "sweep:stale-redeliver" || it.DeliveredAt != "" {
		t.Fatalf("dropped item must be re-queued clean: status=%q assignee=%q flipped=%q delivered=%q", it.Status, it.Assignee, it.FlippedBy, it.DeliveredAt)
	}
	if it := read(1); it.Deadline == "" {
		t.Fatalf("re-queued item must KEEP its deadline so a second owner is watched too")
	}
	if it := read(2); it.Status != "doing" || it.Assignee != "%29" {
		t.Fatalf("alive (beating) deliverable must be untouched: status=%q assignee=%q", it.Status, it.Assignee)
	}
	if it := read(3); it.Status != "doing" {
		t.Fatalf("non-opted-in doing item must be untouched: status=%q", it.Status)
	}
	// a witnessed sweep record exists
	var items []workItem
	b, _ := os.ReadFile(workFile())
	json.Unmarshal(b, &items)
	found := false
	for _, it := range items {
		if it.By == "sweep" && strings.Contains(it.Text, "re-queued deliverable #1") {
			found = true
		}
	}
	if !found {
		t.Fatalf("a witnessed [sweep] record for #1 must be on the surface")
	}
	// idempotent: the re-queued item is now todo, so a second sweep re-queues nothing.
	if n := c.redeliverPass(15 * time.Minute); n != 0 {
		t.Fatalf("second sweep must be a no-op (re-queued item is todo now), got %d", n)
	}
}

// #9923 heartbeat handler: POST {id,beat:true} stamps BeatAt, changing nothing else.
func TestBeatHandler(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeWork([]workItem{{ID: 1, Kind: "task", Status: "doing", By: "%6", Assignee: "%29", Deadline: rfc(time.Now().Add(time.Hour))}})
	c := &collector{}
	rec := httptest.NewRecorder()
	c.handleWork(rec, httptest.NewRequest(http.MethodPost, "/work", strings.NewReader(`{"id":1,"beat":true,"by":"%29"}`)))
	if rec.Code != http.StatusOK {
		t.Fatalf("beat must be 200, got %d %s", rec.Code, rec.Body)
	}
	var items []workItem
	b, _ := os.ReadFile(workFile())
	json.Unmarshal(b, &items)
	if items[0].BeatAt == "" {
		t.Fatalf("beat must stamp BeatAt")
	}
	if items[0].Status != "doing" || items[0].Assignee != "%29" {
		t.Fatalf("beat must not change status/assignee: status=%q assignee=%q", items[0].Status, items[0].Assignee)
	}
}
