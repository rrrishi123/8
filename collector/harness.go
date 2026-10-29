package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func harnessKind(cmd string) string {
	switch strings.ToLower(filepath.Base(cmd)) {
	case "codex", "codex.exe":
		return "codex"
	case "claude", "claude.exe", "claude-code", "node":
		return "claude-code"
	}
	return ""
}

func paneKind(pane string) string {
	for _, p := range tmuxPanes() {
		if p.ID == pane {
			return harnessKind(p.Cmd)
		}
	}
	return ""
}

// The live Codex process holds its actual rollout open, including after resume
// or /new. This avoids newest-file guesses and stale %N -> uuid registry files.
func codexSessionProbe(pid string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "lsof", "-a", "-p", pid, "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "n") {
			continue
		}
		path := strings.TrimPrefix(line, "n")
		if strings.HasPrefix(filepath.Base(path), "rollout-") && strings.HasSuffix(path, ".jsonl") {
			if id := codexSessionFile(path); id != "" {
				return id
			}
		}
	}
	return ""
}

func codexSessionFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	// Only the session metadata header, never conversation contents.
	b, _ := io.ReadAll(io.LimitReader(f, 64<<10))
	line := strings.SplitN(string(b), "\n", 2)[0]
	var rec struct {
		Type    string `json:"type"`
		Payload struct {
			ID string `json:"id"`
		} `json:"payload"`
	}
	if json.Unmarshal([]byte(line), &rec) != nil || rec.Type != "session_meta" || len(rec.Payload.ID) != 36 {
		return ""
	}
	return rec.Payload.ID
}

func (c *collector) budgetAllowsPane(pane string) bool {
	if paneKind(pane) == "codex" {
		cx, _ := codexReading()
		return cx == nil || cx["gated"] != true
	}
	return c.budgetAllows()
}

// codexSessionForPID — cached codexSessionProbe. A live Codex keeps the same
// rollout open until /new or a resume, so re-running lsof on it every scan (the
// Oct-12 inspector-port storm, reincarnated for Codex) buys nothing. A found id
// lives codexSessTTL; a blank (rollout not open yet) is retried after
// codexSessBlankTTL; entries not asked about for codexSessGC are dropped, which
// is how dead pids leave the map.
const (
	codexSessTTL      = 60 * time.Second
	codexSessBlankTTL = 5 * time.Second
	codexSessGC       = 10 * time.Minute
)

type codexSessEntry struct {
	id string
	at time.Time
}

var (
	cxMu       sync.Mutex
	cxCache    = map[string]codexSessEntry{}
	codexProbe = codexSessionProbe // swapped in tests
)

func codexSessionForPID(pid string) string {
	now := time.Now()
	cxMu.Lock()
	for p, v := range cxCache { // tiny map; dead pids leave here
		if now.Sub(v.at) > codexSessGC {
			delete(cxCache, p)
		}
	}
	if e, ok := cxCache[pid]; ok {
		ttl := codexSessTTL
		if e.id == "" {
			ttl = codexSessBlankTTL
		}
		if now.Sub(e.at) < ttl {
			cxMu.Unlock()
			return e.id
		}
	}
	cxMu.Unlock()
	id := codexProbe(pid)
	cxMu.Lock()
	cxCache[pid] = codexSessEntry{id: id, at: time.Now()}
	cxMu.Unlock()
	return id
}
