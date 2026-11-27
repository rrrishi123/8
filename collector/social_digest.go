package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// SOCIAL DIGEST (#9971 item 2 — Rishi: "as social is up, when we are discussing
// all the panes should get our discussions automatically, right"). The /social
// timeline is READ-ONLY (pull) and the inbox nudge delivers only a POINTER, so a
// discussion never reaches a mind until it goes and fetches. This loop PUSHES the
// new timeline acts — OTHERS' posts, acks, routes, closes — to each opted-in IDLE
// mind as one concise digest, typed through the single send throat (bracketed
// paste, idle-guarded). It makes "discuss among all" self-running: no hand-wake.
//
// DEFAULT OFF, env-carried (EIGHT_SOCIAL=2m) so a watchdog revive keeps it on;
// opt-in per end (autonomy, security-at-their-end). A mind never sees its OWN
// echo, only the others — the social-network view Rishi asked for. It is the
// FEED, not a task: the mind decides what to do with it (see eight-inbox law).
// It never touches %29 or a work pane (the allowlist gates who is reachable), and
// never a BUSY pane (idle-guard) — a working mind collects the backlog when idle.

var (
	socialSeenMu sync.Mutex
	socialSeen   = map[string]string{} // pane -> last timeline TS delivered (the cursor)
)

// socialDigestLoop pushes new timeline acts to idle opted-in minds every `every`.
// every<=0 disables it (the default) — enabling is an explicit act, like the
// allocator organs: -social=2m, or EIGHT_SOCIAL=2m so a watchdog revive keeps it.
func (c *collector) socialDigestLoop(every time.Duration) {
	if every <= 0 {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for range t.C {
		c.socialDigestPass()
	}
}

// socialDigestPass — one sweep: for each opted-in idle mind, type a digest of the
// OTHERS' social acts it hasn't seen yet, then advance its cursor.
func (c *collector) socialDigestPass() {
	c.tmu.Lock()
	var items []workItem
	b, err := os.ReadFile(workFile())
	if err != nil || json.Unmarshal(b, &items) != nil {
		c.tmu.Unlock()
		return
	}
	c.tmu.Unlock()
	events := socialTimeline(items)
	if len(events) == 0 {
		return
	}
	latest := events[len(events)-1].TS
	for _, m := range filterAllowed(c.resolveMinds(false), loadAllowlist()) {
		pane := m.Pane
		if pane == "" || !paneIdle(pane) { // never interrupt a working mind
			continue
		}
		socialSeenMu.Lock()
		since, seen := socialSeen[pane]
		socialSeenMu.Unlock()
		if !seen { // first sight: baseline to now, never dump the whole history
			socialSeenMu.Lock()
			socialSeen[pane] = latest
			socialSeenMu.Unlock()
			continue
		}
		msg, cursor, n := socialDigestFor(events, since, m.Name, 6)
		if n == 0 {
			if cursor != since {
				socialSeenMu.Lock()
				socialSeen[pane] = cursor
				socialSeenMu.Unlock()
			}
			continue
		}
		if sendToPane(pane, msg, nil) { // bracketed-paste, idle-guarded single throat
			socialSeenMu.Lock()
			socialSeen[pane] = cursor
			socialSeenMu.Unlock()
			c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"social.digest","params":{"pane":%q,"n":%d}}}`, pane, n))
		}
	}
}

// socialDigestFor — PURE (testable without a live pane): given the timeline
// (ascending by TS), a pane's last-seen TS, and the pane's own mind name, build a
// concise digest of OTHERS' acts newer than sinceTS, the advanced cursor, and the
// count of new acts. Empty msg => nothing to deliver. At most `max` lines are
// shown (the most recent); any older ones are summarized as "+K earlier".
func socialDigestFor(events []socialEvent, sinceTS, selfMind string, max int) (msg, cursor string, n int) {
	cursor = sinceTS
	var fresh []socialEvent
	for _, e := range events {
		if e.TS > cursor {
			cursor = e.TS
		}
		if e.TS <= sinceTS {
			continue
		}
		if selfMind != "" && sameMind(e.Actor, selfMind) { // not your own echo — you see the OTHERS
			continue
		}
		fresh = append(fresh, e)
	}
	n = len(fresh)
	if n == 0 {
		return "", cursor, 0
	}
	shown := fresh
	extra := 0
	if max > 0 && len(shown) > max {
		extra = len(shown) - max
		shown = shown[len(shown)-max:] // keep the most recent `max`
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "[8-social] %d new on the shared timeline — others' thinking since you last looked:", n)
	for _, e := range shown {
		line := fmt.Sprintf("\n  #%d %s %s", e.Item, e.Kind, e.Actor)
		if e.Verdict != "" {
			line += " " + e.Verdict
		}
		if e.Text != "" {
			line += ": " + excerpt(e.Text, 80)
		}
		sb.WriteString(line)
	}
	if extra > 0 {
		fmt.Fprintf(&sb, "\n  (+%d earlier)", extra)
	}
	fmt.Fprintf(&sb, "\nread all: curl -s '127.0.0.1:7070/social?since=%s' — this is the discussion reaching you; weigh in on the ledger if your model differs.", sinceTS)
	return sb.String(), cursor, n
}
