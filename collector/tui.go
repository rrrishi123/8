package main

import (
	"crypto/sha1"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ── TUI PROBE (#897) — the witness is no longer blind to pane STATE ────────────
// Each 5s tick, every live claude pane's screen is captured and hashed:
//   - state: working (Claude Code's "esc to interrupt" indicator), idle (prompt
//     at rest), capped (a spend/usage-limit modal — "Adjust monthly spend limit",
//     "usage limit", "Resets at HH:MM"…), stuck (working indicator but the same
//     screen for ≥ stuckAfter);
//   - screen_same_for_s: how long the pane has shown this exact screen (staleness);
//   - resets: the modal's own reset time when it states one.
// Exposed on GET /panes; dispatch never offers to a capped or stuck pane and the
// nudge never types into one. REPORT, don't poke: capacity returns at reset, not
// by clearing the modal.

const stuckAfter = 30 * time.Minute

type tuiState struct {
	State     string    `json:"state"`             // working|idle|typing|waiting|capped|stuck|shell|unknown
	Resets    string    `json:"resets,omitempty"`  // "Resets 2:30pm" etc., verbatim from the modal
	SameSince time.Time `json:"-"`                 // when this screen hash was first seen
	SameForS  int64     `json:"screen_same_for_s"` // derived at read time
	Hash      string    `json:"-"`
	ProbedAt  string    `json:"probed_at,omitempty"`
}

var (
	tuiMu     sync.Mutex
	tuiByPane = map[string]*tuiState{}
	cappedRe  = regexp.MustCompile(`(?i)adjust monthly spend limit|usage limit|rate limit|out of (credits?|tokens)|upgrade to (increase|continue)|limit reached`)
	resetsRe  = regexp.MustCompile(`(?i)resets?\s+(at\s+|in\s+)?[0-9][0-9:apm\s]*[0-9apm]`)
	// the working-word: Claude Code's whimsical gerund on the spinner line
	// ("✽ Enchanting…", "✻ Discombobulating…"). A mind's palette — you can only
	// read a sibling's words by watching its pane work (#mind naming).
	wordRe = regexp.MustCompile("([A-Z][a-z]{3,})\u2026")
)

var (
	wordsMu     sync.Mutex
	wordsByPane = map[string]map[string]int{} // pane -> word -> times seen
)

// wordsOf — the working-words witnessed in a pane, most-seen first (max 8).
func wordsOf(pane string) []string {
	wordsMu.Lock()
	defer wordsMu.Unlock()
	m := wordsByPane[pane]
	if len(m) == 0 {
		return nil
	}
	type kv struct {
		w string
		n int
	}
	var ks []kv
	for w, n := range m {
		ks = append(ks, kv{w, n})
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i].n > ks[j].n })
	out := make([]string, 0, len(ks))
	for i, k := range ks {
		if i >= 8 {
			break
		}
		out = append(out, k.w)
	}
	return out
}

// classifyScreen — PURE: the state a screen implies, and its reset note. The
// screen may be a plain or an ANSI (`capture-pane -e`) capture; with ANSI, dim
// placeholder text in the composer is told apart from a real draft.
func classifyScreen(screen, cmd string) (state, resets string) {
	k := harnessKind(cmd)
	if k == "" {
		return "shell", ""
	}
	if spec, ok := harnessSpecs[k]; ok {
		return spec.classify(screen) // #4421: ONE engine, per-kind data from the table
	}
	return "unknown", ""
}

// claudeSpinnerRe — Claude Code's live spinner line: "✳ Osmosing… (6s · esc to
// interrupt)". A finished turn reads "✻ Sautéed for 1m 54s" (no ellipsis), which
// must NOT count as working.
var claudeSpinnerRe = regexp.MustCompile(`^[✳✻✽✶✢✺·*+] +[A-Z][\p{L}'-]+…`)

// claudeWorking — the working indicator in the live region at the bottom of the
// screen (not a quoted "esc to interrupt" far up in the transcript).
func claudeWorking(plain string) bool {
	lines := strings.Split(strings.TrimRight(plain, "\n "), "\n")
	if len(lines) > 14 {
		lines = lines[len(lines)-14:]
	}
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if strings.Contains(t, "esc to interrupt") || claudeSpinnerRe.MatchString(t) {
			return true
		}
	}
	return false
}

// codexPlaceholder — Codex's known composer hints, for plain captures only.
func codexPlaceholder(d string) bool {
	if d == "" {
		return true
	}
	for _, p := range []string{"Ask Codex to do anything", "Claim and complete coding tasks",
		"Explain this codebase", "Summarize recent commits", "Implement {feature}",
		"Find and fix a bug in @filename", "Write tests for @filename",
		"Improve documentation in @filename", "Run /review on my current changes",
		"Use /skills to list available skills"} {
		if d == p {
			return true
		}
	}
	return false
}

// probeTUI — one tick over the given panes: capture, hash, classify, and age.
func probeTUI(panes []tmuxPaneRec, now time.Time) {
	tb := tmuxBin()
	if tb == "" {
		return
	}
	live := map[string]bool{}
	for _, p := range panes {
		live[p.ID] = true
		out, err := tmuxOut(tb, "capture-pane", "-e", "-p", "-t", p.ID)
		if err != nil {
			continue
		}
		sum := sha1.Sum(out)
		h := hex.EncodeToString(sum[:8])
		state, resets := classifyScreen(string(out), p.Cmd)
		tuiMu.Lock()
		st := tuiByPane[p.ID]
		if st == nil || st.Hash != h {
			st = &tuiState{Hash: h, SameSince: now}
			tuiByPane[p.ID] = st
		}
		if state == "working" && now.Sub(st.SameSince) >= stuckAfter {
			state = "stuck" // the spinner is on but nothing has changed for a long time
		}
		st.State, st.Resets, st.ProbedAt = state, resets, now.UTC().Format(time.RFC3339)
		tuiMu.Unlock()
		if state == "working" {
			if mm := wordRe.FindAllStringSubmatch(stripANSI(string(out)), -1); len(mm) > 0 {
				word := mm[len(mm)-1][1] // the last gerund on screen = the current one
				wordsMu.Lock()
				if wordsByPane[p.ID] == nil {
					wordsByPane[p.ID] = map[string]int{}
				}
				wordsByPane[p.ID][word]++
				wordsMu.Unlock()
			}
		}
	}
	tuiMu.Lock()
	for id := range tuiByPane {
		if !live[id] {
			delete(tuiByPane, id)
		}
	}
	tuiMu.Unlock()
	wordsMu.Lock()
	for id := range wordsByPane {
		if !live[id] {
			delete(wordsByPane, id)
		}
	}
	wordsMu.Unlock()
}

// tuiOf — a snapshot for one pane (state + staleness), zero value when unprobed.
func tuiOf(pane string) tuiState {
	tuiMu.Lock()
	defer tuiMu.Unlock()
	st := tuiByPane[pane]
	if st == nil {
		return tuiState{}
	}
	cp := *st
	cp.SameForS = int64(time.Since(st.SameSince).Seconds())
	return cp
}

// dispatchable — never offer to a capped or stuck pane (#897 c). Unprobed panes
// (state "") are allowed: absence of evidence is not a cap.
func dispatchable(pane string) bool {
	s := tuiOf(pane).State
	return s != "capped" && s != "stuck" && s != "typing" && s != "waiting" && s != "unknown"
}
