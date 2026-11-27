package main

import (
	"strings"
	"testing"
)

// The multi-line-send guardrail (Rishi, 2026-11-26): a prompt sent to a mind's
// pane must arrive WHOLE. The bug it locks out: delivery via `tmux send-keys -l`
// sends raw newlines, which a REPL (Claude Code, codex) reads as Enter — so a
// multi-paragraph prompt submits at the FIRST newline and only one chunk lands
// ("I only got half the prompt"). The fix is bracketed paste. This test fails if
// anyone reverts to raw send-keys of the message, so the user-flow is trusted
// without reading sendPane.
func TestSendPaneUsesBracketedPasteNotRawNewlines(t *testing.T) {
	// a realistic multi-paragraph prompt — the exact shape that broke
	msg := "line one\n\nsecond paragraph\n  - a bullet\nlast line"
	cmds := pasteCmds("%9", msg)

	if len(cmds) != 2 {
		t.Fatalf("want 2 tmux commands (set-buffer, paste-buffer), got %d: %v", len(cmds), cmds)
	}

	// 1) the message is loaded into a buffer verbatim (after `--`, so leading
	//    dashes/newlines are literal), never passed as send-keys arguments.
	set := cmds[0]
	if set[0] != "set-buffer" {
		t.Fatalf("first command must be set-buffer, got %q", set[0])
	}
	if set[len(set)-2] != "--" || set[len(set)-1] != msg {
		t.Errorf("set-buffer must end with `-- <msg>` so msg is literal; got %v", set)
	}

	// 2) the buffer is pasted WITH bracketed-paste markers (-p) to the pane.
	paste := cmds[1]
	if paste[0] != "paste-buffer" {
		t.Fatalf("second command must be paste-buffer, got %q", paste[0])
	}
	if !contains(paste, "-p") {
		t.Errorf("paste-buffer MUST use -p (bracketed paste) or newlines submit mid-prompt; got %v", paste)
	}
	if !contains(paste, "-t") || !contains(paste, "%9") {
		t.Errorf("paste-buffer must target the pane (-t %%9); got %v", paste)
	}

	// 3) the regression itself: NO command may be a raw `send-keys -l <msg>` that
	//    ships the newline-bearing message straight to the pane.
	for _, c := range cmds {
		if len(c) > 0 && c[0] == "send-keys" && contains(c, "-l") && contains(c, msg) {
			t.Fatalf("REGRESSION: message delivered via raw `send-keys -l` — multi-line prompts will truncate at the first newline: %v", c)
		}
	}
}

// A single-line prompt must still deliver atomically the same way (no special
// path that could skip bracketing and regress just for short messages).
func TestSendPaneSingleLineStillPastes(t *testing.T) {
	cmds := pasteCmds("%6", "take or decline")
	if len(cmds) != 2 || cmds[0][0] != "set-buffer" || cmds[1][0] != "paste-buffer" {
		t.Fatalf("single-line must use the same set-buffer+paste-buffer path, got %v", cmds)
	}
}

// Each send uses its OWN buffer name, so concurrent sends to different minds
// never clobber each other's pending paste.
func TestSendPaneBufferNamesAreDistinct(t *testing.T) {
	a := cmds2buf(pasteCmds("%6", "x"))
	b := cmds2buf(pasteCmds("%7", "y"))
	if a == "" || b == "" {
		t.Fatalf("could not read buffer name: a=%q b=%q", a, b)
	}
	if a == b {
		t.Errorf("two sends reused the same buffer name %q — concurrent sends would clobber", a)
	}
	if !strings.HasPrefix(a, "8send-") {
		t.Errorf("buffer name should be namespaced (8send-*), got %q", a)
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// cmds2buf pulls the buffer name out of the set-buffer command (the arg after -b).
func cmds2buf(cmds [][]string) string {
	for _, c := range cmds {
		for i, x := range c {
			if x == "-b" && i+1 < len(c) {
				return c[i+1]
			}
		}
	}
	return ""
}
