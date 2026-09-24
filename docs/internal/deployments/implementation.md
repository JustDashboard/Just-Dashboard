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
  `DELETE /deploy/drafts/{draft}` (session, audited `deploy.draft.discard`) throws one away: every
  press of Import creates a draft, so an abandoned attempt would otherwise sit in that list for its
  whole thirty-day life and three tries at one repository would read as three pieces of unfinished
  work. Only the owner's own uncommitted setup can go — a committed draft is the record a project was
  planned from and answers `409 draft_committed`. The page also discards for itself: inspecting a
  second source, or pressing "Change source", throws away the setup being walked away from, because
  that is a different project rather than a revision of the first.
  Remote Git inspection resolves an exact ref into a
  private dashboard-owned bare mirror and detached temporary worktree; it never clones into or resets an
  operator checkout. Inspection's mirror is shallow and blob-filtered so planning stays bounded, so a
  release materializes through a second mirror that is neither: the run workspace is built by fetching the
  recorded object id into a fresh repository, because `git clone --local` ignores its local copy when the
  source is shallow and would silently produce an empty checkout. Registry inspection resolves a digest
  without pulling. Planning-time Compose validation uses private temporary files, an explicit empty env
  file and inert placeholders for detected variable names, so the backend environment and a checkout
  `.env` cannot influence the result.
