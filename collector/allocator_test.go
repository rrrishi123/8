package main

import (
	"testing"
	"time"
)

func TestLabelMatches(t *testing.T) {
	conductor := mindView{Name: "conductor", Pane: "%6", UUID: "12444bc8-4325-4c83-8f42-ed642122f0da"}
	for _, l := range []string{"conductor %6 (12444bc8)", "%6", "conductor", "12444bc8-4325-4c83-8f42-ed642122f0da"} {
		if !labelMatches(l, conductor) {
			t.Errorf("%q should match conductor", l)
		}
	}
	for _, l := range []string{"%60", "%16", "sibling-1", "", "pmf"} {
		if labelMatches(l, conductor) {
			t.Errorf("%q must not match conductor", l)
		}
	}
	s1 := mindView{Name: "sibling-1", Pane: "%7", UUID: "818c3f5e-997c"}
	if labelMatches("sibling-10", s1) || !labelMatches("sibling-1 %7", s1) {
		t.Error("sibling-1 boundary")
	}
}

func TestVerifyTarget(t *testing.T) {
	if verifyTarget("[verify #9530] falsify/verify …") != 9530 || verifyTarget("plain") != 0 || verifyTarget("[verify #x]") != 0 {
		t.Error("verifyTarget")
	}
}

func TestPickReroutes(t *testing.T) {
	now := time.Date(2026, 11, 24, 18, 0, 0, 0, time.UTC)
	old := now.Add(-3 * time.Hour).Format(time.RFC3339)
	fresh := now.Add(-10 * time.Minute).Format(time.RFC3339)
	items := []workItem{
		{ID: 100, Text: "claim by %8", Status: "done", By: "%8"},
		{ID: 101, Text: "[verify #100] falsify/verify that 'claim by %8' holds", Status: "todo", By: "auto", Assignee: "conductor", TS: old},
		{ID: 102, Text: "[verify #100] fresh twin", Status: "todo", By: "auto", Assignee: "conductor", TS: fresh},
		{ID: 103, Text: "[verify #100] acked", Status: "todo", By: "auto", Assignee: "conductor", TS: old, AckedAt: old},
		{ID: 104, Text: "not a verify, but old", Status: "todo", TS: old},
		{ID: 105, Text: "[verify #100] done already", Status: "done", TS: old},
	}
	minds := []mindView{
		{Name: "conductor", Pane: "%6", UUID: "12444bc8-x", State: "working"}, // current assignee, busy
		{Name: "", Pane: "%8", UUID: "f9129044-x", State: "idle"},             // the author, and unnamed
		{Name: "philo", Pane: "%12", UUID: "7261eb6b-x", State: "idle"},       // idle but has a pending offer
		{Name: "pmf", Pane: "%13", UUID: "e125a3b2-x", State: "idle"},         // the one eligible receiver
		{Name: "ghost", Pane: "", UUID: "0000", State: "idle"},                // name-dead: no pane
	}
	pending := func(p string) int {
		if p == "%12" {
			return 1
		}
		return 0
	}
	got := pickReroutes(items, minds, now, time.Hour, map[int64]time.Time{}, pending)
	if len(got) != 1 || got[0].Idx != 1 || got[0].From != "conductor" || got[0].To != "%13" {
		t.Fatalf("want one move #101 conductor->%%13, got %+v", got)
	}
	if got[0].Age < 2*time.Hour {
		t.Errorf("age should be ~3h, got %v", got[0].Age)
	}

	// a recent re-route resets the clock -> silence
	if r := pickReroutes(items, minds, now, time.Hour, map[int64]time.Time{101: now.Add(-5 * time.Minute)}, pending); len(r) != 0 {
		t.Errorf("recently re-routed must not move again: %+v", r)
	}
	// the only idle mind is the author -> silence
	minds2 := []mindView{{Name: "eight", Pane: "%8", UUID: "f9129044-x", State: "idle"}}
	if r := pickReroutes(items, minds2, now, time.Hour, nil, nil); len(r) != 0 {
		t.Errorf("author must never verify itself: %+v", r)
	}
	// nobody idle -> silence
	minds3 := []mindView{{Name: "pmf", Pane: "%13", State: "working"}}
	if r := pickReroutes(items, minds3, now, time.Hour, nil, nil); len(r) != 0 {
		t.Errorf("no idle mind -> nothing: %+v", r)
	}
	// two stale verifies, one idle mind -> one move per receiver per pass, oldest first
	items2 := append([]workItem{}, items...)
	items2 = append(items2, workItem{ID: 106, Text: "[verify #100] older still", Status: "todo", By: "auto", Assignee: "conductor", TS: now.Add(-5 * time.Hour).Format(time.RFC3339)})
	r := pickReroutes(items2, minds, now, time.Hour, nil, pending)
	if len(r) != 1 || items2[r[0].Idx].ID != 106 {
		t.Errorf("oldest first, one per receiver: %+v", r)
	}
}

