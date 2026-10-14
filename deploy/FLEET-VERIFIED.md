# Automatic Colima deployment — 2026-09-30 UTC

The single-host T14 acceptance path passed using the installed
`dev.eight.fleet.colima` LaunchAgent. Push `8@fcb9731` triggered
[CI run 36659580980](https://github.com/rrrishi123/8/actions/runs/36659580980),
which built/tested all four arms and published the pinned manifest. After
successful CI, the watcher cloned private sources, built image `96d618a5654c`,
replaced the runtime, and wrote its attestation at 02:26:55 UTC. No manual
build or runtime restart was used for this deployment.

The manifest pins:

| Arm | Commit |
| --- | --- |
| 8 | `fcb9731ed967c737f5d48fd1c8b9372e25d98631` |
| http-mcp | `9e7bf52cd604dac676d51694efea33d795a6837d` |
| pilot | `10c0dc7f89f4a13c1dd5580e0e9342135f743470` |
| adapters | `fde885bc970c4396b251dde429437cc2321d41d9` |

Independent checks used `http-mcp http_request`:

- Runtime `:7071/health`, act 7: alive, exact collector SHA, both Chrome seats.
- Runtime `:7071/work`, act 8: original persistence marker 1 retained.
- Mac `/peers`, act 28547: fresh native `colima-runtime` heartbeat carrying
  that SHA and six tabs.
- Mac `/run?session=fox`, replayable frame 42442: the temporary runtime cockpit
  tab rendered `build fcb9731` with computed green `rgb(66, 184, 131)`.

Docker inspection confirmed the existing `eight-fleet-state:/root/.8` volume,
384 MiB / 1 CPU limits, no OOM, and zero restarts. The image's source manifest
matched all four commits with clean inputs. SQLite `pragma integrity_check`
returned `ok`. The watcher retained the previous stopped runtime for rollback;
its receipt and rollback metadata are in `~/.8/fleet/colima`.

The initial attempt exposed a real bootstrap failure: `clone --no-checkout`
has an empty index and looks dirty before checkout. The watcher now initializes
new clones in staging before promotion, while preserving existing local edits.
Real Git regression tests cover first deployment, repeat/update, failed-fetch
recovery, and refusal to build staged, unstaged or untracked source additions.
Reinstallation also retries launchd's asynchronous bootout/bootstrap race.

Scope: this proves automatic deployment on Colima. The native-host installer
exists but its unattended deployment has not been proven on Mac or Omarchy.
Peer chips compare with the local running collector: a newer Colima build
correctly appears as drift in an older Mac cockpit, while the runtime's own
build chip identifies its new binary. This is not fleet-wide convergence.
The main/tag/published-release hold remains in effect.
