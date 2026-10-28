package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// ── CROSS-HOST RELAY (#1267 / #890) — commands flow to a peer the ONLY way the
// networking allows: the peer PULLS them. A NAT'd / sandboxed peer (colima behind
// slirp, a claude-web sandbox) can reach OUT but can NEVER be reached — the hub
// cannot push a /panes/send or /act to it (proven: colima's beat arrives from
// 127.0.0.1, so the hub can derive no reachable address). So the hub QUEUES a command
// for peer X; on X's next heartbeat it DRAINS its queue in the response, X executes
// each command against its OWN collector (loopback), and carries the results back on
// the following beat. Witnessed, egress-only, and needs no inbound route — the
// primitive the whole federation arc (#1232/#1233/#1136) stands on.

type relayCmd struct {
	ID     string          `json:"id"`             // unique, so a result maps back to its command
	Method string          `json:"method"`         // "get" | "post" — how the peer runs it locally
	Path   string          `json:"path"`           // the peer-local collector path, e.g. "/health", "/panes/send"
	Body   json.RawMessage `json:"body,omitempty"` // POST body (for method "post")
	By     string          `json:"by,omitempty"`   // who issued it (witness)
	At     string          `json:"at"`
}

type relayResult struct {
	ID    string          `json:"id"`
	OK    bool            `json:"ok"`
	Code  int             `json:"code,omitempty"`
	Body  json.RawMessage `json:"body,omitempty"`
	Error string          `json:"error,omitempty"`
	At    string          `json:"at"`
}

var (
	relayMu      sync.Mutex
	relayPending = map[string][]relayCmd{}    // peer host -> queued commands (hub side)
	relayResults = map[string][]relayResult{} // peer host -> results it carried back
	relaySeq     int64
)

func newRelayID() string {
	return fmt.Sprintf("r%d-%d", time.Now().UnixNano(), atomic.AddInt64(&relaySeq, 1))
}

// relayEnqueue queues a command for a peer to pull on its next beat.
func relayEnqueue(host string, cmd relayCmd) {
	relayMu.Lock()
	relayPending[host] = append(relayPending[host], cmd)
	relayMu.Unlock()
}

// relayDrain removes and returns a peer's queued commands — called when the peer beats.
func relayDrain(host string) []relayCmd {
	relayMu.Lock()
	defer relayMu.Unlock()
	cmds := relayPending[host]
	delete(relayPending, host)
	return cmds
}

// relayRecordResults stores the results a peer carried back, bounded to the most
// recent 100 per host so a long-lived hub never grows unbounded.
func relayRecordResults(host string, res []relayResult) {
	if len(res) == 0 {
		return
	}
	relayMu.Lock()
	relayResults[host] = append(relayResults[host], res...)
	if n := len(relayResults[host]); n > 100 {
		relayResults[host] = relayResults[host][n-100:]
	}
	relayMu.Unlock()
}

// handleRelay — POST /relay?peer=X {method, path, body?} queues a command for peer X to
// PULL on its next heartbeat; GET /relay?peer=X returns the results X has carried back
// (and how many commands are still pending).
func (c *collector) handleRelay(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	host := r.URL.Query().Get("peer")
	if host == "" {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "need ?peer=<host>"})
		return
	}
	if r.Method == http.MethodPost {
		var cmd relayCmd
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if json.Unmarshal(body, &cmd) != nil || cmd.Method == "" || cmd.Path == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": `need {"method":"get|post","path":"/...","body"?:{}}`})
			return
		}
		// only queue for a peer we actually know (a live claim) — never a black hole.
		peerMu.Lock()
		_, known := peers[host]
		peerMu.Unlock()
		if !known {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": "no such peer in the roster", "peer": host})
			return
		}
		cmd.ID = newRelayID()
		cmd.By = actorOf(r, "")
		cmd.At = time.Now().UTC().Format(time.RFC3339)
		relayEnqueue(host, cmd)
		c.publish(`{"session":"peers","origin":"COLLECTOR","frame":{"method":"relay.queued","params":{"peer":` + jsonStr(host) + `,"id":` + jsonStr(cmd.ID) + `}}}`)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "queued": cmd.ID, "peer": host})
		return
	}
	relayMu.Lock()
	res := append([]relayResult(nil), relayResults[host]...)
	pending := len(relayPending[host])
	relayMu.Unlock()
	_ = json.NewEncoder(w).Encode(map[string]any{"peer": host, "results": res, "pending": pending})
}

// relayExecute (PEER side) runs one pulled command against this collector's OWN
// loopback and returns the result. selfBase is the peer's own collector base URL
// (http://127.0.0.1:<its listen port>). A peer only ever drives ITSELF this way —
// the hub never reaches in.
func relayExecute(selfBase string, client *http.Client, cmd relayCmd) relayResult {
	res := relayResult{ID: cmd.ID, At: time.Now().UTC().Format(time.RFC3339)}
	url := selfBase + cmd.Path
	var resp *http.Response
	var err error
	switch cmd.Method {
	case "get":
		resp, err = client.Get(url)
	case "post":
		resp, err = client.Post(url, "application/json", bytes.NewReader(cmd.Body))
	default:
		res.Error = "unknown method: " + cmd.Method
		return res
	}
	if err != nil {
		res.Error = err.Error()
		return res
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 256<<10))
	res.Code = resp.StatusCode
	res.OK = resp.StatusCode >= 200 && resp.StatusCode < 300
	if json.Valid(b) {
		res.Body = json.RawMessage(b)
	} else {
		res.Body, _ = json.Marshal(string(b))
	}
	return res
}
