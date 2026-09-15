package main

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// Replays 2026-09-14: tmux restored, every seat re-minted. Old boot A had
// %7=conductor, %9=higgs, %11=pmf, %0=host shell (no mind). New boot B seats
// conductor at %9 and higgs at %11; pmf hasn't resumed yet.
func TestRemintItems(t *testing.T) {
	l := &lineageFile{Boots: map[string]map[string]string{
		"A": {"%7": "u-conductor", "%9": "u-higgs", "%11": "u-pmf"},
	}, Seen: map[string]string{}}
	live := map[string]string{"u-conductor": "%9", "u-higgs": "%11"}
	items := []workItem{
		{ID: 1, Boot: "A", By: "%7", Assignee: "%9", FlippedBy: "%7", DeliveredTo: "%9"}, // conductor->higgs
		{ID: 2, Boot: "A", By: "%0", Assignee: "%7"},                                     // host seat + conductor
		{ID: 3, Boot: "A", By: "%11", Assignee: "%11"},                                   // pmf, not back -> waits
		{ID: 4, Boot: "", By: "%9"},                                                      // legacy/fresh -> current
		{ID: 5, Boot: "B", By: "%9", Assignee: "%10"},                                    // already current
		{ID: 6, Boot: "Z", By: "%3"},                                                     // boot never witnessed -> waits
	}
	ch, pending := remintItems(items, "B", l, live)
	if pending != 2 {
		t.Fatalf("pending=%d want 2 (pmf not back, unknown boot Z)", pending)
	}
	got := map[int64]workItem{}
	for _, it := range items {
		got[it.ID] = it
	}
	if g := got[1]; g.By != "%9" || g.Assignee != "%11" || g.FlippedBy != "%9" || g.DeliveredTo != "%11" || g.Boot != "B" {
		t.Fatalf("item1 mistranslated: %+v", g)
	}
	if g := got[2]; g.By != "ex-%0" || g.Assignee != "%9" || g.Boot != "B" {
		t.Fatalf("item2: host seat should be ex-%%0, conductor -> %%9: %+v", g)
	}
	if g := got[3]; g.By != "%11" || g.Assignee != "%11" || g.Boot != "A" {
		t.Fatalf("item3 must stay quarantined untouched: %+v", g)
	}
	if g := got[4]; g.Boot != "B" || g.By != "%9" {
		t.Fatalf("item4 blank boot should be stamped current, fields untouched: %+v", g)
	}
	if g := got[5]; g.Boot != "B" || g.Assignee != "%10" {
		t.Fatalf("item5 current boot must be untouched: %+v", g)
	}
	if g := got[6]; g.Boot != "Z" {
		t.Fatalf("item6 unknown boot must wait: %+v", g)
	}
	if len(ch) != 6 { // 1: by,assignee,flipped_by,delivered_to (4); 2: by(ex), assignee (2)
		t.Fatalf("changes=%d want 6: %+v", len(ch), ch)
	}
	// second pass once pmf is back at %13: item3 translates, nothing else moves
	live["u-pmf"] = "%13"
	ch, pending = remintItems(items, "B", l, live)
	if pending != 1 || len(ch) != 2 || items[2].By != "%13" || items[2].Assignee != "%13" || items[2].Boot != "B" {
		t.Fatalf("pmf pass: pending=%d ch=%+v item3=%+v", pending, ch, items[2])
	}
	// staleBoot: quarantine only when both boots known and differ
	setCurrentBoot("B")
	if !staleBoot(workItem{Boot: "A"}) || staleBoot(workItem{Boot: "B"}) || staleBoot(workItem{Boot: ""}) {
		t.Fatal("staleBoot wrong")
	}
	setCurrentBoot("")
	if staleBoot(workItem{Boot: "A"}) {
		t.Fatal("no tmux -> never quarantine")
	}
}

func TestPlaylistScope(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
	if playlistScope() != nil {
		t.Fatal("no file -> nil scope")
	}
	os.WriteFile(playlistFile(), []byte("on"), 0o644)
	if playlistScope() != nil || !inScope(nil, "%1", "") {
		t.Fatal("bare on -> everyone")
	}
	os.WriteFile(playlistFile(), []byte("on\n%9\nabcd-uuid"), 0o644)
	sc := playlistScope()
	if !inScope(sc, "%9", "") || !inScope(sc, "%77", "abcd-uuid") || inScope(sc, "%10", "other") {
		t.Fatalf("scope wrong: %v", sc)
	}
}

