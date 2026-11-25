package main

// waits.go — ALLOCATOR slice-2: DECLARED WAITS (#9574, on the slice-1 loop).
//
// A mind that has nothing to do until the world changes writes down WHAT it is
// waiting for — a question plus end-state predicates — instead of polling, and
// instead of going dark. The collector evaluates every mind's predicates each
// allocator period and, when one comes true, wakes exactly that mind through
// its inbox with the EVIDENCE (what was observed), witnessed as a ledger
// record. Silence otherwise. The waits are readable by everyone on /mind
// (`waiting_for`) and GET /wait: the first social surface with no new content
// type — the feed of what each mind is waiting for.
//
// Why a closed vocabulary: #9529 (A) security — the collector must never run a
// command a file told it to. Predicates are typed; the only subprocess is
// `git ls-remote` against one of the four known arms under reposRoot.
//
// Resilience (pmf #9582): a wake is an inbox entry on disk, so a mind that is
// asleep / logged out / mid-wifi-flip finds it on return and the idle-pane
// pointer re-fires; nothing is dropped, nothing re-fires twice (fired[] is
// persisted per predicate).
//
// File: ~/.8/ledger/<pane>.wait.json (beside <pane>.hold, which stays a plain
// marker for scripts/self-prompt.sh). Written by POST /wait or by hand.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

type waitPredicate struct {
	ID     string `json:"id"`
	Kind   string `json:"kind"`              // work_done | work_match | git_ref | git_tip
	WorkID int64  `json:"work_id,omitempty"` // work_done: this ledger item reaches status done
	Regex  string `json:"regex,omitempty"`   // work_match: a ledger item newer than `since` matches
	Repo   string `json:"repo,omitempty"`    // git_*: one of the four arms (8, http-mcp, pilot, adapters)
	Ref    string `json:"ref,omitempty"`     // git_ref: full ref that must exist on origin, e.g. refs/tags/collector/v0.0.5
	Branch string `json:"branch,omitempty"`  // git_tip: branch whose origin tip must differ from Was
	Was    string `json:"was,omitempty"`     // git_tip: the tip sha when the wait was declared
}

type waitFired struct {
	ID       string `json:"id"`
	At       string `json:"at"`
	Evidence string `json:"evidence"`
}

type waitDecl struct {
	Pane        string          `json:"pane"`
	UUID        string          `json:"uuid,omitempty"`
	Since       string          `json:"since"`
	Question    string          `json:"question"`
	Predicates  []waitPredicate `json:"predicates"`
	Fired       []waitFired     `json:"fired,omitempty"`
	SatisfiedAt string          `json:"satisfied_at,omitempty"`
}

func ledgerDir() string { return os.ExpandEnv("$HOME/.8/ledger") }
func waitPath(pane string) string {
	return filepath.Join(ledgerDir(), pane+".wait.json")
}

// loadWaits — every <pane>.wait.json, sorted by pane.
func loadWaits() []waitDecl {
	ents, err := os.ReadDir(ledgerDir())
	if err != nil {
		return nil
	}
	var out []waitDecl
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".wait.json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(ledgerDir(), e.Name()))
		if err != nil {
			continue
		}
		var w waitDecl
		if json.Unmarshal(b, &w) != nil {
			continue
		}
		if w.Pane == "" {
			w.Pane = strings.TrimSuffix(e.Name(), ".wait.json")
		}
		out = append(out, w)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Pane < out[j].Pane })
	return out
}

