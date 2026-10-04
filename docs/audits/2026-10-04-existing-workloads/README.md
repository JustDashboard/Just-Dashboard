# Managed existing-workload adoption acceptance

Date: 2026-10-04. Task branch: `feature/adopt-existing-deployments`, targeting `patch/0.7.1`.

## Implemented behavior

**Import existing → Review migration → Configure → Variables → Review → Adopt deployment** creates a
regular deployment with a pinned live release and the original runtime. Settings, logs, release
history, variables and applicable deployment features use the ordinary deployment pages. Import itself
queues no deployment and does not stop, restart, recreate, relabel or rewrite the current application.
Review changes remain pending; **Deploy changes** applies them, while **Redeploy live release** uses
only the frozen live baseline. Source, image, manager, storage and account fidelity must be recoverable
before adoption is permitted. See [the contract and upstream research](../../internal/deployments/existing-workloads.md).

The previous implementation only registered external observations. Its
[historical evidence](observation-only-history.md) is retained explicitly as history and does not
prove this managed behavior.

## Real bet-bot adoption and browser recording

The existing operator stack was discovered and adopted through authenticated public routes into an
isolated temporary dashboard database. The run passed in 44.4 seconds (45.3 seconds including browser
setup). Its two existing running containers became the initial managed live release. The two declared
services without containers or available images, `eurobet-doubles-tracker` and
`eurobet-high-market-tracker`, were named and acknowledged as `existing_services` exclusions.

Full original container IDs, PIDs, start times, restart counts and configuration/mount/network digests
matched before and after. No runtime actions were invoked and the installed dashboard database was
unchanged. The original containers already reported unhealthy health checks; adoption did not invent
healthy application status. Missing-image recovery used read-only exports and removed only the proof
server's newly created cache images at cleanup.

The recording opens the ordinary deployment shell, Runtime and General settings. Displayed numeric
CPU and memory matched a fresh real Docker stats frame for the selected Runtime service. The normal
authenticated one-line Docker log tail was read successfully, with all output discarded rather than
published. Captured Compose settings were loaded and editable with private values represented only
by variable references. These checks prove nonmutating adoption of the real stack; destructive
migration and rollback are separately exercised on the owned fixtures below.

- [Discovery](managed-native-discovery-1280.png), [configuration](managed-native-configuration-1280.png),
  [adoption review](managed-native-adoption-review-1280.png).
- [Managed project](managed-native-project-1280.png), [live Runtime](managed-native-runtime-1280.png),
  [editable Compose settings](managed-native-settings-1280.png).
- [Real stack recording](managed-native-import.webm), [sanitized continuity evidence](managed-native-continuity.json).

## Real Docker API continuity

An authenticated API fixture created a unique four-container Compose project with two containers
running and two stopped, an HTTP listener, private environment, bind mount, restart policy and named
volumes. It recovered and adopted through the public routes into a temporary database. The test used
the real Docker socket and removed only its own resources.

The run passed in 17.03 seconds. Container IDs, PIDs, start times, restart counts, ports, Config,
HostConfig, mounts and networks were unchanged. The original Compose file and persistent data matched.
105 HTTP requests during adoption had zero failures. One completed migration history entry and a
pinned live release were created, environment values were copied and sealed, and zero execution runs
were enqueued. [Sanitized evidence](managed-api-continuity.json).

## Real managed Compose lifecycle

The fixture declared five services, with four existing containers and two initially running. Adoption
preserved their identities/settings and had zero failures across 137 continuous HTTP checks. An
intentional first readiness failure restored the original four-container/two-running baseline and
persistent volume data. The failed run correctly finished `failed` before activation; restoration
was checked independently rather than inferred from its status.

A subsequent ordinary Deploy changes succeeded, starting the five-service reviewed recipe. Baseline
rollback succeeded and restored exactly the original four containers' service/replica set, two
running services and the missing fifth service remaining absent. Named-volume data and HTTP survived.
An external one-off container under the same Compose project was untouched throughout. The run passed
in 42.31 seconds after the final configuration, population and cleanup guards.
[Sanitized lifecycle evidence](managed-compose-lifecycle.json).

## Real existing-services Compose lifecycle

