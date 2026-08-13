package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	_ "modernc.org/sqlite"
)

// handleSQL — the DB ATOM (#280): raw SQL over eight.db through the wire, engine
// BUNDLED (modernc.org/sqlite, pure-Go — no host sqlite3 binary, cross-compiles).
// Local-only, so there is no injection threat; the WITNESS is the traceability —
// every query is published to the feed. POST /sql with a raw SQL body OR
// {"query":"..."}. SELECT/WITH/PRAGMA return rows; anything else returns affected.
// This is the store's `become`: one general primitive instead of a filter param
// per question — an agent can create its own tables, poll, insert, select.
func (c *collector) handleSQL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}
	raw, _ := io.ReadAll(r.Body)
	q := strings.TrimSpace(string(raw))
	if strings.HasPrefix(q, "{") { // also accept {"query":"..."}
		var b struct {
			Query string `json:"query"`
		}
		if json.Unmarshal(raw, &b) == nil && strings.TrimSpace(b.Query) != "" {
			q = strings.TrimSpace(b.Query)
		}
	}
	if q == "" {
		http.Error(w, `{"error":"empty query"}`, http.StatusBadRequest)
		return
	}
	db, err := sql.Open("sqlite", eightDB())
	if err != nil {
		http.Error(w, `{"error":"open: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	defer db.Close()
	c.publish(fmt.Sprintf(`{"session":"db","origin":"COLLECTOR","frame":{"method":"sql.query","params":{"q":%q}}}`, q))
	w.Header().Set("Content-Type", "application/json")
	up := strings.ToUpper(q)
	if strings.HasPrefix(up, "SELECT") || strings.HasPrefix(up, "WITH") || strings.HasPrefix(up, "PRAGMA") {
		rows, err := db.Query(q)
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
			return
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		out := []map[string]any{}
		for rows.Next() {
			vals := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				continue
			}
			m := map[string]any{}
			for i, col := range cols {
				v := vals[i]
				if b, ok := v.([]byte); ok {
					v = string(b) // TEXT/BLOB come back as bytes; hand back a string
				}
				m[col] = v
			}
			out = append(out, m)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": out, "n": len(out)})
		return
	}
	res, err := db.Exec(q)
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	n, _ := res.RowsAffected()
	_ = json.NewEncoder(w).Encode(map[string]any{"affected": n})
}
