# Contributing

Thanks for looking. Issues and pull requests are welcome.

## Licence of the project

This project is licensed under the **GNU Affero General Public License v3.0**
(see [`LICENSE`](LICENSE)).

The Affero clause is deliberate. This dashboard is root-equivalent software, and
the licence means anyone who runs a modified version as a network service has to
publish their modifications. You are free to run it, change it and distribute
it; you are not free to take it closed.

## Licence of your contribution

By opening a pull request you agree to the following. There is nothing to sign —
submitting the PR is the agreement.

1. **You wrote it, or you have the right to submit it.** Your contribution is
   your own work, or you have permission from whoever owns it. If your employer
   has rights to work you do, you have their clearance to contribute it.

2. **Your contribution is licensed under AGPL-3.0**, the same terms as the rest
   of the project.

3. **You also grant the project owner (Wayy01) a separate, additional licence**
   to your contribution: perpetual, worldwide, non-exclusive, royalty-free and
   irrevocable, to use, reproduce, modify, distribute and sublicense it under
   *any* terms, including proprietary ones.

You keep the copyright to your work. Point 3 is an additional grant, not a
transfer — you can still do anything you like with your own code.

### Why point 3 exists

It is the only way the project can offer a commercial licence later without
having to track down every past contributor for permission. Practically, it
means paid multi-server features can fund the maintenance of the open source
part, rather than the project stalling the way most single-maintainer
infrastructure tools eventually do.

If that trade is not one you want to make, please open an issue describing the
change instead of a pull request — a good bug report is worth as much, and it
carries no licensing question at all.

## Before you open a pull request

- Run the checks: `cd backend && go build ./... && go vet ./... && go test ./...`, then
  `cd ../frontend && bun run lint && bun test src && bun run build && bun run test:browser`. Install the required
  Chromium build once with `bun run test:browser:install`.
  Browser tests reuse a running production frontend on loopback port 43117 locally. Start one from
  the worktree under test and rebuild/restart it after source changes. `JD_BROWSER_BASE_URL` selects
  an explicitly managed frontend on another port when worktrees run alongside one another.
  For the inner loop, also run `bunx tsc --noEmit` and the affected browser spec plus
  `tests/browser/design-system.spec.ts` for UI changes.
- **`bun run build` is the final frontend type-check gate.** `frontend/Dockerfile`
  sets `JD_IMAGE_BUILD=1`, which tells `next.config.ts` to skip the type-check pass and the
  prerender source maps. That is deliberate: install and update are both
  `docker compose up --build`, so the image build runs on the operator's own server, where
  repeating a check that already passed here buys nothing and costs roughly a gigabyte — tsc runs
  in a second Node process while the compiler still holds its graph, which is what made a 2 GB
  machine fail the install intermittently. The emitted application is byte-for-byte identical
  either way. So a type error you do not catch with `bun run build` will not be caught anywhere
  later; it will ship.
- `.github/workflows/verify.yml` runs for every push, and for pull requests from forks. A first job
  reads which paths the push changed and starts only the gates they reach: `backend/` starts the
  backend and race jobs, `frontend/` the frontend and browser jobs, and the deployment packages
  (`internal/{deploy,api,proxysvc,dockerx,store,backups}`, `go.mod`) the live Docker job; a change to
  the workflow or its scripts starts everything, as does a new branch or a manual run, and a
  documentation-only push runs nothing past that first job. The backend, race, frontend and browser
  jobs run on GitHub's hosted runners, in parallel — the repository is public, so they cost nothing —
  and the two long suites are sharded: the race gate is nine jobs (`./internal/api` in five,
  `./internal/deploy` in three, the rest in one, split by `scripts/go-test-shard.sh`) and the browser
  suite six (`playwright test --shard`). The latency budgets are asserted in the plain test run and
  skipped under the race detector, which multiplies a SQLite read ten- to twenty-five-fold and so
  measures itself and the runner's load rather than the read. Go and Bun come from `go.mod` and
  `package.json`; dependencies use the frozen Bun lockfile, and the module, Bun, Playwright and Next
  caches are restored between runs.
