# Runtime verification — 2026-09-28

Local release inputs: 8 `e2972a7` plus the cockpit-root/entrypoint fixes;
http-mcp `64f4d9d`; pilot `fe1ab2c`; adapters `833f993`.
`bash deploy/build-runtime.sh` produced arm64 image `c4bf63cd89dc`.

- `go test ./...` in collector passed; shell syntax checks passed.
- Runtime `/health`: both `eight-chrome-1` and `eight-chrome-2`.
- `/tabs`: two and three tabs respectively; `/shot` returned JPEGs from both.
- `/` served cockpit HTML and its compiled assets from the collector origin.
- Mac `/peers`: `colima-runtime`, actor `peer-join/colima-runtime`, fresh
  native heartbeat with Linux host resources and tab manifest.
- Pilot's child log: `http-mcp: started; stdio MCP; 5 tools`. No model request.
- Created record #1 via runtime `/work`, removed/recreated only the runtime
  container, and fetched the same record. SQLite integrity check: `ok`.

Resource samples (existing browser pages, no synthetic load): before adding
the runtime the browsers used 1.691 + 1.485 GiB, with 19.62% + 26.69% CPU.
Afterward: 1.700 + 1.488 GiB, 11.27% + 16.29% CPU, plus runtime 16.93 MiB /
0.40% CPU. A second idle runtime sample was 8.887 MiB / 0.14% CPU. Process
RSS summed to about 39 MiB (shared pages counted more than once). Colima
reported 5911 MiB total and 2332 MiB available after startup. Browser CPU
varies with existing page activity; these are point samples, not a controlled
before/after CPU benchmark. Levers 1+2 were already live before these samples.

The runtime is capped at 384 MiB / one CPU, and state is in
`eight-fleet-state`. The older stopped `foursys` container and its anonymous
volume were retained. The legacy `colima` heartbeat still represents the VM;
`colima-runtime` represents the new collector within it.
