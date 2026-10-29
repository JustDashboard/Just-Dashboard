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
  Set `JD_BROWSER_WORKERS=1` to limit the selected browser run on a memory-constrained host; omit
  it to use the existing Playwright worker setting.
- Docker remedy changes run the live resource/policy acceptance: first make `alpine:3.20` available
  locally, then run from `backend/`:
  `JD_ADVISOR_DOCKER_LIVE=1 go test ./internal/dockerx -run '^TestLiveAdvisor' -count=1 -v`.
  It owns and removes its temporary fixtures and does not modify existing workloads.
- Changes to `internal/netx` that apply devices, routes, the gateway table or shaping also run, from
  `backend/`, `JD_NETNS_LIVE=1 go test -race ./internal/netx -run Live -count=1`. It needs root or
  passwordless sudo and does everything inside throwaway network namespaces it removes, never on the
  host's own interfaces, firewall or tailscaled.
- Persistent native profile changes also follow the bounded
  [native-owner acceptance](docs/internal/backend/network-native-managers.md#local-acceptance).
  The fixture requires verified native userland and uses private net/mount/PID namespaces, private
  `/etc`, `/run` and `/var/lib`, and explicit private NetworkManager state paths. It builds the actual
  standalone recovery executable with a workspace-local `TMPDIR`. Native-owner activation does not
  substitute for the separate systemd timer, DHCP/SLAAC, boot or structural bond/VRF requirements.
  Include a canonical unchanged-intent transaction: equal bytes with a distinct candidate inode must
  restore the captured authored inode during fresh-helper recovery. Confirmation and terminal retry
  must retain the exact candidate inode. Keep original failed runs separately attributed.
  The [automatic addressing/deadline fixture](docs/internal/backend/evidence/native-manager-automatic-2026-10-09.md)
  has explicit `JD_NATIVE_AUTO_CASE=networkd` or `NetworkManager` selections for its verified direct
  owner slices. Its default all-owner opt-in also runs the still-open Netplan admission cases; those
  known refusals remain failed acceptance rather than successful skips.
  `JD_NATIVE_AUTO_CASE=netplan JD_NATIVE_NETPLAN_AUTO_POLICY=explicit` selects the separately verified
  authored Netplan/networkd DHCPv4/SLAAC policy with DHCP MTU disabled and explicit DHCP/RA domains.
  Follow its [v8 source/helper proof](docs/internal/backend/evidence/native-manager-netplan-v8-2026-10-09.md);
  this selection does not replace the default-policy, DHCPv6 or structural acceptance requirements.
- Remote Desktop has an opt-in live fixture, `JD_REMOTE_DESKTOP_LIVE=1 go test ./internal/remotedesk -run Live -v`.
  It installs the real guacd engine, opens a session over VNC and one over RDP to throwaway XFCE
  desktops on the engine's network and waits for each first frame, and installs Stream mode's bridge
  and reads its stream page as the dashboard's account (`-run LiveBridge` for that alone), then
  removes both unless `JD_REMOTE_DESKTOP_KEEP=1` ([Remote Desktop](docs/internal/backend/remote-desktop.md#tests)).
- The container-source investigator has an opt-in disposable-container fixture, `JD_NETPATH_LIVE=1`.
  Follow [its native acceptance command](docs/internal/backend/network-investigator.md)
  to compile the narrow test binary and run it as root. It creates a network-none fixture with no
  published ports, verifies private DNS/source/route/TCP and restart identity, then removes it.
- Kernel flow observer changes also run the targeted `internal/netflows` race suite and the opted-in
  disposable cgroup/network-namespace fixtures. Build a race binary and invoke it with
  `JD_NETFLOWS_OBSERVER_LIVE=1` as root in the host cgroup namespace; see the exact command and evidence
  limits in [the observer contract](docs/internal/backend/network-flow-observer.md). These fixtures
  never attach a production cgroup, replace a foreign program or migrate an unrelated process.
  Docker attribution acceptance also needs a reachable Docker socket and the already cached
  `python:3.11-slim` image; the fixture never pulls an image and removes its bounded test containers.
- Independent network recovery also has a real systemd timer fixture. On a host with a reachable
  systemd manager, add `JD_SYSTEMD_RECOVERY_LIVE=1` to the network namespace command above. It builds
  the standalone helper and uses uniquely named transient timers with `NetworkNamespacePath` for
  disposable namespaces, then removes those exact fixtures. It does not install persistent host
  units or reboot the host; timer dispatch and cold-runtime reconstruction are separate from actual
  reboot acceptance.
- Native resolver evidence changes also run from `backend/`:
  the private race test binary as root using the commands in
  [native DNS investigations](docs/internal/backend/network-dns-evidence.md#controlled-validation).
  This requires root or passwordless sudo and installed resolved/busctl/dbus-daemon/ip. It uses only
  owned network/mount namespaces, private bus/configuration and a controlled signed TLS DNS fixture;
  it never changes the host resolver. See [native DNS investigations](docs/internal/backend/network-dns-evidence.md).
- Native DNS service changes run the selected private API, sealing, HTTP/TLS and owned-resource races,
  then each actual pinned engine serially with `JD_DNS_SERVICES_LIVE_ENGINE`. See
  [native DNS service acceptance](docs/internal/backend/network-dns-services.md#acceptance-status)
  for the selected test binary, required already-cached images and exact owned cleanup scope.
  These fixtures publish explicit high loopback ports and never redirect the host resolver.
  Filter-inventory changes also verify native source/custom-rule metadata and restart persistence;
  subscription loading and effective client filtering remain unmeasured. Retain failed and
  corrected source/binary/raw records separately.
  Filter UI changes run `network-dns-filters.spec.ts` against the matching production build; width
  assertions must check the settled sheet against the viewport before capturing it.
  Reviewed custom-domain filters also prove native allow/deny configuration with AdGuard suffix
  versus Pi-hole exact semantics, selected-current reads, explicit group membership, duplicate/replay
  refusal, restart persistence and unselected rule/comment/group/source preservation after removal.
  Record/client UI changes also run the selected DNS service browser spec against a fresh production
  build. Its mocks must supply `/changes/{id}/current` for the exact retained selection; ordinary
  connection inventory cannot stand in for selected record/client freshness. Browser mocks and
  actual engine acceptance remain separately attributed.
- Controlled probe-agent changes also run from `backend/`:
  `JD_NETVANTAGE_LIVE=1 go test -race ./internal/netvantage -run '^TestControlledVantageSeparateNamespace$' -count=1 -v`.
  This lane uses passwordless sudo to create an owned loopback namespace, then runs as the contributor
  account. It measures real signed DNS/TCP/TLS in both families and refuses out-of-scope/untrusted
  outcomes. It proves a separate controlled namespace, not a real external host or region.
- Opt-in socket collector changes also run from `backend/`:
  build the private test binary and run its selected fixtures as root using the commands in
  [native socket history](docs/internal/backend/network-flow-accounting.md#measured-acceptance-and-its-limits).
  The fixtures own
  their disposable namespace and exact Docker containers with no published ports; native command
  deadlines remain fixed, and a deadline failure is recorded as unavailable evidence. See
  [native socket history](docs/internal/backend/network-flow-accounting.md) for tools and scope.
- Advanced Docker network changes also run from `backend/`:
  `JD_DOCKER_NETWORK_LIVE=1 go test -race ./internal/dockerx -run '^TestLiveAdvancedNetwork' -count=1 -v`.
  The fixture needs the local Docker socket, `ip` and cached `python:3.11-slim`; it never pulls an
  image or publishes a port. It chooses pools outside observed host routes/Docker allocations,
  creates an internal uniquely labeled dual-stack bridge and one exact owned container, verifies
  native fields/alias/allocation/overlap refusal, then removes only its verified IDs.
- Private PCAP changes also run from `backend/`:
  `JD_NETCAPTURE_LIVE=1 go test -race ./internal/netcapture -run Live -count=1 -v`. The fixture
  needs root or passwordless sudo plus host `ip`, `tcpdump`, `timeout` and Python. It creates and
  removes only a uniquely named disposable namespace, checks known IPv4/IPv6 loopback packets,
  bounds, cancellation and independent timeout after killing its own applying process. It never
  captures production interfaces or starts public listeners. See
  [capture verification](docs/internal/backend/network-captures.md#verification).
- Sign-in and passkey changes (`auth/passkeys.go`, the sign-in page, the account's Security page) run
  the auth and API passkey tests, which drive the ceremonies with the software authenticator in
  `internal/auth/passkeytest` (a replayed challenge, another origin or relying party, a counter going
  backwards, a removed passkey, an enrolled account still asked for its code), and
  `account-passkeys.spec.ts`, which uses Chrome's virtual authenticator over CDP at `localhost` —
  WebAuthn refuses the `127.0.0.1` the other specs use, so the spec opens the same server by name.
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
  tests. `browser` runs, for a pull request, the specs `scripts/test-changed.sh` would pick: a module
  more than one section draws, the shell, a root layout or global CSS runs `design-system.spec.ts`
  and `navigation.spec.ts` rather than every spec that opens a page it reaches, and a public asset is
  followed from the modules that name its file. A pull request is what somebody waits on before
  merging; the push run of the merge, which nobody waits on, follows the same change to every page it
  reaches, the whole suite for the shell or a public asset, so what the pull request left out is
  checked one merge later. A change to the dependencies, the build's configuration or the request
  proxy runs the whole suite in both. The specs are dealt into up to eight jobs, in name order, so
  that a slow section is spread over the jobs: of about a hundred tests each for a pull request, which
  finishes sooner, and two hundred for a merge. A change to the workflow or its scripts runs
  everything, as does a manual run, and a documentation-only change runs nothing past `plan`. A pull
  request from `main` or a `patch/*` branch runs nothing either: its commit is the one that branch's
  push run checks, and GitHub shows that run's checks on the pull request.
  GitHub runs twenty jobs of a public repository's at once across every branch, so the suites are
  split only as far as a runner's four cores are full: more jobs than that queue behind each other and
  behind every other pull request. Go and Bun come from `go.mod` and `package.json`, and dependencies
  use the frozen Bun lockfile. The module, build, Bun, Playwright, Next and `tsc` caches are saved only
  by runs on `main` and `patch/*` and restored by pull requests into them, because a run can read
  the caches of its base branch and never those of another task branch.
- `.github/workflows/release.yml` publishes the GitHub release. It runs after Verify passes on a push
  to `main`, reads `latest` from `backend/internal/selfupdate/changelog.json`, and when that push
  changed it and no release `v<latest>` exists yet, tags that commit and publishes the release with the version's section of
  `CHANGELOG.md` as its notes. The branch a release was cut on does not matter, and a merge that
  bumps no version publishes nothing. Never tag or publish a release by hand; a manual run of the
  workflow on `main` retries one that failed.
  Real-nginx tests that use `http2 on;` probe the installed nginx first and skip if it lacks that
  directive; the other nginx tests still run. `TestLiveSiteControlsAreMeasured` instead writes its
  site for the build it finds — HTTP/2 in the listen before 1.25.1, a QUIC listen only with
  `http_v3_module` — so it runs on the runners' nginx 1.24 too.
- The live Docker fixtures need a real Docker daemon, which GitHub's hosted Ubuntu runners provide, so
  they run there like the other jobs, each job on a fresh daemon, and only when one of the six
  deployment packages (those of the race gate) or `go.mod` changed: `live (fixtures)` runs the
  artifact, activation, preview, runtime-fault, database, Compose and cutover fixtures of whichever of
  `internal/{deploy,api,dockerx,proxysvc}` the change reaches, and a change reaching `internal/deploy`
  also builds the framework fixtures in four jobs. A pull request narrows both to the deployment code
  itself: it runs the fixtures of those four that import the deployment package that changed, so a
  change to a handler and to `internal/metrics`, which `internal/deploy` reads, runs the API's
  fixtures and none of `internal/deploy`'s, and it builds the frameworks only when a file in
  `internal/deploy` changed. The merge's push run keeps the wider rule. The lists are
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
  which builds an npm project whose lockfile matches package.json but leaves out a package a locked
  dependency needs (detection compares only package.json with the lock, so the recipe still runs
  `npm ci` rather than `npm install`) and checks the cause is named from buildx's own output; it
  creates no image.
  Changes to a failed run's diagnosis (what is sent, how it is masked, how the answer is read:
  `deploy/run_diagnosis.go`) also run it against a real model behind an OpenAI-compatible server on
  this machine — never a hosted key — for example Ollama on loopback:
  `JD_DEPLOY_LIVE=1 JD_TEST_ASSISTANT_URL=http://127.0.0.1:11434/v1 JD_TEST_ASSISTANT_MODEL=qwen2.5:3b go test ./internal/deploy -run TestLiveFailedBuildIsDiagnosedByARealModel -count=1 -v`.
  It builds a Node project whose build script needs a package it never declared, sends the digest the
  route would send and fails unless the answer quotes a line the build printed; `JD_TEST_DIAGNOSIS_OUT`
  writes the answer as the run page reads it. A small model on a busy processor is slow: give it a few
  minutes, and fewer threads than the host has cores.
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
- Multi-service (Compose) templates have a sweep of their own. It saves each template's source the way
  the new-project page does (the server's render of the definition), resolves and pulls every service
  image through the Compose build step, starts the stack through the runtime owner with generated
  secrets, and requires the definition's readiness checks to pass, every service's Docker healthcheck
  to report healthy, and only the public service's port to be published, on loopback
  (`JD_BLUEPRINT_ONLY=a,b` narrows it; each stack's containers, volumes and networks are removed after
  it, and so are images it pulled):
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveEveryComposeTemplateStartsAndAnswersItsOwnChecks -count=1 -v -timeout 2h`.
  A template's schema rules run at build time: every definition is parsed and validated when the
  catalogue loads, so `go test ./internal/blueprint` fails on a broken one before it can ship. The
  same package checks the catalogue's size: `catalogue_counts.go` declares how many definitions
  ship, how many deploy and how many are Compose templates, because the competitive report and the
  README quote those numbers, and `TestCatalogueCountsMatchTheShippedDefinitions` fails until a
  change that adds or retires a template bumps them.
- Changes to shared variables or to how a run opens its variables also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveSharedVariableReachesTheContainer -count=1 -v`,
  which starts a real container with the values a run opens for references to a server, a secret and a
  project variable, reads them back from inside it, and checks the next run reads a changed value.
- Changes to secret stores or store references (`internal/secretstore`, `deploy/secret_store_references.go`)
  also run `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveSecretStoreReferencesReachTheContainer -count=1 -v`
  (as root, or build the test binary and run it under `sudo`). It starts a real OpenBao dev server and a
  self-hosted Infisical with its PostgreSQL and Redis on loopback (`openbao/openbao:2.7.2`,
  `infisical/infisical:v0.167.4`, `postgres:17-alpine`, `redis:7-alpine`, pulled beforehand), reads a
  secret from each through AppRole and a Universal Auth machine identity into a real container's
  environment, a pinned version included, then stops OpenBao and checks the next run's first step fails in
  the store's own words. It removes everything it made. Changes to the AWS Secrets Manager client or the
  SigV4 signer (`secretstore/aws.go`, `sigv4.go`) also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveSecretsManagerReferenceReachesTheContainer -count=1 -v`,
  which starts a real LocalStack (`localstack/localstack:2026.10.3`, pulled beforehand, Secrets Manager
  only) on loopback, reads a JSON secret's field, its `AWSPREVIOUS` staging label and a whole plain secret
  into a real container through the dashboard's own signed client, then stops LocalStack and checks the
  next run fails in the store's words; it removes what it made. LocalStack accepts any signature, so the
  signing itself is proved by the unit tests: AWS's documented SigV4 example and a Secrets Manager request
  checked against the AWS SDK's signature (`sigv4_test` in `secretstore_test.go`). Doppler, Secrets Manager
  and Azure Key Vault have no self-hosted server and are covered by httptest servers that follow their
  documented APIs — Doppler's in `secretstore_test.go`, the other two in `secretstore/secretstoretest`
  (the Secrets Manager fake verifies every request's signature with an implementation of its own) — through
  the unit tests, `deploy/secret_store_cloud_test.go` (the live fixture's shape without Docker) and the API
  tests. The real AWS and Azure services are not exercised by any test.
- Changes to staging environments, promotion or how a release is cloned also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveStagingReleaseIsPromotedAsTheSameImageAndRolledBack -count=1 -v`
  on a Docker and Buildx host (as root, through `go test -c` and `sudo`, like the others). Through the
  real routes and engine it builds a local Git checkout in staging, promotes the staging release and
  checks production runs the very image id staging built with no build step in its run, serves with
  production's variable and runs production's release task with production's value, then promotes a
  second staging build and rolls production back to the first promoted release. Its environment and
  run ids start far from 1, and it removes only the containers and tags its own store recorded.
- Object-storage backups against a real S3 API (MinIO in a container: target test, upload, retention,
  restore): `JD_DEPLOY_LIVE=1 go test ./internal/backups -run TestLiveObjectStorageBackupUploadsPrunesAndRestores -count=1 -v`.
  Encrypted archives in a bucket take an S3-compatible service you started yourself, named by
  `JD_TEST_S3_ENDPOINT` and its keys: `go test ./internal/backups -run TestLiveEncryptedArchiveRoundTripsThroughObjectStorage -count=1 -v`;
  a native dump restored from an encrypted archive takes `JD_TEST_POSTGRES_DSN` and
  `JD_TEST_MYSQL_ADMIN_DSN`: `go test ./internal/api -run TestLiveEncryptedArchiveRestoresANativeDump -count=1 -v`.
  SFTP and rclone destinations against an OpenSSH server (atmoz/sftp) and a WebDAV server run by rclone,
  through the same lifecycle plus a refused host-key change:
  `JD_DEPLOY_LIVE=1 go test ./internal/backups -run TestLiveSFTPAndRcloneBackupsUploadPruneAndRestore -count=1 -v`
  (the rclone half needs `rclone` on PATH, as the backend image ships it).
- The public-certificate journey against a real ACME authority (an isolated Caddy and a Pebble that
  validates nothing, on one Docker network):
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyIssuesThroughAConfiguredACMEDirectory -count=1 -v`.
  Changes to how a deployment run orders a certificate over HTTP-01 through certbot (the argv, the
  account check, which certbot runs) also run
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDeploymentHTTP01OrdersFromTheConfiguredDirectory -count=1 -v`,
  which drives the host's certbot against a Pebble that validates nothing, in directories of the
  test's own, and requires the order to register with and be signed by that Pebble while an account
  with Let's Encrypt sits beside it; no port 80 is touched.
  Changes to how a deployment run issues a certificate over DNS-01 (a wildcard plan name, the provider
  choice, the saved credentials it uses) also run
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDeploymentWildcardIsProvedOverDNS01AndServed -count=1 -v`
  and `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveCertificateStepProvesAWildcardOverDNS01 -count=1 -v`,
  as root for Docker. `proxysvc/acmetest` starts a Pebble that really validates DNS-01 against a BIND
  (`internetsystemsconsortium/bind9:9.20`) taking signed RFC 2136 updates, so the host's certbot needs
  its dns-rfc2136 plugin: `python3-certbot-dns-rfc2136`, or that plugin's wheel and dnspython's unpacked
  into a directory on `PYTHONPATH` (`sudo -n env JD_DEPLOY_LIVE=1 PATH=$PATH PYTHONPATH=<dir> <test binary>`);
  without it all three skip. Each waits out the plugin's 60-second propagation delay once, the first
  and the Caddy one twice. The same changes, and any change to how a Docker Caddy route serves a
  certificate copy or how a renewal reaches it (`docker_caddy_copies.go`, the ingress maintenance
  pass, `reloadAfterRenewal`), also run
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyServesAWildcardProvedOverDNS01 -count=1 -v`
  (the wildcard served by a real `caddy:2-alpine`, then renewed and refreshed; needs the plugin above)
  and `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyRefreshesRenewedCertificateCopies -count=1 -v`
  (the renewal refresh on a real Caddy with no authority involved).
  Changes to how a deployment route renders its redirects, response headers, allowed addresses, HSTS
  or a domain's own scheme also
  run `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyServesRouteRules -count=1 -v`,
  which asks a real Caddy what it answers for each, a route serving one name over HTTPS and another
  over plain HTTP with HSTS included, and
  `go test ./internal/proxysvc -run TestLiveDeploymentRouteMixedTLSOnNginx`, which asks the host's
  nginx the same. Changes to a route's sign-in, how Caddy reaches the
  auth server, or the maintenance pass that repairs that network also run
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyGatesRouteBehindForwardAuth -count=1 -v`,
  which signs in at a real Authelia (`authelia/authelia:4.39`, pulled on first run) in front of
  `traefik/whoami`, and `go test ./internal/proxysvc -run TestLiveDeploymentRouteSignsInOnNginx`, which
  needs only the host's nginx. Changes to a route's maintenance switch or error pages also run
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyServesMaintenanceAndErrorPages -count=1 -v`
  and `go test ./internal/proxysvc -run TestLiveDeploymentRouteMaintenanceOnNginx`, which switch a real
  Caddy's and the host nginx's route by its file and ask each what a visitor, an address let past and a
  stopped application get.
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
  including a container a stopped dashboard left under a task's name. Changes to migration rehearsal,
  the release-task runtime it shares, or the dump extraction and point-in-time recovery it restores
  from also run, from `backend/internal/api` as root on a Docker host,
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run 'TestLive(MigrationRehearsal|RehearsalLeftovers)' -count=1 -v`:
  it provisions a PostgreSQL through the routes and deploys an idle image whose release task
  migrates through `${{database.N.url}}`. Before any backup the run ends `rehearsal_unavailable`
  and production is untouched; once a backup job has dumped it, a column added passes against the
  restored copy, holds its lock for two seconds the report measures, and reaches production; a
  `SET NOT NULL` on a column holding a null fails against the copy, the run ends `rehearsal_failed`
  before its release task, production's schema and rows are unchanged, and no container, volume or
  network carrying the run's rehearsal label is left
  (`TestLiveMigrationRehearsalBlocksAFailingMigration`; the dump is the built-in one unless a
  `pg_dump` is on `PATH`, as it is in the dashboard's image, which makes it a custom-format archive
  the copy restores with `pg_restore` — run it both ways after changing the restore).
  `TestLiveMigrationRehearsalRestoresAMariaDBDump`
  does the same against MariaDB (it pulls `mariadb:11.8` once),
  `TestLiveMigrationRehearsalGatesAPromotedRelease` does the same through promotions — a staging
  environment of the same image, its release promoted into production three times: refused for want
  of a backup, passing against the copy and reaching production, then failing against the copy with
  production's schema, rows and live release unchanged — so changes to promotion run it too, and
  `TestLiveRehearsalLeftoversAreRemovedAtStart` checks the start-up sweep removes a copy, its volume,
  a task container and the network by label and leaves a managed container without it. With
  `JD_TEST_WALG=<path>` to the WAL-G release `backend/Dockerfile` pins,
  `TestLiveMigrationRehearsalRecoversFromThePointInTimeArchive` runs as well: a PostgreSQL 17
  archiving into a directory, the copy recovered from the archive. They remove their own
  `jd-rehearsal-…` servers, volumes, networks and release containers.
- `python3 scripts/e2e-deployments.py` is the real-backend acceptance lane for deployments: it builds the
  backend, starts it on loopback with a fresh data directory, and drives the public API through a signed
  webhook channel, an nginx image deployment with resource limits, a busybox image that exits before it
  listens, and a restart. It needs Docker and Go and touches only the containers it creates; run it after
  changes to the engine, run observers, notifications, runtime limits or health-gate diagnostics.
- Notification channels (Discord, Slack, Microsoft Teams, Mattermost, Telegram, ntfy, Gotify, Pushover,
  e-mail, signed webhook) and commit statuses are covered by component tests with fake providers.
  Commit statuses and preview comments on GitLab, Gitea and Forgejo also run against the real servers:
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveForgesReceiveStatusesAndOneCommentPerPreview -count=1 -v`
  starts Gitea and Forgejo; add `JD_FORGE_LIVE_GITLAB=1` (and `-timeout 25m`) for GitLab, which takes
  about five minutes to start.
  `TestLivePushServersReceiveARunFailure` (`JD_DEPLOY_LIVE=1`, Docker) runs real ntfy, Gotify and
  Mattermost servers on loopback and reads the delivered message back from each; the hosted providers
  and a GitHub post are verified manually through **Send test** and a deployment of a GitHub-sourced
  project, and the pull request must say which providers were exercised.
- Log drains run against real services with
  `JD_DEPLOY_LIVE=1 go test ./internal/logdrain -run TestLiveDrainsDeliverToLokiTheCollectorAndVector -count=1 -v`:
  Loki (push API and OTLP, read back with LogQL), an OpenTelemetry Collector and Vector, each in a
  container on loopback. The package's concurrency is worth `-race` (it needs cgo; the `golang` image
  has it).
- Changes to deployment variables, feature links, backup gates or managed-resource lifecycle also run
  `go test -race ./internal/deploy ./internal/api ./internal/proxysvc ./internal/backups ./internal/store -count=1`;
  the browser gate covers the project overview, build transcript and focused settings, including
  variables, domains, storage, dependencies, automation and lifecycle.
- Preview changes also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run 'TestLive(PreviewStorageCredentialsNetworkAndCleanup|LegacyPreviewQuarantinePreservesProduction)$' -count=1 -v`.
  These fixtures cover new-preview isolation and production-preserving quarantine of older previews.
  Compose backup coverage changes run
  `JD_DEPLOY_LIVE=1 go test ./internal/dockerx -run TestLiveDeploymentComposeStorage -count=1 -v`.
- Changes to the connection pooler (`dbx/pooler.go`, `api/database_pooler.go`, its routes, or how a
  deployment reaches it) also run
  `JD_POOLER_LIVE=1 go test ./internal/api -run TestLivePooler -count=1 -v` on a Docker host. It starts a
  PostgreSQL container and, through the routes, a PgBouncer beside it, signs in through the pooler in both
  authentication modes, reads its statistics, changes a setting and a password, follows the database's
  power, reaches it from a probe container on a deployment network, then removes it and forgets the
  connection, and removes its own containers, volumes and networks.
- Changes to database provisioning or deployment connection URLs also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveDeploymentDatabaseConnection -count=1 -v`
  on a Docker host. It provisions five of the templates (PostgreSQL, MySQL, MariaDB, Redis and
  MongoDB), reaches each from separate application containers,
  replaces databases at a different IP, reconnects the same clients using their original URLs,
  verifies ownership/removal, and cleans up its own containers, volumes and networks. It pulls each
  engine's image before creating anything and logs how long that took, so a slow registry reads as a
  pull rather than as a database that never answers; each engine then has two minutes from its
  container starting to accept a sign-in, and a failure reports the adopt route's last reply with the
  container's state and last log lines. MongoDB 8 does not start on Linux 6.19 to 7.0.13 (MongoDB
  SERVER-121912, which a runner image can move to) without `GLIBC_TUNABLES=glibc.pthread.rseq=1`: the
  MongoDB subtest checks that provisioning gave it that on such a kernel and nothing on any other, and
  fails at once, naming the kernel and MongoDB's line, if it still refuses; `mongodb-kernel-mitigation`
  applies the mitigation from the Databases page to a server made without it, on the runner's real
  kernel when it is one of those, and otherwise on a server that is made to log MongoDB's refusal and
  killed, with the kernel read as 7.0.0.
  Compose network integration also runs `JD_DEPLOY_LIVE=1 go test ./internal/dockerx -run TestLiveComposeDatabaseNetworkMerge -count=1 -v`.
- Changes to server TLS for the databases the dashboard starts (`api/database_tls*.go`, `internal/dbtls`,
  `dbx/servertls.go`) also run
  `JD_DB_TLS_LIVE=1 go test ./internal/api -run TestLiveDatabaseServerTLS -count=1 -v` on a Docker host
  with the engines' images already pulled (`postgres:17-alpine`, `redis:8-alpine`, `mysql:8.4`,
  `mariadb:11.4`, `mariadb:10.11`, `mongo:8`, `valkey/valkey:8-alpine`). `JD_DB_TLS_ENGINES=postgres,mariadb-10.11` (the subtests' names) narrows it to
  some of them, and `openssl` is read for the certificate a SQL server presents. For each engine it
  starts a server, switches it to TLS, requires TLS, checks the certificate on the wire against the
  recorded serial, renews it (by request and by the schedule) without a restart, switches TLS off,
  and removes its container and volumes.
- Changes to the ingress request record — the rendered `log` block, `accesslog.Store`, or either
  reader in `proxysvc/deployment_access_log.go` — also run
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveCaddyAccessLogReader -count=1 -v`, which
  drives the container-side script against `caddy:2-alpine`: a roll under the reader, the rolled
  generation found by inode with its tail recovered, the new live file, a vanished inode reading as
  absent. It starts one idle container and removes it.
- Changes to the deployment lifecycle feed — the owner filter, the audit correlation, the kinds,
  either endpoint in `handlers_deploy_requests.go`, or the persisted record in
  `dockerx/events_store.go` — also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveDeploymentEventsAreReadFromDockerAndNamedWithTheirCause -count=1 -v`.
  It labels a real container and a real network the way the runtime owner labels what it creates,
  lets the container exit 137, and reads both back through the real routes and the real socket. The
  feed rests on Docker putting an object's labels in an event's actor attributes, and nothing in this
  code would notice if that stopped being true — it is already false for networks, which is what the
  fixture exists to pin. It then replaces the event ring with one that has never connected — a restart
  of the dashboard, as far as the feed can tell — and requires the exit to still be in the feed, read
  back from the store with its code, its labels and its cause. It creates two containers and one
  network and removes them. Changes to the deployments' Prometheus exposition
  (`api/deployment_metrics_export.go`) or the availability maths (`deploy/availability.go`) run their
  unit tests; a real Prometheus scraping the endpoint is not part of the checks.
- Changes to deployment routes, activation cutover or runtime ownership also run
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run '^TestLiveCutover(TrafficContinuity|SurvivesProxyLoss)$' -count=1 -v`
  and `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveRuntimeLossKeepsExactlyOneReleaseLive -count=1 -v`.
  The first drives sustained fast, long-streamed and WebSocket traffic through 100 real nginx
  activations, for a release of one container and again for one of two replicas, and fails on a single
  lost request; the replicated run also requires both replicas to answer and a replica killed under
  steady traffic to cost no request. Set `JD_CUTOVER_TRANSITIONS` lower only for local iteration, never
  for an acceptance run, and `JD_CUTOVER_EVIDENCE` to retain the measured counts (the replicated run's
  beside them, `-2-replicas.json`).
  The second kills real containers in the blue/green window and at a live release, and requires that
  exactly one release is running afterwards — for a release of three replicas, that one replica of the
  candidate dying removes the whole candidate and one of the live release dying brings it back whole.
  Changes to replicas — how they are started, named, labelled, stopped or kept warm, or how a route
  shares requests between them (`deploy/runtime_replicas.go`, the pool in `proxysvc`) — also run
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyBalancesReplicas -count=1 -v`
  and `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveReplicatedReleaseServesAndRollsBackAsOne -count=1 -v`
  (as root, through `go test -c` and `sudo`). The first puts three replicas behind a real Caddy and kills
  one under traffic; the second deploys a release of two replicas through the routes and its own Caddy
  ingress (`JD_INGRESS_CONTAINER`, on a loopback port), cuts over to a broken release of two only after
  both pass readiness, and requires the automatic rollback to resume both of the first release's paused
  containers and remove both of the second's ([replicas](docs/internal/deployments/canary-and-replicas.md)).
  Changes to canary releases — the strategy and its terms, the `canary` step, how a route is split
  between two releases or records which one answered (`deploy/canary*.go`, `proxysvc/sites_split.go`,
  `caddyReleases`, `accesslog`'s `Release`) — also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveCanaryAbortsAFailingReleaseAndPromotesAHealthyOne -count=1 -v`,
  `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveCanarySplitsTheRouteOnNginx -count=1 -v`
  and `TestLiveBackendKillAtEveryStepLeavesOneRecoveredRun`, whose matrix now includes the step. The
  first runs a real engine behind its own loopback Caddy ingress: a canary that answers 500 to its share
  is aborted on the side-by-side reading with the live release's same container serving everything, and
  a healthy one is promoted at the end of its window with the dashboard restarted in the middle of it;
  the second splits a real nginx route 30%, reads `rel=` from its access log and moves in and out of the
  canary under traffic without losing a request
  ([canary releases](docs/internal/deployments/canary-and-replicas.md#canary-releases)).
  Changes to skew protection — the `jd_release` cookie, a route keeping a warm release for its visitors,
  the warm release kept running and taken off the route (`deploy/skew.go`, `SiteSplit`, `caddyReleases`,
  `DeploymentSkew`) — also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveSkewProtectionKeepsAVisitorOnTheirRelease -count=1 -v`
  and `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveSkewProtectionPinsAVisitorOnNginx -count=1 -v`:
  a visitor with the previous release's cookie stays on it after a cutover, a fresh one gets the new
  release, and after the window both do, through a real Caddy and a real nginx
  ([skew protection](docs/internal/deployments/canary-and-replicas.md#skew-protection)).
- Changes to the warm-release window, instant rollback or the rollback watcher (`deploy/rollback_*.go`,
  `api/handlers_deploy_rollback_watch.go`) also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveMetricGatedRollbackSwitchesBackToTheWarmRelease -count=1 -v`.
  It starts a Caddy ingress of its own published on a loopback port and named to the server
  (`JD_INGRESS_CONTAINER`, so nothing binds 80 or 443), a webhook receiver, and a real engine and
  watcher; deploys a healthy release, then one that passes readiness and answers 500 to everything
  else, restarts the dashboard in the middle of that release's window, drives requests through the
  ingress, and requires the automatic switch back to the first release's same, paused-then-resumed
  container, the `release.rolled_back` delivery and its audit; then deploys a healthy third release and
  requires the warm container removed when its one-minute window passes. It numbers its environment
  far from any other and removes its containers.
- Changes to the traffic replay (`deploy/replay.go`, the `replay_traffic` step, `runtime.replay`, or the
  request record it reads) also run
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveTrafficReplayStopsACandidateThatBreaksARecordedRoute -count=1 -v`
  as root on a Docker host. Like the rollback acceptance it starts a Caddy ingress of its own on a
  loopback port (`JD_INGRESS_CONTAINER`) and deploys `caddy:2-alpine` releases through the real routes
  and engine: a first release whose replay skips for want of a record; thirty pages, a HEAD and a
  write sent through the ingress; a release that answers 404 for one of those pages, which must end
  `replay_status_drift` in `replay_traffic` with nothing cut over, the first release still answering
  every page, its own container gone and the record holding only the test's requests (the replay never
  passes through the ingress); then a release that answers like the first and goes live. It numbers
  its environment far from any other and removes its containers.
- Changes to the engine, its reconciler or run persistence also run
  `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveBackendKillAtEveryStepLeavesOneRecoveredRun -count=1 -v`.
  It builds `internal/deploy/testdata/engine-host`, SIGKILLs that real second process inside every
  step in turn — of a deploy's step list and of a promotion's, which starts at `render_runtime` — and
  requires that the restarted engine ends the run with `restart_evidence_missing`, leaves no claimable
  run behind, and still has the killed process's step output.
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
  Go test run fails if the changelog's newest release and the version ever
  disagree, in either direction. It does not compare the markdown's entries, so a
  note added after the version was bumped reaches `CHANGELOG.md` only when
  `scripts/release.sh` runs again with the version the branch already carries,
  which rewrites nothing else.
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
| `JD_TEST_MYSQL8_DSN` | MySQL 8 itself; `JD_TEST_MYSQL_DSN` is MariaDB in every suite | workbench, catalogue, operations, transfer, seed data |
| `JD_TEST_MYSQL_ADMIN_DSN`, `JD_TEST_MYSQL8_ADMIN_DSN` | a login that may create and drop databases and accounts on those two servers (falls back to `root` on 3306 in the drop and credential tests) | drop, transfer, operations, catalogue, accounts, encrypted backups, statement history, stored programs, the streamed copy from MariaDB to MySQL |
| `JD_TEST_ORACLE_ADMIN_DSN` | an account that may read the `V$` views and create users (falls back to `system` on 1521 in the drop and credential tests) | sessions, locks, dumps, imports, drop, statement history |
| `JD_TEST_B3_MSSQL_DSN`, `JD_TEST_B3_ORACLE_DSN`, `JD_TEST_B3_ORACLE_ADMIN_DSN`, `JD_TEST_B3_MYSQL8_ADMIN_DSN` | the operations suite's own servers, read before the shared variable of the same engine | `dbx/ops_live_test.go`, `api/handlers_db_ops_live_test.go` |
| `JD_TEST_MARIADB_DSN`, `JD_TEST_VALKEY_DSN`, `JD_TEST_KEYDB_DSN`, `JD_TEST_DRAGONFLY_DSN` | one server of each flavour | flavour detection, the capability flags against real servers |
| `JD_TEST_MONGO_RS_DSN` | a MongoDB replica set the run owns: accounts and views are made in `admin` | replication, accounts, the credential guards |
| `JD_TEST_MONGO_AUTH_DSN` | a MongoDB with access control on | `TestLiveMongoSignInIsNotAPing` |
| `JD_TEST_POSTGRES_HYPOPG_DSN` | a PostgreSQL with the `hypopg` extension created in the database the DSN names (`postgres:17` plus `postgresql-17-hypopg`, then `CREATE EXTENSION hypopg`); the tables the tests make are named `jdt_index_advice_…` | the what-if index on the Explain view: `TestLiveHypotheticalIndexChangesThePlanAndNothingElse`, `TestLiveIndexAdviceThroughTheRouter` |
| `JD_TEST_REDIS_ADMIN_DSN` | a Redis the run owns outright: configuration, users, slow log, clients, MONITOR, pub/sub | `TestLiveRedis*`, `TestLiveAPIRedis*` |
| `JD_TEST_REDIS_OWN_DSN` | a Redis whose numbered databases the run may flush | dumps of every numbered database |
| `JD_TEST_POSTGRES_SUBSCRIBER_DSN` | a PostgreSQL the run owns, with a superuser login, that reaches `JD_TEST_POSTGRES_DSN` at the address that string gives — a container on the host's network, because a subscription is dialled by the subscribing server and the publisher's address means itself from inside the publisher's container. `JD_TEST_POSTGRES_DSN` must run with `wal_level=logical` and a login with the REPLICATION attribute. The tests name everything `jdt_online_import_*` and drop their publications, slots and subscriptions | `TestLiveOnlineImport*` |
| `JD_TEST_REDIS_FLAVORS` | `valkey=redis://…,keydb=redis://…,dragonfly=redis://…,redis=redis://…` | the Redis surface on each fork |
| `JD_TEST_REDIS_CLUSTER_DSN` | a node started with `--cluster-enabled yes` | cluster notices |
| `JD_TEST_REDIS_SENTINEL_DSN` | a Redis Sentinel | sentinel notices |
| `JD_TEST_REDIS_REPLICA_DSN` | a replica of the admin server | replication |
| `JD_TEST_ORM_MSSQL_DSN`, `JD_TEST_ORM_ORACLE_DSN`, `JD_TEST_ORM_MYSQL8_DSN`, `JD_TEST_ORM_COCKROACH_DSN` | a database the generator tests may create and drop their schema in | `dbx/orm_live_test.go` |
| `JD_TEST_ORM_POSTGRES_OLD_DSNS` | older PostgreSQL releases, separated by spaces | `TestLiveORMOlderPostgres` |
| `JD_TEST_POSTGRES_FULL_DSN` | a server started from the full template's image (`jd-postgres-full`, built from `backend/internal/api/recipes/postgres-full`) on its own command, as an account that may create databases, connected to the database it was created with; the test makes and drops one database of its own and leaves `pg_cron` as it found it | `TestLivePostgresFullCarriesItsExtensions` |
| `JD_TEST_S3_ENDPOINT`, `JD_TEST_S3_ACCESS_KEY`, `JD_TEST_S3_SECRET_KEY` | an S3-compatible service (MinIO, versitygw) and keys that may create buckets; the test makes one named `jdt-backup-encryption-…` and empties and removes it | `TestLiveEncryptedArchiveRoundTripsThroughObjectStorage` |

Some tests reach past a database to the machine and run only when asked:

| Variable | What it does |
| --- | --- |
| `JD_TEST_INVENTORY_LIVE=1` | reads this machine's real Docker daemon, sockets, units and files (`TestLiveInventoryNeverCarriesAContainerSecret`); it only reads |
| `JD_TEST_INVENTORY_CONTAINER=<name>` | signs in to that one container and nothing else (`TestLiveInventoryConnectsAContainerByKey`) |
| `JD_TEST_PROVISION_ENGINES=redis,valkey,clickhouse:26.3` | starts each named template, connects it the way the page does and turns it off and on again (`TestLiveProvisionAdoptAndPower`), and starts each under a memory and a processor limit, raises them in place, replaces the container and lifts the processor limit (`TestLiveProvisionedLimitsAreAppliedInPlaceAndSurviveAReplacement`, never more than 512 MiB a server); both create and remove their own containers and volumes. A template whose image is built on the server (`postgres-full:18`) is built first through `POST /databases/provision/build`, and the image is left for the next run |
| `JD_TEST_POWER_REDIS_DSN` | a Redis in a container the test may stop and start (`TestLivePowerIsReadInFlightOnARealContainer`) |
| `JD_TEST_STATEMENTS_CONTAINER=<name>` | starts PostgreSQL servers of its own, one at a time, as `<name>-postgres`, `-pgvector`, `-postgis`, `-timescaledb` and `-setup` with a `-data` volume each: every PostgreSQL template counting its statements from its first start (`TestLiveTemplatesCountStatementsFromTheFirstStart`), and the preload, restart and extension on a server that never loaded the module (`TestLiveStatementsSetupKeepsTheLibrariesAServerLoads`); it removes each container and volume when its part is done |
| `JD_TEST_PRELOAD_POSTGRES_DSN` | a PostgreSQL in a container of its own, with `preload` in its name and nothing on its command line about the libraries it loads, signed in to as a superuser: the test adds libraries to `shared_preload_libraries`, restarts it through the power route, creates the extensions and puts it back as it was (`TestLivePreloadRestartAndCreate`); pg_cron is used where the image has it |
| `JD_TEST_UPGRADE_LIVE=1` | starts a PostgreSQL 16 of its own from `postgres:16-alpine` and moves it to 17 and back (`TestLiveUpgradePostgres16To17`): check, upgrade, revert, recovery of an interrupted upgrade, upgrade again and finish; it needs both images and removes the `jdt-major-upgrade-pg…` containers and volumes it makes |
| `JD_TEST_HOST_PG_PORT=<port>` | a PostgreSQL installed on the host, run as root: the host account bootstrap (`TestLiveHostPostgresAccount`) and the log sources read off `/proc` (`TestLiveHostDBLogSources`) |
| `JD_TEST_WALG=<path>` | a WAL-G release binary, the one `backend/Dockerfile` pins: provisions a PostgreSQL 17 through the route, archives it into a directory, recovers copies to a moment and to a restore point, switches one in, turns archiving off and deletes an archive (`TestLivePointInTimeRecoveryIntoADirectory`); it creates and removes its own `jdt-pitr-postgres-…` containers, volumes and directory |
| `JD_TEST_PITR_S3=http://key:secret@host:port/bucket` | with `JD_TEST_WALG`, the same through an S3 store the run owns — a MinIO container on Docker's network, so the server's container reaches it — including a recovery while the server is stopped and one past the end of the archive (`TestLivePointInTimeRecoveryIntoS3`) |

The ones that need Docker (the inventory, provisioning, power, preload, statements and point-in-time recovery and upgrade tests) read
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
`database-fleet-backups.spec.ts`, `database-mcp-server.spec.ts` (the MCP server dialog: its keys and
the databases and deployments each reaches), `database-home.spec.ts`, `database-data.spec.ts`,
`database-query.spec.ts`, `database-query-bodies.spec.ts` (stored programs and script separators in
the editor), `database-explain.spec.ts` (the Plan tree's measured, estimated and printed plans),
`database-assistant.spec.ts` (the editor's assistant, its answers streamed by the mock),
`database-schema.spec.ts`, `database-programs.spec.ts` (functions, procedures and triggers), `database-rls.spec.ts` (row-level
security, the role test, and function and column grants), `database-redis.spec.ts`, `database-mongo.spec.ts`,
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