- The live Docker fixtures need a real Docker daemon, so they run on a **self-hosted runner** on the
  release host (labels `self-hosted, linux, x64, just-dashboard`), a systemd service under
  `~/actions-runner` running as `ubuntu`, one job at a time. It used to take every job, one after
  another — about fifty-five minutes a push, on the machine that serves the dashboard — and now takes
  only this one. Required live fixtures fail CI if skipped or absent. Logs and browser failure traces
  are retained for 30 days, including failed runs. The live job ends by pruning the BuildKit cache its
  fixtures fill back to two gigabytes, because the runner shares the host's Docker daemon and a few
  unpruned runs fill the disk. Workflows from outside contributors wait for approval before they
  touch the runner. CI does not replace public TLS, clean-host installation, remote-host,
  architecture or soak acceptance.
- Changes to deployment builders or artifact handling also run the opt-in Docker boundary on a release
  host: `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveC4ArtifactAdapters -count=1 -v`.
  Recipe/detection/default changes also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveDetectedFrameworkBuildAndServing -count=1 -v`.
  Changes to build-failure diagnosis (the BuildKit reader in `dockerx`, the collector or the signature
  table) also run `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveBuildFailureIsNamedFromBuildKit -count=1 -v`,
  which builds an npm project whose lockfile no longer matches package.json and checks the cause is
  named from buildx's own output; it creates no image.
- Git workspace changes also exercise `internal/gitx`, `internal/ghx` and `internal/forgex`, including
  race checks, plus `git-features.spec.ts`, `git-ui.spec.ts` and `design-system.spec.ts`. The LFS lifecycle
  test needs `git-lfs` on PATH (it is included in the backend image); it touches only a temporary
  repository. Provider fixture tests do not publish live comments or reviews. See
  [the Git workspace contract](docs/internal/backend/git-workspace-expansion.md) for limits and setup.
  Twenty-four fixtures: the locked Node starters (installed by Bun, pnpm and Yarn 1), FastAPI, Flask,
  Django, Streamlit, Gradio, Go, axum, Maven, Gradle, ASP.NET Core, Deno, Laravel and plain PHP.
- The blueprint catalogue sweep pulls every deployable definition's pinned image, starts it through the
  real runtime owner with generated secrets and runs its own readiness checks (`JD_BLUEPRINT_ONLY=a,b`
  narrows it; images it pulled are removed again):
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveEveryBlueprintStartsAndAnswersItsOwnChecks -count=1 -v -timeout 2h`.
  Each sweep uses unique volume names. Startup/readiness is separate from first-use account setup;
  the [template matrix](docs/audits/2026-09-22-deploy-new/template-matrix.md) records which templates
  generate credentials, require setup, have no application login, or remain unavailable.
- Object-storage backups against a real S3 API (MinIO in a container: target test, upload, retention,
  restore): `JD_DEPLOY_LIVE=1 go test ./internal/backups -run TestLiveObjectStorageBackupUploadsPrunesAndRestores -count=1 -v`.
- The public-certificate journey against a real ACME authority (an isolated Caddy and a Pebble that
  validates nothing, on one Docker network):
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyIssuesThroughAConfiguredACMEDirectory -count=1 -v`.
  This uses locked application fixtures and checks served build values, private install credentials,
  SvelteKit adapters, HTML/Containerfile defaults and Go command/version behavior, plus the catalogue's
  own starters: Astro, Nuxt, React Router, FastAPI (unpinned requirements, server auto-installed), a
  Flask factory on a bare pyproject, Django with its migrations, axum, a Maven jar, ASP.NET Core and
  Deno. It pulls the build images and package registries over the network and takes several minutes.
- The daemon-wide prune integration tests are separate: set `JD_DOCKER_PRUNE_LIVE=1` and `DOCKER_HOST`
  to an isolated disposable Docker daemon before running
  `go test ./internal/dockerx -run 'TestLive(PruneAllActuallyDeletes|BuildCachePruneRoundTrips)' -count=1 -v`. A normal `go test ./...`
  does not authorize pruning the Docker host it happens to find.
- Changes to runtime activation, checks, graceful shutdown or Compose release ownership also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveC5ActivationAdapters -count=1 -v` on a Docker
  and Buildx release host. Changes to runtime resource limits or failed-gate diagnostics also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveRuntimeDiagnoserReadsExitedContainer -count=1 -v`,
  which starts a real container with limits, lets it exit non-zero and checks the captured state, output
  and the limits the daemon applied. Changes to release tasks that run in the release image also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveReleaseTaskRunsInTheReleaseImage -count=1 -v`,
  which builds a tiny image and runs tasks in it for their output, exit codes, timeout and cleanup,
  including a container a stopped dashboard left under a task's name.
