package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPeerProxyTargetAndAllowlist(t *testing.T) {
	host, path, target, err := peerProxyTarget("/peer/peer-b/shot", "session=chrome&context=AB", "http://100.64.0.2:7070")
	if err != nil || host != "peer-b" || path != "shot" || target != "http://100.64.0.2:7070/shot?session=chrome&context=AB" {
		t.Fatalf("target: %q %q %q %v", host, path, target, err)
	}
	for _, bad := range []string{"/peer/", "/peer/peer-b", "/peer/peer-b/", "/peer/peer-b/a/b", "/peer/peer-b/../work"} {
		if _, _, _, err := peerProxyTarget(bad, "", "http://x"); err == nil {
			t.Fatalf("%q must be rejected", bad)
		}
	}
	for path, want := range map[string]bool{"shot": true, "stream": true, "act": true, "manifest": true, "work": false, "inbox": false, "panes": false} {
		if ok, _ := peerProxyAllowed(path, "drive"); ok != want {
			t.Fatalf("drive/%s: got %v want %v", path, ok, want)
		}
	}
	if ok, _ := peerProxyAllowed("shot", "view"); !ok {
		t.Fatal("view must let pixels cross")
	}
	if ok, _ := peerProxyAllowed("act", "view"); ok {
		t.Fatal("view must refuse input")
	}
	if ok, _ := peerProxyAllowed("shot", "off"); ok {
		t.Fatal("off must refuse everything")
	}
}

func TestPeerDriveMode(t *testing.T) {
	cases := map[string]string{
		`{"drive":"view"}`: "view", `{"drive":"OFF"}`: "off", `{"drive":"drive"}`: "drive",
		`{"budget":{}}`: "unadvertised", ``: "unadvertised", `{"drive":"maybe"}`: "unadvertised",
	}
	for in, want := range cases {
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

func withPeer(t *testing.T, host, addr, extra string) {
	peerMu.Lock()
	peers[host] = &peer{Host: host, Addr: addr, Extra: json.RawMessage(extra)}
	peerMu.Unlock()
	t.Cleanup(func() { peerMu.Lock(); delete(peers, host); peerMu.Unlock() })
}

// The proxy forwards path+query+body verbatim to the peer collector, carries the
// actor across, stamps the hop (X-8-Via), and reports the peer's posture.
func TestPeerProxyForwardsVerbatimAndStampsHop(t *testing.T) {
	t.Setenv("PEER_HOST", "mac-test")
	var got struct{ method, path, query, actor, via, body string }
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := readAllString(r)
		got = struct{ method, path, query, actor, via, body string }{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("X-8-Actor"), r.Header.Get("X-8-Via"), b}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-8-Witness", "seen · act #1 · by peer")
		w.Write([]byte(`{"ok":true,"via":"cdp"}`))
	}))
	defer fake.Close()
	withPeer(t, "peer-b", fake.URL, `{"drive":"drive","build":"abc"}`)
	c := newCollector(nil)
	t.Setenv("HOME", t.TempDir()) // record() appends to $HOME/.8 — keep it out of the real ledger

	req := httptest.NewRequest(http.MethodPost, "/peer/peer-b/act?session=chrome&context=AB", strings.NewReader(`{"action":"tap","xr":0.5,"yr":0.5,"context":"AB","seat":"ai"}`))
	req.Header.Set("X-8-Actor", "%8 (test)")
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	c.handlePeerProxy(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"via":"cdp"`) {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if got.method != "POST" || got.path != "/act" || got.query != "session=chrome&context=AB" || !strings.Contains(got.body, `"action":"tap"`) {
		t.Fatalf("not forwarded verbatim: %+v", got)
	}
	if got.actor != "%8 (test)" || got.via != "mac-test" {
		t.Fatalf("actor/hop not carried: %+v", got)
	}
	if rr.Header().Get("X-8-Peer-Drive") != "drive" || rr.Header().Get("X-8-Peer") != "peer-b" || rr.Header().Get("X-8-Witness") == "" {
		t.Fatalf("posture/witness headers missing: %v", rr.Header())
	}
	// witnessed on THIS ledger too
	c.lmu.Lock()
	n := len(c.ledger)
	last := reqRec{}
	if n > 0 {
		last = c.ledger[n-1]
	}
	c.lmu.Unlock()
	if n == 0 || last.Session != "peer:peer-b" || last.Actor != "%8 (test)" || last.Status != 200 {
		t.Fatalf("hop not recorded locally: n=%d last=%+v", n, last)
	}
}

func TestPeerProxyHonorsPosture(t *testing.T) {
	t.Setenv("PEER_HOST", "mac-test")
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{}`)) }))
	defer fake.Close()
	c := newCollector(nil)
	withPeer(t, "viewer", fake.URL, `{"drive":"view"}`)
	rr := httptest.NewRecorder()
	c.handlePeerProxy(rr, httptest.NewRequest(http.MethodPost, "/peer/viewer/act?session=chrome", strings.NewReader(`{}`)))
	if rr.Code != http.StatusForbidden || rr.Header().Get("X-8-Peer-Drive") != "view" {
		t.Fatalf("view must refuse input: %d %s", rr.Code, rr.Body.String())
	}
	rr = httptest.NewRecorder()
	c.handlePeerProxy(rr, httptest.NewRequest(http.MethodGet, "/peer/viewer/shot?session=chrome", nil))
	if rr.Code != 200 {
		t.Fatalf("view must let pixels cross: %d %s", rr.Code, rr.Body.String())
	}
	withPeer(t, "dark", fake.URL, `{"drive":"off"}`)
	rr = httptest.NewRecorder()
	c.handlePeerProxy(rr, httptest.NewRequest(http.MethodGet, "/peer/dark/shot?session=chrome", nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("off must refuse: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	c.handlePeerProxy(rr, httptest.NewRequest(http.MethodGet, "/peer/nobody/shot", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("unknown peer: %d", rr.Code)
	}
	rr = httptest.NewRecorder()
	c.handlePeerProxy(rr, httptest.NewRequest(http.MethodGet, "/peer/viewer/work", nil))
	if rr.Code != http.StatusForbidden {
		t.Fatalf("ledger must not cross: %d", rr.Code)
	}
	loop := httptest.NewRequest(http.MethodGet, "/peer/viewer/shot", nil)
	loop.Header.Set("X-8-Via", "peer-b,mac-test")
	rr = httptest.NewRecorder()
	c.handlePeerProxy(rr, loop)
	if rr.Code != http.StatusLoopDetected {
		t.Fatalf("loop guard: %d", rr.Code)
	}
}

func readAllString(r *http.Request) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := r.Body.Read(buf)
		sb.Write(buf[:n])
		if err != nil {
			break
		}
	}
	return sb.String(), nil
}
