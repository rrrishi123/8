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
	"net/http"
	"os"
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

	mu      sync.Mutex
	subs    map[int]chan string
	nextSub int64
}

func newCollector(brokers []broker) *collector {
	return &collector{brokers: brokers, client: &http.Client{}, subs: map[int]chan string{}}
}

func (c *collector) find(id string) *broker {
	for i := range c.brokers {
		if c.brokers[i].id == id {
			return &c.brokers[i]
		}
	}
	return nil
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
				c.publish(fmt.Sprintf(`{"session":%q,"origin":"BIDI","frame":%s}`, b.id, strings.TrimPrefix(line, "data: ")))
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
	c.publish(fmt.Sprintf(`{"session":%q,"origin":"COLLECTOR","frame":{"method":"run","params":{"command":%q}}}`, b.id, probe.Method))

	resp, err := c.client.Post(b.base+"/command", "application/json", bytes.NewReader(body))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
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
	c.publish(fmt.Sprintf(`{"session":"wire","origin":"COLLECTOR","frame":{"method":"http_request","params":{"http_method":%q,"url":%q,"status":%d,"latency_ms":%d}}}`, strings.ToUpper(in.Method), in.URL, resp.StatusCode, latency))

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
			registerSession(sessionRec{ID: sv.Value.SessionID, Hub: strings.TrimSuffix(bare, "/session"), Kind: kind, Physics: "call"})
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

type sessionRec struct {
	ID      string `json:"id"`
	Hub     string `json:"hub"`
	Kind    string `json:"kind"`    // local | cloud
	Physics string `json:"physics"` // call | channel
	Created string `json:"created_at,omitempty"`
}

func registerSession(rec sessionRec) {
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
		byID[b.id] = sessionRec{ID: b.id, Hub: b.base, Kind: "local", Physics: "channel"}
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
	// liveness-prune: ping each call session in parallel, drop the dead ones so
	// 8's rail never shows a terminated session. Channel sessions are alive by
	// the broker holding their socket.
	type res struct {
		rec   sessionRec
		alive bool
	}
	ch := make(chan res, len(byID))
	for _, rec := range byID {
		if rec.Physics != "call" {
			ch <- res{rec, true}
			continue
		}
		go func(rec sessionRec) {
			cl := &http.Client{Timeout: 1500 * time.Millisecond}
			resp, err := cl.Get(rec.Hub + "/session/" + rec.ID + "/timeouts")
			alive := err == nil && resp.StatusCode == 200
			if resp != nil {
				resp.Body.Close()
			}
			ch <- res{rec, alive}
		}(rec)
	}
	out := make([]sessionRec, 0, len(byID))
	for range byID {
		if r := <-ch; r.alive {
			out = append(out, r.rec)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"sessions": out})
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
func (c *collector) handleSource(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session")
	rec := lookupSession(sid)
	if rec == nil || rec.Physics != "call" {
		http.Error(w, `{"error":"source: only call sessions for now (BiDi DOM tree pending)"}`, http.StatusNotImplemented)
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
	rec := lookupSession(sid)
	if rec == nil || rec.Physics != "call" {
		http.Error(w, `{"error":"act: only call sessions for now"}`, http.StatusNotImplemented)
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(rb)
}

func (c *collector) handleHealth(w http.ResponseWriter, r *http.Request) {
	ids := make([]string, len(c.brokers))
	for i, b := range c.brokers {
		ids[i] = b.id
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"alive": true, "sessions": ids})
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
	mux.HandleFunc("/sessions", c.handleSessions)
	mux.HandleFunc("/source", c.handleSource)
	mux.HandleFunc("/act", c.handleAct)
	mux.HandleFunc("/health", c.handleHealth)

	log.Printf("collector: %d session(s), serving on %s (GET /feed, POST /run, /fetch, /broadcast)", len(brokers), *listen)
	log.Fatal(http.ListenAndServe(*listen, cors(mux)))
}
