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
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
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
	brokers     []broker
	client      *http.Client
	geckoClient *http.Client // bounded timeout for geckodriver calls — a dead socket during a recycle can't hang chromeMu (the long-lived SSE pump keeps using client, which must have no timeout)
	gecko       string       // geckodriver session base (http://host:port/session/<id>) — enables /procinfo

	gmu         sync.Mutex  // guards gecko (swapped on session recovery)
	geckoRoot   string      // "http://host:port" prefix, for rebuilding the base on recovery
	sessionFile string      // ~/.8/gecko.json — where up.sh publishes the current SID
	recovering  atomic.Bool // debounce: only one session recovery runs at a time

	mu      sync.Mutex
	subs    map[int]chan string
	nextSub int64

	fmu    sync.Mutex
	frames map[string]chan []byte // sessionID -> latest CDP screencast JPEG (latest-wins)

	lmu    sync.Mutex
	ledger []reqRec // full request/response records — 8's witness ledger (replayable)
	lseq   int64

	// TAB MANIFEST — the durable answer to how-many / what / where / who / why / when
	// for every tab. Reconciled against getTree by a background loop (so it stays true
	// even when the cockpit is backgrounded/throttled). Persisted to ~/.8/manifest.json.
	tmu            sync.Mutex
	manifest       map[string]*tabRec // context-id -> record
	tabseq         int64
	pendOpen       []pendAttr // an agent declares intent before opening; the next-born tab claims it (provenance)
	manifestSeeded bool       // first reconcile after (re)start captures already-open tabs as "unknown" — the witness didn't see them born, so it must NOT claim "human"

	wmu     sync.Mutex        // #12 change-detector: a tab you WATCH pushes tab.changed on DOM shift
	watched map[string]string // ctx -> last DOM signature (opt-in; only watched tabs are read)

	pmu       sync.Mutex        // witness pane appearance (#277): pane_id -> first_seen
	panesSeen map[string]string // so a spawned pane is RECORDED (pane.appeared), not guessed

	xmu       sync.Mutex // #13 cross-substrate timeline: every seat's .changed in one line
	xtimeline []xEvent

	bmu     sync.Mutex
	benches []benchRec // benchmark batch results — clubbed + filterable on 8
	bseq    int64

	rmu     sync.Mutex
	recOn   bool     // a recording is in progress
	recName string   // name of the active recording
	recSeat string   // seat the recorded control is attributed to (operator|pilot|adapter|ai)
	recBuf  []reqRec // frames captured this recording → saved as a replayable series

	dmu      sync.Mutex
	dprCache map[string]float64    // context -> devicePixelRatio (act coord scaling)
	vpCache  map[string][2]float64 // context -> [innerWidth, innerHeight] CSS px (ratio act coords)

	// ATTENTION FOLLOWS ACTION: the last seat anything acted on. 8 polls /focus and
	// auto-foveates (pin + fan + zoom) to it, so driving a tab from the wire makes
	// the seer zoom to that card automatically — you never leave 8 to be "on" it.
	focusMu      sync.Mutex
	focusSession string
	focusContext string
	focusSeq     int64

	// FIREFOX STREAM (the efficient, leak-free path): the HiddenFrame driver POSTs
	// WebM clusters to /fxchunk; the collector relays them to the cockpit's MSE via
	// /fxstream. 8 only relays — capture lives in adapters/browser/firefox-stream.js.
	fxMu     sync.Mutex
	fxRecv   map[string]*fxStream // session -> received WebM relay state
	fxDriver string               // path to adapters/browser/firefox-stream.js
	fxShot   string               // path to adapters/browser/firefox-drawshot.js (leak-free periphery still)

	lastCapture  atomic.Int64 // unixnano of the last capture served (drawshot/shot/fxchunk) — gates the memory aperture
	chromeMu     sync.Mutex   // serializes /moz/context chrome<->content toggles so concurrent chrome-exec (drawshot/procinfo/aperture) don't corrupt each other's context state
	lastChromeOp time.Time    // guarded by chromeMu — for pacing chrome ops (feature A rate-limit)
}

// fxStream is one Firefox tab's live WebM relay: the init segment (first cluster,
// re-sent to every new /fxstream consumer so MSE can decode) + the live subscribers.
type fxStream struct {
	mu     sync.Mutex
	init   []byte
	subs   map[int]chan []byte
	nextID int
	chunks int
	bytes  int
}

func (c *collector) setFocus(session, context string) {
	c.focusMu.Lock()
	c.focusSession, c.focusContext, c.focusSeq = session, context, c.focusSeq+1
	c.focusMu.Unlock()
}

// activateCockpit brings Firefox HOME to 8 on startup: it finds the cockpit tab
// (the :8088 context) on the fox channel and activates it, so every collector
// (re)start returns the view to the seer instead of leaving Firefox on whatever
// tab was foreground. Retries while the broker settles.
func (c *collector) activateCockpit() {
	b := c.find("fox")
	if b == nil {
		return
	}
	for i := 0; i < 12; i++ {
		time.Sleep(time.Second)
		tr, err := c.command(b, `{"method":"browsingContext.getTree","params":{}}`)
		if err != nil {
			continue
		}
		var t struct {
			Result struct {
				Contexts []struct {
					Context string `json:"context"`
					URL     string `json:"url"`
				} `json:"contexts"`
			} `json:"result"`
		}
		if json.Unmarshal(tr, &t) != nil {
			continue
		}
		var cockpits []string
		for _, ctx := range t.Result.Contexts {
			if strings.Contains(ctx.URL, ":8088") {
				cockpits = append(cockpits, ctx.Context)
			}
		}
		if len(cockpits) == 0 {
			continue // no cockpit tab yet — keep retrying while the broker/session settle
		}
		// DEDUPE: keep ONE cockpit, close the rest. Every crash-revive opens a fresh
		// :8088 tab (new #c=) and session-restore brings the dead ones back; unchecked
		// they pile up (7 accumulated by 2026-07-30) and EACH cockpit captures all the
		// others — a runaway multiplier on parent graphics-surface memory that feeds the
		// crash-loop. Deduping on every (re)start bounds it to 1 and starves the leak.
		keep := cockpits[0]
		for _, extra := range cockpits[1:] {
			c.command(b, fmt.Sprintf(`{"method":"browsingContext.close","params":{"context":%q}}`, extra))
		}
		c.command(b, fmt.Sprintf(`{"method":"browsingContext.activate","params":{"context":%q}}`, keep))
		if n := len(cockpits) - 1; n > 0 {
			log.Printf("cockpit dedupe: kept %s, closed %d redundant cockpit(s) — starves the capture leak", keep, n)
		} else {
			log.Printf("brought Firefox home to 8 (activated cockpit %s)", keep)
		}
		return
	}
}

// witnessHeaders makes the REAFFERENCE ride back on the fire itself: an agent
// that fires through 8 learns, in the SAME response, that it was witnessed —
// which replayable frame it became, its physics, its latency, and where the
// gaze moved (attention follows action). The aha for an agent: using the wire
// through 8 means being remembered + replayable, for free, with no second call.
// Headers only — the body stays the pure op result (non-breaking). Exposed via
// CORS so the browser cockpit can surface it too.
func (c *collector) witnessHeaders(w http.ResponseWriter, ledgerID int64, physics string, latencyMs int64) {
	c.focusMu.Lock()
	ctx, seq := c.focusContext, c.focusSeq
	c.focusMu.Unlock()
	h := w.Header()
	h.Set("X-8-Ledger", strconv.FormatInt(ledgerID, 10))
	h.Set("X-8-Physics", physics)
	h.Set("X-8-Replayable", "true")
	h.Set("X-8-Focus-Seq", strconv.FormatInt(seq, 10))
	if ctx != "" {
		h.Set("X-8-Focus-Context", ctx)
	}
	gaze := ctx
	if gaze == "" {
		gaze = "—"
	}
	h.Set("X-8-Witness", fmt.Sprintf("seen · replayable frame #%d · %s · %dms · gaze→%s", ledgerID, physics, latencyMs, gaze))
}

func (c *collector) handleFocus(w http.ResponseWriter, r *http.Request) {
	c.focusMu.Lock()
	s, ctx, seq := c.focusSession, c.focusContext, c.focusSeq
	c.focusMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"session": s, "context": ctx, "seq": seq})
}

func newCollector(brokers []broker) *collector {
	return &collector{brokers: brokers, client: &http.Client{}, geckoClient: &http.Client{Timeout: 25 * time.Second}, subs: map[int]chan string{}, frames: map[string]chan []byte{}, dprCache: map[string]float64{}, vpCache: map[string][2]float64{}, fxRecv: map[string]*fxStream{}, manifest: map[string]*tabRec{}, panesSeen: map[string]string{}}
}

// chanDPR is a tab's devicePixelRatio (cached per context). The /stream screenshot
// is dpr-scaled — a 1796-CSS-px viewport at dpr=2 is a 3592px image — but BiDi
// input.performActions wants CSS pixels. Act coords arrive in screenshot space
// (the cockpit maps clicks to the frame's naturalWidth), so they must be divided
// by this or every click/scroll lands ~dpr× off ("controls on Firefox tabs don't
// work"). Cached: only the first act on a tab pays the extra round-trip.
func (c *collector) chanDPR(b *broker, ctx string) float64 {
	c.dmu.Lock()
	if d, ok := c.dprCache[ctx]; ok {
		c.dmu.Unlock()
		return d
	}
	c.dmu.Unlock()
	d := 1.0
	cmd := fmt.Sprintf(`{"method":"script.evaluate","params":{"awaitPromise":true,"target":{"context":%q},"expression":"window.devicePixelRatio"}}`, ctx)
	if tr, err := c.command(b, cmd); err == nil {
		var rr struct {
			Result struct {
				Result struct {
					Value float64 `json:"value"`
				} `json:"result"`
			} `json:"result"`
		}
		if json.Unmarshal(tr, &rr) == nil && rr.Result.Result.Value > 0 {
			d = rr.Result.Result.Value
		}
	}
	c.dmu.Lock()
	c.dprCache[ctx] = d
	c.dmu.Unlock()
	return d
}

