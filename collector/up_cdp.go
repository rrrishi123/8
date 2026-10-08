package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// prepareCDPSeat holds a single Chrome page channel on a fresh native boot.
// Existing browsers/brokers are reused; this never closes a Firefox seat.
func prepareCDPSeat(root string, s substrate) error {
	if !portUp("127.0.0.1:9333") {
		packUpEngine(root, "chrome", 9333, s.Chrome)
	}
	// Hold the BROWSER-level CDP endpoint (/json/version), not a single page ws.
	// handleTabs enumerates the chrome seat with Target.getTargets and captures by
	// Target.attachToTarget(flatten) — both are browser-level operations. A page
	// ws can speak to only its own target, so the manifest reconcile saw ZERO tabs
	// and no other tab could be captured (2026-10-04). The browser ws sees every
	// tab across every window; cdpShot's browser-seat path already handles it.
	client := &http.Client{Timeout: time.Second}
	var ws string
	for i := 0; i < 40; i++ {
		resp, err := client.Get("http://127.0.0.1:9333/json/version")
		if err == nil {
			var v struct {
				WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
			}
			err = json.NewDecoder(resp.Body).Decode(&v)
			resp.Body.Close()
			if err == nil && v.WebSocketDebuggerURL != "" {
				ws = v.WebSocketDebuggerURL
			}
		}
		if ws != "" {
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if ws == "" {
		return fmt.Errorf("no CDP browser endpoint on :9333; collector can still boot without a browser")
	}
	if portUp("127.0.0.1:4446") {
		return nil
	}
	channel := ""
	for _, candidate := range []string{
		filepath.Join(root, "http-mcp", ".bin", "channel"),
		os.ExpandEnv("$HOME/.8/bin/channel"),
		look("channel"),
	} {
		if st, err := os.Stat(candidate); err == nil && !st.IsDir() {
			channel = candidate
			break
		}
	}
	if channel == "" {
		return fmt.Errorf("channel binary missing; build http-mcp or install the release bundle")
	}
	cmd := exec.Command(channel, "-ws", ws, "-listen", "127.0.0.1:4446")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go cmd.Wait()
	for i := 0; i < 20; i++ {
		if portUp("127.0.0.1:4446") {
			fmt.Printf("  channel:      chrome broker up :4446 (pid %d)\n", cmd.Process.Pid)
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("chrome broker did not bind :4446")
}
