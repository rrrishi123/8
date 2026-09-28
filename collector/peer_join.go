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
	return peer{Host: host, Actor: "peer-join/" + host, HostRes: hr, Manifest: manifest}, nil
}

func sendPeerHeartbeat(ctx context.Context, client *http.Client, cfg peerJoinConfig, p peer) error {
	body, err := json.Marshal(p)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-8-Actor", p.Actor)
	if cfg.token != "" {
		req.Header.Set("X-8-Token", cfg.token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("peer hub returned HTTP %d", resp.StatusCode)
	}
	return nil
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
	for ctx.Err() == nil {
		p, err := c.peerHeartbeat(cfg.host)
		if err == nil {
			err = sendPeerHeartbeat(ctx, client, cfg, p)
		}
		if err != nil && ctx.Err() == nil {
			log.Printf("peer join: %v; retrying in %s", err, interval)
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
