package main

import (
	"os"
	"testing"
)

// TestDispatchCap pins the #4358 stagger: EIGHT_DISPATCH_CAP parses to the per-pass
// summon cap, 0 means uncapped, and an unset/garbage value falls back to the safe
// default of 2 (a few minds at a time, never a thundering herd).
func TestDispatchCap(t *testing.T) {
	old, had := os.LookupEnv("EIGHT_DISPATCH_CAP")
	defer func() {
		if had {
			os.Setenv("EIGHT_DISPATCH_CAP", old)
		} else {
			os.Unsetenv("EIGHT_DISPATCH_CAP")
		}
	}()
	cases := []struct {
		set  bool
		val  string
		want int
	}{
		{false, "", 2},   // unset → default 2
		{true, "", 2},    // empty → default 2
		{true, "0", 0},   // explicit uncapped
		{true, "1", 1},   // one at a time
		{true, "5", 5},   // wider
		{true, "-3", 2},  // negative is invalid → default
		{true, "abc", 2}, // garbage → default
	}
	for _, c := range cases {
		if c.set {
			os.Setenv("EIGHT_DISPATCH_CAP", c.val)
		} else {
			os.Unsetenv("EIGHT_DISPATCH_CAP")
		}
		if got := dispatchCap(); got != c.want {
			t.Errorf("dispatchCap() with %q = %d; want %d", c.val, got, c.want)
		}
	}
}
