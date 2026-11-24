package main

// allocator.go — ALLOCATOR slice-1: the stale-verify re-router (#9580; design
// converged in the together-thread #9570/#9572/#9573/#9574/#9575).
//
// The disease it treats (#9530): a mind idles until a human types at it. The
// ledger already mints a second-observer task for every done claim — the
// `[verify #N]` item (ledger.go, derangedVerifier) — but routes it to ONE ring
// neighbour, and when that mind is busy the verify sits untaken for weeks
// (#9525/#9527 since 2026-10-28) while other minds idle.
//
// The organ: every period, find verifies that are todo, never acked and older
// than `stale`; hand each to an IDLE, NAMED mind (the declared family — the same
// scope derangedVerifier uses) that is neither the claim's author nor the
// current assignee and carries no pending offer (WIP=1). Delivery is the
// existing inbox OFFER (the mind still decides: take or decline), the move is
// witnessed as a ledger record, and the honest default output is SILENCE — no
// stale verify or no idle mind means nothing happens. Unranked: oldest first.
//
// Predicate-hold evaluation (wake a mind when the end-state it declared comes
// true) is slice-2 and builds on this loop.

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// reroute — one decision of a pass: items[Idx] moves From -> To (a pane %N).
type reroute struct {
	Idx  int
	From string
	To   string
	Age  time.Duration
}

// isVerify — an auto-minted second-observer item.
func isVerify(it workItem) bool {
	return strings.HasPrefix(strings.TrimSpace(it.Text), "[verify #")
}

// verifyTarget — N of a "[verify #N] …" item; 0 when unparsable.
func verifyTarget(text string) int64 {
	t := strings.TrimPrefix(strings.TrimSpace(text), "[verify #")
	end := strings.IndexByte(t, ']')
	if end <= 0 {
		return 0
	}
	n, err := strconv.ParseInt(t[:end], 10, 64)
	if err != nil {
		return 0
	}
	return n
}

func isWordByte(b byte) bool {
	return b == '-' || b == '_' || (b >= '0' && b <= '9') || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// containsToken — tok occurs in s on word boundaries ("%6" is not in "%60").
func containsToken(s, tok string) bool {
	if tok == "" {
		return false
	}
	for start := 0; start+len(tok) <= len(s); {
		j := strings.Index(s[start:], tok)
		if j < 0 {
			return false
		}
		i := start + j
		k := i + len(tok)
		left := i == 0 || !isWordByte(s[i-1])
		right := k == len(s) || !isWordByte(s[k])
		if left && right {
			return true
		}
		start = i + 1
	}
	return false
}

// labelMatches — does a free-form by/assignee label ("conductor %6 (12444bc8)",
// "%8", "sibling-1", a uuid) refer to this mind? Pane, name and uuid-prefix count.
func labelMatches(label string, m mindView) bool {
	l := strings.TrimSpace(label)
	if l == "" {
		return false
	}
	if m.Pane != "" && containsToken(l, m.Pane) {
		return true
	}
	if m.Name != "" && containsToken(l, m.Name) {
		return true
	}
	if len(m.UUID) >= 8 && strings.Contains(l, m.UUID[:8]) {
		return true
	}
	return false
}

// pickReroutes — PURE: which stale verifies move, and to whom.
//   - item: todo [verify #N], never acked, older than `stale` (clock resets on a
//     prior re-route via lastRouted);
//   - receiver: live, NAMED, idle; not the claim's author; not the current
//     assignee; no pending inbox offer; at most one item per receiver per pass;
//   - order: oldest verify first.
func pickReroutes(items []workItem, minds []mindView, now time.Time, stale time.Duration, lastRouted map[int64]time.Time, pending func(pane string) int) []reroute {
	byID := map[int64]workItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	type cand struct {
		idx int
		age time.Duration
	}
	var cands []cand
	for i, it := range items {
		if it.Status != "todo" || !isVerify(it) || it.AckedAt != "" {
			continue
		}
		since, err := time.Parse(time.RFC3339, it.TS)
		if err != nil {
			continue
		}
		if lr, ok := lastRouted[it.ID]; ok && lr.After(since) {
			since = lr
		}
		age := now.Sub(since)
		if age < stale {
			continue
		}
		cands = append(cands, cand{i, age})
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(a, b int) bool { return cands[a].age > cands[b].age })

	used := map[string]bool{}
	var out []reroute
	for _, cd := range cands {
		it := items[cd.idx]
		author := byID[verifyTarget(it.Text)].By
		for _, m := range minds {
			if m.Name == "" || m.Pane == "" || m.State != "idle" || used[m.Pane] {
				continue
			}
			if labelMatches(author, m) || labelMatches(it.Assignee, m) || labelMatches(it.By, m) {
				continue
			}
			if pending != nil && pending(m.Pane) > 0 {
				continue
			}
			used[m.Pane] = true
			from := it.Assignee
			if from == "" {
				from = "pool"
			}
			out = append(out, reroute{Idx: cd.idx, From: from, To: m.Pane, Age: cd.age})
			break
		}
	}
	return out
}

// allocatorLoop — the continuous organ. period<=0 disables it (-allocator=0).
func (c *collector) allocatorLoop(period, stale time.Duration) {
	if period <= 0 {
		return
	}
	last := map[int64]time.Time{}
	t := time.NewTicker(period)
	defer t.Stop()
	for range t.C {
		c.allocatorPass(stale, last)
		c.waitPass() // slice-2 (waits.go): declared end-states, evaluated on the same beat
	}
}

// allocatorPass — one evaluation. Returns how many verifies moved (0 = silence).
func (c *collector) allocatorPass(stale time.Duration, last map[int64]time.Time) int {
	minds := c.resolveMinds(false) // tmux execs: outside the ledger lock
	p2u := paneUUIDMap()
	pending := func(pane string) int { return inboxPendingForPane(pane, p2u) }
	now := time.Now()

	c.tmu.Lock()
	var items []workItem
	b, err := os.ReadFile(workFile())
	if err != nil || json.Unmarshal(b, &items) != nil {
		c.tmu.Unlock()
		return 0
	}
	moves := pickReroutes(items, minds, now, stale, last, pending)
	if len(moves) == 0 {
		c.tmu.Unlock()
		return 0
	}
	var max int64
	for _, it := range items {
		if it.ID > max {
			max = it.ID
		}
	}
	ts := now.UTC().Format(time.RFC3339)
	var offers []workItem
	var records []workItem
	for _, m := range moves {
		it := &items[m.Idx]
		it.Assignee = m.To
		it.FlippedBy = "allocator:reroute"
		last[it.ID] = now
		offers = append(offers, *it)
		max++
		records = append(records, workItem{ID: max, Kind: "record", Status: "done", By: "allocator", TS: ts, Epoch: currentEpoch(),
			Text: fmt.Sprintf("[allocator] re-routed #%d (verify of #%d) %s -> %s: untaken %s; receiver idle, named, not the author", it.ID, verifyTarget(it.Text), m.From, m.To, m.Age.Round(time.Minute))})
	}
	items = append(items, records...)
	writeWork(items)
	c.tmu.Unlock()

	for i, it := range offers {
		ok := c.offer(it, fmt.Sprintf("allocator: verify #%d untaken by %s for %s — yours if you take it", it.ID, moves[i].From, moves[i].Age.Round(time.Minute)))
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.reroute","params":{"id":%d,"from":%q,"to":%q,"delivered":%v}}}`, it.ID, moves[i].From, moves[i].To, ok))
	}
	return len(moves)
}
