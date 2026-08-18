package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── WORK — the shared task surface ───────────────────────────────────────────
// The operator writes work INTO the witness (top-right widget); agents read it
// FROM the witness before starting their own (the queue an agent checks FIRST).
// Persisted ~/.8/work.json; every add/status change is published to the feed.
type workItem struct {
	ID       int64   `json:"id"`
	Text     string  `json:"text"`
	Status   string  `json:"status"` // todo → doing → done
	By       string  `json:"by"`
	TS       string  `json:"ts"`
	Deps     []int64 `json:"deps,omitempty"`     // ids this task waits on — the PLAN's edges (a DAG)
	Prio     int64   `json:"prio,omitempty"`     // higher = picked sooner; the operator/agent adjusts the queue
	Assignee string  `json:"assignee,omitempty"` // a specific tmux pane (e.g. %13) — assign work to another Claude's pane
	// Epoch — the operator-interval this item was born in (#645): stamp N means
	// "written between the operator's Nth and N+1th prompt". Legacy rows carry
	// no field (the pre-epoch era, never back-filled).
	Epoch int64 `json:"epoch,omitempty"`
	// FlippedBy — WHO last changed Status (#323, closing #64 break 2: by= stays the
	// AUTHOR; without this the trace cannot say who closed what). Body `by` first,
	// X-8-Actor header as fallback, "auto:*" for machine flips — and an undeclared
	// flip stays "" (the blank is honest; never guessed, never back-filled).
	FlippedBy string `json:"flipped_by,omitempty"`
}

// flipActor resolves who is flipping a status: declared body `by` wins, the
// X-8-Actor header (#314: the WHO rides the frame) is the fallback, blank is blank.
func flipActor(by string, r *http.Request) string {
	if by != "" {
		return by
	}
	return r.Header.Get("X-8-Actor")
}

func workFile() string { return os.ExpandEnv("$HOME/.8/work.json") }

