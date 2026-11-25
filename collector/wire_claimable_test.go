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
	post(`{"id":2,"claimable":true}`)
	if it := read(2); !it.Claimable {
		t.Fatalf("adjust claimable:true must set it, got %v", it.Claimable)
	}
	post(`{"id":2,"claimable":false}`)
	if it := read(2); it.Claimable {
		t.Fatalf("adjust claimable:false must clear it, got %v", it.Claimable)
	}
}
