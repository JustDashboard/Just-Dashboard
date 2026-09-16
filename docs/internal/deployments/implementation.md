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
- Deployment-selected paths use a dedicated `files.Service` scoped to `JD_DEPLOY_ROOTS`; Git, Docker,
  Compose and builder argv use `hostexec.CommandInDir`. The sole shell boundary remains an immutable,
  admin-authored stored release task (including migrated pre/post commands) with a resolved working
  directory and explicit scoped environment.
- Creation is a revisioned, owner-scoped server draft. Remote Git inspection resolves an exact ref into a
  private dashboard-owned bare mirror and detached temporary worktree; it never clones into or resets an
  operator checkout. Inspection's mirror is shallow and blob-filtered so planning stays bounded, so a
  release materializes through a second mirror that is neither: the run workspace is built by fetching the
  recorded object id into a fresh repository, because `git clone --local` ignores its local copy when the
  source is shallow and would silently produce an empty checkout. Registry inspection resolves a digest
  without pulling. Planning-time Compose validation uses private temporary files, an explicit empty env
  file and inert placeholders for detected variable names, so the backend environment and a checkout
  `.env` cannot influence the result.
- `/deploy/new` answers with quick deploy; the five-step wizard is `?mode=advanced`, and a `draft` in
  the URL means one is already in progress. Quick deploy drives the same draft endpoints in one screen
  for a Git repository, an image, or a database, and hands its own draft to the wizard for extended
  configuration. Connected GitHub repositories and manual HTTPS/SSH import are visible immediately;
  branch/tag and saved credential choices stay available before inspection. Compose stacks and adoption
  use the extended wizard. Blueprint and game-server creation are unavailable in this release; the
  catalogue reports the reason, and direct API calls are refused before deployment work is persisted. The screens share `deployment-defaults.ts` and `deployment-findings.tsx` so a default one
  flow relies on cannot be missing from the other, and a refused plan reads identically in both.
  Detected web and static plans carry a required HTTP readiness check: preflight raises `readiness_missing`
  as a *decision* for those profiles, so an empty check list made the most ordinary deployment there is
  unsavable over a control the operator was never shown.
- `GET /git/github/repos` and `GET /git/github/branches` list what the dashboard's own gh credential can
  reach, so a repository is chosen rather than transcribed. Both take no `?path=`: the credential
  consulted is the one every deployment clone will use, and the repository being chosen has not been
  cloned onto this server yet. A repository name is validated as owner/name before it reaches gh's argv
  or a REST path.
- `GET /deploy/hostname` proposes a public address and reports what HTTPS would take. A wildcard
  certificate the host already holds wins; failing that the answer is `<slug>-<random>.<a-b-c-d>.sslip.io`,
  which resolves to this server with no DNS record to create. `covered` is whether a certificate already
  covers the name — the only thing activation accepts — and `certificateMethod` is the HTTP-01 challenge
  this host could issue one with. Both are reported so the screen can say what the run will do, not so
  the operator has to do it.
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
  Settings → Lifecycle provides the separately previewed path for removing managed resources.
- Activation is unchanged and still the last word: it resolves an already-issued certificate/key pair
  through Proxy and fails closed if none exists. `provision_certificate` only makes that resolution
  succeed for a name the operator has just asked to publish.
- `GET /databases/{id}/url` is an admin read with its own audit entry and `Cache-Control: no-store`.
  Its default (or `?target=host`) returns the saved connection unchanged. `?target=container` prepares
  an application URL: a known database's loopback binding is matched to its observed Docker default
  bridge address and internal port. The observed binding must match the saved loopback IP; ambiguous
  localhost bindings are refused. It never publishes a new port, attaches networks, or changes the
  saved connection. An unreachable loopback service and SQLite fail with an actionable error.
  Remote URLs retain their credentials, options, SRV discovery and seed lists. Plain TCP MySQL driver
  strings become `mysql://` URLs; driver-specific query/socket options require manual configuration
  instead of being silently discarded. Bridge addresses must be reconnected if a database container
  is replaced or its address changes.