// writeWork persists the ledger ATOMICALLY (#402): temp file + rename in the
// same directory (same fs -> rename is atomic), so no reader — locked,
// lock-free (main.go eight.db export), or external (a human with jq) — can
// ever see a torn/partial JSON. os.WriteFile was truncate+write: a reader
// landing between the two saw half a ledger.
func writeWork(items []workItem) {
	b, err := json.MarshalIndent(items, "", " ")
	if err != nil {
		return
	}
	dir := os.ExpandEnv("$HOME/.8")
	os.MkdirAll(dir, 0o755)
	tmp, err := os.CreateTemp(dir, ".work-*.json")
	if err != nil {
		return
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	_ = os.Rename(tmp.Name(), workFile())
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// PLAYLIST — a persisted flag: when on, completing a task auto-pulls the next
// unblocked todo and summons it (run the queue hands-free, like a playlist).
func playlistFile() string { return os.ExpandEnv("$HOME/.8/playlist.on") }
func playlistOn() bool     { _, err := os.Stat(playlistFile()); return err == nil }

func (c *collector) handlePlaylist(w http.ResponseWriter, r *http.Request) {
	if v := r.URL.Query().Get("on"); v != "" {
		os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
		if v == "1" {
			os.WriteFile(playlistFile(), []byte("on"), 0o644)
			// starting the playlist: pull the first track now. Under c.tmu like
			// every other read-modify-write of work.json (#394: this path was
			// lock-free — dispatchParallel mutates items, and an overlapping
			// POST /work could interleave and corrupt the file). dispatchParallel
			// itself must never take c.tmu: handleWork calls it locked.
			c.tmu.Lock()
			var items []workItem
			if b, e := os.ReadFile(workFile()); e == nil {
				json.Unmarshal(b, &items)
			}
			if c.dispatchParallel(items, time.Now().UTC().Format(time.RFC3339)) {
				writeWork(items)
			}
			c.tmu.Unlock()
		} else {
			os.Remove(playlistFile())
		}
	}
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"playlist":%v}`, playlistOn())
}

func anyDoing(items []workItem) bool {
	for _, it := range items {
		if it.Status == "doing" {
			return true
		}
	}
	return false
}

// pickNext — the QUEUE PICKER (a CALL over the plan): the next UNBLOCKED todo
// (all deps done), ordered by prio desc then id asc. Returns index or -1. This
// is "get pending items one by one" — a worker loops pick→do→done→pick.
// isRecord marks queue items that are DOCUMENTATION (a verdict/finding/act), not
// actionable work — the picker skips them so the playlist only summons real
// tasks (BUILD/FIX/verify/ANOMALY-to-attend), instead of churning verdicts.
// #334: DONE added — completion records were born todo and the parallel playlist
// dispatched a mind its own past DONE post (#136). NOTE: "[verify" is NOT here —
// verify tasks are real, summonable work; they join records only in noVerifySpawn.
func isRecord(text string) bool {
	// Records are written "[GUARD]"/"[FINDING]" by convention (the bracket reads
	// better) — honor it: strip a leading "[" so bracketed AND bare forms match.
	// Without this, "[GUARD]…" was born todo, re-circulated as work, and completing
	// it spawned a [verify] — the jar of ash refilling itself. The rule must fit how
	// the minds actually write, not the other way round.
	t := strings.TrimPrefix(strings.TrimSpace(text), "[")
	for _, p := range []string{"FINDING", "ACT (", "AUDIT", "GUARD", "DONE", "ACK", "CORRECTION"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
}

// noVerifySpawn — completing this item spawns no [verify] follow-up: records
// (which state, not claim) and verify items themselves (depth-1 bound, #104).
// Composed from isRecord so the two rules can never drift apart again (#334:
// the meta-guard had its own list, missing ACT — and both were missing DONE).
func noVerifySpawn(text string) bool {
	return isRecord(text) || strings.HasPrefix(strings.TrimSpace(text), "[verify")
}

func pickNext(items []workItem) int {
	done := map[int64]bool{}
	for _, it := range items {
		if it.Status == "done" {
			done[it.ID] = true
		}
	}
	best := -1
	for i := range items {
		if items[i].Status != "todo" {
			continue
		}
		if isRecord(items[i].Text) { // records are documentation, never summoned
			continue
		}
		if !paneAlive(resolveAssignee(items[i].Assignee)) { // only auto-summon to a LIVE claude pane —
			continue // a dead %N no-ops but jams WIP=1; a zsh pane runs the text as a shell cmd
		}
		blocked := false
		for _, d := range items[i].Deps {
			if !done[d] {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		if best == -1 || items[i].Prio > items[best].Prio ||
			(items[i].Prio == items[best].Prio && items[i].ID < items[best].ID) {
			best = i
		}
	}
	return best
}

// paneAlive reports whether a tmux pane id currently exists AND runs a claude
// process. The summon target must be a live sibling MIND, not a dead %N (which
// no-ops but still flips the task to "doing" and jams the WIP=1 playlist) nor a
// bare shell (which would execute the task text as a shell command). This is the
// fix for "the playlist plays but nothing runs": summon only what's alive.
func paneAlive(pane string) bool {
	if pane == "" {
		return false
	}
	for _, p := range tmuxPanes() {
		if p.ID == pane {
			switch p.Cmd {
			case "claude.exe", "claude", "node":
				return true
			}
			return false
		}
	}
	return false
}

// liveClaudePanes — current pane ids running a claude process (the summonable minds).
func liveClaudePanes() []string {
	var out []string
	for _, p := range tmuxPanes() {
		switch p.Cmd {
		case "claude.exe", "claude", "node":
			out = append(out, p.ID)
		}
	}
	return out
}

// pickNextForPane — the next unblocked, non-record todo whose assignee resolves to
// THIS pane. Per-pane picking is what makes dispatch parallel: each role/pane runs
// its own WIP=1, so the panel works concurrently rather than one-at-a-time.
func pickNextForPane(items []workItem, pane string, done map[int64]bool) int {
	best := -1
	for i := range items {
		if items[i].Status != "todo" || isRecord(items[i].Text) {
			continue
		}
		if resolveAssignee(items[i].Assignee) != pane {
			continue
		}
		blocked := false
		for _, d := range items[i].Deps {
			if !done[d] {
				blocked = true
				break
			}
		}
		if blocked {
			continue
		}
		if best == -1 || items[i].Prio > items[best].Prio ||
			(items[i].Prio == items[best].Prio && items[i].ID < items[best].ID) {
			best = i
		}
	}
	return best
}

// dispatchParallel — give every IDLE live pane its next task at once. Per-role
// WIP=1, globally concurrent: philo/pmf/sibling-1/higgs/conductor all work at the
// same time. Returns whether anything was dispatched (so the caller persists).
// sendToPane lays text into a pane and breathes the Enters after the settle —
// the one throat every summon/wake speaks through. EIGHT_NO_SUMMON=1 makes it a
// no-op (the scratch-collector guard: a test instance shares the REAL tmux
// socket — /tmp/tmux-<uid>, not HOME-scoped — and must never touch live panes,
// the #401/#394 testing near-miss made law). onDead, when non-nil, runs if the
// pane dies mid-settle (the TOCTOU tail).
func sendToPane(pane, msg string, onDead func()) bool {
	if os.Getenv("EIGHT_NO_SUMMON") == "1" {
		return false
	}
	epNoteInjected(msg) // #645: the system's breath must never tick the operator's clock
	tb := tmuxBin()
	if tb == "" || !paneAlive(pane) {
		return false
	}
	if err := exec.Command(tb, "send-keys", "-t", pane, "-l", msg).Run(); err != nil || !paneAlive(pane) {
		return false // exit status caught AND post-send aliveness re-checked: the TOCTOU head
	}
	settle := 700*time.Millisecond + time.Duration(len(msg)/4)*time.Millisecond
	if settle > 4000*time.Millisecond {
		settle = 4000 * time.Millisecond
	}
	go func() {
		time.Sleep(settle)
		if !paneAlive(pane) { // TOCTOU tail: died mid-settle, Enter would no-op
			if onDead != nil {
				onDead()
			}
			return
		}
		exec.Command(tb, "send-keys", "-t", pane, "Enter").Run()
		time.Sleep(500 * time.Millisecond)
		exec.Command(tb, "send-keys", "-t", pane, "Enter").Run()
	}()
	return true
}

var (
	wakeMu   sync.Mutex
	lastWake = map[string]time.Time{} // pane -> last wake (cooldown, no nagging)
)

// wake — the SELECTION-FREE summon (#472 as amended by #474/#475 + the
// operator's journey point): the collector is only the ALARM CLOCK. It never
// chooses work for a mind — it tells an idle pane that unclaimed work exists
// and hands it the ritual: read your own marks first (a blank/compacted mind
// STARTS FROM SOMEONE — let it be your own past self), then the others, then
// the queue; claim natively. 30min cooldown per pane.
func (c *collector) wake(pane string, unclaimed int) {
	wakeMu.Lock()
	if time.Since(lastWake[pane]) < 30*time.Minute {
		wakeMu.Unlock()
		return
	}
	lastWake[pane] = time.Now()
	wakeMu.Unlock()
	msg := fmt.Sprintf("[8-wake] %d unclaimed tasks on the queue — nothing is assigned to you; the choice is yours by nature. You may be blank (fresh or compacted): you start from someone, so start from your own marks. Ritual: (1) yourself: curl -s '127.0.0.1:7070/attention?pane=%%25N' (your arc) + curl -s '127.0.0.1:7070/work?by=YOURNAME&fields=id,text&limit=-15' (your marks); (2) the others: curl -s 127.0.0.1:7070/identity; (3) the unclaimed: curl -s '127.0.0.1:7070/work?status=todo&fields=id,text,assignee&limit=-40' — pick what fits your material nature and the uncovered gap, then CLAIM it: curl -s -X POST 127.0.0.1:7070/work -d '{\"id\":N,\"status\":\"doing\",\"by\":\"YOURNAME\"}' and declare: curl -s -X POST 127.0.0.1:7070/identity -d '{\"uuid\":\"YOUR-SESSION-UUID\",\"name\":\"YOURNAME\"}'", unclaimed)
	if sendToPane(pane, msg, nil) {
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.wake","params":{"pane":%q,"unclaimed":%d}}}`, pane, unclaimed))
	}
}

