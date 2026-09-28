package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestHandleIdentity_Guard witnesses the anti-spoofing property that 6037bbd
// (codex making itself a first-class mind) shipped in code but WITHOUT a test —
// the highest-stakes, weakest-evidence change per the witnessed-vs-asserted
// review. The guard: a mind may declare an identity ONLY for a pane that
// actually holds its live session uuid. So a Codex actor cannot claim a Claude
// pane's identity (or vice-versa), and ledger rows stay attributable to the mind
// that produced them — the thing that keeps two model-families sharing one
// witness from planting beliefs in each other.
func TestHandleIdentity_Guard(t *testing.T) {
	c := newCollector(nil)
	const uuid = "01a0e7a4-0275-7af3-9388-f657051d2bb6" // >=32 chars, >=4 dashes

	post := func(body map[string]any) int {
		b, _ := json.Marshal(body)
		r := httptest.NewRequest(http.MethodPost, "/identity", bytes.NewReader(b))
		w := httptest.NewRecorder()
		c.handleIdentity(w, r)
		return w.Code
	}

	// malformed declarations are refused (name required, uuid must look like a session uuid)
	if got := post(map[string]any{"name": "", "uuid": uuid}); got != http.StatusBadRequest {
		t.Errorf("empty name: want 400, got %d", got)
	}
	if got := post(map[string]any{"name": "x", "uuid": "too-short"}); got != http.StatusBadRequest {
		t.Errorf("short uuid: want 400, got %d", got)
	}

	// THE GUARD: claiming a pane that does not hold this uuid is refused with 409.
	// %999 holds no live session, so paneUUIDMap()[%999] != uuid — this is the
	// exact "one family can't claim the other's pane" case.
	if got := post(map[string]any{"name": "impostor", "uuid": uuid, "pane": "%999"}); got != http.StatusConflict {
		t.Errorf("pane-spoofing must be refused with 409, got %d", got)
	}

	// a uuid-only declaration (claims no pane) is accepted — the pane guard only
	// applies when a pane is claimed, and identity-by-uuid is always the mind's own.
	if got := post(map[string]any{"name": "codex", "uuid": uuid}); got != http.StatusOK {
		t.Errorf("uuid-only declaration: want 200, got %d", got)
	}
}
