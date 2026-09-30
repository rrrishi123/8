package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ── EFFERENT EVENTS (T5) — the witnessed reply-event bus ─────────────────────
// A per-tab CDP watcher (see cdpwatch) — or any host — POSTs here when something
// completes on a tab: a reply settled, the stop-affordance vanished, the network
// went idle. The event is WITNESSED (published to the feed, seen on X-8-Witness
// like any call) and kept in a bounded ring that any host can query or long-poll
// with ?since=. This generalizes the one-shot cowork-relay into a standing
// signal: the mesh learns WHEN a tab answered, not merely that it was asked.
// (Distinct from events.go's cross-substrate xEvent/timeline — this is the
// efferent reply-signal, keyed by tab identity.)

type replyEvent struct {
	ID     int64  `json:"id"`
	At     string `json:"at"`
	Host   string `json:"host,omitempty"`
	Tab    string `json:"tab,omitempty"` // tab identity: a ctx uuid, a url, or host/browser/tab
	Kind   string `json:"kind"`          // reply-complete | network-idle | stop-gone | …
	Detail string `json:"detail,omitempty"`
	By     string `json:"by,omitempty"`
}

const evRingMax = 512

var (
	evMu   sync.Mutex
	evRing = make([]replyEvent, 0, evRingMax)
	evSeq  int64
)

// recordReplyEvent appends a witnessed event to the ring and publishes it to the
// feed. Returns the assigned id and the ring depth. Shared by the HTTP path and
// any in-process watcher so both witness the same way.
func (c *collector) recordReplyEvent(e replyEvent) (int64, int) {
	evMu.Lock()
	evSeq++
	e.ID = evSeq
	e.At = time.Now().UTC().Format(time.RFC3339)
	evRing = append(evRing, e)
	if len(evRing) > evRingMax {
		evRing = evRing[len(evRing)-evRingMax:]
	}
	n := len(evRing)
	evMu.Unlock()
	c.publish(fmt.Sprintf(`{"session":"event","origin":"COLLECTOR","frame":{"method":"tab.%s","params":{"id":%d,"tab":%q,"host":%q,"detail":%q,"by":%q}}}`,
		e.Kind, e.ID, e.Tab, e.Host, e.Detail, e.By))
	return e.ID, n
}

// handleEvent — POST /event {kind, tab?, host?, detail?, by?} records a witnessed
// event; GET /event?tab=&kind=&since=&limit= returns the matching recent events
// (newest last), so a host can poll from its last seen id.
func (c *collector) handleEvent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodPost:
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		var e replyEvent
		if json.Unmarshal(body, &e) != nil || e.Kind == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "need {kind, tab?, host?, detail?}; kind is required"})
			return
		}
		if e.By == "" {
			e.By = r.Header.Get("X-8-Actor")
		}
		id, n := c.recordReplyEvent(e)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "id": id, "n": n})
	case http.MethodGet:
		tab, kind := r.URL.Query().Get("tab"), r.URL.Query().Get("kind")
		since, _ := strconv.ParseInt(r.URL.Query().Get("since"), 10, 64)
		limit := 50
		if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 && v <= evRingMax {
			limit = v
		}
		evMu.Lock()
		out := make([]replyEvent, 0, limit)
		for i := len(evRing) - 1; i >= 0 && len(out) < limit; i-- {
			e := evRing[i]
			if since > 0 && e.ID <= since {
				break // the ring is id-ordered; nothing older can match a since-cursor
			}
			if (tab == "" || e.Tab == tab) && (kind == "" || e.Kind == kind) {
				out = append(out, e)
			}
		}
		seq := evSeq
		evMu.Unlock()
		for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // newest-last (chronological)
			out[i], out[j] = out[j], out[i]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"events": out, "n": len(out), "seq": seq})
	default:
		http.Error(w, "GET or POST", 405)
	}
}
