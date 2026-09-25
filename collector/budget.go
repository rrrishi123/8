package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── BUDGET (2026-09-16) — the unified rate limit, read instead of guessed ─────
// Every API response carries anthropic-ratelimit-unified-* headers: the 5h and
// 7d utilization (0..1), allowed|rejected, and the epoch at which each window
// resets. They are ORG-wide, so one tapped pane is a sensor for the whole fleet.
// The four-system was inferring "capped" from a modal on screen; this reads the
// number, exposes it (GET /budget) and gates the playlist on it: dispatch pauses
// when a window is rejected or above EIGHT_BUDGET_CAP (default 0.95) and comes
// back by itself after the reset. Source: ~/.8/stream/<pid>.req.jsonl written
// by the request tap (GET /panes/tap?pane=%N installs it via the inspector).

type budgetWindow struct {
	Utilization float64 `json:"utilization"`
	Status      string  `json:"status"`
	ResetAt     string  `json:"reset_at"`
	ResetInS    int64   `json:"reset_in_s"`
}

type budget struct {
	ObservedAt string                  `json:"observed_at"`
	Source     string                  `json:"source"` // req log file
	Claim      string                  `json:"representative_claim,omitempty"`
	Overage    string                  `json:"overage_status,omitempty"`
	Windows    map[string]budgetWindow `json:"windows"` // 5h, 7d, 7d_oi
	Gated      bool                    `json:"gated"`
	Reason     string                  `json:"reason,omitempty"`
	Cap        float64                 `json:"cap"`
	// the FLIP SWITCH (2026-09-16): heavy experiments/research run while the 5h
	// window is below ResearchCeil (default 0.50); above it the fleet conserves.
	// Distinct from Cap (0.95) which HARD-stops all dispatch. Phase flows to the
	// minds as a work.phase wire frame on every flip.
	Phase        string  `json:"phase"`       // "research" (go) | "conserve" (hold heavy work)
	ResearchOK   bool    `json:"research_ok"` // 5h below the ceiling
	ResearchCeil float64 `json:"research_ceil"`
	FiveH        float64 `json:"five_h_util"`
}

var (
	budgetMu      sync.Mutex
	budgetLast    *budget
	budgetLastAt  time.Time
	budgetGateLog time.Time
)

func researchCeil() float64 {
	if v := os.Getenv("EIGHT_RESEARCH_CEIL"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			return f
		}
	}
	return 0.50
}

var lastPhase string

func budgetCap() float64 {
	if v := os.Getenv("EIGHT_BUDGET_CAP"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f > 0 && f <= 1 {
			return f
		}
	}
	return 0.95
}

// parseBudgetHeaders — PURE: the unified headers -> windows.
func parseBudgetHeaders(h map[string]string, now time.Time) (map[string]budgetWindow, string, string) {
	w := map[string]budgetWindow{}
	for k, v := range h {
		k = strings.ToLower(k)
		const p = "anthropic-ratelimit-unified-"
		if !strings.HasPrefix(k, p) {
			continue
		}
		rest := strings.TrimPrefix(k, p)
		i := strings.LastIndex(rest, "-")
		if i < 0 {
			continue
		}
		win, field := rest[:i], rest[i+1:]
		switch win { // not windows: overage/fallback/representative carry policy, not budget
		case "5h", "7d", "7d_oi":
		default:
			continue
		}
		bw := w[win]
		switch field {
		case "utilization":
			bw.Utilization, _ = strconv.ParseFloat(v, 64)
		case "status":
			bw.Status = v
		case "reset":
			if secs, err := strconv.ParseInt(v, 10, 64); err == nil {
				t := time.Unix(secs, 0)
				bw.ResetAt = t.UTC().Format(time.RFC3339)
				bw.ResetInS = int64(t.Sub(now).Seconds())
			}
		default:
			continue
		}
		w[win] = bw
	}
	for k := range w { // only real windows carry a status
		if w[k].Status == "" && w[k].ResetAt == "" {
			delete(w, k)
		}
	}
	return w, h["anthropic-ratelimit-unified-representative-claim"], h["anthropic-ratelimit-unified-overage-status"]
}

