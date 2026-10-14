package main

import "testing"

// Realistic `tmux capture-pane -e -p` frames (bottom of screen) for the two TUIs.
// The bug these pin: an IDLE pane whose composer shows a dimmed placeholder or
// ghost suggestion was read as "typing", so dispatch and /panes/send refused it.
const (
	rule = "\x1b[38;2;136;136;136m────────────────────────────────────────\x1b[39m"

	claudeIdleEmpty = "\x1b[38;2;215;119;87m✻\x1b[39m Sautéed for 1m 54s · done 7:33 pm\n\n" + rule +
		"\n\x1b[1m❯\x1b[22m \x1b[7m \x1b[27m\n" + rule +
		"\n  \x1b[38;2;153;153;153m⏵⏵ bypass permissions on (shift+tab to cycle)\x1b[39m\n"

	claudeIdleGhost = "\x1b[38;2;215;119;87m✻\x1b[39m Sautéed for 1m 54s · done 7:33 pm\n\n" + rule +
		"\n\x1b[1m❯\x1b[22m \x1b[7ms\x1b[27m\x1b[2mend it into the pane myself\x1b[22m\n" + rule +
		"\n  \x1b[38;2;153;153;153m⏵⏵ bypass permissions on (shift+tab to cycle)\x1b[39m\n"

	claudeIdleGhostGrey = rule + "\n❯ \x1b[7mT\x1b[27m\x1b[38;5;246mry \"refactor the collector\"\x1b[39m\n" + rule + "\n"

	claudeWorkingFrame = "● Reading collector/tui.go\n\n\x1b[38;2;215;119;87m✳\x1b[39m \x1b[38;2;215;119;87mOsmosing…\x1b[39m " +
		"\x1b[38;2;153;153;153m(6s · ↑ 1.2k tokens · esc to interrupt)\x1b[39m\n\n" + rule +
		"\n\x1b[1m❯\x1b[22m \x1b[7m \x1b[27m\n" + rule + "\n"

	claudeTyped = "\x1b[38;2;215;119;87m✻\x1b[39m Sautéed for 1m 54s · done 7:33 pm\n\n" + rule +
		"\n\x1b[1m❯\x1b[22m how many commits now\x1b[7m \x1b[27m\n" + rule + "\n"

	codexIdle = "• Done. Tests pass.\n\n\x1b[1m›\x1b[22m \x1b[2mWrite tests for @filename\x1b[22m\n\n" +
		"  \x1b[2m90% context left · ? for shortcuts\x1b[22m\n"

	codexIdleEmpty = "\x1b[1m›\x1b[22m \x1b[7m \x1b[27m\n\n  \x1b[2m90% context left\x1b[22m\n"

	codexWorking = "• \x1b[1mWorking\x1b[22m \x1b[2m(12s • esc to interrupt)\x1b[22m\n\n" +
		"\x1b[1m›\x1b[22m \x1b[2mSummarize recent commits\x1b[22m\n\n  \x1b[2m88% context left\x1b[22m\n"

	codexTyped = "• Done.\n\n\x1b[1m›\x1b[22m fix the flaky test\x1b[7m \x1b[27m\n\n  \x1b[2m90% context left\x1b[22m\n"
)

func TestTUIStateFrames(t *testing.T) {
	cases := []struct{ name, screen, cmd, want string }{
		{"claude idle", claudeIdleEmpty, "claude", "idle"},
		{"claude idle ghost suggestion (dim)", claudeIdleGhost, "claude", "idle"},
		{"claude idle ghost suggestion (grey)", claudeIdleGhostGrey, "claude", "idle"},
		{"claude working", claudeWorkingFrame, "claude", "working"},
		{"claude typed", claudeTyped, "claude", "typing"},
		{"codex idle placeholder", codexIdle, "codex", "idle"},
		{"codex idle empty", codexIdleEmpty, "codex", "idle"},
		{"codex working", codexWorking, "codex", "working"},
		{"codex typed", codexTyped, "codex", "typing"},
		// plain captures still behave
		{"claude done line is not working", "✻ Sautéed for 1m 54s · done 7:33 pm\n❯ ", "claude", "idle"},
		{"claude spinner without hint", "✳ Osmosing… (6s)\n❯ ", "claude", "working"},
		{"claude old prompt in transcript", "❯ earlier prompt\n● reply\n❯ ", "claude", "idle"},
		{"claude quoted esc far up", "esc to interrupt\n" + repeat("● line\n", 20) + "❯ ", "claude", "idle"},
		{"codex rotating placeholder plain", "› Explain this codebase", "codex", "idle"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got, _ := classifyScreen(tc.screen, tc.cmd); got != tc.want {
				t.Fatalf("state = %s, want %s", got, tc.want)
			}
		})
	}
	if hasUnsentInput(claudeIdleGhost) || !hasUnsentInput(claudeTyped) {
		t.Fatal("hasUnsentInput must ignore the ghost and see the draft")
	}
}

func repeat(s string, n int) (o string) {
	for i := 0; i < n; i++ {
		o += s
	}
	return
}