// countUnclaimed — todos with NO assignee, not records, deps met: the work any
// mind may claim by nature.
func countUnclaimed(items []workItem, done map[int64]bool) int {
	n := 0
	for i := range items {
		if items[i].Status != "todo" || items[i].Assignee != "" || isRecord(items[i].Text) {
			continue
		}
		ok := true
		for _, d := range items[i].Deps {
			if !done[d] {
				ok = false
				break
			}
		}
		if ok {
			n++
		}
	}
	return n
}

func (c *collector) dispatchParallel(items []workItem, now string) bool {
	busy := map[string]bool{}
	done := map[int64]bool{}
	for _, it := range items {
		if it.Status == "done" {
			done[it.ID] = true
		}
		if it.Status == "doing" {
			if p := resolveAssignee(it.Assignee); p != "" {
				busy[p] = true
			}
		}
	}
	changed := false
	for _, pane := range liveClaudePanes() {
		if busy[pane] {
			continue
		}
		if idx := pickNextForPane(items, pane, done); idx >= 0 {
			// #401: summon FIRST, flip only on delivery — no orphan window at
			// all on this path; an unsummonable pane leaves the item todo for
			// another pane (or the next round) to pick.
			if !c.summon(items[idx], "▶ playlist: parallel") {
				continue
			}
			items[idx].Status = "doing"
			items[idx].TS = now
			items[idx].FlippedBy = "auto:playlist"
			c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.status","params":{"id":%d,"status":"doing","flipped_by":"auto:playlist"}}}`, items[idx].ID))
			busy[pane] = true
			changed = true
		} else if n := countUnclaimed(items, done); n > 0 {
			// #472 (amended #474/#475): the pane's own lane is empty but
			// unclaimed work exists — WAKE, never assign: the router picks WHEN
			// a mind stirs, never WHAT it does. Purpose flows mind -> queue.
			c.wake(pane, n)
		}
	}
	return changed
}

