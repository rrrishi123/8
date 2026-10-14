# Headless Colima validation — 2026-09-29 UTC

Both `eight-chrome-1` and `eight-chrome-2` now run the pinned Chromium
152.0.7977.82 image with the headless entrypoint. The process tree contains
Chromium and the Go CDP proxy, without the LSIO desktop/Selkies/VNC stack.
The macOS Firefox cockpit was not migrated.

`migrate.sh` copies the stopped profile to a new named volume and retains the
desktop container and original profile for rollback. The health check caught
Chromium's rejection of multiple positional startup URLs and restored the old
seat; the corrected script restores missing URLs through the DevTools HTTP
target endpoint after profile startup. The two old desktop containers remain
stopped under `*-desktop-<timestamp>`. The original profile volumes are retained.

The wire checks used `http-mcp http_request` against a temporary current channel
broker on port 4459. They verified `Target.attachToTarget(flatten=true)`, a
navigation and `Runtime.evaluate` in a fresh test tab, a 1440×813 JPEG screenshot
(17,788 base64 characters; witness #10), and a delayed
`Runtime.consoleAPICalled` event carrying the same session ID (witness #14).
The test target was closed (witness #15). The old installed host channel binary
did not forward flat session IDs; canonical `http-mcp/build.sh` rebuilt it before
the successful test. The deployment image builds that same current source.

After restarting the existing runtime to reconnect browser websockets,
`:7071/manifest` witness #3 reported all five original URLs plus Chromium's new
tab, with fresh timestamps for both seats. Runtime `/work` witness #5 still
returned the original persistence marker #1. Source evidence and migration logs
are under `/tmp/eight-t6-evidence` on the validating host.

Ambient 30-second cgroup CPU samples (100% means one core):

| Seat | Desktop | Headless | Memory point sample after |
| --- | ---: | ---: | ---: |
| eight-chrome-1 | 20.02% | 3.55% | 502.5 MiB |
| eight-chrome-2 | 8.21% immediately before migration | 25.18% | 973 MiB |

The runtime used 21.16 MiB in that point sample. Each browser retains its 1.25
CPU / 2500 MiB ceiling; the runtime retains 1 CPU / 384 MiB. These are ambient
samples, not a controlled benchmark. Seat 1 demonstrates the desktop-overhead
reduction; seat 2 does **not** show a CPU improvement. Its two Claude URLs show
a Cloudflare challenge after migration; no challenge bypass or verified Claude
login is claimed. The retained desktop profile/container provides a reversible
fallback. The browser controls, screenshots and per-target events work.