- Detected JavaScript commands name the package manager the checkout's single lockfile locks to
  (`bun`/`pnpm`/`yarn`/`npm run …`). The recipe picks its base image from that same lockfile, and
  `oven/bun` carries no npm, so a hardcoded `npm run build` was a build that installed cleanly and then
  died on `npm: not found`.
- Deployment preflight depends on a read-only observer: filesystem/proc capacity, listener inventory,
  Docker/Compose availability, proxy inventory and bounded DNS lookups. It cannot build, pull, start,
  stop, write proxy/firewall configuration, modify a checkout or enqueue a backup. The persisted exact
  plan excludes raw observed import material and accepts only typed secret references.
- Normalized build execution uses the project-owned versioned recipe set or an explicit Dockerfile,
  static, immutable-image, or Compose adapter. Reviewed base tags are resolved before rendering and every
  generated `FROM` is digest-pinned. Build secrets are BuildKit environment-backed secret mounts and
  never argv/build args; custom Dockerfiles with requested secrets or obvious embedded credentials fail
  closed because their layer history cannot be guaranteed.
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
  Docker tag only after inspection proves it still names the recorded config/digest.
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
  evidence carries its digest, not the configuration bytes.
- Stop uses the configured signal and a bounded grace period before Docker escalation; predecessor drain
  and activation/cancellation compensation use bounded contexts detached from request cancellation. A
  stop-first failure restarts the predecessor from its exact immutable runtime spec. Cancellation between
  persisted steps removes an uncommitted candidate, or finishes predecessor retirement after a committed
  cutover, and records sequenced cleanup evidence.
- Deploy and force-build resolve the current desired revision (force-build disables cache); redeploy and
  rollback clone only available immutable artifacts and traverse the same checks/cutover path; restart
  stops and starts the existing live runtime without creating a release. Rollback uses the destructive
  capability and ordinary confirmation, not a typed phrase.
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
- A configured backup dependency executes as a step before candidate start. The Backups adapter verifies
  coverage, success, freshness and any required restore-test evidence and returns only bounded evidence;
  a required failure terminates the run before a release/runtime or live-pointer change. Restore evidence
  is explicitly unavailable until the Backups owner persists it, never inferred from artifact existence.
- Import adoption is a dedicated, session-only admin commit that re-runs the read-only preview and requires
  exact acknowledgement of unsupported observations. It records the external resource as observed and
  does not start, stop, reset or claim it. Archiving only disables deployment triggers and visibility; it
  never removes runtime or data. A separate destructive route first returns a digest-bound, managed-only
  target list; data targets require their exact resource name and every removal is delegated to its owning
  feature and audited. Linked and observed targets never enter that plan.
- Remote Git production branches are monitored automatically after the first explicit deployment,
  including existing projects; there is no opt-in switch. `GitWatcher` checks refs every five seconds
  with four bounded concurrent observations using the source adapter's credential isolation and
  `git ls-remote`. This works behind the dashboard's private network allowlist without public ingress
  or GitHub hook registration. Outages and slow Git reads can delay detection; it is polling, not an
  instantaneous push-delivery guarantee. Tags, local checkouts, legacy Compose and archived projects
  are excluded. Monitoring status and access failures appear on the overview and Automations page.
  `deploy_git_watches` persists the last attempted revision and observation generation: restarts,
  failed runs, duplicate provider deliveries and a crash after enqueue cannot cause repeated builds;
  a later branch change (including a force-push back to an older commit) remains eligible. Watching
  never advances a live release pointer. Eligible web/static projects retain blue/green health-gated
  activation; exclusive-storage and other stop-first workloads retain their documented downtime and
  recovery contract. Archiving stops monitoring.
