package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ── NAME DISPLAY + PROVENANCE (2026-09-16) ────────────────────────────────────
// The mind's name shows on its tmux pane border via a per-pane user option
// @mind (Claude Code owns pane_title, so we never fight it). Ownership is the
// point: @mind_by is "8" (the collector set it) or "user" (you set it). The
// collector writes @mind from the resolved name ONLY when @mind_by != "user";
// a name you set is imported into the roster as yours and NEVER overridden by
// the auto-namer or the /clear self-heal. Change a name yourself with
// ~/.8/mindname.sh %N NEWNAME (sets @mind + @mind_by=user).

var (
	nameSrcMu sync.Mutex
	nameSrc   = map[string]string{} // uuid -> "user"|"8" (loaded lazily)
	nameSrcLd bool
)

func nameSrcFile() string { return os.ExpandEnv("$HOME/.8/name-source.json") }

func loadNameSrc() map[string]string {
	nameSrcMu.Lock()
	defer nameSrcMu.Unlock()
	if !nameSrcLd {
		if b, err := os.ReadFile(nameSrcFile()); err == nil {
			json.Unmarshal(b, &nameSrc)
		}
		nameSrcLd = true
	}
	m := make(map[string]string, len(nameSrc))
	for k, v := range nameSrc {
		m[k] = v
	}
	return m
}

func setNameSrc(uuid, src string) {
	nameSrcMu.Lock()
	defer nameSrcMu.Unlock()
	nameSrc[uuid] = src
	if b, err := json.MarshalIndent(nameSrc, "", "  "); err == nil {
		os.WriteFile(nameSrcFile(), b, 0o644)
	}
}

// userOwned — this uuid's name was set by the human; the collector must not override it.
func userOwned(uuid string) bool { return loadNameSrc()[uuid] == "user" }

// syncPaneNames — one tick: reconcile each live claude pane's @mind with the
// roster, honouring user ownership in BOTH directions.
func (c *collector) syncPaneNames(now time.Time) {
	tb := tmuxBin()
	if tb == "" {
		return
	}
	out, err := exec.Command(tb, "list-panes", "-a", "-F",
		"#{pane_id}|#{pane_pid}|#{pane_current_command}|#{@mind}|#{@mind_by}|#{session_name}:#{window_index}.#{pane_index}").Output()
	if err != nil {
		return
	}
	uu := paneUUIDs()
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.SplitN(ln, "|", 6)
		if len(f) != 6 || !strings.Contains(f[2], "claude") {
			continue
		}
		pane, ppid, mind, by, seat := f[0], f[1], f[3], f[4], f[5]
		uuid := uu[ppid]
		if uuid == "" {
			continue
		}
		if by == "user" && mind != "" {
			// YOUR choice — import it as the mind's name, mark it yours, never override.
			if roleUUID(mind) != uuid || !userOwned(uuid) {
				c.setName(mind, seat, uuid, "user")
			}
			continue
		}
		// collector-owned: reflect the resolved name onto the border.
		resolved, _ := nameForUUID(uuid)
		if resolved != "" && mind != resolved {
			exec.Command(tb, "set", "-p", "-t", pane, "@mind", resolved).Run()
			exec.Command(tb, "set", "-p", "-t", pane, "@mind_by", "8").Run()
		}
	}
}
