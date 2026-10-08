# Reading what a pane is doing — the session→transcript recipe

*"What are panes %6–%10 actually doing?"* — answered from ground truth, not
from stale window titles. Four steps, each a probe over a real surface. This
is the manual version of the same provenance 8 automates; a scheduled reader
(the self-prompt loop) is this recipe in a cron.

The load-bearing lesson: **pane titles lie.** They're set once and drift —
this session found `%7`/`%9` still carrying "validator" titles while they
were building Flutter modules. Read the transcript, not the title.

## 1. What tmux says the pane is (fast, but shallow)

```sh
tmux display -p -t %N \
  '#{session_name}:#{window_index}.#{pane_index} | #{pane_current_command} | #{pane_title} | #{pane_current_path}'
```

Gives you session:window, the foreground command (`claude.exe` = a Claude
seat), the (often stale) title, and cwd. Enough to tell a shell pane from a
Claude pane; not enough to know the task.

## 2. Map the pane to its transcript — via 8's DB, not history.jsonl

The collector keeps a `panes` table in `~/.8/eight.db` that ties each
`pane_id` to the Claude session running in it. This is the correct source —
NOT `~/.claude/history.jsonl` (that's the user-level all-projects firehose,
undifferentiated by pane).

```sh
sqlite3 ~/.8/eight.db \
  "select pane_id, label, role, spawned_by, claude_uuid, jsonl_path
   from panes where pane_id in ('%6','%7','%8','%9','%10')"
```

- `jsonl_path` — the transcript file for that pane's session (step 4).
- `claude_uuid` — the session id (its basename).
- `label` / `role` — 8's own name for the seat (e.g. `pmf`, `conductor`);
  more reliable than the tmux title but still author-set.
- `spawned_by` — provenance, if the seat was spawned by another.
- `cwd` matters: a pane whose cwd is elsewhere (e.g. `~/.8/higgsfield`)
  stores its transcript under a **different** project dir
  (`-Users-<user>--8-higgsfield`), not the repo's.

Live equivalent over the wire (subset of columns, no shell):

```sh
curl -s -H 'X-8-Actor: <you>' 127.0.0.1:7070/panes
# → [{id, loc, cmd, title, first_seen, claude_uuid, jsonl_path}, ...]
```

## 3. What's on screen right now (live tail)

```sh
tmux capture-pane -p -t %N -S -25    # last 25 lines of scrollback
```

Shows the current tool call / prompt / spinner. Good for "is it stuck," bad
for "what was it told to do" — that's the transcript's job.

## 4. The actual assignment — last user turns from the transcript

The task lives in the `type:"user"` entries of the jsonl. Filter out
tool-results and system-reminders (they're also `user`-role) and take the
tail:

```python
import json, sys
prompts = []
for line in open(sys.argv[1]):          # the jsonl_path from step 2
    try: d = json.loads(line)
    except: continue
    if d.get("type") != "user": continue
    c = d.get("message", {}).get("content")
    t = c if isinstance(c, str) else " ".join(
        b.get("text", "") for b in c if isinstance(b, dict) and b.get("type") == "text")
    t = t.strip()
    if t and not t.startswith("<"):     # drop tool_result / system-reminder blocks
        prompts.append(t[:230])
for p in prompts[-3:]:
    print("•", p.replace("\n", " "))
```

The last few user turns are the seat's real orders. Cross-check against the
step-3 tail to see how far it's got.

## Gotchas (each cost real time)

- **Stale titles** — the motivating bug; §4 is the only authority.
- **`macOS python3`** — `/usr/bin/python3` exists; a bare `python3` may not.
  Call the absolute path in scripts.
- **cwd-scoped project dirs** — a pane run from a subdir writes its
  transcript under that dir's slug (§2), so a repo-only search misses it.
- **`sqlite3 ~/.8/eight.db` can be a stale snapshot** — the DB is synced
  from the live collector, not the live ledger itself; for current events use
  the wire (`/provenance`, `/timeline`), for pane→transcript mapping the DB
  is fine (panes change slowly).

## Why this is the seed of the self-prompt loop

Steps 1–4 are exactly what a scheduled reader would run per pane to keep the
manifest's who/why/when fresh: tmux says *where*, the DB says *which
session*, capture says *now*, the transcript says *what it was told*. Witness
each read (POST `/witnessed`, actor = the reader) and the map of "who is
doing what" becomes a live, attributable surface instead of a manual sweep.
```
