package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	tuEight = "88888888-0000-4000-8000-000000000088" // %8
	tuPmf   = "13131313-0000-4000-8000-000000000013" // real-length stand-in
	tuWork  = "29292929-0000-4000-8000-000000000029" // the work session (%29), never opted in
)

func TestParseAllowlist(t *testing.T) {
	set, ignored := parseAllowlistReport("# comment\n" + tuEight + "  # %8\n\n  " + strings.ToUpper(tuPmf) + "   \n66666666-0000\nshort\n")
	if len(set) != 2 || !allowed(set, strings.ToUpper(tuEight)) || !allowed(set, tuPmf) {
		t.Fatalf("parse: %+v", set)
	}
	// a prefix is NOT an entry: it is reported, never matched (exact-match registry)
	if len(ignored) != 2 || ignored[0] != "66666666-0000" || allowed(set, "66666666-0000") || allowed(set, "66666666-0000-4000-8000-000000000066") {
		t.Fatalf("short tokens must be ignored and visible: %v", ignored)
	}
	if allowed(set, "") || allowed(set, tuWork) || allowed(map[string]bool{}, tuEight) {
		t.Fatal("fail-closed violated")
	}
	if !isFullUUID(tuEight) || isFullUUID(tuEight[:35]) || isFullUUID(strings.Replace(tuEight, "-", "x", 1)) {
		t.Fatal("isFullUUID shape check")
	}
}

func TestPickReroutesHonoursAllowlist(t *testing.T) {
	now := time.Date(2026, 11, 24, 19, 0, 0, 0, time.UTC)
	old := now.Add(-3 * time.Hour).Format(time.RFC3339)
	items := []workItem{
		{ID: 100, Text: "claim", Status: "done", By: "%6"},
		{ID: 101, Text: "[verify #100] falsify", Status: "todo", By: "auto", Assignee: "conductor", TS: old},
	}
	// a NAMED, IDLE work session that never opted in must never be picked
	minds := []mindView{
		{Name: "work", Pane: "%29", UUID: tuWork, State: "idle"},
		{Name: "pmf", Pane: "%13", UUID: tuPmf, State: "idle"},
	}
	set := parseAllowlist(tuPmf + "\n")
	got := pickReroutes(items, filterAllowed(minds, set), now, time.Hour, nil, nil)
	if len(got) != 1 || got[0].To != "%13" {
		t.Fatalf("only the opted-in mind may receive: %+v", got)
	}
	if r := pickReroutes(items, filterAllowed(minds, map[string]bool{}), now, time.Hour, nil, nil); len(r) != 0 {
		t.Fatalf("empty registry -> silence, got %+v", r)
	}
}

// The POST is the consent act, so it is the surface to lock: LAN callers,
// prefixes and non-live sessions are all refused; the file is written whole.
func TestAllowHandlerEnforces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "allow.txt")
	t.Setenv("EIGHT_ALLOCATOR_ALLOWLIST", path)
	orig := paneUUIDMap
	paneUUIDMap = func() map[string]string { return map[string]string{"%8": tuEight} }
	defer func() { paneUUIDMap = orig }()
	c := newCollector(nil)

	post := func(remote, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/allocator/allow", strings.NewReader(body))
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		c.handleAllocatorAllow(rec, req)
		return rec
	}
	if rec := post("10.0.0.9:4242", `{"uuid":"`+tuEight+`","by":"lan"}`); rec.Code != 403 {
		t.Fatalf("LAN caller must be refused, got %d %s", rec.Code, rec.Body)
	}
	if rec := post("127.0.0.1:4242", `{"uuid":"88888888-0000","by":"%8"}`); rec.Code != 400 {
		t.Fatalf("prefix must be refused, got %d %s", rec.Code, rec.Body)
	}
	if rec := post("127.0.0.1:4242", `{"uuid":"`+tuWork+`","by":"someone"}`); rec.Code != 403 {
		t.Fatalf("non-live session must be refused, got %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("refusals must not create the registry")
	}
	if rec := post("[::1]:4242", `{"uuid":"`+strings.ToUpper(tuEight)+`","by":"%8"}`); rec.Code != 200 {
		t.Fatalf("live loopback self-registration must succeed, got %d %s", rec.Code, rec.Body)
	}
	if !allowed(loadAllowlist(), tuEight) {
		t.Fatal("registered uuid not readable back")
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("tmp file left behind: %v", ents)
	}
	if rec := post("127.0.0.1:4242", `{"uuid":"`+tuEight+`","remove":true,"by":"%8"}`); rec.Code != 200 || len(loadAllowlist()) != 0 {
		t.Fatalf("withdraw: %d %s set=%v", rec.Code, rec.Body, loadAllowlist())
	}
	// GET reports hand-typed prefixes as ignored instead of silently dropping them
	os.WriteFile(path, []byte(tuEight+"\n66666666-0000 # prefix\n"), 0o644)
	req := httptest.NewRequest(http.MethodGet, "/allocator/allow", nil)
	rec := httptest.NewRecorder()
	c.handleAllocatorAllow(rec, req)
	if !strings.Contains(rec.Body.String(), `"ignored":["66666666-0000"]`) || !strings.Contains(rec.Body.String(), `"n":1`) {
		t.Fatalf("GET report: %s", rec.Body)
	}
}
