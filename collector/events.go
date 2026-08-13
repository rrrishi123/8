package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type xEvent struct {
	TS    string `json:"ts"`
	Frame string `json:"frame"`
}

// handleTimeline — #13: the interleaved cross-substrate timeline (tmux · nvim ·
// daemons · browser, one line). GET /timeline[?n=100&since=HH:MM:SS]. Two
// captures compared client-side = the diff of "what changed between runs."
func (c *collector) handleTimeline(w http.ResponseWriter, r *http.Request) {
	c.xmu.Lock()
	out := make([]xEvent, len(c.xtimeline))
	copy(out, c.xtimeline)
	c.xmu.Unlock()
	if since := r.URL.Query().Get("since"); since != "" {
		f := out[:0]
		for _, e := range out {
			if e.TS >= since {
				f = append(f, e)
			}
		}
		out = f
	}
	// distinct substrates present — the "cross" in cross-substrate, proven
	kinds := map[string]int{}
	for _, e := range out {
		for _, k := range []string{"tmux", "nvim", "daemons", "tab"} {
			if strings.Contains(e.Frame, `"`+k+`.changed"`) {
				kinds[k]++
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"timeline": out, "substrates": kinds, "count": len(out)})
}

// handleWitnessed (#70 FIX #24) is the sink that turns cmd/wire (the transparent
// MITM proxy) into a real 8 witness: the proxy POSTs each call it OBSERVED (it
// already forwarded it — this only RECORDS it), and 8 folds it into the ledger/
// provenance/timeline exactly like a /fetch, but WITHOUT re-executing. So a
// MITM'd Selenium/Appium smoke becomes a witnessed replayable record. Credentials
// in the URL (user:pass@host — LambdaTest's key) are REDACTED before recording:
// the witness must never persist the secret it sees in transit (the I1 spirit).
func (c *collector) handleWitnessed(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Physics string  `json:"physics"` // call | channel
		Method  string  `json:"method"`
		URL     string  `json:"url"`
		Status  int     `json:"status"`
		LatUS   float64 `json:"latency_us"`
		RespLen int     `json:"resp_bytes"`
		Session string  `json:"session"`
		Actor   string  `json:"actor"`
	}
	if json.NewDecoder(r.Body).Decode(&in) != nil || in.URL == "" {
		http.Error(w, `{"error":"need {url,...}"}`, http.StatusBadRequest)
		return
	}
	// REDACT credentials: strip user:pass@ from the URL so the key LambdaTest puts
	// in the hub URL is never persisted in 8's ledger.
	redacted := in.URL
	if u, err := url.Parse(in.URL); err == nil && u.User != nil {
		u.User = url.User("REDACTED")
		redacted = u.String()
	}
	phys := in.Physics
	if phys == "" {
		phys = "call"
	}
	id := c.record(reqRec{TS: nowNano(), Physics: phys, Session: in.Session, Method: in.Method,
		URL: redacted, Status: in.Status, LatUS: in.LatUS, RespLen: in.RespLen,
		Actor: actorOf(r, in.Actor)})
	c.publish(fmt.Sprintf(`{"session":%q,"physics":%q,"origin":"COLLECTOR","frame":{"method":"witnessed","params":{"route":%q,"ledger_id":%d,"status":%d,"mitm":true}}}`,
		firstNonEmpty(in.Session, "wire"), phys, in.Method+" "+redacted, id, in.Status))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"witnessed":true,"ledger_id":%d}`, id)
}
