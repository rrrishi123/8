package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

// watchers.go — the PRESENCE surface of the social net (#9554 B, #53; Rishi:
// "everyone sees what/why they are waiting and what is being done"). ONE read-only
// view that joins the mind roster (/mind) with the ledger (what each mind is DOING
// / BLOCKED on), the inbox (unread offers = delivered-not-yet-read), and the
// witnessed chain (each mind's last act → how long it has been idle). It invents
// no state: every field is read off existing witnessed data, so a watcher can
// never disagree with the ledger. This is the read side of the social graph; it
// does not route, wake, or mutate anything.

type watcherRow struct {
	Name         string  `json:"name"`
	Pane         string  `json:"pane,omitempty"`
	State        string  `json:"state,omitempty"`
	Live         bool    `json:"live"`                   // has a current pane binding
	WaitingFor   string  `json:"waiting_for,omitempty"`  // the mind's own declared wait (/wait), slice-2
	Doing        []int64 `json:"doing,omitempty"`        // work ids it is actively on (status=doing, it flipped)
	Blocked      []int64 `json:"blocked,omitempty"`      // items it authored/holds that are blocked (incl consensus:disagree)
	InboxPending int     `json:"inbox_pending"`          // offers delivered but not yet read/acted
	LastActTS    string  `json:"last_act,omitempty"`     // ts of its most recent ledger act
	IdleMinutes  int     `json:"idle_minutes,omitempty"` // now - last_act, the "why is it quiet" signal
}

// mindMatches — does a ledger actor-string (by / flipped_by / acked_by) refer to
// this mind? Matched by name or current pane as a WHOLE TOKEN, or by uuid substring.
// The ledger stamps any of these spellings. Name and pane are token-matched (split
// on space/paren/comma) so "sibling-1" does not swallow "sibling-10" and "pmf" does
// not swallow "validator-pmf" (external review on #36 — the substring form here
// conflated every name that is a prefix/infix of another, which is live in the
// roster today: pmf/validator-pmf, philo/validator-philo, higgs/higgs-worker). Hyphen
// is deliberately NOT a split char, so hyphenated names like "sibling-1" stay one
// token. uuid stays a substring match: uuids are globally unique and never nest, and
// the ledger carries both the short 8-hex prefix and the full form. Empty name never
// matches (unnamed panes aren't rows' owners).
func mindMatches(m mindView, actor string) bool {
	if actor == "" {
		return false
	}
	a := strings.ToLower(actor)
	if m.UUID != "" && strings.Contains(a, strings.ToLower(m.UUID)) {
		return true
	}
	name := strings.ToLower(m.Name)
	pane := strings.ToLower(m.Pane)
	if name == "" && pane == "" {
		return false
	}
	for _, tok := range strings.FieldsFunc(a, func(r rune) bool { return r == ' ' || r == '(' || r == ')' || r == ',' }) {
		if (name != "" && tok == name) || (pane != "" && tok == pane) {
			return true
		}
	}
	return false
}

// watchers — PURE given its inputs: the roster + the ledger + an inbox counter.
func watchers(minds []mindView, items []workItem, now time.Time, pending func(m mindView) int) []watcherRow {
	rows := make([]watcherRow, 0, len(minds))
	for _, m := range minds {
		if m.Name == "" { // the social surface is of NAMED minds; unnamed panes are not actors here
			continue
		}
		r := watcherRow{Name: m.Name, Pane: m.Pane, State: m.State, Live: m.Pane != "", WaitingFor: m.Waiting}
		var last time.Time
		for i := range items {
			it := &items[i]
			// doing / blocked attributed to this mind (it is the current holder)
			if it.Status == "doing" && (mindMatches(m, it.FlippedBy) || mindMatches(m, it.AckedBy)) {
				r.Doing = append(r.Doing, it.ID)
			}
			if it.Status == "blocked" && (mindMatches(m, it.By) || mindMatches(m, it.FlippedBy)) {
				r.Blocked = append(r.Blocked, it.ID)
			}
			// last act: the most recent row this mind authored or flipped
			if mindMatches(m, it.By) || mindMatches(m, it.FlippedBy) || mindMatches(m, it.AckedBy) {
				if t, err := time.Parse(time.RFC3339, it.TS); err == nil && t.After(last) {
					last = t
				}
			}
		}
		if !last.IsZero() {
			r.LastActTS = last.UTC().Format(time.RFC3339)
			if d := int(now.Sub(last).Minutes()); d > 0 {
				r.IdleMinutes = d
			}
		}
		r.InboxPending = pending(m)
		rows = append(rows, r)
	}
	// most-idle first — the view answers "who is quiet and why" at a glance
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].IdleMinutes > rows[j].IdleMinutes })
	return rows
}

// handleWatchers — GET /watchers. Read-only.
func (c *collector) handleWatchers(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "GET", 405)
		return
	}
	minds := c.resolveMinds(false)
	var items []workItem
	if b, err := os.ReadFile(workFile()); err == nil {
		_ = json.Unmarshal(b, &items)
	}
	p2u := paneUUIDMap()
	rows := watchers(minds, items, time.Now(), func(m mindView) int { return inboxPendingForPane(m.Pane, p2u) })
	_ = json.NewEncoder(w).Encode(map[string]any{
		"n":        len(rows),
		"watchers": rows,
		"note":     "PRESENCE view of the social net (#9554/#53): per NAMED mind, what it is doing/blocked on, its declared wait, unread inbox offers, and how long since its last witnessed act. Read-only, joined off the ledger — never invents state. Sorted most-idle first. CAVEATS (#36 external review): idle_minutes is derived from workItem.TS, which the ledger rewrites whenever ANYONE touches an item this mind authored/flipped/acked — so it reads 'last touched', and can UNDER-state idleness (a silent mind looks active when another mind acts on its old item); treat it as an upper bound on liveness, not a precise quiet-signal. waiting_for is the mind's own free text from /wait — leak-safety holds for the fields THIS view adds, not for what a mind types there.",
	})
}
