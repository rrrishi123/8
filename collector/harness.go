package main

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
func codexSessionForPID(pid string) string {
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
