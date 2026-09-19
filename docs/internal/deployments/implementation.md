# Deployment implementation

`internal/deploy` owns desired deployment configuration, immutable releases, persistent runs,
steps, queue leases, sequenced events, triggers and cross-feature relationships. It orchestrates narrow
interfaces from Docker, Git, Proxy, Backups, Metrics, Logs, Files and Terminal; those packages remain the
only renderer/executor/validation authority for their feature.

- A run and its full step list commit before an enqueue answers `202`. Environments serialize work;
  random claim tokens and expiring leases fence stale workers. Restart recovery uses stored step evidence
  plus owning-feature evidence and never guesses that a non-idempotent side effect is safe to repeat.
- Worker capacity defaults to one heavy and two light slots and is bounded by
  `JD_DEPLOY_HEAVY_SLOTS` / `JD_DEPLOY_LIGHT_SLOTS` (1..8). Claims expire after
  `JD_DEPLOY_LEASE_TTL` (30s by default, 5s..5m); these are boot-time settings, not mutable project data.
- The only activation strategies are `blue_green` for an eligible proxy-owned stateless HTTP service and
  `stop_first` for Compose, fixed-port, game and exclusive-storage workloads. A failed candidate cannot
  replace the live release; ambiguous cutover evidence restores or stops for operator recovery.
- Events commit before publish and are monotonically sequenced per run. Reconnect resumes after a sequence;
  compacted history begins with a `resync` snapshot, and a slow subscriber is disconnected rather than
  allowed to stall execution.
- `deploy_projects.id` remains the deployment identity. The additive normalized schema and compatibility
  migration retain legacy route/hook/env/history behavior while the persistent engine and UI replace it.
  `PUT /deploy/{id}` keeps its legacy Compose contract byte for byte for a full body — every field
  present replaces `repoPath`/`branch`/`composeFile`/`preCommand`/`postCommand`/`enabled` exactly as
  before — but a body naming only `name` instead renames a project (legacy or normalized) without
  touching any other column: the same name rule draft intent validation uses, uniqueness enforced by
  the existing unique index on `deploy_projects.name` (archived projects hold only an internal
  tombstone there, never their display name), and a taken name answers `409 name_taken`. The rename shape
  refuses an archived project outright with `409 project_archived` rather than writing the requested name
  over that tombstone — a project's display name comes back only through `POST /deploy/{id}/unarchive`.
  Both shapes audit as `deploy.project.update`.
- Deployment-selected paths use a dedicated `files.Service` scoped to `JD_DEPLOY_ROOTS`; Git, Docker,
  Compose and builder argv use `hostexec.CommandInDir`. The sole shell boundary remains an immutable,
  admin-authored stored release task (including migrated pre/post commands) with a resolved working
  directory and explicit scoped environment.
- Creation is a revisioned, owner-scoped server draft. `GET /deploy/drafts` (session) lists the caller's
  own uncommitted, unexpired drafts newest first — id, name from intent, a one-line source summary,
  `currentStep`, `updatedAt`, `expiresAt` — so the new-project page can offer to resume one instead of
  starting a new setup over; a committed or expired draft has nothing left to resume and is excluded.
  Remote Git inspection resolves an exact ref into a
  private dashboard-owned bare mirror and detached temporary worktree; it never clones into or resets an
  operator checkout. Inspection's mirror is shallow and blob-filtered so planning stays bounded, so a
  release materializes through a second mirror that is neither: the run workspace is built by fetching the
  recorded object id into a fresh repository, because `git clone --local` ignores its local copy when the
  source is shallow and would silently produce an empty checkout. Registry inspection resolves a digest
  without pulling. Planning-time Compose validation uses private temporary files, an explicit empty env
  file and inert placeholders for detected variable names, so the backend environment and a checkout
  `.env` cannot influence the result.
- `/deploy/new` is one page. Unfinished setups (`GET /deploy/drafts`) are offered for resumption at
  the top; a source strip offers a Git repository (connected GitHub list or a pasted URL), a Docker
  image (images already on the server or a reference), a reviewed template, a database, a Compose
  stack (paste, upload, Git, local) and an existing workload (container, stack, checkout). Choosing a
  source creates a draft, saves the intent and source, and runs detection in one action; when
  detection is ambiguous the candidates are offered as a choice that re-runs detection with
  `selectedId`. The configure form then holds the name, the type, the detected build and output
  settings, environment variables, an optional database, the public address (with the hostname
  suggestion and certificate readiness) and an Advanced disclosure carrying the runtime, health check,
  storage, release task, build secret and container fields. Deploy saves the configuration, runs
  preflight, commits, imports the environment text and enqueues the first run; Save only stops after
  the import. The saved draft revision is adopted before preflight, so a failed preflight never
  strands the draft, and a `draft_revision_conflict` re-reads the draft once. `?draft=` resumes a
  draft (including one produced by `POST /deploy/{id}/duplicate`), `?mode=advanced` opens Advanced,
  and existing workloads adopt through `/deploy/import/adopt` without a run. Saved credentials are
  picked from `GET /deploy/credentials`. Environment text never enters the URL or browser storage.
  The environment section opens with the variables detection found the source reading — the template's
  example as the placeholder, the file it was read from beside the key — and a detected row left empty
  is skipped at submit rather than set to nothing; each database the source connects to is one button
  that opens the database sheet on that engine and variable. The framework is named as its own
  documentation spells it (`frameworkLabel`), a Python recipe shows its interpreter field, and static
  output shows the single-page switch. `?repo=<clone url>&ref=<branch>` arrives on the Git tab with
  the URL filled in (only an `https://`, `ssh://` or `git@` URL is accepted), which is what a deploy
  link in a README points at.
  Reviewed blueprints deploy as image releases (below); game-server blueprints and blueprints that
  install configuration files or downloaded artifacts stay preview-only, the catalogue names the
  reason per blueprint, and direct API calls are refused with the same reason before deployment work
  is persisted. The screens share `deployment-defaults.ts` and `deployment-findings.tsx` so a default
  one flow relies on cannot be missing from the other, and a refused plan reads identically in both.
  Detected web and static plans carry a required HTTP readiness check: preflight raises
  `readiness_missing` as a *decision* for those profiles, so an empty check list made the most
  ordinary deployment there is unsavable over a control the operator was never shown.
- A draft route distinguishes why an id did not answer: `403 draft_forbidden` for another user's draft,
  `410 draft_expired` for one past `expiresAt`, and `404 draft_not_found` only for an id that names
  nothing at all — the first two used to collapse into the third, which hid an ownership refusal behind
  the same message as a typo'd id. `POST …/drafts/{draft}/detect` with a `selectedId` absent from the
  detected candidates names the id it did not recognize in its `422 invalid_plan` message instead of
  repeating the generic "invalid deployment plan" text. `GET …/variables/{name}/reveal` for a name that
  exists in no active variable answers `404 variable_not_found`; a name that fails `ValidateEnvKey`
  outright (malformed, not merely absent) stays `400 invalid_variable`.
- `GET /git/github/repos` and `GET /git/github/branches` list what the dashboard's own gh credential can
  reach, so a repository is chosen rather than transcribed. Both take no `?path=`: the credential
  consulted is the one every deployment clone will use, and the repository being chosen has not been
  cloned onto this server yet. A repository name is validated as owner/name before it reaches gh's argv
  or a REST path.
- `GET /deploy/hostname` proposes a public address and reports what HTTPS would take. A wildcard
  certificate the host already holds wins; failing that the answer is `<slug>-<suffix>.<a-b-c-d>.sslip.io`,
  which resolves to this server with no DNS record to create. The suffix is
  `HMAC-SHA256(sha256("jd-derive-v1" || masterKey), "deploy.hostname" || 0x00 || ToLower(TrimSpace(name)))`
  truncated to three bytes, not a random draw, so the same name proposes the same hostname on every call —
  a screen that re-fetches must not see a different address. If that
  deterministic hostname is already a managed domain of another active project, the HMAC input gains a
  `#2`, `#3`, … counter and is recomputed until a free one is found. `covered` is whether a certificate
  already covers the name — the only thing activation accepts — and `certificateMethod` is the HTTP-01
  challenge this host could issue one with. Both are reported so the screen can say what the run will do,
  not so the operator has to do it.
  Address selection excludes private, loopback, link-local and CGNAT (`100.64.0.0/10`) addresses,
  including Tailscale addresses. Go's `IsGlobalUnicast` and `IsPrivate` alone do not exclude CGNAT.
  A host without a public interface address gets no generated fallback; hosts behind provider NAT
  need an operator-supplied domain pointing to the provider's public address.
