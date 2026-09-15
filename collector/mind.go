package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── MIND IDENTITY CONTRACT (2026-09-16) ───────────────────────────────────────
// The problem: a mind wears four ids and none is durable on its own.
//   %N (pane)  re-mints on every tmux server reboot.
//   pid        dies with the process.
//   uuid       stable across resume/reboot, but CHANGES on /clear.
//   seat       session:win.pane — stable, and what restore-8.py rebuilds by.
// Identity used to be name->uuid (roles.json), so a /clear orphaned the name
// (the map kept pointing at the dead uuid). The contract here: the SEAT is the
// durable address and the NAME is the identity; uuid/%N/pid are CURRENT bindings.
// When the live uuid at a named seat differs from the stored one (a /clear or a
// fresh resume in that seat), we re-point the name — the name never orphans.
// We never invent a role for an unnamed mind; it is addressed by seat until a
// human names it.

type mindView struct {
	Name    string   `json:"name"`          // durable identity, or "" when unnamed
	Address string   `json:"address"`       // how to refer to it: the name, else "seat:<loc>"
	Seat    string   `json:"seat"`          // session:win.pane — the durable slot
	Pane    string   `json:"pane"`          // %N — current, volatile
	UUID    string   `json:"uuid"`          // current session id — changes on /clear
	Pid     int      `json:"pid,omitempty"` // current process — dies on exit
	Ctx     int64    `json:"context_tokens,omitempty"`
	State   string   `json:"state,omitempty"`
	Words   []string `json:"words_seen,omitempty"`     // gerunds witnessed while it worked — the naming palette
	Suggest string   `json:"suggested_name,omitempty"` // top unused word, when unnamed
	Issues  []string `json:"issues,omitempty"`
}

var healMu sync.Mutex

// dbSeatNames — the durable seat->name from eight.db (canonical_name per
// session/win/pane). eight.db is the cold store restore rebuilds from.
func dbSeatNames() map[string]string {
	m := map[string]string{}
	out, err := exec.Command("sqlite3", os.ExpandEnv("$HOME/.8/eight.db"),
		"select session||':'||win||'.'||pane, coalesce(nullif(canonical_name,''), nullif(role,'')) from panes where canonical_name<>'' or role<>'';").Output()
	if err != nil {
		return m
	}
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if seat, name, ok := strings.Cut(ln, "|"); ok && name != "" {
			m[seat] = name
		}
	}
	return m
}

// healRole re-points a name to a new uuid across all three stores (roles.json,
// identity.json, eight.db) — the transparent /clear fix. Idempotent.
func (c *collector) healRole(name, seat, newUUID string) {
	healMu.Lock()
	defer healMu.Unlock()
	// roles.json
	if b, err := os.ReadFile(rolesFile()); err == nil {
		var m map[string]string
		if json.Unmarshal(b, &m) == nil && m[name] != newUUID {
			m[name] = newUUID
			if nb, e := json.MarshalIndent(m, "", "  "); e == nil {
				os.WriteFile(rolesFile(), nb, 0o644)
			}
		}
	}
	// identity.json (uuid + jsonl)
	idf := os.ExpandEnv("$HOME/.8/identity.json")
	if b, err := os.ReadFile(idf); err == nil {
		var m map[string]json.RawMessage
		if json.Unmarshal(b, &m) == nil {
			if raw, ok := m[name]; ok {
				var e map[string]any
				if json.Unmarshal(raw, &e) == nil {
					e["uuid"] = newUUID
					e["jsonl"] = jsonlForUUID(newUUID)
					e["healed_at"] = time.Now().UTC().Format(time.RFC3339)
					if nr, err := json.Marshal(e); err == nil {
						m[name] = nr
						if nb, err := json.MarshalIndent(m, "", "  "); err == nil {
							os.WriteFile(idf, nb, 0o644)
						}
					}
				}
			}
		}
	}
	// eight.db: the seat's claude_uuid + jsonl_path
	if seat != "" {
		parts := strings.FieldsFunc(seat, func(r rune) bool { return r == ':' || r == '.' })
		if len(parts) == 3 {
			exec.Command("sqlite3", os.ExpandEnv("$HOME/.8/eight.db"),
				fmt.Sprintf("update panes set claude_uuid=%s, jsonl_path=%s where session=%s and win=%s and pane=%s;",
					sqlStr(newUUID), sqlStr(jsonlForUUID(newUUID)), sqlStr(parts[0]), parts[1], parts[2])).Run()
		}
	}
	c.publish(fmt.Sprintf(`{"session":"panes","origin":"COLLECTOR","frame":{"method":"mind.heal","params":{"name":%q,"seat":%q,"uuid":%q}}}`, name, seat, newUUID))
}

