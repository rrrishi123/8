#!/usr/bin/env bash
# build.sh — the canonical build for 8. ONE bundled binary: collector/collector,
# the witness/panopticon. It also carries the `up` (bring-up) and `watch`
# (supervision) subcommands (#279), so the single binary boots and guards itself
# on any host. (#320: `up`/`watch` are the PORTABLE boot, not yet full parity —
# they start the collector bare, without -brokers/-gecko wiring or the cockpit;
# scripts/up.sh + watchdog.sh remain the full boot on the mac host, stamped so.)
# Build with THIS, not an ad-hoc `go build -o`, so everyone produces the same
# artifact at the same path.
set -euo pipefail
root="$(cd "$(dirname "$0")" && pwd)"
cd "$root/collector"
go build -o collector .
echo "built: $root/collector/collector"
echo "  serve:  ./collector -listen :7070 ...   (the panopticon)"
echo "  boot:   ./collector up                  (discover substrate + start bodies, degrade if absent)"
echo "  guard:  ./collector watch               (revive the collector if it dies)"
