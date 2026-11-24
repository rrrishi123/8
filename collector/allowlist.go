package main

// allowlist.go — the allocator's OPT-IN, FAIL-CLOSED receiver registry (#9602).
//
// A name on /mind is a LABEL, not consent: anyone can name a pane (mindname.sh,
// a /clear re-point, an auto-name) and a work session given a name for any
// reason would become wakeable — the same defect as "joined by a label, not a
// checked map" (the peer host's own finding). So the allocator may only type into a mind whose
// session UUID is in ~/.8/allocator-allowlist.txt, and a mind gets there by its
// OWN act: POST /allocator/allow {"uuid": <its uuid>, "by": <who>}. Unknown ->
// never. Missing or empty file -> no receivers -> silence. Name alone never
// qualifies; the idle/named checks are secondary to this gate.
//
// Applies to BOTH organs: slice-1 re-routes (receiver must be opted in) and
// slice-2 wakes (the waiting pane's uuid must be opted in — a wait file dropped
// for someone else's pane is not their consent).

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
)

func allowlistPath() string {
	if p := os.Getenv("EIGHT_ALLOCATOR_ALLOWLIST"); p != "" {
		return p
	}
	return os.ExpandEnv("$HOME/.8/allocator-allowlist.txt")
}

// parseAllowlist — one uuid per line; '#' comments and trailing notes allowed
// ("f9129044-… # %8"). Returns the set (empty on any error: fail closed).
func parseAllowlist(text string) map[string]bool {
	set := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		ln := sc.Text()
		if i := strings.IndexByte(ln, '#'); i >= 0 {
			ln = ln[:i]
		}
		f := strings.Fields(ln)
		if len(f) == 0 {
			continue
		}
		if u := strings.ToLower(f[0]); len(u) >= 8 {
			set[u] = true
		}
	}
	return set
}

func loadAllowlist() map[string]bool {
	b, err := os.ReadFile(allowlistPath())
	if err != nil {
		return map[string]bool{}
	}
	return parseAllowlist(string(b))
}

// allowed — is this session uuid opted in? Fail closed on empty uuid.
func allowed(set map[string]bool, uuid string) bool {
	return uuid != "" && set[strings.ToLower(uuid)]
}

// filterAllowed — PURE: only minds whose uuid is opted in survive.
func filterAllowed(minds []mindView, set map[string]bool) []mindView {
	var out []mindView
	for _, m := range minds {
		if allowed(set, m.UUID) {
			out = append(out, m)
		}
	}
	return out
}

// handleAllocatorAllow — GET /allocator/allow lists the registry; POST
// {"uuid":U,"by":who} appends U (a mind registering ITSELF; the act is witnessed).
// POST {"uuid":U,"remove":true} withdraws it.
func (c *collector) handleAllocatorAllow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		set := loadAllowlist()
		var list []string
		for u := range set {
			list = append(list, u)
		}
		sort.Strings(list)
		if list == nil {
			list = []string{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"n": len(list), "allowed": list, "path": allowlistPath(),
			"note": "opt-in, fail-closed: the allocator types only into these session uuids. A mind registers ITSELF: POST {uuid, by}."})
	case http.MethodPost:
		var p struct {
			UUID   string `json:"uuid"`
			By     string `json:"by"`
			Remove bool   `json:"remove"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil || len(p.UUID) < 8 {
			http.Error(w, `{"error":"need uuid (>= 8 chars)"}`, 400)
			return
		}
		u := strings.ToLower(strings.TrimSpace(p.UUID))
		set := loadAllowlist()
		if p.Remove {
			delete(set, u)
		} else {
			set[u] = true
		}
		var lines []string
		for k := range set {
			lines = append(lines, k)
		}
		sort.Strings(lines)
		body := "# allocator opt-in registry (#9602) — one session uuid per line; fail-closed.\n# A mind adds ITSELF via POST /allocator/allow. Last change: " + time.Now().UTC().Format(time.RFC3339) + " by " + firstNonEmpty(p.By, "unknown") + "\n" + strings.Join(lines, "\n") + "\n"
		if err := os.MkdirAll(os.ExpandEnv("$HOME/.8"), 0o755); err == nil {
			if err := os.WriteFile(allowlistPath(), []byte(body), 0o644); err != nil {
				http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), 500)
				return
			}
		}
		act := "allow"
		if p.Remove {
			act = "withdraw"
		}
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"allocator.%s","params":{"uuid":%q,"by":%q,"n":%d}}}`, act, u, p.By, len(set)))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "action": act, "uuid": u, "n": len(set)})
	default:
		http.Error(w, "GET or POST", 405)
	}
}