A separate real fixture declared five services, with four existing containers and two running, while
the fifth service's image was deliberately unavailable. Explicit `existing_services` recovery listed
and acknowledged that exclusion. Adoption preserved all existing stopped/running containers and had
120 HTTP samples with zero failures. A failed first deployment restored the original runtime;
a successful Deploy started only the four reviewed services, and baseline rollback restored the
original four-container/two-running set. Persistent volume data and an external one-off were retained.
The unavailable service was never created. The final run passed in 42.51 seconds with the final configuration and
included-service population guards.
[Sanitized evidence](managed-scoped-compose-lifecycle.json).

## Real standalone Docker lifecycle

An isolated container with a persistent volume, custom Docker network and aliases was adopted without
changing its ID, PID, start time or settings. A failed first deployment restored the original
container ID and HTTP service. A successful ordinary deployment preserved the image, resource
settings, persistent data, original container name and network aliases. Baseline rollback then
succeeded with the same data and HTTP response. The run passed in 18.28 seconds.
[Sanitized lifecycle evidence](managed-container-lifecycle.json).

## Real n8n data lifecycle

The actual `n8nio/n8n:2.39.10` fixture passed in 163.99 seconds with a saved workflow and encrypted
credential. Its own credential decryption command verified the original value before adoption, after
normal Deploy and after baseline rollback. The original named SQLite volume, encryption key, image
user and memory/CPU limits remained intact. Adoption preserved ID/PID/start/settings and independently
sealed original/desired inputs. Owned fixture cleanup was verified.
[Detailed record and reproduction](n8n-lifecycle.md), [sanitized evidence](managed-n8n-lifecycle.json).

## Real native lifecycle

The PM2 fixture used an actual existing daemon with a direct Node service and the normal Node recipe;
the systemd fixture used an owned persistent unit and its real Dockerfile. Both imported without
restarting the app, exposed native logs, built real Docker candidates, intentionally failed the first
readiness gate and restored the native service, then migrated successfully through the deployment
engine. Docker UID, working directory, private/empty environment and linked persistent data were
verified. The absolute `APP_DATA_DIR` value was translated to `/app/data` for Docker while the original
private environment remained separately snapshotted. Baseline rollback returned control to the
original manager and preserved data. The final PM2 run passed in 49.56 seconds; persistent systemd in 97.78 seconds.
[PM2 evidence](managed-pm2-lifecycle.json), [systemd evidence](managed-systemd-lifecycle.json).

## Real deleted-image lifecycle

A separate standalone fixture deleted its original local image while leaving the container running.
Recovery exported its filesystem without pause/stop/exec, verified its platform/configuration and
created a private, reusable recovery image. Adoption retained the original ID/PID/start/settings;
repeat capture used the same cached image. An intentionally failed first deployment restored the
original container ID, and a successful managed deployment and baseline rollback preserved its
persistent volume, name and network alias. The final run passed in 27.82 seconds, including the
staging-space reservation checks.
[Sanitized evidence](managed-deleted-image-lifecycle.json).

## Interface evidence

These screenshots and the recording use explicit mocked API fixtures. They demonstrate the current
managed import flow and normal source/runtime/settings controls; the real lifecycle assertions above
establish continuity, cutover and rollback behavior separately. The final screenshot run passed all
16 selected browser cases against the rebuilt production frontend, including delayed preflight and
canonicalized-plan acknowledgement checks.

- [Discovery](managed-discovery.png), [recovery](managed-recovery.png),
  [migration review](managed-migration.png), [private variables](managed-private-variables.png).
- [Editable Compose source](managed-compose-source.png), [service mount review](managed-compose-review.png).
- [Reviewed service exclusions](managed-compose-exclusions.png).
- [PM2 runtime controls](managed-pm2-runtime.png), [systemd runtime controls](managed-systemd-runtime.png).
- [Recorded managed import journey](managed-ui.webm).

## Production boundaries

These tests use owned fixtures and isolated databases. They do not deploy changes to the operator's
production applications. The real bet-bot recording above used read-only recovery and metadata adoption; migration and rollback tests used only uniquely owned fixtures.

Recovery fails closed for missing authoritative files, unrepresentable Engine/native-manager
settings, unsafe replica differences, unknown toolchains, unresolved source/private files, meaningful
writable-layer application data, and unmanaged processes with no verified restart authority. Git
history cannot be inferred from a running image. External proxy routes, certificates, schedulers and
integrations retain their original ownership and require a reviewed handoff; managed Domains are
configured separately. Data/schema changes require application-appropriate
backup/restore; image/configuration rollback does not reverse them. The coverage matrix and manager
limits are in [existing-workloads.md](../../internal/deployments/existing-workloads.md).