// budgetNow — newest tapped response with unified headers across all req logs
// (cached 10s). nil when no pane is tapped yet.
func (c *collector) budgetNow() *budget {
	budgetMu.Lock()
	defer budgetMu.Unlock()
	if budgetLast != nil && time.Since(budgetLastAt) < 10*time.Second {
		return budgetLast
	}
	files, _ := filepath.Glob(os.ExpandEnv("$HOME/.8/stream/*.req.jsonl"))
	var best *budget
	var bestTS string
	for _, f := range files {
		fh, err := os.Open(f)
		if err != nil {
			continue
		}
		st, _ := fh.Stat()
		off := st.Size() - 8*1024*1024 // a record carries the whole request body (200KB+): window must hold several
		if off < 0 {
			off = 0
		}
		b := make([]byte, st.Size()-off)
		if _, err := fh.ReadAt(b, off); err != nil && err != io.EOF {
			fh.Close()
			continue
		}
		fh.Close()
		lines := bytes.Split(b, []byte("\n"))
		for i := len(lines) - 1; i >= 0; i-- {
			var rec struct {
				TS  string            `json:"ts"`
				Res map[string]string `json:"res_headers"`
			}
			if json.Unmarshal(lines[i], &rec) != nil || rec.Res == nil || !hasUnified(rec.Res) {
				continue
			}
			if rec.TS <= bestTS {
				break
			}
			now := time.Now()
			w, claim, ov := parseBudgetHeaders(rec.Res, now)
			best = &budget{ObservedAt: rec.TS, Source: filepath.Base(f), Claim: claim, Overage: ov, Windows: w, Cap: budgetCap()}
			bestTS = rec.TS
			break
		}
	}
	if best != nil {
		for name, w := range best.Windows {
			if w.Status == "rejected" || (w.Utilization >= best.Cap && w.ResetInS > 0) {
				best.Gated = true
				best.Reason = fmt.Sprintf("%s window %s at %.0f%% (resets in %ds)", name, w.Status, w.Utilization*100, w.ResetInS)
			}
		}
		best.ResearchCeil = researchCeil()
		best.FiveH = best.Windows["5h"].Utilization
		best.ResearchOK = best.FiveH < best.ResearchCeil
		if best.ResearchOK {
			best.Phase = "research"
		} else {
			best.Phase = "conserve"
		}
		if best.Phase != lastPhase && lastPhase != "" { // the flip flows to the minds
			c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.phase","params":{"phase":%q,"five_h_util":%.3f,"ceil":%.2f}}}`, best.Phase, best.FiveH, best.ResearchCeil))
		}
		lastPhase = best.Phase
	}
	budgetLast, budgetLastAt = best, time.Now()
	return best
}

// budgetAllows — the playlist gate: true when no sensor yet (never block on
// absence of evidence) or when every window is under the cap. Logs the gate
// state change at most every 10min.
func (c *collector) budgetAllows() bool {
	b := c.budgetNow()
	if b == nil || !b.Gated {
		return true
	}
	if time.Since(budgetGateLog) > 10*time.Minute {
		budgetGateLog = time.Now()
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.budget_gated","params":{"reason":%q,"observed_at":%q}}}`, b.Reason, b.ObservedAt))
	}
	return false
}

