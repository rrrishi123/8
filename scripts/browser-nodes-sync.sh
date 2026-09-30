#!/usr/bin/env bash
# browser-nodes-sync.sh — the browser-node REGISTRY PRODUCER (#1147). Writes
# $EIGHT_HOME/browser-nodes.json from live docker port maps: every container
# that publishes a devtools port (9222) or runs a browser image with a web
# view (3000, Selkies) becomes a node, plus one host node for the host browser
# the pack drives. Nothing here is bound to a machine: names are discovered,
# ports are whatever docker assigned, labels come from an OPTIONAL host-local
# meta file. The collector reads the file at /nodes (and ALSO enumerates docker
# itself, so a host without this script still gets its nodes) and does the
# node⨝seat join — this file never carries a seat.
#
#   $EIGHT_HOME/browser-nodes.meta.json   optional, host-local, never in the repo:
#     {"profiles": {"<container>": "<label>"}, "host": {"id": "mac-firefox", "engine": "firefox"}}
#
# Usage: scripts/browser-nodes-sync.sh            (EIGHT_HOME defaults to ~/.8)
set -uo pipefail
EIGHT_HOME="${EIGHT_HOME:-$HOME/.8}"
mkdir -p "$EIGHT_HOME"
export EIGHT_HOME
python3 - <<'PY'
import json, os, re, subprocess
home = os.environ['EIGHT_HOME']
meta = {}
try: meta = json.load(open(os.path.join(home, 'browser-nodes.meta.json')))
except Exception: pass
profiles = meta.get('profiles', {}) if isinstance(meta, dict) else {}
hostmeta = meta.get('host', {}) if isinstance(meta, dict) else {}

def docker_ps():
    try:
        out = subprocess.check_output(['docker', 'ps', '--format', '{{.Names}}\t{{.Image}}\t{{.Ports}}'], text=True, stderr=subprocess.DEVNULL, timeout=6)
    except Exception:
        return []
    return [ln.split('\t') for ln in out.splitlines() if ln.strip()]

nodes = []
for row in docker_ps():
    if len(row) < 3: continue
    name, image, ports = row[0], row[1].lower(), row[2]
    m = {}
    for hp, cp in re.findall(r'(\d+)->(\d+)/tcp', ports):
        m.setdefault(cp, hp)
    lname = name.lower()
    browserish = any(k in image for k in ('chrom', 'firefox', 'selenium', 'browser')) or any(k in lname for k in ('chrome', 'firefox', 'browser'))
    cdp, view = m.get('9222'), m.get('3000')
    if not cdp and not (browserish and view): continue
    engine = 'firefox' if ('firefox' in image or 'firefox' in lname) else 'chromium'
    nodes.append({'id': name, 'engine': engine, 'mode': 'container', 'container': name,
                  'profile': profiles.get(name, 'default'),
                  'view_url': f'http://127.0.0.1:{view}/' if view else None,
                  'cdp_url': f'http://127.0.0.1:{cdp}' if cdp else None})

# the host browser: present when the pack published a seat (gecko.json) or the meta names one
host_engine = hostmeta.get('engine', 'firefox')
if hostmeta or os.path.exists(os.path.join(home, 'gecko.json')):
    nodes.append({'id': hostmeta.get('id', 'host-' + host_engine), 'engine': host_engine, 'mode': 'host',
                  'profile': hostmeta.get('profile', 'host'), 'view_url': None, 'cdp_url': None})

out = os.path.join(home, 'browser-nodes.json')
tmp = out + '.tmp'
json.dump({'nodes': nodes}, open(tmp, 'w'), indent=2)
os.replace(tmp, out)
print('wrote', len(nodes), 'nodes ->', out)
for n in nodes: print(' ', n['id'], '->', n['view_url'] or 'host', '| cdp', n['cdp_url'] or '-')
PY
