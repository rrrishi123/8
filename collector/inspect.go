package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── PROCESS FACTS + INSPECTOR (2026-09-15) — what only a shell could see ───────
// A pane is a pod: its Claude process has a pid, a resident size, a context
// weight (the last API usage its transcript recorded) and — when launched with
// BUN_INSPECT=ws://127.0.0.1:<port>/dbg — a live WebKit inspector. All of it is
// now on /panes, and /panes/inspect?pane=%N&expr=… evaluates an expression
// inside the pane's process over that socket (loopback only). The collector
// stays stdlib: the WebSocket client below is the RFC 6455 minimum.

type procFacts struct {
	Pid       int    `json:"pid,omitempty"`
	RSSMB     int    `json:"rss_mb,omitempty"`
	Inspector string `json:"inspector,omitempty"` // ws://127.0.0.1:<port>/dbg when the process listens on loopback
	CtxTokens int64  `json:"context_tokens,omitempty"`
	CtxAt     string `json:"context_at,omitempty"`
	ProbedAt  string `json:"proc_probed_at,omitempty"`
}

var (
	procMu     sync.Mutex
	procByPane = map[string]procFacts{}
	lastProcs  time.Time
)

// probeProcs — every ~30s: pane_pid -> child claude pid -> rss + listening
// loopback port; plus the transcript's newest usage (cheap tail read).
func probeProcs(now time.Time) {
	if now.Sub(lastProcs) < 30*time.Second {
		return
	}
	lastProcs = now
	tb := tmuxBin()
	if tb == "" {
		return
	}
	out, err := tmuxOut(tb, "list-panes", "-a", "-F", "#{pane_id}|#{pane_pid}|#{pane_current_command}")
	if err != nil {
		return
	}
	uu := paneUUIDs()
	fresh := map[string]procFacts{}
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(ln, "|")
		if len(f) != 3 {
			continue
		}
		pane, ppid, cmd := f[0], f[1], f[2]
		switch cmd {
		case "claude.exe", "claude", "node":
		default:
			continue
		}
		pf := procFacts{ProbedAt: now.UTC().Format(time.RFC3339)}
		if o, e := exec.Command("pgrep", "-P", ppid).Output(); e == nil {
			for _, p := range strings.Fields(string(o)) {
				if pid, _ := strconv.Atoi(p); pid > 0 {
					pf.Pid = pid
					break
				}
			}
		}
		if pf.Pid > 0 {
			if o, e := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pf.Pid)).Output(); e == nil {
				if kb, _ := strconv.Atoi(strings.TrimSpace(string(o))); kb > 0 {
					pf.RSSMB = kb / 1024
				}
			}
			pf.Inspector = inspectorFor(pf.Pid)
		}
		if u := uu[ppid]; u != "" {
			if jp := jsonlForUUID(u); jp != "" {
				pf.CtxTokens, pf.CtxAt = lastUsage(jp)
			}
		}
		fresh[pane] = pf
	}
	procMu.Lock()
	procByPane = fresh
	procMu.Unlock()
}

func procOf(pane string) procFacts {
	procMu.Lock()
	defer procMu.Unlock()
	return procByPane[pane]
}

// inspectorFor — the first loopback TCP port the process listens on, as our
// BUN_INSPECT convention ws://127.0.0.1:<port>/dbg. Blank when none.
func inspectorFor(pid int) string {
	o, err := exec.Command("lsof", "-a", "-p", strconv.Itoa(pid), "-iTCP", "-sTCP:LISTEN", "-P", "-n", "-Fn").Output()
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(o), "\n") {
		if !strings.HasPrefix(ln, "n") {
			continue
		}
		addr := ln[1:]
		if strings.HasPrefix(addr, "127.0.0.1:") || strings.HasPrefix(addr, "localhost:") {
			if _, port, ok := strings.Cut(addr, ":"); ok {
				return "ws://127.0.0.1:" + port + "/dbg"
			}
		}
	}
	return ""
}

