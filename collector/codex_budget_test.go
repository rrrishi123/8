package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func codexRecord(ts string, used int, id string) string {
	return fmt.Sprintf(`{"timestamp":%q,"type":"event_msg","payload":{"type":"token_count","rate_limits":{"limit_id":%q,"primary":{"used_percent":%d,"window_minutes":300,"resets_at":%d},"secondary":{"used_percent":98,"window_minutes":10080,"resets_at":%d}}}}`+"\n", ts, id, used, time.Now().Add(time.Hour).Unix(), time.Now().Add(24*time.Hour).Unix())
}

func TestCodexSensorAndProviderWithoutClaude(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CODEX_HOME", filepath.Join(home, "custom-codex"))
	t.Setenv("PATH", "") // passive refresh must not execute any CLI/script
	dir := filepath.Join(codexHome(), "sessions", "2020", "01", "01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "resumed.jsonl")
	ts := time.Now().UTC().Truncate(time.Second).Format(time.RFC3339)
	// Oversized leading record, valid account limits, newer empty/premium
	// limits and a torn final write: only the real account observation survives.
	content := strings.Repeat("x", codexTailBytes+100) + "\n" + codexRecord(ts, 42, "codex") +
		codexRecord(time.Now().Add(time.Second).UTC().Format(time.RFC3339), 99, "premium") +
		`{"timestamp":"2099-01-01T00:00:00Z","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":null,"secondary":null}}}` + "\n" + `{"timestamp":`
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := refreshCodexBudget(); err != nil {
		t.Fatal(err)
	}
	cx, _ := codexReading()
	if cx["observed_at"] != ts || cx["five_h_util"] != 0.42 || cx["gated"] != true {
		t.Fatalf("lost account reading or weekly gate: %+v", cx)
	}
	// An older manual result cannot race the ticker backward.
	old := newestCodexBudget(filepath.Join(codexHome(), "sessions"))
	old["observed_at"] = "2000-01-01T00:00:00Z"
	if err := persistCodexBudget(old); err != nil {
		t.Fatal(err)
	}
	budgetMu.Lock()
	prev, prevAt := budgetLast, budgetLastAt
	budgetLast, budgetLastAt = nil, time.Time{}
	budgetMu.Unlock()
	t.Cleanup(func() { budgetMu.Lock(); budgetLast, budgetLastAt = prev, prevAt; budgetMu.Unlock() })
	c := newCollector(nil)
	w := httptest.NewRecorder()
	c.handleBudget(w, httptest.NewRequest("GET", "/budget", nil))
	var body struct {
		Budget    any                       `json:"budget"`
		Providers map[string]map[string]any `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Budget != nil || body.Providers["codex"]["observed_at"] != ts {
		t.Fatalf("Codex must be visible without Claude: %s (%v)", w.Body, err)
	}
	p, err := c.peerHeartbeat("test")
	if err != nil || !strings.Contains(string(p.Extra), `"codex_5h":0.42`) {
		t.Fatalf("native federation lost budget: %s (%v)", p.Extra, err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := refreshCodexBudget(); err != nil {
		t.Fatal(err)
	}
	cx, _ = codexReading()
	if cx["observed_at"] != ts {
		t.Fatal("no rollout must preserve last-known timestamp")
	}
}

func TestCodexBudgetTickerRefreshAndCancel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", t.TempDir())
	dir := filepath.Join(codexHome(), "sessions")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { codexBudgetLoop(ctx, 10*time.Millisecond); close(done) }()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if err := os.WriteFile(filepath.Join(dir, "active.jsonl"), []byte(codexRecord(ts, 31, "codex")), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		cx, _ := codexReading()
		if cx["observed_at"] == ts {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ticker did not read newly written rollout")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ticker ignored cancellation")
	}
}
