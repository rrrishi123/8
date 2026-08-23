package main

import (
	"encoding/json"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// ── INNER-HOST METRICS (#850) — the 4-system observing its own containerized
// incarnation. The operator's lazydocker lives on omarchy and cannot see this
// mac's colima; so the witness reads its OWN inner host and serves it, no
// per-host tool dependency. Data sources are literal and proven:
//   container: docker stats --no-stream --format '...'
//   VM:        colima list
// Both are polled off the request path (a ~4s cache) so /container is cheap and
// the docker/colima CLIs are never invoked per-hit. Absent binaries degrade to
// empty — a mac without colima simply reports no VM, honestly.

type containerStat struct {
	Name  string `json:"name"`
	CPU   string `json:"cpu"` // e.g. "0.00%"
	Mem   string `json:"mem"` // e.g. "408KiB / 3GiB"
	MemPc string `json:"mem_pc"`
	NetIO string `json:"net_io"`
	BlkIO string `json:"blk_io"`
	PIDs  string `json:"pids"`
}

type vmRow struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Arch    string `json:"arch,omitempty"`
	CPUs    string `json:"cpus,omitempty"`
	Memory  string `json:"memory,omitempty"`
	Disk    string `json:"disk,omitempty"`
	Runtime string `json:"runtime,omitempty"`
}

type innerHost struct {
	Containers []containerStat `json:"containers"`
	VMs        []vmRow         `json:"vms"`
	At         string          `json:"at"`
	DockerOK   bool            `json:"docker_ok"`
	ColimaOK   bool            `json:"colima_ok"`
}

var (
	ihMu   sync.Mutex
	ihData innerHost
)

// readContainers — one `docker stats` snapshot (no stream). Empty when docker is
// absent or the daemon is down; the field names are literal (#850: no lingo).
func readContainers() ([]containerStat, bool) {
	out, err := exec.Command("docker", "stats", "--no-stream",
		"--format", "{{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.MemPerc}}\t{{.NetIO}}\t{{.BlockIO}}\t{{.PIDs}}").Output()
	if err != nil {
		return nil, false
	}
	var cs []containerStat
	for _, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		f := strings.Split(ln, "\t")
		if len(f) < 7 {
			continue
		}
		cs = append(cs, containerStat{Name: f[0], CPU: f[1], Mem: f[2], MemPc: f[3], NetIO: f[4], BlkIO: f[5], PIDs: f[6]})
	}
	return cs, true
}

// readVMs — `colima list` (tab/space columns). Header row skipped; absent colima
// degrades to nil.
func readVMs() ([]vmRow, bool) {
	out, err := exec.Command("colima", "list").Output()
	if err != nil {
		return nil, false
	}
	var vms []vmRow
	for i, ln := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if i == 0 || strings.TrimSpace(ln) == "" { // header
			continue
		}
		f := strings.Fields(ln)
		if len(f) < 2 {
			continue
		}
		v := vmRow{Name: f[0], Status: f[1]}
		// colima columns: PROFILE STATUS ARCH CPUS MEMORY DISK RUNTIME
		if len(f) > 2 {
			v.Arch = f[2]
		}
		if len(f) > 3 {
			v.CPUs = f[3]
		}
		if len(f) > 4 {
			v.Memory = f[4]
		}
		if len(f) > 5 {
			v.Disk = f[5]
		}
		if len(f) > 6 {
			v.Runtime = f[6]
		}
		vms = append(vms, v)
	}
	return vms, true
}

// innerHostLoop refreshes the cache every 15s off the request path.
func (c *collector) innerHostLoop() {
	refresh := func() {
		cs, dok := readContainers()
		vms, cok := readVMs()
		ihMu.Lock()
		ihData = innerHost{Containers: cs, VMs: vms, At: time.Now().UTC().Format(time.RFC3339), DockerOK: dok, ColimaOK: cok}
		ihMu.Unlock()
	}
	refresh()
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for range t.C {
		refresh()
	}
}

// handleContainer — GET /container: the inner host, from cache. Cheap, no CLI
// per hit.
func (c *collector) handleContainer(w http.ResponseWriter, r *http.Request) {
	ihMu.Lock()
	d := ihData
	ihMu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(d)
}
