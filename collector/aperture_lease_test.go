package main

import (
	"strings"
	"testing"
	"time"
)

// The aperture must never park a tab an agent holds a LIVE lease on (#9556): a
// park (discardBrowser) reloads the tab on focus, losing the agent's in-progress
// state. leasedTabURLs is the Go-side set the park script skips; this locks in
// that a fresh claim is protected, a stale one is not, and empties/dupes are
// handled — so the guardrail holds without reading memoryAperture.
func TestLeasedTabURLs(t *testing.T) {
	fresh := time.Now().Format(time.RFC3339)
	stale := time.Now().Add(-5 * time.Minute).Format(time.RFC3339) // > 90s TTL

	manifest := map[string]*tabRec{
		"a": {Ctx: "a", URL: "https://app.example/live", ClaimedBy: "%9", ClaimAt: fresh},  // protected
		"b": {Ctx: "b", URL: "https://app.example/idle", ClaimedBy: "", ClaimAt: fresh},    // no lease -> parkable
		"c": {Ctx: "c", URL: "https://app.example/stale", ClaimedBy: "%7", ClaimAt: stale}, // lease expired -> parkable
		"d": {Ctx: "d", URL: "https://app.example/dup", ClaimedBy: "%8", ClaimAt: fresh},   // protected
		"e": {Ctx: "e", URL: "https://app.example/dup", ClaimedBy: "%12", ClaimAt: fresh},  // same URL as d -> deduped
		"f": {Ctx: "f", URL: "", ClaimedBy: "%6", ClaimAt: fresh},                          // no URL -> cannot be matched/skip
	}

	got := leasedTabURLs(manifest)

	want := map[string]bool{
		"https://app.example/live": true,
		"https://app.example/dup":  true,
	}
	if len(got) != len(want) {
		t.Fatalf("expected %d leased URLs, got %d: %v", len(want), len(got), got)
	}
	for _, u := range got {
		if !want[u] {
			t.Errorf("URL %q is NOT live-leased but was returned (would be wrongly protected)", u)
		}
	}
	// the stale lease and the unclaimed tab MUST be parkable (absent from the set)
	for _, u := range got {
		if u == "https://app.example/stale" || u == "https://app.example/idle" {
			t.Errorf("%q should be parkable (stale/unleased) but was protected", u)
		}
	}
	// deterministic sorted order (the park script injects this verbatim)
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("leasedTabURLs not sorted: %v", got)
		}
	}
}

// parkScriptFor must inject the lease set AND keep the non-negotiable skips
// (the focused tab and the cockpit), so a regression that drops them fails here.
func TestParkScriptForInjectsLeaseAndKeepsGuards(t *testing.T) {
	s := parkScriptFor(`["https://app.example/live"]`)
	for _, must := range []string{
		`const leased=["https://app.example/live"];`, // the injected set
		`if(leased.some(`,       // the skip
		`tab===gB.selectedTab`,  // focused tab still skipped
		`http://localhost:8088`, // cockpit still skipped
		`discardBrowser`,        // still actually parks
	} {
		if !strings.Contains(s, must) {
			t.Errorf("parkScriptFor output missing %q", must)
		}
	}
}