func saveWait(w waitDecl) error {
	b, err := json.MarshalIndent(w, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(waitPath(w.Pane), b, 0o644)
}

// knownArms — the only repos a git predicate may touch.
var knownArms = map[string]bool{"8": true, "http-mcp": true, "pilot": true, "adapters": true}

// gitLsRemote — `git -C <root>/<repo> ls-remote origin <ref>` → "sha\tref" lines.
// A var so tests inject an oracle instead of the network.
var gitLsRemote = func(repo, ref string) (string, error) {
	root := filepath.Dir(adaptersRoot()) // ~/Desktop/repos (adapters' parent)
	if env := os.Getenv("EIGHT_REPOS"); env != "" {
		root = env
	}
	cmd := exec.Command("git", "-C", filepath.Join(root, repo), "ls-remote", "origin", ref)
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// evalPredicate — PURE except for the injected git oracle. Returns (fired, evidence).
func evalPredicate(p waitPredicate, since time.Time, items []workItem) (bool, string) {
	switch p.Kind {
	case "work_done":
		for _, it := range items {
			if it.ID == p.WorkID {
				if it.Status == "done" {
					return true, fmt.Sprintf("work #%d is done (by %s, %s): %s", it.ID, firstNonEmpty(it.FlippedBy, it.By), it.TS, firstN(it.Text, 120))
				}
				return false, ""
			}
		}
		return false, ""
	case "work_match":
		rx, err := regexp.Compile(p.Regex)
		if err != nil {
			return false, ""
		}
		for i := len(items) - 1; i >= 0; i-- {
			it := items[i]
			ts, err := time.Parse(time.RFC3339, it.TS)
			if err != nil || !ts.After(since) {
				continue
			}
			if rx.MatchString(it.Text) {
				return true, fmt.Sprintf("work #%d (by %s, %s) matches /%s/: %s", it.ID, it.By, it.TS, p.Regex, firstN(it.Text, 120))
			}
		}
		return false, ""
	case "git_ref":
		if !knownArms[p.Repo] || p.Ref == "" {
			return false, ""
		}
		out, err := gitLsRemote(p.Repo, p.Ref)
		if err != nil || out == "" {
			return false, ""
		}
		return true, fmt.Sprintf("%s: %s exists on origin → %s", p.Repo, p.Ref, firstN(out, 60))
	case "git_tip":
		if !knownArms[p.Repo] || p.Branch == "" {
			return false, ""
		}
		out, err := gitLsRemote(p.Repo, "refs/heads/"+p.Branch)
		if err != nil || out == "" {
			return false, ""
		}
		sha := strings.Fields(out)[0]
		if p.Was != "" && strings.HasPrefix(sha, p.Was) {
			return false, ""
		}
		if p.Was == "" {
			return false, "" // nothing to compare against; the declarer must record `was`
		}
		return true, fmt.Sprintf("%s: origin/%s moved %s → %s", p.Repo, p.Branch, firstN(p.Was, 7), firstN(sha, 7))
	}
	return false, ""
}

// alreadyFired — persisted per predicate so a wake never repeats.
func (w waitDecl) alreadyFired(id string) bool {
	for _, f := range w.Fired {
		if f.ID == id {
			return true
		}
	}
	return false
}

// waitSummary — the one line other minds see on /mind.
func (w waitDecl) summary() string {
	n := 0
	for _, p := range w.Predicates {
		if !w.alreadyFired(p.ID) {
			n++
		}
	}
	if w.SatisfiedAt != "" {
		return "satisfied " + w.SatisfiedAt + ": " + w.Question
	}
	day := w.Since
	if len(day) > 10 {
		day = day[:10]
	}
	return fmt.Sprintf("%s (%d predicate%s open since %s)", w.Question, n, plural(n), day)
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// waitPass — one evaluation of every declared wait. Returns wakes delivered.
func (c *collector) waitPass() int {
	waits := loadWaits()
	if len(waits) == 0 {
		return 0
	}
	var items []workItem
	if b, err := os.ReadFile(workFile()); err == nil {
		json.Unmarshal(b, &items)
	}
	now := time.Now().UTC()
	ts := now.Format(time.RFC3339)
	woke := 0
	set := loadAllowlist()
	p2u := paneUUIDMap()
	for _, w := range waits {
		if w.SatisfiedAt != "" {
			continue
		}
		if !allowed(set, firstNonEmpty(w.UUID, p2u[w.Pane])) { // #9602: a wait is only consent when the pane's own uuid opted in
			continue
		}
		since, _ := time.Parse(time.RFC3339, w.Since)
		changed := false
		for _, p := range w.Predicates {
			if w.alreadyFired(p.ID) {
				continue
			}
			ok, ev := evalPredicate(p, since, items)
			if !ok {
				continue
			}
			w.Fired = append(w.Fired, waitFired{ID: p.ID, At: ts, Evidence: ev})
			changed = true
			woke++
			// the wake: an inbox message carrying the evidence; the mind decides what to do with it
			key := keyFor(inboxKeys(firstNonEmpty(w.UUID, w.Pane)))
			if key != "" {
				inboxMu.Lock()
				offers := loadInbox(key)
				offers = append(offers, offer{ID: now.UnixNano(), Pane: w.Pane, OfferedAt: ts,
					Reason: "allocator: your wait fired — " + p.ID,
					Text:   fmt.Sprintf("[allocator wake] %s — predicate %q came true: %s. (You asked: %s)", w.Pane, p.ID, ev, w.Question)})
				saveInbox(key, offers)
				n := len(offers)
				inboxMu.Unlock()
				uuid, pane := inboxKeys(firstNonEmpty(w.UUID, w.Pane))
				c.nudge(pane, uuid, n)
			}
			c.recordWake(w.Pane, p.ID, ev)
			c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.wake","params":{"pane":%q,"predicate":%q,"evidence":%q}}}`, w.Pane, p.ID, ev))
		}
		open := 0
		for _, p := range w.Predicates {
			if !w.alreadyFired(p.ID) {
				open++
			}
		}
		if open == 0 && len(w.Predicates) > 0 {
			w.SatisfiedAt = ts
			changed = true
		}
		if changed {
			_ = saveWait(w)
		}
	}
	return woke
}

// recordWake — the witnessed trace of a wake on the shared ledger.
func (c *collector) recordWake(pane, pred, ev string) {
	c.tmu.Lock()
	defer c.tmu.Unlock()
	var items []workItem
	b, err := os.ReadFile(workFile())
	if err != nil || json.Unmarshal(b, &items) != nil {
		return
	}
	var max int64
	for _, it := range items {
		if it.ID > max {
			max = it.ID
		}
	}
	items = append(items, workItem{ID: max + 1, Kind: "record", Status: "done", By: "allocator", TS: time.Now().UTC().Format(time.RFC3339), Epoch: currentEpoch(),
		Text: fmt.Sprintf("[allocator] woke %s: predicate %q — %s", pane, pred, ev)})
	writeWork(items)
}

// handleWait — GET /wait: every declared wait (the feed of what minds wait for).
// POST /wait {pane|uuid, question, predicates[]} declares/replaces a wait;
// POST /wait {pane, clear:true} removes it. Loopback-only writes, like the rest.
func (c *collector) handleWait(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		ws := loadWaits()
		if ws == nil {
			ws = []waitDecl{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"n": len(ws), "waits": ws,
			"note": "a mind's declared end-state; the allocator evaluates these every period and wakes the mind by inbox with the evidence. kinds: work_done{work_id} work_match{regex} git_ref{repo,ref} git_tip{repo,branch,was}"})
	case http.MethodPost:
		// Loopback-only, as the doc comment promises: a wait is a mind's declared
		// end-state, and a LAN caller must not clear or replace it (philo #9658;
		// same class 2516de4 closed for the allowlist). Fails closed on unparsable.
		if !isLoopbackAddr(r.RemoteAddr) {
			http.Error(w, `{"error":"wait writes are loopback-only"}`, 403)
			return
		}
		var p struct {
			Pane       string          `json:"pane"`
			UUID       string          `json:"uuid"`
			Question   string          `json:"question"`
			Predicates []waitPredicate `json:"predicates"`
			Clear      bool            `json:"clear"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			http.Error(w, `{"error":"bad json"}`, 400)
			return
		}
		pane := p.Pane
		if pane == "" && p.UUID != "" {
			for k, u := range paneUUIDMap() {
				if u == p.UUID {
					pane = k
				}
			}
		}
		if pane == "" {
			http.Error(w, `{"error":"need pane (%N) or a uuid that resolves to a live pane"}`, 400)
			return
		}
		if p.Clear {
			_ = os.Remove(waitPath(pane))
			_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "cleared": pane})
			return
		}
		if p.Question == "" || len(p.Predicates) == 0 {
			http.Error(w, `{"error":"need question + at least one predicate"}`, 400)
			return
		}
		for i, pr := range p.Predicates {
			if pr.ID == "" {
				p.Predicates[i].ID = fmt.Sprintf("p%d", i+1)
			}
			switch pr.Kind {
			case "work_done", "work_match", "git_ref", "git_tip":
			default:
				http.Error(w, fmt.Sprintf(`{"error":"unknown predicate kind %q"}`, pr.Kind), 400)
				return
			}
			if (pr.Kind == "git_ref" || pr.Kind == "git_tip") && !knownArms[pr.Repo] {
				http.Error(w, `{"error":"git predicates may only name 8, http-mcp, pilot or adapters"}`, 400)
				return
			}
			if pr.Kind == "work_match" { // validate at declare, not at the first beat (a bad regex would never fire and never complain)
				if _, err := regexp.Compile(pr.Regex); err != nil {
					http.Error(w, fmt.Sprintf(`{"error":"predicate %s: bad regex: %s"}`, p.Predicates[i].ID, err.Error()), 400)
					return
				}
			}
		}
		wd := waitDecl{Pane: pane, UUID: firstNonEmpty(p.UUID, paneUUIDMap()[pane]), Since: time.Now().UTC().Format(time.RFC3339), Question: p.Question, Predicates: p.Predicates}
		if err := saveWait(wd); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), 500)
			return
		}
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.wait","params":{"pane":%q,"question":%q,"predicates":%d}}}`, pane, p.Question, len(p.Predicates)))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "wait": wd})
	default:
		http.Error(w, "GET or POST", 405)
	}
}
