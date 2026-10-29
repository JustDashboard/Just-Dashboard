# Invariants that must not regress

A change that weakens any of these has to say so explicitly.

1. The network allowlist runs **before** authentication.
2. Two-factor is *enforced where it applies*, and this is the one invariant that is now a policy rather
   than a constant — see [Invariant 2](#invariant-2-what-two-factor-still-guarantees) below. An account
   with an authenticator is **always** asked for a code, and a session that owes one reaches nothing but
   the 2FA routes. What `JD_REQUIRE_2FA` decides is whether an account that has *not* enrolled may sign
   in at all. It defaults to false.
3. Every destructive action is behind `s.destructive` — capability, `destrLim`, audit entry. Actions
   with a meaningful accidental-click risk pause the operator with a confirmation dialog. A **subset**
   also requires the typed `X-Confirm` phrase,
   enforced server-side inside the handler. See below. A route that also serves routine, non-destructive
   operations — the deployment run route's `stop`/`restart` alongside `deploy`/`redeploy` — cannot be
   wrapped in `s.destructive` wholesale, so it enforces the same capability and `destrLim` budget by hand.
4. Capability checks live on the route, never in the UI alone. Where the answer depends on what is *in* the
   request, the handler checks by hand and fails closed, and each such check has one owner:
   - `api.authoriseSQL` for SQL, on the verdict `dbx` reads off each statement by the rules of the
     connection's own engine (`dbx.ParseScript`): the query, script and analysed-plan routes. Where a
     statement ends is the engine's reading as well, a stored program's body included, and a statement
     that holds statements of its own is never below `high`, so a protected connection refuses it;
   - the handler of `POST …/changes` for a change set, by the operations it holds: one that deletes
     rows needs the destructive capability;
   - `api.runDDL` for a schema form that changes a column's type or whose own SQL (a CHECK condition,
     an index predicate, a USING conversion, a function default) calls anything `dbx` does not vouch
     for — either needs the destructive capability on a route that otherwise asks for
     `service.control`, for a preview as much as for a run. The same owner holds the routes that store
     code in the database (`POST …/ddl/routine` and `…/ddl/trigger`): the query runner classifies every
     `CREATE FUNCTION`, `PROCEDURE` and `TRIGGER` as high risk, so the form asks for what the console
     asks, on every request; and `PATCH …/ddl/row-security`, where turning row-level security off or
     no longer forcing it on the owner (`dbx.RowSecurityChange.TurnsOff`) is what makes it destructive;
   - `dbx.MaintenanceAction.NeedsDestructive` for a maintenance action that locks a table against the
     application or can lose rows, and the body of the power route, where `stop` and `restart` are
     what make the request destructive;
   - `api.planDBLimits` for a change of a database container's limits, where lifting one replaces the
     container;
   - `dbx.RedisClassify` for a Redis console command, and the body of the Redis bulk, rename and copy
     routes, where `action: delete|expire` and `overwrite` are what make the request destructive;
   - `dbx.MongoClassifyPipeline` and `dbx.MongoClassifyCommand` for a MongoDB pipeline and console
     command, and `mongoNeedsDestructive` for the one option an update, rename, index or `collMod`
     route has that removes data;
   - the options of an import, where replacing a table's contents is destructive, and the target of
     a restore, where a database to be created needs `system.admin`;
   - `api.authoriseSpec` for a container spec that is privileged or mounts a host path;
   - `api.authoriseNetworkSpec` for manual Docker network creation: custom drivers and driver options
     require `system.admin`, reserved ownership labels are refused, and explicit pools may not contain
     the observed dashboard client address;
   - the Docker network connect, disconnect, removal and prune handlers, by the conflicts
     `dockerx.PreviewConnect`, `PreviewDisconnect`, `PreviewRemove` and `PruneCandidates` read from the
     network's dependents: the dashboard's own containers, the shared ingress and database-link members
     are never detached, nothing joins the dashboard's own network, a live deployment's network is not
     removed by hand, and an unreadable dependency reading refuses the change;
   - the generic job handlers for `network.diagnostic.*` jobs: lists filter them and get/stream/cancel
     require `system.admin`, matching the saved-artifact routes;
   - `api.boundaryGate` for a fail2ban ban, a CrowdSec decision and an SSH settings change: one that
     cuts this session's own way in is refused, and one that touches the dashboard's access boundary
     for anybody else (an allowlisted network, the tailnet's previews, the SSH tunnel) needs an
     explicit `acknowledgeBoundary` (`netsec.BoundaryImpacts`,
     [observability-security](../backend/observability-security.md#the-access-boundary));
   - `api.logTargetFor` for a log source that is login and sudo records (auth data needs
     `system.admin` on every `/logs` route that reads a source — except the whole journal
     (`journal:`), which stays `read` as it was before the gate; those lines are in it unfiltered, a
     known gap rather than the boundary
     ([observability-security](../backend/observability-security.md))).

   An API key can be narrowed to one capability, and that fails closed too. A key with a `scope`
   (today only `metrics.read`, made by `CreateScopedAPIToken`, always on the read-only role) is
   refused by `Authenticate` on every group with `403 key_scope` before any capability is asked;
   the one group that admits it, `AuthenticateScoped` in `mountDatabaseMetricsExport`, also
   requires a key rather than a session (`httpx.RequireToken`), and `Principal.Can` answers only
   the scoped capability, so a route added to that group later cannot widen the key.

   An MCP key (`scope = 'mcp'`, made by `CreateMCPToken`) is refused the same way by `Authenticate`
   and `AuthenticateScoped` alike, and opens only `/api/v1/mcp` through `AuthenticateMCP`, which
   takes no session cookie and no other key. There it keeps its role — its owner's or one made
   narrower, never more than the owner holds now — so a statement is judged by `authoriseSQL`
   exactly as the SQL editor judges it for that role — a destructive one
   still needs `destructive` and spends the same budget — and the key's grants narrow it further:
   only the connections `api_token_connections` names, each `read` or `write`, re-read on every call,
   and never a protected connection, which is refused for every tool, reads included. The
   deployments are a separate grant, `api_tokens.deploy_access` (`''`, `read` or `deploy`, none for
   every key made before it): a deploy or a rollback goes through the same enqueue functions the
   deployment routes call and asks the key's role, narrowed to its owner's now, for the capability
   the route asks — `service.control` to deploy, `destructive` and the destructive budget to roll
   back. A walk of
   every route with an MCP key (`TestAnMCPKeyOpensNoOtherRoute`) holds the first half; the grants
   are re-checked by the handler rather than trusted from the key.

   A deploy key (`scope = 'deploy'`, made by `CreateDeployToken` on `POST /tokens/deploy`, which an
   administrator reaches only in a session) **deliberately changes the session-only rule** for part of
   the deployment surface. Until it, every deployment write that handles a secret — drafts,
   configuration, source, Git policy, triggers, variables and shared variables — answered any API key
   with `403 session_required`. A deploy key reaches those writes; nothing else changed for any other
   key. The operating verbs beside a deploy that an ordinary key already had — promoting staging
   (`POST …/promote`, which also takes a pull request preview's approved image into staging for an
   administrator, the role the key carries; approving the commit stays a session's), the warm
   release's instant rollback and keep (`POST …/rollback-watch/rollback`,
   `…/keep`), a canary's promote and abort (`POST …/canary/promote`, `…/canary/abort`) and the
   maintenance switch (`PUT …/maintenance`) — are the deploy key's as well, with their reads
   (`GET /deploy/{id}/environments`, `…/promotion`, `…/rollback-watch`, `…/canary`, `…/maintenance`,
   `…/pages/{page}`, which is what every visitor is shown, `…/rehearsal`, the terms alone, and
   `…/data-copies`, what each copy is of and until when, never its sign-in). It
   holds the administrator role (narrowed to its owner's now, like every key), and where it
   goes is decided once, before any other middleware of the route runs: `Authenticate`,
   `AuthenticateScoped` and `AuthenticateMCP` refuse it, and the feature group's
   `httpx.AuthenticateFeatures` admits it only when `api.deployKeyRoutes` (`deploy_keys.go`) has a
   line for the exact route the router's own `Find` resolves (a mount's root, which the router serves
   with and without its trailing slash, being one route; no two routes differ by the slash alone). A route without a line answers
   `key_scope`, so a route added later is refused until someone writes its line; a line marked
   session-only answers `session_required` with the reason. Admission is what lets the key past
   `httpx.RequireSession` (`Principal.DeployRoute`), on that route and no other. The API's own
   description, `GET /deploy/openapi.json` and `.yaml`, is a static document any signed-in account
   and any key read, a deploy key included; it derives each operation's security from this table and
   from `RequireSession` in the route's chain, and a test walks both keys to check it
   ([`../backend/deploy-api.md`](../backend/deploy-api.md)). What stays a
   session's whatever the key: every reveal (`…/variables/{name}/reveal`, shared variables, the legacy
   `…/env/reveal`), the saved registry and Git credentials (list, create, update, test and delete), the secret-store
   connections that hold credentials of their own (create, update, test and delete),
   the pull-request merge and comment that speak as the dashboard's GitHub account, preview approval,
   the removals that take a typed phrase (`DELETE /deploy/{id}/permanent`, `remove-managed` and its
   plan), the terms of the warm-release window (`PUT …/rollback-policy`) and of migration rehearsal
   (`PUT …/rehearsal`, which chooses a database whose backup is restored), a copy of a release's data
   (asking for one, revealing its address, keeping it longer, removing it and comparing it: a second
   copy of production's data whose address carries a password), what a linked database gives
   previews and a preview's database branch restored again (`PUT …/database-links/{connection}/previews`,
   `POST …/pull-requests/{number}/preview/branches/{connection}/refresh`: a second copy of production's
   data next to a pull request's code), and a route's maintenance
   and error pages (`PUT …/pages/{page}`, files on the server beside the proxy's route). The
   secret-store list (`GET /deploy/secret-stores`), which an ordinary key reads, answers a deploy key
   `key_scope`: it carries each connection's address and sign-in fields and every deployment's
   variables that read it, and the key cannot point a variable at a store. Variables are write-only to
   the key: lists carry names and digests, and
   `withholdGeneratedValue` drops the value a generate or rotate would return. A key narrowed to some
   deployments (`api_token_deployments`; none is every deployment) reaches only routes about those —
   the gate checks the route's `{id}` and that its `{env}` belongs to it — lists only those, and none
   that reach what every deployment shares (server shared variables, fleet-wide reads). Drafts are
   every deploy key's own: a draft records the key that made it (`deploy_drafts.key_id`, 0 for a
   session), and a key lists, reads, saves, detects, preflights, commits and discards those alone
   (`deploy.AuthorizeDraftForKey`, through `deploymentDraftForPrincipal` on every draft route), never a
   session's or another key's, while an administrator's session still sees every draft of its
   account. A narrowed key's commit adds the deployment it creates to `api_token_deployments` in the
   commit's own transaction (`DraftCommitRequest.GrantToKey`, set by the handler and never read from a
   body), so the key reaches what it made from the next request and nothing that failed to be made; a
   key reaching every deployment is given no row, since the first would narrow it. Duplicating takes a
   deployment the key reaches and leaves a draft that is the key's. Because a host-level setting or a
   saved credential sent somewhere new would hand the key what the session-only routes keep from it,
   the handlers hold what the key writes (`hostAuthority`, `keySourceGuard`, `keyReferenceGuard`): no
   privileged container, host network, capability, device, host path, host-shell release task,
   folder on the server or Compose file, in a plan it commits or an environment it changes — and an
   environment that already has one, or whose variables a person pointed at a saved credential, is
   changed (configuration and source) by a person only, though the key may still deploy it and set its
   variables; no saved credential chosen, or a Git credential sent to a different repository; no
   variable pointed at a saved credential, a database or a secret store — checked on a draft at commit, where its
   variables reach the form they are stored in — nor, for a narrowed key, at a server shared variable.
   A key that can change a deployment's source and deploy it can still run code that reads that
   deployment's own variables at run time, a database reference a person gave it included:
   write-only is a property of the API, not a sandbox. The walk `TestADeployKeyGetsTheDecidedAnswerOnEveryRoute` holds the table on every
   route of the API and an ordinary key's answers as they were,
   `TestEverySessionOnlyDeploymentRouteHasADeployKeyDecision` fails when a session-only deployment
   route has no line, `TestADeployKeysDraftsAreItsOwnAndANarrowedKeyReachesWhatItMade` holds the
   drafts and the grant, and `TestADeployKeyOperatesTheDeploymentsItReaches` the operating verbs,
   their reads and a narrowed key's refusals, each write audited under the key.

   A database connection marked read-only adds one rule in front of all of these, and it fails closed
   the other way round: `protectReadOnlyConnections` refuses every request under `/databases/{id}` that
   is not a `GET` unless its route is on an allowlist, so a mutating route added later is refused until
   somebody decides it belongs there. The routes on the list that are a read or a write by what they
   carry are judged by the same classifiers as above. It guards the dashboard's own controls; it is
   not a sandbox around the server. Each check and the rule are stated in
   [request lifecycle](../architecture/request-lifecycle.md#checks-that-depend-on-what-a-request-carries).
5. Every state-changing request lands in the audit log. A request made with an API key carries the
   key's id and name (`audit_log.key_id`, `key_name`) as well as its account, and a deploy key refused
   by the gate is written as `auth.deploy_key.refused`, reads included, since it is answered before the
   group's audit middleware runs.
6. Client-supplied paths go through `files.Resolve` — including the ones that do not look like file
   operations (bind-mount source, build context, a new stack's directory). Host commands go through
   `hostexec` with an argv, never a shell string. Request-defined shell source is confined to `deploy.Deployer.shell`, deliberately:
   those are pipelines an admin stored for their own project, not anything supplied per request. Do not add
   a second request-defined shell, and do not "fix" that one into an argv. A git clone's parent and an
   `init` target go through `gitx.ResolveDir` against `JD_GIT_ROOTS` — the Git page's own boundary — and a
   clone can only create a directory that does not exist. Terminal startup also uses
   a bundled constant bootstrap to load the native prompt; paths remain separate positional arguments. An
   apt command that must first finish an interrupted dpkg run is wrapped in the constant
   `updates.finishDpkgScript`, with the command and its validated arguments as positional arguments. `dockerx` invokes the `docker` binary in three places
   (compose, the streaming runner, `Build`) because the Engine API has no equivalent; all three build argv
   explicitly. A database dump that is a SQL script is replayed by `dbx` over the dashboard's own
   connection (`dump_script.go`) and never piped to `psql` or `mysql`: each of those runs a shell for a
   line of what it is fed (`\!`, `system`), which would make an uploaded dump that second shell. A
   major-version upgrade runs the servers' own clients inside their own containers through
   `dockerx.ExecPipe`, each an argv with any password in its environment: `pg_dump` read by `pg_restore`
   (an archive, never a script), and `psql` only with statements `dbx` wrote, each one `-c`. The roles'
   script `pg_dumpall` writes is replayed by `dbx` over a connection, as a restored script is.
7. Nothing but Caddy binds a routable address. The one exception is not the dashboard's own listener:
   a pull request preview is reachable at `https://<node>.<tailnet>.ts.net:<port>` because **tailscaled**
   listens on the host's tailnet address for ports **21000–21999** on the dashboard's behalf
   (`selfcfg.TailnetServe`, `tailscale serve`), forwarding each to a loopback port the dashboard
   published. The preview's container publishes on loopback exactly as production does; the dashboard
   refuses a port in that range that is served with any other target or funnelled to the public internet,
   withdraws a mapping that came back funnelled or whose record could not be written, and at start
   withdraws only a plain loopback mapping in the range that the address table does not vouch for — a
   port no preview owns, or an owned port serving an upstream other than the recorded one. A directory,
   a funnel or any other target of the operator's is never touched, at start, on a failed activation or
   on removal, since restore and withdrawal act only on a mapping whose upstream the row recorded. See
   [preview isolation](../deployments/preview-isolation.md#tailnet-only-addresses).
   The second exception is Remote Desktop's stream bridge: on a server on a tailnet it publishes a
   block of 20 UDP ports from 40000 on the **tailnet address only**, for WebRTC
   ([Stream mode](../backend/remote-desktop.md#stream-mode)). Nothing is sent on them without a
   stream negotiated over the bridge's WebSocket, which is behind the dashboard's session, and no
   block is published on a server off a tailnet.
   A database's port published from its Network page — on one of the host's own addresses, on every
   interface kept to a list, or to anywhere — is not the dashboard's socket either: it is the operator's
   explicit choice, in the destructive group and audited as `database.access.change`. A list is kept by
   the network module's gateway table, whose prerouting drop runs before Docker's translation, never by
   a host firewall rule that a published port's traffic does not meet
   ([databases](../backend/databases-proxy-platform.md)).
   `GET /security/boundary` reports both halves as observed — a dashboard socket other than Caddy's
   on a routable address, no Caddy on the configured port, or a preview port that is funnelled or
   serves anything but a loopback upstream is a broken boundary on the Security overview — but the
   report is evidence, not the enforcement.
8. Store schema changes are additive and tolerate an existing database. `CREATE TABLE IF NOT EXISTS` is a
   no-op against a table that exists, so a **column** added later also goes in `store.addedColumns`, which
   `applyAddedColumns` ALTERs in at open. Every entry needs a `DEFAULT` (SQLite refuses a NOT NULL column
   on a populated table without one) and **no entry is ever removed** — the list is the path from every
   shipped schema to the current one, not a description of the current one.

9. A blueprint is data, never a script. `internal/blueprint` parses with unknown fields rejected and
   validates at package initialization, so a built-in that breaks the supply-chain policy cannot reach a
   running dashboard. Install/release/startup/stop steps come from a closed operation vocabulary; there is
   no "run this string" kind, and adding one would be adding a second request-defined shell by another
   name. No blueprint may ship a default credential, publish a database port, declare a stateful workload
   without persistence, download without https plus a size limit and a checksum or declared version
   source, name an uncontained path, or take privilege without a written reason. A multi-service
   (Compose) template publishes exactly one port — its public service's, on loopback — keeps every other
   service on private networks the ingress never joins (`internal: true`, with no route out unless the
   service declares egress), takes no privilege, device, host
   network or Docker socket, and carries values only as typed references (`{{secret.…}}`,
   `{{variable.…}}`) that render as interpolations of the deployment's sealed variables, so its document
   holds no value; commands and healthchecks are argv that are never templated, so neither an
   operator's input nor a secret reaches a process list or a shell. The stored document is the server's
   own render of a shipped version, never one a client sent under a template's name.
10. The game console is not a shell. `gameserver.ValidateCommand` refuses anything carrying a shell
    metacharacter, newline or NUL, the command reaches Docker exec as separate argv elements, and player
    actions come from a closed set with the account name validated before interpolation. The same
    validator gates the console route and the scheduler, so a schedule cannot send what the console
    refuses.
11. Requested diagnostics claim only what an owner returned. Unavailable measurements carry a
    reason — never as a healthy result, and never as a problem. A finding that cannot name an action or a
    deep link says which external action is required instead.
12. Linking to a just-dashboard.com account adds no inbound path and no second authority. A command from the
    site runs only if its signature verifies against the site key pinned at link time, it has not expired or
    been seen before, and its kind is within the scope chosen on this server, and each is written to the audit
    log whatever its outcome; the kinds are a closed set that call the services the pages call. Linking,
    changing the scope and unlinking are session-only in the browser (an API key is refused) and root-only at
    the terminal, the heartbeat is a fixed field set that names nothing on the machine below `operate`, and
    the installer never handles a link credential. See [account link](../backend/account-link.md).

## Invariant 2: what two-factor still guarantees

Two-factor used to be unconditional, and the reasoning was sound for the install the product was first
written for. It was wrong for the one it is actually installed into: this dashboard is reachable only
over a tailnet or an ssh tunnel, both of which authenticate the network before a packet reaches the login
page, and making an authenticator app compulsory turned the first ninety seconds of a single-operator
install into a chore that could not be skipped.

What did **not** change, and must not:

- An account with `totp_enabled` is asked for a code at every sign-in, whatever `JD_REQUIRE_2FA` says.
  `Service.Login` decides on the account, not on the policy.
- A session that owes a second factor is never elevated. `httpx.Authenticator.resolve` refuses it
  everywhere but the 2FA routes, exactly as before.
- Where `JD_REQUIRE_2FA` is true, an account with no authenticator gets a password-only session that
  reaches nothing but enrolment — the old behaviour, unchanged.
- Turning an authenticator off is the account holder's own action, requires their password, and is
  refused outright by `Service.DisableTOTP` where the policy demands one.
- Setup, enrollment confirmation and code verification share one account-scoped attempt budget.
  Enrollment cannot be used again after TOTP is enabled. An accepted TOTP counter or recovery code is
  consumed atomically with session elevation; another session cannot reuse it.
- A passkey replaces the password and nothing else. A passkey sign-in starts its session through the
  same `auth.startSession` a password sign-in does, so an enrolled account still owes its code (the
  session reaches only the 2FA routes until it is given) and `JD_REQUIRE_2FA` still decides only
  whether an unenrolled account may sign in. A passkey is never counted as the second factor; making
  it one would be a change to this invariant, and is not made. See
  [passkeys](../architecture/runtime-boundaries.md#passkeys).
- A temporary password does not grant feature access. After required factors, only authentication
  status/logout and the session-only password change are available until `must_change_pw` is
  cleared. API tokens cannot bypass this restriction.

The one deliberate loosening: with the policy off, an account that has never enrolled signs in on a
password alone. `auth.Service.Login` elevates that session at creation, and `ResolveSession` completes a
session that was left half-authenticated when the policy changed under it — without that, turning the
setting off would strand everyone who was mid-flow with a session that can never be elevated.

## Invariant 3: which routes take a typed phrase

Typed confirmation is reserved for six deletion operations:

- Permanently deleting an archived deployment project (`DELETE /deploy/{id}/permanent`) requires the
  project's name. Archiving a project is reversible and uses ordinary confirmation.
- Dropping an entire database (`DELETE /databases/{id}/database`) requires the database name.
- Taking down a Docker Compose stack requires the stack name, through both the HTTP and WebSocket
  actions. The other Compose actions use ordinary confirmation.
- Removing a managed Compose stack from an archived deployment's removal plan requires that stack's
  name. The same plan presents volumes, paths, containers, and other managed resources with ordinary
  confirmation.
- Deleting a Git checkout from the server (`POST /git/repository/delete`) requires the checkout's
  directory name, because the uncommitted files, stashes and unpushed branches it holds exist nowhere
  else. Its preview (`GET /git/removal`) counts them first, and a root, the dashboard's own install,
  a repository with worktrees or other checkouts inside it, and a locked worktree are refused before
  the phrase is asked for. Removing a clean linked worktree through `/git/worktree/remove`, deleting a
  branch, and every other Git deletion keep ordinary confirmation.
- Uninstalling the dashboard with a wipe (`POST /dashboard/uninstall` with `mode: "wipe"`) requires
  the server's host name, because it removes every deployment with its managed volumes and paths and
  deletes the dashboard's database and the key it is sealed with. Uninstalling and keeping everything
  takes ordinary confirmation: running `install.sh` again restores the dashboard as it was.

These are the only places the UI asks the operator to type a phrase. Other destructive operations,
including table and collection deletion, data restores, Docker volume removal and pruning, account and
board deletion, Git discard/reset and discard-and-pull, firewall and SSH changes, package changes, certificate export and
revocation, exporting the backup key (`POST /backups/key/export`), self-update, and an uninstall that keeps the data, use an ordinary confirmation in the UI. Read-only actions and routine
mutations may need no dialog. The absence of a typed phrase does not relax capability checks, the
`destrLim` rate budget, audit entries, path containment, or host command rules.

Dropping a Redis logical database is the same drop route, where it is a `FLUSHDB`. The Redis bulk route
(`POST /databases/{id}/keys/bulk`) will not stand in for it: `delete` or `expire` with a pattern that is
every key (`*` and no `type`) is refused with `400 whole_database` and a pointer to the drop route, so a
pattern box left at its default is not a database emptied on an ordinary confirmation. A narrower pattern
that happens to match everything is not caught; the dry run that precedes a bulk action reports `matched`
beside `total`, which is what its confirmation has to show. The Redis console's `FLUSHDB` and `FLUSHALL`,
like `DROP DATABASE` through the SQL query route, take the destructive capability and no phrase: a console
is where an operator types the statement itself.

The server enforces the six phrases inside their handlers. The browser sends an
`encodeURIComponent`-encoded value in `X-Confirm` with `X-Confirm-Encoding: uri`; the backend decodes
it once, requires valid UTF-8, and compares the exact phrase. Legacy unencoded headers remain
supported. Only the Compose WebSocket action accepts a phrase in a query parameter because browsers
cannot set custom WebSocket headers; `wsx` still checks the origin. An ordinary handler must use the
header guard instead.
