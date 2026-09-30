package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── PANE LINEAGE ACROSS TMUX BOOTS — the %N re-mint problem ────────────────────
// %N is a tmux-SERVER-local counter: every server restart (reboot, restore-8)
// re-mints ids from %0, so any literal %N inscribed in the ledger goes stale and
// points at WHOEVER now sits in that seat. 2026-09-14: pre-restore items said
// "%7 = conductor"; after the restore %7 was a host pane, the playlist sprayed
// it, and the operator hand-translated work.json. That translation is now the
// system's own tick.
//
// The stable key is the claude session uuid (roles.go: "the number churns, the
// identity doesn't"); the NUMBERING EPOCH is the tmux server's start_time. Law:
//   1. every ledger item carries `boot` — the numbering its %N fields mean
//      (stamped in writeWork; a blank means "current numbering", i.e. legacy
//      rows at first deploy — the ledger was verified current that day);
//   2. the witness records boot -> {%N -> uuid} every tick as it sees minds,
//      durable in ~/.8/lineage.json (eight.db stays a lens);
//   3. under a NEW boot, items of an old boot are QUARANTINED from dispatch
//      (never summoned, never family, never busy) until every %N in them is
//      translated: old boot lineage -> uuid -> that uuid's CURRENT pane. A seat
//      that never held a mind becomes ex-%N (no identity, no lineage). A mind
//      not yet resumed keeps its items quarantined — they wait, they don't spray.

var (
	bootMu  sync.Mutex
	curBoot string
	// remintPending — items still quarantined after the last pass (a mind not
	// back yet); the witness re-runs the pass each tick while this is non-zero.
	remintPending = -1 // -1 = never ran
	lastBoot      string
)

func currentBoot() string     { bootMu.Lock(); defer bootMu.Unlock(); return curBoot }
func setCurrentBoot(b string) { bootMu.Lock(); curBoot = b; bootMu.Unlock() }

// tmuxBoot — the tmux server's start_time (epoch seconds): the numbering epoch.
// Blank when tmux is unreachable (then nothing is stamped or quarantined).
func tmuxBoot() string {
	tb := tmuxBin()
	if tb == "" {
		return ""
	}
	out, err := tmuxOut(tb, "display-message", "-p", "#{start_time}")
	if err != nil {
		return ""
	}
	s := strings.TrimSpace(string(out))
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return ""
		}
	}
	return s
}

// staleBoot — the item's %N fields belong to an older numbering than the live
// tmux server's. Blank item boot = current (legacy/just-created), blank current
// boot = tmux unreachable, never quarantine on a guess.
func staleBoot(it workItem) bool {
	b := currentBoot()
	return b != "" && it.Boot != "" && it.Boot != b
}

type lineageFile struct {
	Boots map[string]map[string]string `json:"boots"` // boot -> %N -> claude uuid
	Seen  map[string]string            `json:"seen"`  // boot -> last witnessed (RFC3339)
}

func lineagePath() string { return os.ExpandEnv("$HOME/.8/lineage.json") }

func loadLineage() *lineageFile {
	l := &lineageFile{Boots: map[string]map[string]string{}, Seen: map[string]string{}}
	if b, err := os.ReadFile(lineagePath()); err == nil {
		_ = json.Unmarshal(b, l)
		if l.Boots == nil {
			l.Boots = map[string]map[string]string{}
		}
		if l.Seen == nil {
			l.Seen = map[string]string{}
		}
	}
	return l
}

