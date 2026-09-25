package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// ── BiDi SCREENCAST RECORDING (#655) — probe-gated, activates on Firefox >=154 ─
// The task's plan step 2 ("subscribe browsingContext.screencastFrame, feed the
// frames chan") is FALSIFIED at the spec level: WebDriver BiDi's screencast is
// RECORD-TO-FILE — startScreencast{context,mimeType?,video?,audio?} returns
// {screencast, path}; stopScreencast{screencast} returns {path}. There is no
// frame-push event in BiDi (that event is CDP-only). So this file lands the
// primitive the spec ACTUALLY offers: native in-browser video recording of a
// context — zero drawSnapshot, zero per-frame base64 through the parent — as
// POST /screencast?session=&context=&op=start|stop. On the running Firefox (152)
// the probe answers "unknown command" and we return 501 with the honest
// capability report; the day the seat runs >=154 this endpoint just works.
// The drawSnapshot live-stream stays: it is the ONLY live-frames path in BiDi
// Firefox, and its aperture/recycle mitigations remain necessary.

func (c *collector) handleScreencast(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only — /screencast?session=fox&context=<uuid>&op=start|stop&screencast=<id>"}`, http.StatusMethodNotAllowed)
		return
	}
	b := c.find(r.URL.Query().Get("session"))
	if b == nil {
		http.Error(w, `{"error":"unknown or missing ?session="}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	op := r.URL.Query().Get("op")
	actor := r.Header.Get("X-8-Actor")
	switch op {
	case "start":
		ctx := r.URL.Query().Get("context")
		if ctx == "" {
			http.Error(w, `{"error":"need ?context=<browsing context uuid>"}`, http.StatusBadRequest)
			return
		}
		mime := r.URL.Query().Get("mime")
		if mime == "" {
			mime = "video/webm"
		}
		frame := fmt.Sprintf(`{"method":"browsingContext.startScreencast","params":{"context":%q,"mimeType":%q}}`, ctx, mime)
		out, err := c.command(b, frame)
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		if strings.Contains(string(out), "unknown command") || strings.Contains(string(out), "unknown method") {
			w.WriteHeader(http.StatusNotImplemented)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":      "browsingContext.startScreencast not supported by this Firefox",
				"capability": "lands in Firefox >=154 (spec: record-to-file; NO frame-push event exists in BiDi — CDP-only)",
				"fallback":   "the drawSnapshot live-stream remains the live path; this endpoint activates on upgrade",
			})
			return
		}
		c.publish(fmt.Sprintf(`{"session":%q,"origin":"COLLECTOR","frame":{"method":"record.started","params":{"context":%q,"mime":%q,"actor":%q}}}`, b.id, ctx, mime, actor))
		w.Write(out)
	case "stop":
		sc := r.URL.Query().Get("screencast")
		if sc == "" {
			http.Error(w, `{"error":"need ?screencast=<id from start>"}`, http.StatusBadRequest)
			return
		}
		out, err := c.command(b, fmt.Sprintf(`{"method":"browsingContext.stopScreencast","params":{"screencast":%q}}`, sc))
		if err != nil {
			http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadGateway)
			return
		}
		c.publish(fmt.Sprintf(`{"session":%q,"origin":"COLLECTOR","frame":{"method":"record.stopped","params":{"screencast":%q,"actor":%q}}}`, b.id, sc, actor))
		w.Write(out)
	default:
		http.Error(w, `{"error":"need ?op=start|stop"}`, http.StatusBadRequest)
	}
}