- `provision_certificate` is a step of the release path, ordered after `backup_gate` and before
  `start_candidate`. It reads the frozen release snapshot, and for every planned HTTPS domain resolves an
  existing certificate or orders one through `proxysvc.EnsureDeploymentCertificate`. Its position is the
  safety property: a run that cannot obtain the certificate its own plan asked for stops with no
  candidate container to stop, restore or explain. A plan with no HTTPS domain is `skipped`; a host
  without certbot is `unavailable` (`certificate_unavailable`), which is terminal but is not a failure of
  the deployment's own work; a refused order is `failed` (`certificate_issue_failed`). Redeploy and
  rollback carry the step; restart and preview-remove do not, because neither applies a route.
  Issuance is HTTP-01 only and never a wildcard — Let's Encrypt will not sign one that way — is bounded
  at five minutes, is serialized against route application so two runs cannot race one account into a
  rate limit, and passes `--keep-until-expiring` so a redeploy inside the renewal window asks the
  authority for nothing. A first order on a host with no ACME account registers without a contact
  address; an operator who wants expiry mail registers once on the Certificates page and every later
  order reuses that account. certbot's exit code is not the evidence: the certificate is read back and
  must cover the exact names before the step passes. Step evidence carries the lineage name, method and
  outcome, never a key path or key material.
  Before a new HTTP-01 order, a bounded DNS lookup rejects unresolved names and non-public destinations
  with a corrective message, including old saved sslip.io names based on a Tailscale address. Existing
  certificates are reused before this check, so private services can still use a certificate provisioned
  through DNS-01 for a domain the operator controls. Public DNS alone does not prove inbound port 80 is
  reachable; Certbot remains the validation authority. Errors retain ACME `Detail:` messages or the
  underlying failure line instead of Certbot's generic help footer. Correcting a saved hostname requires
  a new release; retrying an old run retains its frozen hostname.
- HTTP-01 selection consults both Certbot's actual plugins and TCP port 80 ownership. A running host
  nginx uses `webroot` at `/srv/just-dashboard-acme`, shared by the host and backend container. For new
  names, Proxy snapshots and installs a temporary challenge-only site, validates and reloads nginx,
  then reads back a random probe file through each existing listener with the requested Host header.
  Listener bind scope is preserved. Asynchronous nginx reloads are given a bounded probe retry.
  Existing sites that already serve the shared challenge path are reused; conflicting sites are never
  overwritten. Cleanup restores the snapshot using a cancellation-independent context and a cleanup
  failure prevents a successful certificate step. This verifies local routing, not public reachability.
  Deployment TLS routes retain that webroot for renewal; `SiteSpec.managedAcme` and the site parser
  preserve it through Proxy form edits, while ordinary sites keep their existing `/var/www/html` default.
  Standalone is selected only when no TCP port 80 listener is observed. Other or unidentified owners
  produce an actionable error instead of an attempted bind or a stopped server. Hostname suggestions
  use this same selector. `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDeploymentWebroot
  -count=1 -v` verifies real nginx routing, continued service and cleanup on an isolated loopback port.
- The workspace header actions menu exposes **Archive deployment** for the destructive capability on both legacy and
  normalized projects, disabled during an active run. It uses the existing `DELETE /deploy/{id}` archive
  contract with ordinary confirmation, returns to the fleet after success, and keeps the dialog open
  on error. The confirmation explicitly states that history, runtime, routes and data are retained;
  Settings → Danger zone provides the separately previewed path for removing managed resources.
- Activation is unchanged and still the last word: it resolves an already-issued certificate/key pair
  through Proxy and fails closed if none exists. `provision_certificate` only makes that resolution
  succeed for a name the operator has just asked to publish.
- `GET /databases/{id}/url` is an admin read with its own audit entry and `Cache-Control: no-store`.
  Its default (or `?target=host`) returns the saved connection unchanged; `?target=public` swaps a
  loopback host for the machine's public address (see the databases guide). `?target=container` prepares
  an application URL: a known database's loopback binding is matched to its Docker container and
  internal port, with a stable `db-ID.jd.internal` hostname. The binding must match the saved loopback IP; ambiguous
  localhost bindings are refused. It never publishes a new port, attaches networks, or changes the
  saved connection. An unreachable loopback service and SQLite fail with an actionable error.
  Remote URLs retain their credentials, options, SRV discovery and seed lists. Plain TCP MySQL driver
  strings become `mysql://` URLs; driver-specific query/socket options require manual configuration
  instead of being silently discarded. Setup saves a typed database reference; activation joins an owned
  environment network and reconciliation repairs the DNS alias after a matching container replacement.
  Existing literal IP variables require reconnecting once. See [database networks](database-networks.md).
- Detected JavaScript commands name the package manager the checkout's lockfile locks to
  (`bun`/`pnpm`/`yarn`/`npm run …`). Competing lockfiles resolve only through the explicit build setting
  or `package.json` `packageManager`; changing the setting in the UI rewrites plain `<manager> run
  <script>` commands to the new runner. The recipe picks its base image from that same lockfile, and
  `oven/bun` carries no npm, so a hardcoded `npm run build` was a build that installed cleanly and then
  died on `npm: not found`.
- Detection is a catalogue, not a handful of special cases. `deploy/frameworks_node.go` names Next.js,
  SvelteKit, Astro, Nuxt, Remix, React Router, SolidStart, TanStack Start, Nitro, Angular, NestJS,
  Gatsby, Docusaurus, VitePress, Eleventy, Create React App, Vue CLI, Ember, Parcel and Vite, in an
  order where a meta-framework built on Vite is read before Vite itself, each with its serving mode
  (site output or server start command), port, runtime environment and the entry file the generated
  Dockerfile checks after the build. `frameworks_python.go` names Django, FastAPI, Flask, Streamlit and
  Gradio from the manifests and finds the application object in the conventional entry files;
  `frameworks_rust.go`, `frameworks_java.go`, `frameworks_dotnet.go` and `frameworks_deno.go` read
  `Cargo.toml`, `pom.xml`/`build.gradle(.kts)`, `*.csproj` and `deno.json(c)`. A `Procfile`'s `web:`
  process outranks every guess. The candidate carries `spaFallback` (a client-routed site's nginx
  fallback), `pythonVersion`, `unpinnedDependencies` (a `dependencies_unpinned` preflight warning,
  never a refusal), `variables` (the environment names the source reads, with example values and
  where each was read — `env_discovery.go`) and `databases` (the engines its dependencies and
  documented URLs name, each with the variable the connection belongs in). The closed recipe set is
  `node`, `go`, `python`, `rust`, `java`, `dotnet`, `deno` (`validRecipe`), and `build.pythonVersion`
  and `build.spaFallback` are the two additive plan fields, bounded by `PlanConfiguration.Validate`.
  The contract per language is [the recipe guide](recipes.md).
- A detected Node service that declares a migration tool applies its schema before it serves. Detection
  records the tool (`schemaTool`), the command it chose (`schemaCommand`) and whether the package's own
  start script already runs it, and chains the step in front of the start command through the manager's
  binary runner (`npx`/`bunx`/`pnpm exec`/`yarn`). Prisma and Drizzle deploy committed migrations and
  otherwise push the declared model; Knex, Sequelize and MikroORM run their migration command; TypeORM
  is recorded as a decision because its data source cannot be guessed. The rule set lives in
  `deploy/schema_tools.go`. Preflight adds `schema_step_missing` (warning, on the start command) when a
  database is linked and neither the start command, a release task nor the start script runs the tool,
  and `schema_step` (pass) when one does; no linked database means no finding. Changing the package
  manager rewrites the chained binary runner with the script runner.
- Deployment preflight depends on a read-only observer: filesystem/proc capacity, listener inventory,
  Docker/Compose availability, proxy inventory and bounded DNS lookups. It cannot build, pull, start,
  stop, write proxy/firewall configuration, modify a checkout or enqueue a backup. The persisted exact
  plan excludes raw observed import material and accepts only typed secret references.
