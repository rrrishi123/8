package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
)

// ── DISTRIBUTED LEDGER BY PANE (2026-09-16) ───────────────────────────────────
// Identification goes back to PANE NUMBERS (the user's call): every open task is
// resolved to the CURRENT pane it targets (name/uuid/%N all fold to %N via
// resolveAssignee), and grouped per pane into what's DOING now vs the NEXT queued
// commands for that same pane. Unassigned tasks are the shared pool any idle pane
// may pull. Names ride along as a secondary label, never the key.
// GET /work/by-pane.
func (c *collector) handleWorkByPane(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var items []workItem
	if b, err := os.ReadFile(workFile()); err == nil {
		json.Unmarshal(b, &items)
	}
	type paneLedger struct {
		Pane  string  `json:"pane"`
		Name  string  `json:"name,omitempty"`
		Doing []int64 `json:"doing"`      // what this pane is working now
		Next  []int64 `json:"next"`       // the next commands queued FOR this pane
		Todo  int     `json:"todo_count"` // total queued for it
	}
	by := map[string]*paneLedger{}
	get := func(pane string) *paneLedger {
		if by[pane] == nil {
			nm, _ := nameForUUID(paneUUIDs()[panePid(pane)])
			by[pane] = &paneLedger{Pane: pane, Name: nm, Doing: []int64{}, Next: []int64{}}
		}
		return by[pane]
	}
	pool := &paneLedger{Pane: "pool", Doing: []int64{}, Next: []int64{}}
	for _, it := range items {
		if it.Status != "todo" && it.Status != "doing" {
			continue
		}
		pane := ""
		if it.Assignee != "" {
			pane = resolveAssignee(it.Assignee) // name/uuid/%N -> current %N
		}
		lg := pool
		if strings.HasPrefix(pane, "%") {
			lg = get(pane)
		}
		if it.Status == "doing" {
			lg.Doing = append(lg.Doing, it.ID)
		} else {
			lg.Todo++
			if len(lg.Next) < 5 {
				lg.Next = append(lg.Next, it.ID)
			}
		}
	}
	out := []*paneLedger{}
	for _, lg := range by {
		out = append(out, lg)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pane < out[j].Pane })
	pool.Todo = 0
	for _, it := range items {
		if it.Status == "todo" && (it.Assignee == "" || !strings.HasPrefix(resolveAssignee(it.Assignee), "%")) {
			pool.Todo++
		}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"by_pane": out, "pool": pool,
		"note": "identification is the pane number; name is a label. doing = working now; next = queued for that same pane; pool = unassigned, any idle pane may pull.",
	})
}

// panePid — current pane_pid for a %N (one tmux call).
func panePid(pane string) string {
	tb := tmuxBin()
	if tb == "" {
		return ""
	}
	if o, e := tmuxOut(tb, "display-message", "-p", "-t", pane, "#{pane_pid}"); e == nil {
		return strings.TrimSpace(string(o))
	}
	return ""
}
