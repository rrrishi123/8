package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const codexTailBytes = 2 << 20

var codexBudgetMu sync.Mutex

func codexHome() string {
	if dir := os.Getenv("CODEX_HOME"); dir != "" {
		return dir
	}
	return os.ExpandEnv("$HOME/.codex")
}

// newestCodexBudget reads bounded tails, newest MODIFIED files first: a resumed
// old session can be active today. Null/premium-only limits must not erase the
// last account windows. This sensor never launches Codex or makes a request.
func newestCodexBudget(root string) map[string]any {
	type file struct {
		path string
		mod  time.Time
	}
	var files []file
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && filepath.Ext(path) == ".jsonl" {
			if st, e := d.Info(); e == nil {
				files = append(files, file{path, st.ModTime()})
			}
		}
		return nil
	})
	sort.Slice(files, func(i, j int) bool { return files[i].mod.After(files[j].mod) })
	if len(files) > 32 {
		files = files[:32]
	}
	var best map[string]any
	var bestAt time.Time
	for _, file := range files {
		f, err := os.Open(file.path)
		if err != nil {
			continue
		}
		st, err := f.Stat()
		if err != nil {
			f.Close()
			continue
		}
		off := max(int64(0), st.Size()-codexTailBytes)
		b, err := io.ReadAll(io.NewSectionReader(f, off, codexTailBytes))
		f.Close()
		if err != nil {
			continue
		}
		lines := bytes.Split(b, []byte{'\n'})
		if off > 0 { // the first line may begin halfway through a JSON record
			lines = lines[1:]
		}
		for i := len(lines) - 1; i >= 0; i-- {
			if !bytes.Contains(lines[i], []byte(`"rate_limits"`)) {
				continue
			}
			var rec struct {
				Timestamp string `json:"timestamp"`
				Type      string `json:"type"`
				Payload   struct {
					Type       string `json:"type"`
					RateLimits struct {
						ID        string             `json:"limit_id"`
						Plan      string             `json:"plan_type"`
						Primary   *codexWindowRecord `json:"primary"`
						Secondary *codexWindowRecord `json:"secondary"`
					} `json:"rate_limits"`
				} `json:"payload"`
			}
			if json.Unmarshal(lines[i], &rec) != nil || rec.Type != "event_msg" || rec.Payload.Type != "token_count" {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, rec.Timestamp)
			if err != nil || !at.After(bestAt) {
				continue
			}
			rl := rec.Payload.RateLimits
			if rl.ID != "" && rl.ID != "codex" {
				continue
			}
			windows := map[string]any{}
			for _, w := range []*codexWindowRecord{rl.Primary, rl.Secondary} {
				if w == nil || w.UsedPercent == nil || *w.UsedPercent < 0 || *w.UsedPercent > 100 {
					continue
				}
				name := map[int]string{300: "5h", 10080: "7d"}[w.Minutes]
				if name == "" {
					continue
				}
				windows[name] = map[string]any{"utilization": *w.UsedPercent / 100, "reset_at": w.Reset, "window_min": w.Minutes}
			}
			if len(windows) == 0 {
				continue
			}
			bestAt = at
			best = map[string]any{"provider": "codex", "plan": rl.Plan, "observed_at": rec.Timestamp,
				"source": file.path, "windows": windows}
		}
	}
	return best
}

type codexWindowRecord struct {
	UsedPercent *float64 `json:"used_percent"`
	Minutes     int      `json:"window_minutes"`
	Reset       int64    `json:"resets_at"`
}

func persistCodexBudget(cx map[string]any) error {
	codexBudgetMu.Lock()
	defer codexBudgetMu.Unlock()
	before, _ := codexReading()
	oldAt, _ := time.Parse(time.RFC3339Nano, stringValue(before["observed_at"]))
	newAt, err := time.Parse(time.RFC3339Nano, stringValue(cx["observed_at"]))
	if err != nil || !newAt.After(oldAt) {
		return err
	}
	dst := os.ExpandEnv("$HOME/.8/codex-budget.json")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cx, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(dst), ".codex-budget-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	_, err = f.Write(b)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(f.Name(), dst)
}

func stringValue(v any) string { s, _ := v.(string); return s }

func refreshCodexBudget() error {
	if cx := newestCodexBudget(filepath.Join(codexHome(), "sessions")); cx != nil {
		return persistCodexBudget(cx)
	}
	return nil
}

func codexBudgetLoop(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := refreshCodexBudget(); err != nil {
			log.Printf("codex budget: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