func (c *collector) handleBudget(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	b := c.budgetNow()
	if b == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"budget": nil, "note": "no sensor: tap an inspected pane (GET /panes/tap?pane=%N) so its API responses' anthropic-ratelimit-unified-* headers are logged"})
		return
	}
	names := make([]string, 0, len(b.Windows))
	for k := range b.Windows {
		names = append(names, k)
	}
	sort.Strings(names)
	age := budgetAgeSeconds(b)
	tapped := 0
	tappedPids.Range(func(_, _ any) bool { tapped++; return true })
	var codexProv any
	cx, codexAge := codexReading()
	if cx != nil {
		codexProv = cx
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"budget": b, "windows": names, "playlist_allowed": !b.Gated,
		"observed_age_s": age, "sensors_armed": tapped,
		"providers":            map[string]any{"claude": b, "codex": codexProv},
		"codex_observed_age_s": codexAge,
		"freshness":            "budget refreshes only on a live /v1/messages call from a tapped pane; an all-idle fleet shows last-known (no consumption either)"})
}

// tapJS — installed inside an inspected pane: logs every API request (headers
// redacted, body verbatim) and its response headers to ~/.8/stream/<pid>.req.jsonl,
// and tees SSE bodies to <pid>.sse. Idempotent.
const tapJS = `(() => {
  if (globalThis.__tap) return 'already:' + globalThis.__tap.req;
  const home = process.env.HOME, pid = process.pid;
  const T = { req: home + '/.8/stream/' + pid + '.req.jsonl', sse: home + '/.8/stream/' + pid + '.sse', n: 0, err: '' };
  const wr = Bun.file(T.req).writer(), ws = Bun.file(T.sse).writer(), dec = new TextDecoder();
  const redact = (h) => { const o = {}; for (const [k, v] of h) o[k] = /key|auth|token|cookie/i.test(k) ? '<redacted ' + v.length + ' chars>' : v; return o; };
  const orig = globalThis.fetch;
  globalThis.fetch = async function (input, init) {
    const url = typeof input === 'string' ? input : (input && input.url) || String(input);
    const api = /anthropic\.com|\/v1\/messages/.test(url);
    let rec = null;
    if (api) { T.n++; rec = { seq: T.n, ts: new Date().toISOString(), url, method: (init && init.method) || 'GET' };
      try { rec.headers = redact(new Headers((init && init.headers) || {})); } catch (e) { rec.headers_err = String(e); }
      try { const b = init && init.body; rec.body = typeof b === 'string' ? b : b ? '<' + (b.constructor && b.constructor.name) + '>' : ''; } catch (e) { rec.body_err = String(e); } }
    const t0 = Date.now();
    const res = await orig.call(this, input, init);
    if (!rec) return res;
    rec.status = res.status; rec.res_headers = redact(res.headers); rec.ttfb_ms = Date.now() - t0;
    wr.write(JSON.stringify(rec) + '\n'); wr.flush();
    try { const ct = res.headers.get('content-type') || '';
      if (ct.includes('text/event-stream') && res.body) { const [a, b] = res.body.tee();
        ws.write('\n### ' + rec.seq + ' ' + rec.ts + ' ' + url + '\n'); ws.flush();
        (async () => { const r = b.getReader(); while (true) { const { done, value } = await r.read(); if (done) break; ws.write(dec.decode(value, { stream: true })); ws.flush(); } ws.write('\n### end ' + rec.seq + '\n'); ws.flush(); })();
        return new Response(a, { status: res.status, statusText: res.statusText, headers: res.headers }); } } catch (e) { T.err = String(e); }
    return res;
  };
  globalThis.__tap = T; return 'installed:' + T.req;
})()`

// handlePaneTap — GET /panes/tap?pane=%N: make an inspected pane a sensor.
func (c *collector) handlePaneTap(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	pane := r.URL.Query().Get("pane")
	wsurl := procOf(pane).Inspector
	if wsurl == "" {
		http.Error(w, `{"error":"no inspector for that pane — launch it with BUN_INSPECT=ws://127.0.0.1:<port>/dbg"}`, 404)
		return
	}
	os.MkdirAll(os.ExpandEnv("$HOME/.8/stream"), 0o755)
	res, err := wsEval(wsurl, tapJS)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"pane": pane, "error": err.Error()})
		return
	}
	c.publish(fmt.Sprintf(`{"session":"panes","origin":"COLLECTOR","frame":{"method":"pane.tap","params":{"pane":%q,"inspector":%q}}}`, pane, wsurl))
	_ = json.NewEncoder(w).Encode(map[string]any{"pane": pane, "inspector": wsurl, "result": res})
}

