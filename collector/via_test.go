package main

import (
	"net/url"
	"testing"
	"time"
)

// The live bridge may only dial a peer that is KNOWN, FRESH, and directly
// reachable. A NAT'd peer's Addr resolves to loopback (its beat came via
// 127.0.0.1) and a stale peer may be gone — both must fall back to the /relay
// pull queue, never be dialed as if live (that would hit THIS host or a dead one).
func TestPeerBaseOnlyLiveReachable(t *testing.T) {
	peerMu.Lock()
	saved := peers
	peers = map[string]*peer{
		"peer-remote": {Host: "peer-remote", Addr: "http://192.0.2.10:7070", lastBeat: time.Now()},
		"colima":      {Host: "colima", Addr: "http://127.0.0.1:7070", lastBeat: time.Now()}, // NAT'd → loopback
		"stalehost":   {Host: "stalehost", Addr: "http://100.1.2.3:7070", lastBeat: time.Now().Add(-5 * time.Minute)},
		"noaddr":      {Host: "noaddr", lastBeat: time.Now()},
	}
	peerMu.Unlock()
	t.Cleanup(func() { peerMu.Lock(); peers = saved; peerMu.Unlock() })

	if base, ok := peerBase("peer-remote"); !ok || base != "http://192.0.2.10:7070" {
		t.Fatalf("a fresh routable peer must be live-reachable, got %q ok=%v", base, ok)
	}
	for _, h := range []string{"colima", "stalehost", "noaddr", "unknown"} {
		if base, ok := peerBase(h); ok {
			t.Fatalf("%s must NOT be live-reachable (falls back to relay), got %q", h, base)
		}
	}
}

// The bridge must forward the peer-local path with EVERY param except its own
// routing keys (peer/to) — so /via?peer=peer-remote&to=/shot&session=chrome&context=X
// reaches the peer as /shot?session=chrome&context=X, untouched.
func TestViaTargetCarriesParamsDropsRouting(t *testing.T) {
	q := url.Values{
		"peer":    {"peer-remote"},
		"to":      {"/shot"},
		"session": {"chrome"},
		"context": {"ABC123"},
		"w":       {"700"},
	}
	got := viaTarget("http://192.0.2.10:7070", "/shot", q)
	want := "http://192.0.2.10:7070/shot?context=ABC123&session=chrome&w=700"
	if got != want {
		t.Fatalf("viaTarget\n got=%q\nwant=%q", got, want)
	}
	// no params beyond routing keys => bare path, no trailing '?'
	bare := viaTarget("http://h:7070", "/tabs", url.Values{"peer": {"x"}, "to": {"/tabs"}})
	if bare != "http://h:7070/tabs" {
		t.Fatalf("bare path must carry no query, got %q", bare)
	}
}