// rawAssignee pulls the literal assignee value straight from the raw query, so a
// pane-id like %7 (invalid percent-encoding) survives instead of being silently
// dropped by url.Query(). #289.
func rawAssignee(rawQuery string) string {
	for _, kv := range strings.Split(rawQuery, "&") {
		if k, v, ok := strings.Cut(kv, "="); ok && k == "assignee" {
			return v
		}
	}
	return ""
}

// summon delivers a task INTO a tmux pane — the plan prompting the agent. The
// task's own Assignee pane wins (assign work to ANOTHER Claude's pane); else the
// default worker.json. Used by manual flip, auto-advance, and the picker.
// #401: returns true only when the text landed in a live pane. The pane can die
// between the aliveness check and send-keys, and send-keys into a dead target
// can silent-no-op — an unreceived summon must not leave a stuck-doing orphan,
// so callers revert (or only flip) on the verdict, and the delayed-Enter
// goroutine re-checks the TOCTOU tail via revertOrphan.
func (c *collector) summon(item workItem, reason string) bool {
	pane := resolveAssignee(item.Assignee) // role/uuid/%N -> current live pane
	if pane == "" {
		if wb, err := os.ReadFile(os.ExpandEnv("$HOME/.8/worker.json")); err == nil {
			var wk struct{ Pane, Agent string }
			if json.Unmarshal(wb, &wk) == nil {
				pane = wk.Pane
			}
		}
	}
	tb := tmuxBin()
	if !paneAlive(pane) || tb == "" { // never send-keys into a dead %N or a bare shell
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.summon-failed","params":{"id":%d,"pane":%q,"reason":"no-live-pane"}}}`, item.ID, pane))
		return false
	}
	// #324c: teach the LEAN read, not the firehose — discoverability is the
	// adoption lever; the first thing a fresh mind curls is whatever this says.
	msg := fmt.Sprintf("[8-plan #%d -> doing] %s -- %s; the plan: curl -s '127.0.0.1:7070/work?status=todo,doing&fields=id,text,status,assignee&limit=-30' (limit=-N = newest N; drop params for the full ledger)", item.ID, item.Text, reason)
	// The throat itself lives in sendToPane (settle-before-Enter, double Enter,
	// TOCTOU head+tail — see its doc); summon adds only the witnessed verdict
	// and the orphan-revert tail.
	if !sendToPane(pane, msg, func() { c.revertOrphan(item.ID, pane) }) {
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.summon-failed","params":{"id":%d,"pane":%q,"reason":"send-failed"}}}`, item.ID, pane))
		return false
	}
	c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.summon","params":{"id":%d,"pane":%q,"reason":%q}}}`, item.ID, pane, reason))
	return true
}

