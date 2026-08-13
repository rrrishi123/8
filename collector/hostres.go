package main

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
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
