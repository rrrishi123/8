package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// ── CONTEXT COMPOSER (2026-09-16) — the vertical-scaling knob ─────────────────
// A resumed Claude loads whatever transcript sits at its session path; the CLI
// replays it. So a session's STARTING context is a thing we can CHOOSE, by
// writing the jsonl it resumes from. Proven 2026-09-16: a hand-written
// header + one seed line resumes at the ~33k floor with the seed remembered.
//
// select:
//   "floor"    header + optional seed only  → ~33k (system+tools+memory+seed)
//   "tail:N"   the last N user-rooted turns of source, re-chained  → chosen slice
//   "head:N"   the first N
// A mind is VERTICAL scaling (how much context one pane carries); many panes are
// HORIZONTAL (how many minds). /compose spins a fresh horizontal unit carrying a
// chosen vertical slice — e.g. clone conductor's last 8 turns into a cheap worker.
// The envelope proven minimal: mode + permission-mode header, then
// parentUuid-chained {type,message,sessionId,cwd,version,timestamp} lines.

type composeReq struct {
	Select string `json:"select"` // floor | tail:N | head:N
	Source string `json:"source"` // uuid or role name to slice from (empty for floor)
	Seed   string `json:"seed"`   // optional charter user message, appended last
	Cwd    string `json:"cwd"`
	Port   int    `json:"port"` // inspector port for the launch line (0 = auto)
}

func composeVersion() string {
	// mirror a live session file's version so the resume is not rejected
	if b, err := os.ReadFile(os.ExpandEnv("$HOME/.8/sessions-version")); err == nil {
		if v := strings.TrimSpace(string(b)); v != "" {
			return v
		}
	}
	return "2.1.259"
}

func freePort(start int) int {
	for p := start; p < start+200; p++ {
		if l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(p)); err == nil {
			l.Close()
			return p
		}
	}
	return start
}

// composeMessages — the source's user-rooted turns as raw message envelopes
// (type + message only; re-stamped on write). A turn starts at a user line.
func sourceTurns(path string) [][]map[string]json.RawMessage {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var turns [][]map[string]json.RawMessage
	var cur []map[string]json.RawMessage
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var e map[string]json.RawMessage
		if json.Unmarshal([]byte(ln), &e) != nil {
			continue
		}
		var t string
		json.Unmarshal(e["type"], &t)
		if t != "user" && t != "assistant" {
			continue
		}
		if _, hasMsg := e["message"]; !hasMsg {
			continue
		}
		// a real user turn (not a tool_result continuation) opens a new turn
		isTurnStart := false
		if t == "user" {
			var msg struct {
				Content json.RawMessage `json:"content"`
			}
			json.Unmarshal(e["message"], &msg)
			// string content = a typed prompt = turn start; array = tool_result = mid-turn
			if len(msg.Content) > 0 && msg.Content[0] == '"' {
				isTurnStart = true
			}
		}
		if isTurnStart && len(cur) > 0 {
			turns = append(turns, cur)
			cur = nil
		}
		cur = append(cur, e)
	}
	if len(cur) > 0 {
		turns = append(turns, cur)
	}
	return turns
}

func (c *collector) handleCompose(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		http.Error(w, `{"error":"POST {select,source,seed,cwd,port}"}`, 405)
		return
	}
	var req composeReq
	if json.NewDecoder(r.Body).Decode(&req) != nil {
		http.Error(w, `{"error":"bad json"}`, 400)
		return
	}
	if req.Select == "" {
		req.Select = "floor"
	}
	if req.Cwd == "" {
		req.Cwd = os.ExpandEnv("$HOME/Desktop/repos")
	}
	newID := uuid.NewString()
	ver := composeVersion()
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	dir := filepath.Dir(jsonlForUUID(roleUUID("conductor")))
	if dir == "." || dir == "" {
		dir = os.ExpandEnv("$HOME/.claude/projects/-Users-rishirajs-Desktop-repos")
	}
	path := filepath.Join(dir, newID+".jsonl")

	var b strings.Builder
	enc := func(v any) { j, _ := json.Marshal(v); b.Write(j); b.WriteByte('\n') }
	enc(map[string]any{"type": "mode", "mode": "normal", "sessionId": newID})
	enc(map[string]any{"type": "permission-mode", "permissionMode": "bypassPermissions", "sessionId": newID})

	parent := ""
	writeMsg := func(typ string, msg json.RawMessage) {
		u := uuid.NewString()
		row := map[string]any{"type": typ, "uuid": u, "parentUuid": nil, "isSidechain": false,
			"cwd": req.Cwd, "sessionId": newID, "version": ver, "gitBranch": "", "timestamp": now, "message": msg}
		if parent != "" {
			row["parentUuid"] = parent
		}
		if typ == "user" {
			row["userType"] = "external"
		}
		enc(row)
		parent = u
	}

	kept := 0
	if strings.HasPrefix(req.Select, "tail:") || strings.HasPrefix(req.Select, "head:") {
		n, _ := strconv.Atoi(strings.SplitN(req.Select, ":", 2)[1])
		src := req.Source
		if u := roleUUID(src); u != "" {
			src = u
		}
		turns := sourceTurns(jsonlForUUID(src))
		if strings.HasPrefix(req.Select, "tail:") && n < len(turns) {
			turns = turns[len(turns)-n:]
		} else if strings.HasPrefix(req.Select, "head:") && n < len(turns) {
			turns = turns[:n]
		}
		for _, turn := range turns {
			for _, e := range turn {
				var t string
				json.Unmarshal(e["type"], &t)
				writeMsg(t, e["message"])
				kept++
			}
		}
	}
	if req.Seed != "" {
		seedMsg, _ := json.Marshal(map[string]any{"role": "user", "content": req.Seed})
		writeMsg("user", seedMsg)
		kept++
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), 500)
		return
	}
	port := req.Port
	if port == 0 {
		port = freePort(65030)
	}
	launch := fmt.Sprintf("BUN_INSPECT=ws://127.0.0.1:%d/dbg claude --resume %s --dangerously-skip-permissions", port, newID)
	c.publish(fmt.Sprintf(`{"session":"panes","origin":"COLLECTOR","frame":{"method":"mind.compose","params":{"uuid":%q,"select":%q,"kept_msgs":%d}}}`, newID, req.Select, kept))
	_ = json.NewEncoder(w).Encode(map[string]any{
		"new_uuid": newID, "jsonl": path, "select": req.Select, "source": req.Source,
		"kept_messages": kept, "inspector_port": port, "launch": launch,
		"note": "resume this uuid to bring up a mind at the chosen context; floor ≈ 33k (system+tools+memory+seed)",
	})
}

