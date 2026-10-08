# Budget sensors

The collector reads Codex's local rollout records on startup and every 60s.
It uses `$CODEX_HOME/sessions` (default `$HOME/.codex/sessions`), checks the
32 most recently modified JSONL files, and reads at most the final 2 MiB of
each. Resumed older sessions are included. The newest valid account-level
`event_msg` / `token_count` observation with 300-minute or 10080-minute windows
is persisted atomically to `~/.8/codex-budget.json`. Empty and premium-only
records do not replace account windows. A missing or truncated rollout leaves
the last valid reading intact. The sensor never starts Codex or spends tokens.

`GET /budget` includes `providers.codex` even if Claude has no sensor. Ages and
reset countdowns are computed when read. An idle session keeps its actual
observation timestamp; the collector does not invent a fresh provider reading.
Both daily and weekly windows participate in gating. Native peer join and the
legacy peer-beat script carry available provider readings and timestamps.

Manual `POST /budget/poke?provider=codex` retains the explicit spending path:
`~/.8/codex-budget.sh --poke`. It shares atomic, monotonic persistence with the
passive sensor, so an older manual result cannot overwrite a newer observation.
The former shell polling loop is unnecessary once this collector is deployed.

Claude's tapped poke resolves its working directory from `EIGHT_BUDGET_DIR`,
then `EIGHT_REPO`, then the collector's checkout root, then the user's home.
Explicit invalid overrides fail with an error. No platform-specific Desktop
path is required.
