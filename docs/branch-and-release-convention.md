# Branch & Release Convention — The Wire (four repos)

The four arms — [http-mcp](https://github.com/rrrishi123/http-mcp) (WIRE) ·
[8](https://github.com/rrrishi123/8) (WITNESS) · [pilot](https://github.com/rrrishi123/pilot) (HOST) ·
[adapters](https://github.com/rrrishi123/adapters) (ARMS) — are one system shipped from four
repositories (a small **polyrepo**, like Android/AOSP or Kubernetes, not a monorepo). This file
is the single source of truth for how branches, tags, CI, and releases work across all four. It is
identical in intent across the repos; it physically lives in `8/docs/` because `8` holds the shared
docs and orchestrates the fleet CI.

## 1. Branches — stable names, never versioned

Each repo has exactly **two** long-lived branches:

| branch  | meaning | rule |
|---------|---------|------|
| `main`  | the released/stable line; always equals the latest `vX` tag's content | merge-only; every commit has passed CI + the fleet gate |
| `next`  | the single active working branch for the next cycle | where work lands; may be ahead of `main` |

- **Branch names never carry a version number.** Versions live in tags and the manifest, so CI
  refs never change when you bump. (This is why we moved off `release/v0.0.2`.)
- Short-lived topic branches are fine, but **delete them when merged**. Steady state per repo is
  `main` + `next` (plus, temporarily, any branch explicitly preserved for review — e.g. pilot's
  `thinking-projection`).
- Agent sessions must either work on `next` or branch-and-delete. Do not leave `claude/…`,
  `seer/…`, `inbox/…` droppings behind.

## 2. Versions live in tags + the manifest

- Tag every release `vX` (currently `v0.0.x`) on `main`.
- **Keep every release tag forever** — tags are the immutable rollback archive and the real source
  of truth for "what shipped as v0.0.2." Minimal *branches*, complete *tags*.
- The live release line is **v0.0.3**. `v0.0.1`, `v0.0.2`, `v0.0.2.1` are retained history.
- `fleet-release.json` (published by the fleet gate) is the single source of truth for
  "system version = these four commits," the way AOSP uses `manifest.xml` / Chromium uses `DEPS`.

## 3. CI wiring (decoupled from version names)

- Per-repo **`ci.yml`** runs on `pull_request` + `push` (lint/build/unit).
- **`fleet-gate.yml`** (defined in `8`, `workflow_call`) checks out all four repos, builds and
  tests them together (`8/scripts/fleet-ci.sh`), and publishes `fleet-release.json` pinning the
  tested commits. Sibling checkouts resolve to each repo's **released head (`main`)** when the push
  is not that repo; to the pushed `github.sha` when it is.
- **`fleet-release.yml`** fires on `push` to **`[main, next]`** and calls the gate. In the three
  siblings it references the gate as `rrrishi123/8/.github/workflows/fleet-gate.yml@main`.
- Because every ref above is a **stable name**, bumping `v0.0.3 → v0.0.4 → …` never touches a
  workflow file again.

## 4. The release cycle

```
work on next
  → open PR next → main (per-repo ci.yml + cross-repo fleet gate must be green)
  → squash-merge into main
  → tag vX on main
  → fleet-release publishes fleet-release.json (the pinned four-commit manifest)
  → hosts deploy by pinning that manifest
  → next continues toward the following version
```

A host never deploys a head newer than the manifest it pinned; the manifest is what makes the four
repos behave as one coherent, tested system.

## 5. Packaging

- `http-mcp`, `pilot`, `adapters` are public **Go modules** (`github.com/rrrishi123/<repo>`),
  released by tagging — `go get …@v0.0.3` works.
- `8` is `collector/` (Go) + `web/` (npm `eight-cockpit`, currently `private`). Keep `web`'s
  `version` aligned to the repo's `v0.0.x` line.

## 6. Housekeeping checklist (run occasionally)

- Prune merged topic branches so each repo stays at `main` + `next`.
- Keep all `vX` release tags; only retire scheme-breakers deliberately (open question: the
  `collector/v0.0.*` component tags in `8`, now that `8` ships as one unit).
- Keep GitHub repo descriptions/topics current (set per arm: WIRE / WITNESS / HOST / PROVIDERS).
