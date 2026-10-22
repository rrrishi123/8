package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// Native peer join is opt-in and travels with the collector process, including
// children started by up/watch. It needs no shell heartbeat or local HTTP probe.
type peerJoinConfig struct {
	endpoint string
	host     string
	token    string
	self     string // this peer's OWN collector base URL (loopback) — where relay commands run
}

// selfBaseFromListen turns a -listen value (":7071", "0.0.0.0:7071") into the loopback
// base URL a pulled relay command executes against on this peer.
func selfBaseFromListen(listen string) string {
	listen = strings.TrimSpace(listen)
	if i := strings.LastIndex(listen, ":"); i >= 0 {
		return "http://127.0.0.1:" + listen[i+1:]
	}
	return "http://127.0.0.1:7070"
}

func peerJoinFromEnv() (peerJoinConfig, error) {
	hub := strings.TrimSpace(os.Getenv("PEER_HUB"))
	if hub == "" {
		hub = strings.TrimSpace(os.Getenv("HUB"))
	}
	if hub == "" {
		return peerJoinConfig{}, nil
	}
	u, err := url.Parse(hub)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		// Do not print the supplied URL: misconfiguration can contain credentials.
		return peerJoinConfig{}, fmt.Errorf("PEER_HUB/HUB must be an absolute http(s) base URL without credentials, query, or fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/peers"
	u.RawPath = ""
	host := strings.TrimSpace(os.Getenv("PEER_HOST"))
	if host == "" {
		host, err = os.Hostname()
		if err != nil || host == "" {
			return peerJoinConfig{}, fmt.Errorf("peer join needs PEER_HOST or a system hostname")
		}
	}
	return peerJoinConfig{endpoint: u.String(), host: host, token: os.Getenv("PEER_TOKEN")}, nil
}

func (c *collector) peerHeartbeat(host string) (peer, error) {
	hr, err := json.Marshal(readHostRes())
	if err != nil {
		return peer{}, err
	}
	// Copy values under the manifest lock: records continue changing while the
	// request is encoded and in flight. Never hold the lock for network I/O.
	c.tmu.Lock()
	tabs := make([]tabRec, 0, len(c.manifest))
	for _, rec := range c.manifest {
		tabs = append(tabs, *rec)
	}
	c.tmu.Unlock()
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].UID < tabs[j].UID })
	live := 0
	for _, rec := range tabs {
		if rec.Status == "live" {
			live++
		}
	}
	manifest, err := json.Marshal(map[string]any{"total": len(tabs), "live": live, "tabs": tabs})
	if err != nil {
		return peer{}, err
	}
	bud := map[string]any{}
	if b := c.budgetNow(); b != nil {
		for _, name := range []string{"5h", "7d"} {
			if w, ok := b.Windows[name]; ok {
				bud["claude_"+name] = w.Utilization
				// carry the window's reset (RFC3339) so a collector's native join
				// federates the SAME reset shape peer-beat.sh does — the hub shows
				// every host's ↻ countdown, inner hosts included, from one field.
				bud["claude_"+name+"_reset"] = w.ResetAt
			}
		}
		bud["claude_gated"] = b.Gated
		bud["claude_observed_at"] = b.ObservedAt
	}
	if cx, _ := codexReading(); cx != nil {
		windows, _ := cx["windows"].(map[string]any)
		for _, name := range []string{"5h", "7d"} {
			if w, ok := windows[name].(map[string]any); ok {
				bud["codex_"+name] = w["utilization"]
				bud["codex_"+name+"_reset"] = w["reset_at"] // codex: epoch seconds
			}
		}
		bud["codex_gated"] = cx["gated"]
		bud["codex_observed_at"] = cx["observed_at"]
	}
	extra, err := json.Marshal(map[string]any{"budget": bud, "build": buildSHA})
	if err != nil {
		return peer{}, err
	}
	return peer{Host: host, Actor: "peer-join/" + host, HostRes: hr, Manifest: manifest, Extra: extra}, nil
}

func sendPeerHeartbeat(ctx context.Context, client *http.Client, cfg peerJoinConfig, p peer) ([]relayCmd, error) {
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-8-Actor", p.Actor)
	if cfg.token != "" {
		req.Header.Set("X-8-Token", cfg.token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("peer hub returned HTTP %d", resp.StatusCode)
	}
	// #1267: the hub's response hands us the commands queued for us to PULL and run.
	var ack struct {
		Pending []relayCmd `json:"pending"`
	}
	_ = json.Unmarshal(rb, &ack)
	return ack.Pending, nil
}

func newPeerJoinClient() *http.Client {
	return &http.Client{
		Timeout: 6 * time.Second,
		// Custom token headers must never follow a redirect to another host.
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func (c *collector) peerJoinLoop(ctx context.Context, cfg peerJoinConfig, interval time.Duration) {
	client := newPeerJoinClient()
	defer client.CloseIdleConnections()
	execClient := &http.Client{Timeout: 8 * time.Second} // runs pulled commands on our OWN loopback
	var carry []relayResult                              // results of last beat's pull, sent on the next beat
	for ctx.Err() == nil {
		p, err := c.peerHeartbeat(cfg.host)
		var pending []relayCmd
		if err == nil {
			p.Results = carry // carry back what we ran since the last beat
			pending, err = sendPeerHeartbeat(ctx, client, cfg, p)
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("peer join: %v; retrying in %s", err, interval)
		} else {
			carry = carry[:0] // the hub accepted our results; don't resend them
			// #1267: run every command the hub queued for us, against OUR OWN collector
			// (loopback) — the hub never reaches in; we pull and self-drive, then carry the
			// results back on the next beat.
			for _, cmd := range pending {
				if cfg.self == "" {
					break
				}
				carry = append(carry, relayExecute(cfg.self, execClient, cmd))
			}
			if len(pending) > 0 {
				log.Printf("peer join: ran %d relayed command(s) locally", len(pending))
			}
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}
