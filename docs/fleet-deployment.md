# CI-gated fleet deployment

Each push to `release/v0.0.2` in any of the four public arms runs the shared
`fleet-gate.yml` workflow. The gate builds all canonical binaries, runs Go
build/vet/tests/format checks, the deployment-agent tests, and the cockpit
build/tests. Only success produces the `fleet-release` artifact containing
`fleet-release.json`: four exact commit SHAs plus the triggering repository/SHA.
This does not merge main, create tags, publish Releases, or invoke the installer
against a release tag.

`scripts/fleet-agent.mjs` polls GitHub for successful push runs and accepts an
artifact only when **all four revisions still equal the release branch heads**.
It uses dedicated deployment checkouts under `~/.8/fleet/<mode>/source`, not the
operator's working trees, and rechecks branch heads after building. Failed CI,
missing artifacts, superseded manifests and unknown build attestations never
authorize a deployment. No model calls or provider budget are consumed.

Fresh deployment clones are initialized at the pinned commit in a temporary
directory before being promoted to `source/<arm>`. A failed clone/fetch leaves
no partial checkout to block later polls. Existing checkouts still reject
tracked edits. If an older watcher stranded an empty `--no-checkout` clone,
stop the service and move that directory aside for inspection before restarting;
do not reset a checkout that might contain operator edits.

Prerequisites: Node 22+, authenticated `gh` with read access to Actions artifacts,
Git, Go, npm and the canonical build prerequisites. Colima mode also needs Docker
and an existing `eight-fleet` container with a named `/root/.8` volume.

Install on a native host (Mac or Linux):

```sh
FLEET_ROOT=/absolute/parent/of/four/repos bash 8/scripts/fleet-agent-install.sh native
```

Install the watcher on the Mac that manages the Colima runtime:

```sh
FLEET_HOST=colima-runtime bash 8/scripts/fleet-agent-install.sh colima
```

The installer writes a private `agent.env` and runner under the state directory,
then registers a user LaunchAgent on macOS or a systemd user unit on Linux. The
Linux user manager must remain running for unattended operation (login session
or operator-configured linger). `FLEET_INTERVAL` defaults to 60 seconds, minimum
15. Configure overrides in `agent.env`; restart the service to apply them.

Native mode requires all live repositories clean on `release/v0.0.2` and all
updates to be fast-forwards. It builds/tests private checkouts first, advances
and rebuilds each live arm, builds the web assets, then calls the watchdog-aware
`collector-restart.sh`. A dirty operator tree stops deployment. A build failure
before restart leaves the old processes running; inspect the error and rerun.

Colima mode builds an immutable image, retains the previous stopped container,
and recreates only the runtime with its existing named state volume, loopback
port, resource limits and peer configuration. Browser container addresses are
resolved again. If startup or build attestation fails, it restores the previous
container. `rollback.json` names that last successful rollback candidate. The
state volume is deliberately shared across upgrades; future incompatible state
migrations require their own backup/migration design.

Success requires `/health.build` to match the tested collector SHA. That SHA is
stamped explicitly even when building copied sources without `.git`, and native
peer joins report it in `/peers.extra.build`. The cockpit shows running build
chips: a peer is green only when its fresh build equals the local collector;
mismatches are red; missing/stale attestations stay unknown/stale. The local
chip identifies its own running binary, not fleet-wide convergence.

Every deploy attempt/result is posted to the configured witness `/work`.
`pending.json`, `deployed.json`, `error.json` and the service log retain the
manifest, CI run and live receipt. Even when the manifest is unchanged, the
agent checks the live binary again, so an old receipt cannot hide later drift.

One-shot diagnosis: `FLEET_MODE=colima node 8/scripts/fleet-agent.mjs --once`.
Stop macOS: `launchctl bootout gui/$(id -u)/dev.eight.fleet.colima`.
Stop Linux: `systemctl --user disable --now dev.eight.fleet.native.service`.
The single-host acceptance deployment uses Colima; native-host installation is
separate from that proof and must not be inferred from a green Colima receipt.
