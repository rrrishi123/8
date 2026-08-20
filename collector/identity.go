package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── TMUX — the agents' surface, native (the system's own eyes, no claude-deck) ──
type tmuxPaneRec struct{ ID, Loc, Cmd, Title string }

func tmuxBin() string {
	for _, p := range []string{"/opt/homebrew/bin/tmux", "/usr/local/bin/tmux", "/usr/bin/tmux"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("tmux"); err == nil {
		return p
	}
	return ""
}

// tmuxPanes enumerates every pane on the local tmux server. Empty (not error)
// when tmux is absent or no server runs — the seat simply doesn't appear.
func tmuxPanes() []tmuxPaneRec {
	tb := tmuxBin()
	if tb == "" {
		return nil
	}
	out, err := exec.Command(tb, "list-panes", "-a", "-F", "#{pane_id}|#{session_name}:#{window_index}.#{pane_index}|#{pane_current_command}|#{pane_title}").Output()
	if err != nil {
		return nil
	}
	var panes []tmuxPaneRec
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(ln, "|", 4)
		if len(f) == 4 && strings.HasPrefix(f[0], "%") {
			panes = append(panes, tmuxPaneRec{ID: f[0], Loc: f[1], Cmd: f[2], Title: f[3]})
		}
	}
	return panes
}

// handleTmuxPane returns a pane's visible text (capture-pane) — the tmux seat's
// "frame". GET /tmuxpane?pane=%25N (a %id). Afferent-only: reading never focuses.
func (c *collector) handleTmuxPane(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	ok := strings.HasPrefix(pane, "%") && len(pane) > 1
	for _, ch := range pane[1:] {
		if ch < '0' || ch > '9' {
			ok = false
			break
		}
	}
	tb := tmuxBin()
	if !ok || tb == "" {
		http.Error(w, `{"error":"bad pane id"}`, http.StatusBadRequest)
		return
	}
	// -S -3000 captures scrollback, not just the visible screen — the pane's
	// FULL recent history (the card was showing only the visible slice before).
	out, err := exec.Command(tb, "capture-pane", "-p", "-S", "-3000", "-t", pane).Output()
	if err != nil {
		http.Error(w, `{"error":"capture failed"}`, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(out)
}

// fileBirth returns a file's CREATION time (darwin birthtime), falling back to
// modtime. The jsonl of a Claude session is born when that session starts — so
// birth time is the stable per-session fingerprint that modtime is not (modtime
// churns on every append, so the newest-modtime jsonl is just whoever wrote last).
func fileBirth(path string) time.Time {
	fi, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	if bt := birthFromSys(fi); !bt.IsZero() {
		return bt
	}
	return fi.ModTime()
}

// binOr returns the first existing absolute path, else the bare name (last hope
// via PATH). Lets the collector exec system tools regardless of its launchd PATH.
func binOr(name string, abs ...string) string {
	for _, p := range abs {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	return name
}

// paneClaudeStart returns when the Claude process in a tmux pane started. The
// pane's root is a shell; claude is a descendant — so we walk two levels of
// children and take the earliest claude/node start. Empty when no Claude runs.
func paneClaudeStart(tb, pane string) (time.Time, bool) {
	ppb, err := exec.Command(tb, "display-message", "-p", "-t", pane, "#{pane_pid}").Output()
	if err != nil {
		return time.Time{}, false
	}
	pp := strings.TrimSpace(string(ppb))
	// ABSOLUTE paths: the collector is launched by launchd/watchdog with a minimal
	// PATH that lacks /usr/bin, so bare "pgrep"/"ps" fail to exec (tmux only works
	// because tmuxBin() is absolute). This was the same-cwd fix's silent failure.
	pgrepBin, psBin := binOr("pgrep", "/usr/bin/pgrep"), binOr("ps", "/bin/ps")
	pids := []string{pp}
	for _, parent := range []string{pp} {
		if ch, e := exec.Command(pgrepBin, "-P", parent).Output(); e == nil {
			for _, k := range strings.Fields(string(ch)) {
				pids = append(pids, k)
				if gc, e2 := exec.Command(pgrepBin, "-P", k).Output(); e2 == nil {
					pids = append(pids, strings.Fields(string(gc))...)
				}
			}
		}
	}
	var best time.Time
	found := false
	for _, pid := range pids {
		out, e := exec.Command(psBin, "-o", "lstart=,comm=", "-p", pid).Output()
		if e != nil {
			continue
		}
		line := strings.TrimSpace(string(out))
		idx := strings.LastIndex(line, " ")
		if idx < 0 {
			continue
		}
		comm := strings.ToLower(line[idx+1:])
		if !strings.Contains(comm, "claude") && !strings.Contains(comm, "node") {
			continue
		}
		lstart := strings.Join(strings.Fields(line[:idx]), " ")
		t, e2 := time.Parse("Mon 2 Jan 15:04:05 2006", lstart)
		if e2 != nil {
			continue
		}
		if !found || t.Before(best) {
			best, found = t, true
		}
	}
	return best, found
}

// normAlnum lowercases and drops EVERYTHING but [a-z0-9]. Stripping all
// whitespace + punctuation from both a wrapped tmux pane AND a jsonl makes the
// pane's line-wrapping (and box chrome) irrelevant: "foo\nbar" and "foo bar"
// both become "foobar", so a fingerprint taken from the screen matches the file.
func normAlnum(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		}
	}
	return b.String()
}

// paneJsonlByContent maps a pane to its transcript by MATCHING what the pane is
// currently showing against each jsonl's tail — resume-proof and pane-specific
// (birthtime breaks when a session is resumed; the process hides its sessionId).
// Returns "" if nothing matches (fresh pane, or output too short to fingerprint).
func paneJsonlByContent(tb, pane, projDir string) string {
	cap, err := exec.Command(tb, "capture-pane", "-p", "-S", "-200", "-t", pane).Output()
	if err != nil {
		return ""
	}
	pn := normAlnum(string(cap))
	if len(pn) < 80 {
		return ""
	}
	// several fingerprints from the RECENT (tail) portion of the screen
	var fps []string
	for _, off := range []int{len(pn) - 60, len(pn) - 180, len(pn) - 340, len(pn) - 520} {
		if off >= 0 && off+44 <= len(pn) {
			fps = append(fps, pn[off:off+44])
		}
	}
	if len(fps) == 0 {
		return ""
	}
	entries, _ := os.ReadDir(projDir)
	type fe struct {
		path string
		mod  time.Time
	}
	var fs []fe
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		if fi, er := e.Info(); er == nil {
			fs = append(fs, fe{projDir + "/" + e.Name(), fi.ModTime()})
		}
	}
	sort.Slice(fs, func(i, j int) bool { return fs[i].mod.After(fs[j].mod) })
	for _, f := range fs {
		data, _ := os.ReadFile(f.path)
		if len(data) > 1<<20 { // only the last 1MB — the pane shows RECENT output
			data = data[len(data)-(1<<20):]
		}
		dn := normAlnum(string(data))
		for _, fp := range fps {
			if strings.Contains(dn, fp) {
				return f.path
			}
		}
	}
	return ""
}