- `python3 scripts/e2e-deployments.py` is the real-backend acceptance lane for deployments: it builds the
  backend, starts it on loopback with a fresh data directory, and drives the public API through a signed
  webhook channel, an nginx image deployment with resource limits, a busybox image that exits before it
  listens, and a restart. It needs Docker and Go and touches only the containers it creates; run it after
  changes to the engine, run observers, notifications, runtime limits or health-gate diagnostics.
- Notification channels (Discord, Slack, Telegram, e-mail, signed webhook) and GitHub commit statuses are
  covered by component tests with fake providers; a real provider or GitHub post is verified manually
  through **Send test** and a deployment of a GitHub-sourced project, and the pull request must say which
  providers were exercised.
- Changes to deployment variables, feature links, backup gates or managed-resource lifecycle also run
  `go test -race ./internal/deploy ./internal/api ./internal/proxysvc ./internal/backups ./internal/store -count=1`;
  the browser gate covers the project overview, build transcript and focused settings, including
  variables, domains, storage, dependencies, automation and lifecycle.
- Preview changes also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run 'TestLive(PreviewStorageCredentialsNetworkAndCleanup|LegacyPreviewQuarantinePreservesProduction)$' -count=1 -v`.
  These fixtures cover new-preview isolation and production-preserving quarantine of older previews.
  Compose backup coverage changes run
  `JD_DEPLOY_LIVE=1 go test ./internal/dockerx -run TestLiveDeploymentComposeStorage -count=1 -v`.
- Changes to database provisioning or deployment connection URLs also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveDeploymentDatabaseConnection -count=1 -v`
  on a Docker host. It exercises all five quick-setup engines from separate application containers,
  replaces databases at a different IP, reconnects the same clients using their original URLs,
  verifies ownership/removal, and cleans up its own containers, volumes and networks. Compose network
  integration also runs `JD_DEPLOY_LIVE=1 go test ./internal/dockerx -run TestLiveComposeDatabaseNetworkMerge -count=1 -v`.
- Changes to the ingress request record — the rendered `log` block, `accesslog.Store`, or either
  reader in `proxysvc/deployment_access_log.go` — also run
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveCaddyAccessLogReader -count=1 -v`, which
  drives the container-side script against `caddy:2-alpine`: a roll under the reader, the rolled
  generation found by inode with its tail recovered, the new live file, a vanished inode reading as
  absent. It starts one idle container and removes it.
- Changes to the deployment lifecycle feed — the owner filter, the audit correlation, the kinds, or
  either endpoint in `handlers_deploy_requests.go` — also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveDeploymentEventsAreReadFromDockerAndNamedWithTheirCause -count=1 -v`.
  It labels a real container and a real network the way the runtime owner labels what it creates,
  lets the container exit 137, and reads both back through the real routes and the real socket. The
  feed rests on Docker putting an object's labels in an event's actor attributes, and nothing in this
  code would notice if that stopped being true — it is already false for networks, which is what the
  fixture exists to pin. It creates two containers and one network and removes them.
- Changes to deployment routes, activation cutover or runtime ownership also run
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run '^TestLiveCutover(TrafficContinuity|SurvivesProxyLoss)$' -count=1 -v`
  and `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveRuntimeLossKeepsExactlyOneReleaseLive -count=1 -v`.
  The first drives sustained fast, long-streamed and WebSocket traffic through 100 real nginx
  activations and fails on a single lost request; set `JD_CUTOVER_TRANSITIONS` lower only for local
  iteration, never for an acceptance run, and `JD_CUTOVER_EVIDENCE` to retain the measured counts.
  The second kills real containers in the blue/green window and at a live release, and requires that
  exactly one release is running afterwards.
- Changes to the engine, its reconciler or run persistence also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveBackendKillAtEveryStepLeavesOneRecoveredRun -count=1 -v`.
  It builds `internal/deploy/testdata/engine-host`, SIGKILLs that real second process inside every
  step in turn, and requires that the restarted engine ends the run with `restart_evidence_missing`,
  leaves no claimable run behind, and still has the killed process's step output.
