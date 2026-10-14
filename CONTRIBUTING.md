# Contributing

## Build

`./build.sh` produces a **SHA-stamped** collector binary (a bare `go build`
leaves `/health`'s build empty — always use `build.sh`). CI gates build + vet +
test + gofmt.

## Restarting the witness

The collector **is** the witness. Restarting it is bouncing the witness, so
prefer `scripts/collector-restart.sh` (or let `watchdog.sh` revive it) over an
ad-hoc kill — and source `~/.8/up.env` so brokers and `research_ceil` are
restored. Exactly one collector at a time.

## Branch model

Work targets `release/v0.0.2`. Do not push to `main` or mint tags without an
explicit release decision.

## Conventions

- **Inscription-only trust:** the mind reads live state (`/manifest`, `/work`,
  `/peers`) rather than reciting memory; a claim without a witness receipt is
  *asserted*, not confirmed.
- `/manifest` is **authoritative** for browser truth; never trust a raw
  `getTree`.
- Every act carries an `X-8-Witness` receipt — prefer the witnessed path.

## Pull requests

Small, reviewable commits. `gofmt` clean, `go vet` clean, `go test ./...` green.
