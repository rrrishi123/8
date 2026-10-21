package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"strings"
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
	// PER-AGENT POINTER: draw this actor's own labeled cursor where it just acted, so
	// the human SEES who is driving (HeyClicky-style, from the witness's who+where). The
	// overlay lives in the tab, so it shows on the real tab AND in 8's card; it fades
	// and self-removes. A blank actor draws nothing — an undeclared drive has no one.
	if js := agentPointerJS(actorOf(r, ""), x, y); js != "" {
		c.cdpCall(b, "Runtime.evaluate", map[string]any{"expression": js}, sid)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"ok":true,"via":"cdp","action":"` + action + `"}`))
}

// agentPointerJS builds a self-removing cursor overlay at (x,y), colored and labeled
// by the actor so each agent gets a stable, distinct pointer. Only single-quotes in
// the DOM-built JS; the label is JSON-encoded so any actor string is safe.
func agentPointerJS(actor string, x, y int) string {
	if strings.TrimSpace(actor) == "" {
		return ""
	}
	colors := []string{"#8e24aa", "#1e88e5", "#e53935", "#43a047", "#fb8c00", "#00897b", "#5e35b1"}
	h := 0
	for _, c := range actor {
		h = h*31 + int(c)
	}
	if h < 0 {
		h = -h
	}
	col := colors[h%len(colors)]
	safe := ""
	for _, c := range actor {
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') {
			safe += string(c)
		}
	}
	lbl, _ := json.Marshal("◆ " + actor)
	return fmt.Sprintf(`(function(){var id='__ptr_%s';var o=document.getElementById(id);if(o)o.remove();var w=document.createElement('div');w.id=id;w.style.cssText='position:fixed;left:%dpx;top:%dpx;z-index:2147483647;pointer-events:none;transform:translate(-50%%,-50%%);font-family:system-ui;text-align:center;transition:opacity .6s';var d=document.createElement('div');d.style.cssText='width:16px;height:16px;border-radius:50%%;background:%s;box-shadow:0 0 0 6px %s55;margin:0 auto';var l=document.createElement('div');l.textContent=%s;l.style.cssText='margin-top:4px;background:%s;color:#fff;font-weight:700;font-size:12px;padding:2px 8px;border-radius:10px;white-space:nowrap';w.appendChild(d);w.appendChild(l);document.body.appendChild(w);setTimeout(function(){w.style.opacity=0},1400);setTimeout(function(){w.remove()},2200)})()`,
		safe, x, y, col, col, string(lbl), col)
}
