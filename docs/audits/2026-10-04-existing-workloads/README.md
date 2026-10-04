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
preserved their identities/settings and had zero failures across 90 continuous HTTP checks. An
intentional first readiness failure restored the original four-container/two-running baseline and
persistent volume data. The failed run correctly finished `failed` before activation; restoration
was checked independently rather than inferred from its status.

A subsequent ordinary Deploy changes succeeded, starting the five-service reviewed recipe. Baseline
rollback succeeded and restored exactly the original four containers' service/replica set, two
running services and the missing fifth service remaining absent. Named-volume data and HTTP survived.
An external one-off container under the same Compose project was untouched throughout. The run passed
in 30.45 seconds. [Sanitized lifecycle evidence](managed-compose-lifecycle.json).

## Real standalone Docker lifecycle

An isolated container with a persistent volume, custom Docker network and aliases was adopted without
changing its ID, PID, start time or settings. A failed first deployment restored the original
container ID and HTTP service. A successful ordinary deployment preserved the image, resource
settings, persistent data, original container name and network aliases. Baseline rollback then
succeeded with the same data and HTTP response. The run passed in 11.07 seconds.
[Sanitized lifecycle evidence](managed-container-lifecycle.json).

## Production boundaries

These tests use owned fixtures and isolated databases. They do not deploy changes to the operator's
production applications. Real native-stack browser evidence, PM2/systemd migration evidence and the
final integrated verification are recorded below when completed.

Recovery fails closed for missing authoritative files, unrepresentable Engine/native-manager
settings, unsafe replica differences, unknown toolchains, unresolved source/private files, meaningful
writable-layer application data, and unmanaged processes with no verified restart authority. Git
history cannot be inferred from a running image. Data/schema changes require application-appropriate
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
  go test ./internal/deploy -run '^TestLiveManaged(Compose|StandaloneContainer)AdoptionAndRollback$' -count=1 -v
```

The opt-in native browser server uses a temporary authenticated database and the real Docker socket;
it never starts the deployment engine or background reconciler. Its default target is the existing
`bet-bot` stack. `ready.json` contains a temporary session and must remain private. The server stops
on its stop file or after fifteen minutes.

```bash
cd backend
JD_IMPORT_BROWSER_EVIDENCE_DIR=/tmp/jd-managed-native-server \
  go test ./internal/api -run '^TestWorkloadImportBrowserEvidenceServer$' -count=1 -v -timeout=20m
```

Build/serve the current frontend against `http://127.0.0.1:44119`, then record the native journey:

```bash
cd frontend
JD_API_URL=http://127.0.0.1:44119 bun run build
bun run start --hostname 127.0.0.1 --port 43151
# In another terminal:
JD_BROWSER_BASE_URL=http://127.0.0.1:43151 \
  JD_IMPORT_NATIVE_READY=/tmp/jd-managed-native-server/ready.json \
  JD_IMPORT_NATIVE_EVIDENCE=/tmp/jd-managed-native-recording \
  bunx playwright test tests/browser/workload-import-native.spec.ts --workers=1
touch /tmp/jd-managed-native-server/stop
```
