#!/bin/sh
set -eu

# BROWSER_ENDPOINTS is comma-separated name=http://host:port. Those browsers
# are separate existing containers; the four arms share this small runtime.
pids=""
stop() { trap - TERM INT; [ -z "$pids" ] || kill $pids 2>/dev/null || true; wait || true; }
trap 'stop; exit 0' TERM INT
trap stop EXIT
mkdir -p /root/.8
brokers=""
port=4446
old_ifs=$IFS
IFS=,
for spec in ${BROWSER_ENDPOINTS:-}; do
  IFS=$old_ifs
  name=${spec%%=*}
  endpoint=${spec#*=}
  case "$name" in ''|*[!a-zA-Z0-9_-]*) echo "invalid browser name" >&2; exit 1;; esac
  [ "$name" != "$endpoint" ] || { echo "need name=http://host:port" >&2; exit 1; }
  ws=$(curl -fsS --max-time 5 "$endpoint/json" | jq -er --arg base "$endpoint" \
    '[.[] | select(.type == "page" and .webSocketDebuggerUrl)][0].webSocketDebuggerUrl
     | select(. != null) | sub("^ws://[^/]+"; ($base | sub("^http"; "ws")))')
  channel -ws "$ws" -listen "127.0.0.1:$port" &
  pids="$pids $!"
  brokers="${brokers:+$brokers,}$name=http://127.0.0.1:$port"
  port=$((port + 1))
  IFS=,
done
IFS=$old_ifs

collector -listen :7070 -brokers "$brokers" &
pids="$pids $!"
wire -listen 127.0.0.1:4724 -witness http://127.0.0.1:7070 &
pids="$pids $!"

# Keep the pilot HOST and its MCP child ready without making model calls or
# loading weights. A prompt/provider can be supplied in a separate exec session.
# Holding the FIFO read/write prevents stdin EOF; no polling LLM is started.
rm -f /tmp/pilot-in
mkfifo /tmp/pilot-in
exec 3<>/tmp/pilot-in
pilot -provider ollama -ollama "${OLLAMA_URL:-http://127.0.0.1:11434}" \
  -server /opt/four-system/http-mcp/.bin/http-mcp -no-readline <&3 &
pids="$pids $!"

# Fail the container if any core process exits; Docker's restart policy revives
# the whole chain from the durable volume. No unsupervised half-live runtime.
while sleep 2; do
  for pid in $pids; do
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "four-system child $pid exited" >&2
      exit 1
    fi
  done
done
