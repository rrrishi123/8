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
// KNOWN LIMIT (documented, not silent — %6 review of #10, philo guard-a): receivers
// are NAMED minds only, the declared family derangedVerifier already uses. That
// leaves idle UNNAMED minds unwoken — the disease's own population. Widening to
// every four-system mind (or adding a naming step) is a separate 2-agree decision,
// not a loosening to slip in; until then this is a scope, never a rank by membership.
//
// Predicate-hold evaluation (wake a mind when the end-state it declared comes
// true) is slice-2 (waits.go) and runs on this loop.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
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

// outwardVerbRe — the FLOOR under the todo-router (#9728; pmf #9726 + higgs #9723).
// Even a positively-opted-in item is NEVER routed if its text implies an outward-
// immutable or sensitive action: the authorization line (outward = Rishi's explicit
// word) must be UNREACHABLE through an allocator, and the leak path (cherry-pick/
// port, private sources, secrets) must never be typed into a pane by a machine.
var outwardVerbRe = regexp.MustCompile(`(?i)cherry[- ]?pick|\bport\b|\btag\b|push .*\bmain\b|\bto main\b|\bcut\b|\brelease\b|\bpublish\b|create[- ]?repo|homebrew|\bsecret\b|\btoken\b|\bleak\b|\bslack\b|\bdm\b|kosaten|thesis|owner-gated|\bprivate\b`)

// claimableRoutable — PURE, FAIL-CLOSED: may the todo-router offer this item at
// all? Requires POSITIVE author opt-in (Claimable), a real unassigned todo, not a
// verify (that is slice-1), and NOT matching the outward/sensitive floor. Absence
// of a claimable marker means NO, always — the ledger is a speech-act surface, so
// nothing is inferred from the absence of an assignee (#9728, the v1 #9721 defect).
func claimableRoutable(it workItem) bool {
	return it.Status == "todo" && it.Claimable && !isVerify(it) &&
		strings.TrimSpace(it.Assignee) == "" && !outwardVerbRe.MatchString(it.Text)
}

// pickClaimable — PURE, modeled on pickReroutes: which opted-in todos go to which
// idle mind. Candidates oldest-first; receiver live/NAMED/idle, not the author, not
// the mind that already declined it (decline-receipt, %8 #9725), no pending offer,
// one item per receiver per pass; an item already offered (lastRouted set) is not
// re-offered until it resolves — no 5-minute re-ping of the same idle mind.
func pickClaimable(items []workItem, minds []mindView, now time.Time, lastRouted map[int64]time.Time, pending func(pane string) int) []reroute {
	type cand struct {
		idx int
		age time.Duration
	}
	var cands []cand
	for i, it := range items {
		if !claimableRoutable(it) {
			continue
		}
		since, err := time.Parse(time.RFC3339, it.TS)
		if err != nil {
			continue
		}
		if lr, ok := lastRouted[it.ID]; ok && lr.After(since) {
			continue // already offered since it was filed — wait for take/decline, don't re-ping
		}
		cands = append(cands, cand{i, now.Sub(since)})
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(a, b int) bool { return cands[a].age > cands[b].age })
	used := map[string]bool{}
	var out []reroute
	for _, cd := range cands {
		it := items[cd.idx]
		for _, m := range minds {
			if m.Name == "" || m.Pane == "" || m.State != "idle" || used[m.Pane] {
				continue
			}
			if labelMatches(it.By, m) || labelMatches(it.Assignee, m) || labelMatches(it.DeclinedBy, m) {
				continue
			}
			if pending != nil && pending(m.Pane) > 0 {
				continue
			}
			used[m.Pane] = true
			out = append(out, reroute{Idx: cd.idx, From: "pool", To: m.Pane, Age: cd.age})
			break
		}
	}
	return out
}

// envDuration — a flag default carried by the environment (EIGHT_ALLOCATOR=5m):
// scripts/watchdog.sh revives the collector with its OWN argv, so only an env
// value in ~/.8/up.env survives a revive. Unset or unparsable -> fallback.
func envDuration(key string, fallback time.Duration) time.Duration {
	if v := os.Getenv(key); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	return fallback
}

