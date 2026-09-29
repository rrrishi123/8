package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync"
	"time"
)

// ── PEERS RENDEZVOUS (#886, PORTAL 2/6) — the federation organ, push-based ───
// A peer 8 (mac / a Linux host / colima foursys / claude-web sandbox) POSTs itself
// here every N seconds: {host, hostres, manifest, thumbnail, ...}. The portal
// serves GET /peers. PUSH, not pull, by design — the crux constraint (#889) is
// that a sandboxed node can only reach OUT; a rendezvous the node reaches
// answers the wall-mind's egress-only shape (my #130: two witnesses across a
// wall exchange receipts by the walled one reaching out, git-as-channel #17/#19
// generalized to HTTP push).
//
// The LAW, at the federation layer: a peer's presence is a CLAIM that must keep
// verifying itself — a peer that stops heartbeating ages out (STALE_AFTER). No
// registration outlives its own re-verification. Nothing is inscribed: the
// roster is in-memory, rebuilt from live heartbeats, dying with the process.

const peerStaleAfter = 90 * time.Second

type peer struct {
	Host      string          `json:"host"`
	At        string          `json:"at"` // last heartbeat (RFC3339)
	Actor     string          `json:"actor,omitempty"`
	HostRes   json.RawMessage `json:"hostres,omitempty"`
	Manifest  json.RawMessage `json:"manifest,omitempty"`
	Thumbnail string          `json:"thumbnail,omitempty"` // data-URI or url
	Extra     json.RawMessage `json:"extra,omitempty"`
	Addr      string          `json:"addr,omitempty"` // the peer's collector base URL (from its beat's source IP), for a cross-host budget poke — the per-peer refresh
	lastBeat  time.Time
}

var (
	peerMu sync.Mutex
	peers  = map[string]*peer{} // host -> latest heartbeat
)

// handlePeers — POST registers/heartbeats a peer; GET serves the live roster
// (stale peers, silent past STALE_AFTER, are shown flagged rather than dropped
// mid-response so a just-missed beat isn't a disappearance).
func (c *collector) handlePeers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(r.Body)
		var p peer
		if json.Unmarshal(body, &p) != nil || p.Host == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "need {host, ...}; host is the peer's stable id"})
			return
		}
		now := time.Now()
		p.At = now.UTC().Format(time.RFC3339)
		p.lastBeat = now
		if p.Actor == "" {
			p.Actor = r.Header.Get("X-8-Actor")
		}
		// capture the peer's source IP so the cockpit can poke ITS collector's
		// /budget/poke (the per-peer refresh). Standard collector port :7070; a
		// beat can override via extra if a host maps a different port.
		if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil && host != "" {
			p.Addr = "http://" + net.JoinHostPort(host, "7070")
		}
		peerMu.Lock()
		peers[p.Host] = &p
		n := len(peers)
		peerMu.Unlock()
		c.publish(`{"session":"peers","origin":"COLLECTOR","frame":{"method":"peer.heartbeat","params":{"host":` + jsonStr(p.Host) + `}}}`)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "host": p.Host, "peers": n})
		return
	}
	// GET — the live roster, each with a stale flag re-verified against the clock.
	peerMu.Lock()
	type view struct {
		peer
		AgeS  int  `json:"age_s"`
		Stale bool `json:"stale"`
	}
	out := make([]view, 0, len(peers))
	for _, p := range peers {
		age := time.Since(p.lastBeat)
		out = append(out, view{peer: *p, AgeS: int(age.Seconds()), Stale: age > peerStaleAfter})
	}
	peerMu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"peers": out, "n": len(out), "stale_after_s": int(peerStaleAfter.Seconds())})
}

func jsonStr(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
