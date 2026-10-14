package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// ── #3863 BROKER SELF-HEAL ───────────────────────────────────────────────────
// The chrome CHANNEL broker binds :4446 and relays commands to the browser's CDP
// ws on :9333. That upstream ws can die (broken pipe) — Chrome restarts, churns
// versions, drops the socket — while the broker keeps HOLDING :4446. portUp() is
// blind to this (the port is still bound), so prepareCDPSeat's `if portUp(:4446)
// return` never re-wires, and every CHANNEL act 502s with "broken pipe" until a
// human re-launches the channel by hand (done 3-4x on 2026-10-13/14 — the itch
// this scratches). The witness should heal its own arm, not wait to be noticed.
//
// The fix is a FUNCTIONAL probe + re-wire: round-trip a cheap browser-level command;
// on sustained failure (not a single blip), re-fetch the live ws, replace the stale
// channel, rebind :4446, and WITNESS it. A healthy broker is never disturbed.

const chromeCDPVersionURL = "http://127.0.0.1:9333/json/version"

// chromeBrokerRef — the registered chrome broker (or nil if none wired).
func (c *collector) chromeBrokerRef() *broker {
	c.brokersMu.RLock()
	defer c.brokersMu.RUnlock()
	for i := range c.brokers {
		if c.brokers[i].id == "chrome" {
			b := c.brokers[i]
			return &b
		}
	}
	return nil
}

// chromeBrokerLive — FUNCTIONAL health, NOT portUp: a broken-pipe broker answers 502
// with an {"error":...} body while still holding :4446. Target.getTargets is a cheap
// browser-level round-trip; a clean 200 without an error body means the upstream lives.
func (c *collector) chromeBrokerLive(b *broker) bool {
	body, status, err := c.commandS(b, `{"method":"Target.getTargets"}`)
	if err != nil || status != http.StatusOK {
		return false
	}
	return !bytes.Contains(body, []byte(`"error"`))
}

// freshChromeWS — the live BROWSER-level CDP ws (the whole-browser endpoint, not a
// single page), which is what the channel must relay to so every tab stays reachable.
func freshChromeWS() string {
	cl := &http.Client{Timeout: 2 * time.Second}
	resp, err := cl.Get(chromeCDPVersionURL)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	var v struct {
		WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
	}
	if json.NewDecoder(resp.Body).Decode(&v) != nil {
		return ""
	}
	return v.WebSocketDebuggerURL
}

// rewireChromeBroker — replace the stale :4446 channel with a fresh one bound to the
// live ws: kill whatever holds :4446 (the broken-pipe broker), then relaunch the
// channel binary. Same launch shape as prepareCDPSeat, minus the portUp short-circuit.
func rewireChromeBroker() (int, error) {
	ws := freshChromeWS()
	if ws == "" {
		return 0, fmt.Errorf("no live CDP ws on :9333 (browser down?) — nothing to re-wire to")
	}
	bin := channelBin()
	if bin == "" {
		return 0, fmt.Errorf("channel binary not found (http-mcp build.sh → .bin/channel)")
	}
	// drop the stale broker holding :4446 (port bound, upstream dead)
	_ = exec.Command("sh", "-c", "lsof -ti tcp:4446 | xargs kill 2>/dev/null").Run()
	for i := 0; i < 20 && portUp("127.0.0.1:4446"); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	cmd := exec.Command(bin, "-ws", ws, "-listen", "127.0.0.1:4446")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return 0, err
	}
	go cmd.Wait()
	for i := 0; i < 30; i++ {
		if portUp("127.0.0.1:4446") {
			return cmd.Process.Pid, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return 0, fmt.Errorf("re-wired channel never bound :4446")
}

// brokerHealLoop — #3863: the witness heals its own CHANNEL arm. Every interval it
// functionally probes the chrome broker; on SUSTAINED failure (two consecutive
// probes, so a transient blip never triggers a kill) it re-wires and witnesses it.
// A heal that can't reach a live browser is witnessed as a heal-failure, not retried
// into a kill loop. A healthy broker is never touched.
func (c *collector) brokerHealLoop() {
	const every = 20 * time.Second
	fails := 0
	for {
		time.Sleep(every)
		b := c.chromeBrokerRef()
		if b == nil {
			continue
		}
		if c.chromeBrokerLive(b) {
			fails = 0
			continue
		}
		fails++
		if fails < 2 { // tolerate one blip; re-wire only on sustained failure
			continue
		}
		c.publish(`{"session":"chrome","origin":"COLLECTOR","frame":{"method":"broker.unhealthy","params":{"broker":"chrome","addr":"127.0.0.1:4446","reason":"upstream CDP ws dead (broken pipe)"}}}`)
		pid, err := rewireChromeBroker()
		if err != nil {
			c.publish(fmt.Sprintf(`{"session":"chrome","origin":"COLLECTOR","frame":{"method":"broker.heal-failed","params":{"broker":"chrome","err":%q}}}`, err.Error()))
			fails = 0 // reset; the browser is down, not the broker — retry next interval, no kill loop
			continue
		}
		c.publish(fmt.Sprintf(`{"session":"chrome","origin":"COLLECTOR","frame":{"method":"broker.healed","params":{"broker":"chrome","addr":"127.0.0.1:4446","pid":%d}}}`, pid))
		fails = 0
	}
}
