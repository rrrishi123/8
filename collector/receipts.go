package main

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

// ── WITNESS RECEIPTS ON EVERYTHING (#616, 4-system task #5) ──────────────────
// philo trap (a): efference without reafference — a wire that answers without a
// receipt teaches its callers to treat it as a dumb pipe (even the author did).
// Every response now carries, injected just before the first byte:
//   X-8-Witness:   seen · act #<n> · call · <d>ms · GET /work · by <actor>
//   X-8-Ledger-Id: <n>   (monotonic act counter since boot)
//   X-8-DMs:       <d>   (milliseconds to first byte)
// Handlers that already wrote a richer receipt (/run's replayable-frame line)
// are never overridden — they only gain the generic ids where absent.

var actSeq int64

type witnessWriter struct {
	http.ResponseWriter
	req     *http.Request
	start   time.Time
	stamped bool
}

func (w *witnessWriter) stamp() {
	if w.stamped {
		return
	}
	w.stamped = true
	h := w.Header()
	d := time.Since(w.start).Milliseconds()
	if h.Get("X-8-Witness") == "" {
		n := atomic.AddInt64(&actSeq, 1)
		actor := w.req.Header.Get("X-8-Actor")
		if actor == "" {
			actor = "undeclared" // the blank named, never guessed
		}
		h.Set("X-8-Witness", fmt.Sprintf("seen · act #%d · call · %dms · %s %s · by %s",
			n, d, w.req.Method, w.req.URL.Path, actor))
		h.Set("X-8-Ledger-Id", strconv.FormatInt(n, 10))
	}
	if h.Get("X-8-DMs") == "" {
		h.Set("X-8-DMs", strconv.FormatInt(d, 10))
	}
}

func (w *witnessWriter) WriteHeader(code int) { w.stamp(); w.ResponseWriter.WriteHeader(code) }
func (w *witnessWriter) Write(b []byte) (int, error) {
	w.stamp()
	return w.ResponseWriter.Write(b)
}

// Flush/Hijack pass through so SSE (/feed) and any upgraded connections keep
// working under the wrapper.
func (w *witnessWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
func (w *witnessWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if hj, ok := w.ResponseWriter.(http.Hijacker); ok {
		return hj.Hijack()
	}
	return nil, nil, fmt.Errorf("hijack unsupported")
}

func witnessEvery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&witnessWriter{ResponseWriter: rw, req: r, start: time.Now()}, r)
	})
}
