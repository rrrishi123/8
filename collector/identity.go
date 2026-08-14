package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"sort"
	"strings"
	"syscall"
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
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return time.Unix(st.Birthtimespec.Sec, st.Birthtimespec.Nsec)
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

// ── IDENTITY (#436) — ~/.8/identity.json is the canonical mind map:
// name -> {uuid, jsonl, aliases}. The db had 12 by_who labels for ~6 minds;
// this file is where an alias is EVIDENCED (never guessed — _unresolved labels
// stay unresolved). The collector CONSUMES it: /panes carries canonical_name +
// jsonl_path, and the eight.db sync folds work.by_who into by_canon.
type mindIdentity struct {
	UUID    string   `json:"uuid"`
	Jsonl   string   `json:"jsonl"`
	Aliases []string `json:"aliases"`
}

func identityFile() string { return os.ExpandEnv("$HOME/.8/identity.json") }

// loadIdentity — name -> identity. Missing file degrades to empty (probe,
// don't assume); "_"-prefixed keys are documentation, not minds.
func loadIdentity() map[string]mindIdentity {
	out := map[string]mindIdentity{}
	b, err := os.ReadFile(identityFile())
	if err != nil {
		return out
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return out
	}
	for name, v := range raw {
		if strings.HasPrefix(name, "_") {
			continue
		}
		var m mindIdentity
		if json.Unmarshal(v, &m) == nil {
			out[name] = m
		}
	}
	return out
}

// canonicalMind folds any label — canonical name, uuid, or evidenced alias —
// to the one mind it names. Unknown labels return "" (the blank is honest).
func canonicalMind(label string, ids map[string]mindIdentity) string {
	if label == "" {
		return ""
	}
	for name, m := range ids {
		if label == name || (m.UUID != "" && label == m.UUID) {
			return name
		}
		for _, a := range m.Aliases {
			if label == a {
				return name
			}
		}
	}
	return ""
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
