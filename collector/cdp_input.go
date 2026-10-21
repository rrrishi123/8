package main

import (
	"encoding/json"
	"math"
	"net/http"
)

// ── CHROME/CDP INPUT — "Live" control for chrome seats (the #1 cockpit gap) ───
// actChannel builds BiDi commands (input.performActions, script.callFunction) that
// ONLY Firefox understands. A CHROME seat (b.id != "fox": mac-chromium, the colima
// containers, any /nodes/attach node) speaks CDP, so those BiDi commands 502 and the
// cockpit's "Live" drive does nothing on every chromium tab — the operator arms a
// seat and the clicks go nowhere. actCDP drives the SAME tap/scroll/drag/type gestures
// over CDP Input instead: attach the target (flatten → sessionId), dispatch
// Input.dispatchMouseEvent / mouseWheel / insertText, detach. This is the exact path
// proven by hand driving deepseek-web + claude-web through the :4446 broker.

// cdpCall sends one CDP command (with an optional flat session id) through the broker.
func (c *collector) cdpCall(b *broker, method string, params map[string]any, sid string) ([]byte, error) {
	m := map[string]any{"method": method, "params": params}
	if sid != "" {
		m["sessionId"] = sid
	}
	body, _ := json.Marshal(m)
	return c.command(b, string(body))
}

// cdpAttach attaches to a CDP target (flatten) and returns its session id ("" on fail).
func (c *collector) cdpAttach(b *broker, targetID string) string {
	out, err := c.cdpCall(b, "Target.attachToTarget", map[string]any{"targetId": targetID, "flatten": true}, "")
	if err != nil {
		return ""
	}
	var a struct {
		Result struct {
			SessionID string `json:"sessionId"`
		} `json:"result"`
	}
	json.Unmarshal(out, &a)
	return a.Result.SessionID
}

// cdpViewport reads the tab's CSS viewport so the cockpit's 0..1 ratio coords resolve
// to real pixels (same contract as the BiDi path's chanViewport).
func (c *collector) cdpViewport(b *broker, sid string) (float64, float64) {
	out, err := c.cdpCall(b, "Runtime.evaluate", map[string]any{
		"expression": `JSON.stringify([innerWidth,innerHeight])`, "returnByValue": true}, sid)
	if err != nil {
		return 0, 0
	}
	var r struct {
		Result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &r) != nil {
		return 0, 0
	}
	var wh []float64
	json.Unmarshal([]byte(r.Result.Result.Value), &wh)
	if len(wh) == 2 {
		return wh[0], wh[1]
	}
	return 0, 0
}

// actCDP drives a chrome/CDP seat's tab for the cockpit's Live gestures (ctx = the
// tab's CDP target id, which is what the manifest carries for a chrome seat).
func (c *collector) actCDP(w http.ResponseWriter, r *http.Request, b *broker, ctx, action, text string, x, y, x2, y2 int, xr, yr float64) {
	sid := c.cdpAttach(b, ctx)
	if sid == "" {
		http.Error(w, `{"error":"act(cdp): cannot attach target"}`, http.StatusBadGateway)
		return
	}
	defer c.cdpCall(b, "Target.detachFromTarget", map[string]any{"sessionId": sid}, "")
	if xr > 0 || yr > 0 { // resolution-independent coords → real pixels
		if vw, vh := c.cdpViewport(b, sid); vw > 0 && vh > 0 {
			x = int(math.Round(xr * vw))
			y = int(math.Round(yr * vh))
		}
	}
	c.setFocus(b.id, ctx)                  // 8 auto-zooms to the tab being driven
	c.setControlledBy(ctx, actorOf(r, "")) // #9425(2): and records WHO is driving it (presence)
	mouse := func(typ string, px, py int, extra map[string]any) {
		p := map[string]any{"type": typ, "x": px, "y": py}
		for k, v := range extra {
			p[k] = v
		}
		c.cdpCall(b, "Input.dispatchMouseEvent", p, sid)
	}
	switch action {
	case "tap", "click":
		mouse("mouseMoved", x, y, nil)
		mouse("mousePressed", x, y, map[string]any{"button": "left", "clickCount": 1})
		mouse("mouseReleased", x, y, map[string]any{"button": "left", "clickCount": 1})
	case "scroll": // CDP wheel: deltas in CSS px (x2,y2)
		mouse("mouseWheel", x, y, map[string]any{"deltaX": x2, "deltaY": y2})
	case "swipe", "drag":
		mouse("mouseMoved", x, y, nil)
		mouse("mousePressed", x, y, map[string]any{"button": "left", "clickCount": 1})
		mouse("mouseMoved", x2, y2, map[string]any{"button": "left"})
		mouse("mouseReleased", x2, y2, map[string]any{"button": "left", "clickCount": 1})
	case "sendkeys", "type":
		c.cdpCall(b, "Input.insertText", map[string]any{"text": text}, sid)
	default:
		http.Error(w, `{"error":"act(cdp): unknown action"}`, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true,"via":"cdp","action":"` + action + `"}`))
}
