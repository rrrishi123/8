package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// ── PEER LIVE PROXY (#9970) — live cross-host tab view + drive, BOTH ways, through
// the one port every host already exposes (:7070). Before this, a peer's tab was a
// metadata card and "drive" was a heartbeat-paced command queue (relay.go): no
// pixels, no real-time input. Yet every peer's OWN collector already serves live
// pixels (/shot, /drawshot, /stream) and real-time input (/act) for its local
// seat, and that collector is the thing reachable over the tailnet. So the live
// cross-host channel is a PROXY: this collector forwards /peer/<host>/<path> to
// the peer collector's /<path>, verbatim, and streams the answer back. The peer
// side keeps its own lease (N minds -> 1 tab), its own witness and its own
// manifest; this side records the hop so the act is witnessed on BOTH ledgers.
//
// Transport choice (the #9970 A/B): B — proxied through :7070. One trust boundary
// (the one already crossing), no new port on any host, one implementation that
// ships to every host via brew + snap (#9964), symmetric by construction: the
// peer runs the same code and proxies back to us.
//
// Autonomy (#9968): the SERVING side decides. A peer advertises its posture in its
// heartbeat (extra.drive = drive | view | off, from EIGHT_PEER_DRIVE on that host);
// this side honors it: off = nothing crosses, view = pixels only, drive = pixels +
// input. A peer on an older build advertises nothing ("unadvertised") and is
// treated as today's status quo — its collector already answers anyone on :7070.
// The posture rides back on X-8-Peer-Drive so the cockpit SHOWS it, never guesses.

const peerDriveEnv = "EIGHT_PEER_DRIVE"

// peerProxyPaths — the only collector paths that may cross a host boundary via
// /peer/. Everything else (ledger, inbox, panes, work) stays host-local.
var peerProxyPaths = map[string]bool{
	"shot": true, "drawshot": true, "stream": true, "text": true, // see
	"act":      true,                 // drive
	"manifest": true, "health": true, // know what you are looking at
}

// peerProxyInput — paths that MOVE the remote tab (need posture "drive").
var peerProxyInput = map[string]bool{"act": true}

// peerDriveModeEnv — this host's advertised posture. Unset/unknown -> "drive",
// the status quo (a collector on :7070 already answers /act for anyone who can
// reach it); the operator restricts with EIGHT_PEER_DRIVE=view|off.
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
	switch strings.ToLower(strings.TrimSpace(e.Drive)) {
	case "drive", "view", "off":
		return strings.ToLower(strings.TrimSpace(e.Drive))
	}
	return "unadvertised"
}

// peerProxyAllowed — PURE: may `path` cross to a peer with this posture?
func peerProxyAllowed(path, mode string) (bool, string) {
	if !peerProxyPaths[path] {
		return false, "path does not cross a host boundary"
	}
	switch mode {
	case "off":
		return false, "peer advertises drive=off: nothing crosses"
	case "view":
		if peerProxyInput[path] {
			return false, "peer advertises drive=view: pixels cross, input does not"
		}
	}
	return true, ""
}

// peerProxyTarget — PURE: "/peer/<host>/<path>" + query -> (host, path, upstream URL).
func peerProxyTarget(reqPath, rawQuery, base string) (host, path, target string, err error) {
	rest := strings.TrimPrefix(reqPath, "/peer/")
	host, path, ok := strings.Cut(rest, "/")
	if !ok || host == "" || path == "" || strings.Contains(path, "/") || strings.Contains(path, "..") {
		return "", "", "", fmt.Errorf("need /peer/<host>/<path>")
	}
	if base == "" {
		return host, path, "", fmt.Errorf("peer %q has no reachable address", host)
	}
	target = strings.TrimRight(base, "/") + "/" + path
	if rawQuery != "" {
		target += "?" + rawQuery
	}
	return host, path, target, nil
}

// jsonErr — an error body that STAYS application/json (http.Error forces text/plain).
func jsonErr(w http.ResponseWriter, status int, body string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	io.WriteString(w, body+"\n")
}

func peerSnapshot(host string) (peer, bool) {
	peerMu.Lock()
	defer peerMu.Unlock()
	p, ok := peers[host]
	if !ok || p == nil {
		return peer{}, false
	}
	return *p, true
}