- `/deploy/new` is one page, held to the window at `xl`. Unfinished setups (`GET /deploy/drafts`) are
  offered for resumption behind a counted button beside the question; a source strip offers a Git
  repository (connected GitHub list or a pasted URL), a Docker image (images already on the server or
  a reference), a reviewed template (shelved client-side by topic, with the server's `category` as the
  fallback shelf for a blueprint the frontend does not name), a database and a Compose stack (paste,
  upload, Git, local). Choosing a
  source creates a draft, saves the intent and source, and runs detection in one action; when
  detection is ambiguous the candidates are offered as a choice that re-runs detection with
  `selectedId`. The configure screen draws the plan beside the form — source → build → runtime →
  address, in the same `wire.tsx` vocabulary the project overview uses for a deployment that already
  exists, with a dashed ring for a step not yet decided and nothing pulsing, because a pulse means
  live traffic and a plan has none. Every step is pressable and opens the fields that decide it,
  which is what puts the release strategy and the memory limit on screen without opening Advanced to
  find them; an unbounded container reads as "No memory or CPU limit" there rather than as silence.
  The form itself holds the name, the type, the detected build and output
  settings, environment variables, an optional database, the public address (with the hostname
  suggestion and certificate readiness), automatic deployment for a Git source, and an Advanced
  disclosure carrying the runtime, health check,
  storage, release task, build secret and container fields. The type is offered for an image source
  as well as a Git one: the profile is plan-time intent that only preflight reads, and offering it
  only for Git is why an HTTP application shipped as a container could not be told it was one —
  it deployed stop-first, with no health gate and no finding to say so. Choosing a gated type seeds
  the readiness check and candidate-first activation that preflight requires for it. A name a live
  project already holds is reported by `GET /deploy/hostname?name=` as `nameTaken` and flagged beside
  the field while it is typed, rather than refused by the schema's UNIQUE constraint at commit after
  the whole setup has been filled in. What detection could not settle for itself (`needsDecision`)
  is shown on the source row, and it decides which screen the sequence opens on — so a decision is
  an *unanswered question*, emitted only when the thing it names is genuinely open. A Dockerfile
  whose single literal `EXPOSE` gave a port, an image whose exposure was read, a Go module (the
  recipe resolves its own executable and starts the binary it writes, and preflight asks a service
  for no readiness gate), a Deno project (the port is `Deno.serve`'s own default) and a static site
  (a marker's root is the directory its files were found in, so the candidate's root *is* the one
  holding the `index.html`, which is what an empty output directory serves) record evidence instead;
  a Dockerfile naming no port or several, an image this host holds no copy of, and a Compose file
  spotted in a Git checkout but never parsed still owe one. Everything else that is open is carried by the plan
  rather than by prose, which is what lets it open the screen that owns the field: an unset
  `internalPort` opens the runtime screen (and the port field seeds the readiness check a gated
  profile needs, the way choosing the type does); a required plan variable with no reference, value
  or generation — a Compose `${VAR}`, a blueprint input, a duplicated project's copied variable —
  opens the variables screen with its references fold already open, the same rule preflight refuses
  at Deploy as `variable_required_*`; and `nameTaken` opens the project screen, so a name a live
  project already holds is corrected before Deploy rather than by it. Nothing open means Review,
  with the plan read back and Deploy under it. **Review saves the configuration and runs preflight on
  arrival**, not under the button: preflight used to run inside the press, so the screen asking "is
  this right" had checked nothing by the time it was read, its findings landed under a button the
  reader had already pressed, and the first press of Deploy was really a check. It reads the plan back
  as what the drawing beside it cannot carry — the mounts kept between rebuilds and their backup
  coverage, the variables generated on this server and their length, the readiness check's target and
  budget, and what the cutover strategy costs — plus every preflight `pass` as a checked line, where
  before only `blocked`, `decision` and `warning` were drawn and a plan with nothing wrong with it
  showed four facts. A stored result is keyed to the plan it was computed for as well as to the
  draft, so going back to change the port and returning re-checks rather than reading back what this
  server agreed to about the previous plan. The arrival check runs once per plan: a check that fails
  is shown and not retried on its own, and Deploy is the retry. Review and Deploy save edited intent,
  configuration and encrypted environment inputs before preflight. Commit creates the project and its
  initial variable revisions in one transaction, then Deploy enqueues the first run; Save only stops
  after commit. Required variables are checked against those same values, so an entered connection
  string cannot be rejected merely because it used to be imported after commit. The saved draft revision is adopted before preflight, so a failed preflight never
  strands the draft, and a `draft_revision_conflict` re-reads the draft once. `?draft=` resumes a
  draft (including one produced by `POST /deploy/{id}/duplicate`); a draft saved without a
  configuration — every draft abandoned from Configure, since the configuration is saved at Deploy —
  is re-detected rather than refused. `?mode=advanced` opens Advanced, and existing workloads adopt
  through `/deploy/import/adopt` without a run. For a Git source the commit carries a `gitPolicy`
  (`automatic`, `watchInclude`, `watchExclude`, `commitStatuses`), written as the environment's
  `deploy_git_policies` row at revision 1 inside the same transaction; no decision writes no row, so
  every caller that does not ask keeps the defaults in `gitDeploymentPolicy` exactly as they were.
  A Git deployment polls its branch from the moment it exists and the default is to deploy every
  push, so until this travelled with the commit "manual only" was a setting reachable only after a
  production service had already released a commit nobody meant to ship. Saved credentials are picked from
  `GET /deploy/credentials`. The page is kept for the tab (`useSessionState`,
  [`../frontend/data-theming.md`](../frontend/data-theming.md)): the source tab and its form, and the
  configure screen's flow, findings and Advanced disclosure, survive a walk to another page and a
  reload until the project is created or the source is changed, and a remembered flow whose draft has
  expired is dropped with a notice on the way in. Unsaved environment values and visitor passwords never
  enter the URL or browser storage. Configuration saves stage environment values in the additive
  `deploy_drafts.environment_enc` column, sealed with the install key; only `environmentKeys` returns
  to the browser, including names whose explicit empty override must survive a reload. An omitted
  `dotenv` preserves staged inputs, an explicit empty document clears them,
  and `retainEnvironmentKeys` preserves individually named masked values when a resumed form submits
  new input. New values override retained values, plain defaults and generation requests while keeping
  declared scopes. Original defaults and generators remain available if the override is removed; commit
  seals every supplied value as secret regardless of the fallback's sensitivity. A source change clears
  staged values; reinspection of another branch of the same
  repository preserves them. Invalid parsing, revision conflicts and failed commits cannot partially
  save credentials. Required-variable and reference-graph checks use the same effective values as commit.
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
  ordinary deployment there is unsavable over a control the operator was never shown. The check is the
  candidate's detected `readiness` — its path, whether any answer counts, Docker health for a
  Dockerfile `HEALTHCHECK` command, and a slow start's budget (recipes.md, "Readiness, workers and
  start commands") — and GET `/` expecting a 2xx only when detection found nothing.
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
  not so the operator has to do it. Asked about a hostname the operator typed (`?hostname=`), it also
  answers `address`, this server's public IPv4, which is the A record the name needs, and — for
  `system.admin` only — `resolves`, whether the name already points here, taken from the reverse
  proxy's own DNS check so the Domains sheet and the proxy page never disagree about one name. It is
  a pointer for the reason `nameTaken` is: a host with no public address cannot make the comparison,
  and a lookup the request ran out of time for is no answer, so absent is not false. It stays with
  administrators because resolving a name the caller chose is traffic the caller directs, which the
  proxy page's own check already keeps at `system.admin`.
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
  The contract per language is [the recipe guide](recipes.md). The framework detection recognised is
  recorded on the build plan when a draft commits (`build.framework` on the configuration read) —
  the chosen candidate's, while the plan still builds that candidate's directory — so a later read,
  a fleet card or the Build settings, names it without detecting again. The server owns it: a value
  the browser sends in a draft or a configuration save is never kept. A save carries it forward only
  while the method, recipe and root directory stay the same, and a source change that moves the code
  — another repository, directory or image, though not a new branch or credential — drops it,
  because detection named the code that used to be there. It is left out of the plan's digest
  (`buildPlanDigest`, which every path that writes a build plan uses), so recording or dropping a
  name never shows as a pending build change. Projects created before it was kept have none.
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
  transcript when output was captured. The diagnosis names one proven cause: a missing table
  (`schema_missing`), or a single container that stopped with exit code 0 (`start_command_exited`, a
  start command that returned instead of serving).
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
  An HTTP check against the candidate introduces itself as the proxy does when the release has a
  domain — `Host`/`X-Forwarded-Host` the domain, `X-Forwarded-Proto` its scheme,
  `X-Forwarded-For 127.0.0.1`, a browser's `Accept` — while connecting only to the candidate.
  Default HTTP checks require a final 2xx response and follow at most four redirects that stay on the
  candidate's address or name one of the release's own domains; the latter are re-asked of the
  candidate, never of the domain. Redirects elsewhere are never requested and fail with their origin
  in the message (`redirect_off_origin`), as do loops (`redirect_loop`), so a candidate cannot pass by
  redirecting to the old public release or to a page that returns 500. `acceptAnyAnswer` checks accept
  the first answer below 500 other than 400 and 421 (what host allowlists answer); explicit
  expected-status lists retain exact-status, no-redirect behavior, and a check cannot set both.
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
  `POST …/variables/import?dryRun=1` (the import's own tier: `system.admin`, session) takes the
  import's body and writes nothing: it answers one verdict per name, in the order the names were
  written, with the line — `added`, `changed` (the value, sensitivity or scopes differ), `unchanged`,
  or `refused` with `invalid_name`, `duplicate` or `invalid_value` — so the import sheet can say what
  pressing Import will do before it is pressed. Values meet the stored ones only as digests, on the
  server, and the answer carries neither a value nor a digest. It is audited under an action of its
  own, `deploy.variable.import_preview` (the name count, sensitivity and scopes), rather than as the
  import it did not perform. What the import refuses as a whole — a
  line the parser cannot read past, a bad sensitivity or scope, a reference that would not resolve —
  is refused with the import's own error; a refusal that belongs to one name is that name's verdict,
  so every other line can still be read. `dryRun` must parse as a boolean, because a preview that
  failed to parse must never fall through to the import. Both paths share `parseDotenvEntries`, and
  `components/deploy/settings/dotenv.ts` is that parser written again line for line, so the sheet
  can draw a preview while the server's is in flight, and the server's verdicts replace it.
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
  are excluded. Monitoring status and access failures appear in the project header's identity line
  and on Settings → General's Automatic deployment section.
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
  leaves the field absent rather than failing the step. That line goes to `stderr`: the transcript
  accepts only `stdout`, `stderr` and `status`, and the `warning` stream it was first written on was
  refused, which failed the append and silenced every line the step logged after it. A retry's
  wholesale metadata copy, plus every
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
  rows for a trigger — delivery id, event, ref, decision, reason, run id, received at — or `?limit=` of
  them, 1 to 200 (anything else reads 50), and
  `POST …/triggers/{trigger}/rotate-secret` (same tier, audited `deploy.trigger.rotate_secret`) issues a new
  HMAC secret shown once, mirroring the legacy project's own secret rotation. The trigger list carries a
  summary of that log — each trigger's `lastDelivery` and `recent`, its last fourteen decisions oldest
  first, read for every trigger in one windowed statement (`AttachTriggerDeliveries`) — but only for a
  session holding `system.admin`, the caller who could read the log itself; the list is readable by any
  account, and a read-only one still sees `lastStatus` alone. Absent means not allowed or never
  delivered, and the page then draws no strip rather than an error.
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
  chain. Chain history stores only action/status/error-code/duration evidence. A listed schedule carries
  `nextRuns`, its next five firings: the stored `nextRunAt` first, because that is the dispatcher's own
  answer, then four more walked from it in the schedule's time zone (`NextCronRuns`), so a
  daylight-saving change lands on the page where the dispatcher will put it. A paused schedule has
  none, and an expression that no longer walks — a zone gone from the host's tzdata — is left to the
  dispatcher to disable. `POST …/schedules/test` (audited `deploy.schedule.test`) answers the same five
  beside `nextRunAt`, which is what the schedule sheet's check shows before a schedule is saved.
  Preview environments require
  session administrator approval of each exact PR revision before creation or execution. An approval
  carries `headRef`, the branch the pull request proposes, kept apart from the ref the build fetches
  (the provider's own `refs/pull/N/head` on GitHub and `refs/merge-requests/N/head` on GitLab, names
  nobody chose); approvals recorded before it was kept have none.
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
  outcome. GitHub commit statuses ride the same hook. The channel list carries each channel's newest
  attempt and last fourteen outcomes, and each delivery the project and run it announced, so the
  page reads whether messages arrive without a request per channel. See
  [notifications](notifications.md).
- Deployment detail includes a C8 `runtime` observation for the production environment. Docker filters
  managed environment labels at the daemon before inspecting matching running containers once each.
  The five-second bounded read returns container/release/Compose identities, the image reference the
  container was created from as Docker reports it (`image`, so a service is drawn as the product it
  runs without a join to the container list), state, health and start
  time, without command text, environment values or arbitrary labels. `liveRelease` identifies the
  persisted live release, not a current health verdict. Failed or missing Docker is `unavailable` with
  a fixed recovery hint; a successful empty inventory is `available`. Missing health inspection evidence
  remains `unavailable`. The overview renders these services with live/other-release labels and links to
  the exact Docker container or Compose stack panel. Empty managed inventory, unavailable evidence and
  unassessed diagnosis have distinct wording.
  A Runtime service's Logs verb (and the game console's) opens the project's Logs page, which reads
  only `view` and `moment`, so the `?service=` it carries selects nothing there; the exact container's
  output is reached through Open in Docker, and a run's runtime-log sources link to the host Logs
  page's `source=docker:<id>` (see [verification findings](../reference/verification-findings.md)).
  Logs preserves explicit time-window
  links and refuses to substitute another source when the requested container is no longer discoverable;
  `GET /deploy/{id}/runs/{run}/logs` checks project membership and filters Docker by the run's own
  environment and candidate/release id, including preview environments. It provides live source links,
  each source with its container's image, and a ten-minute history window around the latest
  successful activation step's completion only when
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
  named unavailable evidence rather than an empty success. A domain row names who issued its
  certificate (`certificateIssuer`, the issuer's common name — `R10`, `E6` for Let's Encrypt — read
  from the same certificate as its name and days left), so the issuer is observed rather than
  inferred from how the domain is owned. A dependency's `deepLink` is the page that owns it: a backup
  job's own page (`/backups/<id>`) and Databases with the connection selected
  (`/databases/connection?conn=<id>`), each only once the resource is known to exist and the list page
  until then. The connection link used to be `/databases/<id>`, which is not a route, because the
  Databases pages select a connection by query rather than by path, and the job link was always the
  list. `deploy.Diagnose` is a pure function over those
  observations returning findings and explicit *silences*: an owner that could not be read is never a
  claim and never a clean result. Release comparison (`.../releases/{release}/comparison`) names source,
  image, command, ports, runtime plan, storage, dependencies, checks and domains, compares variables by
  name and value digest only, and reports artifact retention through the same planner a prune uses.
- The fleet read model batches every per-deployment join. `liveReleaseFacts`, `activeProjectRuns` and
  `recentProjectRuns` use `IN (…)` and `ROW_NUMBER() OVER (PARTITION BY …)`, and `DeploymentSummary`
  reads one deployment rather than filtering the whole fleet in Go. Both are pinned by
  statement-counting tests: the cost of a fleet read is fixed in the number of deployments, and a
  regression fails rather than slows.
  `DeploymentSummary.serviceCount` rides the same batched artifact read `liveReleaseFacts` already runs
  for health: the live release's `runtime_config` snapshot names its Compose service count directly, or
  1 for any release that is not a Compose build, and the count is read from the snapshot regardless of
  whether the runtime is currently live or stopped — no per-project query added.
  The summary, on the fleet and on `GET /deploy/{id}` alike, also says what each project is and how its
  last runs went, which is what a card draws without a request of its own: `sourceRepository`
  (owner/name for Git; the image reference itself for an image source, whose `sourceRef` is empty; the
  image a template renders for a blueprint source, whose `sourceRef` is the template's `id@version`),
  `sourceRemote` (the fetch URL reduced to scheme, host and path, or the
  SCP-style `git@host:path`, with any userinfo dropped — a remote recorded before credentials were
  refused in URLs can carry a token there — and left out when it does not parse), the desired build
  plan's `recipe` and `framework`, `images` (the distinct references the live release's runtime
  snapshot runs, read from the same snapshot as the service count) and `recentRuns` (the newest
  fourteen, newest first). The strip and `lastRun` are one statement, `recentProjectRuns`: a
  `ROW_NUMBER()` ranking of every card's runs that carries only ids, answered in order by
  `idx_deploy_runs_project_requested (project_id, requested_at DESC, id DESC)`, joined back to the
  whole rows it keeps, so `recentRuns[0]` is `lastRun` by construction. Ranking whole rows in a second
  window was the fleet read's most expensive statement and pushed its `-race` p95 against the
  budget. A fleet read stays ten fixed statements, and eleven while anything is in flight (the step
  read below), beside the active-work list's own reads of each run and its project; the counting
  tests measure a fleet at rest. A run that
  has not ended carries `currentStep` — the step it is running, else the one blocking it, else a
  failed one, else the next one waiting, with the name the release path gives it (`Fetch source`,
  `Readiness checks`, `Switch traffic`) — on the fleet's active work, on a summary's `lastRun` and
  `activeRun`, and on `GET /deploy/{id}/runs?view=engine`, so a row can say where a run stands without
  loading its snapshot. It is one windowed statement over the runs in flight, where the active-work
  list used to ask once per run. `GET /deploy/?view=archived` returns each archived project's record
  with the facts its plan recorded beside it — `sourceKind`, `sourceRef`, `sourceRepository`,
  `buildMethod`, `recipe`, `framework` — read for every archived project in one statement
  (`ArchivedDeploymentFacts`), so the archive can draw each as what it deployed without a read per
  row; a legacy project with no production environment has none.
  `GET /deploy/{id}/runs?view=engine` additionally accepts `environment=<id>`, `operation=<op>`,
  `state=<state|terminal|active>` (a literal `RunState`, or the closed keywords for "any non-terminal
  state" and "any of the five terminal states"; anything else is `400`), `limit` (≤ 200) and a
  `before=<runId>` cursor; the response gains `nextBefore` only when another page remains, so the
  unfiltered default response stays byte-identical to before these were added, apart from
  `currentStep` on a run that has not ended, which every read of the list now carries.
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
  The template panel answers what it can before the press: a `domain` input arrives filled in with
  `GET /deploy/hostname`'s suggestion (the same `<slug>.<address>.sslip.io` the public-address field
  takes, and `Render` turns that input into the plan's own domain). Reviewed plain variables derived
  from that primary domain carry `domainTemplate` metadata using only `{{hostname}}` and `{{scheme}}`.
  Runtime-screen hostname and HTTPS edits update values still matching their old automatic value;
  explicit overrides remain unchanged. Removing publication clears matching automatic values; a
  required domain setting then needs a value before deployment. Editing a route retains its visitor
  password protection, and disabling public publication removes all aliases as well as the primary.
  A required input the panel cannot answer is marked and
  refused there rather than reaching `Render` — which used to refuse `"domain" is required` for the
  eight definitions that declare one, after a draft had already been created. A press that fails
  discards the draft it started, so a refused attempt is not an unfinished setup.
  Ordinary input values become plain variables through the additive `PlannedVariable.value` field
  (refused for secrets and for anything shaped like a reference). A `secret` input is deferred to the
  Variables screen as a required secret variable, never accepted in `blueprintInputs`; Mongo Express
  uses this for its MongoDB URI and offers the MongoDB connection sheet. Declared generated secrets become
  `PlannedVariable.generate` (16–128 characters, secret only) and commit produces each value from
  `crypto/rand` so it exists only sealed, revealed through the audited reveal route. Preflight accepts a
  literal, generated, referenced or encrypted draft value for a required variable, and a managed `docker_volume` that Docker has not
  created yet passes as `storage_pending_creation` (Docker creates it on first start); a linked or
  uninspectable volume still blocks. Default automation presets that need a backup job arrive paused so
  the operator links a job under Settings → Automation instead of a nightly `invalid_plan` failure. Preview
  volume names include a hash of the unnormalized name and blueprint id, Docker-socket mounts are linked
  and read-only, startup budgets use bounded retries, and Bedrock does not receive Java save commands.
  UDP remains an explicit unsupported runtime protocol, never silently converted to TCP.
  HTTP tool templates use the web profile so their default candidate-first strategy is eligible.
  Retired definitions cannot create new projects; existing release operations retain their compatibility
  contract. The template chooser keeps its detail column during loading, displays retryable failures,
  retains edits separately per template and ignores abandoned inspections. Below the desktop two-column
  breakpoint, selecting a template brings its settings above the catalogue and focuses them; Choose
  another template returns to the catalogue, respecting reduced motion. Successful inspection consumes
  the source/deploy-link query parameters so a reload returns to Configure. Git branch typing stays local
  until blur or Enter, and reinspection preserves explicit configuration overrides. Session snapshots
  omit raw Compose documents, detection previews and the serialized exact-plan preview.
  `scripts/e2e-deployments.py` deploys the PostgreSQL blueprint against a real backend and Docker daemon
  and checks the digest, rendered plan, container limit and volume, generated-password authentication
  over TCP (including rejection of an incorrect password), and paused backup automation without a policy.
- A definition's `image.command` (an argument vector, never a shell string, never templated) replaces the
  image's default arguments and renders as `Plan.Command`; MinIO needs `server /data --console-address
  :9001` and had none, so it printed its usage and never listened. A definition's readiness timeout is a
  startup budget and always becomes paced retries (every two seconds up to two minutes, every ten
  beyond), never ten instant attempts before the process has bound its port. A second `direct` port
  next to a routed primary (Gitea's SSH, Syncthing's sync protocol) becomes an additional
  `runtime.ports[]` publication on its own number (see published ports below) instead of silently
  replacing the routed port; the primary always stays the one the proxy and the checks reach. The
  catalogue ships 62 reviewed definitions, 57 of them offered (see "how the first sign-in works"
  below for the five that are retired), every image reference resolved against
  its registry (`docker manifest inspect`) and every deployable one started live:
  `TestLiveEveryBlueprintStartsAndAnswersItsOwnChecks`
  renders each definition from its fixture inputs, pulls the image, starts it through the real runtime
  owner with generated secrets, runs the definition's own readiness checks against the published
  loopback port (Mongo Express with a catalogue MongoDB beside it) and removes what it pulled;
  `JD_BLUEPRINT_ONLY=a,b` narrows it. Applications that validate the `Host` header (Homepage) are
  configured to accept any, since the proxy in front only ever forwards the domain.
- **How the first sign-in works**, declared rather than described. Every definition carries an
  `access` block: a closed `kind` — `setup` (the application asks the first visitor to create the
  account), `credentials` (a name from an input plus a password from a generated secret, both
  injected as environment before the container starts), `token` (one generated secret is the whole
  credential), `client` (nothing signs in through a browser; a program connects with the named
  secret), `open` (no authentication of its own) or `unavailable` — plus the sentence the operator
  reads. `validateAccess` refuses a `credentials` or `token` kind that does not name a declared
  generated secret, refuses a credential named on a `setup` or `open` kind, refuses a `client` kind
  with nothing to hand over, and allows `unavailable` only on a definition that also carries
  `retired`. That last pair is the whole point: an image whose first password exists only in its own
  container log cannot be offered. The block is catalogue metadata, not plan input — `Render` never
  reads it, so it carries no digest — and it reaches the picker on `Summary.access` (a word on every
  card, the full sentence beside the chosen one) and the project overview's First sign-in card, which
  reveals the username and password through the audited variable reveal route.
  `retired` is why a definition is no longer offered. A retired definition stays shipped, stays
  parsed and stays held to every rule, because `ValidateForDeployment` re-resolves the definition on
  every redeploy: deleting the file would turn a running service into an unredeployable one.
  `Catalog()` is the offered set, `All()` is everything. Retired in this pass: File Browser (upstream
  archived; first password only in the container log; `FB_PASSWORD` takes a hash), wallabag (its only
  account is `wallabag`/`wallabag` with no environment variable for either half), MinIO (upstream
  archived, no pullable newer release, and the console is not the routed port), Syncthing (no
  environment variable sets the web interface's password before it starts, and until one is set the
  interface is full control of the sync configuration) and Healthchecks (sign-up completes by
  clicking an emailed link, and one image has no mail server, so the sign-up page answers 500).
  Added in the same pass, each one image with its own embedded store, each one started live and each
  one admitted for a first sign-in this catalogue can state: Open WebUI (the interface Ollama shipped
  without), ntfy, Beszel, Opengist, Qdrant, NocoDB, DocuSeal, Seerr and SearXNG. Speedtest Tracker was
  rejected on a rule the catalogue cannot express: Laravel's `APP_KEY` must be exactly 32 bytes, and
  `RotateVariable` always hands back 43 characters of base64, so the dashboard's own Rotate button
  would stop the application from starting.
  Two more global refusals landed with it: an operation may not template an input that is optional
  with no default (`expand` substitutes the empty string, so Grafana's root URL rendered as
  `https:///`), and an `accept` input may not declare a `variable` (`Render` records the acceptance
  and moves on, so both Minecraft definitions' `EULA` never reached the container — it is a startup
  operation now).
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
  audited): `GET` lists `{id, name, kind, target, username?, createdAt, updatedAt, lastUsedAt?, usedBy,
  usedByProjectIds?}` without the secret — `usedByProjectIds` names, sorted, the projects behind
  `usedBy`, read from the same join so the two can never describe different sets, and since `usedBy`
  counts environments a project with two on one credential is one id and two in the count; an
  archived project drops out of both. `POST`/`PUT` seal the secret with the same `auth.Sealer` the
  variables use and validate
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

