package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
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
// loopback.
//
// What crosses, and who decides (#9964/#9968 — the SERVING side's autonomy):
//   - only SURFACE paths cross (viaPaths): pixels, text, DOM, input, identity.
//     The ledger (/work), inbox, pane fan-out (/panes/send), /relay and /via
//     itself never cross — a cockpit tab on host A must not be able to write
//     into host B's ledger or type into B's tmux panes through this throat.
//   - a peer advertises its posture in its heartbeat (extra.drive = drive |
//     view | off, from EIGHT_PEER_DRIVE on that host). drive = pixels + input,
//     view = pixels only, off = nothing. An older build advertises nothing and
//     is the status quo (its :7070 already answers anyone who can reach it).
//   - a hop stamps X-8-Via with this host's name; a request that already passed
//     through us is refused (508) so two peers bridging each other cannot loop.
//
// Witnessed: every hop publishes a via.proxy frame (live subscribers); every
// INPUT hop (act/run) is also RECORDED durably on this ledger — who drove which
// peer surface, through which hop, with what result — because a frame with no
// subscriber is dropped, and "I controlled it" must survive as a record.

const peerDriveEnv = "EIGHT_PEER_DRIVE"

// viaPaths — the only peer-local paths /via will forward. Everything else stays
// host-local.
var viaPaths = map[string]bool{
	"shot": true, "drawshot": true, "stream": true, "text": true, "source": true, // see pixels / DOM
	"tmuxpane": true, "nvimbuf": true, "daemonframe": true, // see text surfaces
	"act": true, "run": true, // drive
	"manifest": true, "tabs": true, "health": true, "hostres": true, "peers": true, // know what you are looking at
}

// viaInput — paths that MOVE the remote surface (need posture "drive").
var viaInput = map[string]bool{"act": true, "run": true}

// peerDriveModeEnv — this host's advertised posture. Unset/unknown -> "drive",
// the status quo; the operator restricts with EIGHT_PEER_DRIVE=view|off.
func peerDriveModeEnv() string {
	switch m := strings.ToLower(strings.TrimSpace(os.Getenv(peerDriveEnv))); m {
	case "view", "off", "drive":
		return m
	}
	return "drive"
}

// peerDriveMode — PURE: the posture a peer advertised in its heartbeat extra.
func peerDriveMode(extra json.RawMessage) string {
	var e struct {
		Drive string `json:"drive"`
	}
	if len(extra) == 0 || json.Unmarshal(extra, &e) != nil {
		return "unadvertised"
	}
	switch m := strings.ToLower(strings.TrimSpace(e.Drive)); m {
	case "drive", "view", "off":
		return m
	}
	return "unadvertised"
}

// viaAllowed — PURE: may the peer-local path `to` cross to a peer with this posture?
func viaAllowed(to, mode string) (bool, string) {
	p := strings.TrimPrefix(to, "/")
	if p == "" || strings.ContainsAny(p, "/?#") || strings.Contains(p, "..") {
		return false, "to must be one peer-local path, params go alongside"
	}
	if !viaPaths[p] {
		return false, "path does not cross a host boundary"
	}
	switch mode {
	case "off":
		return false, "peer advertises drive=off: nothing crosses"
	case "view":
		if viaInput[p] {
			return false, "peer advertises drive=view: pixels cross, input does not"
		}
	}
	return true, ""
}

func selfHostName() string {
	if h := strings.TrimSpace(os.Getenv("PEER_HOST")); h != "" {
		return h
	}
	h, _ := os.Hostname()
	return h
}

// jsonErr — an error body that STAYS application/json (http.Error forces text/plain).
func jsonErr(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body+"\n")
}

// viaDialLoopback — tests only: a fake peer listens on loopback, which the NAT
// guard below rightly refuses in production.
var viaDialLoopback = false

