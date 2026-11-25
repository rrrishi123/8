package main

import (
	"testing"
	"time"
)

// TestNudgeCooldown pins the #9570 trigger knob: EIGHT_NUDGE_COOLDOWN_S parses
// to the min interval between inbox pointers, 0 means every pass, and an
// unset/garbage/negative value falls back to the safe 30min default (the
// pre-#9570 behavior, so deployments without the env are unchanged).
func TestNudgeCooldown(t *testing.T) {
	cases := []struct {
		set  bool
		val  string
		want time.Duration
	}{
		{false, "", 30 * time.Minute}, // unset → default (behavior unchanged)
		{true, "", 30 * time.Minute},  // empty → default
		{true, "0", 0},                // explicit: every dispatch pass
		{true, "240", 4 * time.Minute},
		{true, "1800", 30 * time.Minute},
		{true, "-5", 30 * time.Minute},  // negative is invalid → default
		{true, "abc", 30 * time.Minute}, // garbage → default
	}
	for _, c := range cases {
		if c.set {
			t.Setenv("EIGHT_NUDGE_COOLDOWN_S", c.val)
		}
		if got := nudgeCooldown(); got != c.want {
			t.Errorf("set=%v val=%q: got %v, want %v", c.set, c.val, got, c.want)
		}
	}
}
