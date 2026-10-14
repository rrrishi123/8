package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// The mind decides: an offer never flips the item; reading is the receipt;
// take = doing+acked by the mind; decline = back to the pool, on the record.
// Runs against a temp HOME with a dead pane id, so nothing is ever typed.
func TestInboxOfferTakeDecline(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("EIGHT_NO_SUMMON", "1")
	os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
	items := []workItem{
		{ID: 1, Text: "first", Status: "todo", By: "%9", Assignee: "%99"},
		{ID: 2, Text: "second", Status: "todo", By: "%9", Assignee: "%99"},
		{ID: 3, Text: "third", Status: "todo", By: "%9", Assignee: "%99"},
	}
	writeWork(items)
	c := &collector{}
	if !c.offer(items[0], "test") || !c.offer(items[1], "test") {
		t.Fatal("offer to a known pane id must succeed")
	}
	if !c.offer(items[0], "again") {
		t.Fatal("re-offer is a dedup, still ok")
	}
	if n := inboxPendingForPane("%99", map[string]string{}); n != 2 {
		t.Fatalf("pending=%d want 2 (deduped)", n)
	}
	read := func() []workItem { // fresh slice each time: json reuses elements, stale omitempty fields survive otherwise
		var out []workItem
		b, _ := os.ReadFile(workFile())
		json.Unmarshal(b, &out)
		return out
	}
	after := read()
	if after[0].Status != "todo" || after[1].Status != "todo" {
		t.Fatal("an offer must NOT flip the item — the mind decides")
	}
	// GET = delivered receipt
	rr := httptest.NewRecorder()
	c.handleInbox(rr, httptest.NewRequest(http.MethodGet, "/inbox?pane=%2599", nil))
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"n":2`) {
		t.Fatalf("GET inbox: %d %s", rr.Code, rr.Body.String())
	}
	after = read()
	if after[0].DeliveredAt == "" || after[0].DeliveredTo != "%99" {
		t.Fatalf("reading must stamp delivery: %+v", after[0])
	}
	// take #1
	rr = httptest.NewRecorder()
	c.handleInbox(rr, httptest.NewRequest(http.MethodPost, "/inbox", strings.NewReader(`{"id":1,"action":"take","pane":"%99","by":"tester"}`)))
	if rr.Code != 200 {
		t.Fatalf("take: %d %s", rr.Code, rr.Body.String())
	}
	// decline #2
	rr = httptest.NewRecorder()
	c.handleInbox(rr, httptest.NewRequest(http.MethodPost, "/inbox", strings.NewReader(`{"id":2,"action":"decline","pane":"%99","by":"tester"}`)))
	if rr.Code != 200 {
		t.Fatalf("decline: %d %s", rr.Code, rr.Body.String())
	}
	after = read()
	if after[0].Status != "doing" || after[0].AckedBy != "tester" || after[0].FlippedBy != "tester" {
		t.Fatalf("take must be doing+acked by the mind: %+v", after[0])
	}
	if after[1].Status != "todo" || after[1].Assignee != "" || after[1].DeclinedBy != "tester" {
		t.Fatalf("decline must return to pool on the record: %+v", after[1])
	}
	if n := inboxPendingForPane("%99", map[string]string{}); n != 0 {
		t.Fatalf("inbox must be empty after decisions, got %d", n)
	}
	// a flip via the ledger resolves a standing offer too (no phantom WIP)
	c.offer(items[2], "third")
	if inboxPendingForPane("%99", map[string]string{}) != 1 {
		t.Fatal("offer of #3 should be pending")
	}
	rr = httptest.NewRecorder()
	c.handleWork(rr, httptest.NewRequest(http.MethodPost, "/work", strings.NewReader(`{"id":3,"status":"done","by":"tester","sweep":true}`)))
	if rr.Code != 200 || inboxPendingForPane("%99", map[string]string{}) != 0 {
		t.Fatalf("closing via /work must drop the offer: code=%d pending=%d", rr.Code, inboxPendingForPane("%99", map[string]string{}))
	}
	// bad request
	rr = httptest.NewRecorder()
	c.handleInbox(rr, httptest.NewRequest(http.MethodPost, "/inbox", strings.NewReader(`{"id":1,"action":"steal"}`)))
	if rr.Code != 400 {
		t.Fatalf("bad action must 400, got %d", rr.Code)
	}
	// an unresolvable assignee is a failed offer, witnessed
	if c.offer(workItem{ID: 3, Assignee: ""}, "x") {
		t.Fatal("empty assignee cannot be offered")
	}
}
