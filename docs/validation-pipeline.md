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

Run it standalone any time: `python3 scripts/verify-live.py` (`--no-hard` = soft reload only;
`--fresh` = also open a throwaway cockpit renderer and report today's TRUE footprint, ~20s).

## The crux: a refresh does NOT reclaim RAM — only a fresh renderer does

This is the single most important thing the pipeline taught, and it changes how "did RAM go
down" must be measured:

- **`phys_footprint` is sticky.** A soft/hard reload *reuses the same renderer process*, which
  holds committed high-water memory from before a fix. Measured: after the ledger-cap fix, the
  running cockpit's DOM collapsed from **117,214 → 2,160 nodes** and JS heap from **303 → 29 MB**,
  yet `phys_footprint` stayed ~2.9 GB. Even `Memory.forciblyPurgeJavaScriptMemory` returned only
  ~35 MB. Chrome does not hand committed pages back to the OS on a same-process reload.
- **A fresh renderer (close + reopen the tab) starts clean** — it builds only what today's code
  builds. Measured on a fresh cockpit with the fixes in place: **98–134 MB vs the old renderer's
  2604–2616 MB — a ~20–26× reduction.**

So: **`deploy.sh` / a hard-refresh picks up new code, but to actually shed the RAM on the
running cockpit you must close and reopen the tab.** `verify-live.py --fresh` measures a fresh
renderer precisely so a memory change can be proven even while the running tab's high-water
hides it.

## Honest findings this pipeline has surfaced

- **The ~2.9 GB holder was the work ledger**, not the canvas/GPU: it rendered every one of
  9,000+ ledger items as a DOM row (`work-row ×9495`, ~66 k nodes). Capping it to open + newest
  40 done drops a fresh cockpit to **~100 MB** (#9492). The footprint **gate** still caps the
  *climb* for the long-running process (it was peaking ~5 GB); the cap prevents the climb at the
  source.
- **Control is ~0–1 ms warm** (steady state), after a one-time ~450 ms cold viewport-cache act —
  the async ordered-gesture + browser-side cached viewport fix holds across reloads.
- **Dedup works** — two `pretext/bubbles` tabs collapse to one live stream + one `↔dup` shadow.

The point of writing RAM/latency/DOM as **checks**: the next time a change claims to lower RAM
or keep control fast, `deploy.sh --fresh` says PASS/FAIL with the honest numbers — no assertion
without measurement.
