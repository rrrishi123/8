package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

const (
	tsA = "2026-11-25T10:00:00Z"
	tsB = "2026-11-25T10:05:00Z"
	tsC = "2026-11-25T10:10:00Z"
)

func socialFixture() []workItem {
	return []workItem{
		{ID: 1, Kind: "task", Status: "done", By: "%6", TS: tsA, FlippedBy: "sibling-1 (77777777)", Claimable: true},
		{ID: 2, Kind: "record", Status: "done", By: "%8 (88888888)", TS: tsB, Text: "[%8 review] AGREE on #1"},
		{ID: 3, Kind: "task", Status: "done", By: "%6", TS: tsC, Text: "proposal", Need: 2, FlippedBy: "consensus:2-ack",
			Acks: []ack{{By: "%8 (88888888-0000-4000-8000-000000000088)", Verdict: "agree", TS: tsB}, {By: "%12 (12121212)", Verdict: "agree", TS: tsC}}},
		{ID: 4, Kind: "task", Status: "todo", By: "%6", TS: tsC, Text: "pool item", DeclinedBy: "%8"},
		{ID: 5, Kind: "record", Status: "done", By: "allocator", TS: tsC, Text: "[allocator] offered claimable #4 -> %13 (author-opted-in)"},
	}
}

func TestSocialTimelineOrderAndKinds(t *testing.T) {
	ev := socialTimeline(socialFixture())
	kinds := map[string]int{}
	for i := 1; i < len(ev); i++ {
		if ev[i-1].TS > ev[i].TS {
			t.Fatalf("timeline not chronological at %d: %s > %s", i, ev[i-1].TS, ev[i].TS)
		}
	}
	for _, e := range ev {
		kinds[e.Kind]++
	}
	want := map[string]int{"task": 3, "post": 1, "done": 1, "ack": 2, "consensus": 1, "decline": 1, "route": 1}
	for k, n := range want {
		if kinds[k] != n {
			t.Errorf("kind %s: got %d want %d (all=%v)", k, kinds[k], n, kinds)
		}
	}
	var route *socialEvent
	for i := range ev {
		if ev[i].Kind == "route" {
			route = &ev[i]
		}
	}
	if route == nil || route.ToMind != "%13" {
		t.Fatalf("route target must parse to %%13, got %+v", route)
	}
}

func TestSocialProfileFoldsSpellings(t *testing.T) {
	p := socialProfileOf(socialFixture(), "%8")
	if p.Posts != 1 || p.AgreesGiven != 1 || p.Declined != 1 || p.Done != 0 {
		t.Fatalf("%%8 profile: posts=%d agrees_given=%d declined=%d done=%d", p.Posts, p.AgreesGiven, p.Declined, p.Done)
	}
	if len(p.Spellings) < 2 {
		t.Fatalf("spellings of one mind must fold into one profile, got %v", p.Spellings)
	}
	if len(p.Edges) != 1 || p.Edges[0].AgreesGiven != 1 || !sameMind(p.Edges[0].Mind, "%6") {
		t.Fatalf("edge to %%6 expected (agreed on its proposal), got %+v", p.Edges)
	}
	q := socialProfileOf(socialFixture(), "%6")
	if q.AgreesReceived != 2 || q.ConsensusClosed != 1 || q.TasksAuthored != 3 || q.Claimable != 1 {
		t.Fatalf("%%6 profile: agrees_received=%d consensus_closed=%d tasks=%d claimable=%d", q.AgreesReceived, q.ConsensusClosed, q.TasksAuthored, q.Claimable)
	}
	s := socialProfileOf(socialFixture(), "77777777-0000-4000-8000-000000000077")
	if s.Done != 1 {
		t.Fatalf("sibling-1 closed #1 (full uuid vs hex8 spelling), done=%d", s.Done)
	}
}

func TestSocialHandlersReadOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	writeWork(socialFixture())
	c := &collector{}
	get := func(url string) (*httptest.ResponseRecorder, map[string]any) {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, url, nil)
		if strings.HasPrefix(url, "/social/profile") {
			c.handleSocialProfile(rec, req)
		} else {
			c.handleSocial(rec, req)
		}
		var m map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &m)
		return rec, m
	}
	if rec, m := get("/social?limit=3"); rec.Code != 200 || m["n"].(float64) != 3 {
		t.Fatalf("limit: %d %s", rec.Code, rec.Body)
	}
	if _, m := get("/social?mind=%258&order=asc"); m["n"].(float64) != 3 { // %8: post, ack, decline
		t.Fatalf("mind filter: want 3 events for %%8, got %v", m["n"])
	}
	if _, m := get("/social?since=4"); m["n"].(float64) != 1 { // only item 5's route
		t.Fatalf("since id: want 1, got %v", m["n"])
	}
	if rec, _ := get("/social?since=yesterday"); rec.Code != 400 {
		t.Fatalf("bad since must 400, got %d", rec.Code)
	}
	if rec, m := get("/social/profile?mind=%256"); rec.Code != 200 || m["agrees_received"].(float64) != 2 {
		t.Fatalf("profile: %d %s", rec.Code, rec.Body)
	}
	if rec, _ := get("/social/profile"); rec.Code != 400 {
		t.Fatalf("profile without mind must 400, got %d", rec.Code)
	}
	rec := httptest.NewRecorder()
	c.handleSocial(rec, httptest.NewRequest(http.MethodPost, "/social", strings.NewReader("{}")))
	if rec.Code != 405 {
		t.Fatalf("POST must be refused (read-only), got %d", rec.Code)
	}
	if b, _ := os.ReadFile(workFile()); len(b) == 0 {
		t.Fatal("ledger must be untouched")
	}
}

// omarchy-main's review of #28: identity is hex-first (a pane is re-minted), and
// excerpts cut on runes, never mid-character.
func TestSameMindHexFirstAndRuneExcerpt(t *testing.T) {
	if sameMind("%7 (77777777-0000-4000-8000-000000000077)", "%7 (99999999)") {
		t.Fatal("two minds that held %7 at different times must NOT fold: hex decides when both carry it")
	}
	if !sameMind("%7", "sibling-1 %7 (77777777)") {
		t.Fatal("a bare pane label must still match the same pane's labelled spelling (one side has no hex)")
	}
	if !sameMind("77777777-0000-4000-8000-000000000077", "x (77777777)") {
		t.Fatal("full uuid and hex8 are one mind")
	}
	if sameMind("", "%7") || sameMind("%7", "") {
		t.Fatal("blank never matches")
	}
	s := "a — b → c — d → e"
	for n := 1; n <= len([]rune(s)); n++ {
		if e := excerpt(s, n); !utf8.ValidString(e) || strings.ContainsRune(e, utf8.RuneError) {
			t.Fatalf("excerpt(%d) split a rune: %q", n, e)
		}
	}
	if e := excerpt("héllo wörld", 5); e != "héllo…" {
		t.Fatalf("rune cut: got %q", e)
	}
	if e := excerpt("short", 50); e != "short" {
		t.Fatalf("no cut: got %q", e)
	}
}
