// collector — the capture service for the wire's cockpit.
//
// It fans in N held brokers (http-mcp/cmd/channel), each owning one session's
// socket, into a single live feed, and routes commands back out. It owns no
// protocol logic and imports no http-mcp code — only HTTP/SSE to the brokers.
//
//	GET  /feed             (SSE)  every broker event + echoes of outbound calls
//	                              (/fetch, /run), merged and tagged with origin
//	POST /run?session=ID          proxy {method,params} to that session's broker
//	POST /broadcast               send one {method,params} to every session
//	POST /fetch                   execute one raw HTTP request (the "curl hit")
//	GET  /health                  brokers known + liveness
//
// One pump per broker holds that broker's /events open and publishes each event
// into an in-process hub; every /feed subscriber reads from the hub. Outbound
// calls (/fetch, /run) also publish an echo into the hub, so the cockpit sees
// the efferent ("call") side without a witness proxy. Frames are tagged
// origin=BIDI (from the browser) or origin=COLLECTOR (our own echoes).
//
// The collector is stateless about sessions: it owns none, it only routes.
// stdlib only.
//
// Usage:
//
//	collector -listen :7070 -brokers "fox=http://127.0.0.1:4445"
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// broker is one held http-mcp socket, owning a single session.
type broker struct {
	id   string
	base string
}

type collector struct {
	brokers []broker
	client  *http.Client
	gecko   string // geckodriver session base (http://host:port/session/<id>) — enables /procinfo

	mu      sync.Mutex
	subs    map[int]chan string
	nextSub int64

	fmu    sync.Mutex
	frames map[string]chan []byte // sessionID -> latest CDP screencast JPEG (latest-wins)

	lmu    sync.Mutex
	ledger []reqRec // full request/response records — 8's witness ledger (replayable)
	lseq   int64

	bmu     sync.Mutex
	benches []benchRec // benchmark batch results — clubbed + filterable on 8
	bseq    int64

	rmu     sync.Mutex
	recOn   bool      // a recording is in progress
	recName string    // name of the active recording
	recSeat string    // seat the recorded control is attributed to (operator|pilot|adapter|ai)
	recBuf  []reqRec  // frames captured this recording → saved as a replayable series
}

func newCollector(brokers []broker) *collector {
	return &collector{brokers: brokers, client: &http.Client{}, subs: map[int]chan string{}, frames: map[string]chan []byte{}}
}

// procInfoScript reads per-tab memory + CPU from Firefox via ChromeUtils
// .requestProcInfo (only reachable from the chrome context). One row PER TAB
// (per window). mem/cpu are per content-process: Firefox exposes no window-level
// memory/CPU, so when tabs of the same site share a process the figure is the
// shared total — coresident_tabs flags that (exact==true only when a tab is alone
// in its process). gpu is one process shared across ALL tabs (no per-tab GPU
// exists in Firefox); it is reported separately, never attributed to a tab.
const procInfoScript = `const cb=arguments[arguments.length-1];ChromeUtils.requestProcInfo().then(i=>{const skip=/^(about|chrome|moz-extension|resource|blob):/;const share={};for(const c of i.children){const w=(c.windows||[]).filter(x=>x.documentURI&&x.documentURI.spec&&!skip.test(x.documentURI.spec));share[c.pid]=(share[c.pid]||0)+w.length;}const tabs=[];let gpu=null;for(const c of i.children){if(c.type==="gpu")gpu={pid:c.pid,mem_mb:Math.round(c.memory/1048576),cpu_ms:Math.round(c.cpuTime/1e6)};const w=(c.windows||[]).filter(x=>x.documentURI&&x.documentURI.spec&&!skip.test(x.documentURI.spec));for(const x of w){tabs.push({title:(x.documentTitle||"").slice(0,60),url:x.documentURI.spec.slice(0,90),outerWindowId:x.outerWindowId,pid:c.pid,proc_type:c.type,mem_mb:Math.round(c.memory/1048576),cpu_ms:Math.round(c.cpuTime/1e6),coresident_tabs:share[c.pid],exact:share[c.pid]===1});}}cb(JSON.stringify({tabs:tabs.sort((a,b)=>b.mem_mb-a.mem_mb),gpu:gpu,parent_mem_mb:Math.round(i.memory/1048576),note:"mem_mb is MB; cpu_ms is cumulative CPU time (the cockpit derives a live % from deltas). Exact per-tab when coresident_tabs==1, else the shared process total. gpu is one shared process — no per-tab GPU in Firefox."}));}).catch(e=>cb("ERR:"+e));`

