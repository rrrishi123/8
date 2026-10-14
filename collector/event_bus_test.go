package main

import "testing"

// TestSettleStep pins the reply-completion heuristic (T5): a tab is "settled"
// (its reply complete) only after it CHANGED and then held stable to the
// threshold — never on a baseline read, a lone change, or stability with no prior
// activity.
func TestSettleStep(t *testing.T) {
	t.Run("baseline read emits nothing", func(t *testing.T) {
		d := map[string]int{}
		changed, settled := settleStep(d, "tab", "", "sig1", 1)
		if changed || settled {
			t.Fatalf("baseline: got changed=%v settled=%v, want false,false", changed, settled)
		}
	})

	t.Run("a shift is changed, not settled, and marks dirty", func(t *testing.T) {
		d := map[string]int{}
		changed, settled := settleStep(d, "tab", "sigA", "sigB", 1)
		if !changed || settled {
			t.Fatalf("shift: got changed=%v settled=%v, want true,false", changed, settled)
		}
		if q, ok := d["tab"]; !ok || q != 0 {
			t.Fatalf("shift: dirty[tab]=%v ok=%v, want 0,true", q, ok)
		}
	})

	t.Run("stable with no prior activity emits nothing", func(t *testing.T) {
		d := map[string]int{}
		changed, settled := settleStep(d, "tab", "sig", "sig", 1)
		if changed || settled {
			t.Fatalf("idle-stable: got changed=%v settled=%v, want false,false", changed, settled)
		}
	})

	t.Run("change then one stable check settles at threshold 1", func(t *testing.T) {
		d := map[string]int{}
		settleStep(d, "tab", "s0", "s1", 1) // shift → dirty
		changed, settled := settleStep(d, "tab", "s1", "s1", 1)
		if changed || !settled {
			t.Fatalf("settle@1: got changed=%v settled=%v, want false,true", changed, settled)
		}
		if _, ok := d["tab"]; ok {
			t.Fatalf("settle@1: dirty[tab] should be cleared after settle")
		}
	})

	t.Run("streaming holds dirty until it stops, then settles once at threshold 2", func(t *testing.T) {
		d := map[string]int{}
		settleStep(d, "tab", "s0", "s1", 2)          // shift (arriving)
		settleStep(d, "tab", "s1", "s2", 2)          // still shifting (still arriving)
		_, s1 := settleStep(d, "tab", "s2", "s2", 2) // 1st quiet check (q=1 < 2)
		if s1 {
			t.Fatalf("streaming: settled too early on first quiet check")
		}
		_, s2 := settleStep(d, "tab", "s2", "s2", 2) // 2nd quiet check (q+1=2 >= 2) → settle
		if !s2 {
			t.Fatalf("streaming: expected settle on second quiet check")
		}
	})
}