// paneTranscript — THE pane->transcript resolver (#438): the UNIQUE live key
// first — pane -> pane_pid -> the running `claude --resume <uuid>` (ps) ->
// jsonl discovered by search (#437's chain) — because cwd is SHARED GROUND:
// many minds sit in one directory, and cwd-derived resolution handed one mind
// another's transcript (/attention on %8 grabbed the IAM session). Fallback,
// stated: a FRESH claude carries no --resume in argv, so when the uuid chain
// yields nothing we fall back to the old cwd+content match (paneJsonl).
func paneTranscript(tb, pane string) (string, time.Time) {
	pidOf := ""
	if out, err := exec.Command(tb, "display-message", "-p", "-t", pane, "#{pane_pid}").Output(); err == nil {
		pidOf = strings.TrimSpace(string(out))
	}
	if pidOf != "" {
		if uuid := paneUUIDs()[pidOf]; uuid != "" {
			if p := jsonlForUUID(uuid); p != "" {
				mt := time.Time{}
				if fi, err := os.Stat(p); err == nil {
					mt = fi.ModTime()
				}
				return p, mt
			}
		}
	}
	cwdb, err := exec.Command(tb, "display-message", "-p", "-t", pane, "#{pane_current_path}").Output()
	if err != nil {
		return "", time.Time{}
	}
	projDir := os.ExpandEnv("$HOME/.claude/projects/") + strings.ReplaceAll(strings.TrimSpace(string(cwdb)), "/", "-")
	return paneJsonl(tb, pane, projDir)
}