// resolveMinds — one row per live claude pane, every id layer, the durable name
// (self-healing the uuid binding), and contract issues. heal=true rewrites the
// stores when a named seat's live uuid has moved.
func (c *collector) resolveMinds(heal bool) []mindView {
	pidOf := map[string]string{}
	if tb := tmuxBin(); tb != "" {
		if out, err := exec.Command(tb, "list-panes", "-a", "-F", "#{pane_id}|#{pane_pid}").Output(); err == nil {
			for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
				if id, pid, ok := strings.Cut(ln, "|"); ok {
					pidOf[id] = pid
				}
			}
		}
	}
	uu := paneUUIDs()
	seatName := dbSeatNames()
	uuidCount := map[string]int{}
	var mins []mindView
	for _, p := range tmuxPanes() {
		if !strings.Contains(p.Cmd, "claude") {
			continue
		}
		pf := procOf(p.ID)
		uuid := uu[pidOf[p.ID]] // authoritative via sessions/<pid>.json inside paneUUIDs()
		var issues []string
		// name: roles.json reverse (current uuid) first, else durable seat->name
		name, _ := nameForUUID(uuid)
		if name == "" {
			if sn := seatName[p.Loc]; sn != "" {
				name = sn // durable: this seat is a known mind whose uuid moved
				issues = append(issues, "clear-detected: seat "+p.Loc+" is "+sn+" but its uuid moved to "+first8(uuid))
				if heal && uuid != "" && !userOwned(uuid) {
					c.healRole(name, p.Loc, uuid)
					issues[len(issues)-1] = "clear-healed: " + name + " re-pointed to " + first8(uuid)
				}
			}
		} else if sn := seatName[p.Loc]; sn != "" && sn != name {
			issues = append(issues, "seat-moved: "+name+" is in "+sn+"'s seat "+p.Loc)
		}
		addr := name
		if addr == "" {
			addr = "seat:" + p.Loc
			issues = append(issues, "unnamed: addressable only by seat/uuid — a human must name it")
		}
		if uuid != "" {
			uuidCount[uuid]++
		}
		words := wordsOf(p.ID)
		suggest := ""
		if name == "" {
			for _, wd := range words { // first witnessed word not already a live name
				if roleUUID(strings.ToLower(wd)) == "" {
					suggest = strings.ToLower(wd)
					break
				}
			}
		}
		mins = append(mins, mindView{
			Name: name, Address: addr, Seat: p.Loc, Pane: p.ID, UUID: uuid,
			Pid: pf.Pid, Ctx: pf.CtxTokens, State: tuiOf(p.ID).State,
			Words: words, Suggest: suggest, Issues: issues,
		})
	}
	// second pass: forks (two live panes on one uuid)
	for i := range mins {
		if mins[i].UUID != "" && uuidCount[mins[i].UUID] > 1 {
			mins[i].Issues = append(mins[i].Issues, "uuid-shared: "+strconv.Itoa(uuidCount[mins[i].UUID])+" live panes on uuid "+first8(mins[i].UUID)+" (fork)")
		}
	}
	// third: names whose seat has no live pane (dead binding)
	live := map[string]bool{}
	for _, m := range mins {
		if m.Name != "" {
			live[m.Name] = true
		}
	}
	if b, err := os.ReadFile(rolesFile()); err == nil {
		var rm map[string]string
		if json.Unmarshal(b, &rm) == nil {
			for nm := range rm {
				if !live[nm] {
					mins = append(mins, mindView{Name: nm, Address: nm, UUID: rm[nm],
						Issues: []string{"name-dead: no live pane resolves to " + nm + " (uuid " + first8(rm[nm]) + ")"}})
				}
			}
		}
	}
	sort.Slice(mins, func(i, j int) bool { return mins[i].Seat < mins[j].Seat })
	return mins
}

