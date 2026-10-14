package main

import (
	"strconv"
	"strings"
)

// ── ANSI-AWARE COMPOSER READING ───────────────────────────────────────────────
// Both TUIs paint PLACEHOLDER text into their input line when it is empty:
// Claude Code shows a dimmed ghost suggestion ("❯ send it into the pane myself",
// "Try …", "Press up to edit queued messages"), Codex rotates a dimmed hint
// ("› Explain this codebase", "› Write tests for @filename", …). With plain
// `capture-pane -p` that ghost is indistinguishable from a real draft, and any
// placeholder not on a hard-coded list read as "typing" — an idle pane refused
// dispatch and /panes/send. Captures are therefore taken with -e, and the
// composer's draft is the text that is NOT dim/grey (and not the reverse-video
// cursor cell). Plain captures (no escapes at all) fall back to the known list.

// styledLine splits one captured line into its plain text and its "solid" text:
// the characters painted in a normal (non-faint, non-grey, non-reverse) style.
func styledLine(line string) (plain, solid string) {
	var p, s strings.Builder
	dim, grey, rev := false, false, false
	for i := 0; i < len(line); {
		c := line[i]
		if c == 0x1b && i+1 < len(line) && line[i+1] == '[' {
			j := i + 2
			for j < len(line) && (line[j] < 0x40 || line[j] > 0x7e) {
				j++
			}
			if j >= len(line) {
				break
			}
			if line[j] == 'm' {
				dim, grey, rev = applySGR(line[i+2:j], dim, grey, rev)
			}
			i = j + 1
			continue
		}
		if c == 0x1b { // other escape (OSC etc.): skip to ST/BEL or next char
			if i+1 < len(line) && line[i+1] == ']' {
				j := i + 2
				for j < len(line) && line[j] != 0x07 && !(line[j] == 0x1b && j+1 < len(line) && line[j+1] == '\\') {
					j++
				}
				if j < len(line) && line[j] == 0x1b {
					j++
				}
				i = j + 1
				continue
			}
			i += 2
			continue
		}
		// copy one UTF-8 rune
		n := 1
		for n < 4 && i+n < len(line) && line[i+n]&0xC0 == 0x80 {
			n++
		}
		r := line[i : i+n]
		p.WriteString(r)
		if dim || grey || rev {
			if r == " " {
				s.WriteString(" ")
			}
		} else {
			s.WriteString(r)
		}
		i += n
	}
	return p.String(), s.String()
}

func applySGR(params string, dim, grey, rev bool) (bool, bool, bool) {
	if params == "" {
		return false, false, false
	}
	ps := strings.Split(params, ";")
	for k := 0; k < len(ps); k++ {
		n, _ := strconv.Atoi(ps[k])
		switch {
		case n == 0:
			dim, grey, rev = false, false, false
		case n == 2:
			dim = true
		case n == 22:
			dim = false
		case n == 7:
			rev = true
		case n == 27:
			rev = false
		case n == 90:
			grey = true
		case n == 39 || (n >= 30 && n <= 37) || (n >= 91 && n <= 97):
			grey = false
		case n == 38 && k+1 < len(ps):
			mode, _ := strconv.Atoi(ps[k+1])
			if mode == 5 && k+2 < len(ps) {
				idx, _ := strconv.Atoi(ps[k+2])
				grey = idx == 8 || (idx >= 238 && idx <= 250)
				k += 2
			} else if mode == 2 && k+4 < len(ps) {
				r, _ := strconv.Atoi(ps[k+2])
				g, _ := strconv.Atoi(ps[k+3])
				b, _ := strconv.Atoi(ps[k+4])
				grey = isGrey(r, g, b)
				k += 4
			}
		case n == 48 && k+1 < len(ps): // skip background colour args
			if ps[k+1] == "5" {
				k += 2
			} else if ps[k+1] == "2" {
				k += 4
			}
		}
	}
	return dim, grey, rev
}

// isGrey — a mid-grey foreground (placeholder colour), not white/black text.
func isGrey(r, g, b int) bool {
	mx, mn := max(r, max(g, b)), min(r, min(g, b))
	return mx-mn <= 24 && mx >= 70 && mx <= 180
}

// stripANSI — the screen as plain text.
func stripANSI(screen string) string {
	if !strings.Contains(screen, "\x1b") {
		return screen
	}
	lines := strings.Split(screen, "\n")
	for i, l := range lines {
		lines[i], _ = styledLine(l)
	}
	return strings.Join(lines, "\n")
}

// composerDraft — given one raw captured line that starts (after spaces) with
// the composer marker, the operator's real draft: "" when the composer is empty
// or shows only a placeholder. ok=false when the line is not a composer line.
func composerDraft(raw, marker string, placeholders func(string) bool) (draft string, ok bool) {
	plain, solid := styledLine(raw)
	pt := strings.TrimSpace(plain)
	if !strings.HasPrefix(pt, marker) {
		return "", false
	}
	if !strings.Contains(raw, "\x1b") { // plain capture: only the known list can tell
		d := strings.TrimSpace(strings.TrimPrefix(pt, marker))
		if placeholders(d) {
			return "", true
		}
		return d, true
	}
	st := strings.TrimSpace(solid)
	st = strings.TrimSpace(strings.TrimPrefix(st, marker))
	return st, true
}
