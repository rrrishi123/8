# Codex minds

The collector recognizes `codex` and `codex.exe` panes as minds alongside
Claude Code. It finds the live session from the rollout file held open by the
Codex process, then uses that session for identity, public transcript, context
usage, and the `/mind` roster. The legacy `claude_uuid` field on `/panes` carries
the session UUID for either harness; `harness` identifies the provider.

Declare a discovered session through `POST /identity` with its `uuid`, `pane`,
and chosen `name`. A pane-bound declaration is accepted only when the pane
actually holds that session. Declared Codex minds participate in the same
family verifier ring, inbox, wake, and playlist paths. Playlist budget checks
use the target pane's provider, so Claude's quota does not gate Codex.

Codex's composer determines whether input is safe: an empty composer or a
recognized placeholder is idle; a draft is typing, even when unchanged.
Generation, usage limits, approval prompts, and unrecognized screens prevent
automatic input. `sendToPane` checks the live screen before writing and submits
Codex prompts with one Enter. It does not apply Claude's prompt syntax to
quoted output in a Codex pane.

Codex has no Claude inspector tap. `/panes/tap?pane=%2519` reports the native
rollout budget sensor instead, and `/panes/usage?pane=%2519` reports normalized
usage from that pane's rollout. See [budget sensors](budget-sensors.md) for
refresh behavior.

Validation on 2026-09-28: the collector race suite and vet passed. The live
Mac collector resolved pane `%19` as `codex`, with its actual rollout UUID,
context usage, working state, and provider budget. A witnessed
`POST /panes/send` addressed only to that busy pane returned `sent: 0` (act
#539). Isolated fake-tmux tests cover idle, draft, generating, capped, waiting,
unknown, quoted Claude output, and exactly one submission Enter; they never
send test input to live panes or persist identities in the operator's HOME.