func saveLineage(l *lineageFile) {
	b, err := json.MarshalIndent(l, "", " ")
	if err != nil {
		return
	}
	dir := os.ExpandEnv("$HOME/.8")
	tmp, err := os.CreateTemp(dir, ".lineage-*.json")
	if err != nil {
		return
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	_ = os.Rename(tmp.Name(), lineagePath())
}

// recordLineage — merge this tick's pane->uuid sightings under `boot`; returns
// the pairs that are new (a mind seated, or re-seated after a restart).
func recordLineage(l *lineageFile, boot, now string, paneUUID map[string]string) [][2]string {
	if l.Boots[boot] == nil {
		l.Boots[boot] = map[string]string{}
	}
	var fresh [][2]string
	for pane, uuid := range paneUUID {
		if uuid == "" {
			continue
		}
		if l.Boots[boot][pane] != uuid {
			l.Boots[boot][pane] = uuid
			fresh = append(fresh, [2]string{pane, uuid})
		}
	}
	l.Seen[boot] = now
	return fresh
}

// remintChange — one field translated on one item (the receipt of a re-mint).
type remintChange struct {
	ID    int64  `json:"id"`
	Field string `json:"field"`
	From  string `json:"from"`
	To    string `json:"to"`
}

// remintItems — PURE: translate every stale item's %N fields through the old
// boot's lineage into the current numbering. Returns the changes applied and
// how many items remain quarantined (their mind isn't back yet, or their boot
// has no recorded lineage — both wait rather than guess). Blank-boot items are
// stamped current (rule 1). livePane maps uuid -> current pane id.
func remintItems(items []workItem, boot string, l *lineageFile, livePane map[string]string) ([]remintChange, int) {
	var changes []remintChange
	pending := 0
	for i := range items {
		it := &items[i]
		if it.Boot == "" {
			it.Boot = boot
			continue
		}
		if it.Boot == boot {
			continue
		}
		old, known := l.Boots[it.Boot]
		if !known {
			pending++ // a numbering we never witnessed: nothing to translate through, so wait
			continue
		}
		fields := []struct {
			name string
			p    *string
		}{{"by", &it.By}, {"assignee", &it.Assignee}, {"flipped_by", &it.FlippedBy}, {"delivered_to", &it.DeliveredTo}, {"acked_by", &it.AckedBy}}
		var local []remintChange
		wait := false
		for _, f := range fields {
			v := *f.p
			if !isPaneID(v) {
				continue
			}
			uuid := old[v]
			if uuid == "" { // that seat never held a mind under that boot -> no lineage
				local = append(local, remintChange{it.ID, f.name, v, "ex-" + v})
				continue
			}
			np := livePane[uuid]
			if np == "" { // the mind isn't seated yet under this boot -> keep waiting
				wait = true
				break
			}
			if np != v {
				local = append(local, remintChange{it.ID, f.name, v, np})
			}
		}
		if wait {
			pending++
			continue
		}
		for _, ch := range local {
			switch ch.Field {
			case "by":
				it.By = ch.To
			case "assignee":
				it.Assignee = ch.To
			case "flipped_by":
				it.FlippedBy = ch.To
			case "delivered_to":
				it.DeliveredTo = ch.To
			case "acked_by":
				it.AckedBy = ch.To
			}
		}
		changes = append(changes, local...)
		it.Boot = boot
	}
	return changes, pending
}

// witnessLineage — the per-tick half that needs tmux: current boot, pane->uuid,
// record lineage, and (when the boot moved or items are still quarantined) run
// the re-mint over the ledger under the work mutex.
func (c *collector) witnessLineage(now string) {
	boot := tmuxBoot()
	setCurrentBoot(boot)
	if boot == "" {
		return
	}
	pidOf := map[string]string{}
	if tb := tmuxBin(); tb != "" {
		if out, err := tmuxOut(tb, "list-panes", "-a", "-F", "#{pane_id}|#{pane_pid}"); err == nil {
			for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if id, pid, ok := strings.Cut(ln, "|"); ok {
					pidOf[id] = pid
				}
			}
		}
	}
	uu := paneUUIDs()
	paneUUID := map[string]string{}
	livePane := map[string]string{}
	for pane, pid := range pidOf {
		if u := uu[pid]; u != "" {
			paneUUID[pane] = u
			livePane[u] = pane
		}
	}
	l := loadLineage()
	fresh := recordLineage(l, boot, now, paneUUID)
	saveLineage(l)
	for _, f := range fresh {
		c.publish(fmt.Sprintf(`{"session":"panes","origin":"COLLECTOR","frame":{"method":"pane.lineage","params":{"boot":%q,"pane":%q,"uuid":%q,"at":%q}}}`, boot, f[0], f[1], now))
	}
	bootMoved := boot != lastBoot
	lastBoot = boot
	if os.Getenv("EIGHT_LINEAGE_OBSERVE") == "1" { // a second witness on another tmux server: record, never re-mint
		return
	}
	if !bootMoved && remintPending == 0 && len(fresh) == 0 {
		return
	}
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
	before := 0
	for _, it := range items {
		if it.Boot != boot {
			before++
		}
	}
	changes, pending := remintItems(items, boot, l, livePane)
	remintPending = pending
	if before == 0 {
		return
	}
	writeWork(items)
	cb, _ := json.Marshal(changes)
	c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.remint","params":{"boot":%q,"restamped":%d,"changes":%s,"quarantined":%d,"at":%q}}}`, boot, before-pending, cb, pending, now))
}

// handleLineage — GET /lineage: the numbering epoch, boot -> seat -> uuid, and
// how many ledger items are quarantined under an old numbering right now.
func (c *collector) handleLineage(w http.ResponseWriter, r *http.Request) {
	l := loadLineage()
	boot := currentBoot()
	quar := 0
	var ids []int64
	if b, err := os.ReadFile(workFile()); err == nil {
		var items []workItem
		if json.Unmarshal(b, &items) == nil {
			for _, it := range items {
				if staleBoot(it) {
					quar++
					if len(ids) < 50 {
						ids = append(ids, it.ID)
					}
				}
			}
		}
	}
	boots := make([]string, 0, len(l.Boots))
	for b := range l.Boots {
		boots = append(boots, b)
	}
	sort.Strings(boots)
	bootAt := ""
	if boot != "" {
		var secs int64
		fmt.Sscan(boot, &secs)
		bootAt = time.Unix(secs, 0).UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"boot": boot, "boot_at": bootAt, "boots": boots, "lineage": l.Boots, "seen": l.Seen,
		"quarantined": quar, "quarantined_ids": ids,
		"note": "%N is per tmux boot; items stamped with an older boot are quarantined until translated via uuid",
	})
}
