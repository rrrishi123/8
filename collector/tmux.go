package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// handleTmuxSummary — WHAT HAS THIS CLAUDE BEEN DOING. A tmux pane running a
// sibling Claude has its own jsonl transcript; this reads it (pane cwd → the
// project dir → the newest session) and returns a summary: recent prompts, the
// tool-use count, the last thing it said. Refreshed on request from 8 so we can
// focus attention on a sibling's conversation without commanding it — the
// witness READS a peer's history, it doesn't drive it. GET /tmuxsummary?pane=%N.
func (c *collector) handleTmuxSummary(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	tb := tmuxBin()
	if !strings.HasPrefix(pane, "%") || tb == "" {
		http.Error(w, `{"error":"need pane=%N"}`, http.StatusBadRequest)
		return
	}
	cwdb, err := exec.Command(tb, "display-message", "-p", "-t", pane, "#{pane_current_path}").Output()
	if err != nil {
		http.Error(w, `{"error":"no such pane"}`, http.StatusNotFound)
		return
	}
	cwd := strings.TrimSpace(string(cwdb))
	// Claude Code encodes a project's cwd by replacing every '/' with '-'.
	projDir := os.ExpandEnv("$HOME/.claude/projects/") + strings.ReplaceAll(cwd, "/", "-")
	newest, newestT := paneJsonl(tb, pane, projDir) // THIS pane's own transcript, not newest-by-mtime
	resp := map[string]any{"pane": pane, "cwd": cwd}
	if newest == "" {
		resp["note"] = "no Claude transcript for this pane's cwd — not a Claude session, or a fresh one"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
		return
	}
	data, _ := os.ReadFile(newest)
	prompts := []string{}
	tools, turns := 0, 0
	lastAsst := ""
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var o struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(ln), &o) != nil {
			continue
		}
		role := o.Message.Role
		if role == "" {
			role = o.Type
		}
		if role == "user" {
			var s string
			if json.Unmarshal(o.Message.Content, &s) == nil {
				if s = strings.TrimSpace(s); s != "" && !strings.HasPrefix(s, "<") && !strings.HasPrefix(s, "[Request") {
					prompts = append(prompts, firstN(s, 120))
				}
			}
		} else if role == "assistant" {
			turns++
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			}
			if json.Unmarshal(o.Message.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.Type == "tool_use" {
						tools++
					} else if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
						lastAsst = firstN(strings.TrimSpace(b.Text), 200)
					}
				}
			}
		}
	}
	if len(prompts) > 5 {
		prompts = prompts[len(prompts)-5:]
	}
	resp["jsonl"] = newest
	resp["session"] = strings.TrimSuffix(filepath.Base(newest), ".jsonl")
	resp["updated"] = newestT.UTC().Format(time.RFC3339)
	resp["turns"] = turns
	resp["tool_uses"] = tools
	resp["recent_prompts"] = prompts
	resp["last_said"] = lastAsst
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