// chanViewport returns the tab's CSS viewport [innerWidth, innerHeight], cached like
// chanDPR. Used to resolve resolution-independent act ratios (0..1) to CSS px — this
// is what makes clicks correct regardless of the /drawshot frame's LOD scale.
func (c *collector) chanViewport(b *broker, ctx string) (float64, float64) {
	c.dmu.Lock()
	if v, ok := c.vpCache[ctx]; ok {
		c.dmu.Unlock()
		return v[0], v[1]
	}
	c.dmu.Unlock()
	vw, vh := 0.0, 0.0
	cmd := fmt.Sprintf(`{"method":"script.evaluate","params":{"awaitPromise":true,"target":{"context":%q},"expression":"window.innerWidth+'x'+window.innerHeight"}}`, ctx)
	if tr, err := c.command(b, cmd); err == nil {
		var rr struct {
			Result struct {
				Result struct {
					Value string `json:"value"`
				} `json:"result"`
			} `json:"result"`
		}
		if json.Unmarshal(tr, &rr) == nil {
			if p := strings.Split(rr.Result.Result.Value, "x"); len(p) == 2 {
				vw, _ = strconv.ParseFloat(p[0], 64)
				vh, _ = strconv.ParseFloat(p[1], 64)
			}
		}
	}
	if vw > 0 && vh > 0 {
		c.dmu.Lock()
		c.vpCache[ctx] = [2]float64{vw, vh}
		c.dmu.Unlock()
	}
	return vw, vh
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
	if c.geckoBase() == "" {
		http.Error(w, `{"error":"procinfo disabled: collector started without -gecko"}`, http.StatusServiceUnavailable)
		return
	}
	c.chromeMu.Lock()         // procinfo toggles /moz/context too — serialize with drawshot/aperture
	defer c.chromeMu.Unlock() // runs LAST (after the content-reset defer below)
	c.paceChrome()
	post := func(path, body string) ([]byte, error) {
		return c.geckoPost("POST", path, body) // auto-recovers the session on recycle
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

// drawProbeScript runs in the chrome/parent context: call WindowGlobalParent.
// drawSnapshot() ~250x on a background tab, bounded buffer reuse, and report the
// parent-process memory delta. The DECISIVE test: if drawSnapshot leaks the parent
// like captureScreenshot, the MediaRecorder plan is moot; if memory stays flat,
// the peer's "the leak is the BiDi base64-hold, drawSnapshot bypasses it" holds.
const drawProbeScript = `const cb=arguments[arguments.length-1];(async()=>{try{const wins=gBrowser.browsers.filter(b=>b.browsingContext&&b.browsingContext.currentWindowGlobal);const tgt=wins.find(b=>/youtube|excalidraw|example|lambdatest|deepseek/.test(b.currentURI.spec))||wins[0];const wg=tgt.browsingContext.currentWindowGlobal;const m0=(await ChromeUtils.requestProcInfo()).memory;let n=0,errs=0,lastErr='';for(let i=0;i<250;i++){try{const bmp=await wg.drawSnapshot(null,1.0,'white');if(bmp&&bmp.close)bmp.close();n++;}catch(e){errs++;lastErr=''+e;if(errs>2)break;}}try{if(typeof Cu!=='undefined'&&Cu.forceGC){Cu.forceGC();Cu.forceCC&&Cu.forceCC();}}catch(e){}const m1=(await ChromeUtils.requestProcInfo()).memory;cb(JSON.stringify({frames:n,errs:errs,lastErr:lastErr.slice(0,140),url:tgt.currentURI.spec.slice(0,60),parent_mb_before:Math.round(m0/1048576),parent_mb_after:Math.round(m1/1048576),delta_mb:Math.round((m1-m0)/1048576)}));}catch(e){cb('ERR:'+e);}})();`

// streamProbeScript tests the TRANSFER: create a HiddenFrame (content window with
// a DOM), then try to draw the parent's drawSnapshot ImageBitmap DIRECTLY into a
// canvas in it + canvas.captureStream. If directDrawSameProcess is true, the whole
// encode pipeline can live in the HiddenFrame with NO JSWindowActor cross-process
// transfer — a big simplification. If false, the peer's JSWindowActor path is needed.
const streamProbeScript = `const cb=arguments[arguments.length-1];(async()=>{try{const {HiddenFrame}=ChromeUtils.importESModule('resource://gre/modules/HiddenFrame.sys.mjs');const hf=new HiddenFrame();const win=await hf.get();const hasMR=typeof win.MediaRecorder!=='undefined';const tgt=gBrowser.browsers.find(b=>/youtube|excalidraw|example|lambdatest/.test(b.currentURI.spec))||gBrowser.browsers[0];const wg=tgt.browsingContext.currentWindowGlobal;const bmp=await wg.drawSnapshot(null,1,'white');const w=bmp.width,h=bmp.height;let directDraw=false,capStream=false,detail='';try{const doc=win.document;const canvas=doc.createElement('canvas');canvas.width=w;canvas.height=h;const ctx=canvas.getContext('2d');ctx.drawImage(bmp,0,0);directDraw=true;const st=canvas.captureStream(5);capStream=st.getVideoTracks().length>0;detail='tracks='+st.getVideoTracks().length;}catch(e){detail='draw/capture-failed: '+e;}if(bmp.close)bmp.close();cb(JSON.stringify({hiddenFrameOK:!!win,winHasMediaRecorder:hasMR,snapW:w,snapH:h,directDrawSameProcess:directDraw,captureStreamOK:capStream,detail:(''+detail).slice(0,160)}));}catch(e){cb('ERR:'+e);}})();`

// runChrome switches the geckodriver session to the chrome/parent context, runs
// one /execute/async script (the privileged path drawSnapshot + ChromeUtils need),
// returns to content, and writes the callback result. Shared by /procinfo-style
// probes and the Firefox stream driver injection.
// waitReady holds the collector's Firefox-touching goroutines until Firefox is up
// after a (re)start — a getTree probe through the broker, with backoff. Barraging a
// still-starting Firefox crashes it (feature A). Bounded so we never hang forever.
func (c *collector) waitReady() {
	b := c.find("fox")
	if b == nil {
		return
	}
	backoff := time.Second
	for i := 0; i < 12; i++ {
		if tr, err := c.command(b, `{"method":"browsingContext.getTree","params":{"maxDepth":0}}`); err == nil && bytes.Contains(tr, []byte(`"context"`)) {
			log.Printf("collector: firefox ready (getTree probe ok after %v)", time.Duration(i)*time.Second)
			return
		}
		time.Sleep(backoff)
		backoff = min(backoff*2, 8*time.Second)
	}
	log.Printf("collector: firefox readiness probe gave up — starting goroutines anyway")
}

// paceChrome enforces a minimum interval between chrome-context ops (feature A
// rate-limit). Called under chromeMu, so all chrome exec (drawshot/procinfo/aperture)
// is spaced out — even serialized, high-frequency ops load a fragile Firefox.
func (c *collector) paceChrome() {
	const minGap = 100 * time.Millisecond
	if !c.lastChromeOp.IsZero() {
		if d := time.Since(c.lastChromeOp); d < minGap {
			time.Sleep(minGap - d)
		}
	}
	c.lastChromeOp = time.Now()
}

// geckoBase returns the current geckodriver session base under lock (recovery may
// swap it out from under a caller when Firefox recycles).
func (c *collector) geckoBase() string {
	c.gmu.Lock()
	defer c.gmu.Unlock()
	return c.gecko
}

// readGeckoSession reads the SID up.sh publishes to ~/.8/gecko.json. The write is
// atomic (temp + rename), so a plain read always sees a complete file.
func (c *collector) readGeckoSession() string {
	if c.sessionFile == "" {
		return ""
	}
	b, err := os.ReadFile(c.sessionFile)
	if err != nil {
		return ""
	}
	var v struct {
		SessionID string `json:"session_id"`
	}
	json.Unmarshal(b, &v)
	return v.SessionID
}

// recoverGecko re-derives the collector's geckodriver session after a Firefox recycle
// (SESSION AUTO-RECOVERY, feature B). It DISCOVERS the current SID from the file up.sh
// publishes — it never CREATES a session (that would orphan sessions and fight up.sh /
// pilot for ownership). Validated before use (probe /url); if the discovered session is
// itself already dead, it retries with backoff (up.sh may still be re-establishing).
// Globally debounced so only one recovery runs at a time. Returns true if it swapped in
// a live session.
func (c *collector) recoverGecko() bool {
	if !c.recovering.CompareAndSwap(false, true) {
		return false // another recovery already in flight
	}
	defer c.recovering.Store(false)
	for _, backoff := range []time.Duration{0, time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second} {
		if backoff > 0 {
			time.Sleep(backoff)
		}
		sid := c.readGeckoSession()
		if sid == "" {
			continue
		}
		base := c.geckoRoot + "/session/" + sid
		if base == c.geckoBase() {
			continue // file still points at the session we know is stale — wait for up.sh
		}
		req, _ := http.NewRequest("GET", base+"/url", nil)
		resp, err := c.geckoClient.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode == 200 && !bytes.Contains(body, []byte("invalid session")) {
			c.gmu.Lock()
			c.gecko = base
			c.gmu.Unlock()
			c.dmu.Lock() // context ids changed with the new session — drop the caches
			c.dprCache = map[string]float64{}
			c.vpCache = map[string][2]float64{}
			c.dmu.Unlock()
			log.Printf("collector: recovered gecko session -> %s", sid)
			return true
		}
	}
	log.Printf("collector: gecko recovery failed (file SID stale/unreachable)")
	return false
}

// geckoPost sends one request to the current gecko session; on "invalid session id"
// (Firefox recycled) it recovers the session and retries once on the fresh one.
func (c *collector) geckoPost(method, path, body string) ([]byte, error) {
	do := func() ([]byte, error) {
		var r io.Reader
		if body != "" {
			r = strings.NewReader(body)
		}
		req, _ := http.NewRequest(method, c.geckoBase()+path, r)
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.geckoClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(resp.Body)
	}
	out, err := do()
	if err == nil && bytes.Contains(out, []byte("invalid session id")) && c.recoverGecko() {
		out, err = do() // retry once on the recovered session
	}
	return out, err
}

func (c *collector) runChrome(w http.ResponseWriter, script []byte, args []any) {
	c.chromeMu.Lock()         // hold the chrome context for the whole toggle+exec+untoggle
	defer c.chromeMu.Unlock() // runs LAST (after the content-reset defer below)
	c.paceChrome()            // rate-limit chrome ops so we don't overload a fragile Firefox
	post := func(path, body string) ([]byte, error) {
		return c.geckoPost("POST", path, body)
	}
	post("/timeouts", `{"script":60000}`)
	if _, err := post("/moz/context", `{"context":"chrome"}`); err != nil {
		http.Error(w, `{"error":"chrome context: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer post("/moz/context", `{"context":"content"}`)
	body, _ := json.Marshal(map[string]any{"script": string(script), "args": args})
	out, err := post("/execute/async", string(body))
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	var v struct {
		Value json.RawMessage `json:"value"`
	}
	json.Unmarshal(out, &v)
	w.Header().Set("Content-Type", "application/json")
	var inner string
	if json.Unmarshal(v.Value, &inner) == nil {
		if strings.HasPrefix(inner, "ERR") {
			w.Write([]byte(`{"error":` + strconv.Quote(inner) + `}`))
			return
		}
		w.Write([]byte(inner))
		return
	}
	w.Write(v.Value)
}

// execChrome runs one chrome-context /execute/async script and returns the callback
// string — the internal sibling of runChrome (which writes an HTTP response). Used by
// the memory aperture. geckodriver serializes commands per session, so this is safe
// alongside concurrent /drawshot / /procinfo calls.
func (c *collector) execChrome(script string) (string, error) {
	c.chromeMu.Lock()
	defer c.chromeMu.Unlock()
	c.paceChrome()
	post := func(path, body string) ([]byte, error) {
		return c.geckoPost("POST", path, body)
	}
	post("/timeouts", `{"script":30000}`) // async scripts need a script timeout, or /execute/async returns empty
	if _, err := post("/moz/context", `{"context":"chrome"}`); err != nil {
		return "", err
	}
	defer post("/moz/context", `{"context":"content"}`)
	body, _ := json.Marshal(map[string]any{"script": script, "args": []any{}})
	out, err := post("/execute/async", string(body))
	if err != nil {
		return "", err
	}
	var v struct {
		Value json.RawMessage `json:"value"`
	}
	json.Unmarshal(out, &v)
	var s string
	json.Unmarshal(v.Value, &s)
	return s, nil
}

// memoryAperture is the WITNESS's self-regulation (8 recommends → 8 acts on ITSELF's
// consumption, never the tab). drawSnapshot capture accumulates GRAPHICS-SURFACE
// memory in Firefox's parent process (NOT JS heap — forceGC reclaims ~0; heap-minimize
// reclaimed ~66% in validation) that otherwise only frees on ~6min idle. When the
// parent climbs past a SOFT threshold this fires memory-pressure/heap-minimize (the
// about:memory "Minimize memory usage" path), flushing that cache so the parent holds
// a healthy band WITHOUT the watchdog's blunt full recycle at 4500 (kept as a
// backstop). Gated on RECENT capture (nothing accumulating if idle) and a COOLDOWN (a
// flush is a GC pause — don't thrash). Publishes the act so 8 sees its own efferent.
func (c *collector) memoryAperture(softMB int) {
	if c.geckoBase() == "" || softMB <= 0 {
		return
	}
	// FAIR APERTURE (the operator's law, 2026-07-24): the INVARIANT is the tabs —
	// their identities are conserved, the system never closes one. Memory LIVENESS
	// is the budgeted resource, allocated fairly: under genuine shortage the
	// heaviest non-focused tab pays first (Stochastic-Fair-BLUE semantics), parked
	// via gBrowser.discardBrowser — unloaded from RAM, kept in the strip, reloads
	// on focus. Open != loaded, exactly as lease != liveness. Per-tab accounting is
	// exact (N is small); a bloom-filter variant only earns its keep at thousands.
	const cooldown = 2 * time.Minute
	// parent-only: the aperture targets the parent's compositor-surface leak; content
	// processes of live tabs are legitimate spend. Budgeting the TOTAL made a 12-tab
	// seat (baseline ~4GB) park a victim every tick forever (2026-07-24 over-parking).
	memScript := `const cb=arguments[arguments.length-1];ChromeUtils.requestProcInfo().then(i=>cb(''+Math.round(i.memory/1048576))).catch(e=>cb('ERR'));`
	// flushScript is about:memory's "Minimize memory usage": three memory-pressure
	// notifications (the third with heap-minimize semantics), which is what actually
	// releases the parent's capture-accumulated compositor surfaces. Validated
	// earlier at ~66% parent reclaim; forceGC alone reclaims ~0 (it is not JS heap).
	flushScript := `const cb=arguments[arguments.length-1];(async()=>{try{
for(let i=0;i<3;i++){Services.obs.notifyObservers(null,"memory-pressure","heap-minimize");await new Promise(r=>setTimeout(r,250));}
cb("3");}catch(e){cb("ERR:"+e)}})();`
	parkScript := `const cb=arguments[arguments.length-1];(async()=>{try{
const win=Services.wm.getMostRecentWindow("navigator:browser");const gB=win.gBrowser;
const info=await ChromeUtils.requestProcInfo();const mem={};for(const ch of info.children){mem[ch.pid]=(ch.memory||0)/1048576;}
const cands=[];
for(const tab of gB.tabs){
  if(tab===gB.selectedTab)continue;
  if(tab.getAttribute("pending")==="true")continue;
  const br=tab.linkedBrowser;if(!br)continue;
  const url=(br.currentURI&&br.currentURI.spec)||"";
  if(url.startsWith("http://localhost:8088"))continue;
  let pid=0;try{pid=br.browsingContext.currentWindowGlobal.osPid||0}catch(e){}
  cands.push({u:url.slice(0,90),mb:Math.round(mem[pid]||0),tab});
}
cands.sort((a,b)=>b.mb-a.mb);
if(!cands.length){cb(JSON.stringify({parked:null}));return;}
const v=cands[0];gB.discardBrowser(v.tab);
cb(JSON.stringify({parked:v.u,mb:v.mb}));
}catch(e){cb("ERR:"+e)}})();`
	var lastFire, lastRecycle time.Time
	recycleMB := recycleThresholdMB() // RAM-aware proactive-recycle bound (cross-platform, in Go)
	const recycleCooldown = 5 * time.Minute
	log.Printf("aperture: watching (soft=%dMB, recycle=%dMB, cooldown=%s)", softMB, recycleMB, cooldown)
	for {
		time.Sleep(30 * time.Second)
		if n := c.lastCapture.Load(); n == 0 || time.Since(time.Unix(0, n)) > 90*time.Second {
			continue // idle — nothing accumulating, nothing to flush
		}
		if !lastFire.IsZero() && time.Since(lastFire) < cooldown {
			continue
		}
		out, err := c.execChrome(memScript)
		if err != nil {
			continue
		}
		before, err := strconv.Atoi(strings.TrimSpace(out))
		if err != nil || before < softMB {
			continue
		}
		// STEP 1 — flush the compartment that actually grows: the measured number
		// is the PARENT process, and what bloats it is the drawSnapshot compositor-
		// surface cache (capture-driven; frees on its own only after ~6min idle).
		// Parking a CONTENT tab frees a child process — the wrong compartment —
		// which is why park-only ticks often didn't lower the number (2026-07-27
		// crash-loop: parent climbed 6-8GB past the 4500 recycle bound while the
		// aperture dutifully parked ~230MB tabs). heap-minimize is the about:memory
		// "Minimize memory usage" path this function's contract always promised.
		flushed, err := c.execChrome(flushScript)
		if err != nil {
			continue
		}
		lastFire = time.Now()
		mid, _ := c.execChrome(memScript)
		mmb, _ := strconv.Atoi(strings.TrimSpace(mid))
		c.publish(fmt.Sprintf(`{"session":"fox","origin":"COLLECTOR","frame":{"method":"aperture.flush","params":{"before_mb":%d,"after_mb":%d,"passes":%s}}}`, before, mmb, strings.TrimSpace(flushed)))
		log.Printf("aperture: parent %dMB > %dMB soft -> heap-minimize -> %dMB", before, softMB, mmb)
		if mmb < softMB {
			continue // the flush alone brought the parent home — no victim needed
		}
		// STEP 2 (secondary) — genuine shortage even after the flush: FAIR-park the
		// heaviest non-focused tab (content memory is real spend; identity conserved).
		parked, err := c.execChrome(parkScript)
		if err != nil {
			continue
		}
		after, _ := c.execChrome(memScript)
		amb, _ := strconv.Atoi(strings.TrimSpace(after))
		c.publish(fmt.Sprintf(`{"session":"fox","origin":"COLLECTOR","frame":{"method":"aperture.park","params":{"before_mb":%d,"after_mb":%d,"victim":%s}}}`, mmb, amb, strings.TrimSpace(parked)))
		log.Printf("aperture: still %dMB after flush -> FAIR park heaviest -> %dMB | victim=%s", mmb, amb, strings.TrimSpace(parked))
		// STEP 3 (last resort) — flush+park couldn't bring the parent home and it is
		// above the RAM-aware recycle bound: the graphics-surface leak is un-reclaimable,
		// so RECYCLE Firefox before the OOM crash (the watchdog revives it). FLOW 10, now
		// decided in the CROSS-PLATFORM collector instead of two OS-specific shell watch-
		// dogs — the bloat is capture-driven (collector-driven), so this is where it belongs.
		if recycleMB > 0 && amb >= recycleMB && time.Since(lastRecycle) > recycleCooldown {
			lastRecycle = time.Now()
			// AUTO-DEFER: where a multi-agent lease ledger owns recycle (omarchy
			// ~/.8/leases.json) the watchdog closes tabs gracefully BY LEASE before
			// killing — so the collector must not pkill out from under it. Single-agent
			// machines (mac, no leases file) recycle directly here.
			if _, err := os.Stat(os.ExpandEnv("$HOME/.8/leases.json")); err == nil {
				log.Printf("aperture: parent %dMB >= %dMB — deferring recycle to lease-aware watchdog", amb, recycleMB)
			} else {
				log.Printf("aperture: parent %dMB >= %dMB recycle bound -> proactive recycle (FLOW 10)", amb, recycleMB)
				c.publish(fmt.Sprintf(`{"session":"fox","origin":"COLLECTOR","frame":{"method":"aperture.recycle","params":{"parent_mb":%d,"bound_mb":%d}}}`, amb, recycleMB))
				_ = exec.Command("pkill", "-f", "firefox.*ltqa-firefox-deepseek").Run()
			}
		}
	}
}

// handleFxChunk receives one WebM cluster from the HiddenFrame driver (POST body =
// raw bytes) and fans it to the session's /fxstream consumers. The FIRST cluster is
// the init segment (EBML header + tracks) — kept and re-sent to every new consumer.
func (c *collector) handleFxChunk(w http.ResponseWriter, r *http.Request) {
	c.lastCapture.Store(time.Now().UnixNano())
	sid := r.URL.Query().Get("session")
	if sid == "" {
		sid = "fox"
	}
	buf, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if len(buf) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	c.fxMu.Lock()
	if c.fxRecv == nil {
		c.fxRecv = map[string]*fxStream{}
	}
	s := c.fxRecv[sid]
	if s == nil {
		s = &fxStream{subs: map[int]chan []byte{}}
		c.fxRecv[sid] = s
	}
	c.fxMu.Unlock()
	s.mu.Lock()
	if s.init == nil {
		s.init = append([]byte(nil), buf...) // first cluster carries the init segment
	}
	s.chunks++
	s.bytes += len(buf)
	for _, ch := range s.subs {
		select {
		case ch <- buf:
		default:
		}
	}
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

// handleFxStream serves a session's live WebM to the cockpit: the init segment
// first, then every subsequent cluster. The cockpit appends these to an MSE
// SourceBuffer (mode=sequence). Chunked transfer; runs until the client leaves.
func (c *collector) handleFxStream(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session")
	if sid == "" {
		sid = "fox"
	}
	c.fxMu.Lock()
	s := c.fxRecv[sid]
	c.fxMu.Unlock()
	if s == nil {
		http.Error(w, `{"error":"no fx stream for session"}`, http.StatusNotFound)
		return
	}
	ch := make(chan []byte, 64)
	s.mu.Lock()
	id := s.nextID
	s.nextID++
	s.subs[id] = ch
	init := s.init
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.subs, id); s.mu.Unlock() }()
	w.Header().Set("Content-Type", "video/webm")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	if len(init) > 0 {
		w.Write(init)
		if flusher != nil {
			flusher.Flush()
		}
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case buf := <-ch:
			if _, err := w.Write(buf); err != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
}

// handleFxStart injects the Firefox capture driver (adapters/browser/firefox-
// stream.js) into the chrome context for one tab, so it POSTs WebM to /fxchunk.
// The capture MECHANISM lives in adapters; 8 only orchestrates + relays.
func (c *collector) handleFxStart(w http.ResponseWriter, r *http.Request) {
	if c.geckoBase() == "" {
		http.Error(w, `{"error":"disabled: collector started without -gecko"}`, http.StatusServiceUnavailable)
		return
	}
	driver, err := os.ReadFile(c.fxDriver)
	if err != nil {
		http.Error(w, `{"error":"driver not readable at `+c.fxDriver+`: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	needle := r.URL.Query().Get("needle")
	if needle == "" {
		needle = "excalidraw"
	}
	fps := 5.0
	if v, e := strconv.ParseFloat(r.URL.Query().Get("fps"), 64); e == nil && v > 0 {
		fps = v
	}
	sid := r.URL.Query().Get("session")
	if sid == "" {
		sid = "fox"
	}
	// reset the relay buffer for a fresh stream (drop the stale init segment)
	c.fxMu.Lock()
	delete(c.fxRecv, sid)
	c.fxMu.Unlock()
	chunkURL := fmt.Sprintf("http://127.0.0.1:7070/fxchunk?session=%s", sid)
	c.runChrome(w, driver, []any{chunkURL, needle, fps})
}

// handleDrawShot is the LEAK-FREE periphery still: drawSnapshot -> canvas -> JPEG,
// run from adapters/browser/firefox-drawshot.js. It REPLACES the /shot BiDi
// captureScreenshot path for Firefox periphery tiles — captureScreenshot holds each
// frame's base64 in the parent process (the FLOW-10 recycle sawtooth); drawSnapshot
// does not. The response shape matches /shot ({context,data}) so the tile is
// engine-agnostic. Poll this at a LOW cadence (the hero stream is the only high-rate
// source); ?w= sets the target width (LOD), ?q= the JPEG quality.
func (c *collector) handleDrawShot(w http.ResponseWriter, r *http.Request) {
	if c.geckoBase() == "" {
		http.Error(w, `{"error":"disabled: collector started without -gecko"}`, http.StatusServiceUnavailable)
		return
	}
	c.lastCapture.Store(time.Now().UnixNano())
	script, err := os.ReadFile(c.fxShot)
	if err != nil {
		http.Error(w, `{"error":"drawshot driver not readable at `+c.fxShot+`: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	needle := r.URL.Query().Get("needle")
	if needle == "" {
		needle = r.URL.Query().Get("context") // tolerate the /shot-style param name
	}
	q := 0.6
	if v, e := strconv.ParseFloat(r.URL.Query().Get("q"), 64); e == nil && v > 0 {
		q = v
	}
	scale := 0.5 // render scale (0..1) — the cockpit sends small for periphery, larger for hero
	if v, e := strconv.ParseFloat(r.URL.Query().Get("s"), 64); e == nil && v > 0 {
		scale = v
	}
	c.runChrome(w, script, []any{needle, scale, q})
}

// handleFxStop stops the running pipeline (releases the MediaRecorder/HiddenFrame).
func (c *collector) handleFxStop(w http.ResponseWriter, r *http.Request) {
	if c.geckoBase() == "" {
		http.Error(w, `{"error":"disabled: collector started without -gecko"}`, http.StatusServiceUnavailable)
		return
	}
	// Find the window the SAME way the driver stored the handle
	// (gBrowser.ownerDocument.defaultView) — Services.wm.getMostRecentWindow can
	// resolve to a DIFFERENT chrome window than the one __eightFx lives on, so the
	// stop reported 'none' while the recorder kept running (the 2.7h runaway). Match
	// the store path and the stop reliably releases the encoder.
	stop := `const cb=arguments[arguments.length-1];try{const w=gBrowser.ownerDocument.defaultView;if(w.__eightFx){w.__eightFx.stop();cb('stopped');}else{cb('none');}}catch(e){cb('ERR:'+e);}`
	c.runChrome(w, []byte(stop), []any{})
}

// handleFxStats reports the relay counters for a session (for the standalone test).
func (c *collector) handleFxStats(w http.ResponseWriter, r *http.Request) {
	sid := r.URL.Query().Get("session")
	if sid == "" {
		sid = "fox"
	}
	c.fxMu.Lock()
	s := c.fxRecv[sid]
	c.fxMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if s == nil {
		w.Write([]byte(`{"chunks":0,"bytes":0,"init":false}`))
		return
	}
	s.mu.Lock()
	chunks, bytes, hasInit, subs := s.chunks, s.bytes, len(s.init) > 0, len(s.subs)
	s.mu.Unlock()
	json.NewEncoder(w).Encode(map[string]any{"chunks": chunks, "bytes": bytes, "init": hasInit, "subscribers": subs})
}

// handleFxDiag reads the DRIVER's own counters (ondataavailable fires + fetch
// errors) from inside Firefox — distinguishes "no frames produced" from "POST failing".
func (c *collector) handleFxDiag(w http.ResponseWriter, r *http.Request) {
	if c.geckoBase() == "" {
		http.Error(w, `{"error":"disabled: collector started without -gecko"}`, http.StatusServiceUnavailable)
		return
	}
	s := `const cb=arguments[arguments.length-1];try{const w=gBrowser.ownerDocument.defaultView;cb(w.__eightFx?JSON.stringify(w.__eightFx.stats()):'no-pipeline');}catch(e){cb('ERR:'+e);}`
	c.runChrome(w, []byte(s), []any{})
}

func (c *collector) handleDrawProbe(w http.ResponseWriter, r *http.Request) {
	if c.geckoBase() == "" {
		http.Error(w, `{"error":"disabled: collector started without -gecko"}`, http.StatusServiceUnavailable)
		return
	}
	script := drawProbeScript
	if r.URL.Query().Get("p") == "stream" {
		script = streamProbeScript
	}
	c.runChrome(w, []byte(script), []any{})
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

// streamCDP is the EFFICIENT stream (Chrome): Page.startScreencast makes Chrome
// PUSH frames (the pump routes them to frameChan + acks) — capture ONCE, encode
// continuously, no repeated captureScreenshot. That kills both failure modes of
// the poll loop: the parent-process leak and the single-BiDi-socket saturation
// that was false-tripping the watchdog. maxWidth gives LOD natively (Chrome
// downscales before it sends). One start per stream; stop on disconnect.
func (c *collector) streamCDP(w http.ResponseWriter, r *http.Request, b *broker, quality float64, maxW int) {
	c.command(b, `{"method":"Page.enable","params":{}}`)
	start := fmt.Sprintf(`{"method":"Page.startScreencast","params":{"format":"jpeg","quality":%d,"maxWidth":%d,"maxHeight":%d}}`, int(quality*100), maxW, maxW*2)
	if _, err := c.command(b, start); err != nil {
		http.Error(w, `{"error":"startScreencast: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer c.command(b, `{"method":"Page.stopScreencast","params":{}}`)
	// Own the broker's /events for the life of this stream: read frame -> ack ->
	// write. (The shared pump's frameChan indirection only ever delivered the first
	// frame; a single self-contained consumer that acks each frame streams cleanly,
	// proven at ~10fps.) ack must happen per frame or Chrome sends exactly one.
	// DEDICATED client + transport — not the shared c.client. The fox pump holds a
	// long-lived /events read on c.client; sharing its transport pool starved this
	// stream down to a single frame. Its own transport isolates the SSE read.
	evClient := &http.Client{Transport: &http.Transport{}}
	req, _ := http.NewRequestWithContext(r.Context(), "GET", b.base+"/events", nil)
	resp, err := evClient.Do(req)
	if err != nil {
		http.Error(w, `{"error":"events: `+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	defer evClient.CloseIdleConnections()
	w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	ackClient := &http.Client{Timeout: 3 * time.Second}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") || !strings.Contains(line, `"Page.screencastFrame"`) {
			continue
		}
		var ev struct {
			Params struct {
				Data      string `json:"data"`
				SessionID int    `json:"sessionId"`
			} `json:"params"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &ev) != nil || ev.Params.Data == "" {
			continue
		}
		// ack first so Chrome keeps producing while we decode+write this frame
		go func(id int) {
			if rp, e := ackClient.Post(b.base+"/command", "application/json", strings.NewReader(fmt.Sprintf(`{"method":"Page.screencastFrameAck","params":{"sessionId":%d}}`, id))); e == nil {
				rp.Body.Close()
			}
		}(ev.Params.SessionID)
		raw, derr := base64.StdEncoding.DecodeString(ev.Params.Data)
		if derr != nil || len(raw) == 0 {
			continue
		}
		fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(raw))
		if _, werr := w.Write(raw); werr != nil {
			return
		}
		io.WriteString(w, "\r\n")
		if flusher != nil {
			flusher.Flush()
		}
	}
}

// publish fans one frame to every /feed subscriber, non-blocking so a slow
// consumer never stalls the wire.
func (c *collector) publish(frame string) {
	// #13 CROSS-SUBSTRATE TIMELINE: every `<seat>.changed` push (tmux/nvim/daemons/
	// tab) is one substrate's tick in ONE interleaved, replayable line — a human
	// types in nvim, a tmux agent runs a command, a tab navigates, all one sequence.
	// Capture them (ts-stamped) into a bounded ring; /timeline serves it; two
	// captures diff to show "what an agent did differently this run."
	if strings.Contains(frame, ".changed") {
		c.xmu.Lock()
		c.xtimeline = append(c.xtimeline, xEvent{TS: time.Now().UTC().Format("15:04:05.000"), Frame: frame})
		if len(c.xtimeline) > 800 {
			c.xtimeline = c.xtimeline[len(c.xtimeline)-800:]
		}
		c.xmu.Unlock()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, ch := range c.subs {
		select {
		case ch <- frame:
		default:
		}
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// handleProvenance (#29) is the anomaly's fix made into a real query: the
// actor→atom→surface flow. The ledger recorded WHO (declared actor) fired WHICH
// atom (call|channel) onto WHICH surface (session/tab). Previously provenance was
// by SESSION, so every surface on one broker shared a count and the actor was
// invisible. Now: group by declared actor; per actor, count atoms and list the
// surfaces they touched. Acts with no declared actor land under "" (undeclared) —
// counted honestly, never back-filled to "operator". ?actor= filters to one.
func (c *collector) handleProvenance(w http.ResponseWriter, r *http.Request) {
	c.lmu.Lock()
	led := make([]reqRec, len(c.ledger))
	copy(led, c.ledger)
	c.lmu.Unlock()

	// #29b: an actor is AUTHENTICATED if it holds a LIVE lease (a claimed tab,
	// fresh <90s). The witness does NOT reject a spoofed actor (open model) — it
	// SHOWS the trust bit, so a declared-but-unleased "rishi-the-operator" is
	// visibly authenticated:false. Attributable AND (where leased) authenticated.
	leased := map[string]bool{}
	c.tmu.Lock()
	for _, rec := range c.manifest {
		if rec != nil && rec.ClaimedBy != "" && claimLive(rec) {
			leased[rec.ClaimedBy] = true
		}
	}
	c.tmu.Unlock()

	only := r.URL.Query().Get("actor")
	type actorFlow struct {
		Actor         string         `json:"actor"`         // "" = undeclared
		Authenticated bool           `json:"authenticated"` // #29b: holds a live lease (not just declared)
		Acts          int            `json:"acts"`
		Atoms         map[string]int `json:"atoms"`    // call | channel → count
		Surfaces      map[string]int `json:"surfaces"` // session/tab/host → count
		Methods       map[string]int `json:"methods"`  // the operations they fired
	}
	flows := map[string]*actorFlow{}
	declared, undeclared := 0, 0
	for _, e := range led {
		if e.Actor == "" {
			undeclared++
		} else {
			declared++
		}
		if only != "" && e.Actor != only {
			continue
		}
		f := flows[e.Actor]
		if f == nil {
			f = &actorFlow{Actor: e.Actor, Authenticated: leased[e.Actor], Atoms: map[string]int{}, Surfaces: map[string]int{}, Methods: map[string]int{}}
			flows[e.Actor] = f
		}
		f.Acts++
		f.Atoms[e.Physics]++
		switch {
		case e.Session != "":
			f.Surfaces[e.Session]++
		case e.URL != "": // #29b: a CALL atom's surface is its URL host — fold it in so actor→atom→surface is complete for BOTH atoms
			if u, err := url.Parse(e.URL); err == nil && u.Host != "" {
				f.Surfaces[u.Host]++
			}
		}
		if e.Method != "" {
			f.Methods[e.Method]++
		}
	}
	out := make([]*actorFlow, 0, len(flows))
	for _, f := range flows {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Acts > out[j].Acts })
	w.Header().Set("Content-Type", "application/json")
	// coverage is the honest headline: what fraction of witnessed acts actually
	// declared who did them. This is the anomaly's own progress meter.
	json.NewEncoder(w).Encode(map[string]any{
		"flows":            out,
		"declared":         declared,
		"undeclared":       undeclared,
		"declared_frac":    ratio(declared, declared+undeclared),
		"ledger_in_window": len(led),
		"note":             "actor→atom→surface. \"\" = undeclared (honest, not back-filled). Declare via the X-8-Actor header or an \"actor\" body field on /fetch /run /act.",
	})
}

func ratio(a, total int) float64 {
	if total == 0 {
		return 0
	}
	return float64(a) / float64(total)
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
	// CDP (Chrome) brokers: streamCDP owns /events on demand and MUST be the sole
	// consumer — it acks each screencast frame, and Chrome sends exactly one frame
	// until acked. A second /events consumer here would race it and starve the ack.
	if b.id != "fox" {
		return
	}
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
					continue
				}
				// THE WITNESS MUST NOT WITNESS ITS OWN NERVOUS SYSTEM. The cockpit's
				// own traffic to the collector (:7070) and to itself (:8088) — /shot,
				// /stream, /procinfo, /sessions, /act, /feed — otherwise reflects back
				// as network events (~91% of the feed): a recursive loop that churns
				// memory + adds latency (each render triggers more polls). Drop it.
				if strings.Contains(payload, `"network.`) &&
					(strings.Contains(payload, ":7070") || strings.Contains(payload, ":8088")) {
					continue
				}
				// WIRE-WITNESS: the broker echoes every command it injects to /events as
				// {"__cmd":true,"origin":"wire",...}. This is how 8 SEES a context-less
				// agent driving the raw wire (not just its own /act path). ATTENTION
				// FOLLOWS ACTION: pull the driven context out of the command and setFocus
				// it, so the card being commanded rises to the TOP of its stack on 8 —
				// with ZERO afferent cost (the broker already sent this; we only read it).
				// origin=witness (8's own polling) is skipped so no self-focus loop forms.
				if strings.Contains(payload, `"__cmd"`) {
					var echo struct {
						Origin string          `json:"origin"`
						Method string          `json:"method"`
						Params json.RawMessage `json:"params"`
					}
					if json.Unmarshal([]byte(payload), &echo) == nil && echo.Origin == "wire" {
						// OBSERVATION IS NOT ACTION: captureScreenshot/getTree/print are
						// afferent reads — they must never move the gaze, no matter who
						// fires them. Found live (2026-07-27): an agent filming the demo
						// (captureScreenshot on the cockpit at 2.4/s through the broker)
						// hammered focus back to the cockpit card and drowned its OWN
						// acts on the driven tab — the camera stole the spotlight from
						// the actor. Only efferent acts (input.*, navigate, evaluate,
						// activate…) foveate.
						obs := echo.Method == "browsingContext.captureScreenshot" ||
							echo.Method == "browsingContext.getTree" ||
							echo.Method == "browsingContext.print"
						if !obs {
							var p struct {
								Context string `json:"context"`
								Target  struct {
									Context string `json:"context"`
								} `json:"target"`
							}
							json.Unmarshal(echo.Params, &p)
							fctx := p.Context
							if fctx == "" {
								fctx = p.Target.Context
							}
							if fctx != "" {
								c.setFocus(b.id, fctx) // 8 surfaces the driven card, no fan-out
							}
						}
					}
					c.publish(fmt.Sprintf(`{"session":%q,"origin":"WIRE","frame":%s}`, b.id, payload))
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
		http.Error(w, `{"error":"unknown or missing session — needs ?session=<id>. List live seats at /sessions (e.g. fox, tmux, nvim, daemons). If /sessions is empty this is a cold witness, not broken."}`, http.StatusNotFound)
		return
	}
	body, _ := io.ReadAll(r.Body)
	var probe struct {
		Method string `json:"method"`
		Actor  string `json:"actor"` // #29 declared actor (falls back to X-8-Actor header)
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
	id := c.record(reqRec{TS: nowNano(), Physics: "channel", Session: b.id, Method: probe.Method, URL: b.base + "/command", Body: string(body), Status: resp.StatusCode, LatUS: lat, RespLen: len(rb), RespHead: preview(rb), Actor: actorOf(r, probe.Actor)})
	// echo into view-1 carrying the ledger id, so the row links to its full payload + replay
	c.publish(fmt.Sprintf(`{"session":%q,"physics":"channel","origin":"COLLECTOR","frame":{"method":%q,"params":{"ledger_id":%d,"status":%d,"latency_us":%.0f}}}`, b.id, probe.Method, id, resp.StatusCode, lat))
	w.Header().Set("Content-Type", "application/json")
	c.witnessHeaders(w, id, "channel", int64(lat/1000)) // reafference rides back
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

// handlePanesSend fans ONE prompt into selected panes — or every live claude
// pane — through sendToPane, the same guarded throat wake/summon use (so
// EIGHT_NO_SUMMON, paneAlive and the TOCTOU tail all still hold). This is the
// operator's (and any agent's) "type the same thing into all/any Claude at
// once" nerve — the thing a solo mind kept faking by hand.
//
//	POST /panes/send  {"panes":["%8","%9"],"text":"..."}  — explicit targets
//	POST /panes/send  {"all":true,"text":"..."}           — every live claude pane
//	POST /panes/send  {"text":"..."}                      — same (empty == all claude)
//
// Only panes whose current command is claude are auto-targeted; an explicit
// list is sent as-given (the operator chose it). Reply is per-pane {pane,sent}.
func (c *collector) handlePanesSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Panes []string `json:"panes"`
		All   bool     `json:"all"`
		Text  string   `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Text) == "" {
		http.Error(w, `{"error":"need {text, and panes[] or all:true}"}`, http.StatusBadRequest)
		return
	}
	targets := req.Panes
	if req.All || len(targets) == 0 { // unspecified == the whole live claude choir
		targets = nil
		for _, p := range tmuxPanes() {
			if strings.Contains(strings.ToLower(p.Cmd), "claude") {
				targets = append(targets, p.ID)
			}
		}
	}
	type res struct {
		Pane string `json:"pane"`
		Sent bool   `json:"sent"`
	}
	out := make([]res, 0, len(targets))
	n := 0
	for _, p := range targets {
		ok := sendToPane(p, req.Text, nil)
		if ok {
			n++
		}
		out = append(out, res{Pane: p, Sent: ok})
	}
	c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"panes.send","params":{"sent":%d,"total":%d}}}`, n, len(targets)))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"sent": n, "total": len(targets), "targets": out})
}

// handleFetch executes one raw HTTP request server-side (the "curl hit"), and
// guardFetchTarget blocks the unambiguous SSRF target: the cloud-metadata /
// link-local range (169.254.0.0/16, fe80::/10) — never a legitimate /fetch
// destination, the classic credential-theft pivot (169.254.169.254). It checks
// the RESOLVED IPs, so a hostname that resolves into the range is caught too.
// It deliberately does NOT block loopback/private by default: /fetch's job IS
// hitting local appium/webdriver hubs (127.0.0.1:4723, brokers). Set
// EIGHT_FETCH_DENY_PRIVATE=1 to also block private/loopback for a hardened,
// non-loopback deployment. (Resolve-then-dial leaves a TOCTOU DNS-rebind gap;
// auth + this baseline is the proportionate mitigation for a local wire.)
func guardFetchTarget(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("bad url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("scheme %q not allowed (http/https only)", u.Scheme)
	}
	host := u.Hostname()
	ips, err := net.LookupIP(host)
	if err != nil {
		if ip := net.ParseIP(host); ip != nil {
			ips = []net.IP{ip} // literal IP that doesn't "resolve"
		} else {
			return nil // genuine DNS failure surfaces downstream as a fetch error
		}
	}
	denyPrivate := os.Getenv("EIGHT_FETCH_DENY_PRIVATE") == "1"
	for _, ip := range ips {
		if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
			return fmt.Errorf("blocked link-local/metadata address %s", ip)
		}
		if denyPrivate && (ip.IsLoopback() || ip.IsPrivate()) {
			return fmt.Errorf("blocked private address %s (EIGHT_FETCH_DENY_PRIVATE)", ip)
		}
	}
	return nil
}

// echoes it into the feed so the cockpit shows the call alongside browser events.
func (c *collector) handleFetch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    string            `json:"body"`
		Actor   string            `json:"actor"` // #29 declared actor (falls back to X-8-Actor header)
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.URL == "" {
		http.Error(w, `{"error":"url is required"}`, http.StatusBadRequest)
		return
	}
	if in.Method == "" {
		in.Method = "GET"
	}
	if err := guardFetchTarget(in.URL); err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusForbidden)
		return
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
	// 4min: a device session-create (Appium launching/building WDA, first-run
	// uiautomator2 install) routinely exceeds 30s; a plain curl hit is unaffected.
	resp, err := (&http.Client{Timeout: 240 * time.Second}).Do(req)
	if err != nil {
		c.publish(fmt.Sprintf(`{"session":"wire","origin":"COLLECTOR","frame":{"method":"http_request","params":{"http_method":%q,"url":%q,"status":0,"error":%q}}}`, strings.ToUpper(in.Method), in.URL, err.Error()))
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	latency := time.Since(start).Milliseconds()
	id := c.record(reqRec{TS: nowNano(), Physics: "call", Method: strings.ToUpper(in.Method), URL: in.URL, Headers: in.Headers, Body: in.Body, Status: resp.StatusCode, LatUS: float64(latency) * 1000, RespLen: len(rb), RespHead: preview(rb), Actor: actorOf(r, in.Actor)})
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
	c.witnessHeaders(w, id, "call", latency) // reafference rides back
	json.NewEncoder(w).Encode(map[string]any{
		"status":     resp.StatusCode,
		"latency_ms": latency,
		"headers":    hdr,
		"body":       string(rb),
		"witness":    map[string]any{"ledger_id": id, "physics": "call", "replayable": true, "seen": fmt.Sprintf("replayable frame #%d", id)},
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
// shrinkToRaw downscales a JPEG to ~targetW px wide (nearest-neighbor, stdlib
// only). LEVEL-OF-DETAIL: 8 asks for only as many pixels as the card is actually
// DISPLAYED at — a tab in a tiny zoomed-out container costs tiny memory/bandwidth;
// the zoomed hero asks for full res. "Pay for the pixels you show" (Maps/Figma
// LOD), not the naive fixed 2x. Capture is still 3592 at the Firefox side (that
// needs dpr=1 separately) — this governs what the COCKPIT holds + the wire carries.
// targetW<=0 or >= source returns the input unchanged (never upscales/breaks).
func shrinkToRaw(raw []byte, targetW int) []byte {
	if len(raw) == 0 || targetW <= 0 {
		return raw
	}
	src, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		return raw
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw < 2 || targetW >= sw {
		return raw
	}
	ow := targetW
	oh := sh * ow / sw
	if oh < 1 {
		oh = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, ow, oh))
	for y := 0; y < oh; y++ {
		sy := b.Min.Y + y*sh/oh
		for x := 0; x < ow; x++ {
			r, g, l, _ := src.At(b.Min.X+x*sw/ow, sy).RGBA()
			dst.SetRGBA(x, y, color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(l >> 8), 255})
		}
	}
	var out bytes.Buffer
	if jpeg.Encode(&out, dst, &jpeg.Options{Quality: 70}) != nil {
		return raw
	}
	return out.Bytes()
}

// shrinkTo is the base64 wrapper (for /shot's data URL).
func shrinkTo(b64 string, targetW int) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(raw) == 0 {
		return b64
	}
	return base64.StdEncoding.EncodeToString(shrinkToRaw(raw, targetW))
}

// lodWidth reads the ?w= LOD target the cockpit requests (the card's displayed px
// width). Empty -> a sane cap so a frame is never the full 3592 by accident.
func lodWidth(r *http.Request) int {
	if q := r.URL.Query().Get("w"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 {
			return n
		}
	}
	return 1280
}

func (c *collector) handleShot(w http.ResponseWriter, r *http.Request) {
	c.lastCapture.Store(time.Now().UnixNano())
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
		http.Error(w, `{"error":"unknown or missing session — needs ?session=<id>. List live seats at /sessions (e.g. fox, tmux, nvim, daemons). If /sessions is empty this is a cold witness, not broken."}`, http.StatusNotFound)
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
		"data":    "data:image/jpeg;base64," + shrinkTo(s.Result.Data, lodWidth(r)),
	})
}

// handleTabs lists a session's top-level tabs (context + url) so the cockpit
// can offer a picker for which one to mirror.
func (c *collector) handleTabs(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("session") == "tmux" { // the agents' surface: panes are the tabs
		tabs := make([]map[string]string, 0, 8)
		for _, p := range tmuxPanes() {
			tabs = append(tabs, map[string]string{"context": p.ID, "url": "tmux://" + p.Loc, "title": p.Title})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"tabs": tabs})
		return
	}
	if r.URL.Query().Get("session") == "nvim" { // the editor: buffers are the tabs
		tabs := make([]map[string]string, 0, 8)
		for _, bf := range nvimBufs() {
			nm := bf.Name
			if nm == "" {
				nm = "[No Name]"
			}
			mark := ""
			if bf.Changed != 0 {
				mark = " ●"
			}
			tabs = append(tabs, map[string]string{"context": strconv.Itoa(bf.Nr), "url": "nvim://" + nm + mark, "title": nm})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"tabs": tabs})
		return
	}
	if r.URL.Query().Get("session") == "daemons" { // the host's background minds
		tabs := make([]map[string]string, 0, 8)
		for _, d := range daemonList() {
			tabs = append(tabs, map[string]string{"context": "d-" + d.Name, "url": "daemon://" + d.Name + " · pid " + d.Pid + " · " + d.Info, "title": d.Name})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"tabs": tabs})
		return
	}
	if r.URL.Query().Get("session") == "fox" {
		// #7 — the deck eats the MANIFEST (all tabs), not just BiDi getTree
		// (drivable-only). PARKED = SEEN minus DRIVABLE: a discarded tab has no
		// BiDi context, so chrome-enumeration MINUS getTree IS the parked set — the
		// seen-vs-drivable seam made computable. Loaded tabs carry their BiDi ctx
		// (drivable); parked tabs carry "parked:<bcid>" and a parked flag → the web
		// renders them frozen with a P badge and a click-to-wake.
		urlToCtx := map[string]string{}
		if tr, err := c.command(c.find("fox"), `{"method":"browsingContext.getTree","params":{}}`); err == nil {
			var t struct {
				Result struct {
					Contexts []struct{ Context, URL string } `json:"contexts"`
				} `json:"result"`
			}
			json.Unmarshal(tr, &t)
			for _, ctx := range t.Result.Contexts {
				urlToCtx[ctx.URL] = ctx.Context
			}
		}
		tabs := make([]map[string]string, 0, 16)
		if out, err := c.execChrome(chromeTabsScript); err == nil && strings.HasPrefix(strings.TrimSpace(out), "[") {
			var ct []struct{ Bcid, URL, Title string }
			if json.Unmarshal([]byte(out), &ct) == nil {
				for _, t := range ct {
					if t.URL == "" || strings.HasPrefix(t.URL, "about:") || strings.Contains(t.URL, ":8088") {
						continue
					}
					rec := map[string]string{"url": t.URL, "title": t.Title}
					if ctx, ok := urlToCtx[t.URL]; ok {
						rec["context"], rec["parked"] = ctx, "false"
					} else {
						rec["context"], rec["parked"] = "parked:"+t.Bcid, "true"
					}
					tabs = append(tabs, rec)
				}
			}
		}
		if len(tabs) == 0 { // chrome enum unavailable — fall back to BiDi-only
			if tr, err := c.command(c.find("fox"), `{"method":"browsingContext.getTree","params":{}}`); err == nil {
				var t struct {
					Result struct {
						Contexts []struct{ Context, URL string } `json:"contexts"`
					} `json:"result"`
				}
				json.Unmarshal(tr, &t)
				for _, ctx := range t.Result.Contexts {
					tabs = append(tabs, map[string]string{"context": ctx.Context, "url": ctx.URL, "parked": "false"})
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"tabs": tabs})
		return
	}
	b := c.find(r.URL.Query().Get("session"))
	if b == nil {
		http.Error(w, `{"error":"unknown or missing session — needs ?session=<id>. List live seats at /sessions (e.g. fox, tmux, nvim, daemons). If /sessions is empty this is a cold witness, not broken."}`, http.StatusNotFound)
		return
	}
	// BiDi first; if the broker errors on it, this is a CDP (Chrome) channel —
	// enumerate its page targets instead, so Chrome's tabs show in the canvas
	// (and stack as a solitaire deck) exactly like Firefox's. Probe beats prior.
	tabs := make([]map[string]string, 0, 8)
	tr, err := c.command(b, `{"method":"browsingContext.getTree","params":{}}`)
	if err == nil && strings.Contains(string(tr), `"contexts"`) {
		var t struct {
			Result struct {
				Contexts []struct {
					Context string `json:"context"`
					URL     string `json:"url"`
				} `json:"contexts"`
			} `json:"result"`
		}
		json.Unmarshal(tr, &t)
		for _, ctx := range t.Result.Contexts {
			tabs = append(tabs, map[string]string{"context": ctx.Context, "url": ctx.URL})
		}
	} else {
		cr, cerr := c.command(b, `{"method":"Target.getTargets","params":{}}`)
		if cerr != nil {
			http.Error(w, `{"error":"`+cerr.Error()+`"}`, http.StatusBadGateway)
			return
		}
		var ct struct {
			Result struct {
				TargetInfos []struct {
					TargetID string `json:"targetId"`
					Type     string `json:"type"`
					URL      string `json:"url"`
					Title    string `json:"title"`
				} `json:"targetInfos"`
			} `json:"result"`
		}
		json.Unmarshal(cr, &ct)
		for _, ti := range ct.Result.TargetInfos {
			if ti.Type != "page" {
				continue
			}
			tabs = append(tabs, map[string]string{"context": ti.TargetID, "url": ti.URL, "title": ti.Title})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"tabs": tabs})
}

// chromeTabsScript enumerates EVERY tab across EVERY window from the chrome/parent
// context — the authoritative list. BiDi getTree only sees tabs its session knows
// (it was blind to 8 of 14 user-opened tabs, 2026-07-28); chrome sees them all.
// chromeTabsScript also DEDUPES cockpit (:8088) tabs every reconcile — keeps the
// SELECTED one, closes the rest. Firefox recycles accumulate :8088 tabs (13 seen
// 2026-07-31) WITHOUT restarting the collector, so activateCockpit's startup dedupe
// never fires on recycle — the periodic loop is the only place that catches it. Each
// stray cockpit captures all the others (the parent-memory leak multiplier).
const chromeTabsScript = `const cb=arguments[arguments.length-1];try{` +
	`let ck=[];for(let w of Services.wm.getEnumerator("navigator:browser")){for(let t of Array.from(w.gBrowser.tabs)){if(t.linkedBrowser.currentURI.spec.includes("8088"))ck.push({t:t,w:w,sel:t.selected});}}` +
	`if(ck.length>1){let keep=ck.find(c=>c.sel)||ck[0];for(let c of ck){if(c!==keep){try{c.w.gBrowser.removeTab(c.t);}catch(e){}}}}` +
	`let out=[];for(let w of Services.wm.getEnumerator("navigator:browser")){for(let t of w.gBrowser.tabs){let b=t.linkedBrowser;out.push({bcid:String(b.browsingContext?b.browsingContext.id:""),url:b.currentURI?b.currentURI.spec:"",title:t.label||"",sel:!!t.selected});}}` +
	`cb(JSON.stringify(out));}catch(e){cb("ERR:"+e);}`

// ── TAB MANIFEST ────────────────────────────────────────────────────────────
// The witness's answer to the basic questions it could never answer before:
// how many tabs, what each is, where (session), WHO opened it, WHY, and WHEN.
// getTree gives how-many/what/where but is ephemeral and its context-ids rotate;
// the manifest is the durable identity + provenance layer on top, reconciled every
// few seconds so dead-context ghosts turn "closed" and new tabs are "born".
type tabRec struct {
	UID           int64  `json:"uid"`
	Ctx           string `json:"ctx"`
	URL           string `json:"url"`
	Session       string `json:"session"`
	OpenedBy      string `json:"opened_by"` // "human" | "<agent-id>"
	Why           string `json:"why"`
	FirstSeen     string `json:"first_seen"`
	LastSeen      string `json:"last_seen"`
	Status        string `json:"status"` // live | closed
	ClosedAt      string `json:"closed_at,omitempty"`
	ClaimedBy     string `json:"claimed_by,omitempty"`     // an agent CURRENTLY using this tab (a lease)
	ClaimAt       string `json:"claim_at,omitempty"`       // heartbeat — a claim is LIVE only if fresh (<90s)
	ClaimVerified bool   `json:"claim_verified,omitempty"` // #83 the lease was granted against a host-resolved credential (not a free-text name)
}

// claimLive reports whether a tab's lease is fresh — the FALSIFIABLE basis for
// "an agent is using this tab" (vs the unverified assumption it isn't). A claim
// older than 90s is stale (the agent left / died) and the tab is free.
func claimLive(r *tabRec) bool {
	if r == nil || r.ClaimAt == "" {
		return false
	}
	t, err := time.Parse(time.RFC3339, r.ClaimAt)
	return err == nil && time.Since(t) < 90*time.Second
}

type pendAttr struct {
	Agent string
	Why   string
	At    time.Time
}

// reconcileManifest folds a session's live getTree tabs into the durable manifest.
// New contexts are BORN (attributed to a pending agent-intent if one was declared,
// else "human"); contexts of this session no longer present are CLOSED. No polling
// of its own — fed by handleTabs and the background reconcileLoop.
func (c *collector) reconcileManifest(session string, tabs []map[string]string) {
	now := time.Now().UTC().Format(time.RFC3339)
	c.tmu.Lock()
	live := map[string]bool{}
	for _, t := range tabs {
		ctx := t["context"]
		if ctx == "" {
			continue
		}
		live[ctx] = true
		r := c.manifest[ctx]
		if r == nil {
			c.tabseq++
			// HONEST PROVENANCE: the witness only claims an opener it actually witnessed.
			// - already-open at witness startup  -> "unknown" (it did NOT see them born)
			// - a :8088 cockpit tab               -> "system" (up.sh/activateCockpit opens these)
			// - an agent declared intent (POST /manifest) before opening -> that agent
			// - otherwise a tab just appeared      -> "unknown" (could be human OR an
			//   agent/system that did not declare — never assume "human")
			var by, why string
			switch {
			// a DECLARED intent wins over every heuristic — even for a :8088 tab.
			// (2026-08-07: an agent that declared then opened the cockpit was being
			// credited "system/up.sh" — a false witness; the class-guess shadowed
			// the explicit statement. Explicit beats inferred, always.)
			case c.manifestSeeded && len(c.pendOpen) > 0:
				p := c.pendOpen[len(c.pendOpen)-1]
				c.pendOpen = c.pendOpen[:len(c.pendOpen)-1]
				by, why = p.Agent, p.Why
			case strings.Contains(t["url"], ":8088"):
				by, why = "system", "up.sh cockpit"
			case !c.manifestSeeded:
				by, why = "unknown", "already open at witness startup"
			default:
				by, why = "unknown", "appeared — opener not witnessed"
			}
			r = &tabRec{UID: c.tabseq, Ctx: ctx, Session: session, OpenedBy: by, Why: why, FirstSeen: now, Status: "live"}
			c.manifest[ctx] = r
		}
		r.URL = t["url"]
		r.LastSeen = now
		r.Status = "live"
		r.ClosedAt = ""
	}
	for ctx, r := range c.manifest {
		if r.Session == session && !live[ctx] && r.Status == "live" {
			r.Status = "closed"
			r.ClosedAt = now
		}
	}
	c.manifestSeeded = true // startup snapshot done; later-born tabs can be attributed
	snap := make([]*tabRec, 0, len(c.manifest))
	for _, r := range c.manifest {
		snap = append(snap, r)
	}
	c.tmu.Unlock()
	os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
	if b, err := json.MarshalIndent(snap, "", " "); err == nil {
		os.WriteFile(os.ExpandEnv("$HOME/.8/manifest.json"), b, 0o644)
	}
}

// reconcileLoop keeps the manifest true even when NO cockpit is looking (fix for
// background-throttling) AND enumerates from CHROME context — every tab in every
// window — so the manifest finally sees ALL tabs, not just BiDi's session subset.
// fnv1a — a fast inline content hash (no import), for change-detection.
func fnv1a(b []byte) uint64 {
	var h uint64 = 1469598103934665603
	for _, c := range b {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// seatWatchLoop — REALIZE THE CHANNEL ATOM FOR EVERY SEAT (#11, 2026-08-11).
// The browser already pushes (the broker holds its BiDi socket); the text seats
// (tmux/nvim/daemons) were CALL-only — every client polled every surface. Now the
// collector WATCHES once (cheap, one place) and PUSHES a `<seat>.changed` event
// the instant a surface changes, to the broker's event stream. Clients subscribe
// and are pushed a delta, then fetch just the changed frame — O(1) watch + push
// instead of O(clients × surfaces) polling. The matrix's biggest amber stripe,
// filled. (Change-detection uses the cheapest signal each seat exposes: tmux
// pane_activity, nvim changedtick, the daemon pid-set — not a content re-read.)
func (c *collector) seatWatchLoop() {
	t := time.NewTicker(1500 * time.Millisecond)
	defer t.Stop()
	lastTmux := map[string]string{}
	lastNvim := map[string]string{}
	lastDaemon := ""
	tick := 0
	for range t.C {
		tick++
		// tmux: hash the visible content per pane. (pane_activity/history_size are
		// unreliable — activity needs monitor-activity; history only grows on
		// scroll-off. A capture hash is the honest "did this pane change" signal —
		// one cheap capture per pane in ONE place beats every client polling.)
		if tb := tmuxBin(); tb != "" {
			if out, err := exec.Command(tb, "list-panes", "-a", "-F", "#{pane_id}").Output(); err == nil {
				for _, pane := range strings.Fields(string(out)) {
					if !strings.HasPrefix(pane, "%") {
						continue
					}
					capd, e := exec.Command(tb, "capture-pane", "-p", "-t", pane).Output()
					if e != nil {
						continue
					}
					h := strconv.FormatUint(fnv1a(capd), 16)
					if prev, seen := lastTmux[pane]; seen && prev != h {
						c.publish(fmt.Sprintf(`{"session":"tmux","origin":"COLLECTOR","frame":{"method":"tmux.changed","params":{"pane":%q}}}`, pane))
					}
					lastTmux[pane] = h
				}
			}
		}
		// nvim: changedtick per buffer — bumps on every edit, cheap to read
		if sock := nvimSock(); sock != "" {
			expr := `json_encode(map(getbufinfo({"buflisted":1}),{i,b -> [b.bufnr, b.changedtick]}))`
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			out, err := exec.CommandContext(ctx, nvimBin(), "--server", sock, "--remote-expr", expr).Output()
			cancel()
			if err == nil {
				var pairs [][]int64
				if json.Unmarshal(out, &pairs) == nil {
					for _, p := range pairs {
						if len(p) == 2 {
							k, v := strconv.FormatInt(p[0], 10), strconv.FormatInt(p[1], 10)
							if prev, seen := lastNvim[k]; seen && prev != v {
								c.publish(fmt.Sprintf(`{"session":"nvim","origin":"COLLECTOR","frame":{"method":"nvim.changed","params":{"buf":%d}}}`, p[0]))
							}
							lastNvim[k] = v
						}
					}
				}
			}
		}
		// daemons: watched less often (pgrep is heavier) — emit when the pid-set shifts
		if tick%4 == 0 {
			sig := ""
			for _, d := range daemonList() {
				sig += d.Name + ":" + d.Pid + ","
			}
			if lastDaemon != "" && lastDaemon != sig {
				c.publish(`{"session":"daemons","origin":"COLLECTOR","frame":{"method":"daemons.changed","params":{}}}`)
			}
			lastDaemon = sig
		}
		// #12 CHANGE-DETECTOR: for tabs an agent asked to WATCH, read a cheap DOM
		// signature (title · text-length · tail-hash) every ~3rd tick and PUSH a
		// tab.changed with the DELTA when it shifts — so "a reply arrived on the
		// claude tab" is a push, not a poll (the way its frontend loaded it too).
		// Opt-in: only watched tabs are read, so it stays lean.
		if tick%3 == 0 {
			c.wmu.Lock()
			watch := make(map[string]string, len(c.watched))
			for k, v := range c.watched {
				watch[k] = v
			}
			c.wmu.Unlock()
			if len(watch) > 0 {
				if b := c.find("fox"); b != nil {
					for ctx, prev := range watch {
						q := fmt.Sprintf(`{"method":"script.evaluate","params":{"awaitPromise":true,"target":{"context":%q},"expression":"(()=>{const x=(document.body&&document.body.innerText)||'';return JSON.stringify({t:document.title,n:x.length,tail:x.slice(-160)})})()"}}`, ctx)
						out, err := c.command(b, q)
						if err != nil {
							continue
						}
						var r struct {
							Result struct {
								Result struct {
									Value string `json:"value"`
								} `json:"result"`
							} `json:"result"`
						}
						if json.Unmarshal(out, &r) != nil || r.Result.Result.Value == "" {
							continue
						}
						sig := strconv.FormatUint(fnv1a([]byte(r.Result.Result.Value)), 16)
						if prev != "" && prev != sig {
							var d struct {
								T    string `json:"t"`
								N    int    `json:"n"`
								Tail string `json:"tail"`
							}
							json.Unmarshal([]byte(r.Result.Result.Value), &d)
							c.publish(fmt.Sprintf(`{"session":"fox","origin":"COLLECTOR","frame":{"method":"tab.changed","params":{"context":%q,"title":%q,"len":%d}}}`, ctx, d.T, d.N))
						}
						c.wmu.Lock()
						if _, still := c.watched[ctx]; still {
							c.watched[ctx] = sig
						}
						c.wmu.Unlock()
					}
				}
			}
		}
	}
}

// handleWatch — #12: register a tab to WATCH (its DOM changes get pushed as
// tab.changed) or stop. GET/POST /watch?context=<ctx>[&off=1]. GET with no ctx
// lists the watched set. The change-detector turns the witness into a passive
// ping for tabs you're NOT looking at (a reply landed on claude/deepseek).
func (c *collector) handleWatch(w http.ResponseWriter, r *http.Request) {
	ctx := r.URL.Query().Get("context")
	c.wmu.Lock()
	if c.watched == nil {
		c.watched = map[string]string{}
	}
	if ctx != "" {
		if r.URL.Query().Get("off") == "1" {
			delete(c.watched, ctx)
		} else {
			if _, ok := c.watched[ctx]; !ok {
				c.watched[ctx] = "" // empty sig → first read establishes baseline (no false first event)
			}
		}
	}
	list := make([]string, 0, len(c.watched))
	for k := range c.watched {
		list = append(list, k)
	}
	c.wmu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"watching": list})
}

func (c *collector) reconcileLoop() {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	eyeClosed := 0 // consecutive ticks with NO :8088 tab — the witness's own eye
	for range t.C {
		out, err := c.execChrome(chromeTabsScript)
		if err != nil || strings.HasPrefix(out, "ERR:") || !strings.HasPrefix(strings.TrimSpace(out), "[") {
			continue
		}
		// THE EYE INVARIANT (2026-08-07): the cockpit tab vanished during a recycle
		// and the witness ran HEADLESS FOR A WEEK — activateCockpit only fires at
		// collector start and gives up if no tab exists. The witness must keep its
		// own eye open: if no :8088 tab survives 12 ticks (~60s, debounced so a
		// human closing it isn't fought instantly), reopen one — logged + witnessed.
		if !strings.Contains(out, ":8088") && !strings.Contains(out, "localhost%3A8088") {
			eyeClosed++
			if eyeClosed >= 12 {
				eyeClosed = 0
				if b := c.find("fox"); b != nil {
					if cr, e := c.command(b, `{"method":"browsingContext.create","params":{"type":"tab"}}`); e == nil {
						var cv struct {
							Result struct {
								Context string `json:"context"`
							} `json:"result"`
						}
						if json.Unmarshal(cr, &cv) == nil && cv.Result.Context != "" {
							c.command(b, fmt.Sprintf(`{"method":"browsingContext.navigate","params":{"context":%q,"url":"http://localhost:8088/","wait":"none"}}`, cv.Result.Context))
							c.publish(fmt.Sprintf(`{"session":"fox","origin":"COLLECTOR","frame":{"method":"witness.eye_reopened","params":{"context":%q}}}`, cv.Result.Context))
							log.Printf("witness eye was closed ~60s -> reopened cockpit tab (%s)", cv.Result.Context)
						}
					}
				}
			}
		} else {
			eyeClosed = 0
		}
		var ct []chromeTab
		if json.Unmarshal([]byte(out), &ct) != nil {
			continue
		}
		if c.manifestSeeded { // #643: never prune a world the witness has not yet read
			c.autoDedupe(ct)
		}
		tabs := make([]map[string]string, 0, len(ct))
		for _, tb := range ct {
			key := tb.Bcid
			if key == "" {
				key = tb.URL
			}
			tabs = append(tabs, map[string]string{"context": key, "url": tb.URL})
		}
		c.reconcileManifest("fox", tabs) // one Firefox = one session; chrome sees all its windows
		// the AGENTS' surface flows into the SAME manifest — one db for every
		// surface kind (browser tabs, tmux panes, soon nvim buffers): uid/what/
		// where/who/why/when, births and deaths alike.
		if panes := tmuxPanes(); len(panes) > 0 {
			pt := make([]map[string]string, 0, len(panes))
			for _, p := range panes {
				pt = append(pt, map[string]string{"context": "tmux-" + p.ID, "url": "tmux://" + p.Loc + " · " + p.Title})
			}
			c.reconcileManifest("tmux", pt)
		}
		if bufs := nvimBufs(); len(bufs) > 0 {
			bt := make([]map[string]string, 0, len(bufs))
			for _, bf := range bufs {
				nm := bf.Name
				if nm == "" {
					nm = "[No Name]"
				}
				bt = append(bt, map[string]string{"context": "nvim-" + strconv.Itoa(bf.Nr), "url": "nvim://" + nm})
			}
			c.reconcileManifest("nvim", bt)
		}
	}
}

func (c *collector) handleStopwatch(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, stopwatchHTML)
}

