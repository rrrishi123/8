package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── BROWSER NODES (#1147) — the registry of browser HOSTS this 8 can see, joined
// to the seats that drive them, served at /nodes. The cockpit used to do this
// join itself and got it wrong: it compared a seat's HUB (the channel broker,
// :4445) with a node's cdp_url (the browser's devtools listener) — two different
// listeners that never match, so a declared node never claimed its live seat and
// every card said "no session". The join key is the broker's UPSTREAM: the
// socket the broker HOLDS (ws://host:port/...). The channel broker now reports
// it, redacted to an origin, on /health; for the fox broker that predates that
// field the collector falls back to the gecko session file it already re-reads.
//
// Sources, merged by id (the file names/labels win, live docker port maps win
// for URLs so a recreated container never leaves a stale port in a card):
//   file    $EIGHT_HOME/browser-nodes.json — written by scripts/browser-nodes-sync.sh
//   docker  `docker ps` — any container publishing a devtools port (9222) or a
//           browser image with a web view (3000, Selkies); absent docker → none
//   host    synthesized from a held browser broker no container claims
//
// Off-machine this means /nodes is NEVER empty just because ~/.8 is: a fresh
// host with one Firefox broker gets one host node; a host with browser
// containers gets them without any producer script.
//
// TODO(B4): pixels-per-tab for CDP nodes (Target.attachToTarget flatten + a
// /shot via the probe) is a separate, deeper task — not attempted here.

type browserNode struct {
	ID        string  `json:"id"`
	Engine    string  `json:"engine"`
	Mode      string  `json:"mode"` // container | host
	Container *string `json:"container"`
	Profile   *string `json:"profile"`
	ViewURL   *string `json:"view_url"`
	CDPURL    *string `json:"cdp_url"`
	// the join — filled by the collector, never by a producer
	Seat       string `json:"seat,omitempty"`        // broker id that holds this node's browser
	SeatStatus string `json:"seat_status,omitempty"` // live | disconnected
	Upstream   string `json:"upstream,omitempty"`    // the held socket's origin (redacted)
	Attachable bool   `json:"attachable"`            // has a cdp_url and no seat: /nodes/attach can start a broker
	Source     string `json:"source"`                // file | docker | file+docker | host
}

func strp(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
func strv(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

func eightHome() string {
	if v := os.Getenv("EIGHT_HOME"); v != "" {
		return v
	}
	return os.ExpandEnv("$HOME/.8")
}
func nodesFile() string { return filepath.Join(eightHome(), "browser-nodes.json") }

// readNodesFile — the producer's registry, if any. Missing → nil, honestly.
func readNodesFile() []browserNode {
	b, err := os.ReadFile(nodesFile())
	if err != nil {
		return nil
	}
	var doc struct {
		Nodes []browserNode `json:"nodes"`
	}
	if json.Unmarshal(b, &doc) != nil {
		return nil
	}
	for i := range doc.Nodes {
		doc.Nodes[i].Source = "file"
		if doc.Nodes[i].Mode == "" {
			doc.Nodes[i].Mode = "container"
		}
	}
	return doc.Nodes
}

var portMapRe = regexp.MustCompile(`(\d+)->(\d+)/tcp`)

// dockerNodes — browser containers from live port maps. One `docker ps` per
// refresh (cached below), never per hit. A container is a browser node when it
// publishes a devtools port (9222) or runs a browser image with a web view.
func dockerNodes() ([]browserNode, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", "ps", "--format", "{{.Names}}\t{{.Image}}\t{{.Ports}}").Output()
	if err != nil {
		return nil, false
	}
	var nodes []browserNode
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(ln, "\t")
		if len(f) < 3 || f[0] == "" {
			continue
		}
		name, image, ports := f[0], strings.ToLower(f[1]), f[2]
		hostPort := map[string]string{} // container port -> host port (first mapping wins)
		for _, m := range portMapRe.FindAllStringSubmatch(ports, -1) {
			if _, ok := hostPort[m[2]]; !ok {
				hostPort[m[2]] = m[1]
			}
		}
		lname := strings.ToLower(name)
		isBrowserImage := strings.Contains(image, "chrom") || strings.Contains(image, "firefox") || strings.Contains(image, "selenium") || strings.Contains(image, "browser") ||
			strings.Contains(lname, "chrome") || strings.Contains(lname, "firefox") || strings.Contains(lname, "browser")
		cdp, view := hostPort["9222"], hostPort["3000"]
		if cdp == "" && !(isBrowserImage && view != "") {
			continue
		}
		engine := "chromium"
		if strings.Contains(image, "firefox") || strings.Contains(lname, "firefox") {
			engine = "firefox"
		}
		n := browserNode{ID: name, Engine: engine, Mode: "container", Container: strp(name), Source: "docker"}
		if view != "" {
			n.ViewURL = strp("http://127.0.0.1:" + view + "/")
		}
		if cdp != "" {
			n.CDPURL = strp("http://127.0.0.1:" + cdp)
		}
		nodes = append(nodes, n)
	}
	return nodes, true
}

// ── broker facts: upstream origin + protocol, from /health (cached) ──────────
type brokerFact struct {
	Alive    bool
	Upstream string // scheme://host:port of the held socket, redacted
	Protocol string // bidi | cdp | ""
	at       time.Time
}

var (
	bfMu    sync.Mutex
	bfCache = map[string]brokerFact{} // broker base -> fact
)

// redactOrigin keeps scheme+host[:port] of a socket URL: the path carries the
// session id and userinfo may carry creds — neither belongs in a card.
func redactOrigin(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

func protocolOf(raw string) string {
	switch {
	case strings.Contains(raw, "/devtools/"):
		return "cdp"
	case strings.Contains(raw, "/session/"):
		return "bidi"
	}
	return ""
}

// brokerFactFor probes a broker's /health for its upstream (5s cache). A broker
// built before /health carried `upstream` (the long-running fox one) gets the
// fallback: the gecko session file's ws — the very socket that broker holds.
func (c *collector) brokerFactFor(b broker) brokerFact {
	bfMu.Lock()
	if f, ok := bfCache[b.base]; ok && time.Since(f.at) < 5*time.Second {
		bfMu.Unlock()
		return f
	}
	bfMu.Unlock()
	f := brokerFact{at: time.Now()}
	cl := &http.Client{Timeout: 3 * time.Second}
	if resp, err := cl.Get(b.base + "/health"); err == nil {
		var h struct {
			Alive    bool   `json:"alive"`
			Upstream string `json:"upstream"`
			Protocol string `json:"protocol"`
		}
		json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&h)
		resp.Body.Close()
		f.Alive = resp.StatusCode == 200
		f.Upstream, f.Protocol = h.Upstream, h.Protocol
	}
	if f.Upstream == "" && b.id == "fox" && c.sessionFile != "" {
		if raw, err := os.ReadFile(c.sessionFile); err == nil {
			var s struct {
				WS string `json:"ws"`
			}
			if json.Unmarshal(raw, &s) == nil && s.WS != "" {
				f.Upstream, f.Protocol = redactOrigin(s.WS), protocolOf(s.WS)
			}
		}
	}
	bfMu.Lock()
	bfCache[b.base] = f
	bfMu.Unlock()
	return f
}

// sameListener — two URLs name the same TCP listener: same port, and hosts
// equal or both local (0.0.0.0 / 127.0.0.1 / localhost / ::1).
func sameListener(a, b string) bool {
	ua, ea := url.Parse(a)
	ub, eb := url.Parse(b)
	if ea != nil || eb != nil || ua.Port() == "" || ua.Port() != ub.Port() {
		return false
	}
	local := func(h string) bool {
		switch h {
		case "0.0.0.0", "127.0.0.1", "localhost", "::1", "[::1]", "":
			return true
		}
		return false
	}
	ha, hb := ua.Hostname(), ub.Hostname()
	return ha == hb || (local(ha) && local(hb))
}

// ── the registry, joined (cached 10s; docker ps is the slow part) ────────────
var (
	nodesMu  sync.Mutex
	nodesAt  time.Time
	nodesOut map[string]any
)

func (c *collector) nodesJoined() map[string]any {
	nodesMu.Lock()
	defer nodesMu.Unlock()
	if nodesOut != nil && time.Since(nodesAt) < 10*time.Second {
		return nodesOut
	}
	fileNodes := readNodesFile()
	dockerN, dockerOK := dockerNodes()
	byID := map[string]*browserNode{}
	var order []string
	for i := range fileNodes {
		n := fileNodes[i]
		byID[n.ID] = &n
		order = append(order, n.ID)
	}
	for i := range dockerN {
		d := dockerN[i]
		if n, ok := byID[d.ID]; ok {
			// live port maps beat a stale file; labels stay the file's
			if d.ViewURL != nil {
				n.ViewURL = d.ViewURL
			}
			if d.CDPURL != nil {
				n.CDPURL = d.CDPURL
			}
			n.Source = "file+docker"
			continue
		}
		byID[d.ID] = &d
		order = append(order, d.ID)
	}
	// brokers → facts
	type bf struct {
		b broker
		f brokerFact
	}
	var facts []bf
	for _, b := range c.brokerList() {
		facts = append(facts, bf{b, c.brokerFactFor(b)})
	}
	status := func(f brokerFact) string {
		if f.Alive {
			return "live"
		}
		return "disconnected"
	}
	claimed := map[string]bool{}
	// 1. container nodes claim the broker holding their devtools listener
	for _, id := range order {
		n := byID[id]
		if n.CDPURL == nil {
			continue
		}
		for _, x := range facts {
			if claimed[x.b.id] || x.f.Upstream == "" || !sameListener(x.f.Upstream, *n.CDPURL) {
				continue
			}
			n.Seat, n.SeatStatus, n.Upstream = x.b.id, status(x.f), x.f.Upstream
			claimed[x.b.id] = true
			break
		}
	}
	// 2. host nodes claim a browser broker no container holds (engine-matched first)
	hostPick := func(engine string) *bf {
		var any *bf
		for i := range facts {
			x := &facts[i]
			if claimed[x.b.id] {
				continue
			}
			want := "bidi"
			if strings.Contains(strings.ToLower(engine), "chrom") {
				want = "cdp"
			}
			if x.f.Protocol == want || (x.f.Protocol == "" && x.b.id == "fox" && want == "bidi") {
				return x
			}
			if any == nil {
				any = x
			}
		}
		return any
	}
	for _, id := range order {
		n := byID[id]
		if n.Mode != "host" || n.Seat != "" {
			continue
		}
		if x := hostPick(n.Engine); x != nil {
			n.Seat, n.SeatStatus, n.Upstream = x.b.id, status(x.f), x.f.Upstream
			claimed[x.b.id] = true
		}
	}
	// 3. a held browser broker nobody declared → synthesize its host node
	for _, x := range facts {
		if claimed[x.b.id] {
			continue
		}
		engine := "browser"
		switch x.f.Protocol {
		case "bidi":
			engine = "firefox"
		case "cdp":
			engine = "chromium"
		}
		if x.b.id == "fox" && engine == "browser" {
			engine = "firefox"
		}
		id := "host-" + engine
		if _, dup := byID[id]; dup {
			id = "host-" + x.b.id
		}
		n := &browserNode{ID: id, Engine: engine, Mode: "host", Profile: strp("host"), Source: "host",
			Seat: x.b.id, SeatStatus: status(x.f), Upstream: x.f.Upstream}
		byID[id] = n
		order = append(order, id)
		claimed[x.b.id] = true
	}
	out := make([]browserNode, 0, len(order))
	for _, id := range order {
		n := byID[id]
		n.Attachable = n.CDPURL != nil && n.Seat == ""
		out = append(out, *n)
	}
	src := map[string]any{"docker": dockerOK}
	if fileNodes != nil {
		src["file"] = nodesFile()
	} else {
		src["file"] = ""
	}
	nodesOut = map[string]any{"nodes": out, "sources": src, "at": time.Now().UTC().Format(time.RFC3339)}
	nodesAt = time.Now()
	return nodesOut
}

func invalidateNodes() {
	nodesMu.Lock()
	nodesOut = nil
	nodesMu.Unlock()
	bfMu.Lock()
	bfCache = map[string]brokerFact{}
	bfMu.Unlock()
}

// handleNodes — GET /nodes: the joined registry. ?fresh=1 skips the cache.
func (c *collector) handleNodes(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("fresh") == "1" {
		invalidateNodes()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(c.nodesJoined())
}

// ── ATTACH: start a channel broker for an unjoined node's cdp_url ────────────
// POST /nodes/attach {"id":"<node id>"} (or ?id=). Resolves the browser's
// websocket from <cdp_url>/json/version, spawns `channel -ws <ws> -listen :N`
// on a free port, waits for its /health, and adds it to the held brokers — so
// /sessions, /tabs?session=<id>, /act all work on it at once. The broker is
// ALSO recorded in the sessions registry file, so a collector restart re-adopts
// it (adoptChannelSeats) instead of orphaning a live process.
//
// Non-headless Chrome ignores --remote-debugging-address and binds its devtools
// port to 127.0.0.1 INSIDE the container, so the published port answers with an
// empty reply. When that happens for a container node we start a tiny loopback
// bridge inside the container (python, bound to the container's own address on
// the same port) and probe again. The bridge dies with the container.
func channelBin() string {
	for _, p := range []string{
		os.Getenv("EIGHT_CHANNEL_BIN"),
		filepath.Join(repoRoot(), "http-mcp", ".bin", "channel"),
		os.ExpandEnv("$HOME/.8/bin/channel"),
	} {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return look("channel")
}

const cdpBridgePy = `import asyncio,socket,sys
me=socket.gethostbyname(socket.gethostname())
async def pipe(r,w):
    try:
        while True:
            d=await r.read(65536)
            if not d: break
            w.write(d); await w.drain()
    except Exception: pass
    finally:
        try: w.close()
        except Exception: pass
async def h(cr,cw):
    try: ur,uw=await asyncio.open_connection("127.0.0.1",9222)
    except Exception:
        cw.close(); return
    await asyncio.gather(pipe(cr,uw),pipe(ur,cw))
async def main():
    s=await asyncio.start_server(h,me,9222)
    async with s: await s.serve_forever()
asyncio.run(main())`

func cdpVersion(cdpURL string) (string, error) {
	cl := &http.Client{Timeout: 3 * time.Second}
	resp, err := cl.Get(strings.TrimRight(cdpURL, "/") + "/json/version")
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var v struct {
		WS string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&v); err != nil {
		return "", fmt.Errorf("bad /json/version: %v", err)
	}
	if v.WS == "" {
		return "", fmt.Errorf("no webSocketDebuggerUrl in /json/version")
	}
	// Chrome reports its INSIDE listener (127.0.0.1:9222); we reach it through
	// the published port — rewrite host:port, keep the /devtools/browser/<id> path.
	u, err := url.Parse(v.WS)
	if err != nil {
		return "", err
	}
	cu, _ := url.Parse(cdpURL)
	u.Host = cu.Host
	if u.Hostname() == "0.0.0.0" {
		u.Host = "127.0.0.1:" + u.Port()
	}
	return u.String(), nil
}

func (c *collector) handleNodesAttach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `POST {"id":"<node id>"}`, http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		ID string `json:"id"`
	}
	json.NewDecoder(r.Body).Decode(&in)
	if in.ID == "" {
		in.ID = r.URL.Query().Get("id")
	}
	fail := func(code int, msg string) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		json.NewEncoder(w).Encode(map[string]any{"error": msg, "id": in.ID})
	}
	if in.ID == "" {
		fail(http.StatusBadRequest, `need {"id":"<node id>"}`)
		return
	}
	invalidateNodes()
	var node *browserNode
	for _, n := range c.nodesJoined()["nodes"].([]browserNode) {
		if n.ID == in.ID {
			nn := n
			node = &nn
		}
	}
	if node == nil {
		fail(http.StatusNotFound, "no such node — see GET /nodes")
		return
	}
	if node.Seat != "" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"attached": node.ID, "seat": node.Seat, "already": true})
		return
	}
	if node.CDPURL == nil {
		fail(http.StatusConflict, "node has no cdp_url — nothing to hold (a host browser is attached by the pack, not here)")
		return
	}
	cdp := *node.CDPURL
	if cu, err := url.Parse(cdp); err == nil && cu.Hostname() == "0.0.0.0" {
		cdp = cu.Scheme + "://127.0.0.1:" + cu.Port()
	}
	ws, err := cdpVersion(cdp)
	bridged := false
	if err != nil && node.Container != nil && *node.Container != "" {
		// the inside-loopback case: bridge, then probe again. Direct argv, no
		// shell — the program's own quotes must never meet a shell's. The first
		// interpreter docker can exec wins (linuxserver images carry /lsiopy).
		for _, py := range []string{"python3", "/lsiopy/bin/python3", "python"} {
			if berr := exec.Command("docker", "exec", "-d", *node.Container, py, "-c", cdpBridgePy).Run(); berr == nil {
				bridged = true
				break
			}
		}
		for i := 0; bridged && i < 10 && err != nil; i++ {
			time.Sleep(300 * time.Millisecond)
			ws, err = cdpVersion(cdp)
		}
	}
	if err != nil {
		fail(http.StatusBadGateway, fmt.Sprintf("devtools at %s not reachable: %v (bridge tried: %v)", cdp, err, bridged))
		return
	}
	bin := channelBin()
	if bin == "" {
		fail(http.StatusServiceUnavailable, "channel broker binary not found (http-mcp build.sh → .bin/channel, or EIGHT_CHANNEL_BIN)")
		return
	}
	port := freePort(4450) // attached brokers sit beside fox's :4445, one per node
	logf, _ := os.OpenFile(filepath.Join(eightHome(), "channel-"+node.ID+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	cmd := exec.Command(bin, "-ws", ws, "-listen", "127.0.0.1:"+strconv.Itoa(port))
	if logf != nil {
		cmd.Stdout, cmd.Stderr = logf, logf
	}
	if err := cmd.Start(); err != nil {
		fail(http.StatusInternalServerError, "channel start: "+err.Error())
		return
	}
	go cmd.Wait() // reap; the broker's life is the browser's, not this request's
	base := "http://127.0.0.1:" + strconv.Itoa(port)
	up := false
	for i := 0; i < 25; i++ {
		time.Sleep(200 * time.Millisecond)
		if resp, err := (&http.Client{Timeout: time.Second}).Get(base + "/health"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				up = true
				break
			}
		}
		if cmd.ProcessState != nil {
			break
		}
	}
	if !up {
		fail(http.StatusBadGateway, "channel broker did not come up on "+base+" (see "+filepath.Join(eightHome(), "channel-"+node.ID+".log")+")")
		return
	}
	b := broker{id: node.ID, base: base}
	c.addBroker(b)
	registerSession(sessionRec{ID: node.ID, Hub: base, Kind: "local", Physics: "channel", Stream: "cdp", Upstream: redactOrigin(ws)})
	invalidateNodes()
	c.publish(fmt.Sprintf(`{"session":%q,"physics":"channel","origin":"COLLECTOR","frame":{"method":"node.attach","params":{"node":%q,"broker":%q,"upstream":%q,"bridged":%v,"pid":%d}}}`,
		node.ID, node.ID, base, redactOrigin(ws), bridged, cmd.Process.Pid))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"attached": node.ID, "seat": node.ID, "broker": base, "upstream": redactOrigin(ws), "bridged": bridged, "pid": cmd.Process.Pid})
}

// adoptChannelSeats — at boot, re-hold channel brokers a previous collector
// attached (they outlive it: the broker's life is the browser's). Only a
// broker whose /health answers is adopted; a dead one stays a tombstone on
// the rail exactly as before.
func (c *collector) adoptChannelSeats() {
	data, err := os.ReadFile(sessionsFile())
	if err != nil {
		return
	}
	latest := map[string]sessionRec{}
	for _, line := range strings.Split(string(data), "\n") {
		var rec sessionRec
		if line = strings.TrimSpace(line); line != "" && json.Unmarshal([]byte(line), &rec) == nil && rec.ID != "" {
			latest[rec.ID] = rec
		}
	}
	for _, rec := range latest {
		if rec.Physics != "channel" || !strings.HasPrefix(rec.Hub, "http") || c.find(rec.ID) != nil {
			continue
		}
		resp, err := (&http.Client{Timeout: 2 * time.Second}).Get(rec.Hub + "/health")
		if err != nil {
			continue
		}
		resp.Body.Close()
		if resp.StatusCode == 200 && c.addBroker(broker{id: rec.ID, base: rec.Hub}) {
			log.Printf("nodes: re-adopted channel seat %s on %s", rec.ID, rec.Hub)
		}
	}
}