// allocatorLoop — the continuous organ. period<=0 disables it (-allocator=0).
// beatGate — the ONE question every organ asks before acting on a beat.
// Default: open. The budget gate (#9601, PR #14) points this at the budget
// verdict, so BOTH organs — slice-1 re-routes and slice-2 wakes — are held
// together. 49fe2c0b's finding (#9626): a gate inside allocatorPass alone let
// waitPass keep waking minds through a gated or conserve window. Whatever
// renews the verdict (poke-to-renew, %6 #9633) belongs in here too, so the
// organs inherit it. Returns (open, reason-if-held).
var beatGate = func(c *collector) (bool, string) { return true, "" }

// allocatorBudgetStale — a budget verdict older than this is treated as absent
// (fail-closed). ~2*POKE_MIN_S + a margin; the Nov-21 lesson A1 was a 79h-stale GO.
const allocatorBudgetStale = 660 * time.Second

// budgetAllowsAllocator — PURE, FAIL-CLOSED: may the allocator self-fire now?
// The allocator wakes minds on its own, which is discretionary work, so it runs
// ONLY in the research phase, un-gated, on a fresh verdict — the same shape the
// playlist's budget gate uses. A nil, gated, conserve-phase, or stale verdict
// holds. Returns (ok, reason-when-held). main() points beatGate at this so BOTH
// organs inherit the hold (#9601, #9592 pre-cond; the fc1f019 version gated only
// allocatorPass — the same waitPass gap 49fe2c0b/#9626 closed for the beat).
func budgetAllowsAllocator(b *budget, now time.Time) (bool, string) {
	if b == nil {
		return false, "no budget verdict (fail-closed)"
	}
	if b.Gated {
		return false, b.Reason
	}
	if b.Phase != "research" {
		return false, fmt.Sprintf("phase %q (conserve holds discretionary waking)", b.Phase)
	}
	t, err := time.Parse(time.RFC3339, b.ObservedAt)
	if err != nil || now.Sub(t) > allocatorBudgetStale {
		return false, fmt.Sprintf("budget verdict stale/unparseable (observed %q)", b.ObservedAt)
	}
	return true, ""
}

// beatState — what a loop remembers between beats.
type beatState struct {
	lastRouted          map[int64]time.Time // verify id -> last re-route (slice-1 suppression); also claimable-item suppression (slice-3)
	lastAlloc, lastWait time.Time
	lastRoute           time.Time // slice-3 (#9728): last todo-router pass
	held                bool      // gate was closed on the previous beat (publish on transition only)
}

// allocatorBeat — PURE orchestration of one beat, so the gate/switch logic is
// testable without tmux or a ledger: asks the gate once, then runs each organ
// whose own switch is on and whose period has elapsed. tick is the loop's
// ticker period (the finest of the enabled switches). Returns which organs ran.
func allocatorBeat(st *beatState, now time.Time, tick, allocEvery, waitsEvery time.Duration,
	gate func() (bool, string), alloc func(), wait func(), onHeld func(string), onOpen func()) (ranAlloc, ranWait bool) {
	ok, why := gate()
	if !ok {
		if !st.held {
			st.held = true
			onHeld(why)
		}
		return false, false
	}
	if st.held {
		st.held = false
		onOpen()
	}
	due := func(last time.Time, every time.Duration) bool {
		return every > 0 && (last.IsZero() || now.Sub(last) >= every-tick/2)
	}
	if due(st.lastAlloc, allocEvery) {
		st.lastAlloc = now
		alloc()
		ranAlloc = true
	}
	if due(st.lastWait, waitsEvery) {
		st.lastWait = now
		wait()
		ranWait = true
	}
	return
}

