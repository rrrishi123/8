package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// #9728 wiring: Claimable must be settable through the real POST path — on create
// and via adjust — else slice-3 can never see an opted-in item (the acceptance
// test missed this by constructing workItem{Claimable:true} directly in memory).
func TestClaimableSetViaPOST(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeWork(nil)
	c := &collector{}
	post := func(body string) {
		c.handleWork(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/work", strings.NewReader(body)))
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
		t.Fatalf("item %d not found", id)
		return workItem{}
	}
	// create with claimable:true
	post(`{"text":"[pool] a real claimable task","kind":"task","claimable":true,"by":"%6"}`)
	if it := read(1); !it.Claimable {
		t.Fatalf("create claimable:true must persist, got Claimable=%v", it.Claimable)
	}
	// a plain create is NOT claimable (fail-closed default)
	post(`{"text":"an ordinary task","kind":"task","by":"%6"}`)
	if it := read(2); it.Claimable {
		t.Fatalf("default must be non-claimable, got Claimable=%v", it.Claimable)
	}
	// adjust: mark #2 claimable after the fact, then un-mark
	post(`{"id":2,"claimable":true,"by":"%6"}`) // the author stamps (D2: a blank `by` is refused below)
	if it := read(2); !it.Claimable {
		t.Fatalf("adjust claimable:true must set it, got %v", it.Claimable)
	}
	post(`{"id":2,"claimable":false,"by":"%6"}`)
	if it := read(2); it.Claimable {
		t.Fatalf("adjust claimable:false must clear it, got %v", it.Claimable)
	}
	// %8 #9759 D2: only the author (any spelling), operator or make_tasks may stamp; the stamper is recorded
	post(`{"id":2,"claimable":true,"by":"%12 (7261eb6b-b097-46bb-93d0-71cc54d95684)"}`)
	if it := read(2); it.Claimable {
		t.Fatalf("a non-author must not stamp another mind's item claimable, got %v", it.Claimable)
	}
	post(`{"id":2,"claimable":true}`) // blank by: refused
	if it := read(2); it.Claimable {
		t.Fatalf("blank `by` must not stamp, got %v", it.Claimable)
	}
	post(`{"id":2,"claimable":true,"by":"conductor %6 (12444bc8)"}`) // author, different spelling: same ackKey? %6 pane token
	if it := read(2); !it.Claimable || it.ClaimableBy != "conductor %6 (12444bc8)" {
		t.Fatalf("author under another spelling must stamp and be recorded, got claimable=%v by=%q", it.Claimable, it.ClaimableBy)
	}
	post(`{"id":2,"claimable":false,"by":"operator"}`)
	if it := read(2); it.Claimable || it.ClaimableBy != "operator" {
		t.Fatalf("operator must be able to un-stamp, got claimable=%v by=%q", it.Claimable, it.ClaimableBy)
	}
	if it := read(1); it.ClaimableBy != "%6" {
		t.Fatalf("create path must record the stamper, got %q", it.ClaimableBy)
	}
}

// rememberDecline keeps every decliner once, de-duped by identity not spelling.
func TestRememberDeclineSet(t *testing.T) {
	var it workItem
	rememberDecline(&it, "sibling-1 (818c3f5e)")
	rememberDecline(&it, "%7")
	rememberDecline(&it, "sibling-1 (818c3f5e-997c-4c05-a9e3-61d87d1d5057)") // same mind, third spelling
	rememberDecline(&it, "%12 (7261eb6b)")
	if it.DeclinedBy != "%12 (7261eb6b)" {
		t.Fatalf("DeclinedBy must track the latest, got %q", it.DeclinedBy)
	}
	if len(it.Declined) != 3 { // sibling-1 by hex, %7 by pane token (different keys: hex vs pane), philo by hex
		t.Fatalf("expected 3 distinct keys, got %v", it.Declined)
	}
}