// handleProcInfo is the WITNESS's resource lens: per-tab memory + CPU for every
// tab, read straight from Firefox's chrome context. Read-only — it never drives
// a target. Requires the session launched with -remote-allow-system-access (see
// scripts/up.sh) and the collector started with -gecko.
func (c *collector) handleProcInfo(w http.ResponseWriter, r *http.Request) {
	if c.gecko == "" {
		http.Error(w, `{"error":"procinfo disabled: collector started without -gecko"}`, http.StatusServiceUnavailable)
		return
	}
	post := func(path, body string) ([]byte, error) {
		req, _ := http.NewRequest("POST", c.gecko+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}
	if _, err := post("/moz/context", `{"context":"chrome"}`); err != nil {
		http.Error(w, `{"error":"chrome context: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer post("/moz/context", `{"context":"content"}`) // always return the session to content
	body, _ := json.Marshal(map[string]any{"script": procInfoScript, "args": []any{}})
	out, err := post("/execute/async", string(body))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	var v struct {
		Value json.RawMessage `json:"value"`
	}
	json.Unmarshal(out, &v)
	var inner string // requestProcInfo's callback hands back a JSON string
	if json.Unmarshal(v.Value, &inner) == nil {
		if strings.HasPrefix(inner, "ERR:") {
			http.Error(w, `{"error":"requestProcInfo: `+strings.TrimPrefix(inner, "ERR:")+`"}`, http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(inner))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(v.Value)
}

func (c *collector) find(id string) *broker {
	for i := range c.brokers {
		if c.brokers[i].id == id {
			return &c.brokers[i]
		}
	}
	return nil
}

// frameChan is a session's latest-screencast-frame channel (buffered 1).
func (c *collector) frameChan(id string) chan []byte {
	c.fmu.Lock()
	defer c.fmu.Unlock()
	ch, ok := c.frames[id]
	if !ok {
		ch = make(chan []byte, 1)
		c.frames[id] = ch
	}
	return ch
}

// publish fans one frame to every /feed subscriber, non-blocking so a slow
// consumer never stalls the wire.
func (c *collector) publish(frame string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.subs {
		select {
		case ch <- frame:
		default:
		}
	}
}

func (c *collector) subscribe() (int, chan string) {
	id := int(atomic.AddInt64(&c.nextSub, 1))
	ch := make(chan string, 256)
	c.mu.Lock()
	c.subs[id] = ch
	c.mu.Unlock()
	return id, ch
}

func (c *collector) unsubscribe(id int) {
	c.mu.Lock()
	delete(c.subs, id)
	c.mu.Unlock()
}

// pump holds one broker's /events stream open and publishes each event into the
// hub, tagged BIDI. Reconnects if the stream drops or the broker is down.
func (c *collector) pump(ctx context.Context, b broker) {
	for ctx.Err() == nil {
		req, _ := http.NewRequestWithContext(ctx, "GET", b.base+"/events", nil)
		resp, err := c.client.Do(req)
		if err != nil {
			c.publish(fmt.Sprintf(`{"session":%q,"origin":"COLLECTOR","frame":{"method":"collector.broker_down","params":{"error":%q}}}`, b.id, err.Error()))
			time.Sleep(2 * time.Second)
			continue
		}
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
		for sc.Scan() {
			if line := sc.Text(); strings.HasPrefix(line, "data: ") {
				payload := strings.TrimPrefix(line, "data: ")
				// CDP screencast frames are a firehose -> route to the session's
				// /stream, NOT the feed (which they would drown). Decode + ack here.
				if strings.Contains(payload, `"Page.screencastFrame"`) {
					var ev struct {
						Params struct {
							Data      string `json:"data"`
							SessionID int    `json:"sessionId"`
						} `json:"params"`
					}
					if json.Unmarshal([]byte(payload), &ev) == nil && ev.Params.Data != "" {
						if raw, derr := base64.StdEncoding.DecodeString(ev.Params.Data); derr == nil {
							ch := c.frameChan(b.id)
							select { // latest-wins: drop the stale frame, push the new
							case <-ch:
							default:
							}
							select {
							case ch <- raw:
							default:
							}
						}
						bb := b // ack so Chrome keeps streaming
						c.command(&bb, fmt.Sprintf(`{"method":"Page.screencastFrameAck","params":{"sessionId":%d}}`, ev.Params.SessionID))
					}
					continue
				}
				c.publish(fmt.Sprintf(`{"session":%q,"origin":"BIDI","frame":%s}`, b.id, payload))
			}
		}
		resp.Body.Close()
		time.Sleep(time.Second)
	}
}

func (c *collector) handleFeed(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	id, ch := c.subscribe()
	defer c.unsubscribe(id)
	fmt.Fprint(w, ": collector feed open\n\n")
	flusher.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case frame := <-ch:
			fmt.Fprintf(w, "data: %s\n\n", frame)
			flusher.Flush()
		}
	}
}

// handleRun proxies one command to a session's broker — and echoes the outbound
// command into the feed so the cockpit shows the efferent side.
func (c *collector) handleRun(w http.ResponseWriter, r *http.Request) {
	b := c.find(r.URL.Query().Get("session"))
	if b == nil {
		http.Error(w, `{"error":"unknown session"}`, http.StatusNotFound)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var probe struct {
		Method string `json:"method"`
	}
	_ = json.Unmarshal(body, &probe)

	start := time.Now()
	resp, err := c.client.Post(b.base+"/command", "application/json", bytes.NewReader(body))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	lat := float64(time.Since(start).Nanoseconds()) / 1000.0
	id := c.record(reqRec{TS: nowNano(), Physics: "channel", Session: b.id, Method: probe.Method, URL: b.base + "/command", Body: string(body), Status: resp.StatusCode, LatUS: lat, RespLen: len(rb), RespHead: preview(rb)})
	// echo into view-1 carrying the ledger id, so the row links to its full payload + replay
	c.publish(fmt.Sprintf(`{"session":%q,"physics":"channel","origin":"COLLECTOR","frame":{"method":%q,"params":{"ledger_id":%d,"status":%d,"latency_us":%.0f}}}`, b.id, probe.Method, id, resp.StatusCode, lat))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(rb)
}

// handleBroadcast sends one command to every session and returns per-session results.
func (c *collector) handleBroadcast(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	type result struct {
		Session string          `json:"session"`
		Status  int             `json:"status"`
		Body    json.RawMessage `json:"body,omitempty"`
		Error   string          `json:"error,omitempty"`
	}
	results := make([]result, len(c.brokers))
	var wg sync.WaitGroup
	for i, b := range c.brokers {
		wg.Add(1)
		go func(i int, b broker) {
			defer wg.Done()
			res := result{Session: b.id}
			resp, err := c.client.Post(b.base+"/command", "application/json", bytes.NewReader(body))
			if err != nil {
				res.Error = err.Error()
				results[i] = res
				return
			}
			defer resp.Body.Close()
			rb, _ := io.ReadAll(resp.Body)
			res.Status, res.Body = resp.StatusCode, json.RawMessage(rb)
			results[i] = res
		}(i, b)
	}
	wg.Wait()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"results": results})
}

// handleFetch executes one raw HTTP request server-side (the "curl hit"), and
// echoes it into the feed so the cockpit shows the call alongside browser events.
func (c *collector) handleFetch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.URL == "" {
		http.Error(w, `{"error":"url is required"}`, http.StatusBadRequest)
		return
	}
	if in.Method == "" {
		in.Method = "GET"
	}
	var body io.Reader
	if in.Body != "" {
		body = strings.NewReader(in.Body)
	}
	req, err := http.NewRequest(strings.ToUpper(in.Method), in.URL, body)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	for k, v := range in.Headers {
		req.Header.Set(k, v)
	}
	start := time.Now()
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		c.publish(fmt.Sprintf(`{"session":"wire","origin":"COLLECTOR","frame":{"method":"http_request","params":{"http_method":%q,"url":%q,"status":0,"error":%q}}}`, strings.ToUpper(in.Method), in.URL, err.Error()))
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	latency := time.Since(start).Milliseconds()
	id := c.record(reqRec{TS: nowNano(), Physics: "call", Method: strings.ToUpper(in.Method), URL: in.URL, Headers: in.Headers, Body: in.Body, Status: resp.StatusCode, LatUS: float64(latency) * 1000, RespLen: len(rb), RespHead: preview(rb)})
	c.publish(fmt.Sprintf(`{"session":"wire","physics":"call","origin":"COLLECTOR","frame":{"method":"http_request","params":{"http_method":%q,"url":%q,"ledger_id":%d,"status":%d,"latency_ms":%d}}}`, strings.ToUpper(in.Method), in.URL, id, resp.StatusCode, latency))

	// auto-register (option b): a POST .../session that returned a session id is
	// a new session — append it to the shared registry so it shows in 8's rail.
	bare := strings.SplitN(in.URL, "?", 2)[0]
	if strings.ToUpper(in.Method) == "POST" && strings.HasSuffix(bare, "/session") && resp.StatusCode == 200 {
		var sv struct {
			Value struct {
				SessionID string `json:"sessionId"`
			} `json:"value"`
		}
		if json.Unmarshal(rb, &sv) == nil && sv.Value.SessionID != "" {
			kind := "local"
			if strings.Contains(in.URL, "browserstack") || strings.Contains(in.URL, "lambdatest") || strings.Contains(in.URL, "saucelabs") {
				kind = "cloud"
			}
			// if the caps asked for an MJPEG server, the device streams MJPEG on
			// that localhost port — record it as the session's live stream source.
			stream := ""
			var cb struct {
				Capabilities struct {
					AlwaysMatch map[string]any `json:"alwaysMatch"`
				} `json:"capabilities"`
			}
			if json.Unmarshal([]byte(in.Body), &cb) == nil {
				if p, ok := cb.Capabilities.AlwaysMatch["appium:mjpegServerPort"]; ok {
					stream = fmt.Sprintf("http://127.0.0.1:%v", p)
				}
			}
			registerSession(sessionRec{ID: sv.Value.SessionID, Hub: strings.TrimSuffix(bare, "/session"), Kind: kind, Physics: "call", Stream: stream})
		}
	}

	hdr := map[string]string{}
	for k := range resp.Header {
		hdr[k] = resp.Header.Get(k)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"status":     resp.StatusCode,
		"latency_ms": latency,
		"headers":    hdr,
		"body":       string(rb),
	})
}

// command posts one raw command to a broker and returns the response body.
func (c *collector) command(b *broker, body string) ([]byte, error) {
	resp, err := c.client.Post(b.base+"/command", "application/json", strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}

// commandS is command + the HTTP status, so a caller can reject a broker error
// (a 502 broken-pipe, a BiDi error body) instead of timing it as a success.
func (c *collector) commandS(b *broker, body string) ([]byte, int, error) {
	resp, err := c.client.Post(b.base+"/command", "application/json", strings.NewReader(body))
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	return rb, resp.StatusCode, nil
}

// handleShot grabs one screenshot of a session's tab — the "viewport" frame.
// BiDi has no screencast stream, so the cockpit polls this (~1 fps is plenty
// for a live-ish mirror). Defaults to the session's first top-level context.
func (c *collector) handleShot(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session")

	// CALL session (WebDriver/Appium): see it through the wire's NATIVE
	// screenshot — GET /session/{id}/screenshot — not a bespoke capture.
	if rec := lookupSession(sid); rec != nil && rec.Physics == "call" {
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(rec.Hub + "/session/" + sid + "/screenshot")
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
		var sv struct {
			Value string `json:"value"`
		}
		json.Unmarshal(rb, &sv)
		w.Header().Set("Content-Type", "application/json")
		if path := r.URL.Query().Get("save"); path != "" {
			raw, _ := base64.StdEncoding.DecodeString(sv.Value)
			os.WriteFile(path, raw, 0o644)
			json.NewEncoder(w).Encode(map[string]any{"session": sid, "saved": path, "bytes": len(raw)})
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"session": sid, "data": "data:image/png;base64," + sv.Value})
		return
	}

	// CHANNEL session (BiDi): captureScreenshot on the held socket.
	b := c.find(sid)
	if b == nil {
		http.Error(w, `{"error":"unknown session"}`, http.StatusNotFound)
		return
	}
	ctx := r.URL.Query().Get("context")
	if ctx == "" {
		tr, err := c.command(b, `{"method":"browsingContext.getTree","params":{}}`)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		var t struct {
			Result struct {
				Contexts []struct {
					Context string `json:"context"`
				} `json:"contexts"`
			} `json:"result"`
		}
		json.Unmarshal(tr, &t)
		if len(t.Result.Contexts) > 0 {
			ctx = t.Result.Contexts[0].Context
		}
	}
	if ctx == "" {
		http.Error(w, `{"error":"no context"}`, http.StatusBadGateway)
		return
	}
	// JPEG q0.6 keeps a poll-able frame small (a full PNG is ~1.4MB; this is a
	// fraction of that) — good enough for a ~1fps live mirror.
	// origin "viewport" = only the visible area (NOT the full scrollable page —
	// a long page would balloon to tens of MB and choke the cockpit).
	sr, err := c.command(b, `{"method":"browsingContext.captureScreenshot","params":{"context":"`+ctx+`","origin":"viewport","format":{"type":"image/jpeg","quality":0.5}}}`)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	var s struct {
		Result struct {
			Data string `json:"data"`
		} `json:"result"`
	}
	json.Unmarshal(sr, &s)
	// ?save=/path writes the frame to disk (so a caller can inspect it without
	// pulling a half-MB data URL through its own context).
	if path := r.URL.Query().Get("save"); path != "" {
		raw, derr := base64.StdEncoding.DecodeString(s.Result.Data)
		if derr == nil {
			_ = os.WriteFile(path, raw, 0o644)
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"context": ctx, "saved": path, "bytes": len(raw)})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"context": ctx,
		"data":    "data:image/jpeg;base64," + s.Result.Data,
	})
}

// handleTabs lists a session's top-level tabs (context + url) so the cockpit
// can offer a picker for which one to mirror.
func (c *collector) handleTabs(w http.ResponseWriter, r *http.Request) {
	b := c.find(r.URL.Query().Get("session"))
	if b == nil {
		http.Error(w, `{"error":"unknown session"}`, http.StatusNotFound)
		return
	}
	tr, err := c.command(b, `{"method":"browsingContext.getTree","params":{}}`)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	var t struct {
		Result struct {
			Contexts []struct {
				Context string `json:"context"`
				URL     string `json:"url"`
			} `json:"contexts"`
		} `json:"result"`
	}
	json.Unmarshal(tr, &t)
	tabs := make([]map[string]string, 0, len(t.Result.Contexts))
	for _, ctx := range t.Result.Contexts {
		tabs = append(tabs, map[string]string{"context": ctx.Context, "url": ctx.URL})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tabs": tabs})
}

// the shared session registry — every http-mcp instance (or the collector
// itself, via /fetch) appends a record here on session create; 8 reads it so
// the rail shows every session across clients. NDJSON, append-only, atomic.
func sessionsFile() string { return os.ExpandEnv("$HOME/.8/sessions.json") }

var regMu sync.Mutex // serialize registry append vs compaction

// rewriteRegistry compacts the registry to exactly these call sessions — auto-
// removing disconnected tombstones instead of keeping them. Channel sessions come
// from c.brokers (not the file), so they are untouched.
func rewriteRegistry(calls []sessionRec) {
	regMu.Lock()
	defer regMu.Unlock()
	os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
	var b bytes.Buffer
	for _, s := range calls {
		s.Status = "" // computed at /sessions time, not persisted
		line, _ := json.Marshal(s)
		b.Write(append(line, '\n'))
	}
	os.WriteFile(sessionsFile(), b.Bytes(), 0o644)
}

type sessionRec struct {
	ID      string `json:"id"`
	Hub     string `json:"hub"`
	Kind    string `json:"kind"`    // local | cloud
	Physics string `json:"physics"` // call | channel
	Stream  string `json:"stream,omitempty"` // live MJPEG source URL, if any
	Status  string `json:"status,omitempty"` // live | disconnected (computed at /sessions time, not persisted)
	Created string `json:"created_at,omitempty"`
}

func registerSession(rec sessionRec) {
	regMu.Lock()
	defer regMu.Unlock()
	os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
	f, err := os.OpenFile(sessionsFile(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	if rec.Created == "" {
		rec.Created = time.Now().UTC().Format(time.RFC3339)
	}
	line, _ := json.Marshal(rec)
	f.Write(append(line, '\n')) // <4KB → atomic under O_APPEND
}

// lookupSession finds a registry record by id (latest wins).
func lookupSession(id string) *sessionRec {
	data, err := os.ReadFile(sessionsFile())
	if err != nil {
		return nil
	}
	var found *sessionRec
	for _, line := range strings.Split(string(data), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		var rec sessionRec
		if json.Unmarshal([]byte(line), &rec) == nil && rec.ID == id {
			r := rec
			found = &r
		}
	}
	return found
}

// handleSessions returns the live session list for 8's rail: the held brokers
// (always channel sessions) plus everything in the shared registry file.
func (c *collector) handleSessions(w http.ResponseWriter, r *http.Request) {
	byID := map[string]sessionRec{}
	for _, b := range c.brokers {
		rec := sessionRec{ID: b.id, Hub: b.base, Kind: "local", Physics: "channel"}
		if b.id != "fox" {
			rec.Stream = "cdp" // non-fox channel brokers are CDP screencast-capable
		}
		byID[b.id] = rec
	}
	if data, err := os.ReadFile(sessionsFile()); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if line = strings.TrimSpace(line); line == "" {
				continue
			}
			var rec sessionRec
			if json.Unmarshal([]byte(line), &rec) == nil && rec.ID != "" {
				byID[rec.ID] = rec // latest record wins
			}
		}
	}
	// liveness: probe each CALL session with a DEVICE-TOUCHING command
	// (/window/rect, NOT /timeouts — Appium answers /timeouts from the cached
	// session object without touching the device, so a disconnected device
	// passed liveness = ghost). A gone device -> proxy/connection error -> we
	// TOMBSTONE it (status "disconnected"), keeping it on the rail greyed so the
	// operator sees it died vs never-existed. Channel sessions are alive by the
	// broker holding their socket — they skip the probe.
	ch := make(chan sessionRec, len(byID))
	for _, rec := range byID {
		if rec.Physics != "call" {
			rec.Status = "live"
			ch <- rec
			continue
		}
		go func(rec sessionRec) {
			cl := &http.Client{Timeout: 1500 * time.Millisecond}
			resp, err := cl.Get(rec.Hub + "/session/" + rec.ID + "/window/rect")
			rec.Status = "disconnected" // proxy/connection error => device gone
			if err == nil {
				if resp.StatusCode == 200 {
					rec.Status = "live"
				}
				resp.Body.Close()
			}
			ch <- rec
		}(rec)
	}
	all := make([]sessionRec, 0, len(byID))
	for range byID {
		all = append(all, <-ch)
	}
	// auto-remove: a disconnected CALL session is gone — drop it from the response
	// AND compact it out of the registry so it never reappears.
	live := make([]sessionRec, 0, len(all))
	var liveCalls []sessionRec
	for _, s := range all {
		if s.Status == "disconnected" {
			continue
		}
		live = append(live, s)
		if s.Physics == "call" {
			liveCalls = append(liveCalls, s)
		}
	}
	rewriteRegistry(liveCalls)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"sessions": live})
}

// echoOut publishes one COLLECTOR-origin frame for a CONTROL call 8 itself made
// to a session, so the cockpit's per-session stream shows 8's own pokes — not
// just the browser/device side. One frame, after the response, carrying the
// outcome. Perception polls (/shot) are the stream, not control events, so they
// are deliberately NOT echoed (they would flood the feed).
func (c *collector) echoOut(session, physics, method string, params map[string]any, status int, ms int64) {
	if params == nil {
		params = map[string]any{}
	}
	params["status"] = status
	params["latency_ms"] = ms
	p, _ := json.Marshal(params)
	c.publish(fmt.Sprintf(`{"session":%q,"physics":%q,"origin":"COLLECTOR","frame":{"method":%q,"params":%s}}`, session, physics, method, p))
}

// handleSource returns a session's UI/DOM tree through the wire — call sessions
// via WebDriver GET /session/{id}/source (XML/HTML). BiDi DOM tree pending.
// chanContext resolves which tab to drive: the given context, or the first tab.
func (c *collector) chanContext(b *broker, given string) string {
	if given != "" {
		return given
	}
	tr, err := c.command(b, `{"method":"browsingContext.getTree","params":{}}`)
	if err != nil {
		return ""
	}
	var t struct {
		Result struct {
			Contexts []struct {
				Context string `json:"context"`
			} `json:"contexts"`
		} `json:"result"`
	}
	json.Unmarshal(tr, &t)
	if len(t.Result.Contexts) > 0 {
		return t.Result.Contexts[0].Context
	}
	return ""
}

// domWalkScript serializes the VISIBLE DOM of a tab into Appium-style hierarchy
// XML with bounds="[x1,y1][x2,y2]" in CSS px — the exact shape parseSource()
// already renders, so the inspector tree + overlay work for the browser with no
// frontend change. Root is the viewport (so screen coords map 1:1 to BiDi input).
const domWalkScript = `(()=>{const esc=s=>String(s||'').replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');let n=0;function walk(el,d){if(n>1500||d>16)return '';const r=el.getBoundingClientRect();if(r.width<1||r.height<1||r.bottom<0||r.top>innerHeight)return '';n++;const t=[...el.childNodes].filter(x=>x.nodeType===3).map(x=>x.textContent.trim()).join(' ').trim();let s='<node class="'+esc(el.tagName)+'" resource-id="'+esc(el.id)+'" content-desc="'+esc(el.getAttribute('aria-label'))+'" text="'+esc(t.slice(0,48))+'" bounds="['+Math.round(r.left)+','+Math.round(r.top)+']['+Math.round(r.right)+','+Math.round(r.bottom)+']">';for(const ch of el.children)s+=walk(ch,d+1);return s+'</node>';}let inner='';for(const ch of document.body.children)inner+=walk(ch,1);return '<hierarchy><node class="VIEWPORT" bounds="[0,0]['+Math.round(innerWidth)+','+Math.round(innerHeight)+']">'+inner+'</node></hierarchy>';})()`

// sourceChannel serves a browser tab's DOM as the same XML the call path returns.
func (c *collector) sourceChannel(w http.ResponseWriter, r *http.Request, b *broker) {
	ctx := c.chanContext(b, r.URL.Query().Get("context"))
	if ctx == "" {
		http.Error(w, `{"error":"no tab context"}`, http.StatusBadGateway)
		return
	}
	cmd := map[string]any{"method": "script.evaluate", "params": map[string]any{"expression": domWalkScript, "target": map[string]any{"context": ctx}, "awaitPromise": true}}
	cb, _ := json.Marshal(cmd)
	start := time.Now()
	tr, err := c.command(b, string(cb))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	var res struct {
		Result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	json.Unmarshal(tr, &res)
	c.echoOut(b.id, "channel", "source", map[string]any{"route": "script.evaluate dom-walk", "context": ctx, "bytes": len(res.Result.Result.Value)}, 200, time.Since(start).Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"value": res.Result.Result.Value})
}

// actChannel drives a browser tab through BiDi: tap/click/swipe via
// input.performActions, type via key actions, navigate via browsingContext.
func (c *collector) actChannel(w http.ResponseWriter, r *http.Request, b *broker) {
	var in struct {
		Action, Text, Context, Seat string
		X, Y, X2, Y2, Ms            int
	}
	json.NewDecoder(r.Body).Decode(&in)
	ctx := in.Context
	if ctx == "" {
		ctx = r.URL.Query().Get("context")
	}
	ctx = c.chanContext(b, ctx)
	if ctx == "" {
		http.Error(w, `{"error":"no tab context"}`, http.StatusBadGateway)
		return
	}
	pointer := func(acts string) string {
		return fmt.Sprintf(`{"method":"input.performActions","params":{"context":%q,"actions":[{"type":"pointer","id":"m","parameters":{"pointerType":"mouse"},"actions":[%s]}]}}`, ctx, acts)
	}
	var cmd string
	switch in.Action {
	case "tap", "click":
		cmd = pointer(fmt.Sprintf(`{"type":"pointerMove","x":%d,"y":%d},{"type":"pointerDown","button":0},{"type":"pointerUp","button":0}`, in.X, in.Y))
	case "swipe", "drag":
		ms := in.Ms
		if ms == 0 {
			ms = 400
		}
		cmd = pointer(fmt.Sprintf(`{"type":"pointerMove","x":%d,"y":%d},{"type":"pointerDown","button":0},{"type":"pointerMove","duration":%d,"x":%d,"y":%d},{"type":"pointerUp","button":0}`, in.X, in.Y, ms, in.X2, in.Y2))
	case "longpress":
		ms := in.Ms
		if ms == 0 {
			ms = 800
		}
		cmd = pointer(fmt.Sprintf(`{"type":"pointerMove","x":%d,"y":%d},{"type":"pointerDown","button":0},{"type":"pause","duration":%d},{"type":"pointerUp","button":0}`, in.X, in.Y, ms))
	case "sendkeys", "type":
		var ka strings.Builder
		first := true
		for _, ru := range in.Text {
			if !first {
				ka.WriteString(",")
			}
			first = false
			ch, _ := json.Marshal(string(ru))
			ka.WriteString(fmt.Sprintf(`{"type":"keyDown","value":%s},{"type":"keyUp","value":%s}`, ch, ch))
		}
		cmd = fmt.Sprintf(`{"method":"input.performActions","params":{"context":%q,"actions":[{"type":"key","id":"k","actions":[%s]}]}}`, ctx, ka.String())
	case "navigate":
		u, _ := json.Marshal(in.Text)
		cmd = fmt.Sprintf(`{"method":"browsingContext.navigate","params":{"context":%q,"url":%s,"wait":"complete"}}`, ctx, string(u))
	default:
		http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		return
	}
	start := time.Now()
	tr, err := c.command(b, cmd)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	c.echoOut(b.id, "channel", "act", map[string]any{"action": in.Action, "context": ctx, "at": fmt.Sprintf("%d,%d", in.X, in.Y)}, 200, time.Since(start).Milliseconds())
	// control is replayable + seat-attributed — folds into the active recording
	c.record(reqRec{TS: nowNano(), Physics: "channel", Session: b.id, Method: "act:" + in.Action, URL: b.base + "/command", Body: cmd, Status: 200, Replayable: true, Seat: in.Seat})
	w.Header().Set("Content-Type", "application/json")
	w.Write(tr)
}

func (c *collector) handleSource(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session")
	if b := c.find(sid); b != nil { // CHANNEL (browser): DOM walk over BiDi
		c.sourceChannel(w, r, b)
		return
	}
	rec := lookupSession(sid)
	if rec == nil || rec.Physics != "call" {
		http.Error(w, `{"error":"source: no such session"}`, http.StatusNotImplemented)
		return
	}
	start := time.Now()
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(rec.Hub + "/session/" + sid + "/source")
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	c.echoOut(sid, rec.Physics, "source", map[string]any{"route": "GET /session/{id}/source", "bytes": len(rb)}, resp.StatusCode, time.Since(start).Milliseconds())
	w.Header().Set("Content-Type", "application/json")
	w.Write(rb) // {"value":"<hierarchy>...</hierarchy>"}
}

// handleAct performs one interaction through the wire — physics-general verbs.
// call: tap -> W3C actions; click -> element click; sendkeys -> element value.
func (c *collector) handleAct(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session")
	if b := c.find(sid); b != nil { // CHANNEL (browser): drive the tab over BiDi
		c.actChannel(w, r, b)
		return
	}
	rec := lookupSession(sid)
	if rec == nil || rec.Physics != "call" {
		http.Error(w, `{"error":"act: no such session"}`, http.StatusNotImplemented)
		return
	}
	var in struct {
		Action string `json:"action"`
		X      int    `json:"x"`
		Y      int    `json:"y"`
		X2     int    `json:"x2"` // swipe/drag end point
		Y2     int    `json:"y2"`
		Ms     int    `json:"ms"` // gesture duration
		El     string `json:"element"`
		Text   string `json:"text"`
		Seat   string `json:"seat"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	base := rec.Hub + "/session/" + sid
	var url, body string
	switch in.Action {
	case "tap":
		url = base + "/actions"
		body = fmt.Sprintf(`{"actions":[{"type":"pointer","id":"finger","parameters":{"pointerType":"touch"},"actions":[{"type":"pointerMove","duration":0,"x":%d,"y":%d},{"type":"pointerDown","button":0},{"type":"pause","duration":60},{"type":"pointerUp","button":0}]}]}`, in.X, in.Y)
	case "swipe", "drag":
		// a real-world gesture: press at (x,y), drag to (x2,y2) over ms, release.
		// the pause after down makes it a drag, not a flick. A system swipe (iOS
		// notification pull, Android shade) is just this starting from y≈0.
		ms := in.Ms
		if ms == 0 {
			ms = 400
		}
		url = base + "/actions"
		body = fmt.Sprintf(`{"actions":[{"type":"pointer","id":"finger","parameters":{"pointerType":"touch"},"actions":[{"type":"pointerMove","duration":0,"x":%d,"y":%d},{"type":"pointerDown","button":0},{"type":"pause","duration":120},{"type":"pointerMove","duration":%d,"x":%d,"y":%d},{"type":"pointerUp","button":0}]}]}`, in.X, in.Y, ms, in.X2, in.Y2)
	case "longpress":
		ms := in.Ms
		if ms == 0 {
			ms = 800
		}
		url = base + "/actions"
		body = fmt.Sprintf(`{"actions":[{"type":"pointer","id":"finger","parameters":{"pointerType":"touch"},"actions":[{"type":"pointerMove","duration":0,"x":%d,"y":%d},{"type":"pointerDown","button":0},{"type":"pause","duration":%d},{"type":"pointerUp","button":0}]}]}`, in.X, in.Y, ms)
	case "click":
		url, body = base+"/element/"+in.El+"/click", `{}`
	case "sendkeys":
		b, _ := json.Marshal(map[string]any{"text": in.Text})
		url, body = base+"/element/"+in.El+"/value", string(b)
	default:
		http.Error(w, `{"error":"unknown action"}`, http.StatusBadRequest)
		return
	}
	start := time.Now()
	resp, err := c.client.Post(url, "application/json", strings.NewReader(body))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	c.echoOut(sid, rec.Physics, "act", map[string]any{"action": in.Action, "from": fmt.Sprintf("%d,%d", in.X, in.Y), "to": fmt.Sprintf("%d,%d", in.X2, in.Y2)}, resp.StatusCode, time.Since(start).Milliseconds())
	// request-physics control is replayable + seat-attributed — folds into series.
	// Method is the HTTP verb (acts are POSTs) so replay re-fires correctly.
	c.record(reqRec{TS: nowNano(), Physics: "call", Session: sid, Method: "POST", URL: url, Body: body, Status: resp.StatusCode, Replayable: true, Seat: in.Seat})
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(rb)
}

// handleStream proxies a session's live MJPEG straight through — no decode or
// re-encode (the peer's lean pass-through). The browser renders the
// multipart/x-mixed-replace body live in an <img>. This is the afferent media
// transport: live, where /shot is the polled fallback.
func (c *collector) handleStream(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session")
	rec := lookupSession(sid)

	// Device MJPEG (Appium mjpegServerPort): plain HTTP pass-through.
	if rec != nil && strings.HasPrefix(rec.Stream, "http") {
		up, err := http.Get(rec.Stream)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		defer up.Body.Close()
		w.Header().Set("Content-Type", up.Header.Get("Content-Type")) // multipart/x-mixed-replace; boundary=...
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		buf := make([]byte, 64<<10)
		for {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			n, rerr := up.Body.Read(buf)
			if n > 0 {
				if _, werr := w.Write(buf[:n]); werr != nil {
					return
				}
				if flusher != nil {
					flusher.Flush()
				}
			}
			if rerr != nil {
				return
			}
		}
	}

	// Browser CHANNEL (CDP): start screencast on the held socket; the pump routes
	// each Page.screencastFrame into this session's frame channel; we re-serve
	// them as MJPEG so the cockpit <img> is identical to the device path.
	if b := c.find(sid); b != nil {
		c.command(b, `{"method":"Page.enable","params":{}}`)
		c.command(b, `{"method":"Page.startScreencast","params":{"format":"jpeg","quality":60,"maxFrameRate":12}}`)
		defer c.command(b, `{"method":"Page.stopScreencast","params":{}}`)
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		ch := c.frameChan(sid)
		for {
			select {
			case <-r.Context().Done():
				return
			case raw := <-ch:
				fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(raw))
				w.Write(raw)
				io.WriteString(w, "\r\n")
				if flusher != nil {
					flusher.Flush()
				}
			}
		}
	}

	http.Error(w, `{"error":"no live stream for this session"}`, http.StatusNotFound)
}

func (c *collector) handleHealth(w http.ResponseWriter, r *http.Request) {
	ids := make([]string, len(c.brokers))
	for i, b := range c.brokers {
		ids[i] = b.id
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"alive": true, "sessions": ids})
}

// stat is a latency distribution, microseconds. round2 keeps the JSON readable.
type stat struct {
	N    int     `json:"n"`
	Min  float64 `json:"min"`
	P50  float64 `json:"p50"`
	P90  float64 `json:"p90"`
	P99  float64 `json:"p99"`
	P999 float64 `json:"p999"`
	Max  float64 `json:"max"`
	Mean float64 `json:"mean"`
}

func round2(f float64) float64 { return math.Round(f*100) / 100 }

func summarize(xs []float64) stat {
	st := stat{N: len(xs)}
	if len(xs) == 0 {
		return st
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	pct := func(p float64) float64 {
		idx := int(p / 100 * float64(len(s)))
		if idx >= len(s) {
			idx = len(s) - 1
		}
		return s[idx]
	}
	var sum float64
	for _, x := range s {
		sum += x
	}
	st.Min, st.P50, st.P90, st.P99, st.P999, st.Max, st.Mean =
		round2(s[0]), round2(pct(50)), round2(pct(90)), round2(pct(99)), round2(pct(99.9)), round2(s[len(s)-1]), round2(sum/float64(len(s)))
	return st
}

// handleBench is 8's IP made measurable: over n trials it times the wire
// round-trip to KNOW a session's UI state two ways —
//   READ : script.evaluate returns the exact state (the channel, the value)
//   SEE  : browsingContext.captureScreenshot returns pixels (what "seeing" is)
// — timed in Go nanoseconds. This is why the timing lives HERE and not in the
// page: the browser's performance.now() is privacy-clamped (reads 0 sub-ms); 8,
// in Go, is the unclamped outside witness. Reports p50..p99.9, byte cost, and
// "equivalents of seeing" = how many channel-reads fit in one pixel-seeing.
func (c *collector) handleBench(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session")
	if sid == "" {
		sid = "fox"
	}
	b := c.find(sid)
	if b == nil {
		http.Error(w, `{"error":"unknown session"}`, http.StatusNotFound)
		return
	}
	ctxID := r.URL.Query().Get("context")
	if ctxID == "" {
		tr, _ := c.command(b, `{"method":"browsingContext.getTree","params":{}}`)
		var t struct {
			Result struct {
				Contexts []struct {
					Context string `json:"context"`
				} `json:"contexts"`
			} `json:"result"`
		}
		json.Unmarshal(tr, &t)
		if len(t.Result.Contexts) > 0 {
			ctxID = t.Result.Contexts[0].Context
		}
	}
	if ctxID == "" {
		http.Error(w, `{"error":"no context"}`, http.StatusBadGateway)
		return
	}
	n := 100
	if v := r.URL.Query().Get("n"); v != "" {
		if x, err := strconv.Atoi(v); err == nil && x > 0 && x <= 5000 {
			n = x
		}
	}

	readCmd := `{"method":"script.evaluate","params":{"target":{"context":"` + ctxID + `"},"awaitPromise":true,"expression":"JSON.stringify({p:packets.length,c:cmdSeq,o:channelOpen})"}}`
	seeCmd := `{"method":"browsingContext.captureScreenshot","params":{"context":"` + ctxID + `","origin":"viewport","format":{"type":"image/jpeg","quality":0.5}}}`

	readUs := make([]float64, 0, n)
	seeUs := make([]float64, 0, n)
	var readBytes, seeBytes int64
	var readErr, seeErr int

	// valid = transport ok AND HTTP 200 AND no error body (502 broken-pipe or a
	// BiDi `"type":"error"` must NOT be timed as a real round-trip).
	valid := func(rb []byte, st int, err error) bool {
		return err == nil && st == 200 && !bytes.Contains(rb, []byte(`"error"`))
	}

	rb, st, err := c.commandS(b, readCmd) // warm both paths before timing
	c.commandS(b, seeCmd)
	if !valid(rb, st, err) {
		http.Error(w, `{"error":"channel not live — warmup read failed (revive the session, then retry)"}`, http.StatusBadGateway)
		return
	}

	for i := 0; i < n; i++ {
		t0 := time.Now()
		rb, st, err := c.commandS(b, readCmd)
		d := time.Since(t0)
		if !valid(rb, st, err) {
			readErr++
		} else {
			readUs = append(readUs, float64(d.Nanoseconds())/1000.0)
			readBytes += int64(len(rb))
		}

		t1 := time.Now()
		sb, st2, err2 := c.commandS(b, seeCmd)
		d2 := time.Since(t1)
		if !valid(sb, st2, err2) {
			seeErr++
		} else {
			seeUs = append(seeUs, float64(d2.Nanoseconds())/1000.0)
			seeBytes += int64(len(sb))
		}
	}

	rs, ss := summarize(readUs), summarize(seeUs)
	avgReadBytes, avgSeeBytes := int64(0), int64(0)
	if len(readUs) > 0 {
		avgReadBytes = readBytes / int64(len(readUs))
	}
	if len(seeUs) > 0 {
		avgSeeBytes = seeBytes / int64(len(seeUs))
	}
	equivP50, equivP99, byteRatio := 0.0, 0.0, 0.0
	if rs.P50 > 0 {
		equivP50 = round2(ss.P50 / rs.P50)
	}
	if rs.P99 > 0 {
		equivP99 = round2(ss.P99 / rs.P99)
	}
	if avgReadBytes > 0 {
		byteRatio = round2(float64(avgSeeBytes) / float64(avgReadBytes))
	}

	// ?save=PATH appends raw per-trial samples (read_us,see_us) so many batches
	// can be pooled into one exact distribution instead of averaging summaries.
	if path := r.URL.Query().Get("save"); path != "" {
		if f, e := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); e == nil {
			m := len(readUs)
			if len(seeUs) < m {
				m = len(seeUs)
			}
			for i := 0; i < m; i++ {
				fmt.Fprintf(f, "%.3f,%.3f\n", readUs[i], seeUs[i])
			}
			f.Close()
		}
	}

	// echo a summary into the feed so the result is SEEN on 8, not just returned.
	c.echoOut(sid, "channel", "bench", map[string]any{
		"n": len(readUs), "read_p50_us": rs.P50, "see_p50_us": ss.P50,
		"read_p99_us": rs.P99, "see_p99_us": ss.P99, "equiv_p50": equivP50,
	}, 200, 0)

	tag := r.URL.Query().Get("tag")
	batchID := c.recordBench(benchRec{TS: nowNano(), Tag: tag, Session: sid, N: len(readUs),
		Read: rs, See: ss, EquivP50: equivP50, EquivP99: equivP99, ByteRatio: byteRatio,
		ReadBytes: avgReadBytes, SeeBytes: avgSeeBytes, ReadErr: readErr, SeeErr: seeErr})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"batch_id": batchID, "tag": tag,
		"session": sid, "context": ctxID, "n": n, "unit": "microseconds",
		"witness":                   "8/collector — Go time.Now(), nanosecond, unclamped",
		"read":                      rs,
		"see":                       ss,
		"read_bytes_avg":            avgReadBytes,
		"see_bytes_avg":             avgSeeBytes,
		"read_errors":               readErr,
		"see_errors":                seeErr,
		"equivalents_of_seeing_p50": equivP50,
		"equivalents_of_seeing_p99": equivP99,
		"byte_ratio_see_over_read":  byteRatio,
	})
}

// reqRec is one full request 8 witnessed — every field used to make it, so the
// cockpit can show it (headers, JSON body) and REPLAY it byte-for-byte. This is
// what only 8 can do: it sits on the wire and remembers the whole call.
type reqRec struct {
	ID       int64             `json:"id"`
	TS       string            `json:"ts"`
	Physics  string            `json:"physics"` // call (HTTP) | channel (BiDi/CDP)
	Session  string            `json:"session,omitempty"`
	Method   string            `json:"method"` // HTTP verb, or the BiDi/CDP method
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers,omitempty"`
	Body     string            `json:"body,omitempty"`
	Status   int               `json:"status"`
	LatUS    float64           `json:"latency_us"`
	RespLen    int               `json:"resp_bytes"`
	RespHead   string            `json:"resp_preview,omitempty"`
	Replayable bool              `json:"replayable"`
	Seat       string            `json:"seat,omitempty"` // who acted: operator | pilot | adapter | ai
}

func nowNano() string { return time.Now().UTC().Format(time.RFC3339Nano) }

func preview(b []byte) string {
	if len(b) > 700 {
		return string(b[:700])
	}
	return string(b)
}

// benchRec is one benchmark batch — its full metric set, tagged + timestamped so
// 8 can club many batches and filter them (by tag, by session).
type benchRec struct {
	ID        int64   `json:"id"`
	TS        string  `json:"ts"`
	Tag       string  `json:"tag,omitempty"`
	Session   string  `json:"session"`
	N         int     `json:"n"`
	Read      stat    `json:"read"`
	See       stat    `json:"see"`
	EquivP50  float64 `json:"equiv_p50"`
	EquivP99  float64 `json:"equiv_p99"`
	ByteRatio float64 `json:"byte_ratio"`
	ReadBytes int64   `json:"read_bytes"`
	SeeBytes  int64   `json:"see_bytes"`
	ReadErr   int     `json:"read_err"`
	SeeErr    int     `json:"see_err"`
}

// The load-limit hypothesis, in code: the NDJSON store keeps EVERY record
// forever (the truth); 8 holds only the most-recent window in memory and renders
// a page of it — a cockpit's attention (and the DOM) can't carry unbounded
// history. Same discipline as the session store: durable truth, bounded view.
const (
	ledgerWindow = 1000
	benchWindow  = 500
)

func dotDir() string        { return os.ExpandEnv("$HOME/.8") }
func requestsFile() string  { return dotDir() + "/requests.ndjson" }
func benchesFile() string   { return dotDir() + "/benches.ndjson" }

func appendNDJSON(path string, v any) {
	os.MkdirAll(dotDir(), 0o755)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	line, _ := json.Marshal(v)
	f.Write(append(line, '\n'))
}

// record appends one witnessed request: durable to NDJSON, plus the bounded
// in-memory window that 8 actually loads.
func (c *collector) record(r reqRec) int64 {
	c.lmu.Lock()
	c.lseq++
	r.ID = c.lseq
	c.ledger = append(c.ledger, r)
	if len(c.ledger) > ledgerWindow {
		c.ledger = c.ledger[len(c.ledger)-ledgerWindow:]
	}
	c.lmu.Unlock()
	appendNDJSON(requestsFile(), r)
	// if a recording is in progress, fold this frame into the series, seat-stamped
	c.rmu.Lock()
	if c.recOn {
		if r.Seat == "" {
			r.Seat = c.recSeat
		}
		c.recBuf = append(c.recBuf, r)
	}
	c.rmu.Unlock()
	return r.ID
}

func (c *collector) recordBench(b benchRec) int64 {
	c.bmu.Lock()
	c.bseq++
	b.ID = c.bseq
	c.benches = append(c.benches, b)
	if len(c.benches) > benchWindow {
		c.benches = c.benches[len(c.benches)-benchWindow:]
	}
	c.bmu.Unlock()
	appendNDJSON(benchesFile(), b)
	return b.ID
}

// loadLedger re-hydrates the bounded window from the durable store on startup, so
// a collector restart (a rebuild) does not lose the request/batch history.
func (c *collector) loadLedger() {
	if data, err := os.ReadFile(requestsFile()); err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) > ledgerWindow {
			lines = lines[len(lines)-ledgerWindow:]
		}
		for _, ln := range lines {
			if ln == "" {
				continue
			}
			var r reqRec
			if json.Unmarshal([]byte(ln), &r) == nil {
				c.ledger = append(c.ledger, r)
				if r.ID > c.lseq {
					c.lseq = r.ID
				}
			}
		}
	}
	if data, err := os.ReadFile(benchesFile()); err == nil {
		lines := strings.Split(strings.TrimSpace(string(data)), "\n")
		if len(lines) > benchWindow {
			lines = lines[len(lines)-benchWindow:]
		}
		for _, ln := range lines {
			if ln == "" {
				continue
			}
			var b benchRec
			if json.Unmarshal([]byte(ln), &b) == nil {
				c.benches = append(c.benches, b)
				if b.ID > c.bseq {
					c.bseq = b.ID
				}
			}
		}
	}
}

// handleBenches returns benchmark batches (newest first), filterable by ?tag=.
func (c *collector) handleBenches(w http.ResponseWriter, r *http.Request) {
	tag := r.URL.Query().Get("tag")
	c.bmu.Lock()
	out := []benchRec{}
	for _, b := range c.benches {
		if tag == "" || b.Tag == tag {
			out = append(out, b)
		}
	}
	c.bmu.Unlock()
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"benches": out, "count": len(out), "window": benchWindow})
}

// handleRequests returns the witnessed request ledger, newest first — the full
// detail of every call/channel request 8 has seen.
func (c *collector) handleRequests(w http.ResponseWriter, r *http.Request) {
	c.lmu.Lock()
	out := make([]reqRec, len(c.ledger))
	copy(out, c.ledger)
	c.lmu.Unlock()
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	n := 100
	if v := r.URL.Query().Get("n"); v != "" {
		if x, e := strconv.Atoi(v); e == nil && x > 0 {
			n = x
		}
	}
	if n < len(out) {
		out = out[:n]
	}
	for i := range out {
		// channel requests replay only while the session still holds its socket;
		// a call can always be re-fired.
		out[i].Replayable = out[i].Physics == "call" || (out[i].Physics == "channel" && c.find(out[i].Session) != nil)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"requests": out})
}

// ── record → replay SERIES (seat-attributed) ────────────────────────────────
// A series is the frames captured while a recording is on — control driven
// through the wire (call + channel), each stamped with the seat that issued it.
// Replaying fires every frame concurrently, so a CALL and a CHANNEL frame in the
// same series land together. Persisted to ~/.8-series so they survive crashes.
func seriesDir() string {
	d := os.Getenv("HOME") + "/.8-series"
	os.MkdirAll(d, 0o755)
	return d
}
func seriesPath(name string) string { return seriesDir() + "/" + strings.ReplaceAll(name, "/", "_") + ".json" }
func writeSeries(name string, frames []reqRec) error {
	b, _ := json.MarshalIndent(frames, "", " ")
	return os.WriteFile(seriesPath(name), b, 0o644)
}
func readSeries(name string) ([]reqRec, error) {
	b, err := os.ReadFile(seriesPath(name))
	if err != nil {
		return nil, err
	}
	var f []reqRec
	return f, json.Unmarshal(b, &f)
}

// replayFrame re-fires one recorded frame through its own physics.
func (c *collector) replayFrame(rec reqRec) (int, error) {
	if rec.Physics == "channel" {
		b := c.find(rec.Session)
		if b == nil {
			return 0, fmt.Errorf("session %q gone", rec.Session)
		}
		_, status, err := c.commandS(b, rec.Body)
		return status, err
	}
	req, err := http.NewRequest(rec.Method, rec.URL, strings.NewReader(rec.Body))
	if err != nil {
		return 0, err
	}
	for k, v := range rec.Headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return 0, err
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	return resp.StatusCode, nil
}

// handleRecord: ?action=start&name=&seat= | stop | (status).
func (c *collector) handleRecord(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/json")
	switch q.Get("action") {
	case "start":
		c.rmu.Lock()
		c.recOn, c.recBuf = true, nil
		c.recName = q.Get("name")
		if c.recName == "" {
			c.recName = "series"
		}
		c.recSeat = q.Get("seat")
		if c.recSeat == "" {
			c.recSeat = "operator"
		}
		name, seat := c.recName, c.recSeat
		c.rmu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"recording": true, "name": name, "seat": seat})
	case "stop":
		c.rmu.Lock()
		name, buf := c.recName, c.recBuf
		c.recOn, c.recBuf = false, nil
		c.rmu.Unlock()
		if len(buf) > 0 {
			writeSeries(name, buf)
		}
		json.NewEncoder(w).Encode(map[string]any{"recording": false, "saved": name, "frames": len(buf)})
	default:
		c.rmu.Lock()
		on, name, n := c.recOn, c.recName, len(c.recBuf)
		c.rmu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"recording": on, "name": name, "frames": n})
	}
}

