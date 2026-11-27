package main

import (
	"fmt"
	"strings"
	"testing"
)

// The auto-propagation guarantee (Rishi #9971 item 2): a mind must AUTOMATICALLY
// receive the OTHERS' new discussion acts — not its own echo, not the whole
// history, and the cursor must advance so nothing is re-sent. socialDigestFor is
// pure, so the guarantee is tested without a live pane or tmux.
func TestSocialDigestForExcludesSelfAndAdvances(t *testing.T) {
	events := []socialEvent{
		{TS: "2026-11-27T09:00:00Z", Kind: "post", Actor: "%6", Item: 1, Text: "my own post"},
		{TS: "2026-11-27T09:01:00Z", Kind: "ack", Actor: "%8", Item: 1, Verdict: "agree"},
		{TS: "2026-11-27T09:02:00Z", Kind: "post", Actor: "%13", Item: 2, Text: "pmf idea"},
	}
	msg, cursor, n := socialDigestFor(events, "2026-11-27T08:00:00Z", "%6", 6)
	if n != 2 {
		t.Fatalf("want 2 OTHERS' acts (not my own #1 post), got %d: %q", n, msg)
	}
	if cursor != "2026-11-27T09:02:00Z" {
		t.Fatalf("cursor must advance to the latest event, got %q", cursor)
	}
	if strings.Contains(msg, "post %6") {
		t.Fatalf("a mind must not be fed its OWN echo: %q", msg)
	}
	if !strings.Contains(msg, "#1 ack %8") || !strings.Contains(msg, "#2 post %13") {
		t.Fatalf("others' acts (%%8 ack, %%13 post) must be delivered: %q", msg)
	}
}

// Idempotence: once delivered up to a TS, nothing new => no message, cursor steady.
func TestSocialDigestForNothingNew(t *testing.T) {
	events := []socialEvent{{TS: "2026-11-27T09:00:00Z", Kind: "post", Actor: "%8", Item: 1}}
	msg, cursor, n := socialDigestFor(events, "2026-11-27T09:00:00Z", "%6", 6)
	if n != 0 || msg != "" {
		t.Fatalf("nothing newer than the cursor must deliver nothing, got n=%d %q", n, msg)
	}
	if cursor != "2026-11-27T09:00:00Z" {
		t.Fatalf("cursor must not drift when there is nothing new, got %q", cursor)
	}
}

// A chatty timeline is capped: all N counted, only the most-recent `max` shown.
func TestSocialDigestForCapsButCountsAll(t *testing.T) {
	var events []socialEvent
	for i := 1; i <= 10; i++ {
		events = append(events, socialEvent{TS: fmt.Sprintf("2026-11-27T09:%02d:00Z", i), Kind: "post", Actor: "%8", Item: int64(i)})
	}
	msg, cursor, n := socialDigestFor(events, "2026-11-27T08:00:00Z", "%6", 3)
	if n != 10 {
		t.Fatalf("count must reflect ALL new acts, got %d", n)
	}
	if !strings.Contains(msg, "(+7 earlier)") {
		t.Fatalf("overflow beyond max must be summarized as '+7 earlier': %q", msg)
	}
	if !strings.Contains(msg, "#10 post") || strings.Contains(msg, "#1 post") {
		t.Fatalf("only the most-recent %d should be shown: %q", 3, msg)
	}
	if cursor != "2026-11-27T09:10:00Z" {
		t.Fatalf("cursor must advance past every event, got %q", cursor)
	}
}
