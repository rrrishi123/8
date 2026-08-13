package main

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ── DAEMONS — the host's background minds, 8's own body included ─────────────
// Browsers and tmux are NOT part of the four-body system; they are SURFACES it
// witnesses via the seat contract: {enumerate, frame, control}. Daemons are one
// more kind: enumerate = pgrep, frame = ps line + log tail, control = signals
// (not yet wired — an honest seat-contract gap).
type daemonSpec struct {
	name, pat, log string
	protected      bool // the reviver: the system may not saw off the branch that catches it
}

var daemonSpecs = []daemonSpec{
	{"collector", "collector/collector -listen", "/tmp/collector-8.log", false},
	{"broker-fox", "channel -ws", "/tmp/broker-8.log", false},
	{"geckodriver", "geckodriver --port", "/tmp/geckodriver.log", false},
	{"cockpit-vite", "vite", "", false},
	{"watchdog", "scripts/watchdog.sh", "/tmp/watchdog-8.log", true},
	{"pilot", "pilot -daemon", "", false},
	{"ollama", "ollama serve", "", false},
	{"tailscaled", "tailscaled", "", false},
	{"claude-deck", "claude-deck", "", false},
	// HOST INFRA the witness SEES but the system does NOT own (office-private,
	// provider/auth-bound — must never ship in the single binary; witnessable,
	// not absorbable — the eight.db-vs-tunnel boundary, 2026-08-07 work #8):
	{"adaptive-tunnel", "AdaptiveDesktop.app.*adaptive connect", "$HOME/.lt-tunnels/adaptive-supervisor.log", false},
	{"dbeaver", "DBeaver.app", "", false},
}

type daemonRec struct{ Name, Pid, Info string }

func daemonList() []daemonRec {
	var out []daemonRec
	for _, d := range daemonSpecs {
		pb, err := exec.Command("pgrep", "-o", "-f", d.pat).Output()
		if err != nil {
			continue
		}
		pids := strings.Fields(strings.TrimSpace(string(pb)))
		if len(pids) == 0 {
			continue
		}
		info := ""
		if ib, e := exec.Command("ps", "-o", "rss=,etime=", "-p", pids[0]).Output(); e == nil {
			if f := strings.Fields(string(ib)); len(f) >= 2 {
				if kb, _ := strconv.Atoi(f[0]); kb > 0 {
					info = fmt.Sprintf("%dMB · up %s", kb/1024, f[1])
				}
			}
		}
		out = append(out, daemonRec{d.name, pids[0], info})
	}
	return out
}

// tailFile returns the last n lines of a file, reading at most 16KB from its end.
func tailFile(p string, n int) string {
	f, err := os.Open(p)
	if err != nil {
		return ""
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return ""
	}
	var off int64
	if st.Size() > 16384 {
		off = st.Size() - 16384
	}
	buf := make([]byte, st.Size()-off)
	f.ReadAt(buf, off)
	lines := strings.Split(string(buf), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// handleDaemonSignal — the daemons seat's CONTROL verb (seen ⇒ controllable,
// third leg of the seat contract). GET /daemonsignal?d=<name>&sig=TERM|HUP|KILL
// &by=<who>. The pid is resolved SERVER-SIDE from the spec pattern — a caller
// names a daemon, never a pid. TERM to a supervised daemon = restart (its
// reviver brings it back). The watchdog is PROTECTED from TERM/KILL: it is the
// reviver. Self-signal (the collector) responds first, dies 400ms later — the
// witness kills itself through itself and is reborn by the watchdog.
func (c *collector) handleDaemonSignal(w http.ResponseWriter, r *http.Request) {
	name, sig, by := r.URL.Query().Get("d"), r.URL.Query().Get("sig"), r.URL.Query().Get("by")
	if sig == "" {
		sig = "TERM"
	}
	if by == "" {
		by = "undeclared"
	}
	if sig != "TERM" && sig != "HUP" && sig != "KILL" {
		http.Error(w, `{"error":"sig must be TERM|HUP|KILL"}`, http.StatusBadRequest)
		return
	}
	for _, d := range daemonSpecs {
		if d.name != name {
			continue
		}
		if d.protected && sig != "HUP" {
			http.Error(w, `{"error":"`+name+` is PROTECTED — it is the reviver; killing it leaves nothing to catch the others. HUP is allowed."}`, http.StatusForbidden)
			return
		}
		var pid string
		if d.name == "collector" {
			pid = strconv.Itoa(os.Getpid()) // I resolve MYSELF by identity, never by pattern —
			// pgrep -f once matched a probe SHELL whose cmdline merely CONTAINED the
			// pattern, and the verb killed the observer (2026-08-07, exit 144).
		} else {
			pb, err := exec.Command("pgrep", "-o", "-f", d.pat).Output()
			pids := strings.Fields(strings.TrimSpace(string(pb)))
			if err != nil || len(pids) == 0 {
				http.Error(w, `{"error":"not running"}`, http.StatusNotFound)
				return
			}
			pid = pids[0]
		}
		self := pid == strconv.Itoa(os.Getpid())
		c.publish(fmt.Sprintf(`{"session":"daemons","origin":"COLLECTOR","frame":{"method":"daemon.signal","params":{"daemon":%q,"pid":%s,"sig":%q,"by":%q,"self":%v}}}`, name, pid, sig, by, self))
		if self { // respond first, die after — the reviver brings the witness back
			go func() {
				time.Sleep(400 * time.Millisecond)
				exec.Command("kill", "-"+sig, pid).Run()
			}()
		} else if e := exec.Command("kill", "-"+sig, pid).Run(); e != nil {
			http.Error(w, `{"error":"signal failed"}`, http.StatusBadGateway)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"daemon":%q,"pid":%s,"sig":%q,"self":%v}`, name, pid, sig, self)
		return
	}
	http.Error(w, `{"error":"unknown daemon"}`, http.StatusNotFound)
}

// handleDaemonFrame — a daemon's "frame": its ps line + the tail of its log.
func (c *collector) handleDaemonFrame(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("d")
	for _, d := range daemonSpecs {
		if d.name != name {
			continue
		}
		var b strings.Builder
		if pb, err := exec.Command("pgrep", "-o", "-f", d.pat).Output(); err == nil {
			if pids := strings.Fields(strings.TrimSpace(string(pb))); len(pids) > 0 {
				if ib, e := exec.Command("ps", "-o", "pid=,rss=,etime=,command=", "-p", pids[0]).Output(); e == nil {
					b.WriteString(strings.TrimSpace(string(ib)) + "\n")
				}
			}
		}
		if d.log != "" {
			b.WriteString("\n── log ──\n" + tailFile(os.ExpandEnv(d.log), 24))
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		io.WriteString(w, b.String())
		return
	}
	http.Error(w, `{"error":"unknown daemon"}`, http.StatusNotFound)
}