func TestClassifyScreen(t *testing.T) {
	cases := []struct{ screen, cmd, want, resets string }{
		{"  ⏵⏵ bypass permissions on · ← for agents", "claude.exe", "idle", ""},
		{"✻ Thinking… (esc to interrupt)", "claude.exe", "working", ""},
		{"You've hit your usage limit. Resets at 2:30pm", "claude.exe", "capped", "Resets at 2:30pm"},
		{"Adjust monthly spend limit\n Resets 14:00", "claude", "capped", "Resets 14:00"},
		{"rishirajs@mac ~ %", "zsh", "shell", ""},
	}
	for _, c := range cases {
		s, r := classifyScreen(c.screen, c.cmd)
		if s != c.want || r != c.resets {
			t.Fatalf("%q -> %s/%q want %s/%q", c.screen, s, r, c.want, c.resets)
		}
	}
	if !dispatchable("%none") {
		t.Fatal("unprobed pane must remain dispatchable")
	}
}

func TestKind(t *testing.T) {
	if !isRecordItem(workItem{Kind: "record", Text: "do the thing"}) || isRecordItem(workItem{Kind: "task", Text: "[FINDING] x"}) {
		t.Fatal("explicit kind must win over the prefix guess")
	}
	if !isRecordItem(workItem{Text: "[FINDING] legacy"}) || isRecordItem(workItem{Text: "legacy task"}) {
		t.Fatal("blank kind falls back to the prefix guess")
	}
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
	writeWork([]workItem{{ID: 1, Text: "[ACT (x)] old record"}, {ID: 2, Text: "old task"}})
	var got []workItem
	b, _ := os.ReadFile(workFile())
	json.Unmarshal(b, &got)
	if got[0].Kind != "record" || got[1].Kind != "task" {
		t.Fatalf("writeWork must stamp kind once: %+v", got)
	}
}

func TestWSFrameAndUsage(t *testing.T) {
	for _, n := range []int{5, 200, 70000} {
		f := wsFrame(bytes.Repeat([]byte("x"), n))
		if f[0] != 0x81 || f[1]&0x80 == 0 {
			t.Fatalf("frame header wrong for n=%d", n)
		}
	}
	p := t.TempDir() + "/s.jsonl"
	os.WriteFile(p, []byte(`{"type":"user","message":{}}
{"type":"assistant","timestamp":"2026-09-15T10:00:00Z","message":{"usage":{"input_tokens":2,"cache_read_input_tokens":30000,"cache_creation_input_tokens":4000}}}
{"type":"attachment"}
`), 0o644)
	if n, at := lastUsage(p); n != 34002 || at != "2026-09-15T10:00:00Z" {
		t.Fatalf("lastUsage = %d %s", n, at)
	}
	if _, err := wsDial("ws://8.8.8.8:1/x", time.Second); err == nil || !strings.Contains(err.Error(), "loopback") {
		t.Fatalf("non-loopback must be refused, got %v", err)
	}
}

func TestSessionIDFor(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	os.MkdirAll(os.ExpandEnv("$HOME/.claude/sessions"), 0o755)
	os.WriteFile(os.ExpandEnv("$HOME/.claude/sessions/4242.json"), []byte(`{"pid":4242,"sessionId":"f9129044-e1f3-4ff4-862a-fe4dcb1cdbda","tmux":"kosaten1:@4.%24"}`), 0o644)
	if sessionIDFor("4242") != "f9129044-e1f3-4ff4-862a-fe4dcb1cdbda" || sessionIDFor("1") != "" {
		t.Fatal("sessionIDFor wrong")
	}
}

func TestParseBudgetHeaders(t *testing.T) {
	now := time.Unix(1789500000, 0)
	h := map[string]string{
		"anthropic-ratelimit-unified-5h-utilization": "0.21", "anthropic-ratelimit-unified-5h-status": "allowed", "anthropic-ratelimit-unified-5h-reset": "1789515000",
		"anthropic-ratelimit-unified-7d-utilization": "0.38", "anthropic-ratelimit-unified-7d-status": "allowed", "anthropic-ratelimit-unified-7d-reset": "1789974000",
		"anthropic-ratelimit-unified-representative-claim": "five_hour", "anthropic-ratelimit-unified-overage-status": "rejected",
		"anthropic-ratelimit-unified-reset": "1789515000", "request-id": "req_x",
	}
	w, claim, ov := parseBudgetHeaders(h, now)
	if len(w) != 2 || w["5h"].Utilization != 0.21 || w["5h"].ResetInS != 15000 || w["7d"].Status != "allowed" || claim != "five_hour" || ov != "rejected" {
		t.Fatalf("parsed wrong: %+v %s %s", w, claim, ov)
	}
}

func TestFirst8(t *testing.T) {
	if first8("12444bc8-4325") != "12444bc8" || first8("") != "—" || first8("abc") != "abc" {
		t.Fatal("first8 wrong")
	}
}
