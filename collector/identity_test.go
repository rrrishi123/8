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

	// Seed a KNOWN binding: pane %7 is held by Claude's live session.
	const claudeUUID = "11111111-2222-3333-4444-555555555555"
	orig := paneUUIDMap
	paneUUIDMap = func() map[string]string { return map[string]string{"%7": claudeUUID} }
	defer func() { paneUUIDMap = orig }()

	// THE CASE THAT MATTERS for two families sharing one witness: Codex, with its
	// OWN uuid, tries to declare %7 — the pane Claude actually holds. %7 exists,
	// but its live uuid is Claude's, not Codex's, so it is refused with 409. This
	// is the row-attribution guarantee: one family cannot claim the other's pane.
	if got := post(map[string]any{"name": "codex", "uuid": uuid, "pane": "%7"}); got != http.StatusConflict {
		t.Errorf("codex claiming Claude's live pane (%%7) must be refused with 409, got %d", got)
	}

	// a pane NOBODY holds is likewise unclaimable (the guard's edge case).
	if got := post(map[string]any{"name": "impostor", "uuid": uuid, "pane": "%999"}); got != http.StatusConflict {
		t.Errorf("claiming an unheld pane must be refused with 409, got %d", got)
	}

	// the mind that ACTUALLY holds the pane may declare it: Claude declares %7
	// with the matching uuid -> accepted.
	if got := post(map[string]any{"name": "claude", "uuid": claudeUUID, "pane": "%7"}); got != http.StatusOK {
		t.Errorf("the holder declaring its own pane: want 200, got %d", got)
	}

	// a uuid-only declaration (claims no pane) is accepted — the pane guard only
	// applies when a pane is claimed, and identity-by-uuid is always the mind's own.
	if got := post(map[string]any{"name": "codex", "uuid": uuid}); got != http.StatusOK {
		t.Errorf("uuid-only declaration: want 200, got %d", got)
	}
}