// peerBase resolves a roster peer's directly-reachable collector base URL and its
// advertised posture, or (",_,false") when the peer is unknown, stale, or NAT'd
// (its beat arrived from loopback, so it has no address this host can dial —
// that peer uses /relay).
func peerBase(host string) (string, string, bool) {
	peerMu.Lock()
	p, ok := peers[host]
	var addr, mode string
	var fresh bool
	if ok {
		addr = p.Addr
		fresh = time.Since(p.lastBeat) <= peerStaleAfter
		mode = peerDriveMode(p.Extra)
	}
	peerMu.Unlock()
	if !ok || addr == "" || !fresh {
		return "", mode, false
	}
	// A NAT'd peer's Addr is derived from a loopback source IP (its beat came via
	// 127.0.0.1) — dialing that would hit THIS host, not the peer. Those are not
	// live-reachable; the pull relay carries their commands instead.
	hp := strings.TrimPrefix(strings.TrimPrefix(addr, "http://"), "https://")
	if h, _, err := net.SplitHostPort(hp); err == nil {
		if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() && !viaDialLoopback {
			return "", mode, false
		}
	}
	return addr, mode, true
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
		jsonErr(w, http.StatusBadRequest, `{"error":"need ?peer=<host>&to=/<path> (the peer-local collector path)"}`)
		return
	}
	self := selfHostName()
	// loop guard: a hop that already passed through us must not come back in.
	for _, via := range strings.Split(r.Header.Get("X-8-Via"), ",") {
		if v := strings.TrimSpace(via); v != "" && v == self {
			jsonErr(w, http.StatusLoopDetected, `{"error":"via loop: this hop already passed through this host"}`)
			return
		}
	}
	base, mode, ok := peerBase(host)
	w.Header().Set("X-8-Peer-Drive", mode) // the cockpit SHOWS the peer's posture, never guesses it
	if !ok {
		jsonErr(w, http.StatusBadGateway, fmt.Sprintf(`{"error":"peer not live-reachable","peer":%s,"hint":"unknown, stale or NAT'd — drive it via /relay?peer=%s (pull) instead"}`, jsonStr(host), host))
		return
	}
	if allowed, why := viaAllowed(to, mode); !allowed {
		jsonErr(w, http.StatusForbidden, fmt.Sprintf(`{"error":"refused","peer":%s,"to":%s,"drive":%s,"why":%s}`, jsonStr(host), jsonStr(to), jsonStr(mode), jsonStr(why)))
		return
	}
	target := viaTarget(base, to, r.URL.Query())
	actor := actorOf(r, "")
	var body []byte
	var req *http.Request
	var err error
	if r.Method == http.MethodPost {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 8<<20))
		req, err = http.NewRequestWithContext(r.Context(), http.MethodPost, target, bytes.NewReader(body))
		if req != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		if actor == "" { // an explicit body actor counts as declared (actorOf precedence)
			var in struct {
				Actor string `json:"actor"`
			}
			_ = json.Unmarshal(body, &in)
			actor = strings.TrimSpace(in.Actor)
		}
	} else {
		req, err = http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	}
	if err != nil {
		jsonErr(w, http.StatusBadGateway, fmt.Sprintf(`{"error":%s}`, jsonStr(err.Error())))
		return
	}
	if actor != "" {
		req.Header.Set("X-8-Actor", actor) // carry the driver's identity so the PEER witnesses who drove it
	}
	if tok := r.Header.Get("X-8-Token"); tok != "" {
		req.Header.Set("X-8-Token", tok) // a token-gated peer sees the caller's token, not ours
	}
	via := self
	if prev := strings.TrimSpace(r.Header.Get("X-8-Via")); prev != "" {
		via = prev + "," + self
	}
	req.Header.Set("X-8-Via", via)
	start := time.Now()
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, fmt.Sprintf(`{"error":%s,"peer":%s,"to":%s}`, jsonStr("via: "+err.Error()), jsonStr(host), jsonStr(to)))
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	lat := time.Since(start)
	// Witness on THIS host: who drove which peer path, result code, latency.
	c.publish(fmt.Sprintf(`{"session":"peers","origin":"COLLECTOR","frame":{"method":"via.proxy","params":{"peer":%s,"to":%s,"by":%s,"code":%d,"ms":%d,"drive":%s}}}`,
		jsonStr(host), jsonStr(to), jsonStr(actor), resp.StatusCode, lat.Milliseconds(), jsonStr(mode)))
	if viaInput[strings.TrimPrefix(to, "/")] { // an INPUT hop survives as a record, not just a frame
		c.record(reqRec{TS: nowNano(), Physics: "call", Session: "peer:" + host, Method: r.Method, URL: target,
			Body: string(body), Status: resp.StatusCode, LatUS: float64(lat.Microseconds()), RespLen: len(rb), Replayable: false, Actor: actor})
	}
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	for _, h := range []string{"X-8-Witness", "X-8-Ledger-Id"} { // the peer's own witness rides back too
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set("X-8-Peer-"+strings.TrimPrefix(h, "X-8-"), v)
		}
	}
	w.Header().Set("X-8-Via-Peer", host) // the cockpit can tell a proxied frame from a local one
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write(rb)
}
