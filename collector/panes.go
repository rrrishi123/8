package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// paneWitnessLoop — witness tmux pane CREATION so spawns are RECORDED, not guessed
// (the anomaly: %13/%14 appeared and no one could say when/whence). Each scan, a
// pane id we haven't seen before gets a first_seen stamp + a pane.appeared frame;
// a pane that disappears gets pane.vanished. We cannot witness WHO spawned it —
// tmux has no spawn hook — but WHEN it appeared and its initial cmd/title is now
// a witnessed event on the wire, not a reconstruction after the fact. Honest gap:
// attribution (who/why) needs a spawn hook we don't have; appearance we now hold.
func (c *collector) paneWitnessLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	c.witnessPanes() // seed current panes without announcing them as "appeared"
	for range t.C {
		c.witnessPanes()
	}
}

func (c *collector) witnessPanes() {
	now := time.Now().UTC().Format(time.RFC3339)
	live := map[string]bool{}
	c.pmu.Lock()
	seeded := len(c.panesSeen) > 0 // first scan seeds silently; after that, announce
	for _, p := range tmuxPanes() {
		live[p.ID] = true
		if _, ok := c.panesSeen[p.ID]; !ok {
			c.panesSeen[p.ID] = now
			if seeded {
				c.publish(fmt.Sprintf(`{"session":"panes","origin":"COLLECTOR","frame":{"method":"pane.appeared","params":{"pane":%q,"cmd":%q,"title":%q,"first_seen":%q}}}`, p.ID, p.Cmd, p.Title, now))
			}
		}
	}
	for id := range c.panesSeen {
		if !live[id] {
			delete(c.panesSeen, id)
			if seeded {
				c.publish(fmt.Sprintf(`{"session":"panes","origin":"COLLECTOR","frame":{"method":"pane.vanished","params":{"pane":%q,"at":%q}}}`, id, now))
			}
		}
	}
	c.pmu.Unlock()
}

// handlePanes — GET /panes: the witnessed pane roster, each with its first_seen
// (when 8 first observed it). The panopticon witnessing its own host's panes,
// served through the wire so no agent has to shell `tmux list-panes`.
func (c *collector) handlePanes(w http.ResponseWriter, r *http.Request) {
	type paneView struct {
		ID        string `json:"id"`
		Loc       string `json:"loc"`
		Cmd       string `json:"cmd"`
		Title     string `json:"title"`
		FirstSeen string `json:"first_seen"`
	}
	c.pmu.Lock()
	seen := make(map[string]string, len(c.panesSeen))
	for k, v := range c.panesSeen {
		seen[k] = v
	}
	c.pmu.Unlock()
	out := []paneView{}
	for _, p := range tmuxPanes() {
		out = append(out, paneView{p.ID, p.Loc, p.Cmd, p.Title, seen[p.ID]})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"panes": out, "n": len(out)})
}