- Normalized build execution uses the project-owned versioned recipe set or an explicit Dockerfile,
  static, immutable-image, or Compose adapter. Reviewed base tags are resolved before rendering and every
  generated `FROM` is digest-pinned. Build secrets are BuildKit environment-backed secret mounts and
  never argv/build args; custom Dockerfiles with requested secrets or obvious embedded credentials fail
  closed because their layer history cannot be guaranteed.
  Recipe build scope now supplies values automatically, with explicit install-stage restrictions for
  package credentials. Serving defaults per framework, the Python install shapes and interpreter
  selection, the Rust, Java, .NET and Deno recipes, the single-page fallback and Go version/command
  behavior are defined in [the recipe contract](recipes.md), including the exact limits of live
  framework verification.
- Enqueue atomically freezes exact variable revision ids plus canonical dependency/check JSON. The header
  exists for an empty set, digests are checked before variable decryption, and retry copies the original
  snapshots instead of observing later rotations. Release runtime snapshots store the actual secret-free
  JSON bytes and verified digest, not a pointer back to mutable desired configuration.
- Release tasks are named, bounded shell gates over the immutable source workspace. Only variables
  explicitly declared with `release_task` scope enter their environment; output is exact-value and
  credential-pattern redacted before persistence. Interrupted tasks stop for operator review because
  their side effects cannot be inferred safely.
- Artifact retention keeps the live release, five prior successful rollback releases, candidates, pins,
  retain-until windows, recent failed diagnostics, shared physical digests, and every environment under
  an active deployment lease. Cleanup reports the reason for every retained row and removes a mutable
  Docker tag only after inspection proves it still names the recorded config/digest. A run that fails
  after `render_runtime` moves its candidate release to `failed` the moment the run itself reaches
  `failed`, `failed_activation`, `rolled_back` or `cancelled` — `TransitionRun` does it in the same
  transaction as the run's own state change — so it leaves `candidate` (which retention always keeps,
  reading as a build still in progress) and enters the seven-day failed-diagnostic window like any other
  failed artifact instead of being retained forever.
- Runtime activation consumes only an immutable release snapshot. Direct containers use the recorded
  config digest (or repository plus manifest digest); Compose uses an explicit stable project, the exact
  source file list, and a generated `0600` override that pins every service image with `pull_policy:
  never`. Compose interpolation receives only the frozen runtime scope through a temporary `0600` env
  file which is deleted on every exit path.
- Archived projects keep their original display name in additive `archived_name` storage while the
  unique database name becomes an internal tombstone. Archiving and upgrading previously archived
  projects release the live name without deleting history or resources. New projects from the same
  repository receive independent identities. Each project displays its own persistent run sequence,
  starting at **Run #1** across its environments and operations. Existing history is numbered in run-ID
  order on upgrade; a SQLite insert trigger allocates subsequent numbers atomically for both legacy
  and normalized runs. Global run IDs still identify API routes, links, events and audit evidence.
- The runtime plan carries optional resource limits — `memoryMb`, `cpus`, `pidsLimit` — and a
  `restartPolicy` from the closed set `unless-stopped` (default), `always`, `on-failure`, `no`. Zero means
  Docker's own unlimited default. Validation bounds them (16 MiB–4 TiB, 0.01–1024 CPUs, 16–1,048,576
  processes); preflight warns when a limit exceeds the host's available memory or CPU count; the Docker
  runtime owner passes them to container creation and release comparison labels them. Compose services keep
  the limits their files declare.
- A failed readiness or smoke gate first asks the runtime owner to diagnose the candidate: container
  state, exit code, OOM flag, restart count and a bounded log tail (200 lines, 32 KiB, most recent kept).
  The tail is written to the run transcript after redaction of every runtime variable value; step
  evidence records state and counts, never output. Compensation runs afterwards, so the operator reads
  why the application never listened instead of only "could not connect". The step message points at the
  transcript when output was captured.
- Container applications receive `PORT` from the frozen internal-port setting unless a runtime variable
  explicitly supplies it. Compose and host-network applications keep their own environment conventions.
  This keeps application startup aligned with Docker publication; the host port may still move.
- HTTP/TCP readiness checks follow the recorded runtime publication when a saved check refers to the
  primary service's original internal or requested host port. Explicit unrelated ports, remote hosts
  and full URLs retain their configured targets. Quick deploy leaves the check port unset to follow
  allocation. Failed gates log the check name, safe address, attempt count and specific status/reason;
  saved older failures derive the same explanation from retained check evidence when read.
- HTTP, TCP, Docker-health, command and public-route checks have closed configuration, per-attempt
  timeouts and bounded retries. Persisted evidence contains status/state/error codes and a digest of
  bounded command output, never response bodies, command output, URL credentials/queries or runtime
  variable values. Disabled, unavailable, warning, passed and failed remain distinct outcomes.
  Default HTTP checks require a final 2xx response and follow at most four redirects on the same
  origin. External redirects and loops fail, so a candidate cannot pass by redirecting to the old
  public release or to a page that returns 500. Explicit expected-status lists retain exact-status,
  no-redirect behavior.
- Blue/green is limited to stateless proxy-owned HTTP candidates without fixed ports, host networking or
  writable mounts; dynamic candidate ports are loopback-leased. Fixed-port, Compose, game and exclusive
  writable-storage plans are honest `stop_first` deployments and advertise expected downtime. A route
  moves only after required readiness/smoke checks pass.
- Deployment-owned nginx cutover is serialized and snapshots the prior bytes, mode and exact symlink
  target. Apply/reload/verification failure restores, reloads and verifies that exact snapshot before a
  run may report recovery. The snapshot content is held only for compensation; persisted activation
  evidence carries its digest, not the configuration bytes. The rendered nginx route proxies each
  request over a fresh upstream connection (no `upstream` block, so no upstream keepalive): under
  sustained traffic in the high hundreds of requests a second the proxy's side of every closed
  connection sits in TIME_WAIT, its ephemeral ports run out after roughly half a minute, and every
  proxied request answers 502 until they drain. The cutover continuity test throttles its own
  clients to stay under that ceiling; the ceiling itself is a known limit, not a cutover defect.
- Stop uses the configured signal and a bounded grace period before Docker escalation; predecessor drain
  and activation/cancellation compensation use bounded contexts detached from request cancellation. A
  stop-first failure restarts the predecessor from its exact immutable runtime spec. Cancellation between
  persisted steps removes an uncommitted candidate, or finishes predecessor retirement after a committed
  cutover, and records sequenced cleanup evidence.
- Deploy and force-build resolve the current desired revision (force-build disables cache); redeploy and
  rollback clone only available immutable artifacts and traverse the same checks/cutover path; restart
  stops and starts the existing live runtime without creating a release, then verifies the plan's
  readiness and smoke checks against that live release before recording it. Rollback, stop and restart use
  the destructive capability and ordinary confirmation, not a typed phrase; because stop and restart share
  their enqueue route with deploy and redeploy, which stay at `service.control` alone, the handler checks
  the capability and the shared destructive rate budget by hand instead of wrapping the whole route in
  `s.destructive`. Stop and start each perform one half of a
  restart against the same live release: stop stops the runtime in place and records it `stopped`,
  skipping every other step but `record_release` and `notify`; start starts an already-stopped runtime,
  verifies the plan's readiness and smoke checks against the live release exactly as restart does, and
  records it `live` again. Both are light-slot manual actions that refuse a release target that is no
  longer live, refuse a legacy Compose or preview environment, and refuse a runtime that is not in the
  state the operation expects (`already_stopped`, `not_stopped`); a start that fails leaves the runtime
  recorded stopped, since no compensation is possible. The fleet and workspace read models report the
  live runtime's stopped state as `stopped`, folded into the existing batched runtime join, and the Git
  watcher persists the decision reason `stopped` instead of enqueueing an automatic deployment while the
  live runtime is stopped; a manual deploy remains allowed and, on success, leaves the new release live
  and running. The watcher's cursor still advances with each push while stopped — unlike its `manual_only`
  pause, which leaves the cursor at its prior revision — so a commit pushed during that time is deployed
  by the next push, not retroactively by `start`.
