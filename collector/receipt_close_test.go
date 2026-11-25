package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// #9757: "done" is a mutable flag; a close must carry a verification receipt so
// done means witnessed-done, not claimed-done. The gate is narrow: a [verify #N]
// task and a claimable task may not close without a receipt (verdict text counts);
// ordinary tasks and declared sweeps close as before.
func TestCloseRequiresReceipt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeWork([]workItem{
		{ID: 1, Text: "[verify #99] falsify/verify that X holds", Kind: "task", Status: "doing"},
		{ID: 2, Text: "P3 brew: build the formula", Kind: "task", Status: "doing", Claimable: true},
		{ID: 3, Text: "tidy the README", Kind: "task", Status: "doing"},
		{ID: 4, Text: "[verify #98] falsify/verify that Y holds", Kind: "task", Status: "doing"},
	})
	c := &collector{}
	post := func(body string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		c.handleWork(rec, httptest.NewRequest(http.MethodPost, "/work", strings.NewReader(body)))
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

	// 1. verify task, bare close (no receipt, no text) -> 400, stays doing
	if rec := post(`{"id":1,"status":"done","by":"pmf"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("verify close without receipt must be 400, got %d %s", rec.Code, rec.Body)
	}
	if it := read(1); it.Status != "doing" || it.Receipt != "" {
		t.Fatalf("rejected close must not flip: status=%q receipt=%q", it.Status, it.Receipt)
	}

	// 2. verify task, explicit receipt -> 200, done, receipt stored
	if rec := post(`{"id":1,"status":"done","by":"pmf","receipt":"VERDICT: holds, checked at abc1234"}`); rec.Code != http.StatusOK {
		t.Fatalf("verify close WITH receipt must be 200, got %d %s", rec.Code, rec.Body)
	}
	if it := read(1); it.Status != "done" || it.Receipt != "VERDICT: holds, checked at abc1234" {
		t.Fatalf("receipt must be captured: status=%q receipt=%q", it.Status, it.Receipt)
	}

	// 3. verify task, verdict as TEXT (no explicit receipt field) -> 200, text becomes receipt
	if rec := post(`{"id":4,"status":"done","by":"pmf","text":"[VERIFY #98] PARTIALLY holds; breaks at Z"}`); rec.Code != http.StatusOK {
		t.Fatalf("verify close with text verdict must be 200, got %d %s", rec.Code, rec.Body)
	}
	if it := read(4); it.Status != "done" || !strings.Contains(it.Receipt, "PARTIALLY holds") {
		t.Fatalf("text verdict must become the receipt: status=%q receipt=%q", it.Status, it.Receipt)
	}

	// 4. claimable task, bare close -> 400
	if rec := post(`{"id":2,"status":"done","by":"pmf"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("claimable close without receipt must be 400, got %d %s", rec.Code, rec.Body)
	}
	if it := read(2); it.Status != "doing" {
		t.Fatalf("rejected claimable close must not flip: status=%q", it.Status)
	}

	// 5. ordinary task (not verify, not claimable), bare close -> 200 (gate is narrow)
	if rec := post(`{"id":3,"status":"done","by":"pmf"}`); rec.Code != http.StatusOK {
		t.Fatalf("ordinary task close without receipt must stay 200, got %d %s", rec.Code, rec.Body)
	}
	if it := read(3); it.Status != "done" || it.Receipt != "" {
		t.Fatalf("ordinary close: status=%q receipt=%q, want done/empty", it.Status, it.Receipt)
	}

	// 6. declared SWEEP on a claimable task, no receipt -> 200 (bookkeeping is exempt)
	writeWork([]workItem{{ID: 5, Text: "[verify #97] stale, superseded", Kind: "task", Status: "todo", Claimable: true}})
	if rec := post(`{"id":5,"status":"done","by":"%6","sweep":true}`); rec.Code != http.StatusOK {
		t.Fatalf("declared sweep must be exempt, got %d %s", rec.Code, rec.Body)
	}
	if it := read(5); it.Status != "done" {
		t.Fatalf("sweep close must succeed: status=%q", it.Status)
	}
}
