//go:build nosql

package main

import (
	"encoding/json"
	"net/http"
)

// handleSQL — the `nosql` build: the /sql store primitive is compiled OUT, so the
// collector carries no modernc.org/sqlite dependency and cross-compiles anywhere
// with no engine at all (web-container finding A1). The route stays wired but
// answers 501; build the full store with the default tags, use `-tags nosql` for
// a store-less collector.
func (c *collector) handleSQL(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotImplemented)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": "sql disabled: this collector was built with -tags nosql (no bundled sqlite engine)"})
}