- Deploy/force-build source resolution records the current remote Git object in the additive,
  immutable `deploy_runs.source_revision` field before enqueue; execution derives its source identity
  and digest from that frozen revision. Retries copy it, while redeploy/rollback reuse their selected
  release's identity. Old runs keep their saved plan identity. The deprecated draft `autoDeploy` input
  remains accepted for compatibility but has no effect; draft commit no longer creates a hook without
  a secret or provider registration. Automatic runs are audited as `deploy.git.change` and use the
  same persistent queue, configuration/variable snapshots, checks and activation as manual runs.
- Additional automation provider hooks verify each provider's exact raw-body signature before parsing and then fence
  event, repository, ref and delivery identity. The delivery row is reserved before preview or queue side
  effects, while legacy HMAC and scoped generic hooks retain their existing contracts. Watch paths apply
  only to webhook delivery; default branch monitoring, manual and rollback runs are never filtered.
- The scheduler advances a persisted next-run claim atomically and executes a bounded, ordered action
  chain. Chain history stores only action/status/error-code/duration evidence. Preview environments clone
  immutable desired configuration and sealed variables, inherit only linked/observed dependencies, and own
  only their generated route/runtime; PR close retires those exact preview resources before archival.
  Outbound notifications sign the exact JSON body, keep headers and signing keys sealed, discard response
  bodies, and can warn but never change an otherwise successful deployment outcome.
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
- The fleet read model batches every per-deployment join. `liveReleaseHealths` and `latestProjectRuns`
  use `IN (…)` and `ROW_NUMBER() OVER (PARTITION BY …)`, and `DeploymentSummary` reads one deployment
  rather than filtering the whole fleet in Go. Both are pinned by statement-counting tests: the cost of
  a fleet read is fixed in the number of deployments, and a regression fails rather than slows.
- Blueprint deployment is currently unavailable: source materialization, immutable image resolution,
  generated variable persistence and lifecycle automation are not connected end to end. Catalogue and
  detail responses expose `deploymentSupported: false` and `unavailableReason`; previews remain pure.
  Source saves, preflight, commit, materialization and new deployment queue admission reject blueprint
  sources. Existing workload read, restart and removal controls remain available. Preview volume names
  include a hash of the unnormalized name and blueprint id, Docker-socket mounts are linked and read-only,
  startup budgets use bounded retries, and Bedrock does not receive Java save commands. UDP remains an
  explicit unsupported runtime protocol, never silently converted to TCP. Completing this feature still
  requires durable resource identities and runtime integration; removing the gate alone is unsafe.
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
- Closed vocabularies, route capabilities/confirmations/audit actions, retention limits and error codes
  are contracts. Change one only with an ADR plus migration and exhaustive transition/route tests.

## Public deployment ingress

Docker Caddy public listeners are reused for automatic HTTPS and deployment routes. On a fresh host
with unclaimed TCP 80/443, the first deployment provisions a persisted public Caddy automatically.
The [ingress decision](caddy-ingress.md) specifies ownership, additive snapshot fields, certificate
evidence, lifecycle repair, supported layouts and verification. The dashboard remains private.

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

## Deployment observability workspace

The active fleet opens as a responsive project grid with release, address, health, latest-run, and
pending-change evidence. Search remains visible; a filter popover holds state, type, environment and
pending-change filters. Cards expose project, logs, settings and Visit actions. Wide screens offer a
compact table view without resetting filters; narrow screens retain cards. The header count reflects
the filtered result and total.

Overview centers on the live deployment: a website preview, Visit, release/source and health,
recent runs, branch context and compact measured CPU/memory usage. Runtime, Diagnostics and Settings
own the detailed evidence and forms. The preview loads automatically for a live release with a
recorded HTTP(S) endpoint, with mobile/full-width modes, reload, close and a direct website link.
`GET /deploy/{id}/preview-frame` is an
authenticated, non-cacheable static HTML wrapper with no scripts. Its CSP permits a child frame only
from the recorded endpoint's origin and allows the wrapper itself to be framed only by this dashboard.
This is a deliberate exception to the API's default frame denial; the dashboard document's CSP stays
unchanged. The embedded website is sandboxed without top navigation, popups, or downloads. No website
request is made by the backend and dashboard request headers are never forwarded to it. The browser
applies its normal cookie and mixed-content policies. Sites that disallow embedding, require login,
or use an insecure URL under an HTTPS dashboard may require the direct website link. A preview is
not a deployment health check and does not bypass the site's own framing policy.

