#!/usr/bin/env python3
"""stage_prompt.py — copy a shot's CANONICAL prompt to the clipboard.
Source of truth = frame_zero_v2.json in THIS repo (never /tmp). Usage:
  python3 stage_prompt.py P07      # copies P07's prompt_felt to the clipboard
Both %5 (wire-driver) and %37 stage from here so the script stays inscribed in the repo."""
import json, sys, subprocess, os
here = os.path.dirname(os.path.abspath(__file__))
d = json.load(open(os.path.join(here, "frame_zero_v2.json")))
sid = sys.argv[1] if len(sys.argv) > 1 else ""
for s in d.get("shots", []):
    if s.get("id") == sid:
        p = (s.get("prompt_felt") or s.get("prompt") or "").strip()
        subprocess.run(["pbcopy"], input=p, text=True)
        print(f"{sid}: {len(p)} chars -> clipboard (from repo manifest, not /tmp)")
        break
else:
    print("shots:", ", ".join(s.get("id","?") for s in d.get("shots", [])))
