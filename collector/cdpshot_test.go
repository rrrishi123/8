package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestCdpShot covers the B4 fix: a browser-level CDP seat (a /nodes/attach seat
// holds /devtools/browser/…) returns empty on a direct Page.captureScreenshot,
// so cdpShot must enumerate targets, attach a flat-mode session, capture the
// page, and detach. A page-level seat returns data directly and cdpShot must
// NOT touch targets. The frame stand-in below is not real base64 — cdpShot only
// checks that a `data` field is non-empty.
func TestCdpShot(t *testing.T) {
	const frame = "Zm9vYmFyZnJhbWU" // stand-in; cdpShot only checks data != ""

	// ── browser-level seat: direct empty, session capture real ──
	detached := false
	browser := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case in.Method == "Page.captureScreenshot" && in.SessionID == "":
			w.Write([]byte(`{"id":1,"result":{"data":""}}`)) // no page in scope
		case in.Method == "Target.getTargets":
			w.Write([]byte(`{"id":1,"result":{"targetInfos":[` +
				`{"targetId":"WORKER","type":"worker","url":""},` +
				`{"targetId":"DT","type":"page","url":"devtools://devtools/bundled/x.html"},` +
				`{"targetId":"T1","type":"page","url":"https://claude.ai/code"}]}}`))
		case in.Method == "Target.attachToTarget":
			w.Write([]byte(`{"id":1,"result":{"sessionId":"S1"}}`))
		case in.Method == "Page.captureScreenshot" && in.SessionID == "S1":
			w.Write([]byte(`{"id":1,"result":{"data":"` + frame + `"}}`))
		case in.Method == "Target.detachFromTarget":
			detached = true
			w.Write([]byte(`{"id":1,"result":{}}`))
		default:
			w.Write([]byte(`{"id":1,"error":{"code":-32601,"message":"unexpected ` + in.Method + `"}}`))
		}
	}))
	defer browser.Close()

	c := newCollector([]broker{{id: "cdp", base: browser.URL}})
	b := broker{id: "cdp", base: browser.URL}
	sr, err := c.cdpShot(&b, "")
	if err != nil {
		t.Fatalf("browser-seat cdpShot err: %v", err)
	}
	if !cdpHasData(sr) || !strings.Contains(string(sr), frame) {
		t.Fatalf("browser-seat: expected the page frame via fallback, got %s", sr)
	}
	if !detached {
		t.Errorf("browser-seat: cdpShot should detach the flat-mode session")
	}

	// ── page-level seat: direct returns data; targets must NOT be enumerated ──
	touchedTargets := false
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		if in.Method == "Target.getTargets" {
			touchedTargets = true
		}
		w.Header().Set("Content-Type", "application/json")
		if in.Method == "Page.captureScreenshot" {
			w.Write([]byte(`{"id":1,"result":{"data":"` + frame + `"}}`))
			return
		}
		w.Write([]byte(`{"id":1,"result":{}}`))
	}))
	defer page.Close()

	c2 := newCollector([]broker{{id: "cdp", base: page.URL}})
	b2 := broker{id: "cdp", base: page.URL}
	sr2, err := c2.cdpShot(&b2, "")
	if err != nil || !cdpHasData(sr2) {
		t.Fatalf("page-seat cdpShot: err=%v hasData=%v", err, cdpHasData(sr2))
	}
	if touchedTargets {
		t.Errorf("page-seat: cdpShot must not enumerate targets when direct capture works")
	}

	// ── pinned ctx with TWO tabs: must capture the REQUESTED target, never the
	// held page (the B4 bug: /shot?context=T1 and =T2 returned identical images
	// because the browser-level capture returns the active page regardless). With
	// a ctx pinned, cdpShot must SKIP the held-page shortcut and attach to that
	// exact target. ──
	directCalled, attachedTarget := false, ""
	twoTab := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Method    string `json:"method"`
			SessionID string `json:"sessionId"`
			Params    struct {
				TargetID string `json:"targetId"`
			} `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&in)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case in.Method == "Page.captureScreenshot" && in.SessionID == "":
			directCalled = true // taking this frame for a pinned ctx would be the bug
			w.Write([]byte(`{"id":1,"result":{"data":"HELDPAGEFRAME"}}`))
		case in.Method == "Target.getTargets":
			w.Write([]byte(`{"id":1,"result":{"targetInfos":[` +
				`{"targetId":"T1","type":"page","url":"https://claude.ai/code/one"},` +
				`{"targetId":"T2","type":"page","url":"https://claude.ai/code/two"}]}}`))
		case in.Method == "Target.attachToTarget":
			attachedTarget = in.Params.TargetID
			w.Write([]byte(`{"id":1,"result":{"sessionId":"S-` + in.Params.TargetID + `"}}`))
		case in.Method == "Page.captureScreenshot" && in.SessionID == "S-T2":
			w.Write([]byte(`{"id":1,"result":{"data":"T2FRAME"}}`))
		case in.Method == "Target.detachFromTarget":
			w.Write([]byte(`{"id":1,"result":{}}`))
		default:
			w.Write([]byte(`{"id":1,"result":{"data":"WRONGFRAME"}}`))
		}
	}))
	defer twoTab.Close()

	c3 := newCollector([]broker{{id: "cdp", base: twoTab.URL}})
	b3 := broker{id: "cdp", base: twoTab.URL}
	sr3, err := c3.cdpShot(&b3, "T2")
	if err != nil {
		t.Fatalf("pinned-ctx cdpShot err: %v", err)
	}
	if directCalled {
		t.Errorf("pinned ctx: cdpShot must NOT take the browser-level held-page capture")
	}
	if attachedTarget != "T2" {
		t.Errorf("pinned ctx: expected attach to T2, attached to %q", attachedTarget)
	}
	if !strings.Contains(string(sr3), "T2FRAME") || strings.Contains(string(sr3), "HELDPAGEFRAME") {
		t.Errorf("pinned ctx: expected T2's own frame, got %s", sr3)
	}
}
