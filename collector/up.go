package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	if portUp("127.0.0.1:7070") {
		fmt.Println("  collector:    already up on :7070")
	} else {
		cmd := exec.Command(self, "-listen", ":7070")
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			fmt.Printf("  collector:    FAILED: %v\n", err)
		} else {
			fmt.Printf("  collector:    started (pid %d)\n", cmd.Process.Pid)
		}
	}

	// BODY 2 — the firefox seat. DISCOVERED substrate; absent => dormant, not fatal.
	if s.Firefox == "" || s.Gecko == "" {
		fmt.Println("  browser:      DORMANT — firefox/geckodriver absent; collector-only is a valid boot")
		return
	}
	if portUp("127.0.0.1:4444") {
		fmt.Println("  browser:      seat already up on :4444")
		return
	}
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

// runWatch — `8 watch`: supervision folded into the one binary (#279), replacing
// scripts/watchdog.sh (bash pgrep/lsof/nohup). Every 15s, if the collector's port
// is down, revive it via os/exec — cross-platform, no shell tools. The firefox
// seat's own recycle stays the browser pack's job; this guards the witness itself.
func runWatch() {
	self, _ := os.Executable()
	fmt.Println("8 watch — supervising the collector (Go os/exec; no pgrep/lsof/nohup)")
	for {
		if !portUp("127.0.0.1:7070") {
			cmd := exec.Command(self, "-listen", ":7070")
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
			if err := cmd.Start(); err == nil {
				fmt.Printf("  collector was down -> revived (pid %d)\n", cmd.Process.Pid)
			}
		}
		time.Sleep(15 * time.Second)
	}
}
