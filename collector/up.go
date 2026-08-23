package main

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// ── 8 up — the bring-up as a WIRE PROGRAM folded into the one binary (#279),
// replacing scripts/up.sh (which hardcoded /Users/rishirajs, the office firefox
// profile ~/.ltqa-firefox-deepseek, a DeepSeek chat URL, and unix-only
// lsof/pgrep/jq). The theory we settled and now apply:
//   BUNDLE the machinery  — our Go binaries, paths from os.Executable, never hardcoded
//   DISCOVER the substrate — firefox/geckodriver/tmux via LookPath; probe, don't assume
//   NEVER inscribe the host — nothing external to the four bodies + probed substrate
// Missing substrate DEGRADES gracefully: no firefox -> collector-only is still a
// valid boot (the claude-web-container case).

type substrate struct {
	OS      string
	Tmux    string
	Firefox string
	Gecko   string
}

func look(name string) string { p, _ := exec.LookPath(name); return p }

func firefoxCandidates() []string {
	switch runtime.GOOS {
	case "darwin":
		return []string{"/Applications/Firefox.app/Contents/MacOS/firefox"}
	case "linux":
		return []string{"/usr/bin/firefox", "/usr/local/bin/firefox", "/snap/bin/firefox"}
	}
	return nil
}

func discoverSubstrate() substrate {
	s := substrate{OS: runtime.GOOS, Tmux: look("tmux"), Gecko: look("geckodriver"), Firefox: look("firefox")}
	if s.Firefox == "" { // not on PATH — try the OS's standard install location
		for _, cand := range firefoxCandidates() {
			if st, err := os.Stat(cand); err == nil && !st.IsDir() {
				s.Firefox = cand
				break
			}
		}
	}
	return s
}

func portUp(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// repoRoot derives the four-body root from THIS binary's location — the collector
// lives at <root>/8/collector/collector. Never a hardcoded /Users path.
func repoRoot() string {
	if exe, err := os.Executable(); err == nil {
		// <root>/8/collector/collector -> up 3 (collector dir, 8 dir, root)
		return filepath.Dir(filepath.Dir(filepath.Dir(exe)))
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}

func runUp() {
	root := repoRoot()
	s := discoverSubstrate()
	fmt.Printf("8 up — os=%s root=%s\n", s.OS, root)
	report := func(name, v string) {
		if v == "" {
			fmt.Printf("  %-13s absent (probe/provision)\n", name+":")
		} else {
			fmt.Printf("  %-13s %s\n", name+":", v)
		}
	}
	report("tmux", s.Tmux)
	report("firefox", s.Firefox)
	report("geckodriver", s.Gecko)

	self, _ := os.Executable()

	// BODY 1 — the collector (this binary). Bundled; needs no substrate. Idempotent.
	cargs := collectorArgs()
	if portUp(probeAddr(cargs)) {
		fmt.Printf("  collector:    already up on %s\n", probeAddr(cargs))
	} else {
		cmd := exec.Command(self, cargs...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Printf("  collector:    FAILED: %v\n", err)
		} else {
			fmt.Printf("  collector:    started (pid %d) args=%v\n", cmd.Process.Pid, cargs)
		}
	}
	// #347 gap 3, scope stated: the web cockpit is a vite app — `up` does not
	// start it (scripts/cockpit.sh does); folding a static web/dist serve into
	// the binary is a filed follow-up, not a silent omission.
	fmt.Println("  cockpit:      out of scope — vite app (scripts/cockpit.sh); static-serve fold-in is filed")

	// BODY 3 — the wire (MITM witness proxy, GET /host). Bundled machinery from
	// http-mcp; absent binary degrades with a build hint (#311: shipped features
	// must be live features — /host was code-only for 5 days because nothing
	// started the wire).
	wireUp(root, probeAddr(cargs))

	// BODY 2 — the firefox seat. DISCOVERED substrate; absent => dormant, not fatal.
	if s.Firefox == "" || s.Gecko == "" {
		fmt.Println("  browser:      DORMANT — firefox/geckodriver absent; collector-only is a valid boot")
		return
	}
	if portUp("127.0.0.1:4444") {
		fmt.Println("  browser:      seat already up on :4444")
		return
	}
	packUp(root)
}

// wireBin discovers the wire binary — build.sh output first, then the install.sh
// target, then PATH. Never a hardcoded user path.
func wireBin(root string) string {
	for _, p := range []string{
		filepath.Join(root, "http-mcp", ".bin", "wire"),
		os.ExpandEnv("$HOME/.8/bin/wire"),
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return look("wire")
}

// wireUp starts the wire on :4724 witnessing into the collector (shared by `up`
// and the watch guard). Idempotent; absent binary degrades, never fatal.
func wireUp(root, collectorAddr string) {
	if portUp("127.0.0.1:4724") {
		fmt.Println("  wire:         already up on :4724")
		return
	}
	wb := wireBin(root)
	if wb == "" {
		fmt.Println("  wire:         binary absent (http-mcp build.sh, or install.sh) — /host dormant")
		return
	}
	cmd := exec.Command(wb, "-listen", ":4724", "-witness", "http://"+collectorAddr)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Printf("  wire:         FAILED: %v\n", err)
	} else {
		fmt.Printf("  wire:         started (pid %d, witness -> %s)\n", cmd.Process.Pid, collectorAddr)
	}
}

// packUp launches the firefox seat via the browser pack (shared by `up` and the
// watch guard). The pack owns replace-stale-seat semantics; we just fire it.
func packUp(root string) {
	pack := filepath.Join(root, "adapters", "browser", "browser")
	profile := filepath.Join(root, "8", ".firefox-profile") // OUR profile, not the office one
	if _, err := os.Stat(pack); err != nil {
		fmt.Printf("  browser:      pack not built at %s (run adapters build.sh)\n", pack)
		return
	}
	cmd := exec.Command(pack, "up", "--engine", "firefox", "--port", "4444", "--profile", profile)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Printf("  browser:      seat FAILED: %v\n", err)
	} else {
		fmt.Printf("  browser:      seat starting via browser pack (pid %d, profile %s)\n", cmd.Process.Pid, profile)
	}
}

// ── boot.json — launch flags persisted (#347 gaps 1+2). Serve mode records its
// own argv, so `up` boots and `watch` revives the collector WIRED the way the
// operator last ran it (brokers, gecko session, session-file), never bare. A
// faithful copy of argv beats reconstructing flags.
func bootFile() string { return os.ExpandEnv("$HOME/.8/boot.json") }

func persistBootArgs() {
	os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755)
	if b, err := json.Marshal(os.Args[1:]); err == nil {
		_ = os.WriteFile(bootFile(), b, 0o644)
	}
}

