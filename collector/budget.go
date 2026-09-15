package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
}

var (
	budgetMu      sync.Mutex
	budgetLast    *budget
	budgetLastAt  time.Time
	budgetGateLog time.Time
)

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
func budgetNow() *budget {
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
	}
	budgetLast, budgetLastAt = best, time.Now()
	return best
}

// budgetAllows — the playlist gate: true when no sensor yet (never block on
// absence of evidence) or when every window is under the cap. Logs the gate
// state change at most every 10min.
func (c *collector) budgetAllows() bool {
	b := budgetNow()
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
	b := budgetNow()
	if b == nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"budget": nil, "note": "no sensor: tap an inspected pane (GET /panes/tap?pane=%N) so its API responses' anthropic-ratelimit-unified-* headers are logged"})
		return
	}
	names := make([]string, 0, len(b.Windows))
	for k := range b.Windows {
		names = append(names, k)
	}
	sort.Strings(names)
	_ = json.NewEncoder(w).Encode(map[string]any{"budget": b, "windows": names, "playlist_allowed": !b.Gated})
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