func selfHostName() string {
	if h := strings.TrimSpace(os.Getenv("PEER_HOST")); h != "" {
		return h
	}
	h, _ := os.Hostname()
	return h
}

// handlePeerProxy — GET/POST /peer/<host>/<path>?... -> the peer collector's /<path>.
func (c *collector) handlePeerProxy(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	self := selfHostName()
	// loop guard: a hop that already passed through us must not come back in.
	for _, via := range strings.Split(r.Header.Get("X-8-Via"), ",") {
		if v := strings.TrimSpace(via); v != "" && v == self {
			jsonErr(w, http.StatusLoopDetected, `{"error":"peer proxy loop: this hop already passed through this host"}`)
			return
		}
	}
	host, path, _, err := peerProxyTarget(r.URL.Path, r.URL.RawQuery, "x")
	if err != nil {
		jsonErr(w, http.StatusBadRequest, fmt.Sprintf(`{"error":%q}`, err.Error()))
		return
	}
	p, ok := peerSnapshot(host)
	if !ok {
		jsonErr(w, http.StatusNotFound, fmt.Sprintf(`{"error":"unknown peer %q — it has not beaten into /peers"}`, host))
		return
	}
	mode := peerDriveMode(p.Extra)
	w.Header().Set("X-8-Peer-Drive", mode)
	if allowed, why := peerProxyAllowed(path, mode); !allowed {
		jsonErr(w, http.StatusForbidden, fmt.Sprintf(`{"error":"refused","peer":%q,"path":%q,"drive":%q,"why":%q}`, host, path, mode, why))
		return
	}
	_, _, target, err := peerProxyTarget(r.URL.Path, r.URL.RawQuery, p.Addr)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, fmt.Sprintf(`{"error":%q}`, err.Error()))
		return
	}
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 1<<20))
	}
	up, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(body))
	if err != nil {
		jsonErr(w, http.StatusBadGateway, fmt.Sprintf(`{"error":%q}`, err.Error()))
		return
	}
	for _, h := range []string{"Content-Type", "X-8-Actor", "X-8-Token", "Accept"} {
		if v := r.Header.Get(h); v != "" {
			up.Header.Set(h, v)
		}
	}
	via := self
	if prev := strings.TrimSpace(r.Header.Get("X-8-Via")); prev != "" {
		via = prev + "," + self
	}
	up.Header.Set("X-8-Via", via)
	client := c.client
	if path != "stream" { // a stream lives as long as the viewer; everything else is a call
		client = &http.Client{Timeout: 20 * time.Second}
	}
	start := time.Now()
	resp, err := client.Do(up)
	if err != nil {
		jsonErr(w, http.StatusBadGateway, fmt.Sprintf(`{"error":"peer unreachable","peer":%q,"detail":%q}`, host, err.Error()))
		return
	}
	defer resp.Body.Close()
	if peerProxyInput[path] { // the act is witnessed on THIS ledger too: who drove which peer tab, through which hop
		actor := actorOf(r, "")
		var in struct {
			Action, Seat, Actor, Context string
		}
		_ = json.Unmarshal(body, &in)
		if actor == "" {
			actor = strings.TrimSpace(in.Actor)
		}
		c.record(reqRec{TS: nowNano(), Physics: "channel", Session: "peer:" + host, Method: r.Method, URL: target,
			Body: string(body), Status: resp.StatusCode, LatUS: float64(time.Since(start).Microseconds()), Replayable: false, Seat: in.Seat, Actor: actor})
		c.publish(fmt.Sprintf(`{"session":"peer","origin":"COLLECTOR","frame":{"method":"peer.act","params":{"peer":%q,"context":%q,"action":%q,"actor":%q,"status":%d,"drive":%q}}}`, host, in.Context, in.Action, actor, resp.StatusCode, mode))
	}
	for _, h := range []string{"Content-Type", "Cache-Control", "X-8-Witness", "X-8-Ledger-Id"} {
		if v := resp.Header.Get(h); v != "" {
			w.Header().Set(h, v)
		}
	}
	w.Header().Set("X-8-Peer", host)
	w.WriteHeader(resp.StatusCode)
	flusher, _ := w.(http.Flusher)
	buf := make([]byte, 64<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if rerr != nil {
			return
		}
	}
}
