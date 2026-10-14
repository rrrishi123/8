# Colima runtime

From the four sibling release checkouts, with Go, npm dependencies in `8/web`,
Docker and Colima available on the build host:

```sh
bash deploy/build-runtime.sh
bash deploy/run-colima.sh
```

The image contains the canonical binaries from all four arms and the compiled
cockpit. The runtime needs no Go, Node, source checkout, model weights or host
bind mount. `source-manifest.txt` inside `/opt/four-system` records build inputs.
Pilot holds its MCP child ready without a model request; the browser adapter is
available for new seats. Two channel brokers attach the existing Chromium
containers at their browser-level CDP endpoints, preserving profiles and tabs.
Their CDP bridges must already expose port 9222 on the default Docker bridge.
For other layouts, set `BROWSER_ENDPOINTS=name=http://ip:port,...` explicitly.

Open <http://127.0.0.1:7071/> for the cockpit. API routes share that origin.
`PEER_HUB` defaults to the mac collector through `host.lima.internal:7070`;
the collector itself registers and heartbeats as `colima-runtime` every 30s.
This distinct name avoids colliding with the legacy `colima` VM telemetry beat.
The named volume `eight-fleet-state` preserves all of `/root/.8`, including
`eight.db`, work, identities and the manifest, across container replacement.
Keep that volume when upgrading:

```sh
docker stop eight-fleet
docker rm eight-fleet
bash deploy/run-colima.sh
```

The default runtime ceiling is 384 MiB and one CPU (override `FLEET_MEMORY` /
`FLEET_CPUS`); browsers have their own existing limits. A child exit stops the
runtime and Docker restarts it. `docker logs eight-fleet` explains startup
failures. `FLEET_IMAGE`, `FLEET_ARCH`, `FLEET_CONTAINER`, `FLEET_VOLUME`,
`FLEET_PORT`, `PEER_HOST` and `PEER_TOKEN` are optional overrides.
