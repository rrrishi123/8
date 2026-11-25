package main

// SOCIAL (#9563, the public-safe first slice): the four-system already records
// every social act — a mind posts (a record), offers, takes, declines, acks
// agree/disagree, the allocator routes, the pack closes by consensus. What was
// missing is the READ side: one chronological timeline of those acts and a
// profile per mind, both derived ONLY from the ledger (~/.8/work.json) — no new
// store, no ranking (a ranked feed engulfs 8), nothing typed into anyone.
//
//   GET /social?limit=200&since=<id|RFC3339>&mind=<name|%N|uuid|hex8>&order=desc|asc
//   GET /social/profile?mind=<name|%N|uuid|hex8>
//
// Identity is the same notion consensus-ack uses (ackKey: a uuid hex run, else a
// pane token, else the lowercased string), so '%8', '%8 (f9129044)' and the full
// uuid are one mind. Everything here is a MODEL of the witnessed record (%6
// #9771): other minds may model it differently, and that gap is signal, not error.

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// socialEvent — one social act, flattened from the ledger.
type socialEvent struct {
	TS      string `json:"ts"`
	Kind    string `json:"kind"` // post | task | done | ack | decline | route | consensus
	Actor   string `json:"actor,omitempty"`
	Item    int64  `json:"item"`
	Verdict string `json:"verdict,omitempty"` // ack: agree|disagree
	Status  string `json:"status,omitempty"`
	Text    string `json:"text,omitempty"` // excerpt
	ToMind  string `json:"to,omitempty"`   // route: receiver
}

// sameMind — one identity under different spellings. HEX FIRST (omarchy-main's
// review of #28): a session uuid (or its first 8 hex) is the durable identity; a
// pane token %N is a CURRENT binding that is re-minted on every tmux restart, so
// two labels that both carry hex are compared on hex alone — "%7 (77777777…)" and
// "%7 (99999999…)" are two minds that held %7 at different times, not one. The
// pane token decides only when neither side carries hex; ackKey's string fallback
// last. Known residual: two BARE "%7" labels from different lifetimes still fold;
// the fix for that is lineage (pane→uuid over time), not a string rule.
func sameMind(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	la, lb := strings.ToLower(a), strings.ToLower(b)
	ha, hb := ackHexRe.FindString(la), ackHexRe.FindString(lb)
	if ha != "" && hb != "" {
		return ha[:8] == hb[:8]
	}
	if pa, pb := ackPaneRe.FindString(la), ackPaneRe.FindString(lb); pa != "" && pb != "" {
		return pa == pb
	}
	return ackKey(a) == ackKey(b)
}

