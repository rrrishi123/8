// Chromium intentionally binds its DevTools listener to loopback. Expose that
// listener to the runtime on the container network, including WebSocket upgrades.
package main

import (
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"time"
)

func main() {
	upstream, _ := url.Parse("http://127.0.0.1:9223")
	proxy := httputil.NewSingleHostReverseProxy(upstream)
	director := proxy.Director
	proxy.Director = func(r *http.Request) {
		director(r)
		r.Host = upstream.Host
	}
	server := &http.Server{Addr: ":9222", Handler: proxy, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}
