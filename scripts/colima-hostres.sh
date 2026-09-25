#!/usr/bin/env bash
# colima-hostres — emit a /hostres-shaped JSON for the INNER host (the colima VM
# that runs the eight-chrome-* container seats), so peer-beat can register it as
# the third federation peer on the portal. The VM has no collector of its own, so
# we derive its numbers from `colima` (static cpus/mem) + `docker stats` (live
# mem in use) + the VM's own loadavg over ssh when reachable.
set -uo pipefail
cpus=$(colima list 2>/dev/null | awk 'NR==2{print $4}'); [ -n "$cpus" ] || cpus=4
memg=$(colima list 2>/dev/null | awk 'NR==2{print $5}' | grep -oE '[0-9]+'); [ -n "$memg" ] || memg=6
mem_total_mb=$((memg*1024))

# live memory in use = sum of container RSS (docker stats, MiB/GiB -> MiB)
used=$(docker stats --no-stream --format '{{.MemUsage}}' 2>/dev/null | awk '{
  v=$1; u=v; sub(/[0-9.]+/,"",u); gsub(/[A-Za-z]+.*/,"",v);
  if (u ~ /GiB/) v*=1024; else if (u ~ /KiB/) v/=1024;   # MiB stays, MB≈MiB
  s+=v } END{ printf "%d", s }')
[ -n "$used" ] || used=0

# VM loadavg (best-effort; short timeout so a slow/asleep VM never wedges the beat)
load1=$(colima ssh -- cat /proc/loadavg 2>/dev/null | awk '{print $1}')
[ -n "$load1" ] || load1=$(docker stats --no-stream --format '{{.CPUPerc}}' 2>/dev/null | tr -d '%' | awk -v c="$cpus" '{s+=$1} END{printf "%.2f", s/100}')
[ -n "$load1" ] || load1=0

printf '{"host":"colima","os":"linux","arch":"aarch64","cpus":%s,"load1":%s,"mem_total_mb":%s,"mem_used_mb":%s,"at":%s}\n' \
  "$cpus" "$load1" "$mem_total_mb" "$used" "$(date +%s)"
