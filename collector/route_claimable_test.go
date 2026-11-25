package main

import (
	"testing"
	"time"
)

// #9728 acceptance test (the pack's falsifiable condition): the todo-router routes
// ONLY positively-opted-in items, never speech-acts, and NEVER an outward/leak-path
// item even if opted-in. Fail-closed: no claimable marker => not routable.
func TestPickClaimableOptInAndOutwardFloor(t *testing.T) {
	ts := time.Now().Add(-10 * time.Minute).Format(time.RFC3339)
	items := []workItem{
		{ID: 1, Status: "todo", Claimable: true, Text: "[pool] wire the hit-rate readout over existing records", By: "%6", TS: ts},            // routable
		{ID: 2, Status: "todo", Claimable: false, Text: "[philo review] AGREE on #20, firsthand", By: "%12", TS: ts},                          // speech-act, no opt-in
		{ID: 3, Status: "todo", Claimable: true, Text: "cut the v0.0.5 tag on main and push", By: "%6", TS: ts},                               // outward verb
		{ID: 4, Status: "todo", Claimable: true, Text: "cherry-pick the public collector fixes onto next", By: "%6", TS: ts},                  // leak path
		{ID: 5, Status: "todo", Claimable: true, Text: "ingest Rishi's private Slack DM thread", By: "%6", TS: ts},                            // private source
		{ID: 6, Status: "todo", Claimable: true, Text: "edit the kosaten-deploy steward script (#68) which auto-publishes", By: "%6", TS: ts}, // #68: a steward script that auto-publishes — a peer's catch
	}
	// claimableRoutable gate
	want := map[int64]bool{1: true, 2: false, 3: false, 4: false, 5: false, 6: false}
	for _, it := range items {
		if claimableRoutable(it) != want[it.ID] {
			t.Errorf("claimableRoutable(#%d)=%v want %v (text=%q)", it.ID, claimableRoutable(it), want[it.ID], it.Text)
		}
	}
	// pickClaimable routes ONLY #1, and to an idle named mind that is not the author
	minds := []mindView{{Name: "higgs", Pane: "%9", State: "idle"}}
	got := pickClaimable(items, minds, time.Now(), map[int64]time.Time{}, func(string) int { return 0 })
	if len(got) != 1 || items[got[0].Idx].ID != 1 || got[0].To != "%9" {
		t.Fatalf("expected only #1 routed to %%9, got %+v", got)
	}
	// decline SET (%8 #9759 D1): minds that declined — under ANY spelling — are never
	// re-offered; a mind that has not declined still is. Legacy single DeclinedBy honoured.
	items[0].By = "%6"
	items[0].DeclinedBy = "%7"
	items[0].Declined = []string{"higgs %9 (99999999-0000-4000-8000-000000000009)", "%7"}
	ring := []mindView{
		{Name: "higgs", Pane: "%9", UUID: "99999999-0000-4000-8000-000000000009", State: "idle"},
		{Name: "sibling-1", Pane: "%7", UUID: "77777777-0000-4000-8000-000000000007", State: "idle"},
		{Name: "philo", Pane: "%12", UUID: "12121212-0000-4000-8000-000000000012", State: "idle"},
	}
	g := pickClaimable(items, ring, time.Now(), map[int64]time.Time{}, func(string) int { return 0 })
	if len(g) != 1 || g[0].To != "%12" {
		t.Fatalf("decliners %%9/%%7 must be skipped and %%12 offered, got %+v", g)
	}
	items[0].Declined = append(items[0].Declined, "%12 (12121212)")
	if g := pickClaimable(items, ring, time.Now(), map[int64]time.Time{}, func(string) int { return 0 }); len(g) != 0 {
		t.Fatalf("once every idle mind declined, nothing may route (no cycling), got %+v", g)
	}
	items[0].DeclinedBy, items[0].Declined = "", nil
	// author guard: a claimable item authored BY the only idle mind must NOT route to it
	items[0].By = "higgs"
	if g := pickClaimable(items, minds, time.Now(), map[int64]time.Time{}, func(string) int { return 0 }); len(g) != 0 {
		t.Fatalf("must not route an item to its own author, got %+v", g)
	}
}