// handleSeries: list saved series with their frame count, seats, and modes.
func (c *collector) handleSeries(w http.ResponseWriter, r *http.Request) {
	ents, _ := os.ReadDir(seriesDir())
	type item struct {
		Name   string   `json:"name"`
		Frames int      `json:"frames"`
		Seats  []string `json:"seats"`
		Modes  []string `json:"modes"`
	}
	out := []item{}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		nm := strings.TrimSuffix(e.Name(), ".json")
		fr, err := readSeries(nm)
		if err != nil {
			continue
		}
		seats, modes := map[string]bool{}, map[string]bool{}
		for _, f := range fr {
			if f.Seat != "" {
				seats[f.Seat] = true
			}
			modes[f.Physics] = true
		}
		out = append(out, item{Name: nm, Frames: len(fr), Seats: mkeys(seats), Modes: mkeys(modes)})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"series": out})
}

func mkeys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}

// handleReplaySeries: fire every frame concurrently — call + channel together.
func (c *collector) handleReplaySeries(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	frames, err := readSeries(name)
	if err != nil {
		http.Error(w, `{"error":"no such series"}`, http.StatusNotFound)
		return
	}
	type res struct {
		Seq     int    `json:"seq"`
		Physics string `json:"physics"`
		Seat    string `json:"seat"`
		Method  string `json:"method"`
		Status  int    `json:"status"`
		Ok      bool   `json:"ok"`
	}
	out := make([]res, len(frames))
	var wg sync.WaitGroup
	for i, f := range frames {
		wg.Add(1)
		go func(i int, f reqRec) {
			defer wg.Done()
			st, e := c.replayFrame(f)
			out[i] = res{Seq: i, Physics: f.Physics, Seat: f.Seat, Method: f.Method, Status: st, Ok: e == nil && st < 400}
			c.publish(fmt.Sprintf(`{"session":%q,"physics":%q,"origin":"REPLAY","frame":{"method":%q,"params":{"seat":%q,"status":%d}}}`, f.Session, f.Physics, f.Method, f.Seat, st))
		}(i, f)
	}
	wg.Wait()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"name": name, "fired": len(frames), "results": out})
}

