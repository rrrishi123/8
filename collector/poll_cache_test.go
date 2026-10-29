package main

import (
	"testing"
	"time"
)

func resetCodexCache() {
	cxMu.Lock()
	cxCache = map[string]codexSessEntry{}
	cxMu.Unlock()
}

func TestCodexSessionForPIDCaches(t *testing.T) {
	calls := 0
	old := codexProbe
	codexProbe = func(pid string) string {
		calls++
		if pid == "blank" {
			return ""
		}
		return "sess-" + pid
	}
	defer func() { codexProbe = old; resetCodexCache() }()
	resetCodexCache()

	for i := 0; i < 5; i++ {
		if got := codexSessionForPID("42"); got != "sess-42" {
			t.Fatalf("call %d: got %q", i, got)
		}
	}
	if calls != 1 {
		t.Fatalf("cached pid should probe once, probed %d times", calls)
	}

	codexSessionForPID("blank")
	codexSessionForPID("blank")
	if calls != 2 {
		t.Fatalf("blank inside its TTL should not re-probe, probed %d times", calls)
	}
	cxMu.Lock()
	e := cxCache["blank"]
	e.at = time.Now().Add(-codexSessBlankTTL - time.Second)
	cxCache["blank"] = e
	cxMu.Unlock()
	codexSessionForPID("blank")
	if calls != 3 {
		t.Fatalf("expired blank should re-probe, probed %d times", calls)
	}

	cxMu.Lock()
	e = cxCache["42"]
	e.at = time.Now().Add(-codexSessTTL - time.Second)
	cxCache["42"] = e
	cxMu.Unlock()
	if got := codexSessionForPID("42"); got != "sess-42" || calls != 4 {
		t.Fatalf("expired hit should re-probe once: got %q, %d probes", got, calls)
	}

	cxMu.Lock()
	e = cxCache["blank"]
	e.at = time.Now().Add(-codexSessGC - time.Second)
	cxCache["blank"] = e
	cxMu.Unlock()
	codexSessionForPID("42")
	cxMu.Lock()
	_, still := cxCache["blank"]
	cxMu.Unlock()
	if still {
		t.Fatalf("entry untouched for codexSessGC should be dropped")
	}
}

func TestPaneUUIDsMemoized(t *testing.T) {
	puMu.Lock()
	puOut = nil
	puMu.Unlock()
	a := paneUUIDs()
	puMu.Lock()
	at := puAt
	puMu.Unlock()
	b := paneUUIDs()
	puMu.Lock()
	at2 := puAt
	puMu.Unlock()
	if !at.Equal(at2) {
		t.Fatalf("second call inside paneUUIDsTTL rescanned")
	}
	a["probe"] = "mutated"
	if _, shared := b["probe"]; shared {
		t.Fatalf("callers must get copies, not the cached map")
	}
}
