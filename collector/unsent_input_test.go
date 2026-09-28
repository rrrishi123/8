package main

import "testing"

// TestHasUnsentInput witnesses the collision guard: a "❯ <text>" prompt holds
// composed-but-unsent operator input and must never be typed on top of; a bare
// "❯ " prompt is empty and safe.
func TestHasUnsentInput(t *testing.T) {
	cases := map[string]bool{
		"...\n❯ \n─────":                                false, // empty prompt
		"...\n❯ how many commits now\n─────":            true,  // the exact collision case
		"...\n❯   \n─────":                              false, // spaces only
		"some ❯ arrow mid-line\n❯ hi":                   true,  // real prompt line has text
		"no prompt here at all":                         false,
		"...\n❯ Press up to edit queued messages\n────": false, // grey placeholder, not a draft
		"...\n❯ Try \"how does X work?\"\n────":          false, // grey placeholder
		"...\n❯ ⏎ to send\n────":                        false, // grey hint
	}
	for screen, want := range cases {
		if got := hasUnsentInput(screen); got != want {
			t.Errorf("hasUnsentInput(%q) = %v, want %v", screen, got, want)
		}
	}
}
