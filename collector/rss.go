package main

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// chromeRSS reads the OS-level RESIDENT memory of Chrome renderer processes — the
// 3.6 GB kind that CDP's Performance.getMetrics (JS heap only) CANNOT see. ps is the
// witness's eye into real memory. The heaviest renderer is almost always the cockpit
// itself; this is how 8 stops being blind to the memory it actually consumes.
type rssProc struct {
	PID   int `json:"pid"`
	RSSMB int `json:"rss_mb"`
}

func chromeRSS() []rssProc {
	out, err := exec.Command("ps", "-axo", "pid=,rss=,command=").Output()
	if err != nil {
		return nil
	}
	var procs []rssProc
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.Contains(line, "(Renderer)") || (!strings.Contains(line, "Chrome") && !strings.Contains(line, "Chromium")) {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		pid, _ := strconv.Atoi(f[0])
		rssKB, _ := strconv.Atoi(f[1])
		procs = append(procs, rssProc{PID: pid, RSSMB: rssKB / 1024})
	}
	sort.Slice(procs, func(i, j int) bool { return procs[i].RSSMB > procs[j].RSSMB })
	if len(procs) > 16 {
		procs = procs[:16]
	}
	return procs
}

// handleRSS exposes the real renderer RSS so 8 can SEE (and act on) the memory CDP
// misses — the honest companion to /tabcost (which only reads JS heap).
func (c *collector) handleRSS(w http.ResponseWriter, r *http.Request) {
	procs := chromeRSS()
	total, heaviest := 0, 0
	for _, p := range procs {
		total += p.RSSMB
	}
	if len(procs) > 0 {
		heaviest = procs[0].RSSMB
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"renderers":   procs,
		"heaviest_mb": heaviest,
		"total_mb":    total,
		"note":        "OS resident memory (ps) of Chrome renderer processes — the REAL RSS that CDP heap metrics miss. Heaviest renderer is usually the cockpit. /tabcost reads JS heap; this reads what the OS actually holds.",
	})
}
