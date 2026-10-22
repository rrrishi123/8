package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRelayQueueRoundTrip pins the #1267 primitive: a command enqueued for a peer is
// handed back exactly once (drained), and the results a peer carries back are recorded
// and bounded.
func TestRelayQueueRoundTrip(t *testing.T) {
	const host = "unit-peer"
	relayMu.Lock()
	delete(relayPending, host)
	delete(relayResults, host)
	relayMu.Unlock()

	relayEnqueue(host, relayCmd{ID: "a", Method: "get", Path: "/health"})
	relayEnqueue(host, relayCmd{ID: "b", Method: "get", Path: "/panes"})

	got := relayDrain(host)
	if len(got) != 2 || got[0].ID != "a" || got[1].ID != "b" {
		t.Fatalf("drain = %v; want [a b] in order", got)
	}
	if again := relayDrain(host); len(again) != 0 {
		t.Fatalf("second drain must be empty (commands pulled once), got %d", len(again))
	}

	relayRecordResults(host, []relayResult{{ID: "a", OK: true, Code: 200}})
	relayMu.Lock()
	n := len(relayResults[host])
	relayMu.Unlock()
	if n != 1 {
		t.Fatalf("recorded results = %d; want 1", n)
	}

	// bound: 150 results collapse to the most recent 100
	many := make([]relayResult, 150)
	for i := range many {
		many[i] = relayResult{ID: "x"}
	}
	relayRecordResults(host, many)
	relayMu.Lock()
	n = len(relayResults[host])
	relayMu.Unlock()
	if n != 100 {
		t.Fatalf("results must be bounded to 100, got %d", n)
	}
}

// TestRelayExecuteLoopback pins the PEER side: a pulled command runs against the peer's
// OWN collector (loopback) and the response is captured as the result. The hub never
// reaches in — this is the only direction the networking allows.
func TestRelayExecuteLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			_, _ = w.Write([]byte(`{"alive":true}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"nope"}`))
	}))
	defer srv.Close()
	client := srv.Client()

	ok := relayExecute(srv.URL, client, relayCmd{ID: "1", Method: "get", Path: "/health"})
	if !ok.OK || ok.Code != 200 {
		t.Fatalf("health result = %+v; want ok/200", ok)
	}
	var body struct {
		Alive bool `json:"alive"`
	}
	_ = json.Unmarshal(ok.Body, &body)
	if !body.Alive {
		t.Fatalf("result body did not carry the peer's response: %s", ok.Body)
	}

	miss := relayExecute(srv.URL, client, relayCmd{ID: "2", Method: "get", Path: "/missing"})
	if miss.OK || miss.Code != 404 {
		t.Fatalf("missing path result = %+v; want not-ok/404", miss)
	}

	bad := relayExecute(srv.URL, client, relayCmd{ID: "3", Method: "delete", Path: "/health"})
	if bad.OK || bad.Error == "" {
		t.Fatalf("unknown method must fail with an error, got %+v", bad)
	}
}
