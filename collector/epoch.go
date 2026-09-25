package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

// ── OPERATOR-EPOCH LOCK (#645) — the ledger keeps the operator's clock ───────
// Measured motivation: across a 7h film run, every inter-prompt gap was
// %7-before/%7-after — wall-clock flattens the operator's rhythm out of the
// ledger. An EPOCH is the interval between two real operator turns; every
// work write is stamped with the epoch it happened in, so ?epoch=N answers
// "what did the whole system do between my Nth and N+1th prompt".
//
// WHAT COUNTS AS AN OPERATOR TURN (the falsification, built in):
//  - a new role:user line in a watched pane's transcript WHOSE CONTENT IS
//    PLAIN TEXT — tool_results ride role:user and are excluded by shape;
//  - NOT system-shaped: <system-reminder>/<local-command…>/[Request…/
//    task-notification/compaction continuations are all skipped;
//  - NOT something the collector itself injected: every sendToPane payload
//    is fingerprinted for 10min and matching "user" lines do not tick —
//    a summon/wake/fan-out is the system's breath, never the operator's;
//  - ticks across ALL panes debounce into one 30s attention-burst: the epoch
//    is GLOBAL — one operator, one clock (multi-pane fan-out typing is one
//    burst of attention, not N).
// Compaction: a shrunken transcript resyncs the offset WITHOUT ticking.
// Legacy: rows born before epochs carry no field (epoch 0 = the pre-epoch
// era); nothing is back-filled. The counter persists in ~/.8/epoch.json so
// epochs never repeat across collector restarts (a mark, not a memory).

const (
	epochDebounce  = 30 * time.Second
	metabolismMinW = 30               // writes in one epoch with no tick → hot-run
	metabolismMinT = 60 * time.Minute // or this much wall time with no tick
)

var (
	epMu       sync.Mutex
	epNum      int64
	epStart    = time.Now()
	epWrites   int64
	epLastTick time.Time
	epOffsets  = map[string]int64{} // jsonl path -> bytes scanned
	epFPs      []epFP               // recent injected-prompt fingerprints
	epLoaded   bool
)

type epFP struct {
	fp string
	at time.Time
}

func epochFile() string { return os.ExpandEnv("$HOME/.8/epoch.json") }

func epochLoad() {
	if epLoaded {
		return
	}
	epLoaded = true
	if b, err := os.ReadFile(epochFile()); err == nil {
		var v struct{ Epoch int64 }
		if json.Unmarshal(b, &v) == nil {
			epNum = v.Epoch
		}
	}
}

func epochSave() {
	b, _ := json.Marshal(map[string]int64{"Epoch": epNum})
	_ = os.WriteFile(epochFile(), b, 0o644)
}

// currentEpoch — the stamp for every work write; also counts the write for
// the metabolism signal.
func currentEpoch() int64 {
	epMu.Lock()
	defer epMu.Unlock()
	epochLoad()
	epWrites++
	return epNum
}

func epFingerprint(s string) string {
	s = strings.ToLower(strings.Join(strings.Fields(s), " "))
	if len(s) > 80 {
		s = s[:80]
	}
	return s
}

// epNoteInjected — sendToPane calls this: what the system breathes into a
// pane must never tick the operator's clock.
func epNoteInjected(msg string) {
	epMu.Lock()
	defer epMu.Unlock()
	now := time.Now()
	kept := epFPs[:0]
	for _, f := range epFPs {
		if now.Sub(f.at) < 10*time.Minute {
			kept = append(kept, f)
		}
	}
	epFPs = append(kept, epFP{fp: epFingerprint(msg), at: now})
}

func epIsInjected(s string) bool {
	f := epFingerprint(s)
	for _, k := range epFPs {
		if k.fp != "" && (strings.HasPrefix(f, k.fp) || strings.HasPrefix(k.fp, f)) {
			return true
		}
	}
	return false
}

func epSystemShaped(s string) bool {
	t := strings.TrimSpace(s)
	return t == "" || strings.HasPrefix(t, "<") || strings.HasPrefix(t, "[Request") ||
		strings.Contains(t[:min(len(t), 200)], "This session is being continued") ||
		strings.HasPrefix(t, "Caveat:")
}

// epochLoop — watch every live claude pane's transcript for REAL operator
// turns; tick the global epoch on a qualifying appended user line.
func (c *collector) epochLoop() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for range t.C {
		tb := tmuxBin()
		if tb == "" {
			continue
		}
		ticked := false
		for _, p := range tmuxPanes() {
			switch p.Cmd {
			case "claude.exe", "claude", "node":
			default:
				continue
			}
			path, _ := paneTranscript(tb, p.ID)
			if path == "" {
				continue
			}
			fi, err := os.Stat(path)
			if err != nil {
				continue
			}
			epMu.Lock()
			off := epOffsets[path]
			size := fi.Size()
			if size < off { // compaction/rewrite: resync silently, never tick
				epOffsets[path] = size
				epMu.Unlock()
				continue
			}
			if off == 0 { // first sight: seed at end — history is not a turn
				epOffsets[path] = size
				epMu.Unlock()
				continue
			}
			if size == off {
				epMu.Unlock()
				continue
			}
			epOffsets[path] = size
			epMu.Unlock()
			if size-off > 1<<20 { // huge append = bulk rewrite, not typing
				continue
			}
			f, err := os.Open(path)
			if err != nil {
				continue
			}
			buf := make([]byte, size-off)
			if _, err := f.ReadAt(buf, off); err != nil {
				f.Close()
				continue
			}
			f.Close()
			for _, ln := range strings.Split(string(buf), "\n") {
				if strings.TrimSpace(ln) == "" {
					continue
				}
				var o struct {
					Type    string `json:"type"`
					Message struct {
						Role    string          `json:"role"`
						Content json.RawMessage `json:"content"`
					} `json:"message"`
				}
				if json.Unmarshal([]byte(ln), &o) != nil {
					continue
				}
				if o.Type != "user" && o.Message.Role != "user" {
					continue
				}
				var s string
				if json.Unmarshal(o.Message.Content, &s) != nil {
					continue // block-content (tool_results etc.) never ticks
				}
				epMu.Lock()
				inj := epIsInjected(s)
				epMu.Unlock()
				if epSystemShaped(s) || inj {
					continue
				}
				ticked = true
				break
			}
			if ticked {
				break
			}
		}
		if !ticked {
			continue
		}
		epMu.Lock()
		epochLoad()
		if time.Since(epLastTick) < epochDebounce { // one attention-burst, one tick
			epLastTick = time.Now()
			epMu.Unlock()
			continue
		}
		closedEpoch, w := epNum, epWrites
		mins := time.Since(epStart).Minutes()
		hot := epLastTick.IsZero() == false && (w >= metabolismMinW || mins >= metabolismMinT.Minutes())
		epNum++
		epWrites = 0
		epStart = time.Now()
		epLastTick = time.Now()
		epochSave()
		epMu.Unlock()
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"epoch.tick","params":{"epoch":%d,"closed":%d,"writes":%d,"minutes":%.1f}}}`, closedEpoch+1, closedEpoch, w, mins))
		if hot {
			// the epoch that just closed ran on agent metabolism alone
			c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"epoch.metabolism","params":{"epoch":%d,"writes":%d,"minutes":%.1f,"note":"agent ran as metabolism — no operator tick"}}}`, closedEpoch, w, mins))
		}
	}
}