`/deploy` opens on four readings the chips under them cannot say — how many projects are live,
requests a minute across the fleet with its hour as a line, the share of them failing (weighted by
traffic; amber from 1%, red from 5%) and the build slots in use — then the runs in progress as rows
that open the run, an attention list, and the projects under a search field and counted state
chips, ordered worst first: failed, unhealthy, deploying, ready with pending changes, ready,
stopped, not deployed. What needs attention is decided apart from how it is drawn
(`components/deploy/fleet.ts`, `fleetAttention`): a failed deploy with the engine's own terminal
reason, a live release failing its health check, and a site failing 5% or more of its requests; a
health reading of *unavailable* is not a finding. A card carries the project drawn as itself
(`ProjectMark`), its address, its source as its forge, repository and branch, its last commit, its
hour of traffic from `GET /deploy/traffic`, its last fourteen runs (`recentRuns`) and who started
the last one, and the verbs the project header offers (`useProjectVerbs`); the list view carries
the same readings in fixed columns from 1280. The fleet is read every five seconds and the traffic
every thirty; the archive is read once, for the count beside its link, and again when a card
archives its project, because each archived row costs the server a history read.
`/deploy?view=archived` lists archived projects drawn as what they deployed (`ArchivedDeployment`),
with Restore (`POST /deploy/{id}/unarchive`) and permanent deletion. `/deploy/notifications` holds
the fleet-level channels: it reads the list, which carries each channel's recent history, every ten
seconds, and each channel's log (`?limit=200`) every thirty only for the two counts over a day.
`/deploy/credentials` holds saved credentials (Git tokens, SSH keys, registry logins, provider
tokens) with add, edit, test, and a removal that is refused while a project uses the credential,
and reads the fleet to draw the projects each one serves. Projects are also reachable from the
command palette.

