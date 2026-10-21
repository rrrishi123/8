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
- CI to all arms: the `build`/`vet`/`test`/`gofmt` gate `8` and `http-mcp` carried
  is now on `pilot` and `adapters` too (`adapters` also gates its nested `webrtc`
  module and the `.bin/` build); every arm of the four-system now CI-gates on push
  and PR.
- **Security**: the wire binds to loopback by default (`collector/up.go`);
  `/claim` has a host-resolved credential gate (#83); no host names, addresses
  or private design material in the public tree (`web/public/fleet.html` takes
  peers as `?hosts=name=url,…`); SECURITY.md and CONTRIBUTING.md.
- **Witnessed events**: `POST/GET /event` reply-event bus, reply-completion
  detection, and wake-on-event when a task flips done.
- **Browsers**: CDP input (Live drive controls chrome tabs, not only Firefox),
  chrome broker self-heal on a broken pipe, `EIGHT_ENGINE=chromium|edge|brave`,
  a browser-level broker ws, and watched chrome tabs read over their named broker.
- **Bring-up portability** (fresh-Linux-VM e2e, #4346): `up.sh` installs the
  cockpit deps on first run, starts pilot's broker, and detaches stdin;
  `fleet-ci` smokes boot-to-first-seat.
- **Hosts**: host-supplied daemons (`~/.8/daemons.conf`, `journal:<unit>` logs),
  stable per-identity seats, long-dead peers pruned from the roster after 15m.
- **Cockpit**: resizable cards, typing into a tmux pane from its card,
  ANSI-aware TUI state.
- **`scripts/poke.sh`**: wake an outside agent by webhook when `/work` has open
  items. Off unless `EIGHT_POKE_URL` is set; the URL and `EIGHT_POKE_AUTH` live
  in `~/.8/poke.env` (chmod 600), rate-limited by `EIGHT_POKE_MIN_S`.
- **`scripts/demo.sh`**: the pieces in action, narrated.
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