- `PlanConfiguration.Validate`'s most common refusals — runtime ports (0 is unset and always allowed; the
  message no longer implies otherwise), a runtime mount, a dependency, a domain, a variable (keyed by its
  own name, not a position) and a check or release task (keyed by array index) — wrap a typed
  `ValidationError{Field, Message}` behind `ErrInvalidPlan` (`fmt.Errorf("%w: %w", ErrInvalidPlan,
  validationErr)`, so an existing `errors.Is(err, ErrInvalidPlan)` check is unaffected). `PUT
  …/configuration` and the draft configuration step surface it as `{"error":{"code":"invalid_plan",
  "message":…, "field":"runtime.internalPort"}}`, so the UI can attach the refusal to the one control it
  is about instead of a plan-wide banner; a refusal outside those categories still has no `field`.
- Normalized configuration edits are desired state only. Saving runtime/domain/dependency settings or a
  variable clones the source/build/runtime rows into the next complete revision and never moves the live
  release pointer. Pending state compares that desired revision with the live release's exact plan and
  frozen variable/dependency/check snapshots, by names and digests only; a run clears only the revision it
  actually applied, so a change saved after enqueue stays pending.
- Deployment variables are encrypted, immutable revisions with an exact closed scope set (`build`,
  `runtime`, `release_task`). Lists use a fixed mask; reveal is a separate session-only admin read with an
  explicit audit entry. Bulk dotenv parsing is bounded and inert. Full typed references are parsed into a
  closed kind/target model; missing variable references and cycles fail before commit, secret leaves stay
  masked, and enqueue freezes exact variable revision ids so retries cannot observe a later variable
  rotation. Execution resolves external credential/database/domain/Compose-service references only through
  their owning stores; missing or ambiguous targets fail closed instead of reaching a workload as literal
  reference text. Run-scoped domain and Compose references use the frozen run plan/dependency snapshots.
- Domains, persistent storage, backup jobs and database entries remain resources of Proxy, Docker,
  Backups and Databases. Deployments store typed ownership links and use read-only owner observations for
  domain conflicts, DNS, existing certificate pairs, ports, public binds, firewall policy and dependency
  availability. A deploy re-runs those host observations from its frozen configuration in `analyze_plan`
  before build or backup work; only its exact managed proxy site and exact live Docker runtime may be
  treated as reusable ownership. HTTPS activation resolves an already-issued certificate/key pair through
  Proxy and fails closed if it no longer exists; deployment activation never invents certificate paths or
  performs issuance itself.
- A configured backup dependency executes as a step before candidate start. A linked database whose
  connection the backup job dumps natively is covered by that dump (`BackupGateEvidence.databaseDumps`
  lists it); only a database the job does not dump falls back to covering the engine's files. The
  Backups adapter verifies coverage, success, freshness and any required restore-test evidence and
  returns only bounded evidence;
  a required failure terminates the run before candidate startup or a live-pointer change. Restore evidence
  comes from the Backups owner's isolated application checker, bound to the exact artifact, image,
  schema, canary and completed cleanup. Missing or failed evidence blocks; it is never inferred from
  artifact existence. See [restore verification](restore-verification.md).
  Coverage resolves named volumes and every writable merged Compose service mount, verifies the immutable
  manifest and artifact checksum, and rejects filtered or uncovered data. See [backup coverage](backup-coverage.md).
- Import adoption is a dedicated, session-only admin commit that re-runs the read-only preview and requires
  exact acknowledgement of unsupported observations. It records the external resource as observed and
  does not start, stop, reset or claim it. Archiving disables deployment triggers, the project's schedules
  and visibility; it never removes runtime or data. Schedules are disabled directly (`ClaimDueSchedules`
  already excludes an archived *environment*, not an archived *project*, so without this a schedule kept
  firing and failing every occurrence at `environment_not_found`, invisibly, because even its summary
  marker run could not be enqueued); `POST /deploy/{id}/unarchive` (`system.admin`, session) restores the
  display name from `archived_name` — `409 name_taken` if another active project has since taken it — and
  clears `archived_at`, but leaves triggers and schedules exactly as disabled as Archive left them, audited
  as `deploy.unarchive`: undoing the archive is not a decision to resume automatic deployment, and the
  operator re-enables what they actually want running. A separate destructive route first returns a
  digest-bound, managed-only target list; data targets require their exact resource name and every removal
  is delegated to its owning feature and audited. Linked and observed targets never enter that plan. A
  remover's own failure — "Docker is unavailable", "remove application containers before removing their
  database network" — answers `409 removal_failed` (or `503` when the failure is specifically a missing
  owner) with that exact sentence and the partial `execution` (`removed`, `remaining`) beside the error,
  rather than collapsing to a generic `500`.
- `PUT /deploy/{id}/environments/{env}/releases/{release}/pin` `{"pinned": bool}` (`system.admin`, audited
  `deploy.release.pin`) sets `deploy_releases.pinned`, which `ArtifactRetentionPlan` already reads: a
  pinned release survives a prune plan regardless of its age or rank among the environment's other
  releases.
- Remote Git production branches default to monitoring after the first explicit deployment,
  including existing projects. The environment's [automatic deployment policy](git-policy.md) supplies
  manual-only mode and shared include/exclude filters for polling and push hooks. `GitWatcher` checks refs every five seconds
  with four bounded concurrent observations using the source adapter's credential isolation and
  `git ls-remote`. This works behind the dashboard's private network allowlist without public ingress
  or GitHub hook registration. Outages and slow Git reads can delay detection; it is polling, not an
  instantaneous push-delivery guarantee. Tags, local checkouts, legacy Compose and archived projects
  are excluded. Monitoring status and access failures appear in the project header's facts row and on Settings → General.
  `deploy_git_watches` persists the last observed revision, policy digest, decision reason and observation generation: restarts,
  failed runs, duplicate provider deliveries and a crash after enqueue cannot cause repeated builds;
  a later branch change (including a force-push back to an older commit) remains eligible. Watching
  never advances a live release pointer. Eligible web/static projects retain blue/green health-gated
  activation; exclusive-storage and other stop-first workloads retain their documented downtime and
  recovery contract. Archiving stops monitoring.