- Backup consistency or restore-verification changes also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveSQLiteApplicationRecoveryVerification -count=1 -v`.
  This uses a real application image and live SQLite snapshot, destroys only the fixture's table,
  verifies the archived canary through the application, rejects a wrong schema and checks cleanup.
- Native database dump changes also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveBackupDumpsAndRestoresAPostgresDatabase -count=1 -v`.
  It provisions a real PostgreSQL container, dumps a canary row through a backup job, deletes the row
  live, restores the dump into a drill database over the typed-confirmation route and checks the live
  database was not touched.
- Blueprint, planning or activation changes also run `python3 scripts/e2e-deployments.py`, which
  starts an isolated backend and deploys real nginx, busybox and PostgreSQL-blueprint releases through
  the public API, including generated-password authentication over TCP and paused backup automation.
- Keep the security posture intact. The network allowlist runs before
  authentication, enrolled accounts always require their second factor, every destructive route sits behind
  the destructive capability with an audit entry, and the rare irreversible ones
  require a typed confirmation phrase enforced server-side. A change that
  weakens any of those needs to say so explicitly in the PR description.
- Before putting a typed confirmation on a new route, read invariant 3 in
  `docs/internal/security/invariants.md`. The test is frequency, not severity: everything behind
  `s.destructive` is dangerous, and adding a phrase to something done several
  times a sitting is what teaches operators to type phrases without reading
  them.
- Never edit `CHANGELOG.md` by hand — it is generated from
  `backend/internal/selfupdate/changelog.json`, which is also the file every
  install in the world reads to find out whether it is behind. If your change
  is worth a release, add the entry there and run `scripts/release.sh <version>`;
  it bumps the version everywhere it appears and regenerates the markdown. The
  Go test run fails if the two ever disagree, in either direction.
- Match the surrounding code. Comments explain *why*, not *what*.

## Security issues

Please do not open a public issue for a vulnerability. Report it privately
through GitHub's **Security → Report a vulnerability** on this repository.

## Running the database tests against real engines

`go test ./...` passes with nothing installed: the database integration tests
skip when they cannot reach a server, because a suite that fails for want of a
daemon is a suite people learn to ignore.

They are worth running for real before touching `internal/dbx`, though. The
unit tests prove the generated SQL is the SQL intended; only a live server
proves it is SQL that server accepts, and the catalogue queries are exactly
where that gap bites — every engine spells its metadata differently.

Each engine reads a DSN from an environment variable, defaulting to a local
instance on the standard port:

| Variable | Default |
| --- | --- |
| `JD_TEST_POSTGRES_DSN` | `postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable` |
| `JD_TEST_MYSQL_DSN` | `jdtest:jdtest@tcp(127.0.0.1:3306)/jdtest` |
| `JD_TEST_MSSQL_DSN` | `sqlserver://sa:…@127.0.0.1:1433?database=master` |
| `JD_TEST_ORACLE_DSN` | `oracle://jdtest:jdtest@127.0.0.1:1521/FREEPDB1` |
| `JD_TEST_CLICKHOUSE_DSN` | `clickhouse://default@127.0.0.1:9000/default` |
| `JD_TEST_MONGO_DSN` | `mongodb://127.0.0.1:27017/jdtest` |
| `JD_TEST_REDIS_DSN` | `redis://127.0.0.1:6379/0` |

The quickest way to get all of them is containers:

```bash
docker run -d -p 5432:5432 -e POSTGRES_USER=jdtest -e POSTGRES_PASSWORD=jdtest -e POSTGRES_DB=jdtest postgres:16
docker run -d -p 3306:3306 -e MARIADB_ROOT_PASSWORD=jdtest -e MARIADB_DATABASE=jdtest -e MARIADB_USER=jdtest -e MARIADB_PASSWORD=jdtest mariadb:11
docker run -d -p 1433:1433 -e ACCEPT_EULA=Y -e MSSQL_SA_PASSWORD='JdTest#2024pw' mcr.microsoft.com/mssql/server:2022-latest
docker run -d -p 9000:9000 clickhouse/clickhouse-server:latest
docker run -d -p 27017:27017 mongo:8
docker run -d -p 6379:6379 redis:7
```

Then `go test ./internal/dbx/ ./internal/api/ -run Live -v` and watch which
engines report rather than skip. SQLite needs nothing — it is embedded.

Oracle has unit coverage for statement guards, SQL rendering and adapter behavior. Live server
coverage requires an available Oracle instance: set `JD_TEST_ORACLE_DSN` to run the existing Oracle
fixture alongside the others. If no server was used, identify that validation limit in the pull request;
unit results do not establish that the generated statements work against an Oracle server.