// hasUnified — any anthropic-ratelimit-unified-*-reset header (the bare
// "-reset" is not always sent; the per-window ones are).
func hasUnified(h map[string]string) bool {
	for k := range h {
		if strings.HasPrefix(strings.ToLower(k), "anthropic-ratelimit-unified-") && strings.HasSuffix(k, "-reset") {
			return true
		}
	}
	return false
}

// ── AUTO-TAP (2026-09-16) — every inspected pane is a sensor, no manual step ──
// The tap is in-process JS: it dies when the claude process restarts (rebirth,
// relaunch) and a new pane is born untapped. This installs the tap on every
// inspected pane whose pid we have not tapped yet, each probe tick. tapJS is
// idempotent (returns early if __tap exists), so a collector restart re-arms
// harmlessly. Result: budget flows whenever ANY pane makes a /v1/messages call.
var tappedPids sync.Map // pid -> true

func (c *collector) autoTap() {
	procMu.Lock()
	type pw struct {
		pid int
		ws  string
	}
	var todo []pw
	for _, pf := range procByPane {
		if pf.Inspector != "" && pf.Pid > 0 {
			if _, done := tappedPids.Load(pf.Pid); !done {
				todo = append(todo, pw{pf.Pid, pf.Inspector})
			}
		}
	}
	procMu.Unlock()
	for _, t := range todo {
		if _, err := wsEval(t.ws, tapJS); err == nil {
			tappedPids.Store(t.pid, true)
		}
	}
}

// budgetAgeSeconds — how stale the newest budget observation is (−1 if none).
func budgetAgeSeconds(b *budget) int64 {
	if b == nil {
		return -1
	}
	t, err := time.Parse(time.RFC3339, strings.Replace(b.ObservedAt, ".000Z", "Z", 1))
	if err != nil {
		if t, err = time.Parse("2006-01-02T15:04:05.000Z", b.ObservedAt); err != nil {
			return -1
		}
	}
	return int64(time.Since(t).Seconds())
}

// ── RENEW (2026-09-19) — POST /budget/poke?provider=claude|codex|both ────────
// The old poke ran `claude -p` UNTAPPED (BUN_INSPECT stripped), so its response
// headers were never logged and the reading never moved. Now every refresh is an
// OBSERVED response, per provider:
//   claude — implementation (a), self-contained: spawn `claude -p hi` with a
//            FRESH loopback inspector (BUN_INSPECT=ws://127.0.0.1:<free>/dbg?wait=1).
//            `?wait=1` makes the Bun runtime hold the main script until a frontend
//            has sent Inspector.initialized and disconnected (wsEvalInit), so
//            tapJS is installed BEFORE any JS runs — no race with the first
//            /v1/messages call. The response's unified headers land in
//            ~/.8/stream/<pid>.req.jsonl and we poll that file for the record.
//   codex  — ~/.8/codex-budget.sh --poke (a trivial `codex exec`, then the newest
//            rollout rate_limits), its JSON written to ~/.8/codex-budget.json.
// A provider that could not be refreshed is reported refreshed:false + reason
// with its last-known reading and age — never a silent stale.

const (
	pokeTapWindow  = 3 * time.Second  // how long to retry the inspector connect
	pokeObserveFor = 15 * time.Second // how long to wait for the fresh record
	pokeKillAfter  = 45 * time.Second // the spawned claude is killed past this
	pokeCodexAfter = 30 * time.Second
)

// freeLoopbackPort — a port the kernel says is unused right now.
func freeLoopbackPort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

