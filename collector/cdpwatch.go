package main

import (
	"encoding/json"
	"fmt"
	"strconv"
)

// ── T5 reply-completion watcher, substrate-agnostic core ─────────────────────
// The #12 change-detector reads a DOM signature for each WATCHED tab; T5 folds
// that read into the settle state machine and emits a witnessed reply-complete
// when a tab that changed then held still. The READ differs by substrate — fox
// speaks BiDi script.evaluate, chrome speaks CDP Runtime.evaluate — but the
// probe expression, the settle logic, and the emission are identical, so they
// live here and both paths call watchStep with the same raw value.

// the DOM-signature probe: title · innerText length · last 160 chars. A reply
// growing shifts n/tail; a settled reply stops shifting. Identical across fox
// (BiDi) and chrome (CDP) so their signatures are comparable.
const watchProbeExpr = `(()=>{const x=(document.body&&document.body.innerText)||'';return JSON.stringify({t:document.title,n:x.length,tail:x.slice(-160)})})()`

// watchSubOf — the substrate a watched ctx is read over (default fox).
func (c *collector) watchSubOf(ctx string) string {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if s := c.watchSub[ctx]; s != "" {
		return s
	}
	return "fox"
}

// watchStep folds one watched tab's fresh DOM-signature read into the settle
// machine and emits: tab.changed on a shift (a reply arriving), a witnessed
// reply-complete on settle (the reply finished). rawValue is the probe's JSON
// string ("" = read failed, skip). Shared by the fox and chrome read paths.
func (c *collector) watchStep(ctx, prev, rawValue, substrate string) {
	if rawValue == "" {
		return
	}
	sig := strconv.FormatUint(fnv1a([]byte(rawValue)), 16)
	var d struct {
		T    string `json:"t"`
		N    int    `json:"n"`
		Tail string `json:"tail"`
	}
	json.Unmarshal([]byte(rawValue), &d)
	// decide under the lock (settleStep is pure); emit after unlock (publish +
	// recordReplyEvent do I/O and take their own locks — never nest under wmu).
	c.wmu.Lock()
	if c.watchDirty == nil {
		c.watchDirty = map[string]int{}
	}
	changed, settled := settleStep(c.watchDirty, ctx, prev, sig, watchSettleChecks)
	if _, still := c.watched[ctx]; still {
		c.watched[ctx] = sig
	}
	c.wmu.Unlock()
	if changed {
		c.publish(fmt.Sprintf(`{"session":%q,"origin":"COLLECTOR","frame":{"method":"tab.changed","params":{"context":%q,"title":%q,"len":%d}}}`, substrate, ctx, d.T, d.N))
	}
	if settled {
		c.recordReplyEvent(replyEvent{Kind: "reply-complete", Tab: ctx, Detail: d.T, By: "cdpwatch:" + substrate})
	}
}

// chromeSig reads a watched CHROME tab's DOM signature over CDP — the chrome
// analogue of the fox BiDi path: attach to the target (flatten), Runtime.evaluate
// the shared probe with the session id, detach. Returns the raw JSON value
// ("" on any failure — the caller skips this tick).
func (c *collector) chromeSig(b *broker, targetID string) string {
	ar, err := c.command(b, `{"method":"Target.attachToTarget","params":{"targetId":"`+targetID+`","flatten":true}}`)
	if err != nil {
		return ""
	}
	var a struct {
		Result struct {
			SessionID string `json:"sessionId"`
		} `json:"result"`
	}
	if json.Unmarshal(ar, &a) != nil || a.Result.SessionID == "" {
		return ""
	}
	sid := a.Result.SessionID
	defer c.command(b, `{"method":"Target.detachFromTarget","params":{"sessionId":"`+sid+`"}}`) // best-effort
	q := fmt.Sprintf(`{"method":"Runtime.evaluate","params":{"expression":%s,"returnByValue":true},"sessionId":%q}`, strconv.Quote(watchProbeExpr), sid)
	out, err := c.command(b, q)
	if err != nil {
		return ""
	}
	var r struct {
		Result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	if json.Unmarshal(out, &r) != nil {
		return ""
	}
	return r.Result.Result.Value
}
