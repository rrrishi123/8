package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

// ── INBOX — the mind decides; the collector never types into a working pane ──
// Until 2026-09-15 a summon was `tmux send-keys` of the task text into the
// pane's TUI: it interrupted whatever the mind was doing, raced its death
// (TOCTOU), ran as a shell command in a zsh pane, and "delivered" meant "typed"
// (#131 typed-but-never-sent). The operator's law: while a pane is working, it
// is THAT mind which decides what is best. So:
//   - a summon is an OFFER into the mind's inbox (~/.8/inbox/<uuid>.json — keyed
//     by session uuid, so it survives a tmux re-mint); the item STAYS todo;
//   - the mind PULLS at its own boundary: GET /inbox?uuid=ME (reading is the
//     delivery receipt), then POST /inbox {id, action: take|decline};
//   - take = doing + acked by the mind; decline = back to the pool, recorded;
//   - WIP=1 on offers: one outstanding offer per mind, never a flood;
//   - the only thing ever typed is a one-line pointer to the inbox, and only
//     when the pane is IDLE (no "esc to interrupt" on screen), 30min cooldown.
// EIGHT_TYPE_SUMMON=1 restores the legacy typed summon (rollback only).

type offer struct {
	ID        int64  `json:"id"`
	Text      string `json:"text"`
	Reason    string `json:"reason"`
	Pane      string `json:"pane,omitempty"`
	OfferedAt string `json:"offered_at"`
}

var (
	inboxMu   sync.Mutex
	nudgeMu   sync.Mutex
	lastNudge = map[string]time.Time{}
)

func inboxDir() string { return os.ExpandEnv("$HOME/.8/inbox") }
func inboxPath(key string) string {
	return inboxDir() + "/" + strings.ReplaceAll(key, "%", "pane-") + ".json"
}

