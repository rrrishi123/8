# Security Policy

`8` is the **WITNESS** of the four-system: the collector that records every act
to a ledger and serves the live view (`/work`, `/peers`, `/manifest`, `/budget`,
`/mind`) on `:7070`.

## Supported versions

Active development is on `release/v0.0.2`; security fixes land there first.

## Trust & exposure model

- The collector listens on `:7070` with permissive CORS and **no auth by
  default** — it is designed for loopback / a trusted Tailscale mesh. **Do not
  expose `:7070` to an untrusted network.**
- It has **local authority**: it reads host state (processes, panes) and can
  drive tmux panes (`send-keys`). Treat the host it runs on as the trust
  boundary.
- The witnessed ledger records operational detail (acts, URLs, host resources).
  It redacts credentials seen on the wire, but still should not be served
  publicly.

## Reporting a vulnerability

Report privately via this repository's GitHub Security Advisories
(**Security → Report a vulnerability**), or open a minimal issue describing the
*class* of problem — no live credentials or working exploit in a public issue.
