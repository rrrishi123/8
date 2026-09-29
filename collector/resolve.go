package main

import (
	"encoding/json"
	"net/http"
	"os"
	"sort"
	"strings"
)

// ── T1 · /resolve — the ONE shared address ───────────────────────────────────
// A federation-wide lookup so operator-referent == mind-referent: never a private
// BiDi getTree (blind to hand-opened tabs — the bug that "lost" the cowork tab),
// always the reconciled witness. Address grammar host/browser/tab, unioned from
// /manifest (tabs), /nodes (browsers + their drive endpoints) and /peers (hosts +
// each peer's own manifest). Every tab carries opened_by/why, so an un-witnessed
// tab is flagged, not silently drivable (T12: only the inscribed is actionable).
// Read-only; GET /resolve[?q=<fragment>]. The default shape (built per the
// conductor's threshold while the format bless is pending) — steerable live.
type resolveEntry struct {
	Address  string `json:"address"` // host | host/browser | host/browser/tab
	Host     string `json:"host"`
	Browser  string `json:"browser,omitempty"` // session/seat
	Tab      string `json:"tab,omitempty"`     // ctx/uid
	URL      string `json:"url,omitempty"`
	Drive    string `json:"drive,omitempty"` // cdp_url / broker upstream / peer collector — where to ACT
	Status   string `json:"status,omitempty"`
	OpenedBy string `json:"opened_by,omitempty"`
	Why      string `json:"why,omitempty"`
	Kind     string `json:"kind"`   // host | browser | tab
	Source   string `json:"source"` // manifest | nodes | peers | peer-manifest
}

func (c *collector) handleResolve(w http.ResponseWriter, r *http.Request) {
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	localHost := firstNonEmpty(os.Getenv("PEER_HOST"), "local")
	var out []resolveEntry

	// 1) local browsers (nodes) — and their drive endpoints, indexed by seat
	nodeDrive := map[string]string{}
	if nj := c.nodesJoined(); nj != nil {
		if raw, ok := nj["nodes"]; ok {
			b, _ := json.Marshal(raw)
			var nodes []browserNode
			_ = json.Unmarshal(b, &nodes)
			for _, n := range nodes {
				drive := firstNonEmpty(strv(n.CDPURL), n.Upstream)
				if n.Seat != "" {
					nodeDrive[n.Seat] = drive
				}
				out = append(out, resolveEntry{
					Address: localHost + "/" + firstNonEmpty(n.Seat, n.ID),
					Host:    localHost, Browser: firstNonEmpty(n.Seat, n.ID),
					Drive: drive, Kind: "browser", Source: "nodes",
				})
			}
		}
	}

	// 2) local tabs (manifest)
	c.tmu.Lock()
	tabs := make([]tabRec, 0, len(c.manifest))
	for _, rec := range c.manifest {
		tabs = append(tabs, *rec)
	}
	c.tmu.Unlock()
	for _, t := range tabs {
		if t.Status != "live" {
			continue
		}
		out = append(out, resolveEntry{
			Address: localHost + "/" + t.Session + "/" + t.Ctx,
			Host:    localHost, Browser: t.Session, Tab: t.Ctx, URL: t.URL,
			Drive: nodeDrive[t.Session], Status: t.Status, OpenedBy: t.OpenedBy, Why: t.Why,
			Kind: "tab", Source: "manifest",
		})
	}

	// 3) peers (hosts) + each peer's own manifest tabs
	peerMu.Lock()
	plist := make([]*peer, 0, len(peers))
	for _, p := range peers {
		plist = append(plist, p)
	}
	peerMu.Unlock()
	for _, p := range plist {
		out = append(out, resolveEntry{Address: p.Host, Host: p.Host, Drive: p.Addr, Kind: "host", Source: "peers"})
		if len(p.Manifest) == 0 {
			continue
		}
		var pm struct {
			Tabs []tabRec `json:"tabs"`
		}
		if json.Unmarshal(p.Manifest, &pm) != nil {
			continue
		}
		for _, t := range pm.Tabs {
			if t.Status != "live" {
				continue
			}
			out = append(out, resolveEntry{
				Address: p.Host + "/" + t.Session + "/" + t.Ctx,
				Host:    p.Host, Browser: t.Session, Tab: t.Ctx, URL: t.URL,
				Status: t.Status, OpenedBy: t.OpenedBy, Why: t.Why,
				Kind: "tab", Source: "peer-manifest",
			})
		}
	}

	// ?q= — substring match across the address, url, host, browser, tab
	if q != "" {
		f := make([]resolveEntry, 0, len(out))
		for _, e := range out {
			if strings.Contains(strings.ToLower(e.Address+" "+e.URL+" "+e.Host+" "+e.Browser+" "+e.Tab), q) {
				f = append(f, e)
			}
		}
		out = f
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Address < out[j].Address })
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"n": len(out), "q": q, "resolved": out,
		"note": "host/browser/tab — union of /manifest+/nodes+/peers; drive = cdp_url/upstream or peer collector; opened_by/why flag un-witnessed tabs (T12).",
	})
}
