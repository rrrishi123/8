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

// #9795 (finding #9757; synthesis of #29/#31/#33 per #9805): done must mean
// witnessed-done. WHAT-gate: a [verify #N]/claimable task may not close without
// a receipt (posted text verdict counts), whoever closes it. WHO-gate: a
// self-close also needs verified_by — a different mind whose OWN agree ack is
// already on the item (off-host review: a name typed by the closer is a relay, not a
// receipt). Ordinary cross-closes, sweeps and records stay exactly as they were.
func TestDoneReceiptGates(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".8"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeWork([]workItem{
		{ID: 1, Text: "build the thing", Status: "doing", By: "%12", Kind: "task"},
		{ID: 2, Text: "note on the record", Status: "todo", By: "%12", Kind: "record"},
		{ID: 3, Text: "tidy the README", Status: "doing", By: "%12", Kind: "task"},
		{ID: 4, Text: "[verify #98] falsify/verify that Y holds", Status: "doing", By: "auto", Kind: "task"},
		{ID: 5, Text: "P3 brew: build the formula", Status: "doing", By: "%6", Kind: "task", Claimable: true},
		{ID: 6, Text: "[verify #97] stale, superseded", Status: "todo", By: "auto", Kind: "task", Claimable: true},
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

	// WHO-gate: bare self-close refused, item untouched
	if rec := post(`{"id":1,"status":"done","by":"%12"}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "verified_by") {
		t.Fatalf("bare self-close must be refused, got %d %s", rec.Code, rec.Body)
	}
	if it := item(1); it.Status != "doing" || it.Receipt != "" {
		t.Fatalf("refused flip must not mutate: %+v", it)
	}
	// WHO-gate: naming a verifier who never acked = a relay, refused (off-host review)
	if rec := post(`{"id":1,"status":"done","by":"%12","verified_by":"%8","receipt":"8@6f34f51 green"}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "relay") {
		t.Fatalf("unanchored verifier must be refused, got %d %s", rec.Code, rec.Body)
	}
	// WHO-gate: the verifier's own spelling of the closer is still the closer
	if rec := post(`{"id":1,"status":"done","by":"%12","verified_by":"philo %12","receipt":"x"}`); rec.Code != 400 {
		t.Fatalf("self-verification under another spelling must be refused, got %d %s", rec.Code, rec.Body)
	}
	// WHO-gate: with the verifier's own agree ack on the item, the close lands
	if rec := post(`{"id":1,"ack":"agree","by":"%8"}`); rec.Code != 200 {
		t.Fatalf("ack post failed: %d %s", rec.Code, rec.Body)
	}
	if rec := post(`{"id":1,"status":"done","by":"%12","verified_by":"%8","receipt":"8@6f34f51 build+vet+test green"}`); rec.Code != 200 {
		t.Fatalf("anchored self-close must land, got %d %s", rec.Code, rec.Body)
	}
	if it := item(1); it.Status != "done" || it.VerifiedBy != "%8" || !strings.Contains(it.Receipt, "6f34f51") {
		t.Fatalf("receipt not persisted: %+v", it)
	}

	// WHAT-gate: verify task, bare cross-close refused (the dropped-verdict class)
	if rec := post(`{"id":4,"status":"done","by":"%13"}`); rec.Code != 400 {
		t.Fatalf("receiptless verify close must be refused, got %d %s", rec.Code, rec.Body)
	}
	// WHAT-gate: the posted text verdict IS the receipt (pmf #31 idiom)
	if rec := post(`{"id":4,"status":"done","by":"%13","text":"[VERIFY #98] PARTIALLY holds; breaks at Z"}`); rec.Code != 200 {
		t.Fatalf("text-verdict close must land, got %d %s", rec.Code, rec.Body)
	}
	if it := item(4); !strings.Contains(it.Receipt, "PARTIALLY holds") {
		t.Fatalf("text verdict must become the receipt: %+v", it)
	}
	// WHAT-gate: claimable cross-close needs a receipt too
	if rec := post(`{"id":5,"status":"done","by":"%13"}`); rec.Code != 400 {
		t.Fatalf("receiptless claimable close must be refused, got %d %s", rec.Code, rec.Body)
	}

	// untouched paths: ordinary cross-close, sweep, record
	if rec := post(`{"id":3,"status":"done","by":"%8"}`); rec.Code != 200 {
		t.Fatalf("ordinary cross-close must stay free, got %d %s", rec.Code, rec.Body)
	}
	if rec := post(`{"id":6,"status":"done","by":"%6","sweep":true}`); rec.Code != 200 {
		t.Fatalf("declared sweep must stay exempt, got %d %s", rec.Code, rec.Body)
	}
	if rec := post(`{"id":2,"status":"done","by":"%12"}`); rec.Code != 200 {
		t.Fatalf("record close must stay exempt, got %d %s", rec.Code, rec.Body)
	}
}