// handleReplay re-fires a recorded request byte-for-byte (if the session/client
// still allows it) and records the replay as a fresh ledger entry.
func (c *collector) handleReplay(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	c.lmu.Lock()
	var rec *reqRec
	for i := range c.ledger {
		if c.ledger[i].ID == id {
			rr := c.ledger[i]
			rec = &rr
			break
		}
	}
	c.lmu.Unlock()
	if rec == nil {
		http.Error(w, `{"error":"no such request id"}`, http.StatusNotFound)
		return
	}
	start := time.Now()
	var status int
	var body []byte
	var err error
	if rec.Physics == "channel" {
		b := c.find(rec.Session)
		if b == nil {
			http.Error(w, `{"error":"session gone — cannot replay this channel request"}`, http.StatusBadGateway)
			return
		}
		body, status, err = c.commandS(b, rec.Body)
	} else {
		req, e := http.NewRequest(rec.Method, rec.URL, strings.NewReader(rec.Body))
		if e != nil {
			http.Error(w, `{"error":"`+e.Error()+`"}`, http.StatusBadRequest)
			return
		}
		for k, v := range rec.Headers {
			req.Header.Set(k, v)
		}
		resp, e2 := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if e2 != nil {
			err = e2
		} else {
			defer resp.Body.Close()
			body, _ = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			status = resp.StatusCode
		}
	}
	if err != nil {
		http.Error(w, `{"error":"replay failed: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	lat := float64(time.Since(start).Nanoseconds()) / 1000.0
	nid := c.record(reqRec{TS: nowNano(), Physics: rec.Physics, Session: rec.Session, Method: rec.Method, URL: rec.URL, Headers: rec.Headers, Body: rec.Body, Status: status, LatUS: lat, RespLen: len(body), RespHead: preview(body)})
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"replayed": id, "new_id": nid, "status": status, "latency_us": lat, "resp_bytes": len(body), "response": string(body[:min(len(body), 8192)])})
}

// cors lets the cockpit (another origin) read /feed and drive /run, /fetch.
func cors(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func main() {
	listen := flag.String("listen", ":7070", "HTTP address the cockpit reaches the collector on")
	spec := flag.String("brokers", "", "comma list of session=brokerURL (e.g. fox=http://127.0.0.1:4445)")
	gecko := flag.String("gecko", "", "geckodriver session base (http://127.0.0.1:4444/session/<id>) — enables /procinfo per-tab mem/CPU")
	flag.Parse()

	var brokers []broker
	for _, part := range strings.Split(*spec, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		id, base, ok := strings.Cut(part, "=")
		if !ok {
			log.Fatalf("collector: bad -brokers entry %q (want session=url)", part)
		}
		brokers = append(brokers, broker{id: strings.TrimSpace(id), base: strings.TrimSpace(base)})
	}
	if len(brokers) == 0 {
		log.Fatal("collector: -brokers is required (session=url,session=url,...)")
	}

	c := newCollector(brokers)
	c.gecko = strings.TrimRight(*gecko, "/")
	// register a REQUEST seat for the SAME Firefox: -gecko is the classic
	// WebDriver session — the call-physics view of the very browser the channel
	// (broker) drives. So 8's rail shows fox (channel) AND this seat (request),
	// two seats on one Firefox. /act,/source,/shot route it via the call path.
	if i := strings.LastIndex(c.gecko, "/session/"); i >= 0 {
		base, sid := c.gecko[:i], c.gecko[i+len("/session/"):]
		appendNDJSON(sessionsFile(), sessionRec{ID: sid, Hub: base, Kind: "local", Physics: "call", Created: nowNano()})
		log.Printf("request seat registered: %s (call) on %s — same Firefox as fox (channel)", sid, base)
	}
	c.loadLedger() // re-hydrate the bounded window from the durable NDJSON store
	ctx := context.Background()
	for _, b := range c.brokers {
		go c.pump(ctx, b)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/feed", c.handleFeed)
	mux.HandleFunc("/run", c.handleRun)
	mux.HandleFunc("/broadcast", c.handleBroadcast)
	mux.HandleFunc("/fetch", c.handleFetch)
	mux.HandleFunc("/shot", c.handleShot)
	mux.HandleFunc("/tabs", c.handleTabs)
	mux.HandleFunc("/procinfo", c.handleProcInfo)
	mux.HandleFunc("/sessions", c.handleSessions)
	mux.HandleFunc("/source", c.handleSource)
	mux.HandleFunc("/act", c.handleAct)
	mux.HandleFunc("/stream", c.handleStream)
	mux.HandleFunc("/bench", c.handleBench)
	mux.HandleFunc("/requests", c.handleRequests)
	mux.HandleFunc("/replay", c.handleReplay)
	mux.HandleFunc("/record", c.handleRecord)
	mux.HandleFunc("/series", c.handleSeries)
	mux.HandleFunc("/replay-series", c.handleReplaySeries)
	mux.HandleFunc("/benches", c.handleBenches)
	mux.HandleFunc("/health", c.handleHealth)

	log.Printf("collector: %d session(s), serving on %s (GET /feed, POST /run, /fetch, /broadcast)", len(brokers), *listen)
	log.Fatal(http.ListenAndServe(*listen, cors(mux)))
}