// collectorArgs — how up/watch start the collector: the persisted launch flags
// when present; else a discovered minimum (probe the broker port, wire the
// session file if one exists) — never inscribe, never assume.
func collectorArgs() []string {
	if b, err := os.ReadFile(bootFile()); err == nil {
		var a []string
		if json.Unmarshal(b, &a) == nil && len(a) > 0 {
			return a
		}
	}
	args := []string{"-listen", ":7070"}
	if portUp("127.0.0.1:4445") {
		args = append(args, "-brokers", "fox=http://127.0.0.1:4445")
	}
	if sf := os.ExpandEnv("$HOME/.8/gecko.json"); func() bool { _, e := os.Stat(sf); return e == nil }() {
		args = append(args, "-session-file", sf)
	}
	return args
}

// probeAddr — the dialable address of the collector those args would start
// (":7070" -> "127.0.0.1:7070"), so up/watch probe the RIGHT port when the
// operator runs a nonstandard -listen.
func probeAddr(args []string) string {
	addr := ":7070"
	for i, a := range args {
		if a == "-listen" && i+1 < len(args) {
			addr = args[i+1]
		}
	}
	if strings.HasPrefix(addr, ":") {
		addr = "127.0.0.1" + addr
	}
	return addr
}

// foxSeat — (pid, rssMB) of the PARENT firefox running our pack profile, via
// POSIX ps (the one external probe watch makes; mac has no /proc). Judge the
// PROCESS, never the BiDi socket — under capture load the socket saturates and
// looks dead while Firefox is merely busy (watchdog.sh's hard lesson).
func foxSeat() (int, int) {
	out, err := exec.Command("ps", "-axo", "pid=,rss=,command=").Output()
	if err != nil {
		return 0, 0
	}
	for _, ln := range strings.Split(string(out), "\n") {
		if !strings.Contains(ln, "8/.firefox-profile") || strings.Contains(ln, "-contentproc") || strings.Contains(ln, "geckodriver") {
			continue // parent surface only: children carry -contentproc
		}
		f := strings.Fields(ln)
		if len(f) < 3 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		rssKB, _ := strconv.Atoi(f[1])
		if pid > 0 {
			return pid, rssKB / 1024
		}
	}
	return 0, 0
}

// runWatch — `8 watch`: supervision folded into the one binary (#279, #347).
// Every 15s: (1) collector port down -> revive it WIRED from boot.json (a bare
// revival would silently drop the fox/gecko wiring — worse than the bash
// watchdog it replaces); (2) firefox guard, but ONLY for a seat this watch has
// SEEN alive — it never starts a browser the operator didn't. Process-judged
// death -> pack relaunch; parent RSS >4500MB on two consecutive ticks (the
// sustained-bloat recycle threshold, not a single oscillation spike) -> pack
// replace. The pack owns the actual seat semantics.
func runWatch() {
	self, _ := os.Executable()
	root := repoRoot()
	fmt.Println("8 watch — supervising the collector + a seen firefox seat (Go os/exec + POSIX ps)")
	seenFox, highMem := false, 0
	for {
		if cargs := collectorArgs(); !portUp(probeAddr(cargs)) {
			cmd := exec.Command(self, cargs...)
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if err := cmd.Start(); err == nil {
				fmt.Printf("  collector was down -> revived (pid %d) args=%v\n", cmd.Process.Pid, cargs)
			}
		}
		if !portUp("127.0.0.1:4724") { // wire down -> revive, same contract as the collector
			wireUp(root, probeAddr(collectorArgs()))
		}
		pid, rssMB := foxSeat()
		if pid != 0 && portUp("127.0.0.1:4444") {
			seenFox = true
		}
		if seenFox {
			switch {
			case pid == 0: // the process is gone — not a busy socket, gone
				fmt.Println("  firefox seat gone -> pack relaunch")
				packUp(root)
				highMem = 0
			case rssMB > 4500:
				highMem++
				if highMem >= 2 { // sustained, not a spike (aperture oscillates ~3.5-4.6GB)
					fmt.Printf("  firefox parent %dMB sustained -> pack recycle\n", rssMB)
					packUp(root)
					highMem = 0
				}
			default:
				highMem = 0
			}
		}
		time.Sleep(15 * time.Second)
	}
}
