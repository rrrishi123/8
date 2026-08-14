package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
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
}

func workFile() string { return os.ExpandEnv("$HOME/.8/work.json") }

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
			// starting the playlist: pull the first track now
			var items []workItem
			if b, e := os.ReadFile(workFile()); e == nil {
				json.Unmarshal(b, &items)
			}
			if c.dispatchParallel(items, time.Now().UTC().Format(time.RFC3339)) {
				if b, e := json.MarshalIndent(items, "", " "); e == nil {
					os.WriteFile(workFile(), b, 0o644)
				}
			}
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
func isRecord(text string) bool {
	t := strings.TrimSpace(text)
	for _, p := range []string{"FINDING", "ACT (", "AUDIT", "GUARD"} {
		if strings.HasPrefix(t, p) {
			return true
		}
	}
	return false
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
			items[idx].Status = "doing"
			items[idx].TS = now
			c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.status","params":{"id":%d,"status":"doing"}}}`, items[idx].ID))
			c.summon(items[idx], "▶ playlist: parallel")
			busy[pane] = true
			changed = true
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
func (c *collector) summon(item workItem, reason string) {
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
		return
	}
	msg := fmt.Sprintf("[8-plan #%d -> doing] %s -- %s; the plan: curl -s 127.0.0.1:7070/work", item.ID, item.Text, reason)
	exec.Command(tb, "send-keys", "-t", pane, "-l", msg).Run()
	// SETTLE BEFORE ENTER (2026-08-11): send-keys returns once keys are QUEUED,
	// not once the TUI has INGESTED them. For a long paste the Enter beat the
	// ingest and submitted empty/partial — then the text landed and just sat in
	// the prompt (#16 never fired). Fix: wait for the TUI to absorb the paste
	// (delay scales with length), THEN Enter; a backup Enter after another beat
	// catches a still-settling ingest. A second Enter is safe — Claude Code
	// ignores an empty submit, so it can't double-post.
	settle := 700*time.Millisecond + time.Duration(len(msg)/4)*time.Millisecond
	if settle > 4000*time.Millisecond {
		settle = 4000 * time.Millisecond
	}
	go func() {
		time.Sleep(settle)
		exec.Command(tb, "send-keys", "-t", pane, "Enter").Run()
		time.Sleep(500 * time.Millisecond)
		exec.Command(tb, "send-keys", "-t", pane, "Enter").Run()
	}()
	c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.summon","params":{"id":%d,"pane":%q,"reason":%q}}}`, item.ID, pane, reason))
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
			items[idx].TS = time.Now().UTC().Format(time.RFC3339)
			if a := r.URL.Query().Get("assignee"); a != "" {
				items[idx].Assignee = a
			}
			if b, e := json.MarshalIndent(items, "", " "); e == nil {
				os.WriteFile(workFile(), b, 0o644)
			}
		}
		it := items[idx]
		picked = &it
	}
	c.tmu.Unlock()
	if picked != nil && claim != "" {
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.claim","params":{"id":%d,"by":%q}}}`, picked.ID, claim))
		c.summon(*picked, "picked from the queue by "+claim)
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
		}
		json.NewDecoder(r.Body).Decode(&p)
		now := time.Now().UTC().Format(time.RFC3339)
		var ackID int64                // #313: the created/affected item's id, told not inferred; 0 = nothing matched
		if p.Text != "" && p.ID == 0 { // add (optionally with plan-edges: deps, prio, assignee)
			var max int64
			for _, it := range items {
				if it.ID > max {
					max = it.ID
				}
			}
			if p.By == "" {
				p.By = "operator"
			}
			ni := workItem{ID: max + 1, Text: p.Text, Status: "todo", By: p.By, TS: now, Deps: p.Deps, Assignee: p.Assignee}
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
					ackID = items[i].ID
					c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.status","params":{"id":%d,"status":%q}}}`, p.ID, p.Status))
					// Manual flip-to-doing by the OPERATOR still summons (the 2-way
					// surface); agent-driven flips don't self-summon.
					if p.Status == "doing" && (p.By == "" || p.By == "operator") {
						c.summon(items[i], "assigned via the cockpit")
					}
				}
			}
			// THE HABIT LOOP: completing a task auto-advances the plan — every todo
			// whose deps are now ALL done flips to doing and SUMMONS the worker on
			// tmux. A task with an unmet dep does NOT fire (the falsifiable invariant).
			// This is the plan driving the agent, not Anthropic's task tool: it lives
			// in the witness, on the feed, and prompts through the real tmux seat.
			if p.Status == "done" {
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
					t := justDone.Text
					meta := strings.HasPrefix(t, "[verify") || strings.HasPrefix(t, "GUARD") || strings.HasPrefix(t, "FINDING") || strings.HasPrefix(t, "AUDIT")
					if !meta {
						var max int64
						for _, it := range items {
							if it.ID > max {
								max = it.ID
							}
						}
						vtext := fmt.Sprintf("[verify #%d] falsify/verify that '%s' actually holds — independently, with evidence; find where it breaks.", justDone.ID, firstN(t, 90))
						items = append(items, workItem{ID: max + 1, Text: vtext, Status: "todo", By: "auto", TS: now, Prio: 2})
						c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.spawn","params":{"of":%d,"verify":%d}}}`, justDone.ID, max+1))
					}
				}
				advanced := advanceUnblocked(items, now)
				for _, idx := range advanced {
					c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.status","params":{"id":%d,"status":"doing"}}}`, items[idx].ID))
					c.summon(items[idx], fmt.Sprintf("auto-advanced: dep #%d done", p.ID))
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
		os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
		if b, err := json.MarshalIndent(items, "", " "); err == nil {
			os.WriteFile(workFile(), b, 0o644)
		}
		// LEAN ACK (#278, #313) — a POST returns the affected count AND the affected
		// item's id, NOT the whole 164KB ledger. id is TOLD, never inferred from n
		// (id=n is a coincidence that dies on first deletion); id=0 = nothing matched.
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "n": len(items), "id": ackID})
		return
	}
	// PROJECTION — the wire wins on leanness only if it hands back the VIEW, not the
	// firehose. GET /work?status=todo,doing&assignee=%9&fields=id,text,assignee&limit=20
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
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n >= 0 && n < len(items) {
		items = items[:n]
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
				}
			}
			proj = append(proj, m)
		}
		json.NewEncoder(w).Encode(map[string]any{"work": proj, "n": len(proj)})
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"work": items, "n": len(items)})
}
