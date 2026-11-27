package main

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ── LIVE CROSS-HOST BRIDGE (#9970) — symmetry across hosts ───────────────────
// The principle (Rishi): "what the cockpit sees for ITS host, the same could be
// replicated across all the instances — the relations the logic describes for one
// exist for the other too." So every surface the cockpit drives for its OWN host
// (a chrome tab's live pixels via /shot, a click/scroll/type via /act, a tmux
// pane's tabs, a daemon list, DOM /source) must be reachable for a PEER host by
// the SAME path name — just addressed with &peer=<host>.
//
// ONE mechanism, not a special case per surface: GET/POST /via?peer=<host>&to=<p>
// reverse-proxies <p> (with its other params/body) SYNCHRONOUSLY to that peer's
// OWN collector (peerBase). The peer drives its OWN local seat and returns the
// bytes — host A never reaches into host B's browser, it asks B's collector to,
// so each host's security stays at its own end. This is the LIVE throat for a
// DIRECTLY-REACHABLE peer (two hosts on the same tailnet): sub-second, not the
// ~30s heartbeat. The /relay pull queue remains the fallback for a NAT'd peer
// (colima behind slirp) that can only reach OUT and whose Addr resolves to
// loopback. Witnessed on THIS side: a via.proxy frame records who drove which
// peer path, the result code and the latency — so a cross-host drive is seen.

// peerBase resolves a roster peer's directly-reachable collector base URL, or
// (",false") when the peer is unknown, stale, or NAT'd (its beat arrived from
// loopback, so it has no address this host can dial — that peer uses /relay).
func peerBase(host string) (string, bool) {
	peerMu.Lock()
	p, ok := peers[host]
	var addr string
	var fresh bool
	if ok {
		addr = p.Addr
		fresh = time.Since(p.lastBeat) <= peerStaleAfter
	}
	peerMu.Unlock()
	if !ok || addr == "" || !fresh {
		return "", false
	}
	// A NAT'd peer's Addr is derived from a loopback source IP (its beat came via
	// 127.0.0.1) — dialing that would hit THIS host, not the peer. Those are not
	// live-reachable; the pull relay carries their commands instead.
	hp := strings.TrimPrefix(strings.TrimPrefix(addr, "http://"), "https://")
	if h, _, err := net.SplitHostPort(hp); err == nil {
		if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
			return "", false
		}
	}
	return addr, true
}

// viaTarget builds the peer-local URL for a /via request: the peer's base, the
// `to` path, and every ORIGINAL query param except peer/to carried through. Pure
// (no network) so the forwarding contract is unit-testable.
func viaTarget(base, to string, q url.Values) string {
	uq := url.Values{}
	for k, vs := range q {
		if k == "peer" || k == "to" {
			continue
		}
		uq[k] = vs
	}
	target := base + to
	if enc := uq.Encode(); enc != "" {
		target += "?" + enc
	}
	return target
}

// handleVia is the live cross-host bridge. See the file header.
func (c *collector) handleVia(w http.ResponseWriter, r *http.Request) {
	host := r.URL.Query().Get("peer")
	to := r.URL.Query().Get("to")
	w.Header().Set("Content-Type", "application/json")
	if host == "" || to == "" || !strings.HasPrefix(to, "/") {
		http.Error(w, `{"error":"need ?peer=<host>&to=/<path> (the peer-local collector path)"}`, http.StatusBadRequest)
		return
	}
	base, ok := peerBase(host)
	if !ok {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"error":"peer not live-reachable","peer":%s,"hint":"stale or NAT'd — drive it via /relay?peer=%s (pull) instead"}`, jsonStr(host), host)
		return
	}
	target := viaTarget(base, to, r.URL.Query())
	actor := actorOf(r, "")
	var req *http.Request
	var err error
	if r.Method == http.MethodPost {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		req, err = http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
		if req != nil {
			req.Header.Set("Content-Type", "application/json")
		}
	} else {
		req, err = http.NewRequest(http.MethodGet, target, nil)
	}
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"error":%s}`, jsonStr(err.Error()))
		return
	}
	if actor != "" {
		req.Header.Set("X-8-Actor", actor) // carry the driver's identity so the PEER witnesses who drove it
	}
	start := time.Now()
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		fmt.Fprintf(w, `{"error":%s,"peer":%s,"to":%s}`, jsonStr("via: "+err.Error()), jsonStr(host), jsonStr(to))
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	ms := time.Since(start).Milliseconds()
	// Witness on THIS host: who drove which peer path, result code, latency.
	c.publish(fmt.Sprintf(`{"session":"peers","origin":"COLLECTOR","frame":{"method":"via.proxy","params":{"peer":%s,"to":%s,"by":%s,"code":%d,"ms":%d}}}`,
		jsonStr(host), jsonStr(to), jsonStr(actor), resp.StatusCode, ms))
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.Header().Set("X-8-Via-Peer", host) // the cockpit can tell a proxied frame from a local one
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(rb)
}
