# Frame Zero pipeline — Mac takeover notes

Files: pipeline.py (orchestrator, driven through the wire's http_request atom),
frame_zero_v2.json (living manifest — the artifact of record; edit freely between
generations), pipeline_state.json (current state: P01 ingested, download blocked
in the cloud container by egress 403 — will succeed on the Mac).

Three constants to adjust at the top of pipeline.py for the Mac:
- WIRE  -> path to your locally built http-mcp binary (from rrrishi123/http-mcp)
- STATE_F / MANIFEST_F / CLIPS -> a local working dir (e.g. ~/kosaten/frame-zero/)
- stitch output path -> wherever you want frame-zero-cut.mp4

Usage:
  python3 pipeline.py ingest '[{"id":"P02","job_id":"<uuid>","url":"<cloudfront mp4 url>"}]'
  python3 pipeline.py download     # curls every ingested URL into clips/
  python3 pipeline.py stitch       # ffmpeg concat of all downloaded shots, manifest order
  python3 pipeline.py              # print state

Job ids + URLs come from the Higgsfield account history (the web History tab, or
this chat). P01 = 493dac87-b3c4-40b1-9bff-b1f1db8fae56 (already in state).
Generation stays browser-Unlimited only (zero credit); the Mac side polls,
downloads, stitches. ffmpeg required (brew install ffmpeg).