// ── DELEGATE (2026-09-16) — cheap work in a FRESH context ─────────────────────
// My own turns re-send the whole transcript: ~357k tokens billed per turn even
// when 1k is new. A fresh `claude -p` starts at the ~31k floor, does one task,
// exits — ~10x cheaper. So isolated work should be DELEGATED, not done inline.
// This spawns `claude -p <task> --output-format json` (its own new session), and
// returns the result WITH the usage it billed, so the fleet routes by cost and
// the budget/phase governs whether to spend. Like kosaten's delegate_work, but
// first-party and budget-aware. BUN_INSPECT is stripped (else port collision).
type delegateReq struct {
	Task     string `json:"task"`
	Cwd      string `json:"cwd"`
	TimeoutS int    `json:"timeout_s"`
}

func (c *collector) handleDelegate(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		http.Error(w, `{"error":"POST {task,cwd,timeout_s}"}`, 405)
		return
	}
	var req delegateReq
	if json.NewDecoder(r.Body).Decode(&req) != nil || strings.TrimSpace(req.Task) == "" {
		http.Error(w, `{"error":"need a task"}`, 400)
		return
	}
	if req.Cwd == "" {
		req.Cwd = os.ExpandEnv("$HOME/Desktop/repos")
	}
	if req.TimeoutS <= 0 || req.TimeoutS > 900 {
		req.TimeoutS = 300
	}
	// budget guard: never spend when the hard cap has gated dispatch.
	if b := c.budgetNow(); b != nil && b.Gated {
		http.Error(w, fmt.Sprintf(`{"error":"budget gated: %s"}`, b.Reason), 429)
		return
	}
	start := time.Now()
	ctx, cancel := contextWithTimeout(req.TimeoutS)
	defer cancel()
	cmd := execCommandContext(ctx, "claude", "-p", req.Task, "--output-format", "json", "--dangerously-skip-permissions")
	cmd.Dir = req.Cwd
	cmd.Env = envWithout(os.Environ(), "BUN_INSPECT")
	cmd.Stdin = devNull()
	out, err := cmd.Output()
	secs := time.Since(start).Seconds()
	var env struct {
		Result      string          `json:"result"`
		SessionID   string          `json:"session_id"`
		IsError     bool            `json:"is_error"`
		TotalCost   float64         `json:"total_cost_usd"`
		Usage       json.RawMessage `json:"usage"`
		usageParsed struct {
			In    int64 `json:"input_tokens"`
			Read  int64 `json:"cache_read_input_tokens"`
			Write int64 `json:"cache_creation_input_tokens"`
			Out   int64 `json:"output_tokens"`
		}
	}
	_ = json.Unmarshal(out, &env)
	json.Unmarshal(env.Usage, &env.usageParsed)
	cost := env.usageParsed.In + env.usageParsed.Read + env.usageParsed.Write
	resp := map[string]any{
		"result": env.Result, "session_id": env.SessionID, "is_error": env.IsError,
		"cost_tokens": cost, "cost_usd": env.TotalCost, "seconds": int(secs),
		"note": "fresh claude -p floor context; ~10x cheaper than an inline turn on a large session",
	}
	if err != nil && env.Result == "" {
		resp["error"] = err.Error()
		if len(out) > 0 {
			resp["raw"] = string(out[:min(len(out), 500)])
		}
	}
	c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"work.delegate","params":{"session":%q,"cost_tokens":%d,"seconds":%d}}}`, env.SessionID, cost, int(secs)))
	_ = json.NewEncoder(w).Encode(resp)
}
