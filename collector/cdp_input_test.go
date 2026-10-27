package main

import (
	"net/http"
	"testing"
	"time"
)

// TestCdpKey guards the key mapping that backs the "press"/"key" action — so CI
// catches a regression in Enter/Tab/arrow/char handling without a live browser.
func TestCdpKey(t *testing.T) {
	cases := []struct {
		name          string
		wantKey, code string
		vk            int
	}{
		{"Enter", "Enter", "Enter", 13},
		{"enter", "Enter", "Enter", 13},
		{"Tab", "Tab", "Tab", 9},
		{"Escape", "Escape", "Escape", 27},
		{"ArrowDown", "ArrowDown", "ArrowDown", 40},
		{"Down", "ArrowDown", "ArrowDown", 40},
		{"Backspace", "Backspace", "Backspace", 8},
		{"a", "a", "KeyA", 65},
		{"Z", "Z", "KeyZ", 90},
		{" ", " ", "Space", 32},
	}
	for _, c := range cases {
		k, code, vk := cdpKey(c.name)
		if k != c.wantKey || code != c.code || vk != c.vk {
			t.Errorf("cdpKey(%q) = (%q,%q,%d); want (%q,%q,%d)", c.name, k, code, vk, c.wantKey, c.code, c.vk)
		}
	}
}

// TestCdpSessionCache verifies the smooth-control invariant: a second lookup of the
// same (broker,ctx) returns the CACHED session without a second attach, and
// cdpInvalidate forces a fresh attach. attach is counted via a stub broker base that
// never resolves, so a real attach returns "" — we seed the cache directly and assert
// reuse + eviction, the pure cache behaviour that keeps control from re-attaching.
func TestCdpSessionCache(t *testing.T) {
	c := &collector{cdpSes: map[string]string{}, client: &http.Client{Timeout: time.Second}}
	b := &broker{id: "chrome", base: "http://127.0.0.1:0"} // unreachable: attach yields ""
	c.cdpSes["chrome|CTX"] = "SID1"                        // seed as if already attached
	if got := c.cdpSession(b, "CTX"); got != "SID1" {
		t.Fatalf("cached session not reused: got %q want SID1", got)
	}
	c.cdpInvalidate(b, "CTX")
	if _, ok := c.cdpSes["chrome|CTX"]; ok {
		t.Fatalf("cdpInvalidate did not evict the session")
	}
	if got := c.cdpSession(b, "CTX"); got != "" { // no live broker → no new session
		t.Fatalf("expected empty session after eviction with unreachable broker, got %q", got)
	}
}
