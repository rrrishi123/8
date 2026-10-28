package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// tabcostClient bounds every probe call so ONE unresponsive renderer can't wedge
// the whole request (the collector's own c.command has NO timeout by design — the
// SSE pump needs that — so the cost probe must not borrow it).
var tabcostClient = &http.Client{Timeout: 2 * time.Second}

func tabcostCmd(base, body string) ([]byte, error) {
	resp, err := tabcostClient.Post(base+"/command", "application/json", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// TabCost is the engine-AGNOSTIC per-tab resource STATE (#4421/#4427). Operator's
// principle: "nothing is a static mapping — it is state-managed; if something is
// Firefox-only, what it CAPTURES is the real state, and Firefox/Chrome/any other
// is that state's BEHAVIOUR, which can be added to." So the canonical thing is the
// cost state; each engine is a capture behaviour that fills what IT can measure and
// DECLARES it in Measured. A consumer never assumes a field is present.
//
// Firefox reports true per-tab RSS (ChromeUtils.requestProcInfo, see /procinfo).
// Chrome exposes NO per-tab RSS over CDP, only JS heap + DOM counts
// (Performance.getMetrics) — so for chromium HeapBytes is real and RSSBytes is
// absent, and Derived is true. Honest, not dressed up as RSS.
type TabCost struct {
	Ctx       string   `json:"ctx"`
	URL       string   `json:"url,omitempty"`
	Title     string   `json:"title,omitempty"`
	Engine    string   `json:"engine"`   // "firefox" | "chromium" | …
	Measured  []string `json:"measured"` // the fields this probe actually read, e.g. ["rss"] or ["js_heap","dom_nodes","listeners"]
	RSSBytes  int64    `json:"rss_bytes,omitempty"`
	HeapBytes int64    `json:"heap_bytes,omitempty"`
	Nodes     int64    `json:"dom_nodes,omitempty"`
	Listeners int64    `json:"listeners,omitempty"`
	Derived   bool     `json:"derived"` // true when the magnitude is a proxy (heap), not full RSS
	Source    string   `json:"source"`  // the probe behaviour that produced this row
}

// weight is the single scalar the ranking sorts on, using whichever real number
// the engine's behaviour gave us: RSS when present (Firefox), else JS heap (Chrome).
func (t TabCost) weight() int64 {
	if t.RSSBytes > 0 {
		return t.RSSBytes
	}
	return t.HeapBytes
}

// costProbe is the BEHAVIOUR layer: one implementation per engine, SELECTED by live
// state (which broker exists), never a hardcoded assumption. Adding a new engine is
// adding a costProbe — not a new branch in the handler.
type costProbe interface {
	engine() string
	capture(c *collector) ([]TabCost, error)
}

// chromeProbe captures per-tab cost over CDP: list page targets, then per target
// attach → Performance.enable → getMetrics → detach. JS heap + DOM counts only.
type chromeProbe struct{ brokerID string }

func (chromeProbe) engine() string { return "chromium" }

func (p chromeProbe) capture(c *collector) ([]TabCost, error) {
	b := c.find(p.brokerID)
	if b == nil {
		return nil, fmt.Errorf("no %q broker", p.brokerID)
	}
	raw, err := tabcostCmd(b.base, `{"method":"Target.getTargets","params":{}}`)
	if err != nil {
		return nil, err
	}
	var tg struct {
		Result struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
				URL      string `json:"url"`
				Title    string `json:"title"`
			} `json:"targetInfos"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &tg); err != nil {
		return nil, fmt.Errorf("getTargets: %w", err)
	}
	// LEAN probe: cap the targets and bound the whole pass with a deadline, returning
	// PARTIAL results rather than hang — measuring cost must itself be cheap. Combined
	// with tabcostClient's 2s per-call timeout, one dead renderer costs 2s, not forever.
	const maxTargets = 40
	deadline := time.Now().Add(8 * time.Second)
	out := make([]TabCost, 0, len(tg.Result.TargetInfos))
	probed := 0
	for _, t := range tg.Result.TargetInfos {
		if t.Type != "page" {
			continue
		}
		if probed >= maxTargets || time.Now().After(deadline) {
			break
		}
		probed++
		tc := TabCost{
			Ctx: t.TargetID, URL: tabcostClip(t.URL, 120), Title: tabcostClip(t.Title, 80),
			Engine: "chromium", Derived: true, Source: "cdp:Performance.getMetrics",
			Measured: []string{"js_heap", "dom_nodes", "listeners"},
		}
		ar, err := tabcostCmd(b.base, fmt.Sprintf(`{"method":"Target.attachToTarget","params":{"targetId":%q,"flatten":true}}`, t.TargetID))
		if err != nil {
			out = append(out, tc) // record the tab even if its probe timed out
			continue
		}
		var at struct {
			Result struct {
				SessionID string `json:"sessionId"`
			} `json:"result"`
		}
		if json.Unmarshal(ar, &at) != nil || at.Result.SessionID == "" {
			out = append(out, tc)
			continue
		}
		sid := at.Result.SessionID
		_, _ = tabcostCmd(b.base, fmt.Sprintf(`{"sessionId":%q,"method":"Performance.enable","params":{}}`, sid))
		mr, _ := tabcostCmd(b.base, fmt.Sprintf(`{"sessionId":%q,"method":"Performance.getMetrics","params":{}}`, sid))
		var mm struct {
			Result struct {
				Metrics []struct {
					Name  string  `json:"name"`
					Value float64 `json:"value"`
				} `json:"metrics"`
			} `json:"result"`
		}
		if json.Unmarshal(mr, &mm) == nil {
			for _, m := range mm.Result.Metrics {
				switch m.Name {
				case "JSHeapUsedSize":
					tc.HeapBytes = int64(m.Value)
				case "Nodes":
					tc.Nodes = int64(m.Value)
				case "JSEventListeners":
					tc.Listeners = int64(m.Value)
				}
			}
		}
		_, _ = tabcostCmd(b.base, fmt.Sprintf(`{"method":"Target.detachFromTarget","params":{"sessionId":%q}}`, sid))
		out = append(out, tc)
	}
	return out, nil
}

// handleTabCost is the engine-agnostic resource view. It SELECTS a probe from live
// state (a chrome broker ⇒ chromeProbe) and returns the ranked canonical TabCost.
// Firefox RSS stays at /procinfo (requestProcInfo); this is additive — a new engine
// is a new costProbe, not a new mapping here.
func (c *collector) handleTabCost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	var probe costProbe
	for _, id := range []string{"chrome"} {
		if c.find(id) != nil {
			probe = chromeProbe{brokerID: id}
			break
		}
	}
	if probe == nil {
		http.Error(w, `{"error":"no live engine probe (chrome broker absent); Firefox RSS is at /procinfo"}`, http.StatusServiceUnavailable)
		return
	}
	tabs, err := probe.capture(c)
	if err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), http.StatusBadGateway)
		return
	}
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].weight() > tabs[j].weight() })
	var heaviest *TabCost
	if len(tabs) > 0 {
		heaviest = &tabs[0]
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"engine":   probe.engine(),
		"count":    len(tabs),
		"heaviest": heaviest,
		"tabs":     tabs,
		"note":     "state-managed per-tab cost (#4421): engine is a behaviour that fills the canonical state. chromium = js_heap+dom (derived; CDP exposes no per-tab RSS). firefox RSS = /procinfo. A new engine is a new costProbe, not a branch.",
	})
}

func tabcostClip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