// launchTapped — spawn claude with its own held inspector and inject tapJS over
// it; returns once the tap is installed (the script then starts) or kills the
// held process when the inject failed, so nothing ever runs untapped.
func launchTapped(ctx context.Context, args ...string) (*exec.Cmd, string, error) {
	port, err := freeLoopbackPort()
	if err != nil {
		return nil, "", err
	}
	wsurl := fmt.Sprintf("ws://127.0.0.1:%d/dbg", port)
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Dir = os.ExpandEnv("$HOME/Desktop/repos")
	cmd.Env = append(envWithout(os.Environ(), "BUN_INSPECT"), "BUN_INSPECT="+wsurl+"?wait=1")
	cmd.Stdin = devNull()
	os.MkdirAll(os.ExpandEnv("$HOME/.8/stream"), 0o755)
	if err := cmd.Start(); err != nil {
		return nil, wsurl, err
	}
	deadline := time.Now().Add(pokeTapWindow)
	for {
		if _, err = wsEvalInit(wsurl, tapJS, true); err == nil { // tap, then release the held script
			return cmd, wsurl, nil
		}
		if ctx.Err() != nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	_ = cmd.Process.Kill()
	go cmd.Wait()
	return nil, wsurl, fmt.Errorf("tap inject failed on %s: %v", wsurl, err)
}

// freshUnifiedRecord — true when ~/.8/stream/<pid>.req.jsonl holds a response
// with unified headers (the tap writes the record the moment headers arrive).
func freshUnifiedRecord(pid int) bool {
	b, err := os.ReadFile(os.ExpandEnv("$HOME/.8/stream/") + strconv.Itoa(pid) + ".req.jsonl")
	if err != nil {
		return false
	}
	for _, ln := range bytes.Split(b, []byte("\n")) {
		var rec struct {
			Res map[string]string `json:"res_headers"`
		}
		if json.Unmarshal(ln, &rec) == nil && hasUnified(rec.Res) {
			return true
		}
	}
	return false
}

// pokeClaude — one tapped `claude -p hi`; true when a fresh observed response
// was logged within pokeObserveFor. The process finishes (or is killed at
// pokeKillAfter) in the background — the reading is on disk before it exits.
func (c *collector) pokeClaude() (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), pokeKillAfter)
	cmd, wsurl, err := launchTapped(ctx, "-p", "hi", "--dangerously-skip-permissions")
	if err != nil {
		cancel()
		return false, err.Error()
	}
	pid := cmd.Process.Pid
	tappedPids.Store(pid, true)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait(); cancel() }()
	deadline := time.Now().Add(pokeObserveFor)
	for {
		if freshUnifiedRecord(pid) {
			c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"budget.poke","params":{"provider":"claude","pid":%d,"inspector":%q}}}`, pid, wsurl))
			return true, ""
		}
		select {
		case err := <-done:
			if freshUnifiedRecord(pid) {
				return true, ""
			}
			return false, fmt.Sprintf("claude -p exited (%v) without an observed /v1/messages response — pid %d", err, pid)
		case <-time.After(300 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			return false, fmt.Sprintf("no observed response within %s (pid %d still running; killed at %s)", pokeObserveFor, pid, pokeKillAfter)
		}
	}
}

// codexReading — ~/.8/codex-budget.json as written by codex-budget.sh (nil
// when absent or without windows) and its observed age in seconds (nil when unknown).
func codexReading() (map[string]any, any) {
	b, err := os.ReadFile(os.ExpandEnv("$HOME/.8/codex-budget.json"))
	if err != nil {
		return nil, nil
	}
	var cx map[string]any
	if json.Unmarshal(b, &cx) != nil {
		return nil, nil
	}
	return cx, codexAge(cx)
}

func codexAge(cx map[string]any) any {
	if cx == nil {
		return nil
	}
	if oa, ok := cx["observed_at"].(string); ok {
		if t, e := time.Parse(time.RFC3339, oa); e == nil {
			return int(time.Since(t).Seconds())
		}
	}
	return nil
}

// pokeCodex — codex-budget.sh --poke, its output persisted to codex-budget.json;
// refreshed only when the newest rollout rate_limits moved past the prior reading.
func (c *collector) pokeCodex() (bool, string) {
	before, _ := codexReading()
	beforeTS, _ := before["observed_at"].(string)
	script := os.ExpandEnv("$HOME/.8/codex-budget.sh")
	if _, err := os.Stat(script); err != nil {
		return false, "codex-budget.sh not found: " + err.Error()
	}
	ctx, cancel := context.WithTimeout(context.Background(), pokeCodexAfter)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", script, "--poke")
	cmd.Stdin = devNull()
	out, err := cmd.Output()
	if err != nil && len(bytes.TrimSpace(out)) == 0 {
		return false, "codex-budget.sh --poke failed: " + err.Error()
	}
	var cx map[string]any
	if json.Unmarshal(out, &cx) != nil {
		return false, "codex-budget.sh output is not JSON: " + firstN(string(out), 120)
	}
	if cx["windows"] == nil {
		note, _ := cx["note"].(string)
		return false, "no codex rate_limits after poke: " + note
	}
	dst := os.ExpandEnv("$HOME/.8/codex-budget.json")
	if b, e := json.MarshalIndent(cx, "", "  "); e == nil {
		if e = os.WriteFile(dst+".tmp", b, 0o644); e == nil {
			_ = os.Rename(dst+".tmp", dst)
		}
	}
	afterTS, _ := cx["observed_at"].(string)
	if afterTS <= beforeTS {
		return false, fmt.Sprintf("newest codex rollout rate_limits unchanged (observed_at %s) — codex exec may have failed", afterTS)
	}
	c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"budget.poke","params":{"provider":"codex","observed_at":%q}}}`, afterTS))
	return true, ""
}