// setName binds a CHOSEN name to the mind currently at a seat/uuid, durably
// (roles.json + identity.json + eight.db), removing any prior name for that
// uuid. A rename is just a set. Returns the old name (if any).
func (c *collector) setName(newName, seat, uuid, source string) (string, error) {
	newName = strings.TrimSpace(newName)
	if newName == "" || uuid == "" {
		return "", fmt.Errorf("need a non-empty name and a live uuid")
	}
	healMu.Lock()
	defer healMu.Unlock()
	old := ""
	m := map[string]string{}
	if b, err := os.ReadFile(rolesFile()); err == nil {
		json.Unmarshal(b, &m)
	}
	for nm, u := range m {
		if u == uuid && nm != newName {
			old = nm
			delete(m, nm) // a mind has ONE name; renaming drops the previous
		}
	}
	m[newName] = uuid
	if nb, err := json.MarshalIndent(m, "", "  "); err == nil {
		os.WriteFile(rolesFile(), nb, 0o644)
	}
	// identity.json entry
	idf := os.ExpandEnv("$HOME/.8/identity.json")
	im := map[string]json.RawMessage{}
	if b, err := os.ReadFile(idf); err == nil {
		json.Unmarshal(b, &im)
	}
	if old != "" {
		delete(im, old)
	}
	entry, _ := json.Marshal(map[string]any{"uuid": uuid, "jsonl": jsonlForUUID(uuid), "seat": seat, "named_at": time.Now().UTC().Format(time.RFC3339)})
	im[newName] = entry
	if nb, err := json.MarshalIndent(im, "", "  "); err == nil {
		os.WriteFile(idf, nb, 0o644)
	}
	// eight.db canonical_name at the seat
	if parts := strings.FieldsFunc(seat, func(r rune) bool { return r == ':' || r == '.' }); len(parts) == 3 {
		exec.Command("sqlite3", os.ExpandEnv("$HOME/.8/eight.db"),
			fmt.Sprintf("update panes set canonical_name=%s, role=%s where session=%s and win=%s and pane=%s;", sqlStr(newName), sqlStr(newName), sqlStr(parts[0]), parts[1], parts[2])).Run()
	}
	c.publish(fmt.Sprintf(`{"session":"panes","origin":"COLLECTOR","frame":{"method":"mind.name","params":{"name":%q,"was":%q,"seat":%q,"uuid":%q}}}`, newName, old, seat, uuid))
	// declared[] live cache so /panes reflects it at once
	declMu.Lock()
	declared[uuid] = declaredMind{Name: newName, At: time.Now().UTC().Format(time.RFC3339)}
	declMu.Unlock()
	if source == "" {
		source = "user"
	}
	setNameSrc(uuid, source)
	return old, nil
}

// sqlStr — a single-quoted SQLite string literal (doubles embedded quotes).
func sqlStr(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

func first8(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	if s == "" {
		return "—"
	}
	return s
}

// handleMind — GET /mind : the identity roster + a contract report.
// GET /mind?name=conductor or ?pane=%9 : one mind. ?heal=1 : re-point moved names.
func (c *collector) handleMind(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// rename: POST {pane|name, set} or GET ?pane=%N&set=word
	set := r.URL.Query().Get("set")
	selPane, selName := r.URL.Query().Get("pane"), r.URL.Query().Get("name")
	if r.Method == "POST" {
		var body struct{ Pane, Name, Set string }
		json.NewDecoder(r.Body).Decode(&body)
		if body.Set != "" {
			set = body.Set
		}
		if body.Pane != "" {
			selPane = body.Pane
		}
		if body.Name != "" {
			selName = body.Name
		}
	}
	if set != "" {
		for _, m := range c.resolveMinds(false) {
			if (selPane != "" && m.Pane == selPane) || (selName != "" && m.Name == selName) {
				if m.UUID == "" {
					http.Error(w, `{"error":"that mind has no live uuid to bind a name to"}`, 409)
					return
				}
				old, err := c.setName(set, m.Seat, m.UUID, "user")
				if err != nil {
					http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), 400)
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"named": set, "was": old, "seat": m.Seat, "pane": m.Pane, "uuid": m.UUID})
				return
			}
		}
		http.Error(w, `{"error":"no live mind matched pane/name to rename"}`, 404)
		return
	}
	heal := r.URL.Query().Get("heal") == "1"
	mins := c.resolveMinds(heal)
	if selName != "" {
		for _, m := range mins {
			if m.Name == selName {
				_ = json.NewEncoder(w).Encode(m)
				return
			}
		}
		http.Error(w, `{"error":"no mind by that name"}`, 404)
		return
	}
	if selPane != "" {
		for _, m := range mins {
			if m.Pane == selPane {
				_ = json.NewEncoder(w).Encode(m)
				return
			}
		}
		http.Error(w, `{"error":"no live mind in that pane"}`, 404)
		return
	}
	named, unnamed, issues := 0, 0, 0
	for _, m := range mins {
		if m.Name != "" {
			named++
		} else {
			unnamed++
		}
		issues += len(m.Issues)
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"minds": mins, "n": len(mins), "named": named, "unnamed": unnamed, "issues": issues,
		"contract": "seat is the durable address; name is the identity; uuid/%N/pid are current bindings. A /clear re-points the name (?heal=1).",
	})
}