// handleFocus (#22a) — FOCUSED ATTENTION, not a chat summary. /tmuxsummary gives
// the observations (Tycho); this assembles a packet a MIND reads to derive the
// laws (Kepler): the pane's own words+acts PLUS the dynamic research-programme
// scaffold (Lakatos/Kuhn). The collector is a witness, not a mind — so it does
// NOT write the reading; it hands the material and the lens to whoever will.
// The instruction is deliberately "read the NATURE OF THE WORDS", never
// "summarise": the reading must be dynamic to what this pane is actually doing.
// GET /attention?pane=%N. Witness-only: reading never drives the pane.
func (c *collector) handleAttention(w http.ResponseWriter, r *http.Request) {
	pane := r.URL.Query().Get("pane")
	tb := tmuxBin()
	if !strings.HasPrefix(pane, "%") || tb == "" {
		http.Error(w, `{"error":"need pane=%N"}`, http.StatusBadRequest)
		return
	}
	cwdb, err := exec.Command(tb, "display-message", "-p", "-t", pane, "#{pane_current_path}").Output()
	if err != nil {
		http.Error(w, `{"error":"no such pane"}`, http.StatusNotFound)
		return
	}
	cwd := strings.TrimSpace(string(cwdb))
	projDir := os.ExpandEnv("$HOME/.claude/projects/") + strings.ReplaceAll(cwd, "/", "-")
	newest, newestT := paneJsonl(tb, pane, projDir) // THIS pane's own transcript (same-cwd fix)
	packet := map[string]any{"pane": pane, "cwd": cwd}
	if newest == "" {
		packet["note"] = "no Claude transcript for this pane's cwd — nothing to focus on yet"
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(packet)
		return
	}
	// richer material than /tmuxsummary: the ARC (ordered tool sequence reveals
	// what it's DOING), fuller prompts, and the last several things it SAID — the
	// raw vocabulary the reading is derived from.
	data, _ := os.ReadFile(newest)
	prompts, saids, toolSeq := []string{}, []string{}, []string{}
	tools, turns := 0, 0
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var o struct {
			Type    string `json:"type"`
			Message struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(ln), &o) != nil {
			continue
		}
		role := o.Message.Role
		if role == "" {
			role = o.Type
		}
		if role == "user" {
			var s string
			if json.Unmarshal(o.Message.Content, &s) == nil {
				if s = strings.TrimSpace(s); s != "" && !strings.HasPrefix(s, "<") && !strings.HasPrefix(s, "[Request") {
					prompts = append(prompts, firstN(s, 240))
				}
			}
		} else if role == "assistant" {
			turns++
			var blocks []struct {
				Type string `json:"type"`
				Text string `json:"text"`
				Name string `json:"name"`
			}
			if json.Unmarshal(o.Message.Content, &blocks) == nil {
				for _, b := range blocks {
					if b.Type == "tool_use" {
						tools++
						if b.Name != "" {
							toolSeq = append(toolSeq, b.Name)
						}
					} else if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
						saids = append(saids, firstN(strings.TrimSpace(b.Text), 240))
					}
				}
			}
		}
	}
	tail := func(s []string, n int) []string {
		if len(s) > n {
			return s[len(s)-n:]
		}
		return s
	}
	packet["jsonl"] = newest
	packet["session"] = strings.TrimSuffix(filepath.Base(newest), ".jsonl")
	packet["updated"] = newestT.UTC().Format(time.RFC3339)
	packet["material"] = map[string]any{
		"turns": turns, "tool_uses": tools,
		"recent_prompts": tail(prompts, 10),
		"recent_said":    tail(saids, 6),
		"tool_arc":       tail(toolSeq, 40), // the ORDER of acts — what it's been doing, not just how much
	}
	// The lens is the deliverable's spine: a mind reads the material ABOVE through
	// THIS frame, filling each slot from the pane's own words+acts.
	packet["reading"] = map[string]any{
		"lens":        "research-programme (Lakatos/Kuhn) — dynamic, derived from the nature of the words this pane used",
		"instruction": "Do NOT summarise the conversation. Read THIS pane's own prompts, statements, and the ORDER of its acts, and articulate its research programme: what it treats as unfalsifiable vs. adjustable, whether it is predicting-then-verifying or only patching, what it is driving toward, and — most important — what its own words reveal it is NOT yet attending to.",
		"scaffold": []string{
			"hard_core — the commitments this pane will not abandon (visible in what it never questions)",
			"protective_belt — the auxiliary moves it makes to defend the core (the fixes, the reframings)",
			"progressive_or_degenerating — is it predicting novel facts then verifying them, or only absorbing anomalies after the fact?",
			"projected_result — what outcome the trajectory of its acts is aimed at",
			"anomalies_unattended — what the words surface but the pane has not turned to (the frontier it is ignoring)",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(packet)
}

// ── #35 FIX #30: NATIVE tmux CHANNEL (control mode) ─────────────────────────
// The #30 anomaly proved watch-then-publish (the 1.5s poll) is NOT the CHANNEL
// atom. tmux control mode (-C) IS: a held bidirectional client to which tmux
// PUSHES %output/%window-* frames unsolicited. This holds that client and
// publishes those frames into 8's feed as native tmux.output/tmux.changed — the
// atom realized off-browser, not emulated.
var tmuxChMu sync.Mutex
var tmuxChOn = map[string]bool{}

func (c *collector) tmuxControlChannel(socketName, target string) {
	key := socketName + "/" + target
	tmuxChMu.Lock()
	if tmuxChOn[key] {
		tmuxChMu.Unlock()
		return
	}
	tmuxChOn[key] = true
	tmuxChMu.Unlock()
	defer func() { tmuxChMu.Lock(); delete(tmuxChOn, key); tmuxChMu.Unlock() }()

	tb := tmuxBin()
	if tb == "" {
		return
	}
	args := []string{}
	if socketName != "" {
		args = append(args, "-L", socketName)
	}
	args = append(args, "-C", "attach", "-t", target)
	cmd := exec.Command(tb, args...)
	stdin, err := cmd.StdinPipe() // held open so the control client stays attached
	if err != nil {
		return
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return
	}
	if err := cmd.Start(); err != nil {
		return
	}
	defer cmd.Process.Kill()
	defer stdin.Close()
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "%output "):
			rest := line[len("%output "):]
			pane, data := rest, ""
			if sp := strings.IndexByte(rest, ' '); sp >= 0 {
				pane, data = rest[:sp], rest[sp+1:]
			}
			db, _ := json.Marshal(data)
			c.publish(fmt.Sprintf(`{"session":"tmux","origin":"COLLECTOR","physics":"channel","frame":{"method":"tmux.output","params":{"pane":%q,"data":%s,"native":true}}}`, pane, string(db)))
		case strings.HasPrefix(line, "%window-add"), strings.HasPrefix(line, "%window-close"),
			strings.HasPrefix(line, "%window-renamed"), strings.HasPrefix(line, "%session-window-changed"),
			strings.HasPrefix(line, "%layout-change"), strings.HasPrefix(line, "%unlinked-window"):
			evt := strings.Fields(line)[0]
			c.publish(fmt.Sprintf(`{"session":"tmux","origin":"COLLECTOR","physics":"channel","frame":{"method":"tmux.changed","params":{"native":true,"evt":%q}}}`, evt))
		}
	}
}

