package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// HUMAN PRESENCE — "you are here", the third layer of attention. Agents light their
// tabs (controlled_by); 8 points the human (POST /focus → hero); and THIS shows where
// the HUMAN actually is — their foreground Chrome tab — so the cockpit can mark it
// distinctly from where agents are. Lean by design: the human usually stays put, so we
// re-check the LAST-known foreground first and only sweep the rest when it has gone
// hidden (a real tab switch). Amortized ~1 probe per tick, not 27.

// tabVisible reports whether a chrome tab's document is the visible (active) one —
// visibilityState is 'visible' for the active tab in its window (even if Chrome is
// itself backgrounded), 'hidden' for every other tab.
func (c *collector) tabVisible(b *broker, targetID string) bool {
	ar, err := tabcostCmd(b.base, fmt.Sprintf(`{"method":"Target.attachToTarget","params":{"targetId":%q,"flatten":true}}`, targetID))
	if err != nil {
		return false
	}
	var at struct {
		Result struct {
			SessionID string `json:"sessionId"`
		} `json:"result"`
	}
	if json.Unmarshal(ar, &at) != nil || at.Result.SessionID == "" {
		return false
	}
	sid := at.Result.SessionID
	defer func() {
		_, _ = tabcostCmd(b.base, fmt.Sprintf(`{"method":"Target.detachFromTarget","params":{"sessionId":%q}}`, sid))
	}()
	er, _ := tabcostCmd(b.base, fmt.Sprintf(`{"sessionId":%q,"method":"Runtime.evaluate","params":{"expression":"document.visibilityState","returnByValue":true}}`, sid))
	var ev struct {
		Result struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		} `json:"result"`
	}
	_ = json.Unmarshal(er, &ev)
	return ev.Result.Result.Value == "visible"
}

// findVisibleTab sweeps chrome page targets for the single visible one (early-exit on
// the first hit). Only runs when the cached foreground has gone hidden.
func (c *collector) findVisibleTab(b *broker) string {
	raw, err := tabcostCmd(b.base, `{"method":"Target.getTargets","params":{}}`)
	if err != nil {
		return ""
	}
	var tg struct {
		Result struct {
			TargetInfos []struct {
				TargetID string `json:"targetId"`
				Type     string `json:"type"`
			} `json:"targetInfos"`
		} `json:"result"`
	}
	if json.Unmarshal(raw, &tg) != nil {
		return ""
	}
	for _, t := range tg.Result.TargetInfos {
		if t.Type != "page" {
			continue
		}
		if c.tabVisible(b, t.TargetID) {
			return t.TargetID
		}
	}
	return ""
}

// watchHumanForeground maintains c.humanForeground. Cache-first: if the known tab is
// still visible it's one probe; a switch triggers one sweep. Publishes human.here when
// it changes so the cockpit can move the "you are here" marker.
func (c *collector) watchHumanForeground() {
	for {
		b := c.find("chrome")
		next := ""
		if b != nil {
			c.hfMu.Lock()
			cur := c.humanForeground
			c.hfMu.Unlock()
			if cur != "" && c.tabVisible(b, cur) {
				next = cur // cheap path: the human stayed — ONE probe
			} else {
				next = c.findVisibleTab(b) // the costly sweep — only on a switch / when none known
			}
			if next != cur {
				c.hfMu.Lock()
				c.humanForeground = next
				c.hfMu.Unlock()
				if next != "" {
					c.publish(fmt.Sprintf(`{"session":"chrome","origin":"COLLECTOR","frame":{"method":"human.here","params":{"context":%q}}}`, next))
				}
			}
		}
		// ADAPTIVE: fast + cheap while we're tracking a visible tab; back off hard when
		// nothing is visible (the human is outside Chrome) so we don't sweep 27 tabs
		// every few seconds for no one.
		if next != "" {
			time.Sleep(3 * time.Second)
		} else {
			time.Sleep(20 * time.Second)
		}
	}
}
