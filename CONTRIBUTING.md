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

## Reporting bugs and requesting features

Use the [issue chooser](https://github.com/JustDashboard/Just-Dashboard/issues/new/choose) to open a
bug report or feature request. Search existing issues first and keep each report focused on one problem.
For bugs, include reproduction steps, expected and actual behavior, the dashboard version, and your
Linux host, Docker/Compose, and browser details. Logs and screenshots help, but remove credentials and
private information; never attach your `.env` file or the bootstrap admin password.

Feature requests should explain the task you want to complete, the proposed improvement, and any
workaround. Report vulnerabilities [privately](https://github.com/JustDashboard/Just-Dashboard/security/advisories/new),
as described under [Security issues](#security-issues).

## Before you open a pull request

Keep the PR focused and use the template to explain what changed, why, and how you verified it. Link
related issues and include before/after screenshots for visual changes or a recording for interaction
changes. Record validation limits and whether documentation needed updating; submitting the PR agrees
to the contribution terms above, including the additional licence grant to the project owner.

- Run the checks with `scripts/test-changed.sh`. It runs only what your diff can reach: Prettier and
  ESLint on the changed frontend files, `tsc --noEmit`, `bun test src`, `go build`/`go vet` and the
  Go tests beside each changed file, and the browser specs that open a page the change renders. Do not
  run the whole browser suite or `go test ./...` locally. Install the required Chromium build once
  with `bun run test:browser:install`.
  Browser tests reuse a running production frontend on loopback port 43117 locally. Start one from
  the worktree under test and rebuild/restart it after source changes. `JD_BROWSER_BASE_URL` selects
  an explicitly managed frontend on another port when worktrees run alongside one another.
- Local server advisor changes also run `scripts/test-server-advisor-linux.sh`, which builds static
  Go test binaries and checks native filesystem/procfs behavior in Ubuntu 22.04/24.04, Debian 12,
  Alpine 3.20 and Fedora 42 containers. Containers use no network during tests and share the host
  kernel; this checks userlands, not every kernel. For live Docker resource/policy acceptance, first
  make `alpine:3.20` available locally, then run from `backend/`:
  `JD_ADVISOR_DOCKER_LIVE=1 go test ./internal/dockerx -run '^TestLiveAdvisor' -count=1 -v`.
  It owns and removes its temporary fixtures and does not modify existing workloads. The Linux
  harness also verifies `/host` against host PID 1 using a read-only host-root bind and an isolated
  fixture. It never cleans host files during that check; restricted roots remain enforced.
- Changes to `internal/netx` that apply devices, routes, the gateway table or shaping also run, from
  `backend/`, `JD_NETNS_LIVE=1 go test -race ./internal/netx -run Live -count=1`. It needs root or
  passwordless sudo and does everything inside throwaway network namespaces it removes, never on the
  host's own interfaces, firewall or tailscaled.
- Independent network recovery also has a real systemd timer fixture. On a host with a reachable
  systemd manager, add `JD_SYSTEMD_RECOVERY_LIVE=1` to the network namespace command above. It builds
  the standalone helper and uses uniquely named transient timers with `NetworkNamespacePath` for
  disposable namespaces, then removes those exact fixtures. It does not install persistent host
  units or reboot the host; timer dispatch and cold-runtime reconstruction are separate from actual
  reboot acceptance.
- Installer and terminal-admin changes also run `python3 scripts/test_manage.py` and
  `bash -n install.sh scripts/manage.sh scripts/create-user.sh scripts/reset-password.sh`. The fixtures
  use fake host commands and temporary state rather than modifying an installed dashboard.
- **`bun run build` is the final frontend type-check gate.** `frontend/Dockerfile`
  sets `JD_IMAGE_BUILD=1`, which tells `next.config.ts` to skip the type-check pass and the
  prerender source maps. That is deliberate: install and update are both
  `docker compose up --build`, so the image build runs on the operator's own server, where
  repeating a check that already passed here buys nothing and costs roughly a gigabyte — tsc runs
  in a second Node process while the compiler still holds its graph, which is what made a 2 GB
  machine fail the install intermittently. The emitted application is byte-for-byte identical
  either way. So a type error you do not catch with `bun run build` will not be caught anywhere
  later; it will ship.
- `.github/workflows/verify.yml` runs for every pull request, as it would merge, and for every push to
  `main` or a `patch/*` branch. A first job, `plan`, reads the change and starts only what it reaches;
  `scripts/ci-plan.py` is the picking, and `python3 scripts/test_ci_plan.py` checks it. A Go package is
  checked when it changed or imports one that did, so a change to one handler does not run the
  deployment suite and a change to the store runs everything above it. `backend` builds, vets, and tests
  those packages plainly; the six of the race gate (`internal/{api,deploy,proxysvc,backups,store,dockerx}`)
  run under the race detector instead, in at most two jobs — `scripts/go-test-race.sh` splits
  `./internal/api` across four processes to fill the runner's cores — and only their latency budgets
  run plainly, because the detector multiplies a SQLite read ten- to twenty-five-fold and so measures
  itself rather than the read. A package whose tests read the frontend's half of a contract
  (`internal/version`, `internal/deploy`) runs when that file changes. `frontend` lints the changed
  files (the whole tree when the rules or the dependencies change), type-checks, and runs the unit
  tests. `browser` runs the specs `scripts/test-changed.sh` would pick, except that a change reaching
  the dashboard's shell runs the whole suite rather than the two specs that open every page; the specs
  are dealt into up to eight jobs of about two hundred tests each, in name order, so that a slow
  section is spread over the jobs. A change to the workflow or its scripts runs everything,
  as does a manual run, and a documentation-only change runs nothing past `plan`.
  GitHub runs twenty jobs of a public repository's at once across every branch, so the suites are
  split only as far as a runner's four cores are full: more jobs than that queue behind each other and
  behind every other pull request. Go and Bun come from `go.mod` and `package.json`, and dependencies
  use the frozen Bun lockfile. The module, build, Bun, Playwright, Next and `tsc` caches are saved only
  by runs on `main` and `patch/*` and restored by pull requests into them, because a run can read
  the caches of its base branch and never those of another task branch.
  Real-nginx tests that use `http2 on;` probe the installed nginx first and skip if it lacks that
  directive; the other nginx tests still run.
- The live Docker fixtures need a real Docker daemon, which GitHub's hosted Ubuntu runners provide, so
  they run there like the other jobs, each job on a fresh daemon, and only when one of the six
  deployment packages (those of the race gate) or `go.mod` changed: `live (fixtures)` runs the
  artifact, activation, preview, runtime-fault, database, Compose and cutover fixtures of whichever of
  `internal/{deploy,api,dockerx,proxysvc}` the change reaches, and a change reaching `internal/deploy`
  also builds the framework fixtures in four jobs. The lists are
  in `scripts/ci-plan.py`; the last framework job runs every framework the others do not name, so a
  new one is built without being added there. They used to run on a self-hosted runner on the release
  host, which put the fixtures' builds on the daemon that serves the dashboard, needed its BuildKit
  cache pruned after every run, and left every run waiting whenever that service was down. Each live
  job has a 45-minute test timeout. Required live fixtures fail CI if skipped or absent. Logs and
  browser failure traces are retained for 30 days, including failed runs. CI does not replace public
  TLS, clean-host installation, remote-host, architecture or soak acceptance.
- A test that wants the dashboard's store opens it with `storetest.Open` (`internal/store/storetest`),
  which copies a database built once per test binary instead of running the schema again: under the
  race detector the schema was nearly two seconds of every such test. `store.Open` itself stays for
  the tests of the schema and its migrations. A test binary hashes passwords with parameters that
  cost nothing (`auth.HashPassword` under `testing.Testing()`): the real ones were seventy percent of
  what the API suite spent, and a hash carries its own parameters, so verification is unchanged. A
  test that names a MongoDB nothing listens on puts `serverSelectionTimeoutMS` in the connection
  string, or it waits eight seconds to find out.
- Changes to deployment builders or artifact handling also run the opt-in Docker boundary on a release
  host: `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveC4ArtifactAdapters -count=1 -v`.
  Recipe/detection/default changes also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveDetectedFrameworkBuildAndServing -count=1 -v`,
  and changes to a recipe's base images
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveRecipeBaseCatalogueRunsOnAmd64AndArm64 -count=1 -v`,
  which resolves (without pulling) every catalogue image and requires it for amd64 and arm64.
  Changes to build-failure diagnosis (the BuildKit reader in `dockerx`, the collector or the signature
  table) also run `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveBuildFailureIsNamedFromBuildKit -count=1 -v`,
  which builds an npm project whose lockfile no longer matches package.json and checks the cause is
  named from buildx's own output; it creates no image.
  Sixty-four fixtures: the locked Node starters (installed by Bun, pnpm and Yarn 1), a Next.js static
  export and a standalone server, SvelteKit on adapter-auto, Express serving a Vite client, a Hono
  dev-only starter on Bun, React Router in SPA mode with a prerendered home, FastAPI, Flask, Django,
  Streamlit, Gradio, the Python install shapes (PDM, Pipenv, uv on Python 3.14, a nested Django project
  with a `requirements/` folder and psycopg2, a Flask app with a Node asset stage), Go, a `go.work`
  member, a Go server embedding its Vite build with cgo SQLite and templ, axum, a Cargo workspace member,
  Leptos with hashed file names, Trunk, Maven, Gradle, the JVM and .NET layouts (a Maven reactor module, a
  multi-project and a composite Gradle build, a solution's web project, a multi-target project with a
  library, Blazor WebAssembly, F#, an ASP.NET Core project publishing an npm front end), ASP.NET Core,
  Deno, Laravel, Laravel with Vite and Wayfinder (assets built with PHP and vendor/, served behind a
  forwarded HTTPS), Symfony with AssetMapper (a committed `.env` that says dev) and plain PHP, Rails,
  Sinatra, Phoenix, Play, a Leiningen uberjar and Gleam, the site generators Hugo, Zola, mdBook, Jekyll,
  MkDocs, Lume and Eleventy, and a plain site whose `_redirects`, `_headers` and `netlify.toml` repeat
  and overlap each other's rules. The Leptos and Trunk builds install their tool from source, so give
  the run `-timeout 90m`; `TestLiveGoRecipeCatalogueResolves` checks every Go and Rust base image
  resolves.
- Git workspace changes also exercise `internal/gitx`, `internal/ghx` and `internal/forgex`, including
  race checks, plus `git-features.spec.ts`, `git-ui.spec.ts` and `design-system.spec.ts`. The LFS lifecycle
  test needs `git-lfs` on PATH (it is included in the backend image); it touches only a temporary
  repository. Provider fixture tests do not publish live comments or reviews. See
  [the Git workspace contract](docs/internal/backend/git-workspace-expansion.md) for limits and setup.
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
  Flask factory on a bare pyproject, Django with its migrations, axum, a Maven jar, ASP.NET Core,
  Deno, Laravel with Vite and Wayfinder, Symfony with AssetMapper, and the site generators, each also
  fetched by a clean URL and a missing page. It pulls the build images and package registries over the
  network and takes several minutes.
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
  on a Docker host. It provisions five of the templates (PostgreSQL, MySQL, MariaDB, Redis and
  MongoDB), reaches each from separate application containers,
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
  live, restores the dump into a drill database over an ordinary-confirmation route and checks the live
  database was not touched.
- Blueprint, planning or activation changes also run `python3 scripts/e2e-deployments.py`, which
  starts an isolated backend and deploys real nginx, busybox and PostgreSQL-blueprint releases through
  the public API, including generated-password authentication over TCP and paused backup automation.
- Keep the security posture intact. The network allowlist runs before
  authentication, enrolled accounts always require their second factor, every destructive route sits behind
  the destructive capability with an audit entry, and deletion of deployment projects, entire databases,
  and Docker stacks requires a typed confirmation phrase enforced server-side. A change that
  weakens any of those needs to say so explicitly in the PR description.
- Before changing typed confirmation, read invariant 3 in
  `docs/internal/security/invariants.md`. The typed set is limited to deletion of deployment projects,
  entire databases, and Docker stacks; other destructive actions use ordinary confirmation.
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

### The rule: no variable, no test — with six exceptions

A live test names its engine by an environment variable and skips without it.
Six variables are older than that rule and fall back to a local instance on
the engine's standard port in the suites written before it — the fixtures of
`dbx/live_test.go` and the tests built on them, the dump, drop, transfer and
credential tests, and `api/handlers_db_live_test.go`:

| Variable | Fallback |
| --- | --- |
| `JD_TEST_POSTGRES_DSN` | `postgres://jdtest:jdtest@127.0.0.1:5432/jdtest?sslmode=disable` |
| `JD_TEST_MYSQL_DSN` | `jdtest:jdtest@tcp(127.0.0.1:3306)/jdtest` (the MariaDB fixture) |
| `JD_TEST_MSSQL_DSN` | `sqlserver://sa:…@127.0.0.1:1433?database=master` |
| `JD_TEST_ORACLE_DSN` | `oracle://jdtest:jdtest@127.0.0.1:1521/FREEPDB1` |
| `JD_TEST_CLICKHOUSE_DSN` | `clickhouse://default@127.0.0.1:9000/default` |
| `JD_TEST_MONGO_DSN` | `mongodb://127.0.0.1:27017/jdtest` |

Those tests create, drop and restore. **On a machine where a standard port is a
real database, set all six explicitly before running anything under
`internal/dbx` or `internal/api`** — to a fixture, or to an address nothing
listens on, which makes the test skip. The two administrator variables below
fall back the same way in the tests that drop databases and read credential
catalogues. SQLite needs no server: it is embedded, and its fixture is a file in
the test's own temporary directory unless `JD_TEST_SQLITE_DSN` names another
(`JD_TEST_SQLITE_DROP_DSN` for the file the drop test makes and unlinks). Leave
both unset: set, they point tests that create and drop tables at that file.

Every other variable has no fallback, on purpose: the tests behind it write,
stop a server or change its configuration, and an address nobody chose is as
likely to be somebody's data as a fixture.

| Variable | What it must be | Used by |
| --- | --- | --- |
| `JD_TEST_REDIS_DSN` | a Redis the tests may write to, under the `jdb4:`, `jdb4api:`, `jdtest:`, `jdscan:` and `jdapi:` prefixes, in the logical database the string names | every Redis test |
| `JD_TEST_MYSQL8_DSN` | MySQL 8 itself; `JD_TEST_MYSQL_DSN` is MariaDB in every suite | workbench, catalogue, operations, transfer |
| `JD_TEST_MYSQL_ADMIN_DSN`, `JD_TEST_MYSQL8_ADMIN_DSN` | a login that may create and drop databases and accounts on those two servers (falls back to `root` on 3306 in the drop and credential tests) | drop, transfer, operations, catalogue, accounts |
| `JD_TEST_ORACLE_ADMIN_DSN` | an account that may read the `V$` views and create users (falls back to `system` on 1521 in the drop and credential tests) | sessions, locks, dumps, drop |
| `JD_TEST_B3_MSSQL_DSN`, `JD_TEST_B3_ORACLE_DSN`, `JD_TEST_B3_ORACLE_ADMIN_DSN`, `JD_TEST_B3_MYSQL8_ADMIN_DSN` | the operations suite's own servers, read before the shared variable of the same engine | `dbx/ops_live_test.go`, `api/handlers_db_ops_live_test.go` |
| `JD_TEST_MARIADB_DSN`, `JD_TEST_VALKEY_DSN`, `JD_TEST_KEYDB_DSN`, `JD_TEST_DRAGONFLY_DSN` | one server of each flavour | flavour detection, the capability flags against real servers |
| `JD_TEST_MONGO_RS_DSN` | a MongoDB replica set the run owns: accounts and views are made in `admin` | replication, accounts, the credential guards |
| `JD_TEST_MONGO_AUTH_DSN` | a MongoDB with access control on | `TestLiveMongoSignInIsNotAPing` |
| `JD_TEST_REDIS_ADMIN_DSN` | a Redis the run owns outright: configuration, users, slow log, clients, MONITOR, pub/sub | `TestLiveRedis*`, `TestLiveAPIRedis*` |
| `JD_TEST_REDIS_OWN_DSN` | a Redis whose numbered databases the run may flush | dumps of every numbered database |
| `JD_TEST_REDIS_FLAVORS` | `valkey=redis://…,keydb=redis://…,dragonfly=redis://…,redis=redis://…` | the Redis surface on each fork |
| `JD_TEST_REDIS_CLUSTER_DSN` | a node started with `--cluster-enabled yes` | cluster notices |
| `JD_TEST_REDIS_SENTINEL_DSN` | a Redis Sentinel | sentinel notices |
| `JD_TEST_REDIS_REPLICA_DSN` | a replica of the admin server | replication |
| `JD_TEST_ORM_MSSQL_DSN`, `JD_TEST_ORM_ORACLE_DSN`, `JD_TEST_ORM_MYSQL8_DSN`, `JD_TEST_ORM_COCKROACH_DSN` | a database the generator tests may create and drop their schema in | `dbx/orm_live_test.go` |
| `JD_TEST_ORM_POSTGRES_OLD_DSNS` | older PostgreSQL releases, separated by spaces | `TestLiveORMOlderPostgres` |

Some tests reach past a database to the machine and run only when asked:

| Variable | What it does |
| --- | --- |
| `JD_TEST_INVENTORY_LIVE=1` | reads this machine's real Docker daemon, sockets, units and files (`TestLiveInventoryNeverCarriesAContainerSecret`); it only reads |
| `JD_TEST_INVENTORY_CONTAINER=<name>` | signs in to that one container and nothing else (`TestLiveInventoryConnectsAContainerByKey`) |
| `JD_TEST_PROVISION_ENGINES=redis,valkey,clickhouse:24.8` | starts each named template, connects it the way the page does and turns it off and on again (`TestLiveProvisionAdoptAndPower`); it creates and removes its own containers and volumes |
| `JD_TEST_POWER_REDIS_DSN` | a Redis in a container the test may stop and start (`TestLivePowerIsReadInFlightOnARealContainer`) |
| `JD_TEST_HOST_PG_PORT=<port>` | a PostgreSQL installed on the host, run as root: the host account bootstrap (`TestLiveHostPostgresAccount`) and the log sources read off `/proc` (`TestLiveHostDBLogSources`) |

The ones that need Docker (the inventory, provisioning and power tests) read
`JD_TEST_DOCKER_HOST` where the daemon is not at `unix:///var/run/docker.sock`.

### Fixtures

The quickest way to get the engines is containers:

```bash
docker run -d -p 5432:5432 -e POSTGRES_USER=jdtest -e POSTGRES_PASSWORD=jdtest -e POSTGRES_DB=jdtest postgres:16
docker run -d -p 3306:3306 -e MARIADB_ROOT_PASSWORD=jdtest -e MARIADB_DATABASE=jdtest -e MARIADB_USER=jdtest -e MARIADB_PASSWORD=jdtest mariadb:11
docker run -d -p 1433:1433 -e ACCEPT_EULA=Y -e MSSQL_SA_PASSWORD='JdTest#2024pw' mcr.microsoft.com/mssql/server:2022-latest
docker run -d -p 9000:9000 clickhouse/clickhouse-server:latest
docker run -d -p 27017:27017 mongo:8
docker run -d -p 6379:6379 redis:7
```

Then, from `backend/`, with `JD_TEST_REDIS_DSN=redis://127.0.0.1:6379/0` set
for the Redis one, `go test ./internal/dbx/ ./internal/api/ -run Live -count=1 -v`
and watch which engines report rather than skip. Publish the containers on
other ports and set the variables instead wherever 5432, 3306 or 6379 is
already somebody's server.

On a server shared between runs, point `JD_TEST_MSSQL_DSN` at a database of the
run's own: the workbench tests name every table they make `jdwb_…`, the
operations suite works in a database called `jd_b3`, and the dump tests make a
database (SQL Server) or a user (Oracle) of their own, because a restore
replaces every table where it lands.

Oracle has unit coverage for statement guards, SQL rendering and adapter behavior. Live server
coverage requires an available Oracle instance: set `JD_TEST_ORACLE_DSN` to run the existing Oracle
fixture alongside the others. The cases that use the JSON and BOOLEAN types need 23ai. If no server
was used, identify that validation limit in the pull request; unit results do not establish that the
generated statements work against an Oracle server.


### Two files a test holds to the code

- **The capability table.** What `GET /databases/drivers` answers is kept as
  `backend/internal/api/testdata/database-drivers.json`, which the frontend's
  engine registry tests and the browser fixture read, since they run with no
  server. `TestTheDriverCatalogueSnapshotIsCurrent` fails when the route and the
  file differ. After giving an engine a capability or taking one away, write the
  file again and run the frontend's tests against it:

  ```bash
  cd backend && go test ./internal/api -run TestTheDriverCatalogueSnapshotIsCurrent -update-drivers
  cd ../frontend && bun test src/components/database
  ```

- **Generated code.** Each generator's output for each engine and option is a
  golden file under `backend/internal/dbx/testdata/orm`. After a deliberate
  change to a generator, rewrite them with
  `go test ./internal/dbx -run TestORMGolden -update-orm` and read the diff.

### The Databases pages

Each area of the section has its own browser spec over one mocked server
(`frontend/tests/browser/database-fixture.ts`, which answers with the driver
catalogue above, and `database-fleet-fixture.ts` for a machine with and without
Docker): `database-shell.spec.ts`, `database-control-center.spec.ts`,
`database-home.spec.ts`, `database-data.spec.ts`, `database-query.spec.ts`,
`database-schema.spec.ts`, `database-redis.spec.ts`, `database-mongo.spec.ts`,
`database-performance.spec.ts` and `database-logos.spec.ts`.
`scripts/test-changed.sh` picks the ones a change can reach: it follows a
changed file through its imports to the pages that use it and runs the specs
that name those pages' addresses. A generator's file reaches the Generate page
and runs the one spec that opens it; the engine registry and the shell, which
every page reads, run every Databases spec. A changed component or stylesheet
also runs `design-system.spec.ts`, which walks every Databases page at 1280 and
at 390.
Run one by hand against a production build:

```bash
cd frontend
bun run build
bunx playwright test tests/browser/database-data.spec.ts
```

Playwright serves that build on 127.0.0.1:43117 itself, or uses the server
already listening there.