- Deploy/force-build source resolution records the current remote Git object in the additive,
  immutable `deploy_runs.source_revision` field before enqueue; execution derives its source identity
  and digest from that frozen revision. A manual `deploy`/`force_build` on a normalized project whose
  source is a remote Git repository may instead name `sourceRevision` (a 40- or 64-hex object id, frozen
  as-is) or `ref` (a branch or tag name, resolved through the same `git ls-remote` path
  `ResolveGitRevision` uses for the configured branch, `HostSourceAnalyzer.ResolveGitRef`) — never both.
  Anything other than a remote Git source refuses either field with `400 ref_not_applicable`; a name the
  remote does not have answers `400 ref_not_found` (git's own `ls-remote --exit-code` exit status 2, "read
  the remote fine, no such ref"), and a remote that could not be read at all answers `502
  source_unavailable`. The resolved revision and, when given, the requested ref name are recorded as
  `{"requestedRevision", "requestedRef"}` in the run's metadata so the UI can say "Deploy of v1.4.2". This
  never touches `deploy_git_watches`: that cursor is written only by the watcher's own poll/webhook path,
  so a manual deploy of an older commit cannot make the watcher believe the branch lives there and
  suppress its next real push. Retries copy it, while redeploy/rollback reuse their selected
  release's identity. Old runs keep their saved plan identity. The deprecated draft `autoDeploy` input
  remains accepted for compatibility but has no effect; draft commit no longer creates a hook without
  a secret or provider registration. Automatic runs are audited as `deploy.git.change` and use the
  same persistent queue, configuration/variable snapshots, checks and activation as manual runs.
  For a Git source, `acquire_source` additionally reads the frozen revision's subject, author and date
  with one `git log` against whichever repository it already has open — the materialized workspace, or
  a local checkout — and folds a `commit` object (`sha`, `subject` capped at 200 characters, `author`,
  `authoredAt`) into the run's metadata through `OrchestrationStore.MergeRunMetadata`, the one path that
  mutates `deploy_runs.metadata_json` after enqueue. An unreadable history logs a transcript line and
  leaves the field absent rather than failing the step; a retry's wholesale metadata copy, plus every
  run independently re-deriving the same commit from the same frozen revision, is what carries it
  forward through a retry, redeploy or rollback. `GET /deploy/{id}/commits` still reads a legacy
  project's local checkout only; a normalized Git-URL project's private release mirror keeps only
  narrow per-revision snapshot refs, not a maintained branch tip, so listing its history cheaply from
  that mirror remains open.
- Additional automation provider hooks verify each provider's exact raw-body signature before parsing and then fence
  event, repository, ref and delivery identity. The delivery row is reserved before preview or queue side
  effects, while legacy HMAC and scoped generic hooks retain their existing contracts. Environment branch/
  path policy applies to polling and provider/generic hooks; manual and rollback runs are not path-filtered.
  `GET …/triggers/{trigger}/deliveries` (`system.admin`, session) reads the last 50 `deploy_webhook_deliveries`
  rows for a trigger — delivery id, event, ref, decision, reason, run id, received at — and
  `POST …/triggers/{trigger}/rotate-secret` (same tier, audited `deploy.trigger.rotate_secret`) issues a new
  HMAC secret shown once, mirroring the legacy project's own secret rotation.
  A new *automatic* run only supersedes an older queued one in the same environment when its own
  `changedPaths` cover the older run's — an empty set can only cover another empty set — so the git
  watcher and provider webhook handlers record `run.metadata.changedPaths` only from the watcher's own
  complete baseline diff (computed when a watch-path filter is configured) instead of always freezing
  `[]`. The provider payload's own per-push commit list is not a baseline diff — two disjoint pushes each
  have a non-empty, non-covering list — so it stays scoped to matching the webhook's own watch-path filter
  and never reaches run metadata; leaving metadata unset in every other case (the common unfiltered push,
  and every preview push) reads as "unscoped" instead. A schedule's own dispatch never claims to
  know changed paths, so its `[]` reads as "unscoped": a routine scheduled action queued behind a real
  push no longer supersedes it, while a real push still supersedes a stale unscoped schedule ahead of it.
- The scheduler advances a persisted next-run claim atomically and executes a bounded, ordered action
  chain. Chain history stores only action/status/error-code/duration evidence. Preview environments require
  session administrator approval of each exact PR revision before creation or execution.
  `POST …/previews/approvals/{approval}/reject` (`system.admin`) moves a `pending` or `approved` row to a
  new closed state, `rejected`: `ApprovePreview`'s own `WHERE state IN ('pending','approved')` already
  refuses it afterward without any extra check, and the trigger's next delivery for that pull request
  inserts an independent new `pending` row at its own revision — a rejection does not follow it. They start without
  inherited variables, linked databases or release tasks; container mounts become fresh preview volumes on
  a dedicated bridge. Compose and host-access plans currently fail closed. PR close retires only owned
  resources and archives after successful cleanup. Reopening waits for cleanup and a new approval
  generation; closing cancels queued/build work. Startup quarantines older unsafe previews, stops owned
  containers without deleting data, withdraws routes, and blocks old releases from activation. Failed
  isolation retries while the UI reports the block. See [preview isolation](preview-isolation.md).
  Outbound notifications are delivered by engine run observers for every terminal outcome and for run
  start, through Discord, Slack, Telegram, e-mail or a signed webhook; a failed or cancelled run reaches
  the same channels as a successful one. Credentials are sealed and never listed, deliveries record only
  a status class and are deduplicated per run and event, and a delivery failure never changes a run's
  outcome. GitHub commit statuses ride the same hook. See [notifications](notifications.md).
- Deployment detail includes a C8 `runtime` observation for the production environment. Docker filters
  managed environment labels at the daemon before inspecting matching running containers once each.
  The five-second bounded read returns container/release/Compose identities, state, health and start
  time, without command text, environment values or arbitrary labels. `liveRelease` identifies the
  persisted live release, not a current health verdict. Failed or missing Docker is `unavailable` with
  a fixed recovery hint; a successful empty inventory is `available`. Missing health inspection evidence
  remains `unavailable`. The overview renders these services with live/other-release labels and links to
  the exact Docker container or Compose stack panel. Empty managed inventory, unavailable evidence and
  unassessed diagnosis have distinct wording.
  Runtime rows also link to the exact container source in Logs. Logs preserves explicit time-window
  links and refuses to substitute another source when the requested container is no longer discoverable;
  `GET /deploy/{id}/runs/{run}/logs` checks project membership and filters Docker by the run's own
  environment and candidate/release id, including preview environments. It provides live source links
  and a ten-minute history window around the latest successful activation step's completion only when
  its persisted evidence identifies that release and contains no recovery. It never uses the current
  live release's activation timestamp to describe an older run. Missing/failed activation has no history
  link; removed runtimes point the operator to the transcript or an external log archive. The run page embeds application runtime logs separately from the orchestrator transcript, and the
  project Logs tab embeds the observed runtime sources. Both reuse the Logs workspace for live
  streaming, pause/resume, filters, and historical search without leaving the deployment. The run
  viewer uses the server-provided activation window when available. A removed selected service
  is reported as unavailable rather than silently replaced by another service.
- `GET /deploy/{id}/operations` is the one operational read. It resolves the live release's own runtime
  snapshot and asks each feature owner once: Docker for containers, Proxy for routes and certificates, and
  the C6 dependency observer for volumes, bind paths, backup jobs and database connections in a single
  batched call. Every section carries its own availability, so a host without nginx or Backups renders
  named unavailable evidence rather than an empty success. `deploy.Diagnose` is a pure function over those
  observations returning findings and explicit *silences*: an owner that could not be read is never a
  claim and never a clean result. Release comparison (`.../releases/{release}/comparison`) names source,
  image, command, ports, runtime plan, storage, dependencies, checks and domains, compares variables by
  name and value digest only, and reports artifact retention through the same planner a prune uses.
- The fleet read model batches every per-deployment join. `liveReleaseFacts` and `latestProjectRuns`
  use `IN (…)` and `ROW_NUMBER() OVER (PARTITION BY …)`, and `DeploymentSummary` reads one deployment
  rather than filtering the whole fleet in Go. Both are pinned by statement-counting tests: the cost of
  a fleet read is fixed in the number of deployments, and a regression fails rather than slows.
  `DeploymentSummary.serviceCount` rides the same batched artifact read `liveReleaseFacts` already runs
  for health: the live release's `runtime_config` snapshot names its Compose service count directly, or
  1 for any release that is not a Compose build, and the count is read from the snapshot regardless of
  whether the runtime is currently live or stopped — no per-project query added.
  `GET /deploy/{id}/runs?view=engine` additionally accepts `environment=<id>`, `operation=<op>`,
  `state=<state|terminal|active>` (a literal `RunState`, or the closed keywords for "any non-terminal
  state" and "any of the five terminal states"; anything else is `400`), `limit` (≤ 200) and a
  `before=<runId>` cursor; the response gains `nextBefore` only when another page remains, so the
  unfiltered default response stays byte-identical to before these were added.
- A blueprint deploys as an image release with reviewed defaults. `blueprint.DeploymentSupport`
  decides per definition: game profiles, blueprints that ship configuration files, blueprints that
  download an install-time artifact and UDP ports stay preview-only with a specific reason, exposed as
  `deploymentSupported`/`unavailableReason` on the catalogue and detail responses and enforced by
  `DraftSourceConfig.ValidateForDeployment` at source save, preflight, commit, materialization, Git
  watching and queue admission (the stored source is re-validated, so a stale draft cannot enter the
  queue). Detection renders the definition and resolves its image through the registry exactly as an
  operator-typed image is resolved: the source identity carries the normalized image reference
  (`repository`), the reviewed `id@version` (`ref`), the render digest (`revision`) and the image digest
  (`digest`); a preview-only blueprint renders without a registry lookup and has no digest. Saving that
  detection stores the rendered `PlanConfiguration` on the draft — image, command, stop signal, memory
  and CPU from the definition's resources, managed `docker_volume` mounts named
  `<slug>-<hash>-<volume>`, command/HTTP checks with bounded retries, domains — and the configure form offers
  it for review instead of composing a default; the runtime and variable sections open for a blueprint.
  Input values become plain variables through the additive `PlannedVariable.value` field (refused for
  secrets and for anything shaped like a reference); declared secrets become
  `PlannedVariable.generate` (16–128 characters, secret only) and commit produces each value from
  `crypto/rand` so it exists only sealed, revealed through the audited reveal route. Preflight accepts a
  literal or generated value for a required variable, and a managed `docker_volume` that Docker has not
  created yet passes as `storage_pending_creation` (Docker creates it on first start); a linked or
  uninspectable volume still blocks. Default automation presets that need a backup job arrive paused so
  the operator links a job under Settings → Automation instead of a nightly `invalid_plan` failure. Preview
  volume names include a hash of the unnormalized name and blueprint id, Docker-socket mounts are linked
  and read-only, startup budgets use bounded retries, and Bedrock does not receive Java save commands.
  UDP remains an explicit unsupported runtime protocol, never silently converted to TCP.
  `scripts/e2e-deployments.py` deploys the Redis blueprint against a real backend and Docker daemon and
  checks the digest, the rendered plan, the container's limit and volume, and the generated password.
- A definition's `image.command` (an argument vector, never a shell string, never templated) replaces the
  image's default arguments and renders as `Plan.Command`; MinIO needs `server /data --console-address
  :9001` and had none, so it printed its usage and never listened. A definition's readiness timeout is a
  startup budget and always becomes paced retries (every two seconds up to two minutes, every ten
  beyond), never ten instant attempts before the process has bound its port. A second `direct` port
  next to a routed primary (Gitea's SSH, Syncthing's sync protocol) becomes an additional
  `runtime.ports[]` publication on its own number (see published ports below) instead of silently
  replacing the routed port; the primary always stays the one the proxy and the checks reach. The
  catalogue is 53 reviewed definitions (36 added in the sixth pass: whoami, IT-Tools, draw.io,
  CyberChef, linkding, FreshRSS, wallabag, Memos, Trilium, Actual, Gotify, Healthchecks, Directus,
  Jellyfin, Navidrome, Nextcloud, Shlink, Kavita, Homepage, Audiobookshelf, pgAdmin, phpMyAdmin,
  Mongo Express, code-server, Portainer, File Browser, Syncthing, Stirling PDF, Metabase, Jupyter,
  Ollama, Meilisearch, Typesense, RabbitMQ, InfluxDB, MySQL), every image reference resolved against
  its registry (`docker manifest inspect`; the Minecraft and MinIO pins had gone stale and were
  repointed) and every deployable one started live: `TestLiveEveryBlueprintStartsAndAnswersItsOwnChecks`
  renders each definition from its fixture inputs, pulls the image, starts it through the real runtime
  owner with generated secrets, runs the definition's own readiness checks against the published
  loopback port (Mongo Express with a catalogue MongoDB beside it) and removes what it pulled;
  `JD_BLUEPRINT_ONLY=a,b` narrows it. Applications that validate the `Host` header (Homepage,
  Healthchecks) are configured to accept any, since the proxy in front only ever forwards the domain.
- Published ports. `runtime.ports[]` (`hostPort`, `containerPort`, `protocol` tcp/udp, optional
  `bindAddress`, every interface when empty) publishes container ports beside the routed one for
  protocols a reverse proxy cannot carry. Validation bounds each, refuses a repeated host port per
  protocol (including the fixed `hostPort`) and refuses them with host networking; preflight observes
  each as a port conflict; a plan with any is stop-first only (`validateRuntimeActivationStrategy` and the
  preflight strategy finding); previews drop them; the release comparison lists them; the runtime owner
  maps each in addition to the leased routed port. The Domains settings show them as **Also published**.
- Password protection. `domains[].protection` (`username`, `password` on write, `hash` at rest) puts
  HTTP basic authentication in front of a deployment's public route without the application knowing.
  `sealDomainProtection` bcrypts the password once at the API boundary (configuration save and the draft
  configuration step; 8–72 characters, bcrypt's limit) so a stored plan, a release snapshot, a preview and
  the rendered route carry only what an htpasswd file holds; `Validate` refuses a plan that still carries a
  password, and the secret-shaped-key scan is why the JSON key is `hash`. Activation hands the proxy the
  union of the protected domains' credentials once per user: Caddy renders a `basic_auth` block, nginx an
  `auth_basic` with an htpasswd file beside the site written before the site and removed with it. The
  Domains settings and the public-address step offer **Ask visitors for a password**; the overview tags a
  protected domain. Previews inherit the plan's protection.
- The [GitHub App](github-app.md): one App per dashboard through the manifest flow, installation tokens
  for private clones through a `github_app` credential per installation, triggers on `delivery: "app"`
  fed by the App's single webhook (`POST /api/v1/hooks/github-app`, routed by repository through the
  same `dispatchAutomationEvent` decision as the per-trigger hooks), statuses posted as the App where it
  is installed, and one edited-in-place comment per preview on its pull request.
- `GET /deploy/{id}/insights?days=N` (7–365, default 30) computes delivery figures from persisted runs
  only: release runs (`deploy`, `redeploy`, `force_build`, `rollback`) that reached a terminal state,
  success rate over decided runs, median and p95 claim-to-finish duration of successful releases,
  successful releases per week, mean time from a failed release's end to the next successful one,
  the current failure streak, a per-day series with zero-filled days, and the five most common terminal
  codes. Restarts and scheduler markers are excluded. The Deployments tab renders it with each figure's
  basis and a window selector; nothing is estimated in the browser.
- Legacy compatibility logs redact stored environment values before persistent output, including secrets
  split across stdout/stderr writes. Error text passes through the same redaction. Generated `.env` files
  use private, random staging files and atomic replacement; dotenv escaping preserves literal dollars.
- `internal/blueprint` is the one workload catalogue. A blueprint is data parsed with unknown fields
  rejected, validated at package initialization, and rendered by a pure function with no clock,
  randomness, network or filesystem — so the same version and inputs always produce the same secret-free
  plan digest. Install/release/startup/stop steps come from a closed operation vocabulary with no shell
  escape. `validate.go` enforces the supply-chain policy as refusals: no default credential, no published
  database port, no stateful workload without persistence, no download without https plus a size limit and
  a checksum or declared version source, no uncontained path, no privilege without a stated reason, and a
  render fixture whose digest CI recomputes. The container form's starting points are rendered from the
  same catalogue through `GET /docker/templates`; there is no second template list.
- `internal/gameserver` holds the game-specific adapters. The console runs one command through the
  container's own client as separate argv and refuses anything carrying a shell metacharacter, newline or
  NUL *before* it looks at whether a container is running. Player identities come from the server's own
  reply; a server whose reply cannot be parsed reports "not supported" rather than an empty list. The
  settings editor writes only keys the blueprint declares and retains unrelated raw lines and comments.
  Public reads include only declared editable properties, excluding RCON and plugin credentials. Parsing
  understands escaped separators, Unicode escapes and continued lines; duplicate declarations use the
  last value and edits update every duplicate. Changed values escape backslashes and numeric bounds
  enforce a zero minimum. Upstream versions come from a bounded cached adapter that reports
  unavailable or explicitly stale rather than substituting an unverified `latest`. A game server's data
  volume is named from the deployment, not the release, which is what makes rollback restore the previous
  server build against the same world.
- `deploy_credentials` rows are managed under `/deploy/credentials` (`system.admin`, session, every mutation
  audited): `GET` lists `{id, name, kind, target, username?, createdAt, updatedAt, lastUsedAt?, usedBy}`
  without the secret; `POST`/`PUT` seal the secret with the same `auth.Sealer` the variables use and validate
  its shape per kind — a PEM private key for `git_ssh`, a bounded token for `git_bearer`/`provider_token`, a
  non-empty opaque value plus a required target host and username for `registry` (`registryAuth` already
  required both); an omitted `secret` on `PUT` keeps the sealed one. `DELETE` answers `409 credential_in_use`
  while any non-archived project's current desired source still resolves to it, the same "recreate it from
  the same form" reasoning the invariants already give a saved DNS-provider credential, and is behind
  `s.destructive` without a typed phrase for the same reason. `POST .../test` runs `git ls-remote` (a full
  URL, or an `owner/name` shorthand combined with the credential's own saved target) for the Git kinds or an
  authenticated manifest resolution of the image named in `repository` for `registry` — every kind needs
  something concrete to try, and the page's Test dialog asks for it — through the exact adapter isolation
  detection uses, and never returns the secret. `target`/`username`/`lastUsedAt` live in the row's existing `config_json`, so this
  needed no schema change; `lastUsedAt` is set by `OpenCredential`'s own callers after a real use, best-effort.
  `gitEnvironment` branches on kind: `git_bearer`/`provider_token` (and a minted App token) go out as an
  `http.<remote>.extraHeader` of HTTP Basic with `x-access-token` (`x-token-auth` on bitbucket.org) and the
  token as the password — the form a Git host's smart-HTTP endpoint actually accepts; `Authorization: Bearer`,
  the REST convention it used to send, is answered by GitHub with a username prompt, so every token clone
  failed while the API calls through the same token succeeded. `registryAuth` encodes `X-Registry-Auth` with
  padded `base64.URLEncoding`, which is what the daemon decodes with; the unpadded form was dropped silently
  whenever the JSON's length was not a multiple of three, and the registry saw an anonymous pull. `git_ssh` writes the
  sealed private key to a private 0600 file for the lifetime of one Git invocation and points
  `GIT_SSH_COMMAND` at it exclusively, with `-F /dev/null` and both known-hosts files pointed at `/dev/null`
  so the connection never reads or writes the operator's real `~/.ssh`; `SSH_AUTH_SOCK` was already stripped
  from this environment, so an agent identity cannot be tried either. A draft's source step and the
  source-change route below both refuse a `credentialId` that names no row before saving, rather than
  waiting for detection to discover it.
- `PUT /deploy/{id}/environments/{env}/source` (`system.admin`, session, audited `deploy.source.update`)
  changes a committed project's source without touching the live release: the same `DraftSourceConfig` shape
  a draft's source step uses, plus the caller's current `revision`. The handler validates it with
  `ValidateForDeployment`, runs the exact read-only inspection a draft's detect step runs
  (`HostSourceAnalyzer.Analyze`, resolving the Git ref or the registry digest) so an unreachable branch or
  image is refused with the adapter's own message before anything is written, then `PlanningStore.
  SaveEnvironmentSource` writes a new `deploy_sources` revision and clones build/runtime forward unchanged —
  exactly as a configuration save clones the source row the other way. The source kind cannot change; build
  and runtime were built for the kind that is already live. Response is the refreshed
  `EnvironmentConfiguration` plus the saved `source` and the freshly resolved `identity`, since the
  configuration response alone carries no source field. Nothing else needed to change for this to work:
  `PendingState` already compares the live release's frozen source digest against the desired one, so a new
  source revision alone is what makes it report a `source` pending change, and the Git watcher already keys
  its cursor on the desired source's own config digest, so a changed URL/ref/subdirectory resets that cursor
  and the very next poll reports `branch_changed` on its own.
- `POST /deploy/{id}/duplicate` `{name}` (`system.admin`, session, audited `deploy.project.duplicate`) creates
  a draft pre-filled from the project's current desired source and configuration — the same draft the
  new-project page resumes with `?draft=`, landing at the `configuration` step with no `detection` yet, so
  the operator still runs detect/preflight/commit like any other draft. `PlanningStore.Duplicate` copies the
  source and the build/runtime/dependency/check rows verbatim and changes exactly three things: domains are
  dropped (a hostname belongs to one project), variables are flattened to name/sensitivity/scopes with every
  value cleared and `required` set (a secret's plaintext is never available to copy, and a reference names
  something scoped to the source project), and a managed Docker volume's literal name is re-derived with the
  same `<slug>-<hash>` shape a blueprint's own volumes already get (`blueprintVolumePrefix`, keyed on the new
  name and the old literal volume name so two mounts never collide) — committing the duplicate unchanged
  would otherwise hand it the source project's own live volume. A linked or observed dependency, and a
  bind-path mount, are left exactly as saved: nothing about their identity is owned by lifecycle re-derivation.
- Closed vocabularies, route capabilities/confirmations/audit actions, retention limits and error codes
  are contracts. Change one only with an ADR plus migration and exhaustive transition/route tests.

## Public deployment ingress

Docker Caddy public listeners are reused for automatic HTTPS and deployment routes. On a fresh host
with unclaimed TCP 80/443, the first deployment provisions a persisted public Caddy automatically.
The [ingress decision](caddy-ingress.md) specifies ownership, additive snapshot fields, certificate
evidence, lifecycle repair, supported layouts and verification. The dashboard remains private.

The certificate authority is Let's Encrypt unless `JD_ACME_DIRECTORY` names another ACME directory
(`proxysvc.ACMEDirectory`, read on each use): the staging endpoint for a rehearsal, or a private
authority such as Pebble or step-ca with `JD_ACME_CA_ROOT` naming the PEM bundle to trust — the roots
that sign its certificates and, if private too, the root behind the directory's own TLS listener. A
Caddy-managed route (the certificate preparation route and any route served from a `caddy-` import)
renders `tls { issuer acme { dir … trusted_roots … } }`, the bundle is copied to
`/config/just-dashboard/acme-root.pem` in the container before the route is written, the issued chain
is verified against those roots when it is read back, and the public-DNS precheck is skipped for a
private authority (public DNS says nothing about it; Let's Encrypt and its staging twin still require the
name to resolve here). certbot issuance adds `--server <directory>` for a non-staging override. The
journey is proven end to end by `TestLiveDockerCaddyIssuesThroughAConfiguredACMEDirectory`: an isolated
Caddy on loopback ports and a Pebble that validates nothing (`PEBBLE_VA_ALWAYS_VALID=1`) on one Docker
network; the certificate is issued, verified against Pebble's freshly minted root, reused on the second
request, and served on a route whose chain verifies the same way — everything Let's Encrypt would do
differently is the validation Pebble skipped and the roots browsers trust.

## Deployment topology

```
browser ──(Tailscale / SSH tunnel)──▶ Caddy :8443
                                        ├─ /api/* ─▶ backend :8080  (loopback)
                                        └─ /*     ─▶ frontend :3000 (loopback)
```

Ports are variables — `JD_PORT` (8443), `JD_BACKEND_PORT`, `JD_FRONTEND_PORT` — read by
`docker-compose.yml` and `deploy/Caddyfile` from one `.env`. `install.sh` keeps the memorable default for
the one port a person types and walks it upward only if something already holds it; the two internal ports
are **picked at random from 20000–59999** and checked free, because their historical defaults (3000, 8080)
are the two most contested numbers on a Linux server and nothing outside this machine ever addresses them.
Installation, restart, rebuild and self-update share `internal/stackports`. Before startup it identifies
this checkout's running services using Compose labels and host process ownership, reserves distinct
available ports, and atomically updates only the three port values in `.env`. Existing dashboard-owned
ports stay put; foreign listeners stay running. Private `.env.jd-port-previous-*` backups preserve the
prior configuration. Startup waits for container health and retries a newly observed port conflict up to
three times. Health URLs and the dashboard endpoint follow the persisted choices. Settings changes use
the same free-port policy and preserve configuration rollback.

New container and Compose starts recover published-port conflicts automatically. Bind scope, protocols
and internal ports are preserved, and replacement host ports are concrete so stop/start keeps the same
addresses. Container creation returns actual published ports; deployment checks, proxy routes and fleet
summaries use the recorded runtime port. Compose stores only port mappings in `.just-dashboard-ports.yml`
(or next to the immutable release override), never resolved secrets. Port edits invalidate that override.
Port replacement uses Compose's `!override` support (Compose 2.24.4+). Host-network application ports and
ACME's public challenge ports cannot be moved by rewriting Docker publication rules.

**The frontend is the one service not on the host network**, which is what makes a taken port survivable
rather than silent. On the host namespace Next failed to bind, the container restart-looped, and Caddy's
catch-all forwarded to whatever already held 3000 — the operator got a stranger's application over the
dashboard's own certificate, with nothing in any log saying so. Published on loopback, Docker refuses
first, before anything serves. Inside the container the port is always 3000; only the host side varies,
because only the host side can collide.

Caddy is the only listener on anything but loopback and binds `{$JD_BIND}` (falling back to `{$JD_SITE}`)
**plus** loopback explicitly — site addresses alone would leave it listening on every interface. The bind
is separable from the site because a Tailscale install answers for a MagicDNS name whose certificate is
issued to that name while the socket must be opened on the tailnet IP: this container resolves names
through Docker's resolver, not the host's. `deploy/proxy-entrypoint.sh` derives the scheme and the `tls`
directive from `JD_TLS` — `tailscale` (a real certificate from `JD_DATA_DIR/certs`, mounted read-only and
renewed by the backend), `internal` (Caddy's CA, the source of the browser warning) or `off` (plain HTTP,
loopback only, which browsers treat as a secure context and therefore do not warn about). A Caddyfile
cannot branch, and generating one would put a tracked file in the way of every `git pull`. One origin for UI and API is
load-bearing: `SameSite=Strict` cookies, the mutation CSRF header and the WebSocket origin check all
depend on it. The frontend's `src/proxy.ts` creates a fresh CSP nonce per document and passes the policy
into Next so framework scripts and the pre-paint theme script receive it; no production policy grants
`script-src 'unsafe-inline'`. Caddy preserves that header and supplies a deny-all fallback for
non-document responses, plus Permissions-Policy. Caddy rewrites
`X-Forwarded-For` to the real client address (what makes `JD_TRUSTED_PROXIES` safe); `flush_interval -1`
and zero read/write timeouts keep the long-lived streams alive. The backend container runs `privileged`,
`pid: host`, `network_mode: host` with the Docker socket and real host paths mounted **at their real
names** — remove a mount and the file manager silently browses the container's own empty filesystem.

## Deployment workspace

`/deploy` opens as a grid of project cards (rows on a phone) under the in-progress runs, a search
field and state chips; a card carries the workload mark, the address, the branch and commit
subject (or the Compose service count), and one status word. `/deploy?view=archived` lists archived
projects with Restore (`POST /deploy/{id}/unarchive`) and permanent deletion. `/deploy/notifications`
holds the fleet-level channels; `/deploy/credentials` holds saved credentials (Git tokens, SSH keys,
registry logins, provider tokens) with add, edit, test, and a removal that is refused while a project
uses the credential. Projects are also reachable from the command palette.

The project pages under `/deploy/[id]` share one read of the project through a layout-level provider
(`detail`, runs, releases and the slower operational evidence), so moving between a project's pages
never refetches the project. The shell draws the name and a status word (deploying, ready, failed,
unhealthy, stopped, not deployed, archived — derived from the summary and the runtime observation),
Visit, the one command (Deploy, Deploy changes, Redeploy, Start, or View deployment while a run is
active), a verbs menu (Restart, Stop/Start, Redeploy live release, Rebuild without cache, Deploy a
specific version, Duplicate project, Open in Docker, Archive) and a facts row (address, branch and
commit, live release, automatic-deployment status).

**The project's pages are on the sidebar, not in a strip above them.** `ProjectShell` publishes them
through `useNavScope` (`components/nav-scope.tsx`) and the rail draws a third level inside Deployments:
Overview, Deployments, Logs, Runtime, Console, Players and Server settings for a game server, then a
Settings group of the nine settings pages carrying the pending-changes mark. The strip of eight tabs and
the settings rail beside them are both gone; the lists themselves live in `components/nav.ts` as
`PROJECT_NAV` and `PROJECT_SETTINGS_NAV`, which is also what the rail draws from the route alone until
the project's read lands. Stop and Restart need the destructive capability, as the Docker container
verbs do; the menu hides them otherwise. Legacy `?tab=` links
redirect to these routes. Operation failures are titled by the operation and re-read the project at
once so the header and the Danger zone never disagree about a stopped service.

Overview centres on the production block: the constrained site preview, domains with route and
certificate status, health, source, the live release with who deployed it, automatic deployments,
Build logs and Roll back. Findings from the operations diagnosis are a plain list under it; recent
deployments and live usage follow. `GET /deploy/{id}/preview-frame` is an
authenticated, non-cacheable static HTML wrapper with no scripts. Its CSP permits a child frame only
from the recorded endpoint's origin and allows the wrapper itself to be framed only by this dashboard.
This is a deliberate exception to the API's default frame denial; the dashboard document's CSP stays
unchanged. The embedded website is sandboxed without top navigation, popups, or downloads. No website
request is made by the backend for the preview and dashboard request headers are never forwarded to
it. The browser
applies its normal cookie and mixed-content policies. Sites that disallow embedding, require login,
or use an insecure URL under an HTTPS dashboard may require the direct website link. A preview is
not a deployment health check and does not bypass the site's own framing policy.

`GET /deploy/{id}/favicon` is the one request the backend makes to a deployed website, so a project
card and the project header can carry the site's own icon (the dashboard's image policy allows only
its own origin). It reads the recorded endpoint's page for `<link rel="icon">` (then
`apple-touch-icon`), falling back to `/favicon.ico`, `/favicon.png` and `/apple-touch-icon.png`.
Every request stays on the recorded scheme and host, port included: a declared icon or a redirect
anywhere else is refused, not followed. It reads at most 512 KB of page and 1 MB of icon within five
seconds, accepts only a response that is an image, and remembers each project's answer for an hour
(an absence for ten minutes). The icon is served with `nosniff`, a sandboxing CSP and private
caching, so an SVG opened directly is a picture and not a document; `404 favicon_unavailable` means
the card keeps its workload glyph.

Deployments carries the delivery insights, filter chips and the run rows — status, duration, title,
commit subject, then branch (or the requested tag or commit) · sha · trigger · time. A row's menu
offers Redeploy (live), Roll back to this release (a retained release, through a two-step dialog that
names the domains and what a rollback does not restore), Compare with live (a sheet), Pin or Unpin,
Retry (only a `failed` or `cancelled` run) and Cancel (never during activation or a rollback). Older
runs load on request through the `before` cursor. Runtime lists the managed services with live usage
and the recorded charts, then the routes, storage, backup and dependency evidence with every owner's
own availability. Logs and Console keep their contracts; Logs offers the activation window of the
live run when the run's log handoff names one.

Settings are nine sections of setting cards — General, Build, Runtime, Environment variables,
Domains, Storage, Databases & backups, Automation, Danger zone — each saving the whole configuration
with the revision it read; a pending-changes notice at the top of every section names what the next
deployment applies, and a refusal that names a field lands on that control (a refused row is marked
in place). General holds the name, the Git policy with the GitHub commit-status toggle, and the
Source card that changes a repository, branch, root directory, credential, submodule and LFS choice
or an image reference and platform in place (`PUT …/environments/{env}/source`, checked before it is
saved). Runtime holds the limits, the restart policy and the health-check editor; Environment
variables is an inline form over a list with reveal, rotate and remove, and editing an existing
variable asks for its value again (an administrator can reveal the current one into the form) so a
scope change can never blank a secret; Domains checks a new hostname through `GET /deploy/hostname`;
Databases & backups links databases through the same sheet the creation flow uses and never commits
a half-filled sibling row when a database is connected or removed; Automation holds webhooks (with
their delivery log and secret rotation, the hook URL shown absolute), schedules (with their run
history) and previews (approve, reject, variables, deploy); Danger zone holds Stop or Start, Archive,
managed-resource removal and permanent deletion.

The deployment page (`/deploy/[id]/runs/[run]`) keeps the sequence-based stream, resync and the
5,000-event cap; the build console numbers lines, strips terminal escapes, filters by stage and
errors, wraps, follows and copies, and stays mounted while Details, Runtime logs or Metrics are
open so its search and scroll position survive the switch; the release path shows each group's
duration; Details lists every step attempt with its evidence; Runtime logs and Metrics keep their
server-provided windows. A successful run shows Visit only when its recorded release is the project's
current live release; the success block waits for the project read so it never flashes Superseded
first.

Archived deployments are searchable at `/deploy?view=archived`. They offer Restore and a separate
permanent record deletion with explicit confirmation, preserving host resources and the audit log. See
the [permanent-deletion decision](permanent-deletion.md) for the API and transaction contract.