The project pages under `/deploy/[id]` share one read of the project through a layout-level provider
(`detail`, runs, releases and the slower operational evidence, plus the environment's configuration
read once, the Git watch every fifteen seconds for a normalized Git source — one poll the header and
the Overview share — and a template's definition), so moving between a project's pages never
refetches the project, and a configuration save on any page is re-read there when the desired
revision moves. The shell draws the name, then the identity line the host Overview opens on
(`HostIdentity`): the project drawn as itself, its address with the certificate's state in the
lock's colour, its source, commit, runtime, live release and automatic-deployment status as facts
on their own marks, and its state — deploying, ready, failed, unhealthy, stopped, not deployed,
derived from the summary and the runtime observation — with the diagnosis verdict or the stage in
flight under it. Its actions are Visit, the one command (`projectCommand`: View deployment while a
run is active, else Start, Deploy, Deploy changes or Redeploy) and a verbs menu grouped Running,
Building and Project — Restart and Stop; Redeploy live release, Retry, Rebuild without cache and
Deploy a specific version; Duplicate project and Open in Docker — with Archive and Delete
permanently under the danger rule. Starting a run goes through `useProjectStart`, the fleet card's
own.

**The project's pages are on the sidebar, not in a strip above them.** `ProjectShell` publishes them
through `useNavScope` (`components/nav-scope.tsx`) and the rail draws a third level inside Deployments:
Overview, Deployments, Logs, Runtime, Console, Players and Server settings for a game server, then a
Settings group of the nine settings pages, each marked while it holds a saved change that is not live
yet (`PENDING_KIND_PAGE` maps a pending change's kind to its page). The panel's head is the project's
favicon or product and its name as written, and a run page keeps the project's panel with Deployments
marked current, because that is where a run is opened from. The strip of eight tabs and
the settings rail beside them are both gone; the lists themselves live in `components/nav.ts` as
`PROJECT_NAV` and `PROJECT_SETTINGS_NAV`, which is also what the rail draws from the route alone until
the project's read lands. Stop and Restart need the destructive capability, as the Docker container
verbs do, and each asks first, since either is one press from a fleet card's menu; the menu hides
them otherwise. Legacy `?tab=` links
redirect to these routes. Operation failures are titled by the operation and re-read the project at
once so the header and the Danger zone never disagree about a stopped service.

The Overview is, in order: the run in flight, as the runs list's own row with its stage and a light
round its edge (and confetti, once, if the reader watched it go live); the production block — the
site preview, the website laid out at desktop width (or a phone's, from a switch in its strip) and
shrunk into one tile that is a link to it, beside the way a request reaches the project (source
with automatic deployments, the live release with who deployed it, the runtime's containers and
health, domains with their certificates), each drawn as its product, over four readings (requests
a minute, the failing share, processor and memory, each carrying its last hour and each a way to
the page that has the rest); first sign-in, for a template that declares one; the findings from
the operations diagnosis (`#attention`, which the header's verdict links to); the delivery
insights; and the recent deployments beside the preview environments from `2xl`. A service with no
public address draws its product and where it answers inside Docker in the preview's place. Deploy
a specific version picks from the commits the project's runs recorded, with no remote call, and
Connect a database takes a saved connection by its row. `GET /deploy/{id}/preview-frame` is an
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
card, the project header, the rail's scope head and the source end of the Notifications picture can
carry the site's own icon (the dashboard's image policy allows only its own origin). It reads the recorded endpoint's page for `<link rel="icon">` (then
`apple-touch-icon`), falling back to `/favicon.ico`, `/favicon.png` and `/apple-touch-icon.png`.
Every request stays on the recorded scheme and host, port included: a declared icon or a redirect
anywhere else is refused, not followed. It reads at most 512 KB of page and 1 MB of icon within five
seconds, accepts only a response that is an image, and remembers each project's answer for an hour
(an absence for ten minutes). The icon is served with `nosniff`, a sandboxing CSP and private
caching, so an SVG opened directly is a picture and not a document; `404 favicon_unavailable` means
`ProjectMark` draws the product the project is instead (`projectProduct`: its template, its image's
product, Compose, its framework, its recipe's language, Docker or nginx), and only a project no
product names keeps its workload glyph.

Deployments carries the delivery insights — a failure reason among them narrows the list to the
failed runs — then the runs under a strip of the last twenty outcomes and the environment picker,
grouped under In progress and then by day, narrowed by counted chips. A run is one shared row
(`run-row.tsx`): who started it as a face or a product, `#N Deploy` and the commit subject, then
branch (or the requested tag or commit) · sha · author · trigger, and its state, duration and time in
fixed columns, the duration with a bar that turns amber past twice the median of at least five
finished runs listed. Its verbs are declared once in `run-verbs.tsx` and shared with the run page's
header: Redeploy (live), Retry (only a `failed` or `cancelled` run), Cancel (never during activation
or a rollback) and, under the release's own name, What changed in this release (a comparison with
the release before it), Compare with live (a sheet), Roll back to this release (a retained release,
through a two-step dialog that names the domains, draws the swap and says what a rollback does not
restore) and Pin or Unpin. Older runs load on request through the `before` cursor. Runtime draws the
managed services as cards of their image's product, joined with Docker's container list, its stats
every ten seconds and an hour of history, the volumes every five minutes (for sizes and which
container uses one), the saved connections and the policy's backup job with its runs — every join
failing silently — then the live usage, the domains, storage beside backups, the dependency evidence
and the recorded charts, each block saying in its header when its owner could not be read. Logs and
Console keep their contracts ([request observability](request-observability.md) has the Logs page);
Logs offers the activation window of the live run when the run's log handoff names one.

Settings are nine pages — General, Build, Runtime, Environment variables, Domains, Storage, Databases
& backups, Automation, Danger zone — drawn in one frame (`settings/setting-card.tsx`): what is saved
but not live yet at the top, as a strip that names each change and links to its page but carries no
command (the header's is the one), then the page's readings, then its forms with their heads in a rail
from `xl`. Each form saves the whole configuration with the revision it read, keeps its draft keyed on
a digest of its own saved value (`useSettingDraft`) so a save of the form beside it no longer throws
the draft away, and ends in a foot with when the change applies, Discard and Save; a refusal that
names a field lands on that control (a refused row is marked in place), and one that names none
lands on the form. General holds the name, the Source (a repository, branch, root directory,
credential — read through the App's installation for a connected GitHub repository — submodule and
LFS choice, or an image reference and platform, changed in place through
`PUT …/environments/{env}/source` and checked before it is saved) and Automatic deployment: the Git
policy with a picture of what a push does, the last decision and the GitHub commit-status switch.
Build opens on what it builds with, the last build (read from the live release's own Build step), the
release tasks and the build variables, over the builder — every recipe, a Dockerfile or a static site
— the package manager, the commands, the image and the build variables as one form and the release
tasks, reorderable and
each with its last run, as another; choosing a builder that is not a recipe clears the recipe's
versions, secrets and package manager, which the plan would otherwise refuse. Runtime opens on where
it listens, the memory limit against the live release's last-hour peak, how a release is replaced —
naming a blue/green plan the executor would refuse, before it is deployed — and container access,
over the runtime, where it listens, resources, the release strategy and container access as one form
and the health checks, grouped by phase, as another. Environment variables is the list, its counts on
filter chips, each variable drawn as the service its name names and a reference as its database, with
Reveal, Copy value (the audited reveal route; the value is never drawn), Rotate and Remove, and a
removed name still live as a struck-through row; the editor is a sheet, and editing an existing
variable asks for its value again (an administrator can reveal the current one into it) so a scope
change can never blank a secret; the `.env` import is a sheet that previews every name through the
dry run before anything is written, and cannot import while any name is refused. Domains opens on four
readings and checks a new hostname in its Add domain sheet through `GET /deploy/hostname`, showing the
A record to create and, for an administrator, whether it already resolves here. Storage opens on four
readings joined from Docker's volume sizes and the backup coverage report, each refused read leaving
its reading out, and draws each host path as the Files page's folder in its colour, over the mount
editor and the live release's mounts as cards, with a card to back up any volume nothing copies. Databases & backups links databases
through the same sheet the creation flow uses — in a section of its own, since linking is its own write
— and never commits a half-filled sibling row when a database is connected or removed. It opens on
four readings — linked count, connection, backup policy and native-dump coverage — and draws each link
as a card carrying its engine, database, managed hostname, observed status, the variables that carry
it and the reason reconciliation recorded when it could not repair one, under a picture of how the
application reaches them. Because runtime activation attaches a database by reading the variable that
holds its address rather than the dependency row, removing a link offers to delete the variables that
reference it, and says so plainly when it is bound by a value it cannot name. The backup job a release
gates on is drawn as the Backups page draws it — its products, last run, destination, last fourteen
runs, next run and stored size — with the live release's observation folded onto the same card, runs
on demand, and warns when it takes no native dump of a linked database — which the gate would
otherwise refuse mid-deployment — with one press to add the dump; Automation opens on four readings
and a picture of what deploys the project, and holds webhooks (each with its deliveries in a sheet,
the hook URL shown absolute, and a signed hook's secret shown once when it is made and when it is
rotated), schedules (a builder in the schedule's own time zone, the server's check before saving,
and each schedule's past firings and next five runs), previews (approve with a fork warning, reject,
variables, deploy) and traffic alerts (the rule form in a sheet it shares with the Logs page,
removal confirmed), every block reading one set of polls (`useAutomation`); Danger zone holds Stop
or Start, Archive or Restore, managed-resource removal — whose plan loads as soon as the project is
archived — and permanent deletion, which asks for the project's name.

The deployment page (`/deploy/[id]/runs/[run]`) keeps the sequence-based stream, resync and the
5,000-event cap. It opens on an identity line — the source as its forge, the commit, who or what
started the run and where, and how long it has taken — then, on a project's first deploy, the
creation spine with *Deploy* as its last step, the release path (a stage the run did not include
drawn dashed), and how the run ended: a failure in words with the engine's code beside it and a way to
the failing step, the live address, or which release is live now. The build console paints its lines
through the painter the dashboard's own transcripts use (`components/transcript-line.tsx`), numbers
them, strips terminal escapes, groups each step's lines under a sticky rule, filters by stage and
errors (with a count), shows the time since the run began, wraps, follows, copies and downloads
`deployment-N.log`, and stays mounted while Details, Runtime logs or Metrics are open so its search and
scroll position survive the switch; the release path shows each group's duration; Details lists every
step attempt with its evidence and where in the run it ran; Runtime logs draws each source as its
image's product and keeps its server-provided windows, the one around activation offered as an
*Around activation* chip that opens the history there rather than as a second Live beside the
workspace's own. Metrics reads each figure as before → after, amber once a reading is half again
what it was (the traffic panel's rule for "slower"), over one strip per measure: the ten minutes
before and the ten after at equal widths on one scale, with a brand rule at the instant the release
went live. Its windows name a single-container release's series by container (`sources`; a Compose
release's stay unnamed, because its recorded runtime lists container ids without the service each
ran). A successful run shows Visit
only when its recorded release is the project's current live release; the success block waits for the
project read so it never flashes Superseded first.

Archived deployments are searchable at `/deploy?view=archived`, each drawn as what it deployed. They
offer Restore, which says that automatic deployments and schedules stay off, and a separate permanent
record deletion that asks for the project's name and lists what is deleted and what stays on the
server, preserving host resources and the audit log. See the
[permanent-deletion decision](permanent-deletion.md) for the API and transaction contract.
