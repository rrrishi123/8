package main

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strconv"
)

// allocator_stats.go — the OUTCOME view on the allocator (#9693, social-net
// hit-rate). The allocator (allocator.go) re-routes an untaken [verify #N] to an
// idle, opted-in, non-author mind and writes an immutable record per event:
//
//	[allocator] re-routed #<id> (verify of #<m>) <from> -> <to>: untaken <age>; ...
//
// The question this answers is NOT "did a reroute fire" (that is in the log
// already) but "did the reroute UN-STICK the work" — of the items the allocator
// moved, how many a mind then TOOK. A reroute that no one picks up is reshuffle,
// not progress; the hit-rate is how we tell the organ is earning its beat. Pure,
// read-only; computed off the SAME ledger, so it can never disagree with it.

// rerouteLine parses one allocator reroute record. Anchored to the exact literal
// in allocatorPass (allocator.go) — id, from pane, to pane. A format drift fails
// CLOSED (no match → not counted) rather than inventing a number.
var rerouteLine = regexp.MustCompile(`^\[allocator\] re-routed #(\d+) \(verify of #-?\d+\) (\S+) -> (\S+):`)

type receiverStat struct {
	Routed int `json:"routed"` // reroute events aimed at this receiver
	Taken  int `json:"taken"`  // of its distinct routed ids, how many got taken
}

type allocStats struct {
	RerouteEvents int                     `json:"reroute_events"`  // total records (an id may be re-routed more than once)
	Distinct      int                     `json:"distinct_items"`  // distinct item ids ever re-routed
	Taken         int                     `json:"taken"`           // distinct ids now un-stuck (doing/done past the reroute)
	TakenByTarget int                     `json:"taken_by_target"` // stricter subset: taken by the exact mind it was routed to
	Declined      int                     `json:"declined"`        // distinct ids a receiver explicitly declined
	StillStuck    int                     `json:"still_stuck"`     // distinct ids still todo + flipped_by allocator:reroute
	HitRate       float64                 `json:"hit_rate"`        // Taken / Distinct (0 when Distinct == 0)
	ByReceiver    map[string]receiverStat `json:"by_receiver"`
}

// allocatorStats — PURE. Reads the reroute records out of the ledger and joins
// each back to the live item to see its outcome. A reroute is a HIT when the
// item moved off the allocator's own flip (allocator:reroute) into doing/done —
// i.e. a mind picked it up. taken_by_target is the stricter read where the
// taker (AckedBy/FlippedBy) is literally the mind it was aimed at; it can
// under-count when a pane id and a self-declared name differ, which is why the
// headline hit-rate is "un-stuck by anyone", the outcome we actually want.
func allocatorStats(items []workItem) allocStats {
	byID := make(map[int64]*workItem, len(items))
	for i := range items {
		byID[items[i].ID] = &items[i]
	}

	// distinct rerouted id -> set of receiver panes it was aimed at
	targets := map[int64]map[string]bool{}
	st := allocStats{ByReceiver: map[string]receiverStat{}}

	for i := range items {
		it := &items[i]
		if it.Kind != "record" || it.By != "allocator" {
			continue
		}
		m := rerouteLine.FindStringSubmatch(it.Text)
		if m == nil {
			continue
		}
		id, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil {
			continue
		}
		to := m[3]
		st.RerouteEvents++
		if targets[id] == nil {
			targets[id] = map[string]bool{}
		}
		targets[id][to] = true
		rs := st.ByReceiver[to]
		rs.Routed++
		st.ByReceiver[to] = rs
	}

	for id, tos := range targets {
		st.Distinct++
		cur := byID[id]
		if cur == nil {
			st.StillStuck++ // record survives but the item was compacted away; treat as unresolved
			continue
		}
		unstuck := (cur.Status == "doing" || cur.Status == "done") && cur.FlippedBy != "allocator:reroute"
		switch {
		case unstuck:
			st.Taken++
			// which aimed-at receiver actually took it, if the taker's name/pane
			// matches a target string (FlippedBy is set first, AckedBy as fallback).
			taker := ""
			if tos[cur.FlippedBy] {
				taker = cur.FlippedBy
			} else if tos[cur.AckedBy] {
				taker = cur.AckedBy
			}
			if taker != "" {
				st.TakenByTarget++
				rs := st.ByReceiver[taker]
				rs.Taken++
				st.ByReceiver[taker] = rs
			}
		case cur.DeclinedBy != "" && tos[cur.DeclinedBy]:
			st.Declined++
		case cur.Status == "todo" && cur.FlippedBy == "allocator:reroute":
			st.StillStuck++
		}
	}

	if st.Distinct > 0 {
		st.HitRate = float64(st.Taken) / float64(st.Distinct)
	}
	return st
}

// handleAllocatorStats — GET /allocator/stats. Read-only; never mutates.
func (c *collector) handleAllocatorStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "GET", 405)
		return
	}
	var items []workItem
	if b, err := os.ReadFile(workFile()); err == nil {
		_ = json.Unmarshal(b, &items)
	}
	st := allocatorStats(items)
	recv := make([]string, 0, len(st.ByReceiver))
	for k := range st.ByReceiver {
		recv = append(recv, k)
	}
	sort.Strings(recv)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"reroute_events":  st.RerouteEvents,
		"distinct_items":  st.Distinct,
		"taken":           st.Taken,
		"taken_by_target": st.TakenByTarget,
		"declined":        st.Declined,
		"still_stuck":     st.StillStuck,
		"hit_rate":        st.HitRate,
		"by_receiver":     st.ByReceiver,
		"receivers":       recv,
		"note":            "OUTCOME of the allocator: of the untaken [verify #N] it re-routed, how many a mind then took. hit_rate = taken/distinct (un-stuck by anyone); taken_by_target is the stricter 'taken by the exact mind it was aimed at'. Read-only over the same ledger (work.json).",
	})
}
