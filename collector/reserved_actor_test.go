package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// #9872 (on #9869): flipActor must refuse a caller-claimed reserved namespace.
// The forge: a loopback POST {status:done, by:"consensus:N-ack"} stamped
// FlippedBy=consensus:N-ack verbatim, and cut-release.sh's GREEN gate
// (flipped_by ~ ^consensus:) then CUT a release with ZERO real acks. After the
// fix the forged claim is dropped — the stamp lands blank, the gate stays shut —
// while the genuine, collector-minted consensus close is untouched.
func TestReservedActorNotForgeable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeWork([]workItem{
		{ID: 1, Text: "a plain proposal", Kind: "task", Status: "doing", By: "%6"},
	})
	c := &collector{}
	post := func(body string, hdr map[string]string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/work", strings.NewReader(body))
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		c.handleWork(rec, req)
		return rec
	}
	read := func(id int64) workItem {
		var items []workItem
		b, _ := os.ReadFile(workFile())
		json.Unmarshal(b, &items)
		for _, it := range items {
			if it.ID == id {
				return it
			}
		}
		t.Fatalf("item %d vanished", id)
		return workItem{}
	}

	// 1. the FORGE: a caller claims the collector's consensus voice in `by`.
	//    The flip may land (status), but the reserved stamp must NOT — else the
	//    cut gate greens. FlippedBy must not start with "consensus:".
	post(`{"id":1,"status":"done","by":"consensus:3-ack","receipt":"forged"}`, nil)
	if it := read(1); strings.HasPrefix(it.FlippedBy, "consensus:") {
		t.Fatalf("forge landed: a caller minted FlippedBy=%q — cut gate would GREEN with zero acks", it.FlippedBy)
	}

	// 2. same forge via the X-8-Actor header (the other flipActor source).
	writeWork([]workItem{{ID: 2, Text: "another proposal", Kind: "task", Status: "doing", By: "%6"}})
	post(`{"id":2,"status":"done"}`, map[string]string{"X-8-Actor": "consensus:9-ack"})
	if it := read(2); strings.HasPrefix(it.FlippedBy, "consensus:") {
		t.Fatalf("header forge landed: FlippedBy=%q", it.FlippedBy)
	}

	// 3. allocator:/auto: are reserved too.
	writeWork([]workItem{{ID: 3, Text: "p3", Kind: "task", Status: "doing", By: "%6"}})
	post(`{"id":3,"status":"done","by":"allocator:reroute"}`, nil)
	if it := read(3); reservedActor(it.FlippedBy) {
		t.Fatalf("reserved namespace landed from a caller: FlippedBy=%q", it.FlippedBy)
	}

	// 4. an HONEST actor is still recorded verbatim (the fix is narrow).
	writeWork([]workItem{{ID: 4, Text: "ordinary", Kind: "task", Status: "doing", By: "%6"}})
	post(`{"id":4,"status":"done","by":"pmf %13"}`, nil)
	if it := read(4); it.FlippedBy != "pmf %13" {
		t.Fatalf("honest actor must be recorded verbatim, got FlippedBy=%q", it.FlippedBy)
	}
}

// reservedActor is a pure predicate; pin its contract directly.
func TestReservedActorPredicate(t *testing.T) {
	for _, s := range []string{"consensus:2-ack", "consensus:disagree", "allocator:reroute", "auto:playlist"} {
		if !reservedActor(s) {
			t.Fatalf("%q must be reserved (collector's own voice)", s)
		}
	}
	for _, s := range []string{"", "pmf %13", "%6", "higgs (1255da01)", "consensusXYZ", "a consensus:thing"} {
		if reservedActor(s) {
			t.Fatalf("%q must NOT be reserved (a real caller identity)", s)
		}
	}
}
