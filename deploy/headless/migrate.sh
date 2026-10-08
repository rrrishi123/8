#!/usr/bin/env bash
# One occupied LSIO Chromium seat -> headless, with an untouched rollback profile.
# First save /manifest through http-mcp; compare restored URLs through the witness.
set -euo pipefail
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
name="${1:?usage: migrate.sh <browser-container> <witness-manifest.json>}"
manifest="${2:?save the pre-migration GET /manifest body through http-mcp first}"
urls=()
while IFS= read -r url; do [ -z "$url" ] || urls+=("$url"); done < <(
  jq -er --arg name "$name" '.tabs[] | select(.session == $name and .status == "live") | .url | select(length > 0)' "$manifest"
)
[ "${#urls[@]}" -gt 0 ] || { echo 'manifest has no live URLs for this seat; refusing occupied-seat migration' >&2; exit 1; }
case "$name" in ''|*[!a-zA-Z0-9_-]*) echo 'invalid container name' >&2; exit 1;; esac
stamp="$(date -u +%Y%m%dT%H%M%SZ)"
backup="${name}-desktop-${stamp}"
volume="${name}-headless-${stamp}"
image="${HEADLESS_IMAGE:-eight-chrome-headless:local}"
inspect="$(docker inspect "$name")"
base="$(jq -r '.[0].Image' <<<"$inspect")"
arch="$(docker image inspect "$base" --format '{{.Architecture}}')"
port="$(jq -er '.[0].NetworkSettings.Ports["9222/tcp"][0].HostPort' <<<"$inspect")"
source_volume="$(jq -er '.[0].Mounts[] | select(.Destination == "/config" and .Type == "volume") | .Name' <<<"$inspect")"
[ "$(jq -r '.[0].State.Running' <<<"$inspect")" = true ] || { echo 'seat must be running' >&2; exit 1; }
[ "$(jq -r '.[0].Config.Entrypoint[0]' <<<"$inspect")" = /init ] || { echo 'seat is not a desktop migration candidate' >&2; exit 1; }
context="$(mktemp -d)"
trap 'rm -rf "$context"' EXIT
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -trimpath -o "$context/cdp-proxy" "$here/proxy.go"
cp "$here/Dockerfile" "$here/start.sh" "$context/"
docker build --build-arg "BROWSER_BASE=$base" -t "$image" "$context"

# Stop completely before copying the profile; retaining both the original volume
# and container makes rollback independent of any writes by headless Chromium.
docker stop --time 45 "$name"
docker rename "$name" "$backup"
docker volume create "$volume" >/dev/null
rollback() {
  echo "migration failed; restoring $backup" >&2
  docker logs --tail 50 "$name" >&2 || true
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker rename "$backup" "$name"
  docker start "$name" >/dev/null
}
trap 'rollback' ERR
docker run --rm --user 0 --entrypoint /bin/sh \
  -v "$source_volume:/source:ro" -v "$volume:/destination" "$base" \
  -c 'cp -a /source/. /destination/'
docker run -d --name "$name" --init --restart unless-stopped \
  --memory "${BROWSER_MEMORY:-2500m}" --cpus "${BROWSER_CPUS:-1.25}" --shm-size 512m \
  --label four-system.browser-mode=headless --label "four-system.rollback=$backup" \
  -p "127.0.0.1:$port:9222" -v "$volume:/config" "$image"
# A started container is not proof of a functioning browser. Roll back if its
# DevTools endpoint cannot serve a browser websocket within the startup window.
ready=0
for _ in {1..60}; do
  # Colima's host-port forwarder can lag a recreated binding. Probe inside the
  # container first; wire verification checks the host forwarding separately.
  if docker exec "$name" curl -fsS --max-time 2 http://127.0.0.1:9222/json/version 2>/dev/null | jq -e '.webSocketDebuggerUrl | length > 0' >/dev/null 2>&1; then
    ready=1; break
  fi
  sleep 1
done
[ "$ready" = 1 ] || { echo 'headless CDP did not become ready' >&2; false; }
# Chromium rejects multiple positional startup URLs in headless mode. Let the
# copied profile restore first, then reconcile missing manifest URLs over CDP's
# HTTP target endpoint (PUT). Keep existing tabs and their authenticated state.
for url in "${urls[@]}"; do
  if ! docker exec "$name" curl -fsS http://127.0.0.1:9222/json/list | jq -e --arg url "$url" 'any(.[]; .type == "page" and .url == $url)' >/dev/null; then
    encoded="$(jq -rn --arg url "$url" '$url|@uri')"
    docker exec "$name" curl -fsS -X PUT "http://127.0.0.1:9222/json/new?$encoded" >/dev/null
  fi
done
trap - ERR
printf 'Headless seat: %s, host CDP port: %s\nRollback container (stopped): %s\nOriginal profile volume (unchanged): %s\nNew profile volume: %s\n' "$name" "$port" "$backup" "$source_volume" "$volume"
echo 'Verify /json/version and /manifest through http-mcp, then reattach/restart the runtime if its browser IP changed.'
echo "Rollback: docker stop $name; docker rename $name ${name}-headless-failed; docker rename $backup $name; docker start $name"
