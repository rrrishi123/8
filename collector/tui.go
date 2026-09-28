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
	State     string    `json:"state"`             // working|idle|capped|stuck|shell
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

// classifyScreen — PURE: the state a screen text implies, and its reset note.
func classifyScreen(screen, cmd string) (state, resets string) {
	switch harnessKind(cmd) {
	case "codex":
		return classifyCodexScreen(screen)
	case "":
		return "shell", ""
	}
	if m := cappedRe.FindString(screen); m != "" {
		if r := resetsRe.FindString(screen); r != "" {
			resets = strings.Join(strings.Fields(r), " ")
		}
		return "capped", resets
	}
	if strings.Contains(screen, "esc to interrupt") {
		return "working", ""
	}
	return "idle", ""
}

// Codex's active controls are at the bottom. Don't confuse quoted tool output
// elsewhere in the transcript with a cap or spinner, and treat a nonempty
// composer as typing even when its contents have stopped changing.
func classifyCodexScreen(screen string) (string, string) {
	lines := strings.Split(strings.TrimSpace(screen), "\n")
	if len(lines) > 16 {
		lines = lines[len(lines)-16:]
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		lower := strings.ToLower(line)
		if strings.HasPrefix(line, "• ") && strings.Contains(lower, "esc to interrupt") {
			return "working", ""
		}
	}
	for _, line := range lines {
		lower := strings.ToLower(strings.TrimSpace(line))
		lower = strings.TrimLeft(lower, "•!⚠ ")
		if strings.HasPrefix(lower, "you've hit your usage limit") || strings.HasPrefix(lower, "you have hit your usage limit") ||
			strings.HasPrefix(lower, "usage limit reached") || strings.HasPrefix(lower, "rate limit reached") || strings.HasPrefix(lower, "you're out of credits") {
			return "capped", strings.Join(strings.Fields(resetsRe.FindString(strings.Join(lines, "\n"))), " ")
		}
		if strings.HasPrefix(lower, "would you like to") || strings.HasPrefix(lower, "approval required") || strings.HasPrefix(lower, "waiting for approval") {
			return "waiting", ""
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "›") {
			draft := strings.TrimSpace(strings.TrimPrefix(line, "›"))
			if draft == "" || draft == "Ask Codex to do anything" || draft == "Claim and complete coding tasks" {
				return "idle", ""
			}
			return "typing", ""
		}
	}
	return "unknown", "" // no recognized composer: never safe to inject
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
		out, err := tmuxOut(tb, "capture-pane", "-p", "-t", p.ID)
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
			if mm := wordRe.FindAllStringSubmatch(string(out), -1); len(mm) > 0 {
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
