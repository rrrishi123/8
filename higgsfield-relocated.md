# higgsfield/ — relocated out of the module (#321/#356, 2026-08-14)

The movie-pipeline content that briefly shipped in v0.0.2 (briefs, python,
frame_zero json, a checked-in PDF) is **operator content, not witness
machinery** — the 8 repo's charter is observe / attribute / replay, and per
module-design cohesion (go.dev/doc/modules/developing#design) it does not
belong here. It is now untracked (`/higgsfield` gitignored).

Where it lives:
- **canonical home**: `~/.8/higgsfield/` — physically moved there (#356) once
  the P-series wrapped (Frame Zero v2 complete + P05-redo accepted). The move
  was same-filesystem, so the resident worker's CWD followed the inode.
- **on this host**: `repos/8/higgsfield` is a compat **symlink** to the
  canonical home, kept so stale path references keep resolving; it is
  gitignored, never tracked.

History note: the files remain in git history before this commit; this is a
module-surface relocation, not a history scrub.