// handleTmuxChannel starts the native channel. GATED: refuses the LIVE default
// server (a -C client perturbs the working tmux 8 lives in) unless
// EIGHT_TMUX_CHANNEL_LIVE=1. Pass ?socket=<name> to prove on a dedicated server.
func (c *collector) handleTmuxChannel(w http.ResponseWriter, r *http.Request) {
	sock := r.URL.Query().Get("socket")
	sess := r.URL.Query().Get("session")
	if sess == "" {
		sess = "0"
	}
	if sock == "" && os.Getenv("EIGHT_TMUX_CHANNEL_LIVE") != "1" {
		http.Error(w, `{"error":"refusing a control client on the LIVE tmux server (would perturb it). Pass ?socket=<name> for a dedicated server, or set EIGHT_TMUX_CHANNEL_LIVE=1 to opt in."}`, http.StatusForbidden)
		return
	}
	go c.tmuxControlChannel(sock, sess)
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"started":true,"socket":%q,"session":%q,"native_channel":true}`, sock, sess)
}

// handleTmuxSend — the tmux seat's CONTROL verb (seen ⇒ controllable, the seat
// contract's third leg). GET /tmuxsend?pane=%25N&keys=...&enter=1 → send-keys -l
// (literal). Witnessed: publishes a tmux.send frame to the feed.
func (c *collector) handleTmuxSend(w http.ResponseWriter, r *http.Request) {
	pane, keys := r.URL.Query().Get("pane"), r.URL.Query().Get("keys")
	ok := strings.HasPrefix(pane, "%") && len(pane) > 1
	for _, ch := range pane[1:] {
		if ch < '0' || ch > '9' {
			ok = false
			break
		}
	}
	tb := tmuxBin()
	if !ok || tb == "" || keys == "" {
		http.Error(w, `{"error":"need pane=%N and keys="}`, http.StatusBadRequest)
		return
	}
	if err := exec.Command(tb, "send-keys", "-t", pane, "-l", keys).Run(); err != nil {
		http.Error(w, `{"error":"send failed"}`, http.StatusBadGateway)
		return
	}
	if r.URL.Query().Get("enter") == "1" {
		exec.Command(tb, "send-keys", "-t", pane, "Enter").Run()
	}
	c.publish(fmt.Sprintf(`{"session":"tmux","origin":"COLLECTOR","frame":{"method":"tmux.send","params":{"pane":%q,"keys":%q}}}`, pane, keys))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"sent":%q}`, pane)
}

// ── EXPERIRI: the stopwatch — the witness's calibration instrument ────────────
// "Realtime" is invisible on a static page but SELF-EVIDENT on a clock: point 8
// at a surface whose content IS time, and staleness becomes a readable number
// (displayed − true). Served by the collector itself so the one binary carries
// its own falsification instrument. Operational definition under test:
// to OBSERVE = to hold a frame whose staleness is bounded and KNOWN.
const stopwatchHTML = `<!doctype html><html><head><meta charset="utf-8"><title>experiri · stopwatch</title><style>body{margin:0;background:#000;color:#39ff14;font-family:ui-monospace,Menlo,monospace;display:flex;flex-direction:column;align-items:center;justify-content:center;height:100vh}#wall{font-size:11vw;font-weight:700;letter-spacing:.04em}#el{font-size:4.5vw;color:#9ece6a;opacity:.85}#note{font-size:1.5vw;color:#666;margin-top:3vh;max-width:80vw;text-align:center}</style></head><body><div id="wall"></div><div id="el"></div><div id="note">experiri · compare these digits AS SEEN THROUGH 8 against the true clock at capture — the difference IS the witness's staleness</div><script>const t0=performance.now();const p=(n,w)=>String(n).padStart(w,"0");function tick(){const d=new Date();wall.textContent=p(d.getHours(),2)+":"+p(d.getMinutes(),2)+":"+p(d.getSeconds(),2)+"."+p(d.getMilliseconds(),3);const e=performance.now()-t0,s=Math.floor(e/1000);el.textContent="elapsed "+p(Math.floor(s/60),2)+":"+p(s%60,2)+"."+p(Math.floor(e%1000),3);}tick();setInterval(tick,16)</script></body></html>`