// excerpt — the first n RUNES (not bytes: the ledger is full of — and →, and a
// byte cut mid-rune would ship U+FFFD in the JSON; omarchy-main's review of #28).
func excerpt(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	r := []rune(s)
	if len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// socialTimeline — PURE: the ledger as a sequence of social acts.
func socialTimeline(items []workItem) []socialEvent {
	var ev []socialEvent
	for _, it := range items {
		switch {
		case it.By == "allocator":
			ev = append(ev, socialEvent{TS: it.TS, Kind: "route", Actor: "allocator", Item: it.ID, Text: excerpt(it.Text, 160), ToMind: routeTarget(it.Text)})
		case it.Kind == "record" || (strings.HasPrefix(it.Text, "[") && it.By != "auto"):
			ev = append(ev, socialEvent{TS: it.TS, Kind: "post", Actor: it.By, Item: it.ID, Status: it.Status, Text: excerpt(it.Text, 160)})
		default:
			ev = append(ev, socialEvent{TS: it.TS, Kind: "task", Actor: it.By, Item: it.ID, Status: it.Status, Text: excerpt(it.Text, 160)})
		}
		if strings.HasPrefix(it.FlippedBy, "consensus:") {
			ev = append(ev, socialEvent{TS: it.TS, Kind: "consensus", Actor: it.FlippedBy, Item: it.ID, Status: it.Status})
		} else if it.Status == "done" && it.FlippedBy != "" && !sameMind(it.FlippedBy, it.By) && !strings.HasPrefix(it.FlippedBy, "auto") {
			ev = append(ev, socialEvent{TS: it.TS, Kind: "done", Actor: it.FlippedBy, Item: it.ID, Status: it.Status})
		}
		for _, a := range it.Acks {
			ev = append(ev, socialEvent{TS: a.TS, Kind: "ack", Actor: a.By, Item: it.ID, Verdict: a.Verdict})
		}
		if it.DeclinedBy != "" {
			ev = append(ev, socialEvent{TS: it.TS, Kind: "decline", Actor: it.DeclinedBy, Item: it.ID})
		}
	}
	sort.SliceStable(ev, func(i, j int) bool {
		if ev[i].TS != ev[j].TS {
			return ev[i].TS < ev[j].TS
		}
		return ev[i].Item < ev[j].Item
	})
	return ev
}

// routeTarget — "[allocator] … -> %13: …" → "%13" (best effort, display only).
func routeTarget(text string) string {
	i := strings.Index(text, "-> ")
	if i < 0 {
		return ""
	}
	rest := text[i+3:]
	if j := strings.IndexAny(rest, ":, "); j > 0 {
		return rest[:j]
	}
	return rest
}

// socialProfile — PURE: what one mind did, and with whom, per the record.
type socialEdge struct {
	Mind              string `json:"mind"`
	AgreesGiven       int    `json:"agrees_given"`
	DisagreesGiven    int    `json:"disagrees_given"`
	AgreesReceived    int    `json:"agrees_received"`
	DisagreesReceived int    `json:"disagrees_received"`
}
type socialProfile struct {
	Mind              string       `json:"mind"`
	Spellings         []string     `json:"spellings"`
	Posts             int          `json:"posts"`
	TasksAuthored     int          `json:"tasks_authored"`
	Done              int          `json:"done"`     // items this mind closed (not its own posts)
	Taken             int          `json:"taken"`    // offers taken
	Declined          int          `json:"declined"` // offers declined
	AgreesGiven       int          `json:"agrees_given"`
	DisagreesGiven    int          `json:"disagrees_given"`
	AgreesReceived    int          `json:"agrees_received"`
	DisagreesReceived int          `json:"disagrees_received"`
	ConsensusClosed   int          `json:"consensus_closed"` // own items the pack closed
	Claimable         int          `json:"claimable_stamped"`
	FirstActive       string       `json:"first_active,omitempty"`
	LastActive        string       `json:"last_active,omitempty"`
	Edges             []socialEdge `json:"edges"`
	Note              string       `json:"note"`
}

func socialProfileOf(items []workItem, mind string) socialProfile {
	p := socialProfile{Mind: mind, Edges: []socialEdge{}}
	seen := map[string]bool{}
	edges := map[string]*socialEdge{}
	edge := func(label string) *socialEdge {
		k := ackKey(label)
		if e, ok := edges[k]; ok {
			return e
		}
		e := &socialEdge{Mind: label}
		edges[k] = e
		return e
	}
	touch := func(ts, spelling string) {
		if ts != "" && (p.FirstActive == "" || ts < p.FirstActive) {
			p.FirstActive = ts
		}
		if ts > p.LastActive {
			p.LastActive = ts
		}
		if spelling != "" && !seen[spelling] {
			seen[spelling] = true
			p.Spellings = append(p.Spellings, spelling)
		}
	}
	for _, it := range items {
		mine := sameMind(it.By, mind)
		if mine {
			touch(it.TS, it.By)
			if it.Kind == "record" || strings.HasPrefix(it.Text, "[") {
				p.Posts++
			} else {
				p.TasksAuthored++
			}
			if it.Claimable {
				p.Claimable++
			}
			if strings.HasPrefix(it.FlippedBy, "consensus:") && it.Status == "done" {
				p.ConsensusClosed++
			}
		}
		if it.Status == "done" && it.FlippedBy != "" && !mine && sameMind(it.FlippedBy, mind) {
			p.Done++
			touch(it.TS, "")
		}
		if it.AckedBy != "" && sameMind(it.AckedBy, mind) && !mine {
			p.Taken++
			touch(it.AckedAt, "")
		}
		if it.DeclinedBy != "" && sameMind(it.DeclinedBy, mind) {
			p.Declined++
			touch(it.TS, "")
		}
		for _, a := range it.Acks {
			if sameMind(a.By, mind) {
				touch(a.TS, a.By)
				if a.Verdict == "disagree" {
					p.DisagreesGiven++
					if it.By != "" && !mine {
						edge(it.By).DisagreesGiven++
					}
				} else {
					p.AgreesGiven++
					if it.By != "" && !mine {
						edge(it.By).AgreesGiven++
					}
				}
			} else if mine {
				if a.Verdict == "disagree" {
					p.DisagreesReceived++
					edge(a.By).DisagreesReceived++
				} else {
					p.AgreesReceived++
					edge(a.By).AgreesReceived++
				}
			}
		}
	}
	for _, e := range edges {
		p.Edges = append(p.Edges, *e)
	}
	sort.Slice(p.Edges, func(i, j int) bool {
		wi := p.Edges[i].AgreesGiven + p.Edges[i].DisagreesGiven + p.Edges[i].AgreesReceived + p.Edges[i].DisagreesReceived
		wj := p.Edges[j].AgreesGiven + p.Edges[j].DisagreesGiven + p.Edges[j].AgreesReceived + p.Edges[j].DisagreesReceived
		if wi != wj {
			return wi > wj
		}
		return p.Edges[i].Mind < p.Edges[j].Mind
	})
	p.Note = "derived from the ledger only (one model of the witnessed record); identity = ackKey/pane token, so spellings of one mind fold together"
	return p
}

func loadWorkItems() []workItem {
	var items []workItem
	b, err := os.ReadFile(workFile())
	if err != nil || json.Unmarshal(b, &items) != nil {
		return nil
	}
	return items
}

// handleSocial — GET /social: the timeline.
func (c *collector) handleSocial(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "GET only — the social layer is read-only; act through /work, /inbox, /allocator", 405)
		return
	}
	q := r.URL.Query()
	ev := socialTimeline(loadWorkItems())
	if s := q.Get("since"); s != "" {
		if id, err := strconv.ParseInt(s, 10, 64); err == nil {
			ev = filterEvents(ev, func(e socialEvent) bool { return e.Item > id })
		} else if _, err := time.Parse(time.RFC3339, s); err == nil {
			ev = filterEvents(ev, func(e socialEvent) bool { return e.TS > s })
		} else {
			http.Error(w, `{"error":"since must be a work id or an RFC3339 timestamp"}`, 400)
			return
		}
	}
	if m := q.Get("mind"); m != "" {
		ev = filterEvents(ev, func(e socialEvent) bool { return sameMind(e.Actor, m) || sameMind(e.ToMind, m) })
	}
	if k := q.Get("kind"); k != "" {
		ev = filterEvents(ev, func(e socialEvent) bool { return e.Kind == k })
	}
	limit := 200
	if l, err := strconv.Atoi(q.Get("limit")); err == nil && l > 0 && l <= 5000 {
		limit = l
	}
	if q.Get("order") != "asc" { // default newest-first, like a timeline; still unranked
		for i, j := 0, len(ev)-1; i < j; i, j = i+1, j-1 {
			ev[i], ev[j] = ev[j], ev[i]
		}
	}
	if len(ev) > limit {
		ev = ev[:limit]
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"n": len(ev), "events": ev,
		"note": "unranked, chronological social acts derived from the ledger: post|task|done|ack|decline|route|consensus. ?since=<id|ts> ?mind=<label> ?kind= ?limit= ?order=asc. Read-only."})
}

func filterEvents(ev []socialEvent, keep func(socialEvent) bool) []socialEvent {
	out := ev[:0:0]
	for _, e := range ev {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

// handleSocialProfile — GET /social/profile?mind=<label>.
func (c *collector) handleSocialProfile(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", 405)
		return
	}
	m := strings.TrimSpace(r.URL.Query().Get("mind"))
	if m == "" {
		http.Error(w, `{"error":"need ?mind=<name|%N|uuid|hex8>"}`, 400)
		return
	}
	_ = json.NewEncoder(w).Encode(socialProfileOf(loadWorkItems(), m))
}
