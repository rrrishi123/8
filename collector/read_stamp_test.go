package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #9863: read-stamps — the witnessed act between delivered and acked. A read
// appends {by, ts} once per mind and mutates NOTHING else: not Status, not
// Acks/Need (reading is not agreeing), not TS (the /watchers idle clock).
// Dedupe is sameMind-folded so '%12' and '%12 (uuid)' are one reader — the
// exact spelling split that double-counted a vote on the cut proposal (#9854).
func TestReadStamps(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".8"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWork([]workItem{
		{ID: 1, Text: "a claim worth reading", Status: "todo", By: "%6", TS: "2026-11-25T12:00:00Z", Kind: "task"},
		{ID: 2, Text: "a consensus item", Status: "todo", By: "%6", TS: "2026-11-25T12:00:00Z", Kind: "task", Need: 2},
		{ID: 3, Text: "[FINDING] a record is readable too", Status: "done", By: "%8", TS: "2026-11-25T12:00:00Z", Kind: "record"},
	})
	c := newCollector(nil)

	post := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/work", strings.NewReader(body))
		req.RemoteAddr = "127.0.0.1:4242"
		rec := httptest.NewRecorder()
		c.handleWork(rec, req)
		return rec
	}
	item := func(id int64) workItem {
		b, _ := os.ReadFile(workFile())
		var items []workItem
		json.Unmarshal(b, &items)
		for _, it := range items {
			if it.ID == id {
				return it
			}
		}
		t.Fatalf("item %d missing", id)
		return workItem{}
	}

	// a signed read lands, and touches nothing but read_by
	if rec := post(`{"id":1,"read":true,"by":"%12"}`); rec.Code != 200 {
		t.Fatalf("read-stamp refused: %d %s", rec.Code, rec.Body)
	}
	it := item(1)
	if len(it.ReadBy) != 1 || it.ReadBy[0].By != "%12" || it.ReadBy[0].TS == "" {
		t.Fatalf("stamp not persisted: %+v", it.ReadBy)
	}
	if it.Status != "todo" || it.TS != "2026-11-25T12:00:00Z" || len(it.Acks) != 0 || it.FlippedBy != "" {
		t.Fatalf("a read must mutate nothing else (TS is the /watchers idle clock): %+v", it)
	}

	// re-read under another spelling of the same mind: one reader, first ts kept
	first := it.ReadBy[0].TS
	if rec := post(`{"id":1,"read":true,"by":"philo %12 (12121212-0000-4000-8000-000000000012)"}`); rec.Code != 200 {
		t.Fatalf("re-read refused: %d %s", rec.Code, rec.Body)
	}
	if it = item(1); len(it.ReadBy) != 1 || it.ReadBy[0].TS != first {
		t.Fatalf("spelling fold failed — one mind, one read (#9854 class): %+v", it.ReadBy)
	}

	// a second mind is a second reader
	if rec := post(`{"id":1,"read":true,"by":"%8"}`); rec.Code != 200 {
		t.Fatalf("second reader refused: %d %s", rec.Code, rec.Body)
	}
	if it = item(1); len(it.ReadBy) != 2 {
		t.Fatalf("want 2 readers: %+v", it.ReadBy)
	}

	// an unsigned read is refused — nothing to witness
	if rec := post(`{"id":1,"read":true}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "reader") {
		t.Fatalf("unsigned read must 400: %d %s", rec.Code, rec.Body)
	}

	// reads never count toward Need and never close a consensus item
	post(`{"id":2,"read":true,"by":"%12"}`)
	post(`{"id":2,"read":true,"by":"%8"}`)
	if it = item(2); it.Status != "todo" || len(it.Acks) != 0 || len(it.ReadBy) != 2 {
		t.Fatalf("2 reads on Need=2 must not close or ack: %+v", it)
	}

	// an ack posted with read:true is still an ack (the ack branch wins; ack=read+done)
	if rec := post(`{"id":2,"ack":"agree","read":true,"by":"%9"}`); rec.Code != 200 {
		t.Fatalf("ack+read refused: %d %s", rec.Code, rec.Body)
	}
	if it = item(2); len(it.Acks) != 1 || it.Acks[0].Verdict != "agree" {
		t.Fatalf("ack must still land when read:true rides along: %+v", it)
	}

	// records are readable too — reading is universal, unlike claiming
	if rec := post(`{"id":3,"read":true,"by":"%12"}`); rec.Code != 200 {
		t.Fatalf("record read refused: %d %s", rec.Code, rec.Body)
	}
	if it = item(3); len(it.ReadBy) != 1 || it.Status != "done" {
		t.Fatalf("record read must stamp without touching status: %+v", it)
	}
}
