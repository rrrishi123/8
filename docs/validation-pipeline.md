# Validation pipeline — what is checked, where, and how

A change to the four-system passes through **two** validation halves. CI is static and
headless; the live pipeline is runtime and on-host. Neither replaces the other.

```
  edit ─► commit/push ─► ① CI (GitHub, headless)  ─► ② deploy ─► ③ reload cockpit ─► ④ live verify
          (source of        compiles / typechecks        build +      soft + HARD        RAM, control
           truth)           / unit tests / gofmt /        restart      reload onto        latency, gate,
                            no secrets / fleet build       witness      the new code       dedup → PASS/FAIL
```

## ① CI — the STATIC half (GitHub Actions, headless)

CI proves a commit is *correct in isolation*. It runs on `ubuntu-latest` with no browser,
so it can never observe a running cockpit. Per repo:

| Repo      | `ci.yml` validates                                                                 |
|-----------|-------------------------------------------------------------------------------------|
| **8**     | `build.sh` (bundled binary) · `go vet` · `go test` · `gofmt -l` (fail if unformatted). **web job:** `npm run build` = `tsc -b && vite build` (type-check + build) · `npm test` (vitest: card-size / setUnit layout arithmetic). |
| **http-mcp** | `go build` · `build.sh` · `go vet` · `go test` · `gofmt`. **harvest job:** `cmd/harvest` + new-snapshot detection. **probe job:** start chromedriver, `cmd/probe`, assert the transport floor. |
| **pilot**    | `go build` · `build.sh` · `go vet` · `go test` · `gofmt`. |
| **adapters** | `go build` · `build.sh` · `go vet` · `go test` · `gofmt` (+ the `webrtc/` module too). |

Shared across all four:
- **`leakguard.yml`** — a denylist (operator paths, IPs, private dirs) + gitleaks. Blocks a
  push that would leak a secret or a private host path.
- **`fleet-gate.yml` / `fleet-release.yml`** — checks out all four repos at the pushed SHA,
  builds + tests every arm via `scripts/fleet-ci.sh`, and publishes `fleet-release.json`:
  the pinned, tested-commit manifest a host may deploy. One arm failing fails the fleet.

**CI cannot answer:** did RAM go down? is control still fast? did the gate fire? does dedup
work? — those need a live browser.

## ② ③ ④ The live pipeline — the RUNTIME half (this host)

One command does build → restart → reload → measure:

```sh
./scripts/deploy.sh          # collector + cockpit change
./scripts/deploy.sh --web    # web-only change (vite HMR already served it; just reload + verify)
./scripts/deploy.sh --check  # print the plan
```

Steps:

1. **build** — `build.sh` (same canonical binary CI builds).
2. **restart the witness** — `scripts/collector-restart.sh`: kill `:7070`, let `watchdog.sh`
   revive it (re-deriving the live gecko SID — more correct than any captured argv), verify
   `/health`. A collector binary only runs after this bounce.
3. **reload the cockpit** — the running chromium tab at `localhost:8088` holds the OLD bundle
   in memory. `verify-live.py` resolves that tab from the shared `/manifest` (never hardcoded —
   a reload re-mints the target id), then **soft-reloads** (`Page.reload`, picks up HMR) and
   **hard-reloads** (`Page.reload ignoreCache`, fresh bundle + fresh memory baseline).
4. **measure + PASS/FAIL** — `scripts/verify-live.py`:
   - **collector up** — `/health` responds, prints the build hash.
   - **cockpit tab resolved** — the `:8088` tab is in the manifest.
   - **gate holds footprint ≤ 3600 MB** — the heaviest Chrome renderer's `phys_footprint`
     (via `/rss`, the number Chrome's Task Manager shows — *not* RSS) stays under the ceiling
     across the reload.
   - **control latency < 200 ms (warm)** — a real witnessed `/act` drive, timed after a warmup
     act (the first act after a reload pays a one-time cold viewport-cache round-trip).
   - **dedup** — duplicate-URL tabs collapse to one live observer each (`/manifest`).

Run it standalone any time: `python3 scripts/verify-live.py` (add `--no-hard` to keep the
accumulated RAM and only soft-reload).

## Honest findings this pipeline has surfaced (and why they matter)

- **A reload does NOT reclaim RAM.** Measured: cockpit `phys_footprint` 2826 MB before, a
  transient spike to ~4100 MB *during* a soft reload (the new page allocates before the old is
  torn down), settling at ~2950 MB after a hard reload — i.e. right back to its resting size.
  The 2.9 GB is **structural** (the zoomable canvas + its live streams + GPU compositor), so a
  refresh rebuilds it. The footprint **gate** caps the *climb* (it was peaking ~5 GB) but does
  not shrink the *base*. **Reducing the resting ~2.9 GB is a separate structural change**
  (tracked on ledger #9492; needs a heap snapshot to find the retained holders).
- **Control is ~1 ms warm** (steady state), after a ~450 ms cold-cache first act — the async
  ordered-gesture + cached browser-side viewport fix holds across reloads.
- **Dedup works** — two `pretext/bubbles` tabs collapse to one live stream + one `↔dup` shadow.

The point of writing RAM/latency as **checks**: the next time a change claims to lower RAM or
keep control fast, `deploy.sh` says PASS/FAIL with the honest before/after numbers — no
assertion without measurement.
