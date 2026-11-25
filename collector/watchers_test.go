package main

import (
	"testing"
	"time"
)

func TestWatchersPresenceView(t *testing.T) {
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	ago := func(m int) string { return now.Add(time.Duration(-m) * time.Minute).Format(time.RFC3339) }
	minds := []mindView{
		{Name: "sibling-1", Pane: "%7", UUID: "818c3f5e", State: "active", Waiting: ""},
		{Name: "higgs", Pane: "%9", UUID: "1255da01", State: "idle", Waiting: "on PR #10 review"},
		{Name: "ghost", Pane: "", UUID: "deadbeef"}, // named but no live pane
		{Name: "", Pane: "%3"}, // unnamed pane — must be excluded
	}
	items := []workItem{
		{ID: 1, Status: "doing", Text: "verify a thing", FlippedBy: "sibling-1", TS: ago(5)},
		{ID: 2, Status: "blocked", Text: "a held proposal", By: "higgs (1255da01)", FlippedBy: "consensus:disagree", TS: ago(40)},
		{ID: 3, Status: "done", Text: "old", FlippedBy: "%91", TS: ago(2)}, // %91 must NOT match %9 (token guard)
		{ID: 4, Status: "doing", Text: "higgs on this", AckedBy: "%9", TS: ago(40)},
	}
	pending := func(m mindView) int {
		if m.Name == "higgs" {
			return 3
		}
		return 0
	}
	rows := watchers(minds, items, now, pending)

	if len(rows) != 3 { // ghost included (named), unnamed %3 excluded
		t.Fatalf("want 3 rows (named minds), got %d", len(rows))
	}
	by := map[string]watcherRow{}
	for _, r := range rows {
		by[r.Name] = r
	}
	// sibling-1: doing #1, live, ~5m idle, no pending
	s := by["sibling-1"]
	if len(s.Doing) != 1 || s.Doing[0] != 1 || !s.Live || s.IdleMinutes != 5 || s.InboxPending != 0 {
		t.Errorf("sibling-1 row wrong: %+v", s)
	}
	// higgs: blocked #2 + doing #4, declared wait, 3 pending, ~40m idle
	h := by["higgs"]
	if len(h.Blocked) != 1 || h.Blocked[0] != 2 || len(h.Doing) != 1 || h.Doing[0] != 4 ||
		h.WaitingFor != "on PR #10 review" || h.InboxPending != 3 || h.IdleMinutes != 40 {
		t.Errorf("higgs row wrong: %+v", h)
	}
	// %91 must not have matched %9 (token-boundary guard)
	for _, id := range append(append([]int64{}, h.Doing...), s.Doing...) {
		if id == 3 {
			t.Error("%%91 wrongly matched %%9 — token guard failed")
		}
	}
	// ghost: named but no pane → not live, no acts
	g := by["ghost"]
	if g.Live || len(g.Doing) != 0 {
		t.Errorf("ghost should be not-live/empty: %+v", g)
	}
	// most-idle-first ordering: higgs (40) before sibling-1 (5)
	if rows[0].IdleMinutes < rows[len(rows)-1].IdleMinutes {
		t.Errorf("rows not sorted most-idle-first: %+v", rows)
	}
}

// TestMindMatchesTokenBoundary locks the #36 external-review fix: name/pane match on a
// whole token, so a mind name is never swallowed by a longer actor string. These
// collisions are live in the roster today (pmf/validator-pmf, philo/validator-philo,
// higgs/higgs-worker) and sibling-1/sibling-10 is the forward-looking case.
func TestMindMatchesTokenBoundary(t *testing.T) {
	cases := []struct {
		name, pane, uuid, actor string
		want                    bool
	}{
		{"sibling-1", "%7", "aaaa1111", "sibling-1 (aaaa1111)", true},   // own stamp
		{"sibling-1", "%7", "aaaa1111", "sibling-10 (deadbeef)", false}, // not a superstring
		{"pmf", "%13", "bbbb2222", "pmf", true},
		{"pmf", "%13", "bbbb2222", "validator-pmf", false}, // was a false positive under Contains
		{"philo", "%12", "cccc3333", "validator-philo", false},
		{"higgs", "%9", "dddd4444", "higgs-worker", false},
		{"higgs", "%9", "dddd4444", "higgs %9 (dddd4444)", true},
		{"conductor", "%6", "eeee5555", "conductor %6 (eeee5555)", true},      // name+pane+uuid all present
		{"higgs", "%9", "dddd4444", "%91 something", false},                   // pane token guard
		{"higgs", "%9", "dddd4444", "acked by aaaa1111, dddd4444-full", true}, // uuid substring still matches
	}
	for _, c := range cases {
		m := mindView{Name: c.name, Pane: c.pane, UUID: c.uuid}
		if got := mindMatches(m, c.actor); got != c.want {
			t.Errorf("mindMatches(%q, %q) = %v, want %v", c.name, c.actor, got, c.want)
		}
	}
}
