# Join a peer hub

A newly started collector can register itself with another collector's `/peers`
endpoint without a heartbeat script:

```sh
PEER_HUB=http://hub-host:7070 PEER_HOST=worker-1 collector up
```

`HUB` is an alias; `PEER_HUB` wins when both are set. The value is the hub's base
URL, including any reverse-proxy path prefix, without `/peers`. If `PEER_HOST`
is omitted, the system hostname is used. Choose a distinct name per node.
For an authenticated hub, set `PEER_TOKEN` to its `EIGHT_TOKEN` in the process
environment. The token is sent in `X-8-Token`, never in the URL or boot flags.
Redirects are refused so a token cannot be forwarded to a different host.

The native loop starts after the collector successfully binds its listener. It
sends a heartbeat immediately and then every 30 seconds, carrying host resources
and the current tab manifest, including on browserless hosts. Each HTTP attempt
has a six-second timeout; failures are logged and retried without stopping the
collector. The hub marks a peer stale after 90 seconds without a heartbeat.

`collector up` and `collector watch` pass their environment to new collector
processes. An already running collector is not reconfigured by `up`; apply these
variables to its supervisor and restart it through the host's normal deployment
procedure. Keep the variables in that supervisor's environment for future boots.
Leaving both hub variables unset disables native join.

The existing `scripts/peer-beat.sh` remains useful for synthetic hosts such as a
Colima VM without its own collector, and for its budget summary/custom metrics.
Do not run it and native join with the same peer name at the same hub: their
payloads would overwrite each other. Native join currently sends metrics and
manifest, not a screenshot or budget summary. Verify registration with
`GET http://hub-host:7070/peers`; the entry should have `stale: false`.

This joins a reachable HTTP hub. It does not bypass a sandbox's outbound-network
restrictions or enable remote relay control.
