package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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
		"peer-remote": {Host: "peer-remote", Addr: "http://192.0.2.10:7070", lastBeat: time.Now(), Extra: json.RawMessage(`{"drive":"view"}`)},
		"colima":      {Host: "colima", Addr: "http://127.0.0.1:7070", lastBeat: time.Now()}, // NAT'd → loopback
		"stalehost":   {Host: "stalehost", Addr: "http://100.1.2.3:7070", lastBeat: time.Now().Add(-5 * time.Minute)},
		"noaddr":      {Host: "noaddr", lastBeat: time.Now()},
	}
	peerMu.Unlock()
	t.Cleanup(func() { peerMu.Lock(); peers = saved; peerMu.Unlock() })

	if base, mode, ok := peerBase("peer-remote"); !ok || base != "http://192.0.2.10:7070" || mode != "view" {
		t.Fatalf("a fresh routable peer must be live-reachable with its posture, got %q %q ok=%v", base, mode, ok)
	}
	for _, h := range []string{"colima", "stalehost", "noaddr", "unknown"} {
		if base, _, ok := peerBase(h); ok {
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

// Only SURFACE paths cross a host boundary; the peer's posture decides whether
// input crosses at all. The ledger, inbox, pane fan-out and /via itself never do.
func TestViaAllowlistAndPosture(t *testing.T) {
	for to, want := range map[string]bool{
		"/shot": true, "/drawshot": true, "/tmuxpane": true, "/source": true, "/act": true, "/run": true, "/manifest": true, "/health": true,
		"/work": false, "/inbox": false, "/panes/send": false, "/relay": false, "/via": false, "/compose": false,
		"/via?peer=x&to=/shot": false, "/../work": false, "/": false,
	} {
		if ok, _ := viaAllowed(to, "drive"); ok != want {
			t.Fatalf("drive %s: got %v want %v", to, ok, want)
		}
	}
	if ok, _ := viaAllowed("/shot", "unadvertised"); !ok {
		t.Fatal("an older peer (unadvertised) is the status quo: pixels cross")
	}
	if ok, _ := viaAllowed("/act", "unadvertised"); !ok {
		t.Fatal("an older peer (unadvertised) is the status quo: input crosses")
	}
	if ok, _ := viaAllowed("/shot", "view"); !ok {
		t.Fatal("view must let pixels cross")
	}
	for _, in := range []string{"/act", "/run"} {
		if ok, _ := viaAllowed(in, "view"); ok {
			t.Fatalf("view must refuse input %s", in)
		}
	}
	if ok, _ := viaAllowed("/shot", "off"); ok {
		t.Fatal("off must refuse everything")
	}
	for in, want := range map[string]string{`{"drive":"view"}`: "view", `{"drive":"OFF"}`: "off", `{"budget":{}}`: "unadvertised", ``: "unadvertised", `{"drive":"maybe"}`: "unadvertised"} {
		if got := peerDriveMode(json.RawMessage(in)); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
	t.Setenv(peerDriveEnv, "")
	if peerDriveModeEnv() != "drive" {
		t.Fatal("unset env must be the status quo: drive")
	}
	t.Setenv(peerDriveEnv, "view")
	if peerDriveModeEnv() != "view" {
		t.Fatal("env view must be honored")
	}
}

func withViaPeer(t *testing.T, host, addr, extra string) {
	viaDialLoopback = true // the fake peer is an httptest server on loopback
	t.Cleanup(func() { viaDialLoopback = false })
	peerMu.Lock()
	peers[host] = &peer{Host: host, Addr: addr, Extra: json.RawMessage(extra), lastBeat: time.Now()}
	peerMu.Unlock()
	t.Cleanup(func() { peerMu.Lock(); delete(peers, host); peerMu.Unlock() })
}

// An INPUT hop: forwarded verbatim with the driver's identity, stamped with the
// hop (X-8-Via), reported with the peer's posture, and RECORDED on this ledger —
// a frame with no subscriber is dropped; "I drove that" must survive.
func TestViaForwardsStampsAndRecordsInput(t *testing.T) {
	t.Setenv("PEER_HOST", "mac-test")
	t.Setenv("HOME", t.TempDir()) // record() appends under $HOME/.8 — keep it out of the real ledger
	var got struct{ method, path, query, actor, via, body string }
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var sb strings.Builder
		buf := make([]byte, 4096)
		for {
			n, err := r.Body.Read(buf)
			sb.Write(buf[:n])
			if err != nil {
				break
			}
		}
		got = struct{ method, path, query, actor, via, body string }{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-8-Actor"), r.Header.Get("X-8-Via"), sb.String()}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-8-Witness", "seen · act #1 · by peer")
		w.Write([]byte(`{"ok":true,"via":"cdp"}`))
	}))
	defer fake.Close()
	withViaPeer(t, "peer-b", fake.URL, `{"drive":"drive","build":"abc"}`)
	c := newCollector(nil)

	req := httptest.NewRequest(http.MethodPost, "/via?peer=peer-b&to=/act&session=chrome", strings.NewReader(`{"action":"tap","xr":0.5,"yr":0.5,"context":"AB","seat":"ai"}`))
	req.Header.Set("X-8-Actor", "cockpit@mac-test")
	rr := httptest.NewRecorder()
	c.handleVia(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"via":"cdp"`) {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if got.method != "POST" || got.path != "/act" || got.query != "session=chrome" || !strings.Contains(got.body, `"action":"tap"`) {
		t.Fatalf("not forwarded verbatim: %+v", got)
	}
	if got.actor != "cockpit@mac-test" || got.via != "mac-test" {
		t.Fatalf("actor/hop not carried: %+v", got)
	}
	if rr.Header().Get("X-8-Peer-Drive") != "drive" || rr.Header().Get("X-8-Via-Peer") != "peer-b" || rr.Header().Get("X-8-Peer-Witness") == "" {
		t.Fatalf("posture/witness headers missing: %v", rr.Header())
	}
	c.lmu.Lock()
	n := len(c.ledger)
	last := reqRec{}
	if n > 0 {
		last = c.ledger[n-1]
	}
	c.lmu.Unlock()
	if n == 0 || last.Session != "peer:peer-b" || last.Actor != "cockpit@mac-test" || last.Status != 200 || !strings.HasSuffix(last.URL, "/act?session=chrome") {
		t.Fatalf("input hop not recorded locally: n=%d last=%+v", n, last)
	}
	// a VIEW hop is a frame, not a record (2fps polls must not flood the ledger)
	rr = httptest.NewRecorder()
	c.handleVia(rr, httptest.NewRequest(http.MethodGet, "/via?peer=peer-b&to=/shot&session=chrome", nil))
	c.lmu.Lock()
	n2 := len(c.ledger)
	c.lmu.Unlock()
	if rr.Code != 200 || n2 != n {
		t.Fatalf("view hop: code %d, records %d -> %d", rr.Code, n, n2)
	}
}

func TestViaRefusals(t *testing.T) {
	t.Setenv("PEER_HOST", "mac-test")
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer fake.Close()
	c := newCollector(nil)
	withViaPeer(t, "viewer", fake.URL, `{"drive":"view"}`)
	rr := httptest.NewRecorder()
	c.handleVia(rr, httptest.NewRequest(http.MethodPost, "/via?peer=viewer&to=/act&session=chrome", strings.NewReader(`{}`)))
	if rr.Code != http.StatusForbidden || rr.Header().Get("X-8-Peer-Drive") != "view" {
		t.Fatalf("view must refuse input: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	c.handleVia(rr, httptest.NewRequest(http.MethodGet, "/via?peer=viewer&to=/shot&session=chrome", nil))
	if rr.Code != 200 {
		t.Fatalf("view must let pixels cross: %d %s", rr.Code, rr.Body.String())
	}
	for _, to := range []string{"/work", "/inbox", "/panes/send", "/via"} {
		rr = httptest.NewRecorder()
		c.handleVia(rr, httptest.NewRequest(http.MethodPost, "/via?peer=viewer&to="+to, strings.NewReader(`{"text":"x","by":"anyone"}`)))
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s must not cross a host boundary: %d %s", to, rr.Code, rr.Body.String())
		}
	}
	withViaPeer(t, "dark", fake.URL, `{"drive":"off"}`)
	rr = httptest.NewRecorder()
	c.handleVia(rr, httptest.NewRequest(http.MethodGet, "/via?peer=dark&to=/shot", nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("off must refuse: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	c.handleVia(rr, httptest.NewRequest(http.MethodGet, "/via?peer=nobody&to=/shot", nil))
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("unknown peer falls to relay hint: %d", rr.Code)
	}
	loop := httptest.NewRequest(http.MethodGet, "/via?peer=viewer&to=/shot", nil)
	loop.Header.Set("X-8-Via", "peer-b,mac-test")
	rr = httptest.NewRecorder()
	c.handleVia(rr, loop)
	if rr.Code != http.StatusLoopDetected {
		t.Fatalf("loop guard: %d", rr.Code)
	}
}
