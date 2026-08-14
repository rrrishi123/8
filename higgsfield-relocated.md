# higgsfield/ — relocated out of the module (#321, 2026-08-14)

The movie-pipeline content that briefly shipped in v0.0.2 (briefs, python,
frame_zero json, a checked-in PDF) is **operator content, not witness
machinery** — the 8 repo's charter is observe / attribute / replay, and per
module-design cohesion (go.dev/doc/modules/developing#design) it does not
belong here. It is now untracked (`/higgsfield/` gitignored).

Where it lives:
- **on this host**: `repos/8/higgsfield/` remains as a live, gitignored scratch
  workspace — at relocation time a claude worker held its CWD there with a
  stitch in flight, so the directory was untracked in place rather than moved
  under running processes.
- **canonical home**: `~/.8/higgsfield/` — the physical move happens when the
  pipeline is idle (filed on the 8 work queue).

History note: the files remain in git history before this commit; this is a
module-surface relocation, not a history scrub.
