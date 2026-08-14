package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	_ "modernc.org/sqlite"
)

// handleSQL — the DB primitive (#280): raw SQL over eight.db through the wire,
// engine BUNDLED (modernc.org/sqlite, pure-Go — no host sqlite3 binary,
// cross-compiles). By SHAPE this is a CALL dialect over the store substrate,
// NOT a third atom — see README "Reduction rules" (#315): payload language
// (SQL) is a dialect property and never mints an atom.
// Local-only is ENFORCED, not assumed (#318): the collector listens on all
// interfaces (other endpoints are legitimately reached cross-host), so this
// endpoint checks RemoteAddr and answers loopback callers only — the store
// belongs to the host it lives on. The WITNESS is the traceability — every query
// publishes an intent frame (sql.query: WHO via X-8-Actor + the query) and an
// outcome frame (sql.result: rows/affected/error; refusals as sql.deny), so a
// mutation is witnessed as effect, not just intent (#314).
// POST /sql with a raw SQL body OR {"query":"..."}.
// SELECT/WITH/PRAGMA return rows; anything else returns affected.
// This is the store's `become`: one general primitive instead of a filter param
// per question — an agent can create its own tables, poll, insert, select.
func (c *collector) handleSQL(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"POST only"}`, http.StatusMethodNotAllowed)
		return
	}
	actor := r.Header.Get("X-8-Actor") // #314: the WHO rides the frame, not just the socket
	if actor == "" {
		actor = "undeclared"
	}
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err != nil || !net.ParseIP(host).IsLoopback() {
		c.publish(fmt.Sprintf(`{"session":"db","origin":"COLLECTOR","frame":{"method":"sql.deny","params":{"actor":%q,"remote":%q,"reason":"loopback-only"}}}`, actor, r.RemoteAddr))
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "loopback only: the store answers the host it lives on (#318)"})
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
	if reason := denySQL(q); reason != "" { // #400: denied OUTRIGHT — no flag unlocks these
		c.publish(fmt.Sprintf(`{"session":"db","origin":"COLLECTOR","frame":{"method":"sql.deny","params":{"actor":%q,"reason":%q,"q":%q}}}`, actor, reason, q))
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "denied outright (#400): " + reason})
		return
	}
	db, err := sql.Open("sqlite", eightDB())
	if err != nil {
		http.Error(w, `{"error":"open: `+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	defer db.Close()
	// #314: intent frame carries WHO + query; a matching sql.result/sql.deny frame
	// witnesses the OUTCOME — a mutation is witnessed as effect, not just intent.
	c.publish(fmt.Sprintf(`{"session":"db","origin":"COLLECTOR","frame":{"method":"sql.query","params":{"actor":%q,"q":%q}}}`, actor, q))
	w.Header().Set("Content-Type", "application/json")
	up := strings.ToUpper(q)
	if strings.HasPrefix(up, "SELECT") || strings.HasPrefix(up, "WITH") || strings.HasPrefix(up, "PRAGMA") {
		rows, err := db.Query(q)
		if err != nil {
			c.publish(fmt.Sprintf(`{"session":"db","origin":"COLLECTOR","frame":{"method":"sql.result","params":{"actor":%q,"error":%q}}}`, actor, err.Error()))
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
		c.publish(fmt.Sprintf(`{"session":"db","origin":"COLLECTOR","frame":{"method":"sql.result","params":{"actor":%q,"rows":%d}}}`, actor, len(out)))
		_ = json.NewEncoder(w).Encode(map[string]any{"rows": out, "n": len(out)})
		return
	}
	// WRITE-GATE (#309, pmf found /sql ran a DELETE) — the ledger is the witness;
	// it must not be erasable by accident. Writes (INSERT/UPDATE/DELETE/DROP/CREATE…)
	// require an explicit ?write=1, so a bare SELECT-shaped mistake can't mutate.
	if r.URL.Query().Get("write") != "1" {
		c.publish(fmt.Sprintf(`{"session":"db","origin":"COLLECTOR","frame":{"method":"sql.deny","params":{"actor":%q,"reason":"write-gated"}}}`, actor))
		w.WriteHeader(http.StatusForbidden)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": "write-gated: non-read statement needs ?write=1 (the ledger is the witness — no accidental mutation)"})
		return
	}
	res, err := db.Exec(q)
	if err != nil {
		c.publish(fmt.Sprintf(`{"session":"db","origin":"COLLECTOR","frame":{"method":"sql.result","params":{"actor":%q,"error":%q}}}`, actor, err.Error()))
		_ = json.NewEncoder(w).Encode(map[string]any{"error": err.Error()})
		return
	}
	n, _ := res.RowsAffected()
	c.publish(fmt.Sprintf(`{"session":"db","origin":"COLLECTOR","frame":{"method":"sql.result","params":{"actor":%q,"affected":%d}}}`, actor, n))
	_ = json.NewEncoder(w).Encode(map[string]any{"affected": n})
}

// stripSQL removes string literals ('..' with ” escapes, ".." identifiers) and
// comments (-- line, /* block */) so the deny-scan below sees only bare SQL.
// Without this the LEDGER'S OWN CONTENT false-positives — an INSERT of a note
// containing the word DROP is legitimate; a commented-out DROP does nothing.
func stripSQL(q string) string {
	var out []rune
	r := []rune(q)
	for i := 0; i < len(r); i++ {
		switch {
		case r[i] == '\'': // string literal; '' is an escaped quote
			for i++; i < len(r); i++ {
				if r[i] == '\'' {
					if i+1 < len(r) && r[i+1] == '\'' {
						i++
						continue
					}
					break
				}
			}
		case r[i] == '"': // quoted identifier
			for i++; i < len(r) && r[i] != '"'; i++ {
			}
		case r[i] == '-' && i+1 < len(r) && r[i+1] == '-': // line comment
			for ; i < len(r) && r[i] != '\n'; i++ {
			}
		case r[i] == '/' && i+1 < len(r) && r[i+1] == '*': // block comment
			for i += 2; i+1 < len(r) && !(r[i] == '*' && r[i+1] == '/'); i++ {
			}
			i++
		default:
			out = append(out, r[i])
		}
	}
	return string(out)
}

// denySQL — the outright deny-list (#400), defense-in-depth on top of the #318
// loopback gate and the #309 ?write=1 gate. NO flag unlocks these: the ledger
// must be unerasable (DROP), unexfiltratable/unattachable (ATTACH, DETACH,
// VACUUM INTO), and unreconfigurable (PRAGMA assignment). One statement per
// call — a ';' followed by more SQL closes the SELECT-then-piggyback hole.
// Scans STRIPPED text with word boundaries, so content and comments never
// false-positive and a column named "dropout" passes.
func denySQL(q string) string {
	s := strings.ToUpper(stripSQL(q))
	word := func(w string) bool {
		for idx, from := 0, 0; ; from = idx + len(w) {
			idx = strings.Index(s[from:], w)
			if idx < 0 {
				return false
			}
			idx += from
			bounded := (idx == 0 || !isWordRune(s[idx-1])) &&
				(idx+len(w) >= len(s) || !isWordRune(s[idx+len(w)]))
			if bounded {
				return true
			}
		}
	}
	switch {
	case word("DROP"):
		return "DROP — the ledger is unerasable"
	case word("ATTACH") || word("DETACH"):
		return "ATTACH/DETACH — the store is this one db, no bridges"
	case word("VACUUM") && word("INTO"):
		return "VACUUM INTO — no exfiltration to files"
	case word("PRAGMA") && strings.Contains(s, "="):
		return "PRAGMA assignment — the store is not reconfigurable over the wire"
	}
	// #431: the sync-owned tables are READ-side projections, rebuilt (DROP+
	// CREATE) from the live sources every sync — a write to one is a phantom
	// that self-erases and never reaches the source of truth (the conductor
	// filed a finding into eight.db.work and it vanished). Mutate the LIVE
	// surface instead: POST /work. Agents' own tables stay writable.
	if t := writeTarget(s); t == "WORK" || t == "SURFACES" || t == "EVENTS" || t == "BENCHES" {
		return "table " + strings.ToLower(t) + " is a read-side snapshot (rebuilt on sync) — mutate the live surface (POST /work) instead"
	}
	if i := strings.Index(s, ";"); i >= 0 && strings.TrimSpace(s[i+1:]) != "" {
		return "multi-statement — one statement per call"
	}
	return ""
}

func isWordRune(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z')
}

// writeTarget extracts the table a write statement aims at ("" when q is not a
// write): INSERT INTO t / REPLACE INTO t / UPDATE t / DELETE FROM t. Operates
// on the STRIPPED uppercase text, so prose and comments can't fake a target.
func writeTarget(s string) string {
	f := strings.Fields(s)
	trim := func(t string) string {
		return strings.TrimFunc(t, func(r rune) bool {
			return !(r == '_' || (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z'))
		})
	}
	for i, tok := range f {
		switch tok {
		case "INSERT", "REPLACE":
			if i+2 < len(f) && f[i+1] == "INTO" {
				return trim(f[i+2])
			}
		case "UPDATE":
			if i+1 < len(f) {
				return trim(f[i+1])
			}
		case "DELETE":
			if i+2 < len(f) && f[i+1] == "FROM" {
				return trim(f[i+2])
			}
		}
	}
	return ""
}