func TestEnvDuration(t *testing.T) {
	t.Setenv("EIGHT_ALLOCATOR", "5m")
	if envDuration("EIGHT_ALLOCATOR", 0) != 5*time.Minute {
		t.Fatal("env value should win")
	}
	t.Setenv("EIGHT_ALLOCATOR", "garbage")
	if envDuration("EIGHT_ALLOCATOR", 0) != 0 {
		t.Fatal("unparsable -> fallback")
	}
	t.Setenv("EIGHT_ALLOCATOR", "")
	if envDuration("EIGHT_ALLOCATOR", 7*time.Second) != 7*time.Second {
		t.Fatal("unset -> fallback")
	}
}

// One gate above BOTH organs (49fe2c0b #9626): a closed gate must stop the
// wait pass as well as the re-router, publish the hold once, and each organ
// must obey its own switch.
func TestAllocatorBeatGate(t *testing.T) {
	var allocs, waits, helds, opens int
	open := true
	gate := func() (bool, string) { return open, "budget: conserve" }
	alloc := func() { allocs++ }
	wait := func() { waits++ }
	onHeld := func(string) { helds++ }
	onOpen := func() { opens++ }
	tick := time.Minute
	st := &beatState{lastRouted: map[int64]time.Time{}}
	now := time.Date(2026, 11, 25, 2, 0, 0, 0, time.UTC)

	// both switches on, gate open: both organs run on the first beat
	a, w := allocatorBeat(st, now, tick, 5*time.Minute, time.Minute, gate, alloc, wait, onHeld, onOpen)
	if !a || !w || allocs != 1 || waits != 1 {
		t.Fatalf("first beat should run both: %v %v %d %d", a, w, allocs, waits)
	}
	// next minute: only the 1m organ is due
	now = now.Add(time.Minute)
	a, w = allocatorBeat(st, now, tick, 5*time.Minute, time.Minute, gate, alloc, wait, onHeld, onOpen)
	if a || !w || allocs != 1 || waits != 2 {
		t.Fatalf("periods: alloc=%v wait=%v %d %d", a, w, allocs, waits)
	}
	// gate closes: NEITHER organ runs, hold published once across three beats
	open = false
	for i := 0; i < 3; i++ {
		now = now.Add(5 * time.Minute)
		if a, w := allocatorBeat(st, now, tick, 5*time.Minute, time.Minute, gate, alloc, wait, onHeld, onOpen); a || w {
			t.Fatal("gate closed yet an organ ran")
		}
	}
	if allocs != 1 || waits != 2 || helds != 1 || opens != 0 {
		t.Fatalf("held: allocs=%d waits=%d helds=%d opens=%d", allocs, waits, helds, opens)
	}
	// gate reopens: published once, organs resume
	open = true
	now = now.Add(5 * time.Minute)
	if a, w := allocatorBeat(st, now, tick, 5*time.Minute, time.Minute, gate, alloc, wait, onHeld, onOpen); !a || !w || opens != 1 {
		t.Fatalf("reopen: %v %v opens=%d", a, w, opens)
	}
	// slice-2 switch OFF: the wait pass never runs even with the gate open
	st2 := &beatState{lastRouted: map[int64]time.Time{}}
	waits = 0
	if a, w := allocatorBeat(st2, now, 5*time.Minute, 5*time.Minute, 0, gate, alloc, wait, onHeld, onOpen); !a || w || waits != 0 {
		t.Fatalf("waits switch off: %v %v %d", a, w, waits)
	}
	// slice-1 OFF, slice-2 ON: only waits
	st3 := &beatState{lastRouted: map[int64]time.Time{}}
	allocs = 0
	if a, w := allocatorBeat(st3, now, time.Minute, 0, time.Minute, gate, alloc, wait, onHeld, onOpen); a || !w || allocs != 0 {
		t.Fatalf("alloc switch off: %v %v %d", a, w, allocs)
	}
	// default gate is open
	if ok, _ := beatGate(nil); !ok {
		t.Fatal("default beatGate must be open")
	}
}

// #9601: the pure budget verdict the beat gate is armed with. nil/gated/conserve/
// stale all HOLD (fail-closed); only a fresh, un-gated, research verdict goes.
func TestBudgetAllowsAllocator(t *testing.T) {
	now := time.Now()
	fresh := now.Add(-30 * time.Second).Format(time.RFC3339)
	stale := now.Add(-20 * time.Minute).Format(time.RFC3339)
	cases := []struct {
		name string
		b    *budget
		want bool
	}{
		{"nil fail-closed", nil, false},
		{"gated", &budget{Gated: true, Phase: "research", ObservedAt: fresh}, false},
		{"conserve phase", &budget{Phase: "conserve", ObservedAt: fresh}, false},
		{"stale verdict", &budget{Phase: "research", ObservedAt: stale}, false},
		{"research fresh ungated", &budget{Phase: "research", ObservedAt: fresh}, true},
	}
	for _, c := range cases {
		if ok, _ := budgetAllowsAllocator(c.b, now); ok != c.want {
			t.Errorf("%s: got %v want %v", c.name, ok, c.want)
		}
	}
}