## Reproduction

From the task worktree, serve a fresh production frontend and use its loopback URL for the selective
checks. Never run the entire browser suite or `go test ./...`.

```bash
JD_BROWSER_BASE_URL=http://127.0.0.1:43151 scripts/test-changed.sh patch/0.7.1
```

The Docker fixtures require a locally available `caddy:2-alpine` image and a working Docker socket:

```bash
cd backend
JD_WORKLOAD_IMPORT_LIVE=1 JD_WORKLOAD_IMPORT_EVIDENCE_DIR=/tmp/jd-managed-api-proof \
  go test ./internal/api -run '^TestLiveWorkloadAdoptionKeepsFourContainerStackAndHTTPServiceUnchanged$' -count=1 -v
JD_DOCKER_ADOPTION_LIVE=1 JD_ADOPTION_EVIDENCE_DIR=/tmp/jd-managed-lifecycle-proof \
  go test ./internal/deploy -run '^TestLiveManaged(Compose|ScopedCompose|StandaloneContainer|DeletedImageContainer)AdoptionAndRollback$' -count=1 -v
```

The opt-in native browser server uses a temporary authenticated database and the real Docker socket;
it never starts the deployment engine or background reconciler. Its default target is the existing
`bet-bot` stack. `ready.json` contains a temporary session and must remain private. The server stops
on its stop file or after fifteen minutes.
The browser proof compares full container IDs, PIDs, running state, status, start times, restart
counts and configuration digests before and after adoption. It checks the normal runtime/settings
links and deployment controls without invoking any runtime action.
The isolated server rebinds its read-only Docker consumers and the advisory deployment checker to
the same real source, native-runtime and preflight adapters. Its startup requires actual Docker and
Compose availability, and the browser verifies the Overview check's corresponding pass findings
before recording the project. It also requires a pass for the adopted stack's exact runtime
reservation and refuses any `runtime_unavailable` finding. This is an adapter-availability check, not a claim that the imported
application is healthy. The router permits same-origin HTTP WebSocket upgrades through loopback.
The browser waits for real CPU/memory stats frames and the
editable captured Compose source before recording Runtime and General settings. It never starts the
deployment engine or background reconciler, and it does not manufacture metrics or history.
Runtime waits on the selected service's newly opened socket, then checks that its displayed CPU and
memory match a ready frame. Reduced motion makes the actual values and loaded forms appear immediately
for the Overview/Runtime/settings recording instead of capturing intermediate fades or number springs.
It also opens the ordinary authenticated Docker log stream with a one-line tail and discards the
contents, recording only successful stream metadata as a boolean in the sanitized continuity JSON.

```bash
cd backend
JD_IMPORT_BROWSER_EVIDENCE_DIR=/tmp/jd-managed-native-server \
  go test ./internal/api -run '^TestWorkloadImportBrowserEvidenceServer$' -count=1 -v -timeout=20m
```

Build/serve the current frontend, then put a loopback-only Caddy router in front of the UI and API,
as the installed Compose stack does. Next's development rewrite has a 30-second upstream timeout;
read-only image exports can exceed it and must reach the Go API directly through Caddy.
The reproduction config is [proof.Caddyfile](proof.Caddyfile).

```bash
cd frontend
JD_API_URL=http://127.0.0.1:44119 bun run build
bun run start --hostname 127.0.0.1 --port 43151
# In another terminal, from the repository root:
docker run --rm --name jd-managed-adoption-proof-router --network host \
  --tmpfs /data --tmpfs /config \
  --mount type=bind,src="$PWD/docs/audits/2026-10-04-existing-workloads/proof.Caddyfile",dst=/etc/caddy/Caddyfile,readonly \
  caddy:2-alpine caddy run --config /etc/caddy/Caddyfile
# In a third terminal, from the repository root:
cd frontend
# Record against Caddy:
JD_BROWSER_BASE_URL=http://127.0.0.1:43152 \
  JD_IMPORT_NATIVE_READY=/tmp/jd-managed-native-server/ready.json \
  JD_IMPORT_NATIVE_EVIDENCE=/tmp/jd-managed-native-recording \
  bunx playwright test tests/browser/workload-import-native.spec.ts --workers=1
touch /tmp/jd-managed-native-server/stop
```