// ── EIGHT.DB — the one queryable store, projected from the scattered JSON ─────
// The JSON/ndjson stores stay the SOURCE OF TRUTH (append-only, the "observation
// only ADDS" invariant). eight.db is a READ-MODEL LENS over them — rebuilt
// idempotently each sync (DROP/CREATE), so it can never desync into a second
// authority DBeaver would trust wrongly. Built by shelling the sqlite3 binary,
// so 8 stays stdlib-only (like tmux/pgrep/ps) and DBeaver opens a real file:
//
//	dbeaver → SQLite → ~/.8/eight.db  (tables: surfaces, events, work, benches)
//
// One db, four surface-kinds and every act, ready for data modelling.
func eightDB() string { return os.ExpandEnv("$HOME/.8/eight.db") }

// sqlQ single-quotes a value for SQLite, doubling embedded quotes (the only
// escaping SQLite string literals need). NUL is stripped (SQLite can't store it).
func sqlQ(s string) string {
	s = strings.ReplaceAll(s, "\x00", "")
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func (c *collector) syncDB() (map[string]int, error) {
	counts := map[string]int{}
	var b strings.Builder
	b.WriteString("PRAGMA journal_mode=WAL;\nBEGIN;\n")
	b.WriteString(`DROP TABLE IF EXISTS surfaces; CREATE TABLE surfaces(uid INTEGER PRIMARY KEY, ctx TEXT, session TEXT, url TEXT, opened_by TEXT, why TEXT, first_seen TEXT, last_seen TEXT, status TEXT, closed_at TEXT);` + "\n")
	b.WriteString(`DROP TABLE IF EXISTS events; CREATE TABLE events(id INTEGER PRIMARY KEY, ts TEXT, physics TEXT, session TEXT, method TEXT, url TEXT, status INTEGER, latency_us INTEGER, resp_bytes INTEGER, replayable INTEGER);` + "\n")
	// #431: the RELATIONS ride along — assignee/prio/flipped_by were dropped by
	// the projection, so /sql could report how-much-is-undone but never WHO owns
	// it; the distribution of work across minds is exactly what "how ready are
	// we" asks, and the witness omitted it.
	b.WriteString(`DROP TABLE IF EXISTS work; CREATE TABLE work(id INTEGER PRIMARY KEY, text TEXT, status TEXT, by_who TEXT, ts TEXT, assignee TEXT, prio INTEGER, flipped_by TEXT, by_canon TEXT);` + "\n")
	b.WriteString(`DROP TABLE IF EXISTS benches; CREATE TABLE benches(id INTEGER, ts TEXT, tag TEXT, session TEXT, n INTEGER, equiv_p50 REAL, equiv_p99 REAL, byte_ratio REAL);` + "\n")

	// #437: identity DERIVED live (no roster file) — names are self-declared.
	// panes+windows ← live tmux (#436: rows were frozen at 2026-08-13 — the
	// witness had stopped writing what it sees; no source code even wrote these
	// tables). GUARD: only rewrite when tmux answers non-empty — eight.db.panes
	// doubles as restore-8.sh's source, and last-good must survive a dead tmux.
	// role now carries the CANONICAL name (it wrongly held the tmux title).
	if tb := tmuxBin(); tb != "" {
		pout, _ := exec.Command(tb, "list-panes", "-a", "-F", "#{session_name}|#{window_index}|#{pane_index}|#{pane_id}|#{pane_pid}|#{pane_current_path}|#{pane_current_command}|#{pane_title}").Output()
		var plines []string
		for _, ln := range strings.Split(strings.TrimSpace(string(pout)), "\n") {
			if strings.TrimSpace(ln) != "" {
				plines = append(plines, ln)
			}
		}
		if len(plines) > 0 {
			b.WriteString(`DROP TABLE IF EXISTS panes; CREATE TABLE panes(session TEXT, win INTEGER, pane INTEGER, pane_id TEXT, claude_uuid TEXT, cwd TEXT, title TEXT, label TEXT, role TEXT, bypass INTEGER DEFAULT 1, cmd TEXT, updated_at TEXT, canonical_name TEXT, jsonl_path TEXT, PRIMARY KEY(session,win,pane));` + "\n")
			uu := paneUUIDs()
			pnow := time.Now().UTC().Format(time.RFC3339)
			for _, ln := range plines {
				f := strings.SplitN(ln, "|", 8)
				if len(f) != 8 {
					continue
				}
				uuid := uu[f[4]]
				name, _ := nameForUUID(uuid) // self-declared; "" when undeclared — honest
				jsonl := jsonlForUUID(uuid)  // discovered by search, never assumed
				b.WriteString(fmt.Sprintf("INSERT INTO panes VALUES(%s,%s,%s,%s,%s,%s,%s,%s,%s,1,%s,%s,%s,%s);\n",
					sqlQ(f[0]), f[1], f[2], sqlQ(f[3]), sqlQ(uuid), sqlQ(f[5]), sqlQ(f[7]), sqlQ(f[7]), sqlQ(name), sqlQ(f[6]), sqlQ(pnow), sqlQ(name), sqlQ(jsonl)))
				counts["panes"]++
			}
			wout, _ := exec.Command(tb, "list-windows", "-a", "-F", "#{session_name}|#{window_index}|#{window_name}|#{window_layout}").Output()
			b.WriteString(`DROP TABLE IF EXISTS windows; CREATE TABLE windows(session TEXT, win INTEGER, name TEXT, layout TEXT, updated_at TEXT, PRIMARY KEY(session,win));` + "\n")
			for _, ln := range strings.Split(strings.TrimSpace(string(wout)), "\n") {
				f := strings.SplitN(ln, "|", 4)
				if len(f) != 4 || strings.TrimSpace(ln) == "" {
					continue
				}
				b.WriteString(fmt.Sprintf("INSERT INTO windows VALUES(%s,%s,%s,%s,%s);\n", sqlQ(f[0]), f[1], sqlQ(f[2]), sqlQ(f[3]), sqlQ(pnow)))
				counts["windows"]++
			}
		}
	}

	// surfaces ← manifest.json (a list of surface world-lines)
	if data, err := os.ReadFile(os.ExpandEnv("$HOME/.8/manifest.json")); err == nil {
		var recs []tabRec
		if json.Unmarshal(data, &recs) == nil {
			for _, r := range recs {
				b.WriteString(fmt.Sprintf("INSERT INTO surfaces VALUES(%d,%s,%s,%s,%s,%s,%s,%s,%s,%s);\n",
					r.UID, sqlQ(r.Ctx), sqlQ(r.Session), sqlQ(r.URL), sqlQ(r.OpenedBy), sqlQ(r.Why), sqlQ(r.FirstSeen), sqlQ(r.LastSeen), sqlQ(r.Status), sqlQ(r.ClosedAt)))
				counts["surfaces"]++
			}
		}
	}
	// events ← requests.ndjson (every witnessed atom)
	if data, err := os.ReadFile(os.ExpandEnv("$HOME/.8/requests.ndjson")); err == nil {
		for _, ln := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(ln) == "" {
				continue
			}
			var e struct {
				ID         int64  `json:"id"`
				TS         string `json:"ts"`
				Physics    string `json:"physics"`
				Session    string `json:"session"`
				Method     string `json:"method"`
				URL        string `json:"url"`
				Status     int    `json:"status"`
				LatencyUS  int64  `json:"latency_us"`
				RespBytes  int64  `json:"resp_bytes"`
				Replayable bool   `json:"replayable"`
			}
			if json.Unmarshal([]byte(ln), &e) != nil {
				continue
			}
			rep := 0
			if e.Replayable {
				rep = 1
			}
			b.WriteString(fmt.Sprintf("INSERT INTO events VALUES(%d,%s,%s,%s,%s,%s,%d,%d,%d,%d);\n",
				e.ID, sqlQ(e.TS), sqlQ(e.Physics), sqlQ(e.Session), sqlQ(e.Method), sqlQ(e.URL), e.Status, e.LatencyUS, e.RespBytes, rep))
			counts["events"]++
		}
	}
	// work ← work.json. Lock-free BY DESIGN (#402): writes are atomic
	// temp+rename now, so this can never see a torn file — only a slightly
	// stale snapshot, which is exactly what an export wants.
	if data, err := os.ReadFile(workFile()); err == nil {
		var items []workItem
		if json.Unmarshal(data, &items) == nil {
			for _, it := range items {
				b.WriteString(fmt.Sprintf("INSERT INTO work VALUES(%d,%s,%s,%s,%s,%s,%d,%s,%s);\n", it.ID, sqlQ(it.Text), sqlQ(it.Status), sqlQ(it.By), sqlQ(it.TS), sqlQ(it.Assignee), it.Prio, sqlQ(it.FlippedBy), sqlQ(foldLabel(it.By))))
				counts["work"]++
			}
		}
	}
	// benches ← benches.ndjson (the sense-cost measurements)
	if data, err := os.ReadFile(os.ExpandEnv("$HOME/.8/benches.ndjson")); err == nil {
		for _, ln := range strings.Split(string(data), "\n") {
			if strings.TrimSpace(ln) == "" {
				continue
			}
			var bn struct {
				ID        int64   `json:"id"`
				TS        string  `json:"ts"`
				Tag       string  `json:"tag"`
				Session   string  `json:"session"`
				N         int     `json:"n"`
				EquivP50  float64 `json:"equiv_p50"`
				EquivP99  float64 `json:"equiv_p99"`
				ByteRatio float64 `json:"byte_ratio"`
			}
			if json.Unmarshal([]byte(ln), &bn) != nil {
				continue
			}
			b.WriteString(fmt.Sprintf("INSERT INTO benches VALUES(%d,%s,%s,%s,%d,%f,%f,%f);\n",
				bn.ID, sqlQ(bn.TS), sqlQ(bn.Tag), sqlQ(bn.Session), bn.N, bn.EquivP50, bn.EquivP99, bn.ByteRatio))
			counts["benches"]++
		}
	}
	b.WriteString("COMMIT;\n")

	cmd := exec.Command("sqlite3", eightDB())
	cmd.Stdin = strings.NewReader(b.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		return counts, fmt.Errorf("sqlite3: %v: %s", err, out)
	}
	return counts, nil
}

