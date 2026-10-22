package main

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// chromeRSS reads the OS-level RESIDENT memory of Chrome renderer processes via ps.
// RSS is cheap and covers EVERY renderer — but it is NOT the number a human sees in
// Chrome's Task Manager. That number is phys_footprint (committed physical memory:
// JS heap + GPU transfer buffers + reserved), which can be ~10x the RSS for a heavy
// canvas page. The cockpit (localhost:8088) renderer measured 335 MB RSS but 2924 MB
// phys_footprint (peak ~5 GB) — so a witness that reads ONLY RSS is blind to the very
// bloat the operator is staring at. handleRSS pairs both: RSS for breadth, footprint
// for the heaviest few (bounded) so 8 SEES the real committed memory and can act.
type rssProc struct {
	PID         int `json:"pid"`
	RSSMB       int `json:"rss_mb"`
	FootprintMB int `json:"footprint_mb,omitempty"` // phys_footprint: the Chrome-Task-Manager number; 0 = not probed
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

var footprintRe = regexp.MustCompile(`phys_footprint:\s+(\d+)\s*MB`)

// footprintMB runs macOS `footprint <pid>` and returns phys_footprint in MB (0 on any
// failure). This is the committed-memory number Chrome's own Task Manager mirrors — the
// one the operator sees — which ps RSS cannot report. Bounded to the heaviest few
// renderers by the caller so the probe stays cheap.
func footprintMB(pid int) int {
	out, err := exec.Command("footprint", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0
	}
	m := footprintRe.FindSubmatch(out)
	if m == nil {
		return 0
	}
	v, _ := strconv.Atoi(string(m[1]))
	return v
}

// handleRSS exposes BOTH the RSS (ps, all renderers) and the phys_footprint (the real
// committed memory Chrome's Task Manager shows) for the heaviest renderers — so 8 is no
// longer blind to the 2.9 GB kind of memory the operator actually sees and complains
// about. heaviest_footprint_mb is the honest "how big is the worst tab" number.
func (c *collector) handleRSS(w http.ResponseWriter, r *http.Request) {
	procs := chromeRSS()
	// footprint the top few by RSS (cockpit canvas is usually #1); bounded so cheap.
	topN := 4
	if len(procs) < topN {
		topN = len(procs)
	}
	totalRSS, heaviestRSS, heaviestFP := 0, 0, 0
	for i := range procs {
		totalRSS += procs[i].RSSMB
		if i < topN {
			procs[i].FootprintMB = footprintMB(procs[i].PID)
			if procs[i].FootprintMB > heaviestFP {
				heaviestFP = procs[i].FootprintMB
			}
		}
	}
	if len(procs) > 0 {
		heaviestRSS = procs[0].RSSMB
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"renderers":             procs,
		"heaviest_mb":           heaviestRSS, // RSS of the heaviest (kept for back-compat)
		"heaviest_footprint_mb": heaviestFP,  // phys_footprint — the number the operator SEES
		"total_mb":              totalRSS,
		"note":                  "rss_mb = OS resident (ps); footprint_mb = phys_footprint (the committed memory Chrome's Task Manager shows — often ~10x the RSS for a canvas tab). heaviest_footprint_mb is the honest worst-tab number; it is what a human calls '3.6 GB'.",
	})
}
