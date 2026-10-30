package main

import (
	"os/exec"
	"testing"
)

func TestWatchDecision(t *testing.T) {
	cases := []struct {
		name       string
		ready      bool
		misses     int
		childAlive bool
		want       string
		wantMisses int
	}{
		{"ready resets", true, 5, true, "ok", 0},
		{"first miss, child alive", false, 0, true, "wait", 1},
		{"first miss, child gone", false, 0, false, "wait", 1},
		{"third miss, child gone -> spawn", false, 2, false, "spawn", 3},
		{"third miss, child alive -> no duplicate", false, 2, true, "wait", 3},
		{"seventh miss, child alive -> still wait", false, 6, true, "wait", 7},
		{"eighth miss, child alive -> kill (wedge)", false, 7, true, "kill", 8},
		{"eighth miss, child gone -> spawn", false, 7, false, "spawn", 8},
	}
	for _, c := range cases {
		got, m := watchDecision(c.ready, c.misses, c.childAlive)
		if got != c.want || m != c.wantMisses {
			t.Errorf("%s: got (%q,%d), want (%q,%d)", c.name, got, m, c.want, c.wantMisses)
		}
	}
}

func TestProcAlive(t *testing.T) {
	if procAlive(nil) {
		t.Fatal("nil cmd must not be alive")
	}
	cmd := exec.Command("sleep", "30")
	if err := cmd.Start(); err != nil {
		t.Skipf("cannot start sleep: %v", err)
	}
	if !procAlive(cmd) {
		t.Fatal("running child reported dead")
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	if procAlive(cmd) {
		t.Fatal("reaped child reported alive")
	}
}