Deployment section links use the shared tab appearance. Opening a section directly or resizing the
workspace reveals the selected tab within its horizontal strip without scrolling the page content.

The deployment Metrics tab selects from the observed runtime services and embeds the Docker owner's
per-container stats WebSocket and recorded CPU, memory, network, and block I/O charts. Live readings
are separate from retained history and release activation comparisons; disconnected readings retain
an explicit last-sample label. Selecting another service remounts its stream and charts so samples
cannot carry across container identities. Missing runtime evidence renders an unavailable state.

The primary deploy action remains in the workspace header. Restart, redeploy-live, uncached build,
and archive are in the deployment actions menu, with capability checks and active-run restrictions.
Archive uses an ordinary confirmation and retains runtime resources and recorded history.

The creation landing page pairs repository import with database, image, Compose, template, game,
worker, static-site and existing-workload entry points. GitHub includes account controls, owner and
repository filters, refresh and branch selection. Manual import accepts HTTPS/SSH Git URLs. Compose,
template, game, worker, static-site and adoption entries preselect their workload profile in the full
wizard; existing drafts keep their saved intent. Quick application setup shows the
selected source and branch before configuration. Project type and detected build settings are
editable; a Dockerfile supports other languages and custom builds. Missing detection keeps the build
settings open. Environment values have masked key/value rows and a separate dotenv import disclosure.
The database sheet creates or links a connection, explicitly reads its container URL, adds the chosen
environment key and records a linked database dependency before deployment. Created databases remain
independent resources if project creation is abandoned. Retrying setup reuses the created container,
including after closing and reopening the sheet. Compose projects declare database services in their
Compose file to share its network. Deployment names are validated on blur and again before submission.
Opening the full wizard first saves edited quick-form settings. Environment values travel separately
in memory within the current browser tab and are imported after the wizard commits the project; they
never enter draft configuration, URLs, or browser storage. The wizard explains that refreshing clears
these unsaved values and preserves them for copying if the post-commit import fails.

Quick creation records the committed project before importing variables or requesting its first run.
Failures after commit show the saved deployment and a link to Environment variables or run history.
They do not offer to recommit the draft. Unimported environment text stays available in the current tab for
copying; it is never placed in the URL or browser storage. A successful configuration save also
advances the local draft revision before preflight, so a preflight failure does not leave retry using
an outdated revision.

Run pages default to a compact progress summary and one build transcript across all execution stages.
The transcript splits streamed chunks into numbered lines, removes terminal color escapes, shows time,
highlights warning/error text, and supports search, stage/error filtering, wrapping, follow-tail and
copying visible lines. Replay remains sequence-based and retains at most 5,000 log events. Execution
details, application runtime logs and metrics are separate views. A successful run shows Visit only
when its recorded release is the project's current live release; older runs link back to the project.

Settings has dedicated Build settings, Runtime settings, Environment variables, Domains & ports,
Storage, Databases & backups, Automations and Lifecycle destinations. Variable edits and automation
creation use sheets. Automatic branch monitoring has read-only status; Automation separates additional
webhooks, schedules, previews and notifications;
one-time signing secrets remain visible after either provider or notification creation. Dependency
pickers show resource names, while the owner modules retain execution authority. Game console output
uses numbered lines, and the raw server settings file is available through a disclosure.

Archived deployments are searchable at `/deploy?view=archived`. They offer a separate permanent
record deletion with explicit confirmation, preserving host resources and the audit log. See the
[permanent-deletion decision](permanent-deletion.md) for the API and transaction contract.
