#!/usr/bin/env bash
# Attach the existing Colima browsers; never create or replace their profiles.
set -euo pipefail
name="${FLEET_CONTAINER:-eight-fleet}"
image="${FLEET_IMAGE:-eight-fleet:local}"
volume="${FLEET_VOLUME:-eight-fleet-state}"

if docker container inspect "$name" >/dev/null 2>&1; then
  echo "$name already exists. Stop/remove that runtime container before recreating; keep its volume." >&2
  exit 1
fi
endpoints="${BROWSER_ENDPOINTS:-}"
if [ -z "$endpoints" ]; then
  for browser in eight-chrome-1 eight-chrome-2; do
    # The existing CDP bridges bind the default bridge IP, not a second network.
    ip=$(docker inspect --format '{{(index .NetworkSettings.Networks "bridge").IPAddress}}' "$browser")
    [ -n "$ip" ] || { echo "$browser needs a running CDP bridge on port 9222" >&2; exit 1; }
    endpoints="${endpoints:+$endpoints,}$browser=http://$ip:9222"
  done
fi
docker volume create "$volume" >/dev/null
docker run -d --name "$name" --hostname "${PEER_HOST:-colima-runtime}" \
  --restart unless-stopped --memory "${FLEET_MEMORY:-384m}" --cpus "${FLEET_CPUS:-1}" \
  -p "127.0.0.1:${FLEET_PORT:-7071}:7070" -v "$volume:/root/.8" \
  -e PEER_HUB="${PEER_HUB:-http://host.lima.internal:7070}" \
  -e PEER_HOST="${PEER_HOST:-colima-runtime}" -e PEER_TOKEN \
  -e BROWSER_ENDPOINTS="$endpoints" "$image"
