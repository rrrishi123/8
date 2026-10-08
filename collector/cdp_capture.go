package main

import (
	"encoding/json"
	"fmt"
	"math"
)

// cdpCapture asks Chrome to scale the visible viewport before its JPEG encoder.
// /shot can then return the encoded bytes untouched, just like streamCDP's
// startScreencast(maxWidth) path. No viewport emulation or page resize is needed.
func (c *collector) cdpCapture(b *broker, sessionID string, targetW int) ([]byte, error) {
	command := func(method string, params any) ([]byte, error) {
		cmd := map[string]any{"method": method, "params": params}
		if sessionID != "" {
			cmd["sessionId"] = sessionID
		}
		body, err := json.Marshal(cmd)
		if err != nil {
			return nil, err
		}
		return c.command(b, string(body))
	}
	params := map[string]any{"format": "jpeg", "quality": 50, "captureBeyondViewport": false}
	if targetW > 0 {
		metrics, err := command("Page.getLayoutMetrics", map[string]any{})
		if err != nil {
			return nil, err
		}
		var m struct {
			Result struct {
				Viewport struct {
					PageX, PageY, ClientWidth, ClientHeight, Scale, Zoom float64
				} `json:"cssVisualViewport"`
			} `json:"result"`
		}
		if err := json.Unmarshal(metrics, &m); err != nil {
			return nil, fmt.Errorf("CDP viewport metrics: %w", err)
		}
		v := m.Result.Viewport
		if v.ClientWidth <= 0 || v.ClientHeight <= 0 {
			return nil, fmt.Errorf("CDP seat has no visible viewport for capture")
		}
		// Layout metrics omit emulated deviceScaleFactor. Read DPR from this same
		// target so Retina/emulated screens do not silently send 2x the LOD width.
		dprReply, err := command("Runtime.evaluate", map[string]any{"expression": "window.devicePixelRatio", "returnByValue": true})
		if err != nil {
			return nil, err
		}
		var d struct {
			Result struct {
				Result struct {
					Value float64 `json:"value"`
				} `json:"result"`
			} `json:"result"`
		}
		if err := json.Unmarshal(dprReply, &d); err != nil || d.Result.Result.Value <= 0 {
			return nil, fmt.Errorf("CDP seat did not report a valid devicePixelRatio")
		}
		if v.Zoom <= 0 {
			v.Zoom = 1
		}
		if v.Scale <= 0 {
			v.Scale = 1
		}
		// clip coordinates are DIP; cssVisualViewport is CSS. Zoom converts the
		// rectangle to DIP, while DPR converts its width to physical pixels.
		params["clip"] = map[string]float64{
			"x": v.PageX * v.Zoom, "y": v.PageY * v.Zoom,
			"width": v.ClientWidth * v.Zoom, "height": v.ClientHeight * v.Zoom,
			"scale": math.Min(v.Scale, float64(targetW)/(v.ClientWidth*d.Result.Result.Value)),
		}
	}
	return command("Page.captureScreenshot", params)
}