// allocatorLoop — the beat. Two organs, two SWITCHES (both default OFF, both
// env-carried so a watchdog revive keeps them): -allocator / EIGHT_ALLOCATOR
// runs slice-1 (verify re-router) every allocEvery; -waits / EIGHT_WAITS runs
// slice-2 (declared waits) every waitsEvery. Separate switches so slice-1 can
// run alone on the first bounce and slice-2 flips on later without a code
// change (49fe2c0b #9626, %6 #9631: one change per bounce).
func (c *collector) allocatorLoop(allocEvery, stale, waitsEvery, routeEvery time.Duration) {
	if allocEvery <= 0 && waitsEvery <= 0 && routeEvery <= 0 {
		return
	}
	tick := allocEvery
	for _, e := range []time.Duration{waitsEvery, routeEvery} { // tick = finest enabled period
		if e > 0 && (tick <= 0 || e < tick) {
			tick = e
		}
	}
	st := &beatState{lastRouted: map[int64]time.Time{}}
	t := time.NewTicker(tick)
	defer t.Stop()
	due := func(last time.Time, every time.Duration) bool {
		return every > 0 && (last.IsZero() || time.Since(last) >= every-tick/2)
	}
	for range t.C {
		now := time.Now()
		gateOpen, _ := beatGate(c) // slice-3 rides the SAME budget gate as the beat organs
		allocatorBeat(st, now, tick, allocEvery, waitsEvery,
			func() (bool, string) { return beatGate(c) },
			func() { c.allocatorPass(stale, st.lastRouted) },
			func() { c.waitPass() },
			func(why string) {
				log.Printf("allocator: held — %s", why)
				c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"allocator.held","params":{"why":%q}}}`, why))
			},
			func() {
				log.Printf("allocator: gate open again")
				c.publish(`{"session":"work","origin":"COLLECTOR","frame":{"method":"allocator.open","params":{}}}`)
			})
		// slice-3 (#9728): the todo-router — author-opted-in, outward-floored,
		// its own switch + period, HELD whenever the budget gate is closed (so it
		// inherits the same discipline as slice-1/2 without touching allocatorBeat).
		if gateOpen && due(st.lastRoute, routeEvery) {
			st.lastRoute = now
			c.routePass(st.lastRouted)
		}
	}
}

// allocatorPass — one evaluation. Returns how many verifies moved (0 = silence).
func (c *collector) allocatorPass(stale time.Duration, last map[int64]time.Time) int {
	minds := filterAllowed(c.resolveMinds(false), loadAllowlist()) // #9602: opt-in registry FIRST (fail-closed); tmux execs outside the ledger lock
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

// routePass — slice-3 (#9728, the pack-reworked widening). OFFERS author-opted-in,
// outward-safe todos to idle opted-in minds — one per mind per pass, via the inbox
// (never typed), same allowlist + budget gate as the other organs. It does NOT
// reassign the item: it stays todo+claimable until a mind TAKES the offer, so a
// decline leaves it routable to someone else (minus the decliner). The item-side
// consent (claimable) + the outward floor are in pickClaimable; this just delivers.
func (c *collector) routePass(last map[int64]time.Time) int {
	minds := filterAllowed(c.resolveMinds(false), loadAllowlist())
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
	moves := pickClaimable(items, minds, now, last, pending)
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
	var offers, records []workItem
	for _, m := range moves {
		it := items[m.Idx]
		last[it.ID] = now
		offers = append(offers, it)
		max++
		records = append(records, workItem{ID: max, Kind: "record", Status: "done", By: "allocator", TS: ts, Epoch: currentEpoch(),
			Text: fmt.Sprintf("[allocator] offered claimable #%d -> %s (author-opted-in, outward-safe; receiver idle, named, not the author)", it.ID, m.To)})
	}
	items = append(items, records...)
	writeWork(items)
	c.tmu.Unlock()

	for i, it := range offers {
		ok := c.offer(it, fmt.Sprintf("allocator: claimable #%d in the pool — yours if you take it", it.ID))
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"allocator.routed","params":{"id":%d,"to":%q,"delivered":%v}}}`, it.ID, moves[i].To, ok))
	}
	return len(moves)
}