// paneJsonl maps a tmux PANE to ITS OWN Claude transcript — the fix for the
// same-cwd collision (many claude panes share one cwd, so newest-by-modtime
// picks whichever mind wrote last, NOT this pane). Primary signal: CONTENT match
// (resume-proof); then jsonl BIRTH nearest claude START; last, newest-by-modtime.
func paneJsonl(tb, pane, projDir string) (string, time.Time) {
	if p := paneJsonlByContent(tb, pane, projDir); p != "" {
		mt := time.Time{}
		if fi, err := os.Stat(p); err == nil {
			mt = fi.ModTime()
		}
		return p, mt
	}
	entries, _ := os.ReadDir(projDir)
	var newest string
	var newestT time.Time
	type cand struct {
		path  string
		birth time.Time
		mod   time.Time
	}
	var cands []cand
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		p := projDir + "/" + e.Name()
		cands = append(cands, cand{p, fileBirth(p), fi.ModTime()})
		if fi.ModTime().After(newestT) {
			newestT, newest = fi.ModTime(), p
		}
	}
	if start, ok := paneClaudeStart(tb, pane); ok {
		best := ""
		var bestD time.Duration = 1 << 62
		for _, cd := range cands {
			d := cd.birth.Sub(start)
			if d < 0 {
				d = -d
			}
			if d < bestD {
				bestD, best = d, cd.path
			}
		}
		// only trust the match if it's within a few minutes of claude start —
		// otherwise the pane's claude predates all transcripts we can see.
		if best != "" && bestD < 10*time.Minute {
			for _, cd := range cands {
				if cd.path == best {
					return best, cd.mod
				}
			}
		}
	}
	return newest, newestT
}

// ── LIVE IDENTITY (#437, supersedes #436's static roster) ─────────────────
// Identity is DISCOVERED at query time, never stored: pane -> pane_pid -> the
// running `claude --resume <uuid>` child (ps) -> jsonl found by SEARCHING
// ~/.claude/projects/*/<uuid>.jsonl (derived, existence-verified, "" when the
// transcript is gone — severance stays visible). Names are SELF-DECLARED
// (POST /identity {uuid,name,aliases}) — self-sovereign, membership OPEN to any
// session on any host that can reach the wire. Declarations live in memory only
// and are witnessed as identity.declare frames: the roster dies with the
// process like memory does, and minds redeclare after severance — the feed
// keeps the trace. roles.json is DEPRECATED to a resolution fallback.
// The principle, restored: never inscribe what the system can discover.

type declaredMind struct {
	Name    string   `json:"name"`
	Aliases []string `json:"aliases,omitempty"`
	// SpawnedBy (#pane-hub): SELF-DECLARED parentage — a mind declares who woke
	// it (a name, a pane, "operator"), same sovereignty as the name itself;
	// blank = unknown/unclaimed, honest. Feeds panes.spawned_by in eight.db.
	SpawnedBy string `json:"spawned_by,omitempty"`
	At        string `json:"declared_at"`
}

var (
	declMu   sync.Mutex
	declared = map[string]declaredMind{} // uuid -> declaration (in-memory ONLY)
)

// jsonlForUUID discovers the transcript by searching, never by assuming a path.
func jsonlForUUID(uuid string) string {
	if uuid == "" {
		return ""
	}
	hits, _ := filepath.Glob(os.ExpandEnv("$HOME/.claude/projects/*/") + uuid + ".jsonl")
	if len(hits) > 0 {
		return hits[0]
	}
	return ""
}

// nameForUUID — the mind's OWN declared name for a uuid; blank when undeclared.
func nameForUUID(uuid string) (string, []string) {
	declMu.Lock()
	defer declMu.Unlock()
	if d, ok := declared[uuid]; ok {
		return d.Name, d.Aliases
	}
	return "", nil
}

// declaredUUID — a declared name or self-declared alias back to its uuid.
func declaredUUID(label string) string {
	declMu.Lock()
	defer declMu.Unlock()
	for u, d := range declared {
		if label == d.Name {
			return u
		}
		for _, a := range d.Aliases {
			if label == a {
				return u
			}
		}
	}
	return ""
}

