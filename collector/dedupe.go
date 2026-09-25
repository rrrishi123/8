package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ── AUTO-DEDUPE + /read (#643) — the organism does its own tab hygiene ───────
// The operator hand-closed 2 of 3 identical claude.ai tabs and named the smell:
// the agent doing the organism's job. The reconciler already dedupes :8088;
// this generalizes it WITH the legitimate-concurrent-use guard: N tabs sharing
// a normalized URL collapse to one — UNLESS >=2 distinct agents are actively
// holding it (recent acts on its context, or live manifest claims). Never
// blind: dedupe by URL AND holder-count. Every close publishes tab.deduped —
// the close carries its own receipt.

// normTabURL: lowercase scheme+host, strip fragment + trailing slash. Query is
// KEPT deliberately — two views differing only by query are distinct on
// purpose more often than they are garbage; we never over-close.
func normTabURL(u string) string {
	if i := strings.Index(u, "#"); i >= 0 {
		u = u[:i]
	}
	if i := strings.Index(u, "://"); i > 0 {
		rest := u[i+3:]
		if j := strings.IndexAny(rest, "/?"); j >= 0 {
			u = strings.ToLower(u[:i+3]+rest[:j]) + rest[j:]
		} else {
			u = strings.ToLower(u)
		}
	}
	return strings.TrimSuffix(u, "/")
}

func dedupeSkip(u string) bool {
	return u == "" || strings.HasPrefix(u, "about:") || strings.HasPrefix(u, "chrome:") ||
		strings.HasPrefix(u, "moz-extension:") || strings.Contains(u, ":8088") ||
		strings.Contains(u, "localhost%3a8088")
}

var ctxInBody = regexp.MustCompile(`"context"\s*:\s*"([0-9a-f-]{36})"`)

// holdersByURL — distinct identities actively on each normalized URL:
// (a) declared actors whose channel acts in the last window targeted a BiDi
// context currently showing that URL; (b) live manifest claims on it. All
// undeclared acts collapse to one anonymous holder — two blanks can't fake
// two agents.
func (c *collector) holdersByURL(window time.Duration) map[string]map[string]bool {
	uuidURL := map[string]string{}
	for i := range c.brokers { // every configured broker is a channel seat by construction
		b := &c.brokers[i]
		out, err := c.command(b, `{"method":"browsingContext.getTree","params":{}}`)
		if err != nil {
			continue
		}
		var tr struct {
			Result struct {
				Contexts []struct {
					Context string `json:"context"`
					URL     string `json:"url"`
				} `json:"contexts"`
			} `json:"result"`
		}
		if json.Unmarshal(out, &tr) == nil {
			for _, t := range tr.Result.Contexts {
				uuidURL[t.Context] = normTabURL(t.URL)
			}
		}
	}
	holders := map[string]map[string]bool{}
	cut := time.Now().Add(-window)
	c.lmu.Lock()
	for _, e := range c.ledger {
		if e.Physics != "channel" {
			continue
		}
		if ts, err := time.Parse(time.RFC3339Nano, e.TS); err != nil || ts.Before(cut) {
			continue
		}
		m := ctxInBody.FindStringSubmatch(e.Body)
		if m == nil {
			continue
		}
		u := uuidURL[m[1]]
		if u == "" {
			continue
		}
		if holders[u] == nil {
			holders[u] = map[string]bool{}
		}
		holders[u][e.Actor] = true // "" = the one anonymous holder
	}
	c.lmu.Unlock()
	c.tmu.Lock()
	for _, rec := range c.manifest {
		if rec != nil && rec.ClaimedBy != "" && claimLive(rec) {
			u := normTabURL(rec.URL)
			if holders[u] == nil {
				holders[u] = map[string]bool{}
			}
			holders[u][rec.ClaimedBy] = true
		}
	}
	c.tmu.Unlock()
	return holders
}

type chromeTab struct {
	Bcid  string `json:"bcid"`
	URL   string `json:"url"`
	Title string `json:"title"`
	Sel   bool   `json:"sel"`
}

// autoDedupe collapses duplicate tabs, holder-guarded. Keeper preference: the
// SELECTED tab (never yank the operator's focus), else the first enumerated
// (window order — the elder). Runs only after the manifest is seeded, so the
// witness never prunes a world it has not yet read.
func (c *collector) autoDedupe(ct []chromeTab) {
	groups := map[string][]chromeTab{}
	for _, t := range ct {
		u := normTabURL(t.URL)
		if dedupeSkip(strings.ToLower(t.URL)) {
			continue
		}
		groups[u] = append(groups[u], t)
	}
	var holders map[string]map[string]bool // lazy — only when a dupe exists
	for u, g := range groups {
		if len(g) < 2 {
			continue
		}
		if holders == nil {
			holders = c.holdersByURL(10 * time.Minute)
		}
		if len(holders[u]) >= 2 { // legitimate concurrent use — honor it
			c.publish(fmt.Sprintf(`{"session":"fox","origin":"COLLECTOR","frame":{"method":"tab.dedupe_skipped","params":{"url":%q,"tabs":%d,"holders":%d}}}`, u, len(g), len(holders[u])))
			continue
		}
		keep := g[0]
		for _, t := range g {
			if t.Sel {
				keep = t
				break
			}
		}
		var kill []string
		for _, t := range g {
			if t.Bcid != keep.Bcid {
				kill = append(kill, t.Bcid)
			}
		}
		if len(kill) == 0 {
			continue
		}
		js, _ := json.Marshal(kill)
		script := `const cb=arguments[arguments.length-1];try{let kill=new Set(` + string(js) + `);let n=0;` +
			`for(let w of Services.wm.getEnumerator("navigator:browser")){for(let t of Array.from(w.gBrowser.tabs)){let b=t.linkedBrowser;` +
			`if(b.browsingContext&&kill.has(String(b.browsingContext.id))){try{w.gBrowser.removeTab(t);n++;}catch(e){}}}}cb(String(n));}catch(e){cb('ERR:'+e);}`
		if out, err := c.execChrome(script); err == nil && !strings.HasPrefix(out, "ERR:") {
			c.publish(fmt.Sprintf(`{"session":"fox","origin":"COLLECTOR","frame":{"method":"tab.deduped","params":{"url":%q,"closed":%s,"kept":%q,"holders":%d}}}`, u, string(js), keep.Bcid, len(holders[u])))
		}
	}
}

// handleRead (#643 second half) — kill the spawn-and-scrape reflex: GET
// /read?url=U resolves to an ALREADY-OPEN context and reads it afferently;
// it only ever creates a tab on an explicit &create=1 — spawning is intent,
// never a reflex.
func (c *collector) handleRead(w http.ResponseWriter, r *http.Request) {
	u := r.URL.Query().Get("url")
	if u == "" {
		http.Error(w, `{"error":"need ?url="}`, http.StatusBadRequest)
		return
	}
	want := normTabURL(u)
	w.Header().Set("Content-Type", "application/json")
	read := func(b *broker, ctx, curURL string, created bool) {
		expr := `(()=>{const t=document.title;const x=(document.body&&document.body.innerText)||'';return JSON.stringify({title:t,text:x.slice(0,100000)})})()`
		frame := fmt.Sprintf(`{"method":"script.evaluate","params":{"expression":%q,"target":{"context":%q},"awaitPromise":false}}`, expr, ctx)
		start := time.Now()
		out, err := c.command(b, frame)
		lat := float64(time.Since(start).Nanoseconds()) / 1000.0
		id := c.record(reqRec{TS: nowNano(), Physics: "channel", Session: b.id, Method: "read." + map[bool]string{true: "created", false: "reuse"}[created], URL: u, Body: frame, Status: 200, LatUS: lat, RespLen: len(out), Actor: actorOf(r, "")})
		c.witnessHeaders(w, id, "channel", int64(lat/1000))
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error(), "context": ctx})
			return
		}
		var ev struct {
			Result struct {
				Result struct {
					Value string `json:"value"`
				} `json:"result"`
			} `json:"result"`
		}
		var doc struct{ Title, Text string }
		if json.Unmarshal(out, &ev) == nil {
			_ = json.Unmarshal([]byte(ev.Result.Result.Value), &doc)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"context": ctx, "session": b.id, "url": curURL, "created": created, "title": doc.Title, "text": doc.Text})
	}
	for i := range c.brokers {
		b := &c.brokers[i]
		out, err := c.command(b, `{"method":"browsingContext.getTree","params":{}}`)
		if err != nil {
			continue
		}
		var tr struct {
			Result struct {
				Contexts []struct {
					Context string `json:"context"`
					URL     string `json:"url"`
				} `json:"contexts"`
			} `json:"result"`
		}
		if json.Unmarshal(out, &tr) != nil {
			continue
		}
		for _, t := range tr.Result.Contexts {
			if normTabURL(t.URL) == want {
				read(b, t.Context, t.URL, false)
				return
			}
		}
	}
	if r.URL.Query().Get("create") != "1" {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "not open in any driven session", "hint": "pass &create=1 to spawn DELIBERATELY — creation is intent, never a reflex (#643); a parent-only tab can still be seen via /drawshot?needle="})
		return
	}
	b := c.find("fox")
	if b == nil {
		http.Error(w, `{"error":"no fox session to create in"}`, http.StatusServiceUnavailable)
		return
	}
	cr, err := c.command(b, `{"method":"browsingContext.create","params":{"type":"tab"}}`)
	if err != nil {
		http.Error(w, `{"error":"create: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	var cv struct {
		Result struct {
			Context string `json:"context"`
		} `json:"result"`
	}
	if json.Unmarshal(cr, &cv) != nil || cv.Result.Context == "" {
		http.Error(w, `{"error":"create gave no context"}`, http.StatusBadGateway)
		return
	}
	c.command(b, fmt.Sprintf(`{"method":"browsingContext.navigate","params":{"context":%q,"url":%q,"wait":"complete"}}`, cv.Result.Context, u))
	read(b, cv.Result.Context, u, true)
}