// lastUsage — newest assistant usage in a transcript (context that turn =
// input + cache_read + cache_creation), from the file's last 512KB.
func lastUsage(path string) (int64, string) {
	f, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer f.Close()
	st, _ := f.Stat()
	off := st.Size() - 512*1024
	if off < 0 {
		off = 0
	}
	b := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(b, off); err != nil && err != io.EOF {
		return 0, ""
	}
	lines := bytes.Split(b, []byte("\n"))
	for i := len(lines) - 1; i >= 0; i-- {
		if !bytes.Contains(lines[i], []byte(`"cache_read_input_tokens"`)) {
			continue
		}
		var e struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Usage struct {
					In    int64 `json:"input_tokens"`
					Read  int64 `json:"cache_read_input_tokens"`
					Write int64 `json:"cache_creation_input_tokens"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(lines[i], &e) == nil && e.Type == "assistant" {
			u := e.Message.Usage
			return u.In + u.Read + u.Write, e.Timestamp
		}
	}
	return 0, ""
}

// ── minimal RFC 6455 client (text frames, loopback only) ─────────────────────
type wsConn struct {
	c  net.Conn
	br *bufio.Reader
}

func wsDial(raw string, timeout time.Duration) (*wsConn, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "ws" {
		return nil, errors.New("ws:// url required")
	}
	host := u.Hostname()
	if host != "127.0.0.1" && host != "localhost" {
		return nil, errors.New("loopback only")
	}
	c, err := net.DialTimeout("tcp", u.Host, timeout)
	if err != nil {
		return nil, err
	}
	c.SetDeadline(time.Now().Add(timeout))
	key := make([]byte, 16)
	rand.Read(key)
	k := base64.StdEncoding.EncodeToString(key)
	path := u.Path
	if path == "" {
		path = "/"
	}
	fmt.Fprintf(c, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", path, u.Host, k)
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		c.Close()
		return nil, err
	}
	if resp.StatusCode != 101 {
		c.Close()
		return nil, fmt.Errorf("upgrade refused: %s", resp.Status)
	}
	h := sha1.Sum([]byte(k + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	if resp.Header.Get("Sec-WebSocket-Accept") != base64.StdEncoding.EncodeToString(h[:]) {
		c.Close()
		return nil, errors.New("bad accept key")
	}
	return &wsConn{c: c, br: br}, nil
}

func wsFrame(payload []byte) []byte {
	var hdr []byte
	n := len(payload)
	switch {
	case n < 126:
		hdr = []byte{0x81, byte(0x80 | n)}
	case n < 65536:
		hdr = []byte{0x81, 0x80 | 126, 0, 0}
		binary.BigEndian.PutUint16(hdr[2:], uint16(n))
	default:
		hdr = make([]byte, 10)
		hdr[0], hdr[1] = 0x81, 0x80|127
		binary.BigEndian.PutUint64(hdr[2:], uint64(n))
	}
	mask := make([]byte, 4)
	rand.Read(mask)
	out := append(hdr, mask...)
	for i, b := range payload {
		out = append(out, b^mask[i%4])
	}
	return out
}

func (w *wsConn) sendText(s string) error { _, err := w.c.Write(wsFrame([]byte(s))); return err }

func (w *wsConn) readText() (string, error) {
	for {
		h := make([]byte, 2)
		if _, err := io.ReadFull(w.br, h); err != nil {
			return "", err
		}
		op := h[0] & 0x0f
		n := uint64(h[1] & 0x7f)
		masked := h[1]&0x80 != 0
		switch n {
		case 126:
			e := make([]byte, 2)
			io.ReadFull(w.br, e)
			n = uint64(binary.BigEndian.Uint16(e))
		case 127:
			e := make([]byte, 8)
			io.ReadFull(w.br, e)
			n = binary.BigEndian.Uint64(e)
		}
		var mask []byte
		if masked {
			mask = make([]byte, 4)
			io.ReadFull(w.br, mask)
		}
		p := make([]byte, n)
		if _, err := io.ReadFull(w.br, p); err != nil {
			return "", err
		}
		if masked {
			for i := range p {
				p[i] ^= mask[i%4]
			}
		}
		switch op {
		case 0x1:
			return string(p), nil
		case 0x8:
			return "", errors.New("closed")
		default: // ping/pong/binary: ignore
		}
	}
}

// wsEval — Runtime.enable + Runtime.evaluate(returnByValue) over the inspector.
func wsEval(wsurl, expr string) (json.RawMessage, error) {
	w, err := wsDial(wsurl, 5*time.Second)
	if err != nil {
		return nil, err
	}
	defer w.c.Close()
	call := func(id int, method string, params any) (map[string]any, error) {
		b, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err := w.sendText(string(b)); err != nil {
			return nil, err
		}
		for {
			s, err := w.readText()
			if err != nil {
				return nil, err
			}
			var m map[string]any
			if json.Unmarshal([]byte(s), &m) == nil {
				if fid, ok := m["id"].(float64); ok && int(fid) == id {
					return m, nil
				}
			}
		}
	}
	if _, err := call(1, "Runtime.enable", map[string]any{}); err != nil {
		return nil, err
	}
	m, err := call(2, "Runtime.evaluate", map[string]any{"expression": expr, "returnByValue": true})
	if err != nil {
		return nil, err
	}
	if e, ok := m["error"]; ok {
		b, _ := json.Marshal(e)
		return nil, errors.New(string(b))
	}
	b, _ := json.Marshal(m["result"])
	return b, nil
}

// handlePaneInspect — GET /panes/inspect?pane=%N[&expr=...] (or ?url=ws://127.0.0.1:PORT/dbg).
// Default expression: the process's own view of its weight.
func (c *collector) handlePaneInspect(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	pane, wsurl := r.URL.Query().Get("pane"), r.URL.Query().Get("url")
	if wsurl == "" && pane != "" {
		wsurl = procOf(pane).Inspector
	}
	if wsurl == "" {
		http.Error(w, `{"error":"no inspector for that pane — launch it with BUN_INSPECT=ws://127.0.0.1:<port>/dbg"}`, 404)
		return
	}
	expr := r.URL.Query().Get("expr")
	if expr == "" {
		expr = `({pid:process.pid,rss_mb:process.memoryUsage().rss/1048576|0,heap_mb:process.memoryUsage().heapUsed/1048576|0,uptime_s:process.uptime()|0,bun:Bun.version})`
	}
	res, err := wsEval(wsurl, expr)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"pane": pane, "inspector": wsurl, "error": err.Error()})
		return
	}
	c.publish(fmt.Sprintf(`{"session":"panes","origin":"COLLECTOR","frame":{"method":"pane.inspect","params":{"pane":%q,"inspector":%q,"expr":%q}}}`, pane, wsurl, firstN(expr, 80)))
	_ = json.NewEncoder(w).Encode(map[string]any{"pane": pane, "inspector": wsurl, "expr": expr, "result": res})
}

// ── /panes/usage — the session's own token record, turn by turn ────────────────
// GET /panes/usage?pane=%N[&n=40]: the newest N assistant turns from the pane's
// CURRENT session file (identity via ~/.claude/sessions/<pid>.json, so a /clear
// is followed), with every usage field the API returned. This is the measured
// weight; the TUI's /context is an estimate derived from the same lines.
type usageTurn struct {
	TS        string `json:"ts"`
	Model     string `json:"model,omitempty"`
	Input     int64  `json:"input_tokens"`
	CacheRead int64  `json:"cache_read_input_tokens"`
	CacheNew  int64  `json:"cache_creation_input_tokens"`
	Output    int64  `json:"output_tokens"`
	Thinking  int64  `json:"thinking_tokens,omitempty"`
	Context   int64  `json:"context_tokens"` // input + cache_read + cache_creation
}

func usageTurns(path string, n int) []usageTurn {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	st, _ := f.Stat()
	off := st.Size() - 4*1024*1024
	if off < 0 {
		off = 0
	}
	b := make([]byte, st.Size()-off)
	if _, err := f.ReadAt(b, off); err != nil && err != io.EOF {
		return nil
	}
	lines := bytes.Split(b, []byte("\n"))
	var out []usageTurn
	for i := len(lines) - 1; i >= 0 && len(out) < n; i-- {
		if !bytes.Contains(lines[i], []byte(`"cache_read_input_tokens"`)) {
			continue
		}
		var e struct {
			Type      string `json:"type"`
			Timestamp string `json:"timestamp"`
			Message   struct {
				Model string `json:"model"`
				Usage struct {
					In    int64 `json:"input_tokens"`
					Read  int64 `json:"cache_read_input_tokens"`
					Write int64 `json:"cache_creation_input_tokens"`
					Out   int64 `json:"output_tokens"`
					Det   struct {
						Thinking int64 `json:"thinking_tokens"`
					} `json:"output_tokens_details"`
				} `json:"usage"`
			} `json:"message"`
		}
		if json.Unmarshal(lines[i], &e) != nil || e.Type != "assistant" {
			continue
		}
		u := e.Message.Usage
		out = append(out, usageTurn{TS: e.Timestamp, Model: e.Message.Model, Input: u.In, CacheRead: u.Read, CacheNew: u.Write, Output: u.Out, Thinking: u.Det.Thinking, Context: u.In + u.Read + u.Write})
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 { // oldest first
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (c *collector) handlePaneUsage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	pane := r.URL.Query().Get("pane")
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 || n > 500 {
		n = 40
	}
	pf := procOf(pane)
	sid := ""
	if pf.Pid > 0 {
		sid = sessionIDFor(strconv.Itoa(pf.Pid))
	}
	if sid == "" {
		if tb := tmuxBin(); tb != "" {
			if o, e := tmuxOut(tb, "display-message", "-p", "-t", pane, "#{pane_pid}"); e == nil {
				sid = paneUUIDs()[strings.TrimSpace(string(o))]
			}
		}
	}
	jp := jsonlForUUID(sid)
	var size int64
	if st, err := os.Stat(jp); err == nil {
		size = st.Size()
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"pane": pane, "session": sid, "jsonl": jp, "jsonl_bytes": size,
		"pid": pf.Pid, "rss_mb": pf.RSSMB, "inspector": pf.Inspector, "state": tuiOf(pane).State,
		"turns": usageTurns(jp, n),
		"note": "context_tokens = input + cache_read + cache_creation, as the API billed that turn; the TUI /context is an estimate",
	})
}