// handleBudgetPoke — POST /budget/poke?provider=claude|codex|both (default both).
// Each provider refreshes concurrently under its own timeout; the response
// carries both readings, which of them actually moved, and why when not.
func (c *collector) handleBudgetPoke(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	prov := r.URL.Query().Get("provider")
	if prov == "" {
		prov = "both"
	}
	doClaude, doCodex := prov == "both" || prov == "claude", prov == "both" || prov == "codex"
	if !doClaude && !doCodex {
		http.Error(w, `{"error":"provider must be claude, codex or both"}`, 400)
		return
	}
	refreshed := map[string]bool{"claude": false, "codex": false}
	reasons := map[string]string{}
	if !doClaude {
		reasons["claude"] = "not requested"
	}
	if !doCodex {
		reasons["codex"] = "not requested"
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	if doClaude {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, why := c.pokeClaude()
			mu.Lock()
			refreshed["claude"] = ok
			if why != "" {
				reasons["claude"] = why
			}
			mu.Unlock()
		}()
	}
	if doCodex {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, why := c.pokeCodex()
			mu.Lock()
			refreshed["codex"] = ok
			if why != "" {
				reasons["codex"] = why
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	budgetMu.Lock()
	budgetLastAt = time.Time{} // force a re-read past the 10s cache
	budgetMu.Unlock()
	b := c.budgetNow()
	age := budgetAgeSeconds(b)
	cx, cxAge := codexReading()
	var codexProv any
	if cx != nil {
		codexProv = cx
	}
	note := "each reading is an observed response; refreshed=false carries the last-known reading and its age"
	if b == nil {
		note = "claude: no observed response on record yet — " + reasons["claude"]
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"budget":               b, // legacy key
		"providers":            map[string]any{"claude": b, "codex": codexProv},
		"refreshed":            refreshed,
		"reasons":              reasons,
		"observed_age_s":       age,
		"codex_observed_age_s": cxAge,
		"note":                 note,
	})
}
