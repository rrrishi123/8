package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
)

// ── ROLE-BASED ASSIGNEES — the real %N-is-identity fix. A task's Assignee may be
// a stable ROLE (philo, pmf, sibling-1, higgs, conductor), a claude session uuid,
// or a raw %N. Role/uuid are re-resolved to the CURRENT live pane at every
// dispatch, so a renumbered pane (reboot, restart) still gets its work — the
// number churns, the identity (uuid) doesn't. Roles live in ~/.8/roles.json
// (host-specific, out of code), mapping name -> uuid.

func rolesFile() string { return os.ExpandEnv("$HOME/.8/roles.json") }

func roleUUID(role string) string {
	b, err := os.ReadFile(rolesFile())
	if err != nil {
		return ""
	}
	var m map[string]string
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m[role]
}

// paneForUUID resolves a claude session uuid to its current live tmux pane by
// reading the running claude child's args (`claude --resume <uuid>`) and matching
// its PPID to a pane's pane_pid.
func paneForUUID(uuid string) string {
	if uuid == "" {
		return ""
	}
	out, _ := exec.Command("ps", "-eo", "ppid,args").Output()
	var ppid string
	for _, line := range strings.Split(string(out), "\n") {
		if strings.Contains(line, "--resume "+uuid) || strings.Contains(line, "--resume="+uuid) {
			if f := strings.Fields(line); len(f) > 0 {
				ppid = f[0]
				break
			}
		}
	}
	if ppid == "" {
		return ""
	}
	po, _ := exec.Command(tmuxBin(), "list-panes", "-a", "-F", "#{pane_id} #{pane_pid}").Output()
	for _, line := range strings.Split(string(po), "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == ppid {
			return f[0]
		}
	}
	return ""
}

// resolveAssignee turns a task's Assignee into a live pane id: a SELF-DECLARED
// name (#437, preferred — the mind's own claim via POST /identity), a role via
// roles.json (DEPRECATED static roster, kept as fallback until every mind
// declares), a uuid directly, or a raw %N as-is. Empty when it can't resolve.
func resolveAssignee(a string) string {
	switch {
	case a == "":
		return ""
	case strings.HasPrefix(a, "%"):
		return a // already a pane id
	case declaredUUID(a) != "":
		return paneForUUID(declaredUUID(a)) // declared name/alias -> uuid -> live pane
	case roleUUID(a) != "":
		return paneForUUID(roleUUID(a)) // DEPRECATED fallback: roles.json
	case len(a) >= 8 && strings.Count(a, "-") >= 4:
		return paneForUUID(a) // looks like a uuid
	default:
		return a
	}
}
