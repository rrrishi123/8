package main

import "strings"

// ── HARNESS KINDS as a table (#4421) ─────────────────────────────────────────
// Each harness (claude-code, codex, …) is a STATE whose BEHAVIORS — read the
// composer, detect working/capped/waiting, classify idle/typing — were once
// SCATTERED across a classifyScreen switch and a separate classifyCodexScreen. Here
// each kind declares its DATA ONCE { composer marker, placeholder set, working/capped/
// waiting detectors, fallback } and ONE shared engine (spec.classify) consumes them.
// A new kind (deepseek-web, grok) adds a row, never a new switch arm. Shared behavior
// is reused (composerState, styledLine, the detectors); per-kind specifics are data.

type harnessSpec struct {
	kind        string
	composer    string                      // composer marker: ❯ (claude), › (codex)
	placeholder func(string) bool           // is this composer text a placeholder, not a real draft?
	working     func(string) bool           // working indicator present in the live region
	capped      func(string) (bool, string) // a spend/usage-limit modal present (+ its reset note)
	waiting     func(string) bool           // an approval/confirmation prompt (nil → this kind has none)
	fallback    string                      // state when no composer is recognized (claude: idle, codex: unknown)
}

// composerState — shared: scan bottom-up for the LAST line that is a composer (starts,
// after spaces, with the kind's marker) and report its draft. The composer is the last
// prompt line; earlier marker lines are transcript. Reused by every kind and by
// hasUnsentInput, so the "what's in the composer" logic lives in exactly one place.
func composerState(screen, marker string, placeholder func(string) bool) (draft string, ok bool) {
	lines := strings.Split(screen, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if d, k := composerDraft(lines[i], marker, placeholder); k {
			return d, true
		}
	}
	return "", false
}

// classify — the ONE engine. Order: capped → working → waiting → composer. A capped or
// working pane is never offered work; a real draft is "typing"; an empty composer is
// "idle"; an unrecognized composer falls back (claude: idle, codex: unknown — never
// inject into a pane whose composer we can't read).
func (spec harnessSpec) classify(screen string) (state, resets string) {
	if spec.capped != nil {
		if yes, r := spec.capped(screen); yes {
			return "capped", r
		}
	}
	if spec.working != nil && spec.working(screen) {
		return "working", ""
	}
	if spec.waiting != nil && spec.waiting(screen) {
		return "waiting", ""
	}
	if d, ok := composerState(screen, spec.composer, spec.placeholder); ok {
		if d == "" {
			return "idle", ""
		}
		return "typing", ""
	}
	return spec.fallback, ""
}

// codexTail — codex draws its active controls in the bottom ~16 lines; styledLine drops
// the dim placeholder cells so a ghost suggestion is not mistaken for a working line.
func codexTail(screen string) []string {
	raw := strings.Split(strings.TrimRight(screen, "\n "), "\n")
	if len(raw) > 16 {
		raw = raw[len(raw)-16:]
	}
	lines := make([]string, len(raw))
	for i, l := range raw {
		lines[i], _ = styledLine(l)
	}
	return lines
}

func codexIsWorking(screen string) bool {
	lines := codexTail(screen)
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if strings.HasPrefix(line, "• ") && strings.Contains(strings.ToLower(line), "esc to interrupt") {
			return true
		}
	}
	return false
}

func codexCapped(screen string) (bool, string) {
	lines := codexTail(screen)
	for _, line := range lines {
		lower := strings.TrimLeft(strings.ToLower(strings.TrimSpace(line)), "•■!⚠ ")
		if strings.HasPrefix(lower, "you've hit your usage limit") || strings.HasPrefix(lower, "you have hit your usage limit") ||
			strings.HasPrefix(lower, "usage limit reached") || strings.HasPrefix(lower, "rate limit reached") || strings.HasPrefix(lower, "you're out of credits") {
			return true, strings.Join(strings.Fields(resetsRe.FindString(strings.Join(lines, "\n"))), " ")
		}
	}
	return false, ""
}

func codexWaiting(screen string) bool {
	lines := codexTail(screen)
	for _, line := range lines {
		lower := strings.TrimLeft(strings.ToLower(strings.TrimSpace(line)), "•■!⚠ ")
		if strings.HasPrefix(lower, "would you like to") || strings.HasPrefix(lower, "approval required") || strings.HasPrefix(lower, "waiting for approval") {
			return true
		}
	}
	return false
}

// claudeCapped — the claude-code spend/usage-limit modal, read from the plain capture.
func claudeCapped(screen string) (bool, string) {
	plain := stripANSI(screen)
	if cappedRe.FindString(plain) == "" {
		return false, ""
	}
	return true, strings.Join(strings.Fields(resetsRe.FindString(plain)), " ")
}

// harnessSpecs — the registry. Each kind's data+behavior declared exactly once.
var harnessSpecs = map[string]harnessSpec{
	"claude-code": {
		kind: "claude-code", composer: "❯", placeholder: claudePlaceholder, fallback: "idle",
		working: func(s string) bool { return claudeWorking(stripANSI(s)) },
		capped:  claudeCapped,
	},
	"codex": {
		kind: "codex", composer: "›", placeholder: codexPlaceholder, fallback: "unknown",
		working: codexIsWorking,
		capped:  codexCapped,
		waiting: codexWaiting,
	},
}
