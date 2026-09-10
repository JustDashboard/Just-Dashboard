# Deployment implementation

The 0.6.7 deployment contract is frozen in `docs/plans/0.6.7-deployments/09-frozen-contracts.md` and its
ADRs. `internal/deploy` owns desired deployment configuration, immutable releases, persistent runs,
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
  operator checkout. Registry inspection resolves a digest without pulling. Planning-time Compose
  validation uses private temporary files, an explicit empty env file and inert placeholders for detected
  variable names, so the backend environment and a checkout `.env` cannot influence the result.
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
- HTTP, TCP, Docker-health, command and public-route checks have closed configuration, per-attempt
  timeouts and bounded retries. Persisted evidence contains status/state/error codes and a digest of
  bounded command output, never response bodies, command output, URL credentials/queries or runtime
  variable values. Disabled, unavailable, warning, passed and failed remain distinct outcomes.
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
- Automation provider hooks verify each provider's exact raw-body signature before parsing and then fence
  event, repository, ref and delivery identity. The delivery row is reserved before preview or queue side
  effects, while legacy HMAC and scoped generic hooks retain their existing contracts. Watch paths apply
  only to webhook delivery; manual and rollback runs are never filtered.
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
  unassessed diagnosis have distinct wording; the remaining C8 summaries are still in progress.
  Runtime rows also link to the exact container source in Logs. Logs preserves explicit time-window
  links and refuses to substitute another source when the requested container is no longer discoverable;
  `GET /deploy/{id}/runs/{run}/logs` checks project membership and filters Docker by the run's own
  environment and candidate/release id, including preview environments. It provides live source links
  and a ten-minute history window around the latest successful activation step's completion only when
  its persisted evidence identifies that release and contains no recovery. It never uses the current
  live release's activation timestamp to describe an older run. Missing/failed activation has no history
  link; removed runtimes point the operator to the transcript or an external log archive. The run page
  renders these application log links separately from the orchestrator transcript, and the project Logs
  tab offers the observed runtime sources.
- Closed vocabularies, route capabilities/confirmations/audit actions, retention limits and error codes
  are contracts. Change one only with an ADR plus migration and exhaustive transition/route tests.

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
On a re-run it fills a missing variable into an older `.env` but never *moves* a recorded port: the process
holding it is this dashboard, and telling that apart from a squatter is a guess that breaks a working
install when wrong. `internal/selfcfg` is the other half — changing a port from the dashboard probes the
new one first and rolls the whole change back if the stack does not come up on it.

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