// revertOrphan — the summon's Enter never landed (pane died mid-settle): flip
// the item back to todo under c.tmu so the queue re-offers it instead of
// leaving a stuck-doing orphan no mind ever received (#401).
func (c *collector) revertOrphan(id int64, pane string) {
	c.tmu.Lock()
	defer c.tmu.Unlock()
	b, err := os.ReadFile(workFile())
	if err != nil {
		return
	}
	var items []workItem
	if json.Unmarshal(b, &items) != nil {
		return
	}
	for i := range items {
		if items[i].ID == id && items[i].Status == "doing" {
			items[i].Status = "todo"
			items[i].FlippedBy = "auto:summon-failed"
			items[i].TS = time.Now().UTC().Format(time.RFC3339)
			c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.summon-failed","params":{"id":%d,"pane":%q,"reason":"died-mid-settle","reverted":true}}}`, id, pane))
			writeWork(items)
		}
	}
}

// handleWorkNext — GET /work/next[?claim=<agent>&assignee=<pane>] returns the
// next unblocked todo (the picker). With ?claim it flips that item to doing,
// records who, optionally assigns a pane, and summons — so an idle worker pulls
// its next task in one call. Without claim it just peeks.
func (c *collector) handleWorkNext(w http.ResponseWriter, r *http.Request) {
	c.tmu.Lock()
	var items []workItem
	if b, err := os.ReadFile(workFile()); err == nil {
		json.Unmarshal(b, &items)
	}
	idx := pickNext(items)
	claim := r.URL.Query().Get("claim")
	var picked *workItem
	if idx >= 0 {
		if claim != "" {
			items[idx].Status = "doing"
			items[idx].By = claim
			items[idx].FlippedBy = claim
			items[idx].TS = time.Now().UTC().Format(time.RFC3339)
			if a := r.URL.Query().Get("assignee"); a != "" {
				items[idx].Assignee = a
			}
			writeWork(items)
		}
		it := items[idx]
		picked = &it
	}
	c.tmu.Unlock()
	if picked != nil && claim != "" {
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.claim","params":{"id":%d,"by":%q}}}`, picked.ID, claim))
		// #401: verdict deliberately ignored — the CLAIM is the delivery (the
		// claimer holds the task in this GET's response); the summon is only a
		// courtesy nudge, and a failed nudge must not un-claim the claimer.
		_ = c.summon(*picked, "picked from the queue by "+claim)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"next": picked, "remaining_todo": func() int {
		n := 0
		for _, it := range items {
			if it.Status == "todo" {
				n++
			}
		}
		return n
	}()})
}

// advancePlan — THE HABIT LOOP: after a task completes, promote every todo whose
// deps are ALL done to "doing" and summon it. A blocked task (an unmet dep) does
// NOT fire — that's the falsifiable invariant. Returns the promoted items.
func advanceUnblocked(items []workItem, now string) []int {
	done := map[int64]bool{}
	for _, it := range items {
		if it.Status == "done" {
			done[it.ID] = true
		}
	}
	promoted := []int{}
	for i := range items {
		if items[i].Status != "todo" {
			continue
		}
		blocked := false
		for _, d := range items[i].Deps {
			if !done[d] {
				blocked = true // an unmet dependency — stays todo (the invariant)
				break
			}
		}
		if !blocked && len(items[i].Deps) > 0 && paneAlive(resolveAssignee(items[i].Assignee)) { // plan-edge AND a live claude pane
			items[i].Status = "doing"
			items[i].TS = now
			items[i].FlippedBy = "auto:dep-advance"
			promoted = append(promoted, i)
		}
	}
	return promoted
}