// handleDB — GET /db triggers a projection sync and returns the row counts +
// the file path DBeaver should open. The one store, on demand.
func (c *collector) handleDB(w http.ResponseWriter, r *http.Request) {
	if _, err := exec.LookPath("sqlite3"); err != nil {
		http.Error(w, `{"error":"sqlite3 binary not found — brew install sqlite / apt install sqlite3"}`, http.StatusServiceUnavailable)
		return
	}
	counts, err := c.syncDB()
	if err != nil {
		http.Error(w, `{"error":`+sqlQ(err.Error())+`}`, http.StatusInternalServerError)
		return
	}
	c.publish(fmt.Sprintf(`{"session":"db","origin":"COLLECTOR","frame":{"method":"db.sync","params":{"surfaces":%d,"events":%d,"work":%d,"benches":%d}}}`, counts["surfaces"], counts["events"], counts["work"], counts["benches"]))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"db": eightDB(), "counts": counts, "open_with": "DBeaver → SQLite → " + eightDB()})
}

// handleClaim — an agent LEASES a tab it is actively using. POST /claim
// {ctx, agent} stamps claimed_by + claim_at=now; re-post = heartbeat. This makes
// "no other agent is using this tab" FALSIFIABLE (check for a live claim) instead
// of assumed — the same law the other Claude found the channel path violating.
// handlePark — manually park (discard) a tab: the aperture's on-demand sibling
// and the symmetric partner of /wake. GET /park?url=<substr>. Runs through the
// collector's OWN execChrome (whose marionette window stays on the cockpit), and
// REFUSES the selected tab and the :8088 cockpit — so it never wedges the session
// the way a naive discard through the shared driver does (learned 2026-08-10).
func (c *collector) handlePark(w http.ResponseWriter, r *http.Request) {
	sub := r.URL.Query().Get("url")
	if sub == "" {
		http.Error(w, `{"error":"need url=<substr>"}`, http.StatusBadRequest)
		return
	}
	script := `const cb=arguments[arguments.length-1];const want=` + strconv.Quote(sub) + `;try{for(let w of Services.wm.getEnumerator("navigator:browser")){let gB=w.gBrowser;for(let t of Array.from(gB.tabs)){let b=t.linkedBrowser,u=b.currentURI?b.currentURI.spec:"";if(u.includes(want)&&!u.includes("8088")&&t!==gB.selectedTab){let id=String(b.browsingContext?b.browsingContext.id:"");gB.discardBrowser(t);cb("parked:"+id);return;}}}cb("none");}catch(e){cb("ERR:"+e);}`
	out, err := c.execChrome(script)
	if err != nil {
		http.Error(w, `{"error":"park failed"}`, http.StatusBadGateway)
		return
	}
	c.publish(fmt.Sprintf(`{"session":"fox","origin":"COLLECTOR","frame":{"method":"tab.park","params":{"url":%q,"result":%q}}}`, sub, strings.TrimSpace(out)))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"parked":%q}`, strings.TrimSpace(out))
}

// handleWake — un-park a parked tab (#7): select it in chrome, which reloads the
// discarded browser and gives it a BiDi context again → it becomes drivable. GET
// /wake?bcid=<id>. The paired sense-change is visible: next enumerate shows it
// with a real ctx and parked=false.
func (c *collector) handleWake(w http.ResponseWriter, r *http.Request) {
	bcid := r.URL.Query().Get("bcid")
	url := r.URL.Query().Get("url")
	if bcid == "" && url == "" {
		http.Error(w, `{"error":"need bcid or url"}`, http.StatusBadRequest)
		return
	}
	// A DISCARDED tab has NO browsingContext.id, so a parked tab must be woken by
	// its stable URL, not bcid (2026-08-11). Match by bcid when given, else by URL
	// substring; selecting the tab reloads the discarded browser → drivable again.
	var script string
	if bcid != "" {
		script = `const cb=arguments[arguments.length-1];const want=` + strconv.Quote(bcid) + `;try{for(let w of Services.wm.getEnumerator("navigator:browser")){for(let t of w.gBrowser.tabs){let b=t.linkedBrowser;if(b.browsingContext&&String(b.browsingContext.id)===want){w.gBrowser.selectedTab=t;cb("woke:"+b.currentURI.spec);return;}}}cb("not-found");}catch(e){cb("ERR:"+e);}`
	} else {
		script = `const cb=arguments[arguments.length-1];const want=` + strconv.Quote(url) + `;try{for(let w of Services.wm.getEnumerator("navigator:browser")){for(let t of w.gBrowser.tabs){let b=t.linkedBrowser,u=b.currentURI?b.currentURI.spec:"";if(u.includes(want)){w.gBrowser.selectedTab=t;cb("woke:"+u);return;}}}cb("not-found");}catch(e){cb("ERR:"+e);}`
	}
	out, err := c.execChrome(script)
	if err != nil {
		http.Error(w, `{"error":"wake failed"}`, http.StatusBadGateway)
		return
	}
	c.publish(fmt.Sprintf(`{"session":"fox","origin":"COLLECTOR","frame":{"method":"tab.wake","params":{"bcid":%q,"url":%q}}}`, bcid, url))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"woke":%q,"result":%q}`, bcid+url, strings.TrimSpace(out))
}