func loadInbox(key string) []offer {
	var out []offer
	if b, err := os.ReadFile(inboxPath(key)); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func saveInbox(key string, offers []offer) {
	os.MkdirAll(inboxDir(), 0o755)
	if len(offers) == 0 {
		os.Remove(inboxPath(key))
		return
	}
	b, err := json.MarshalIndent(offers, "", " ")
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(inboxDir(), ".inbox-*.json")
	if err != nil {
		return
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return
	}
	tmp.Close()
	_ = os.Rename(tmp.Name(), inboxPath(key))
}

// paneUUIDMap — current pane id -> claude session uuid (one tmux call, one ps scan).
func paneUUIDMap() map[string]string {
	out := map[string]string{}
	tb := tmuxBin()
	if tb == "" {
		return out
	}
	o, err := tmuxOut(tb, "list-panes", "-a", "-F", "#{pane_id}|#{pane_pid}")
	if err != nil {
		return out
	}
	uu := paneUUIDs()
	for _, ln := range strings.Split(strings.TrimSpace(string(o)), "\n") {
		if id, pid, ok := strings.Cut(ln, "|"); ok {
			if u := uu[pid]; u != "" {
				out[id] = u
			}
		}
	}
	return out
}

// inboxKeys — where an assignee's offers live: (uuid, pane). uuid preferred
// (durable across re-mints); pane as the fallback key when no uuid is known.
func inboxKeys(assignee string) (uuid, pane string) {
	pane = resolveAssignee(assignee)
	if pane != "" && isPaneID(pane) {
		uuid = paneUUIDMap()[pane]
	}
	if uuid == "" {
		if u := declaredUUID(assignee); u != "" {
			uuid = u
		} else if u := roleUUID(assignee); u != "" {
			uuid = u
		} else if len(assignee) >= 32 && strings.Count(assignee, "-") >= 4 {
			uuid = assignee
		}
	}
	return
}

func keyFor(uuid, pane string) string {
	if uuid != "" {
		return uuid
	}
	return pane
}

// paneIdle — the pane exists, runs claude, and shows no "esc to interrupt"
// (Claude Code's working indicator; the self-prompt.sh heuristic). A working
// mind is never touched.
func paneIdle(pane string) bool {
	if !paneAlive(pane) {
		return false
	}
	tb := tmuxBin()
	if tb == "" {
		return false
	}
	out, err := tmuxOut(tb, "capture-pane", "-p", "-t", pane)
	if err != nil {
		return false
	}
	state, _ := classifyScreen(string(out), "claude") // #897: capped/stuck are not idle either
	return state == "idle"
}

// offer — enqueue the item into the mind's inbox; the item stays todo. Returns
// false only when the assignee cannot be resolved to any mind or pane.
func (c *collector) offer(item workItem, reason string) bool {
	uuid, pane := inboxKeys(item.Assignee)
	key := keyFor(uuid, pane)
	if key == "" {
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.summon-failed","params":{"id":%d,"assignee":%q,"reason":"no-mind"}}}`, item.ID, item.Assignee))
		return false
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if item.Status == "doing" { // already held by the mind (claimed/operator-flipped): a notice, not an offer
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.notice","params":{"id":%d,"uuid":%q,"pane":%q,"reason":%q}}}`, item.ID, uuid, pane, reason))
		return true
	}
	inboxMu.Lock()
	offers := loadInbox(key)
	dup := false
	for _, o := range offers {
		if o.ID == item.ID {
			dup = true
		}
	}
	if !dup {
		offers = append(offers, offer{ID: item.ID, Text: item.Text, Reason: reason, Pane: pane, OfferedAt: now})
		saveInbox(key, offers)
	}
	n := len(offers)
	inboxMu.Unlock()
	if !dup {
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.offer","params":{"id":%d,"uuid":%q,"pane":%q,"reason":%q,"pending":%d}}}`, item.ID, uuid, pane, reason, n))
	}
	c.nudge(pane, uuid, n)
	return true
}

// nudge — the ONE line the collector may still type: a pointer to the inbox,
// never the work, only into an IDLE pane, once per 30min per pane.
func (c *collector) nudge(pane, uuid string, n int) {
	if pane == "" || !paneIdle(pane) {
		return
	}
	nudgeMu.Lock()
	if time.Since(lastNudge[pane]) < 30*time.Minute {
		nudgeMu.Unlock()
		return
	}
	lastNudge[pane] = time.Now()
	nudgeMu.Unlock()
	who := uuid
	q := "uuid=" + uuid
	if uuid == "" {
		who = pane
		q = "pane=" + strings.ReplaceAll(pane, "%", "%25")
	}
	msg := fmt.Sprintf("[8-inbox] %d offer(s) wait for you (%s) — your call, take or decline: curl -s '127.0.0.1:7070/inbox?%s' ; then curl -s -X POST 127.0.0.1:7070/inbox -d '{\"id\":N,\"action\":\"take\",\"uuid\":\"%s\",\"by\":\"YOURNAME\"}' (or \"action\":\"decline\")", n, who, q, uuid)
	if sendToPane(pane, msg, nil) {
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.nudge","params":{"pane":%q,"uuid":%q,"pending":%d}}}`, pane, uuid, n))
	}
}

// inboxPendingForPane — outstanding offers for the mind seated in this pane
// (uuid key first, pane key as fallback): the WIP=1 gate for dispatch.
func inboxPendingForPane(pane string, p2u map[string]string) int {
	inboxMu.Lock()
	defer inboxMu.Unlock()
	n := len(loadInbox(pane))
	if u := p2u[pane]; u != "" {
		n += len(loadInbox(u))
	}
	return n
}

func dropOffer(key string, id int64) {
	if key == "" {
		return
	}
	inboxMu.Lock()
	defer inboxMu.Unlock()
	offers := loadInbox(key)
	kept := offers[:0]
	for _, o := range offers {
		if o.ID != id {
			kept = append(kept, o)
		}
	}
	saveInbox(key, kept)
}

// handleInbox — GET /inbox?uuid=U|pane=%N lists the mind's offers (reading IS
// the delivery receipt: delivered_at/_to are stamped on first read). POST
// /inbox {"id":N,"action":"take"|"decline","uuid":U,"by":"name"} is the mind's
// decision: take -> doing + acked by the mind; decline -> back to the pool.
func (c *collector) handleInbox(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		uuid, pane := r.URL.Query().Get("uuid"), r.URL.Query().Get("pane")
		if uuid == "" && pane != "" {
			uuid = paneUUIDMap()[pane]
		}
		inboxMu.Lock()
		offers := loadInbox(uuid)
		if pane != "" {
			offers = append(offers, loadInbox(pane)...)
		}
		inboxMu.Unlock()
		if len(offers) > 0 {
			ids := map[int64]bool{}
			for _, o := range offers {
				ids[o.ID] = true
			}
			to := keyFor(uuid, pane)
			c.tmu.Lock()
			if b, err := os.ReadFile(workFile()); err == nil {
				var items []workItem
				if json.Unmarshal(b, &items) == nil {
					now := time.Now().UTC().Format(time.RFC3339)
					ch := false
					for i := range items {
						if ids[items[i].ID] && items[i].DeliveredAt == "" {
							items[i].DeliveredAt, items[i].DeliveredTo = now, to
							ch = true
						}
					}
					if ch {
						writeWork(items)
					}
				}
			}
			c.tmu.Unlock()
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"uuid": uuid, "pane": pane, "offers": offers, "n": len(offers),
			"note": "reading = delivered. Decide: POST /inbox {\"id\":N,\"action\":\"take\"|\"decline\",\"uuid\":\"...\",\"by\":\"name\"}"})
	case http.MethodPost:
		var p struct {
			ID       int64  `json:"id"`
			Action   string `json:"action"`
			UUID     string `json:"uuid"`
			Pane     string `json:"pane"`
			By       string `json:"by"`
			Assignee string `json:"assignee"` // offer only: whose inbox (role/%N/uuid); defaults to the item's own assignee
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil || p.ID == 0 || (p.Action != "take" && p.Action != "decline" && p.Action != "offer") {
			http.Error(w, `{"error":"need id + action take|decline|offer"}`, 400)
			return
		}
		if p.Action == "offer" { // a mind hands an item to another mind's inbox — no playlist, no typing
			c.tmu.Lock()
			var items []workItem
			if b, err := os.ReadFile(workFile()); err == nil {
				json.Unmarshal(b, &items)
			}
			var it *workItem
			for i := range items {
				if items[i].ID == p.ID {
					if p.Assignee != "" && items[i].Assignee != p.Assignee {
						items[i].Assignee = p.Assignee
						writeWork(items)
					}
					cp := items[i]
					it = &cp
				}
			}
			c.tmu.Unlock()
			if it == nil {
				http.Error(w, `{"error":"no such item"}`, 404)
				return
			}
			ok := c.offer(*it, "offered by "+firstNonEmpty(firstNonEmpty(p.By, p.UUID), firstNonEmpty(p.Pane, "a sibling")))
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": ok, "action": "offer", "item": it})
			return
		}
		who := p.By
		if who == "" {
			who = keyFor(p.UUID, p.Pane)
		}
		now := time.Now().UTC().Format(time.RFC3339)
		c.tmu.Lock()
		var items []workItem
		if b, err := os.ReadFile(workFile()); err == nil {
			json.Unmarshal(b, &items)
		}
		var got *workItem
		for i := range items {
			if items[i].ID != p.ID {
				continue
			}
			it := &items[i]
			if p.Action == "take" {
				it.Status, it.FlippedBy, it.TS = "doing", who, now
				it.AckedAt, it.AckedBy = now, who
				if it.Assignee == "" { // taking a pool item makes it yours: the lane follows the decision
					if p.Pane != "" {
						it.Assignee = p.Pane
					} else {
						it.Assignee = p.UUID
					}
				}
				if it.DeliveredAt == "" {
					it.DeliveredAt, it.DeliveredTo = now, keyFor(p.UUID, p.Pane)
				}
			} else {
				it.Assignee, it.DeclinedBy, it.TS = "", who, now
			}
			cp := *it
			got = &cp
		}
		if got != nil {
			writeWork(items)
		}
		c.tmu.Unlock()
		if got == nil {
			http.Error(w, `{"error":"no such item"}`, 404)
			return
		}
		dropOffer(p.UUID, p.ID)
		dropOffer(p.Pane, p.ID)
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.%s","params":{"id":%d,"by":%q,"status":%q}}}`, p.Action, p.ID, who, got.Status))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "action": p.Action, "item": got})
	default:
		http.Error(w, "GET or POST", 405)
	}
}