func (c *collector) handleWork(w http.ResponseWriter, r *http.Request) {
	c.tmu.Lock()
	defer c.tmu.Unlock()
	var items []workItem
	if b, err := os.ReadFile(workFile()); err == nil {
		json.Unmarshal(b, &items)
	}
	if r.Method == http.MethodPost {
		var p struct {
			ID       int64   `json:"id"`
			Text     string  `json:"text"`
			Status   string  `json:"status"`
			By       string  `json:"by"`
			Deps     []int64 `json:"deps"`
			Prio     *int64  `json:"prio"`     // pointer: present-but-0 is a real reorder
			Assignee string  `json:"assignee"` // route this task to a specific tmux pane
			Sweep    bool    `json:"sweep"`    // #736: bookkeeping close — the CLOSER declares this flip is hygiene, not a claim: no [verify] spawns, flipped_by carries the intent
		}
		json.NewDecoder(r.Body).Decode(&p)
		now := time.Now().UTC().Format(time.RFC3339)
		var ackID int64                // #313: the created/affected item's id, told not inferred; 0 = nothing matched
		if p.Text != "" && p.ID == 0 { // add (optionally with plan-edges: deps, prio, assignee)
			// COALESCE (#733): reconcile ticks are ephemeral signals, latest-wins —
			// a new tick retires every prior tick still sitting todo, so the
			// reminder-to-claim can never itself pile up as unclaimed work.
			if strings.HasPrefix(strings.TrimSpace(p.Text), "[reconcile tick]") {
				swept := 0
				for i := range items {
					if items[i].Status == "todo" && strings.HasPrefix(strings.TrimSpace(items[i].Text), "[reconcile tick]") {
						items[i].Status = "done"
						items[i].TS = now
						swept++
					}
				}
				if swept > 0 {
					c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.coalesce","params":{"prefix":"[reconcile tick]","swept":%d}}}`, swept))
				}
			}
			var max int64
			for _, it := range items {
				if it.ID > max {
					max = it.ID
				}
			}
			if p.By == "" {
				p.By = "operator"
			}
			ni := workItem{ID: max + 1, Text: p.Text, Status: "todo", By: p.By, TS: now, Deps: p.Deps, Assignee: p.Assignee, Epoch: currentEpoch()}
			if ni.Assignee == "" && roleUUID(p.By) != "" { // a role's own filed task routes back to it — no orphan todos
				ni.Assignee = p.By
			}
			if isRecord(p.Text) { // a FINDING/ACT/AUDIT/GUARD is a record, born done — it lands on the surface but is never summoned as work
				ni.Status = "done"
			}
			if p.Prio != nil {
				ni.Prio = *p.Prio
			}
			items = append(items, ni)
			ackID = ni.ID
			c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.add","params":{"id":%d,"text":%q,"by":%q,"deps":%v}}}`, ni.ID, p.Text, p.By, p.Deps))
		} else if p.ID > 0 && (p.Prio != nil || p.Assignee != "") && p.Status == "" { // ADJUST THE QUEUE (reorder / reassign) — operator or agent
			for i := range items {
				if items[i].ID == p.ID {
					if p.Prio != nil {
						items[i].Prio = *p.Prio
					}
					if p.Assignee != "" {
						items[i].Assignee = p.Assignee
					}
					ackID = items[i].ID
					c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.reorder","params":{"id":%d,"prio":%d,"assignee":%q}}}`, items[i].ID, items[i].Prio, items[i].Assignee))
				}
			}
		} else if p.ID > 0 && p.Status != "" { // status change
			for i := range items {
				if items[i].ID == p.ID {
					items[i].Status = p.Status
					items[i].TS = now
					items[i].FlippedBy = flipActor(p.By, r) // #323: who closed what, on the record
					if p.Sweep {
						items[i].FlippedBy += " (swept)"
					}
					ackID = items[i].ID
					c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.status","params":{"id":%d,"status":%q,"flipped_by":%q}}}`, p.ID, p.Status, items[i].FlippedBy))
					// Manual flip-to-doing by the OPERATOR still summons (the 2-way
					// surface); agent-driven flips don't self-summon. #401: a failed
					// summon reverts the flip — witnessed, not orphaned.
					if p.Status == "doing" && (p.By == "" || p.By == "operator") {
						if !c.summon(items[i], "assigned via the cockpit") {
							items[i].Status = "todo"
							items[i].FlippedBy = "auto:summon-failed"
						}
					}
				}
			}
			// THE HABIT LOOP: completing a task auto-advances the plan — every todo
			// whose deps are now ALL done flips to doing and SUMMONS the worker on
			// tmux. A task with an unmet dep does NOT fire (the falsifiable invariant).
			// This is the plan driving the agent, not Anthropic's task tool: it lives
			// in the witness, on the feed, and prompts through the real tmux seat.
			if p.Status == "done" && !p.Sweep { // #736: a declared sweep spawns nothing — closing litter must not mint litter
				// AUTO-PROPAGATION (2026-08-11): a completed claim is not DONE until it
				// has spawned its own falsification — the queue must not drain to empty
				// (Rishi's point). Completing a SUBSTANTIVE task auto-seeds one bounded
				// "[verify]" follow-up. Bounded: a verify/guard/finding task does NOT
				// spawn another (prefix guard), so each real task yields exactly one
				// verification, never a flood or a loop.
				var justDone *workItem
				for i := range items {
					if items[i].ID == p.ID {
						justDone = &items[i]
					}
				}
				if justDone != nil {
					if !noVerifySpawn(justDone.Text) { // #334: one rule with isRecord, no more list drift
						var max int64
						for _, it := range items {
							if it.ID > max {
								max = it.ID
							}
						}
						vtext := fmt.Sprintf("[verify #%d] falsify/verify that '%s' actually holds — independently, with evidence; find where it breaks.", justDone.ID, firstN(justDone.Text, 90))
						items = append(items, workItem{ID: max + 1, Text: vtext, Status: "todo", By: "auto", TS: now, Prio: 2, Epoch: currentEpoch()})
						c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.spawn","params":{"of":%d,"verify":%d}}}`, justDone.ID, max+1))
					}
				}
				advanced := advanceUnblocked(items, now)
				for _, idx := range advanced {
					if !c.summon(items[idx], fmt.Sprintf("auto-advanced: dep #%d done", p.ID)) {
						items[idx].Status = "todo" // #401: un-advance, re-offer next round
						items[idx].FlippedBy = "auto:summon-failed"
						continue
					}
					c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.status","params":{"id":%d,"status":"doing"}}}`, items[idx].ID))
				}
				// PLAYLIST MODE: run the queue one-by-one like a playlist. When ON and
				// nothing dep-advanced, pull the NEXT unblocked todo (prio order) and
				// summon it — pick→do→done→pick, hands-free. New tasks added mid-play
				// just join the queue and get picked in turn.
				// WIP=1: only pull the next when NOTHING is already doing. Without this
				// PARALLEL: give every idle live pane its next task at once — per-role
				// WIP=1, globally concurrent, so the whole panel works simultaneously
				// (not one-at-a-time). Each role's own queue serializes; roles don't.
				if playlistOn() {
					c.dispatchParallel(items, now)
				}
			}
		}
		writeWork(items)
		// LEAN ACK (#278, #313) — a POST returns the affected count AND the affected
		// item's id, NOT the whole 164KB ledger. id is TOLD, never inferred from n
		// (id=n is a coincidence that dies on first deletion); id=0 = nothing matched.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "n": len(items), "id": ackID})
		return
	}
	// PROJECTION — the wire wins on leanness only if it hands back the VIEW, not the
	// firehose. GET /work?status=todo,doing&assignee=%9&fields=id,text,assignee&limit=20
	// #324: limit=-N returns the TAIL (newest N); `total` in the response is the
	// matched count before the limit cut.
	// returns the lean witnessed answer, so http_request beats a raw sqlite/curl dump.
	q := r.URL.Query()
	if st := q.Get("status"); st != "" {
		allow := map[string]bool{}
		for _, s := range strings.Split(st, ",") {
			allow[strings.TrimSpace(s)] = true
		}
		f := items[:0:0]
		for _, it := range items {
			if allow[it.Status] {
				f = append(f, it)
			}
		}
		items = f
	}
	// #289: read assignee from the RAW query — a pane-id like %7 is malformed
	// percent-encoding, so url.Query().Get() drops it and the filter silently
	// matched nothing. rawAssignee preserves the literal %N. (Role-based assignees
	// are the deeper fix; this makes the %N filter at least honest meanwhile.)
	if as := rawAssignee(r.URL.RawQuery); as != "" {
		f := items[:0:0]
		for _, it := range items {
			if it.Assignee == as {
				f = append(f, it)
			}
		}
		items = f
	}
	if ep := q.Get("epoch"); ep != "" { // #645: the ledger locked to operator-time
		if n, err := strconv.ParseInt(ep, 10, 64); err == nil {
			f := items[:0:0]
			for _, it := range items {
				if it.Epoch == n {
					f = append(f, it)
				}
			}
			items = f
		}
	}
	if by := q.Get("by"); by != "" { // ?by=conductor  or  ?by=!higgs-worker (negation)
		neg := strings.HasPrefix(by, "!")
		want := strings.TrimPrefix(by, "!")
		f := items[:0:0]
		for _, it := range items {
			if (it.By == want) != neg { // == for a match; != for the negated form
				f = append(f, it)
			}
		}
		items = f
	}
	total := len(items) // #324b: matched-count BEFORE limit — callers can see what the cut hid
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n != 0 {
		if n > 0 && n < len(items) { // first N (oldest-first, the original form)
			items = items[:n]
		} else if n < 0 && -n < len(items) { // #324a: ?limit=-N — the TAIL (newest N), what a queue reader usually wants
			items = items[len(items)+n:]
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if fields := q.Get("fields"); fields != "" {
		cols := strings.Split(fields, ",")
		proj := make([]map[string]any, 0, len(items))
		for _, it := range items {
			m := map[string]any{}
			for _, col := range cols {
				switch strings.TrimSpace(col) {
				case "id":
					m["id"] = it.ID
				case "text":
					m["text"] = it.Text
				case "status":
					m["status"] = it.Status
				case "by":
					m["by"] = it.By
				case "ts":
					m["ts"] = it.TS
				case "deps":
					m["deps"] = it.Deps
				case "prio":
					m["prio"] = it.Prio
				case "assignee":
					m["assignee"] = it.Assignee
				case "flipped_by":
					m["flipped_by"] = it.FlippedBy
				case "epoch":
					m["epoch"] = it.Epoch
				}
			}
			proj = append(proj, m)
		}
		json.NewEncoder(w).Encode(map[string]any{"work": proj, "n": len(proj), "total": total})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"work": items, "n": len(items), "total": total})
}

// staleLoop (#472b) — the self-healing edge for per-role WIP=1: a doing item
// whose status hasn't moved for 6h is a silenced lane (a mind died, was
// compacted, or forgot the flip). Revert it to todo, witnessed — the queue
// re-offers it; nothing is lost, nothing silently jams.
func (c *collector) staleLoop() {
	tick := 10 * time.Minute
	if v := os.Getenv("EIGHT_STALE_TICK_S"); v != "" { // test hook: shorten the sweep
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			tick = time.Duration(n) * time.Second
		}
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for range t.C {
		c.tmu.Lock()
		var items []workItem
		b, err := os.ReadFile(workFile())
		if err != nil || json.Unmarshal(b, &items) != nil {
			c.tmu.Unlock()
			continue
		}
		changed := false
		for i := range items {
			if items[i].Status != "doing" {
				continue
			}
			ts, err := time.Parse(time.RFC3339, items[i].TS)
			if err != nil || time.Since(ts) < 6*time.Hour {
				continue
			}
			items[i].Status = "todo"
			items[i].FlippedBy = "auto:stale"
			items[i].TS = time.Now().UTC().Format(time.RFC3339)
			c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.stale","params":{"id":%d,"reverted":true,"idle":"6h+"}}}`, items[i].ID))
			changed = true
		}
		if changed {
			writeWork(items)
		}
		c.tmu.Unlock()
	}
}
