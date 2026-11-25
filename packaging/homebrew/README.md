# Homebrew tap for the four-system (#9793 / #53 P3-BREW)

This directory is the **inward-staged contents of the future `rrrishi123/homebrew-tap`
repo**. It is reviewed and merged here first; creating/publishing the public tap
repo is the one **outward** step and is Rishi's to cut.

## What is here

- `Formula/eight-collector.rb` — the **8 collector** (the witness), built from the
  `collector/` Go module in [rrrishi123/8](https://github.com/rrrishi123/8).

## Install (once the tap is published)

```sh
brew tap rrrishi123/tap            # https://github.com/rrrishi123/homebrew-tap
brew install rrrishi123/tap/eight-collector
# before the v0.0.5 tag is minted, install the tip:
brew install --HEAD rrrishi123/tap/eight-collector
```

## Cutting the stable release (Rishi, at the v0.0.5 tag)

1. Mint the `v0.0.5` tag on `rrrishi123/8` (and `collector/v0.0.5` for `go install`).
2. In `eight-collector.rb`, uncomment the stable stanza and fill `url` +
   `sha256` (the sha of `…/archive/refs/tags/v0.0.5.tar.gz`).
3. `brew audit --strict --formula Formula/eight-collector.rb` → green.
4. Copy this `Formula/` dir into the published `homebrew-tap` repo.

## Still to formula-ize (follow-on checkpoints, same pattern)

The full installer (`8/install.sh`) ships 7 binaries across 4 modules. Beyond the
collector, the remaining entrypoints each want their own formula:

- `http-mcp`: `cmd/eight` (dispatcher), `cmd/mcp` → `http-mcp`, `cmd/wire`, `cmd/channel`
- `pilot`
- `adapters/browser/cmd/browser`

Each is a straight Go build of a `cmd/…` path; the collector formula is the
template (nested-module `cd` + `std_go_args` + a usage/`--version` smoke test).
