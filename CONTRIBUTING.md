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
  `cd ../frontend && bun run lint && bun run build && bun run test:browser`. Install the required
  Chromium build once with `bun run test:browser:install`.
  Browser tests use the freshly built production frontend on loopback port 43117 and refuse to reuse
  an unrelated server. Run `bun run build` after source changes before running browser tests alone.
  `JD_BROWSER_BASE_URL` explicitly selects an externally managed test frontend when needed.
- `.github/workflows/verify.yml` runs the backend, deployment race, frontend and live Docker gates
  for pushes and pull requests on a **self-hosted runner** on the release host (labels `self-hosted,
  linux, x64, just-dashboard`), because the suites need a real Docker daemon, host tools and a shell
  that GitHub's hosted runners do not provide. The runner is a systemd service under `~/actions-runner`
  on that machine, running as `ubuntu`; one job runs at a time. Go and Bun come from `go.mod` and
  `package.json`; dependencies use the frozen Bun lockfile; Playwright's Chromium is installed into the
  runner user's cache. Race packages run serially on a dedicated job so frontend builds do not compete
  with the fleet latency check. Required live fixtures fail CI if skipped or absent. Logs and browser
  failure traces are retained for 30 days, including failed runs. Workflows from outside contributors
  wait for approval before they touch the runner. CI does not replace public TLS, clean-host
  installation, remote-host, architecture or soak acceptance.
- Changes to deployment builders or artifact handling also run the opt-in Docker boundary on a release
  host: `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveC4ArtifactAdapters -count=1 -v`.
  Recipe/detection/default changes also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveDetectedFrameworkBuildAndServing -count=1 -v`.
  Twenty-two fixtures: the locked Node starters, FastAPI, Flask, Django, Streamlit, Gradio, Go, axum,
  Maven, Gradle, ASP.NET Core, Deno, Laravel and plain PHP.
- The blueprint catalogue sweep pulls every deployable definition's pinned image, starts it through the
  real runtime owner with generated secrets and runs its own readiness checks (`JD_BLUEPRINT_ONLY=a,b`
  narrows it; images it pulled are removed again):
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveEveryBlueprintStartsAndAnswersItsOwnChecks -count=1 -v -timeout 2h`.
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
  and the limits the daemon applied.
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
  starts an isolated backend and deploys real nginx, busybox and Redis-blueprint releases through the
  public API.
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
