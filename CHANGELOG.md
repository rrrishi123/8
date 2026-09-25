# Changelog — 8 (the witness)

All four arms of the four-system (8, http-mcp, pilot, adapters) version
independently; the wire contract carries its own version (`contract.Version`
in http-mcp). Tags are unsigned.

## v0.0.3 (unreleased, branch `release/v0.0.2`)

Release-readiness follow-ups to the v0.0.2 line:

- Untrack the committed collector binary (`collector/.bin-collector`) and the
  private design-system reference under `web/design-reference/_ds/`; both are
  now gitignored (anchored).
- `install.sh`: target `v0.0.3`, require Go >= 1.25 (matches `go.mod`), and
  stop claiming the tags are signed.
- `scripts/up.sh`: the browser pack is resolved from `EIGHT_ADAPTERS` (else the
  sibling `adapters` checkout) at `.bin/browser` — the path `adapters/build.sh`
  writes — instead of the untracked in-tree `browser/browser` binary.
- This changelog.

## v0.0.2 — 2026-09

The witness grows a cockpit and an identity contract.

- **Collector**: `/mind` identity contract (seat=address, name=identity,
  uuid/%N/pid=bindings) with self-heal; `/compose` mints a session at a chosen
  context (the vertical-scaling knob); `/delegate` runs cheap work in a fresh
  `claude -p` context with billed cost; `/work/by-pane` ledger keyed on pane
  numbers; `/inbox` offer/take/decline; lineage across tmux restarts
  (`~/.8/lineage.json`, boot epoch, quarantine); `EIGHT_LINEAGE_OBSERVE` second
  witness; `EIGHT_ADAPTERS` env root for adapter paths.
- **Budget**: rate-limit header reader (8MB tail window, unified `*-reset`),
  overage/fallback pseudo-window filtering, freshness (`observed_age_s`,
  `sensors_armed`), `/budget/poke`, research ceiling flip-switch.
- **Cockpit (web)**: budget HUD in the statusline, draggable non-overlapping
  panels, per-card scrolling, pane·live view, hard-reset of stale layouts.
- **Ops**: `install.sh` (go-install flavour over the public module proxy),
  `scripts/up.sh` boots the seat/broker/collector, watchdog pins the witnessed
  tmux server via `~/.8/witness-tmux.env`.
- **Hygiene**: CI (build/vet/test/gofmt) on this arm; private/office paths
  purged; branch renamed to `release/v0.0.2`.

## v0.0.1

First cut of the witness: tmux pane collector, ledger, receipts, canvas UI.
