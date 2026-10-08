package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// ── NVIM — the editor seat, over msgpack-rpc WITHOUT a msgpack codec ─────────
// nvim's own binary is the msgpack client: `nvim --server <sock> --remote-expr`
// evaluates vimscript against a running nvim over its socket, and `--remote-send`
// types into it. So 8 speaks the editor's held CHANNEL through the same shell-out
// pattern as tmux/sqlite3 — stdlib-only, no dependency. Buffers are this seat's
// tabs; a buffer's lines are its frame; :buffer N (switch) is its control verb.
func nvimBin() string {
	for _, p := range []string{"/opt/homebrew/bin/nvim", "/usr/local/bin/nvim", "/usr/bin/nvim"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	if p, err := exec.LookPath("nvim"); err == nil {
		return p
	}
	return ""
}

// nvimSock finds the first REACHABLE nvim server socket. Platform-agnostic: nvim
// puts sockets under $TMPDIR/nvim.$USER/*/ (macOS) or $XDG_RUNTIME_DIR (Linux);
// $NVIM_8_SOCK overrides. Empty when nvim is absent or no server runs — the seat
// simply doesn't appear.
func nvimSock() string {
	nb := nvimBin()
	if nb == "" {
		return ""
	}
	var cands []string
	if s := os.Getenv("NVIM_8_SOCK"); s != "" {
		cands = append(cands, s)
	}
	roots := []string{os.TempDir(), os.Getenv("XDG_RUNTIME_DIR"), "/tmp"}
	user := os.Getenv("USER")
	for _, root := range roots {
		if root == "" {
			continue
		}
		for _, pat := range []string{
			filepath.Join(root, "nvim."+user, "*", "nvim.*"),
			filepath.Join(root, "nvim.*"),
			filepath.Join(root, "nvimsocket"),
		} {
			if m, _ := filepath.Glob(pat); len(m) > 0 {
				cands = append(cands, m...)
			}
		}
	}
	for _, s := range cands {
		if fi, err := os.Stat(s); err != nil || fi.Mode()&os.ModeSocket == 0 {
			continue
		}
		// reachable = it answers a trivial expr fast
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		out, err := exec.CommandContext(ctx, nb, "--server", s, "--remote-expr", "1").Output()
		cancel()
		if err == nil && strings.TrimSpace(string(out)) == "1" {
			return s
		}
	}
	return ""
}

type nvimBufRec struct {
	Nr      int    `json:"nr"`
	Name    string `json:"name"`
	Lines   int    `json:"lines"`
	Changed int    `json:"changed"`
	Active  bool   `json:"active"`
}

func nvimBufs() []nvimBufRec {
	sock := nvimSock()
	nb := nvimBin()
	if sock == "" || nb == "" {
		return nil
	}
	expr := `json_encode(map(getbufinfo({"buflisted":1}), {i,b -> {"nr":b.bufnr,"name":fnamemodify(b.name,":t"),"lines":b.linecount,"changed":b.changed,"active":b.bufnr==bufnr("")}}))`
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, nb, "--server", sock, "--remote-expr", expr).Output()
	if err != nil {
		return nil
	}
	var bufs []nvimBufRec
	json.Unmarshal(out, &bufs)
	return bufs
}

// handleNvimBuf returns a buffer's lines — the editor seat's frame (afferent).
// GET /nvimbuf?buf=N. Capped at 500 lines so a huge buffer stays a card.
func (c *collector) handleNvimBuf(w http.ResponseWriter, r *http.Request) {
	buf, _ := strconv.Atoi(r.URL.Query().Get("buf"))
	sock, nb := nvimSock(), nvimBin()
	if buf <= 0 || sock == "" || nb == "" {
		http.Error(w, `{"error":"need buf=N and a running nvim"}`, http.StatusBadRequest)
		return
	}
	expr := fmt.Sprintf(`join(getbufline(%d, 1, 500), "\n")`, buf)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, nb, "--server", sock, "--remote-expr", expr).Output()
	if err != nil {
		http.Error(w, `{"error":"read failed"}`, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(out)
}

// handleNvimOpen — the editor seat's CONTROL verb (seen ⇒ controllable): switch
// the active buffer. GET /nvimopen?buf=N. The paired sense-change is visible —
// the ACTIVE buffer moves, which the next frame/enumerate reports.
func (c *collector) handleNvimOpen(w http.ResponseWriter, r *http.Request) {
	buf, _ := strconv.Atoi(r.URL.Query().Get("buf"))
	sock, nb := nvimSock(), nvimBin()
	if buf <= 0 || sock == "" || nb == "" {
		http.Error(w, `{"error":"need buf=N and a running nvim"}`, http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	exec.CommandContext(ctx, nb, "--server", sock, "--remote-expr", fmt.Sprintf(`execute("buffer %d")`, buf)).Run()
	c.publish(fmt.Sprintf(`{"session":"nvim","origin":"COLLECTOR","frame":{"method":"nvim.buffer","params":{"buf":%d}}}`, buf))
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprintf(w, `{"opened":%d}`, buf)
}