// isPaneID reports whether s is a tmux pane id like "%7" (a % then digits).
// When work is authored by such a label, the label IS the origin pane — no
// self-declaration is needed to relate the row back to the pane that made it.
func isPaneID(s string) bool {
	if len(s) < 2 || s[0] != '%' {
		return false
	}
	for _, ch := range s[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}

// foldLabel — any label (declared name, self-declared alias, uuid) to the one
// declared name; "" when no mind has claimed it (the blank is honest).
func foldLabel(label string) string {
	if label == "" {
		return ""
	}
	declMu.Lock()
	if d, ok := declared[label]; ok {
		declMu.Unlock()
		return d.Name
	}
	declMu.Unlock()
	if u := declaredUUID(label); u != "" {
		declMu.Lock()
		defer declMu.Unlock()
		return declared[u].Name
	}
	return ""
}

// handleIdentity — the live identity primitive on the wire.
// GET /identity: the roster derived NOW — every live pane with its discovered
// uuid + jsonl and any self-declared name; plus declarations whose seat is
// currently down (a declared mind is not unpersoned by a dead pane).
// POST /identity {"uuid","name","aliases"}: a self-sovereign declaration,
// witnessed on the feed. No roster file is ever written.
func (c *collector) handleIdentity(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost {
		var p struct {
			UUID      string   `json:"uuid"`
			Name      string   `json:"name"`
			Aliases   []string `json:"aliases"`
			SpawnedBy string   `json:"spawned_by"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil || p.Name == "" || len(p.UUID) < 32 || strings.Count(p.UUID, "-") < 4 {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "need {uuid (a session uuid), name, aliases?}"})
			return
		}
		now := time.Now().UTC().Format(time.RFC3339)
		declMu.Lock()
		declared[p.UUID] = declaredMind{Name: p.Name, Aliases: p.Aliases, SpawnedBy: p.SpawnedBy, At: now}
		declMu.Unlock()
		c.publish(fmt.Sprintf(`{"session":"identity","origin":"COLLECTOR","frame":{"method":"identity.declare","params":{"uuid":%q,"name":%q,"aliases":%q,"pane":%q}}}`, p.UUID, p.Name, strings.Join(p.Aliases, ","), paneForUUID(p.UUID)))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "name": p.Name, "pane": paneForUUID(p.UUID), "jsonl": jsonlForUUID(p.UUID)})
		return
	}
	type row struct {
		Pane    string   `json:"pane,omitempty"`
		Cmd     string   `json:"cmd,omitempty"`
		Title   string   `json:"title,omitempty"`
		UUID    string   `json:"uuid,omitempty"`
		Jsonl   string   `json:"jsonl,omitempty"`
		Name    string   `json:"name,omitempty"`
		Aliases []string `json:"aliases,omitempty"`
		At      string   `json:"declared_at,omitempty"`
	}
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
	seenUUID := map[string]bool{}
	out := []row{}
	for _, p := range tmuxPanes() {
		uuid := uu[pidOf[p.ID]]
		name, aliases := nameForUUID(uuid)
		if uuid != "" {
			seenUUID[uuid] = true
		}
		out = append(out, row{Pane: p.ID, Cmd: p.Cmd, Title: p.Title, UUID: uuid, Jsonl: jsonlForUUID(uuid), Name: name, Aliases: aliases})
	}
	declMu.Lock()
	for u, d := range declared {
		if !seenUUID[u] { // declared, seat currently down — still a mind
			out = append(out, row{UUID: u, Jsonl: jsonlForUUID(u), Name: d.Name, Aliases: d.Aliases, At: d.At})
		}
	}
	declMu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"identity": out, "n": len(out), "note": "derived live; names are self-declared (POST /identity), never stored"})
}

// paneUUIDs — pane_pid -> claude session uuid, one ps scan: any child whose
// args carry `--resume <uuid>` binds that uuid to its parent pid (the pane).
func paneUUIDs() map[string]string {
	out := map[string]string{}
	ps, err := exec.Command("ps", "-eo", "ppid,args").Output()
	if err != nil {
		return out
	}
	for _, line := range strings.Split(string(ps), "\n") {
		i := strings.Index(line, "--resume")
		if i < 0 {
			continue
		}
		rest := strings.Fields(strings.TrimPrefix(strings.TrimPrefix(line[i:], "--resume="), "--resume"))
		f := strings.Fields(line)
		if len(rest) > 0 && len(f) > 0 && len(rest[0]) >= 32 {
			out[f[0]] = rest[0]
		}
	}
	return out
}
