package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeerJoinConfig(t *testing.T) {
	for _, key := range []string{"PEER_HUB", "HUB", "PEER_HOST", "PEER_TOKEN"} {
		t.Setenv(key, "")
	}
	cfg, err := peerJoinFromEnv()
	if err != nil || cfg.endpoint != "" {
		t.Fatalf("join must be disabled by default: %+v, %v", cfg, err)
	}
	t.Setenv("HUB", "https://fallback.example")
	cfg, err = peerJoinFromEnv()
	if err != nil || cfg.endpoint != "https://fallback.example/peers" || cfg.host == "" {
		t.Fatalf("HUB alias/hostname fallback: %+v, %v", cfg, err)
	}
	t.Setenv("PEER_HUB", "https://hub.example/prefix/")
	t.Setenv("PEER_HOST", `worker-"one`)
	t.Setenv("PEER_TOKEN", "test-token")
	cfg, err = peerJoinFromEnv()
	if err != nil || cfg.endpoint != "https://hub.example/prefix/peers" || cfg.host != `worker-"one` || cfg.token != "test-token" {
		t.Fatalf("explicit hub must win and preserve path prefix: %+v, %v", cfg, err)
	}
	for _, hub := range []string{"relative", "ftp://hub", "http://", "https://user:secret@hub", "http://hub?token=secret", "http://hub#secret", "http://hub?"} {
		t.Setenv("PEER_HUB", hub)
		if _, err := peerJoinFromEnv(); err == nil || strings.Contains(err.Error(), "secret") {
			t.Errorf("must reject invalid hub without exposing secrets: %q, %v", hub, err)
		}
	}
}

// Exercise the real producer and rendezvous handler over HTTP, including auth,
// payload fidelity, freshness, retries after a transient 503, and cancellation.
func TestPeerJoinRoundTripAndRetry(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // federation must not scan the operator's real sensor logs
	const host = `codex-test-"peer`
	peerMu.Lock()
	previous := peers
	peers = map[string]*peer{}
	peerMu.Unlock()
	t.Cleanup(func() {
		peerMu.Lock()
		peers = previous
		peerMu.Unlock()
	})
	hub := newCollector(nil)
	var attempts atomic.Int32
	accepted := make(chan struct{}, 10)
	server := httptest.NewServer(auth("test-token", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/prefix/peers" {
			t.Errorf("wrong path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost {
			if r.Header.Get("X-8-Actor") != "peer-join/"+host || r.Header.Get("Content-Type") != "application/json" {
				t.Errorf("missing heartbeat headers: %v", r.Header)
			}
			if attempts.Add(1) == 1 {
				http.Error(w, "try again", http.StatusServiceUnavailable)
				return
			}
		}
		hub.handlePeers(w, r)
		if r.Method == http.MethodPost {
			select {
			case accepted <- struct{}{}:
			default:
			}
		}
	})))
	defer server.Close()
	c := newCollector(nil)
	c.manifest["tab2"] = &tabRec{UID: 2, Ctx: "tab2", Status: "closed"}
	c.manifest["tab1"] = &tabRec{UID: 1, Ctx: "tab1", Status: "live", URL: "https://example.test/"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		c.peerJoinLoop(ctx, peerJoinConfig{server.URL + "/prefix/peers", host, "test-token"}, 10*time.Millisecond)
	}()
	defer func() { cancel(); <-done }()
	for i := 0; i < 2; i++ {
		select {
		case <-accepted:
		case <-time.After(3 * time.Second):
			t.Fatal("heartbeat did not recover and repeat")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("heartbeat loop did not stop on cancellation")
	}
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/prefix/peers", nil)
	request.Header.Set("X-8-Token", "test-token")
	resp, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var roster struct {
		N     int `json:"n"`
		Peers []struct {
			peer
			Stale bool `json:"stale"`
		} `json:"peers"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&roster); err != nil {
		t.Fatal(err)
	}
	if roster.N != 1 || len(roster.Peers) != 1 || roster.Peers[0].Host != host || roster.Peers[0].Stale || roster.Peers[0].At == "" {
		t.Fatalf("bad roster: %+v", roster)
	}
	p := roster.Peers[0]
	var hr hostRes
	if err := json.Unmarshal(p.HostRes, &hr); err != nil || hr.CPUs < 1 || hr.OS == "" {
		t.Fatalf("missing host metrics: %s (%v)", p.HostRes, err)
	}
	var manifest struct {
		Total, Live int
		Tabs        []tabRec
	}
	if err := json.Unmarshal(p.Manifest, &manifest); err != nil || manifest.Total != 2 || manifest.Live != 1 || len(manifest.Tabs) != 2 || manifest.Tabs[0].Ctx != "tab1" {
		t.Fatalf("wrong manifest: %s (%v)", p.Manifest, err)
	}
}

func TestPeerJoinRejectsFailuresAndRedirects(t *testing.T) {
	var leaked atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked.Store(true)
	}))
	defer destination.Close()
	for _, status := range []int{http.StatusUnauthorized, http.StatusServiceUnavailable, http.StatusTemporaryRedirect} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", destination.URL)
			w.WriteHeader(status)
		}))
		client := newPeerJoinClient()
		err := sendPeerHeartbeat(context.Background(), client, peerJoinConfig{server.URL, "test", "secret"}, peer{Host: "test"})
		client.CloseIdleConnections()
		server.Close()
		if err == nil {
			t.Errorf("HTTP %d must be a failed heartbeat", status)
		}
	}
	if leaked.Load() {
		t.Fatal("heartbeat followed redirect; peer token could leak")
	}
}

func TestPeerJoinCancelsInflightRequest(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	client := newPeerJoinClient()
	defer client.CloseIdleConnections()
	go func() {
		done <- sendPeerHeartbeat(ctx, client, peerJoinConfig{endpoint: server.URL}, peer{Host: "test"})
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled heartbeat succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("cancelled heartbeat remained blocked")
	}
}
