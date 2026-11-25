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
//
// WHAT THE POST ENFORCES (second-observer review of PR #11): the caller must be
// LOOPBACK (:7070 is tokenless on the LAN — a LAN client must not be able to
// opt a session in); the uuid must be a FULL 36-char session uuid (exact match
// only, so a prefix can never silently match or silently miss); and, to ADD,
// the uuid must belong to a LIVE pane on this host (paneUUIDMap), so an
// arbitrary string or a dead session cannot be registered. Withdrawal is always
// allowed. RESIDUAL, stated: :7070 has no per-session token, so the daemon
// cannot PROVE the requester is that session — another loopback mind could POST
// a sibling's uuid. Every change is witnessed (allocator.allow/withdraw) and
// the file is the operator's: a hand edit wins. Closing the residual is the
// per-session auth work, not this file's.
//
// The file is written tmp+fsync+rename under a mutex: a torn file would read
// as empty (silence — the safe direction) but would lose every opt-in, and two
// concurrent POSTs must not drop each other's line.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var allowMu sync.Mutex // guards read-modify-write of the registry file

func allowlistPath() string {
	if p := os.Getenv("EIGHT_ALLOCATOR_ALLOWLIST"); p != "" {
		return p
	}
	return os.ExpandEnv("$HOME/.8/allocator-allowlist.txt")
}

// isFullUUID — 8-4-4-4-12 lowercase/uppercase hex, 36 chars. Exact-match
// registry: anything shorter is NOT an entry (reported as ignored, never matched).
func isFullUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, ch := range s {
		switch i {
		case 8, 13, 18, 23:
			if ch != '-' {
				return false
			}
		default:
			if !(ch >= '0' && ch <= '9' || ch >= 'a' && ch <= 'f' || ch >= 'A' && ch <= 'F') {
				return false
			}
		}
	}
	return true
}

// parseAllowlistReport — one uuid per line; '#' comments and trailing notes
// allowed ("f9129044-… # %8"). Only FULL uuids become entries; every other
// token is returned as ignored so a hand-typed prefix is visible, not silent.
func parseAllowlistReport(text string) (map[string]bool, []string) {
	set := map[string]bool{}
	var ignored []string
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
		if isFullUUID(f[0]) {
			set[strings.ToLower(f[0])] = true
		} else {
			ignored = append(ignored, f[0])
		}
	}
	return set, ignored
}

// parseAllowlist — the set alone (empty on any error: fail closed).
func parseAllowlist(text string) map[string]bool {
	set, _ := parseAllowlistReport(text)
	return set
}

func loadAllowlist() map[string]bool {
	b, err := os.ReadFile(allowlistPath())
	if err != nil {
		return map[string]bool{}
	}
	return parseAllowlist(string(b))
}

// allowed — is this session uuid opted in? Exact match, case-insensitive;
// fail closed on empty uuid.
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

// isLoopbackAddr — "127.0.0.1:5xxxx" / "[::1]:…" → true. Anything else,
// including an unparsable address, is NOT loopback (fail closed).
func isLoopbackAddr(remote string) bool {
	host, _, err := net.SplitHostPort(remote)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// writeFileAtomic — tmp in the same dir, fsync, rename. A reader never sees a
// torn registry; a crash mid-write leaves the old file.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".allowlist-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}

// livePaneUUID — does any live pane on this host run this session uuid?
func livePaneUUID(uuid string) bool {
	for _, u := range paneUUIDMap() {
		if strings.EqualFold(u, uuid) {
			return true
		}
	}
	return false
}

// handleAllocatorAllow — GET /allocator/allow lists the registry; POST
// {"uuid":U,"by":who} appends U (a mind registering ITSELF; the act is witnessed).
// POST {"uuid":U,"remove":true} withdraws it. See the header for what is enforced.
func (c *collector) handleAllocatorAllow(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.Method {
	case http.MethodGet:
		var set map[string]bool
		var ignored []string
		if b, err := os.ReadFile(allowlistPath()); err == nil {
			set, ignored = parseAllowlistReport(string(b))
		} else {
			set = map[string]bool{}
		}
		list := make([]string, 0, len(set))
		for u := range set {
			list = append(list, u)
		}
		sort.Strings(list)
		if ignored == nil {
			ignored = []string{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"n": len(list), "allowed": list, "ignored": ignored, "path": allowlistPath(),
			"note": "opt-in, fail-closed: the allocator types only into these FULL session uuids (exact match). A mind registers ITSELF: POST {uuid, by} from loopback; 'ignored' lists tokens in the file that are not full uuids and so never match."})
	case http.MethodPost:
		if !isLoopbackAddr(r.RemoteAddr) {
			http.Error(w, `{"error":"loopback only: a session opts itself in from its own host"}`, 403)
			return
		}
		var p struct {
			UUID   string `json:"uuid"`
			By     string `json:"by"`
			Remove bool   `json:"remove"`
		}
		if json.NewDecoder(r.Body).Decode(&p) != nil {
			http.Error(w, `{"error":"bad json"}`, 400)
			return
		}
		u := strings.ToLower(strings.TrimSpace(p.UUID))
		if !isFullUUID(u) {
			http.Error(w, `{"error":"need the FULL 36-char session uuid (exact match registry; prefixes never match)"}`, 400)
			return
		}
		if !p.Remove && !livePaneUUID(u) {
			http.Error(w, `{"error":"uuid is not a live pane's session on this host; only a running session may be opted in (hand-edit the file to override)"}`, 403)
			return
		}
		allowMu.Lock()
		defer allowMu.Unlock()
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
		body := "# allocator opt-in registry (#9602) — one FULL session uuid per line; fail-closed, exact match.\n# A mind adds ITSELF via POST /allocator/allow (loopback, live pane). Last change: " + time.Now().UTC().Format(time.RFC3339) + " by " + firstNonEmpty(p.By, "unknown") + "\n" + strings.Join(lines, "\n") + "\n"
		if err := writeFileAtomic(allowlistPath(), []byte(body), 0o644); err != nil {
			http.Error(w, fmt.Sprintf(`{"error":%q}`, err.Error()), 500)
			return
		}
		act := "allow"
		if p.Remove {
			act = "withdraw"
		}
		c.publish(fmt.Sprintf(`{"session":"work","origin":"COLLECTOR","frame":{"method":"allocator.%s","params":{"uuid":%q,"by":%q,"from":%q,"n":%d}}}`, act, u, p.By, r.RemoteAddr, len(set)))
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "action": act, "uuid": u, "n": len(set)})
	default:
		http.Error(w, "GET or POST", 405)
	}
}
