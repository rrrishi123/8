package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// philo #9658: handleWait's doc comment promised loopback-only writes but the
// POST path never checked, so a tokenless LAN caller could clear or replace a
// mind's declared end-state. Also: a work_match regex is validated at declare,
// not silently at the first beat.
func TestWaitHandlerLoopbackAndRegex(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".8", "ledger"), 0o755); err != nil {
		t.Fatal(err)
	}
	orig := paneUUIDMap
	paneUUIDMap = func() map[string]string { return map[string]string{"%8": tuEight} }
	defer func() { paneUUIDMap = orig }()
	c := newCollector(nil)

	post := func(remote, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/wait", strings.NewReader(body))
		req.RemoteAddr = remote
		rec := httptest.NewRecorder()
		c.handleWait(rec, req)
		return rec
	}
	good := `{"pane":"%8","question":"did it land?","predicates":[{"kind":"work_match","regex":"^landed"}]}`
	path := waitPath("%8")

	if rec := post("10.0.0.9:4242", good); rec.Code != 403 {
		t.Fatalf("LAN declare must be refused, got %d %s", rec.Code, rec.Body)
	}
	if rec := post("garbage", good); rec.Code != 403 {
		t.Fatalf("unparsable remote must fail closed, got %d %s", rec.Code, rec.Body)
	}
	if rec := post("127.0.0.1:4242", `{"pane":"%8","question":"q","predicates":[{"kind":"work_match","regex":"("}]}`); rec.Code != 400 || !strings.Contains(rec.Body.String(), "bad regex") {
		t.Fatalf("bad regex must be refused at declare, got %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("refusals must not write a wait file")
	}
	if rec := post("[::1]:4242", good); rec.Code != 200 {
		t.Fatalf("loopback declare must succeed, got %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("declared wait not on disk: %v", err)
	}
	if rec := post("192.168.1.7:4242", `{"pane":"%8","clear":true}`); rec.Code != 403 {
		t.Fatalf("LAN clear must be refused, got %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("LAN clear must not remove the wait")
	}
	if rec := post("127.0.0.1:4242", `{"pane":"%8","clear":true}`); rec.Code != 200 {
		t.Fatalf("loopback clear must succeed, got %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("loopback clear must remove the wait")
	}
	// GET stays open to anyone (read-only feed)
	req := httptest.NewRequest(http.MethodGet, "/wait", nil)
	req.RemoteAddr = "10.0.0.9:4242"
	rec := httptest.NewRecorder()
	c.handleWait(rec, req)
	if rec.Code != 200 {
		t.Fatalf("GET must stay readable from anywhere, got %d", rec.Code)
	}
}