// handleMatrix — the SURFACES × SENSES coverage matrix: the map of the unfound.
// Rows are the live surface-kinds (seats); columns are the senses/verbs. The
// point is the DIFFERENCE between empty cells:
//
//	live    — probed just now, works (carries a count/proof)
//	yes     — built and declared
//	na      — structurally inapplicable (pixels of a text seat)
//	unfound — PLAUSIBLE but not built: a sense this surface COULD expose and
//	          doesn't — where something can stand seen-able-but-unseen. Each is
//	          a candidate work item; these cells ARE the map.
func (c *collector) handleMatrix(w http.ResponseWriter, r *http.Request) {
	cols := []string{"enumerate", "frame·pixels", "frame·text", "symbol·eval", "control", "events·push", "memory·ledger"}
	type cell struct {
		S      string `json:"s"`
		Detail string `json:"detail"`
	}
	type row struct {
		Kind  string          `json:"kind"`
		Cells map[string]cell `json:"cells"`
	}
	mk := func(vals ...string) map[string]cell {
		m := map[string]cell{}
		for i, cn := range cols {
			m[cn] = cell{S: vals[2*i], Detail: vals[2*i+1]}
		}
		return m
	}
	foxN := 0
	c.tmu.Lock()
	for _, t := range c.manifest {
		if t.Status == "live" && t.Session == "fox" {
			foxN++
		}
	}
	c.tmu.Unlock()
	tmuxN, nvimN, dmnN := len(tmuxPanes()), len(nvimBufs()), len(daemonList())
	rows := []row{}
	live := map[string]bool{}
	for _, b := range c.brokers {
		live[b.id] = true
	}
	if live["fox"] {
		rows = append(rows, row{"fox · browser", mk(
			"live", fmt.Sprintf("getTree · %d tabs", foxN),
			"yes", "drawSnapshot→jpeg", "yes", "channel DOM source", "yes", "script.evaluate",
			"yes", "/act /run (BiDi input)", "yes", "BiDi subscribe → broker push", "yes", "ledger + manifest")})
	}
	if tmuxN > 0 {
		rows = append(rows, row{"tmux · agents", mk(
			"live", fmt.Sprintf("list-panes · %d", tmuxN),
			"unfound", "could render text→img; not built", "yes", "capture-pane",
			"unfound", "shell has no eval-returns-value; control-mode query?",
			"yes", "send-keys", "live", "collector watches pane_activity → tmux.changed push",
			"unfound", "in manifest; NOT in ledger (no per-pane events)")})
	}
	if nvimN > 0 {
		rows = append(rows, row{"nvim · editor", mk(
			"live", fmt.Sprintf("getbufinfo · %d bufs", nvimN),
			"unfound", "TUI has no raster; could screencap the pane", "yes", "getbufline",
			"yes", "remote-expr (real eval!)", "yes", ":buffer / remote-send",
			"live", "collector watches changedtick → nvim.changed push", "unfound", "in manifest; NOT in ledger")})
	}
	if dmnN > 0 {
		rows = append(rows, row{"daemons · host", mk(
			"live", fmt.Sprintf("pgrep · %d", dmnN),
			"na", "a process has no surface", "yes", "ps + log tail",
			"na", "no eval into a foreign process", "yes", "signals (TERM/HUP)",
			"live", "collector watches pid-set → daemons.changed push", "unfound", "in manifest; NOT in ledger")})
	}
	if data, err := os.ReadFile(sessionsFile()); err == nil && strings.Contains(string(data), `"call"`) {
		rows = append(rows, row{"device · appium", mk(
			"live", "session probe", "yes", "mjpeg / screenshot", "yes", "page source",
			"yes", "execute script", "yes", "/act (appium)",
			"unfound", "no event push from the device", "yes", "ledger")})
	}
	unfound := 0
	for _, rr := range rows {
		for _, cn := range cols {
			if rr.Cells[cn].S == "unfound" {
				unfound++
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	resp := map[string]any{"cols": cols, "rows": rows, "unfound": unfound,
		"legend": "live=probed now · yes=built · na=inapplicable · unfound=plausible-but-unbuilt (the map)"}
	if len(rows) == 0 { // COLD-INSTANCE HONESTY (container-Claude's finding, 2026-08-11):
		// empty ≠ broken. Tell a newcomer WHY it's empty and what to do.
		resp["note"] = "empty is not broken — no live surfaces to map yet. Start a browser/tmux/nvim (or attach a session); the matrix populates as seats come alive. See /sessions."
	}
	json.NewEncoder(w).Encode(resp)
}

// claimTokens reads the HOST-side agent→token map (~/.8/claim_tokens.json). The
// map lives on the host and is NEVER in any public artifact — the I1 invariant.
// Returns (tokens, enforced): enforced=false when the file is absent (open dev
// model, unchanged); enforced=true means /claim must present a matching token.
func claimTokens() (map[string]string, bool) {
	b, err := os.ReadFile(dotDir() + "/claim_tokens.json")
	if err != nil {
		return nil, false
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil || len(m) == 0 {
		return nil, false
	}
	return m, true
}

func (c *collector) handleClaim(w http.ResponseWriter, r *http.Request) {
	var p struct{ Ctx, Agent, Token string }
	json.NewDecoder(r.Body).Decode(&p)
	if p.Ctx == "" || p.Agent == "" {
		http.Error(w, `{"error":"need {ctx, agent}"}`, http.StatusBadRequest)
		return
	}
	// #83 real spoof closure: when tokens are configured, a claim must present the
	// host-resolved credential for its agent, else it is REJECTED — /claim can no
	// longer mint a lease for an arbitrary free-text name. Non-breaking: absent a
	// tokens file, the open model is unchanged and the claim is marked unverified.
	tokens, enforced := claimTokens()
	tok := p.Token
	if h := r.Header.Get("X-8-Token"); h != "" {
		tok = h
	}
	verified := false
	if enforced {
		if want, ok := tokens[p.Agent]; !ok || subtle.ConstantTimeCompare([]byte(want), []byte(tok)) != 1 {
			http.Error(w, `{"error":"claim rejected — bad or missing credential for this agent (spoof closed). Present X-8-Token or {token}."}`, http.StatusForbidden)
			return
		}
		verified = true
	}
	now := time.Now().UTC().Format(time.RFC3339)
	c.tmu.Lock()
	if c.manifest == nil {
		c.manifest = map[string]*tabRec{}
	}
	rec := c.manifest[p.Ctx]
	if rec == nil {
		// LAZY CLAIM (2026-08-11): a JUST-created tab (e.g. one the git-broker's
		// far-end Claude opened) isn't in the manifest yet — the reconcile runs
		// every 5s, and it keys by chrome bcid while a fresh BiDi ctx is a uuid.
		// So claim by ANY ctx creates a stub keyed by it: the lease + provenance
		// stick immediately, and reconcile fills the rest. A claim never 404s on a
		// real tab you just made.
		c.tabseq++
		rec = &tabRec{UID: c.tabseq, Ctx: p.Ctx, Session: "fox", OpenedBy: p.Agent,
			Why: "claimed (lazy — not yet reconciled)", FirstSeen: now, Status: "live"}
		c.manifest[p.Ctx] = rec
	}
	rec.ClaimedBy, rec.ClaimAt, rec.ClaimVerified = p.Agent, now, verified
	c.tmu.Unlock()
	c.publish(fmt.Sprintf(`{"session":%q,"origin":"COLLECTOR","frame":{"method":"tab.claim","params":{"ctx":%q,"agent":%q,"verified":%v}}}`, rec.Session, p.Ctx, p.Agent, verified))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"claimed":%q,"by":%q,"verified":%v}`, p.Ctx, p.Agent, verified)
}

// handleDedup — find same-URL duplicate live tabs and (when asked=&close=1)
// close the ones SAFE to close: a duplicate with NO live claim by another agent.
// GET reports candidates without closing; the verify/falsify gate is claimLive.
func (c *collector) handleDedup(w http.ResponseWriter, r *http.Request) {
	me := r.URL.Query().Get("agent")
	doClose := r.URL.Query().Get("close") == "1"
	c.tmu.Lock()
	byURL := map[string][]*tabRec{}
	for _, rec := range c.manifest {
		if rec.Status == "live" && rec.Session == "fox" {
			byURL[rec.URL] = append(byURL[rec.URL], rec)
		}
	}
	type cand struct {
		URL, Ctx, ClaimedBy string
		Claimed             bool
		Kept                bool
	}
	var out []cand
	var toClose []string
	for url, recs := range byURL {
		if len(recs) < 2 {
			continue
		}
		// keep exactly one: prefer a live-claimed tab, else the most-recently-seen.
		keepIdx := 0
		for i, rec := range recs {
			if claimLive(rec) {
				keepIdx = i
				break
			}
			if rec.LastSeen > recs[keepIdx].LastSeen {
				keepIdx = i
			}
		}
		for i, rec := range recs {
			busyByOther := claimLive(rec) && rec.ClaimedBy != me
			out = append(out, cand{url, rec.Ctx, rec.ClaimedBy, claimLive(rec), i == keepIdx})
			if doClose && i != keepIdx && !busyByOther {
				toClose = append(toClose, rec.Ctx)
			}
		}
	}
	c.tmu.Unlock()
	closed := []string{}
	if doClose {
		if b := c.find("fox"); b != nil {
			for _, ctx := range toClose {
				if _, err := c.command(b, fmt.Sprintf(`{"method":"browsingContext.close","params":{"context":%q}}`, ctx)); err == nil {
					closed = append(closed, ctx)
				}
			}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"duplicates": out, "closed": closed})
}

// handleManifest — GET returns the full manifest (the answer to how-many/what/where/
// who/why/when). POST {agent, why} declares intent BEFORE opening a tab, so the
// next-born tab is attributed to that agent instead of "human" (provenance capture).
func (c *collector) handleManifest(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var p struct {
			Agent string `json:"agent"`
			Why   string `json:"why"`
		}
		json.NewDecoder(r.Body).Decode(&p)
		if p.Agent == "" {
			p.Agent = "unknown-agent"
		}
		c.tmu.Lock()
		c.pendOpen = append(c.pendOpen, pendAttr{Agent: p.Agent, Why: p.Why, At: time.Now()})
		c.tmu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"ok": true, "note": "next-born tab -> " + p.Agent})
		return
	}
	c.tmu.Lock()
	snap := make([]*tabRec, 0, len(c.manifest))
	for _, rec := range c.manifest {
		snap = append(snap, rec)
	}
	c.tmu.Unlock()
	sort.Slice(snap, func(i, j int) bool { return snap[i].UID < snap[j].UID })
	live := 0
	for _, rec := range snap {
		if rec.Status == "live" {
			live++
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"total": len(snap), "live": live, "tabs": snap})
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
	Kind    string `json:"kind"`             // local | cloud
	Physics string `json:"physics"`          // call | channel
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

// handleAdapterFire — fire a REAL own-both-ends loopback for a transport the
// wire delegates to adapters (grpc/mqtt/webrtc/unix), by exec-ing the adapters
// repo's `loopback` command. The result is WITNESSED into the feed like any op,
// so the WIRE pane's adapter rows are genuinely fireable — no faking a
// transport the browser cannot originate; the adapter originates it and 8 sees.
func (c *collector) handleAdapterFire(w http.ResponseWriter, r *http.Request) {
	t := r.URL.Query().Get("t")
	switch t {
	case "grpc", "mqtt", "webrtc", "unix":
	default:
		http.Error(w, `t must be grpc|mqtt|webrtc|unix`, http.StatusBadRequest)
		return
	}
	bin := os.ExpandEnv("$HOME/Desktop/repos/adapters/loopback")
	if _, err := os.Stat(bin); err != nil {
		http.Error(w, "adapters loopback binary not found at "+bin+" — build with: (cd adapters && go build -o loopback ./cmd/loopback)", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, bin, t).Output()
	line := strings.TrimSpace(string(out))
	if err != nil && line == "" {
		http.Error(w, "loopback failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	phys := "channel"
	if t == "webrtc" || t == "unix" {
		phys = "call" // the wire-visible surface of these fires is a CALL
	}
	c.publish(fmt.Sprintf(`{"session":"wire","physics":%q,"origin":"COLLECTOR","frame":{"method":"adapter.loopback","params":%s}}`, phys, line))
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, line)
}

// redactHub strips userinfo (user:key creds) from a hub URL for publishing —
// the credential crosses into the registry file only, never into the feed.
func redactHub(hub string) string {
	if u, err := url.Parse(hub); err == nil && u.User != nil {
		u.User = nil
		return u.String()
	}
	return hub
}

// handleAttach — connect ANY existing session from ANY provider, at runtime:
// POST {"hub":"https://user:key@hub.lambdatest.com/wd/hub","session_id":"<sid>"}.
// CALL sessions are driven entirely off hub+id, so on attach the WHOLE cockpit
// works on it — the rail, the Interaction view (inspector-on-attach), /shot,
// /source, /act, liveness tombstoning. Probe verdict beats claim: we attach only
// a session that actually answers a device-touching command (/window/rect).
// CHANNEL attach (a raw ws url) needs a broker spawn + brokers-slice locking —
// deliberately not here yet; this endpoint is the CALL half.
func (c *collector) handleAttach(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var in struct {
		Hub       string `json:"hub"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Hub == "" || in.SessionID == "" {
		http.Error(w, `need {"hub":"https://user:key@hub/wd/hub","session_id":"..."}`, http.StatusBadRequest)
		return
	}
	in.Hub = strings.TrimRight(in.Hub, "/")
	cl := &http.Client{Timeout: 8 * time.Second}
	resp, err := cl.Get(in.Hub + "/session/" + in.SessionID + "/window/rect")
	if err != nil {
		http.Error(w, "probe failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		http.Error(w, fmt.Sprintf("hub answered %d: %s", resp.StatusCode, body), http.StatusBadGateway)
		return
	}
	registerSession(sessionRec{ID: in.SessionID, Hub: in.Hub, Kind: "cloud", Physics: "call"})
	c.publish(fmt.Sprintf(`{"session":%q,"physics":"call","origin":"COLLECTOR","frame":{"method":"session.attach","params":{"hub":%q}}}`, in.SessionID, redactHub(in.Hub)))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"attached":%q,"physics":"call"}`, in.SessionID)
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
	// TMUX SEAT — the agents' surface, enumerated NATIVELY (no claude-deck): tmux
	// itself is the source of truth. Panes are this seat's "tabs"; a pane's title
	// is its story; capture-pane is its frame (/tmuxpane). Same two atoms:
	// send-keys=CALL, capture=afferent, control-mode=CHANNEL.
	if len(tmuxPanes()) > 0 {
		byID["tmux"] = sessionRec{ID: "tmux", Hub: "tmux://local", Kind: "local", Physics: "channel", Stream: "text"}
	}
	// DAEMONS SEAT — the host's background minds (always ≥1: the collector sees
	// ITSELF here — the witness's own body is one of its surfaces).
	if len(daemonList()) > 0 {
		byID["daemons"] = sessionRec{ID: "daemons", Hub: "host://daemons", Kind: "local", Physics: "channel", Stream: "text"}
	}
	// NVIM SEAT — the editor, if a server socket is reachable (buffers as tabs).
	if len(nvimBufs()) > 0 {
		byID["nvim"] = sessionRec{ID: "nvim", Hub: "nvim://" + filepath.Base(nvimSock()), Kind: "local", Physics: "channel", Stream: "text"}
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
			// CHANNEL LIVENESS (2026-08-10) — the other Claude, running this code on
			// its own machine, found the bug: channel sessions were marked live
			// UNCONDITIONALLY ("alive by the broker holding their socket"), an
			// UNVERIFIED prior that my own law forbids — a dead broker showed live
			// forever. Fix per its suggestion: a broker (http hub) is probed at
			// /health, exactly as call sessions are probed at /window/rect. The local
			// text seats (tmux://, host://, nvim://) are verified by their enumerators
			// when added, so they stay live without an http probe.
			if strings.HasPrefix(rec.Hub, "http") {
				go func(rec sessionRec) {
					cl := &http.Client{Timeout: 3 * time.Second}
					resp, err := cl.Get(rec.Hub + "/health")
					rec.Status = "disconnected" // dead/absent broker => not live (was: forever-live)
					if err == nil {
						if resp.StatusCode == 200 {
							rec.Status = "live"
						}
						resp.Body.Close()
					}
					ch <- rec
				}(rec)
				continue
			}
			rec.Status = "live"
			ch <- rec
			continue
		}
		go func(rec sessionRec) {
			cl := &http.Client{Timeout: 4 * time.Second}
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
	// liveness is a HINT, not a death sentence: a CALL session that fails one probe
	// is greyed out of the response (the canvas stops drawing a dead seat) but KEPT
	// in the registry, so a transient blip (device busy, appium mid-command, mjpeg
	// reconnecting) recovers next cycle instead of vanishing forever.
	live := make([]sessionRec, 0, len(all))
	var keepCalls []sessionRec
	for _, s := range all {
		if s.Physics == "call" {
			keepCalls = append(keepCalls, s) // keep every call session on disk
		}
		if s.Status == "disconnected" {
			continue
		}
		live = append(live, s)
	}
	rewriteRegistry(keepCalls)
	// DETERMINISTIC ORDER (2026-08-07): sessions came off a map + a channel —
	// random per request — so the cockpit's decks reshuffled rows on every poll.
	// Position isn't FIXED, it's DETERMINISTIC: a stable rank makes "tmux renders
	// below fox" a verifiable (falsifiable) statement. Browsers, then agents,
	// then daemons, then the rest a-z.
	rank := func(id string) int {
		switch id {
		case "fox":
			return 0
		case "chrome":
			return 1
		case "tmux":
			return 2
		case "nvim":
			return 3
		case "daemons":
			return 4
		}
		return 5
	}
	sort.Slice(live, func(i, j int) bool {
		if ri, rj := rank(live[i].ID), rank(live[j].ID); ri != rj {
			return ri < rj
		}
		return live[i].ID < live[j].ID
	})
	w.Header().Set("Content-Type", "application/json")
	sresp := map[string]any{"sessions": live}
	if len(live) == 0 { // cold instance: no seats. Empty ≠ broken.
		sresp["note"] = "no live seats yet — this is a cold witness. Start the channel Firefox (scripts/up.sh), or run tmux/nvim; seats appear here as they come alive. This is empty, not broken."
	}
	json.NewEncoder(w).Encode(sresp)
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
		Action, Text, Context, Seat, Actor string // #29 Actor = declared identity (falls back to X-8-Actor header)
		X, Y, X2, Y2, Ms                   int
		Xr, Yr                             float64 // 0..1 ratio of the frame (resolution-independent)
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
	// RESOLUTION-INDEPENDENT coords: the cockpit sends 0..1 ratios of where in the frame
	// you clicked (the /drawshot frame's LOD scale is irrelevant). Resolve to the CSS px
	// input.performActions expects via the tab's REAL viewport. This replaces the old
	// dpr-divide, which assumed coords arrived at the frame's NATIVE device resolution —
	// true for full-res captureScreenshot, WRONG once capture moved to LOD stills. x2/y2
	// stay raw (scroll deltas in CSS px). Legacy device-px coords (old replays, no ratio)
	// still take the dpr path.
	if in.Xr > 0 || in.Yr > 0 {
		if vw, vh := c.chanViewport(b, ctx); vw > 0 && vh > 0 {
			in.X = int(math.Round(in.Xr * vw))
			in.Y = int(math.Round(in.Yr * vh))
		}
	} else if dpr := c.chanDPR(b, ctx); dpr > 1 {
		in.X = int(math.Round(float64(in.X) / dpr))
		in.Y = int(math.Round(float64(in.Y) / dpr))
		in.X2 = int(math.Round(float64(in.X2) / dpr))
		in.Y2 = int(math.Round(float64(in.Y2) / dpr))
	}
	c.setFocus(b.id, ctx) // 8 auto-zooms to the tab being driven
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
	case "scroll":
		// BiDi wheel input reports success but does NOT actually scroll in this Firefox.
		// Scroll via JS instead: walk up from the element under the origin to the nearest
		// scrollable ancestor (handles nested scrollers like YouTube's container, not just
		// the window) and scrollBy the delta. X,Y = origin; X2,Y2 = deltaX,deltaY.
		fn := `(x,y,dx,dy)=>{let el=document.elementFromPoint(x,y);while(el){const s=getComputedStyle(el);if(el===document.scrollingElement||((el.scrollHeight>el.clientHeight)&&/(auto|scroll)/.test(s.overflowY))){el.scrollBy(dx,dy);return 'el';}el=el.parentElement;}(document.scrollingElement||document.documentElement).scrollBy(dx,dy);return 'win';}`
		cmd = fmt.Sprintf(`{"method":"script.callFunction","params":{"target":{"context":%q},"functionDeclaration":%q,"awaitPromise":true,"arguments":[{"type":"number","value":%d},{"type":"number","value":%d},{"type":"number","value":%d},{"type":"number","value":%d}]}}`, ctx, fn, in.X, in.Y, in.X2, in.Y2)
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
	id := c.record(reqRec{TS: nowNano(), Physics: "channel", Session: b.id, Method: "act:" + in.Action, URL: b.base + "/command", Body: cmd, Status: 200, Replayable: true, Seat: in.Seat, Actor: actorOf(r, in.Actor)})
	w.Header().Set("Content-Type", "application/json")
	c.witnessHeaders(w, id, "channel", time.Since(start).Milliseconds()) // reafference: what you did, replayable, gaze moved here
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

// handleAct is 8's CONTROL RELAY — it routes an act into the wire on behalf of the pilot, operator, or adapter. 8 NEVER INITIATES acts (the pilot/operator decide); 8 only RELAYS them through the correct physics (call vs channel) and WITNESSES the result. The boundary: control originates outside 8; 8 provides the pipe.
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
		Actor  string `json:"actor"` // #29 declared actor (falls back to X-8-Actor header)
	}
	json.NewDecoder(r.Body).Decode(&in)
	base := rec.Hub + "/session/" + sid
	c.setFocus(sid, "") // 8 auto-zooms to the device being driven
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
	case "scroll":
		// a device scroll IS a swipe opposite the wheel delta (content follows the
		// finger): wheel-down (deltaY>0) → drag finger up. X,Y origin; X2,Y2 deltas.
		url = base + "/actions"
		body = fmt.Sprintf(`{"actions":[{"type":"pointer","id":"finger","parameters":{"pointerType":"touch"},"actions":[{"type":"pointerMove","duration":0,"x":%d,"y":%d},{"type":"pointerDown","button":0},{"type":"pause","duration":80},{"type":"pointerMove","duration":250,"x":%d,"y":%d},{"type":"pointerUp","button":0}]}]}`, in.X, in.Y, in.X-in.X2, in.Y-in.Y2)
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
	c.record(reqRec{TS: nowNano(), Physics: "call", Session: sid, Method: "POST", URL: url, Body: body, Status: resp.StatusCode, Replayable: true, Seat: in.Seat, Actor: actorOf(r, in.Actor)})
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

	// CALL session (WebDriver) with no device mjpeg: stream the wire's NATIVE
	// screenshot in a loop. Heavier than channel (full PNG, not viewport-jpeg), so
	// the default fps is gentle — still a live mirror where /shot was one still.
	if rec != nil && rec.Physics == "call" && !strings.HasPrefix(rec.Stream, "http") {
		fps := 3.0
		if v, err := strconv.ParseFloat(r.URL.Query().Get("fps"), 64); err == nil && v > 0 && v <= 30 {
			fps = v
		}
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		client := &http.Client{Timeout: 10 * time.Second}
		interval := time.Duration(float64(time.Second) / fps)
		url := rec.Hub + "/session/" + sid + "/screenshot"
		fails := 0
		for {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			start := time.Now()
			resp, err := client.Get(url)
			if err != nil {
				if fails++; fails > 40 { // sustained failure → device/socket gone
					return
				}
				time.Sleep(interval)
				continue
			}
			fails = 0
			rb, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
			resp.Body.Close()
			var sv struct {
				Value string `json:"value"`
			}
			json.Unmarshal(rb, &sv)
			if sv.Value != "" {
				if raw, derr := base64.StdEncoding.DecodeString(sv.Value); derr == nil && len(raw) > 0 {
					fmt.Fprintf(w, "--frame\r\nContent-Type: image/png\r\nContent-Length: %d\r\n\r\n", len(raw))
					if _, werr := w.Write(raw); werr != nil {
						return
					}
					io.WriteString(w, "\r\n")
					if flusher != nil {
						flusher.Flush()
					}
				}
			}
			if d := interval - time.Since(start); d > 0 {
				time.Sleep(d)
			}
		}
	}

	// Browser CHANNEL (BiDi): Firefox has NO screencast push — Page.startScreencast
	// is a CDP/Chrome command, and Firefox BiDi answers "unknown command". So the
	// live wire is a tight captureScreenshot loop re-emitted as MJPEG: identical
	// multipart to the device path, so the cockpit <img> renders it live. Frame
	// rate is bounded by the BiDi round-trip (a few fps) — enough to SEE a video
	// tab move, where /shot's slow poll only showed stills. fps/quality tunable.
	if b := c.find(sid); b != nil {
		// Detect physics by probing: browsingContext.getTree is BiDi-only. A CDP
		// (Chrome) broker errors on it -> capture with Page.captureScreenshot and
		// no context (the held ws IS the page). Probe verdict beats a naming prior.
		ctx := r.URL.Query().Get("context")
		isCDP := false
		if tr, err := c.command(b, `{"method":"browsingContext.getTree","params":{}}`); err == nil && strings.Contains(string(tr), `"contexts"`) {
			if ctx == "" {
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
		} else {
			isCDP = true // Chrome: the held CDP ws is the page, no context to resolve
		}
		if !isCDP && ctx == "" {
			http.Error(w, `{"error":"no context"}`, http.StatusBadGateway)
			return
		}
		fps := 6.0
		if v, err := strconv.ParseFloat(r.URL.Query().Get("fps"), 64); err == nil && v > 0 && v <= 30 {
			fps = v
		}
		quality := 0.5
		if v, err := strconv.ParseFloat(r.URL.Query().Get("quality"), 64); err == nil && v >= 0 && v <= 1 {
			quality = v
		}
		lw := lodWidth(r) // LOD: downscale each frame to the card's displayed px width
		if isCDP {
			// EFFICIENT STREAM (Chrome): push, don't poll. Page.startScreencast — no
			// repeated captureScreenshot -> no parent leak, no socket saturation. The
			// Firefox captureScreenshot loop below is the leaky fallback until the
			// drawSnapshot/MediaRecorder Firefox-native path lands.
			c.streamCDP(w, r, b, quality, lw)
			return
		}
		shot := fmt.Sprintf(`{"method":"browsingContext.captureScreenshot","params":{"context":%q,"origin":"viewport","format":{"type":"image/jpeg","quality":%g}}}`, ctx, quality)
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
		w.Header().Set("Cache-Control", "no-cache")
		flusher, _ := w.(http.Flusher)
		interval := time.Duration(float64(time.Second) / fps)
		fails := 0
		for {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			start := time.Now()
			sr, err := c.command(b, shot)
			if err != nil {
				// transient (e.g. socket busy under N-seat contention) — skip this
				// frame and keep streaming; only give up after sustained failure so a
				// momentary timeout never turns a seat permanently black.
				if fails++; fails > 40 {
					return
				}
				time.Sleep(interval)
				continue
			}
			fails = 0
			var s struct {
				Result struct {
					Data string `json:"data"`
				} `json:"result"`
			}
			json.Unmarshal(sr, &s)
			if s.Result.Data != "" {
				if raw, derr := base64.StdEncoding.DecodeString(s.Result.Data); derr == nil && len(raw) > 0 {
					raw = shrinkToRaw(raw, lw) // LOD: only the pixels the card shows
					fmt.Fprintf(w, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(raw))
					if _, werr := w.Write(raw); werr != nil {
						return
					}
					io.WriteString(w, "\r\n")
					if flusher != nil {
						flusher.Flush()
					}
				}
			}
			if d := interval - time.Since(start); d > 0 {
				time.Sleep(d)
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
//
//	READ : script.evaluate returns the exact state (the channel, the value)
//	SEE  : browsingContext.captureScreenshot returns pixels (what "seeing" is)
//
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
		http.Error(w, `{"error":"unknown or missing session — needs ?session=<id>. List live seats at /sessions (e.g. fox, tmux, nvim, daemons). If /sessions is empty this is a cold witness, not broken."}`, http.StatusNotFound)
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
	ID         int64             `json:"id"`
	TS         string            `json:"ts"`
	Physics    string            `json:"physics"` // call (HTTP) | channel (BiDi/CDP)
	Session    string            `json:"session,omitempty"`
	Method     string            `json:"method"` // HTTP verb, or the BiDi/CDP method
	URL        string            `json:"url"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body,omitempty"`
	Status     int               `json:"status"`
	LatUS      float64           `json:"latency_us"`
	RespLen    int               `json:"resp_bytes"`
	RespHead   string            `json:"resp_preview,omitempty"`
	Replayable bool              `json:"replayable"`
	Seat       string            `json:"seat,omitempty"`  // COARSE category, may be guessed: operator | pilot | adapter | ai
	Actor      string            `json:"actor,omitempty"` // #29 X-8-Actor: the DECLARED identity that fired this act. Never guessed — "" means undeclared, which is honest, not "operator".
}

// actorOf reads the DECLARED actor for one act (#29). Precedence: the X-8-Actor
// request header, then an explicit "actor" field the caller put in the body,
// then "" — undeclared. We NEVER infer it from the seat/session: a guessed actor
// is exactly the false provenance this anomaly named. Declared, or nothing.
func actorOf(r *http.Request, bodyActor string) string {
	if r != nil {
		if a := strings.TrimSpace(r.Header.Get("X-8-Actor")); a != "" {
			return a
		}
	}
	return strings.TrimSpace(bodyActor)
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

func dotDir() string       { return os.ExpandEnv("$HOME/.8") }
func requestsFile() string { return dotDir() + "/requests.ndjson" }
func benchesFile() string  { return dotDir() + "/benches.ndjson" }

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
func seriesPath(name string) string {
	return seriesDir() + "/" + strings.ReplaceAll(name, "/", "_") + ".json"
}
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
		// status — and a COMPACT LIVE VIEW of what's been captured, so the canvas
		// can show the recording IN PLACE (a deck of captured command-cards) instead
		// of banishing it to the default feed page. Last 50, newest last; bounded.
		c.rmu.Lock()
		on, name, n := c.recOn, c.recName, len(c.recBuf)
		type capFrame struct {
			Seq     int    `json:"seq"`
			TS      string `json:"ts"`
			Physics string `json:"physics"`
			Seat    string `json:"seat,omitempty"`
			Session string `json:"session,omitempty"`
			Method  string `json:"method"`
			URL     string `json:"url"`
			Status  int    `json:"status"`
		}
		start := 0
		if n > 50 {
			start = n - 50
		}
		caps := make([]capFrame, 0, n-start)
		for i := start; i < n; i++ {
			f := c.recBuf[i]
			caps = append(caps, capFrame{Seq: i + 1, TS: f.TS, Physics: f.Physics, Seat: f.Seat, Session: f.Session, Method: f.Method, URL: f.URL, Status: f.Status})
		}
		c.rmu.Unlock()
		json.NewEncoder(w).Encode(map[string]any{"recording": on, "name": name, "frames": n, "captured": caps})
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

// auth gates every control endpoint when a shared secret is configured. "Trust
// the wire" means trust PROOF on it: a token in X-8-Token (or Authorization:
// Bearer). When no token is set (-token/$EIGHT_TOKEN empty) auth is OFF — the
// local-dev default that preserves the unauthed loopback flow, so this change is
// non-breaking until you choose to lock the surface. /health and OPTIONS stay
// open (liveness + CORS preflight need no secret). Constant-time compare so the
// gate doesn't leak the token by timing.
func auth(token string, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" || r.Method == http.MethodOptions || r.URL.Path == "/health" {
			h.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("X-8-Token")
		if got == "" {
			if b := r.Header.Get("Authorization"); strings.HasPrefix(b, "Bearer ") {
				got = strings.TrimPrefix(b, "Bearer ")
			}
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// cors lets the cockpit (another origin) read /feed and drive /run, /fetch.
// Origin is SCOPED when an allowlist is set: only matching origins are reflected
// (no blanket "*" once you care). Empty allowlist = "*" (local-dev fallback).
func cors(allow map[string]bool, h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if len(allow) == 0 {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" && allow[origin] {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Add("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-8-Token, Authorization")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Expose-Headers", "X-8-Witness, X-8-Ledger, X-8-Ledger-Id, X-8-DMs, X-8-Physics, X-8-Replayable, X-8-Focus-Seq, X-8-Focus-Context")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		h.ServeHTTP(w, r)
	})
}

func main() {
	if len(os.Args) > 1 { // #279: bring-up + supervision, folded into the one binary
		switch os.Args[1] {
		case "up":
			runUp()
			return
		case "watch":
			runWatch()
			return
		}
	}
	listen := flag.String("listen", ":7070", "HTTP address the cockpit reaches the collector on")
	spec := flag.String("brokers", "", "comma list of session=brokerURL (e.g. fox=http://127.0.0.1:4445)")
	gecko := flag.String("gecko", "", "geckodriver session base (http://127.0.0.1:4444/session/<id>) — enables /procinfo per-tab mem/CPU")
	fxdriver := flag.String("fxdriver", os.ExpandEnv("$HOME/Desktop/repos/adapters/browser/firefox-stream.js"), "path to the Firefox capture driver (adapters/browser/firefox-stream.js)")
	fxshot := flag.String("fxshot", os.ExpandEnv("$HOME/Desktop/repos/adapters/browser/firefox-drawshot.js"), "path to the Firefox leak-free still driver (adapters/browser/firefox-drawshot.js)")
	sessionFile := flag.String("session-file", os.ExpandEnv("$HOME/.8/gecko.json"), "file where up.sh publishes the current geckodriver SID; the collector re-reads it to auto-recover the session after a Firefox recycle")
	apertureMB := flag.Int("aperture-mb", 3000, "soft parent+children memory threshold (MB): above this the FAIR aperture parks the heaviest non-focused tab (gBrowser.discardBrowser — memory freed, tab identity preserved, reloads on focus). INVARIANT: no tab is ever closed. 0 disables. Watchdog 4500 full-recycle stays as the backstop.")
	token := flag.String("token", os.Getenv("EIGHT_TOKEN"), "shared secret required on every endpoint via X-8-Token/Bearer (empty = auth off, local-dev default)")
	origins := flag.String("origins", os.Getenv("EIGHT_ORIGINS"), "comma CORS origin allowlist, e.g. http://localhost:8088 (empty = *, local-dev)")
	flag.Parse()
	persistBootArgs() // #347: record the wired argv so `up` boots and `watch` revives NON-bare

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
	c.fxDriver = *fxdriver
	c.fxShot = *fxshot
	c.sessionFile = *sessionFile
	if i := strings.LastIndex(c.gecko, "/session/"); i >= 0 {
		c.geckoRoot = c.gecko[:i] // for rebuilding the base when the session recovers
	} else {
		c.geckoRoot = strings.TrimRight(c.gecko, "/")
	}
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
	// GENTLE STARTUP (feature A): a collector restart used to barrage a Firefox that
	// had JUST recycled and was still starting up — crashing it. Serve HTTP immediately
	// (so /health is up), but hold the Firefox-touching goroutines until a getTree probe
	// says Firefox is ready, then start them STAGGERED so no wave of requests hits it.
	go func() {
		c.waitReady()
		for _, b := range c.brokers {
			go c.pump(ctx, b)
			time.Sleep(100 * time.Millisecond)
		}
		time.Sleep(200 * time.Millisecond)
		go c.activateCockpit() // every (re)start returns Firefox to 8's tab
		time.Sleep(200 * time.Millisecond)
		go c.memoryAperture(*apertureMB) // self-regulate parent memory (heap-minimize at the soft threshold)
		go c.reconcileLoop()             // keep the tab manifest true even when no cockpit is watching
		go c.seatWatchLoop()             // #11: PUSH change events for every seat (the CHANNEL atom, realized)
		go c.paneWitnessLoop()           // #277: witness pane appearance (first_seen + pane.appeared/vanished)
		go c.staleLoop()                 // #472b: revert 6h-silent doing items — the WIP lane self-heals
		go c.epochLoop()                 // #645: the operator-epoch clock — ledger locked to operator-time
		go func() {                      // project eight.db every 30s so DBeaver always sees fresh data
			if _, err := exec.LookPath("sqlite3"); err != nil {
				return
			}
			c.syncDB()
			t := time.NewTicker(30 * time.Second)
			defer t.Stop()
			for range t.C {
				c.syncDB()
			}
		}()
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("/feed", c.handleFeed)
	mux.HandleFunc("/run", c.handleRun)
	mux.HandleFunc("/broadcast", c.handleBroadcast)
	mux.HandleFunc("/fetch", c.handleFetch)
	mux.HandleFunc("/shot", c.handleShot)
	mux.HandleFunc("/drawshot", c.handleDrawShot) // leak-free Firefox periphery still (replaces /shot)
	mux.HandleFunc("/tabs", c.handleTabs)
	mux.HandleFunc("/procinfo", c.handleProcInfo)
	mux.HandleFunc("/drawprobe", c.handleDrawProbe)
	mux.HandleFunc("/fxchunk", c.handleFxChunk)   // HiddenFrame driver POSTs WebM here
	mux.HandleFunc("/fxstream", c.handleFxStream) // cockpit MSE consumes WebM here
	mux.HandleFunc("/fxstart", c.handleFxStart)   // inject the capture driver for a tab
	mux.HandleFunc("/fxstop", c.handleFxStop)
	mux.HandleFunc("/fxstats", c.handleFxStats)
	mux.HandleFunc("/fxdiag", c.handleFxDiag)
	mux.HandleFunc("/sessions", c.handleSessions)
	mux.HandleFunc("/attach", c.handleAttach)             // connect any provider's live session (CALL) at runtime
	mux.HandleFunc("/adapters/fire", c.handleAdapterFire) // fire a real adapter loopback (grpc|mqtt|webrtc|unix), witnessed
	mux.HandleFunc("/source", c.handleSource)
	mux.HandleFunc("/read", c.handleRead) // #643: read an OPEN tab; create only on explicit intent
	mux.HandleFunc("/act", c.handleAct)
	mux.HandleFunc("/stream", c.handleStream)
	mux.HandleFunc("/bench", c.handleBench)
	mux.HandleFunc("/requests", c.handleRequests)
	mux.HandleFunc("/replay", c.handleReplay)
	mux.HandleFunc("/record", c.handleRecord)
	mux.HandleFunc("/series", c.handleSeries)
	mux.HandleFunc("/replay-series", c.handleReplaySeries)
	mux.HandleFunc("/benches", c.handleBenches)
	mux.HandleFunc("/focus", c.handleFocus)
	// SELF-DESCRIBING ROOT (2026-08-11): two fresh agents (and this session's own
	// habituated author) hit bare 404s at / with zero discovery signal. The wire
	// should tell a newcomer what it is and how to touch it — the same self-evidence
	// `discover` gives, at the front door. GET / → what/how/endpoints.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{
  "what": "8 — the WITNESS. It watches every surface on this machine (browser tabs, tmux panes, nvim buffers, host daemons) as one provenance-tracked manifest, streams all traffic live, and replays it.",
  "how": "The wire reduces to two atoms: CALL (one request/response) and CHANNEL (one held bidirectional socket). Prefer the http-mcp tools (http_request / bidi_command / discover) — they ARE the wire; discover self-describes any hub. curl works too, but discover beats guessing.",
  "start_here": {
    "what_is_alive": "GET /sessions   (seat ids: fox, tmux, nvim, daemons)",
    "how_many_tabs": "GET /tabs?session=fox   (needs the session param)",
    "who_opened_what": "GET /manifest   (uid/what/where/who/why/when)",
    "the_map_of_gaps": "GET /matrix   (surfaces × senses — what is NOT yet seeable)",
    "the_plan": "GET /work   (the dependency DAG that auto-advances)",
    "drive_a_tab": "POST /run?session=fox {\"method\":\"...\",\"params\":{}}   (every op returns an X-8-Witness reafference receipt)",
    "self_describe": "point http-mcp discover at http://127.0.0.1:4445"
  },
  "views": "the cockpit at http://localhost:8088 has four: feed · CANVAS (every tab a live card) · LAB · WIRE"
}
`)
	})
	mux.HandleFunc("/health", c.handleHealth)
	mux.HandleFunc("/park", c.handlePark)         // manually park a tab (#7, symmetric with /wake)
	mux.HandleFunc("/wake", c.handleWake)         // un-park a parked tab (#7 click-to-wake)
	mux.HandleFunc("/matrix", c.handleMatrix)     // surfaces × senses coverage — the map of the unfound
	mux.HandleFunc("/claim", c.handleClaim)       // an agent leases a tab (verify/falsify: is anyone using it?)
	mux.HandleFunc("/dedup", c.handleDedup)       // same-URL duplicates; close the unclaimed ones
	mux.HandleFunc("/manifest", c.handleManifest) // durable tab manifest: how-many/what/where/who/why/when
	mux.HandleFunc("/tmuxpane", c.handleTmuxPane)
	mux.HandleFunc("/tmuxsummary", c.handleTmuxSummary)   // a tmux pane's visible text — the agents' surface frame
	mux.HandleFunc("/attention", c.handleAttention)       // #22a the research-programme READING packet (Lakatos/Kuhn), witness-only
	mux.HandleFunc("/tmuxchannel", c.handleTmuxChannel)   // #35 native tmux CHANNEL (control-mode push), gated off the live server
	mux.HandleFunc("/tmuxsend", c.handleTmuxSend)         // the tmux seat's CONTROL verb (seen ⇒ controllable)
	mux.HandleFunc("/nvimbuf", c.handleNvimBuf)           // an nvim buffer's lines — the editor seat's frame
	mux.HandleFunc("/nvimopen", c.handleNvimOpen)         // the editor seat's CONTROL verb (:buffer N)
	mux.HandleFunc("/daemonframe", c.handleDaemonFrame)   // a daemon's frame: ps line + log tail
	mux.HandleFunc("/daemonsignal", c.handleDaemonSignal) // the daemons seat's CONTROL verb (watchdog protected)
	mux.HandleFunc("/identity", c.handleIdentity)         // #437: live identity — GET derives, POST declares
	mux.HandleFunc("/db", c.handleDB)                     // project the scattered stores into ~/.8/eight.db for DBeaver
	mux.HandleFunc("/stopwatch", c.handleStopwatch)       // experiri: the witness's staleness made readable
	mux.HandleFunc("/work", c.handleWork)
	mux.HandleFunc("/work/next", c.handleWorkNext)
	mux.HandleFunc("/work/playlist", c.handlePlaylist)
	mux.HandleFunc("/panes", c.handlePanes)          // #277 witnessed pane roster (first_seen per pane)
	mux.HandleFunc("/panes/send", c.handlePanesSend) // type one prompt into selected panes / all live claude at once — the operator+agent fan-out nerve
	mux.HandleFunc("/sql", c.handleSQL)              // #280 the DB primitive: a CALL dialect over eight.db (modernc, witnessed; #315)
	mux.HandleFunc("/watch", c.handleWatch)
	mux.HandleFunc("/timeline", c.handleTimeline)     // #13 the interleaved cross-substrate timeline
	mux.HandleFunc("/provenance", c.handleProvenance) // #29 actor→atom→surface flow (declared X-8-Actor)
	mux.HandleFunc("/witnessed", c.handleWitnessed)   // #70 sink: cmd/wire POSTs observed MITM calls → 8 ledger (creds redacted)               // #12 change-detector: push tab.changed for a watched tab // run the queue hands-free, one-by-one like a playlist     // the queue PICKER — next unblocked todo, one by one               // the shared task surface — operator writes, agents check FIRST

	allow := map[string]bool{}
	for _, o := range strings.Split(*origins, ",") {
		if o = strings.TrimSpace(o); o != "" {
			allow[o] = true
		}
	}
	authState := "OFF (local-dev)"
	if *token != "" {
		authState = "ON (X-8-Token/Bearer)"
	}
	originState := "* (local-dev)"
	if len(allow) > 0 {
		originState = *origins
	}
	log.Printf("collector: %d session(s), serving on %s — auth %s, cors origins %s", len(brokers), *listen, authState, originState)
	// BIND-RETRY (#18, 2026-08-11): a redeploy (kill old → start new) can race the
	// OS releasing the port, so a plain ListenAndServe would exit 'address already
	// in use' and leave a ~3s gap until the watchdog revived it. Retry the bind for
	// ~3s first; the old process's TIME_WAIT clears well within that, so deploys are
	// seamless instead of a blip. (The audit's closest-to-broken finding.)
	handler := cors(allow, auth(*token, witnessEvery(mux))) // #616: receipts on everything
	var ln net.Listener
	var lerr error
	for i := 0; i < 30; i++ {
		if ln, lerr = net.Listen("tcp", *listen); lerr == nil {
			break
		}
		log.Printf("bind %s busy (%v) — retrying %d/30", *listen, lerr, i+1)
		time.Sleep(100 * time.Millisecond)
	}
	if lerr != nil {
		log.Fatalf("could not bind %s after ~3s of retries: %v", *listen, lerr)
	}
	log.Fatal(http.Serve(ln, handler))
}
