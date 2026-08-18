package main

import (
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// totalRAMMB returns physical RAM in MB, platform-agnostically (macOS/BSD sysctl,
// Linux /proc/meminfo) — sizes the proactive-recycle bound to the machine. One Go
// function replaces the per-OS shell that used to live in two watchdog scripts.
func totalRAMMB() int {
	if out, err := exec.Command("sysctl", "-n", "hw.memsize").Output(); err == nil {
		if b, e := strconv.ParseInt(strings.TrimSpace(string(out)), 10, 64); e == nil && b > 0 {
			return int(b / 1048576)
		}
	}
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				if f := strings.Fields(line); len(f) >= 2 {
					if kb, e := strconv.Atoi(f[1]); e == nil {
						return kb / 1024
					}
				}
			}
		}
	}
	return 0
}

// recycleThresholdMB — ~18% of physical RAM, capped 4500 (Firefox OOMs below 4500 on
// RAM-starved machines: 18GB→~3300 < ~3800 crash). 0 RAM → 4500 fallback.
func recycleThresholdMB() int {
	ram := totalRAMMB()
	if ram <= 0 {
		return 4500
	}
	if rec := ram * 18 / 100; rec < 4500 {
		return rec
	}
	return 4500
}

// ── HOST RESOURCE METRICS (/collector/hostres) ──────────────────────────────
// Each collector reports the resources of the host it runs on, so 8's main
// collector on this mac can show an inner host — omarchy, a container — over
// the wire. Read straight from the OS (no external dependency): /proc on Linux,
// sysctl/vm_stat on macOS — the same runtime-branch pattern totalRAMMB uses.

type hostRes struct {
	Host       string  `json:"host"`
	OS         string  `json:"os"`
	Arch       string  `json:"arch"`
	CPUs       int     `json:"cpus"`
	Load1      float64 `json:"load1"`
	Load5      float64 `json:"load5"`
	Load15     float64 `json:"load15"`
	MemTotalMB int     `json:"mem_total_mb"`
	MemUsedMB  int     `json:"mem_used_mb"`
	MemFreeMB  int     `json:"mem_free_mb"`
	UptimeSec  int64   `json:"uptime_sec"`
	At         int64   `json:"at"`
}

func readHostRes() hostRes {
	h, _ := os.Hostname()
	total := totalRAMMB()
	used, free := memUsedFreeMB(total)
	l1, l5, l15 := loadAvg()
	return hostRes{
		Host:       h,
		OS:         runtime.GOOS,
		Arch:       runtime.GOARCH,
		CPUs:       runtime.NumCPU(),
		Load1:      l1,
		Load5:      l5,
		Load15:     l15,
		MemTotalMB: total,
		MemUsedMB:  used,
		MemFreeMB:  free,
		UptimeSec:  uptimeSec(),
		At:         time.Now().Unix(),
	}
}

// loadAvg returns the 1/5/15-minute load averages. Linux reads /proc/loadavg;
// macOS/BSD read `sysctl -n vm.loadavg` ("{ 1.98 2.03 2.10 }").
func loadAvg() (float64, float64, float64) {
	if data, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(data)); len(f) >= 3 {
			return atof(f[0]), atof(f[1]), atof(f[2])
		}
	}
	if out, err := exec.Command("sysctl", "-n", "vm.loadavg").Output(); err == nil {
		var nums []float64
		for _, t := range strings.Fields(string(out)) { // { 1.98 2.03 2.10 }
			if v, e := strconv.ParseFloat(t, 64); e == nil {
				nums = append(nums, v)
			}
		}
		if len(nums) >= 3 {
			return nums[0], nums[1], nums[2]
		}
	}
	return 0, 0, 0
}

// memUsedFreeMB derives used/free MB from the OS given the total. Linux uses
// MemAvailable from /proc/meminfo (free = available, used = total - available);
// macOS sums free+inactive+speculative pages from `vm_stat`, best-effort.
func memUsedFreeMB(totalMB int) (used, free int) {
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemAvailable:") {
				if f := strings.Fields(line); len(f) >= 2 {
					if kb, e := strconv.Atoi(f[1]); e == nil {
						free = kb / 1024
						if totalMB > free {
							used = totalMB - free
						}
						return used, free
					}
				}
			}
		}
	}
	if out, err := exec.Command("vm_stat").Output(); err == nil {
		pageSize := 4096
		lastInt := func(l string) int64 {
			l = strings.TrimSuffix(strings.TrimSpace(l), ".")
			f := strings.Fields(l)
			if len(f) == 0 {
				return 0
			}
			v, _ := strconv.ParseInt(f[len(f)-1], 10, 64)
			return v
		}
		var freePages, inactivePages, specPages int64
		for _, line := range strings.Split(string(out), "\n") {
			switch {
			case strings.Contains(line, "page size of"):
				for _, t := range strings.Fields(line) {
					if v, e := strconv.Atoi(t); e == nil {
						pageSize = v
					}
				}
			case strings.HasPrefix(line, "Pages free:"):
				freePages = lastInt(line)
			case strings.HasPrefix(line, "Pages inactive:"):
				inactivePages = lastInt(line)
			case strings.HasPrefix(line, "Pages speculative:"):
				specPages = lastInt(line)
			}
		}
		free = int((freePages + inactivePages + specPages) * int64(pageSize) / 1048576)
		if totalMB > 0 && free <= totalMB {
			used = totalMB - free
		}
		return used, free
	}
	return 0, 0
}

// uptimeSec returns seconds since boot. Linux reads /proc/uptime; macOS derives
// it from `sysctl -n kern.boottime` ("{ sec = 1723600000, usec = 0 } ...").
func uptimeSec() int64 {
	if data, err := os.ReadFile("/proc/uptime"); err == nil {
		if f := strings.Fields(string(data)); len(f) >= 1 {
			if v, e := strconv.ParseFloat(f[0], 64); e == nil {
				return int64(v)
			}
		}
	}
	if out, err := exec.Command("sysctl", "-n", "kern.boottime").Output(); err == nil {
		s := string(out)
		if i := strings.Index(s, "sec ="); i >= 0 {
			rest := strings.TrimLeft(s[i+len("sec ="):], " ")
			num := ""
			for _, r := range rest {
				if r >= '0' && r <= '9' {
					num += string(r)
				} else {
					break
				}
			}
			if boot, e := strconv.ParseInt(num, 10, 64); e == nil && boot > 0 {
				return time.Now().Unix() - boot
			}
		}
	}
	return 0
}

func atof(s string) float64 { v, _ := strconv.ParseFloat(s, 64); return v }

// handleHostRes serves this host's resource metrics as JSON. The witness
// middleware stamps the receipt; this only writes the body.
func (c *collector) handleHostRes(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(readHostRes())
}
