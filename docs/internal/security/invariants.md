# Invariants that must not regress

A change that weakens any of these has to say so explicitly.

1. The network allowlist runs **before** authentication.
2. Two-factor is *enforced where it applies*, and this is the one invariant that is now a policy rather
   than a constant — see [Invariant 2](#invariant-2-what-two-factor-still-guarantees) below. An account
   with an authenticator is **always** asked for a code, and a session that owes one reaches nothing but
   the 2FA routes. What `JD_REQUIRE_2FA` decides is whether an account that has *not* enrolled may sign
   in at all. It defaults to false. **One documented exception:** with `JD_HUB_SIGNIN=true` (off by
   default), an account that opted in may be opened by a sign-in ticket from the Just Hub this server is
   linked to, without this server's own code. Its limits are in
   [the exception to invariant 2](#the-exception-hub-sign-in).
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
   - `api.handleJobCancel` for stopping a job: it takes the capability the job's route took
     (`jobs.Spec.Capability`, kept on a detached job's record across a restart), and `system.admin` for a
     job naming none;
   - the generic job handlers for `network.diagnostic.*` jobs: lists filter them and get/stream/cancel
     require `system.admin`, matching the saved-artifact routes;
   - `api.boundaryGate` for a fail2ban ban, a CrowdSec decision and an SSH settings change: one that
     cuts this session's own way in is refused, and one that touches the dashboard's access boundary
     for anybody else (an allowlisted network, the tailnet's previews, the SSH tunnel) needs an
     explicit `acknowledgeBoundary` (`netsec.BoundaryImpacts`,
     [observability-security](../backend/observability-security.md#the-access-boundary));
   - `api.spendDestructive` for a body under `/system-users` that is destructive though its route is not:
     disabling with `endSessions`, a sudo level of `none`, and bulk `end-sessions`;
   - `api.guardSystemAccount` for every write under `/system-users/{name}`: an account a package made
     (below uid 1000, and `nobody`, but not root) is refused with `403 system_account` unless the request
     says `allowSystem=true`, and `handleTerminalCreate`, where `asUser` asks for `system.admin` on a route
     that otherwise asks only for `terminal` and refuses an API key, because root is one of the accounts it
     can name;
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
   back. The grant reaches every deployment or, like a deploy key, only those chosen for it
   (`api_token_deployments`, read with the key on every request). **With the generated tools on, it
   deliberately reaches more than those two verbs:** every deployment route a deploy key may call (`keyOpen`, `keyEvery` in
   `deployKeyRoutes`) is also a tool generated from the API's description, so `read` reads what a
   deploy key reads and `deploy` changes what a deploy key changes — configuration, source, Git
   policy, triggers, variables, drafts — held to the key's owner's role (not the administrator role a
   deploy key carries), with the routes behind `s.destructive` only for a role holding `destructive`.
   **The generated tools are each key's to turn on** (`api_tokens.mcp_operations`, additive, `0` for
   every key made before them): a key without them is offered exactly the hand-written tools under its
   grant and is refused a generated one by name, so no key gains a tool with an upgrade; a new key made
   from the dialog has them on unless its maker unticks them, and the flag is audited with the grant.
   A generated tool's call
   never leaves the process: the MCP handler makes it through the dashboard's router as a request whose
   context carries the MCP key's principal (`httpx.WithMCPDelegate`); `AuthenticateFeatures` admits it
   only through the deploy key gate — the table's line, the key's chosen deployments, the
   environment's — and marks it `Principal.ViaMCP`, which `DeployKey` reads as a deploy key, so every
   handler's deploy key rule (write-only variables, `keyReferenceGuard`, `keySourceGuard`,
   `hostAuthority`) holds; every other group refuses a delegated request, and no request from the
   network can carry a context value. A walk of
   every route with an MCP key (`TestAnMCPKeyOpensNoOtherRoute`) holds the first half; the grants
   are re-checked by the handler rather than trusted from the key, and
   `TestEveryDeployKeyRouteIsAnMCPTool` holds that no session-only or deploy-key-refused route is a tool.

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
   data next to a pull request's code), a route's maintenance
   and error pages (`PUT …/pages/{page}`, files on the server beside the proxy's route), and a domain's
   DNS record (`GET` and `POST …/domains/{domain}/dns-record`: read and written in a zone outside this
   server with the DNS credentials saved under Certificates, which the key never sees). The
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
   privileged container, host network, capability, device, GPU, host path, host-shell release task,
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
   those are pipelines an admin stored for their own project, not anything supplied per request — and, since
   0.8.5, to the assistant's `run_command` (`term.Manager.RunCommand`), which is the terminal's own way of running
   a line handed a line to run: the terminal's account and login shell, under the terminal capability checked on
   every call, with a built environment (never the dashboard's own, which holds its secrets), nothing on standard
   input, a deadline that stops its process group, its output masked, each line in the press's audit entry, and,
   unless the person chose full access, an Allow for every line that is not a plain read
   ([the assistant's modes](../deployments/operator.md#the-modes)). It is a shell on purpose, as the terminal is;
   the argv rule is about the dashboard's own features never building a shell string out of what a request
   carries. The assistant's terminal (`terminal_run`, `terminal_send`) is not a third: it types into a real window
   of the terminal's own, as the person would at the Terminal page, under the same capability, with each line in
   the audit entry. A `run_command` line that changes packages, restarts Docker or runs a `sudo` that may ask is
   typed into that window instead (`commandForTerminal`), so that an upgrade restarting Docker does not stop it
   halfway; its kind, and the Allow that kind needs, are still the line's own. A password a command asks for is never the model's to type: the person types it into the
   window from the chat (`POST /assistant/chats/{chat}/terminal`), and it reaches no provider, saved line or audit
   entry. Do not add a third request-defined shell, and do not "fix" either into an argv. A git clone's parent and an
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
   [preview isolation](../deployments/preview-isolation.md#tailnet-only-addresses). An environment whose
   plan is tailnet-only is published the same way, from the same range and table. Every such mapping,
   a preview's included, goes to the address's front instead of the container — its tailnet gate, when
   the plan names who may open it — a route bound to `127.0.0.1` alone, on host nginx, or on the
   dashboard's own Caddy on the host's network, which publishes nothing and binds every site to
   loopback, so this exception gains no listener on a routable address
   ([tailnet-only deployments](../deployments/tailnet-apps.md)). The front is also what keeps the
   dashboard's session cookie from the application: the address is a port of the name the dashboard is
   reached by, and a browser sends a cookie to every port of its name, so the front, and a deployment
   route serving the dashboard's own name, take the dashboard's cookies out of the request
   ([the dashboard's session cookie](../deployments/tailnet-apps.md#the-dashboards-session-cookie)).
   The second exception is Remote Desktop's stream bridge: on a server on a tailnet it publishes a
   block of 20 UDP ports from 40000 on the **tailnet address only**, for WebRTC
   ([Stream mode](../backend/remote-desktop.md#stream-mode)). Nothing is sent on them without a
   stream negotiated over the bridge's WebSocket, which is behind the dashboard's session, and no
   block is published on a server off a tailnet.
   A game server's port is published on loopback like every other deployment publication; publishing
   it on every interface, so players can join, is the operator's explicit choice of bind address under
   the deployment's Runtime settings, warned about in preflight (`game_loopback_only`, `public_bind`)
   and held to the firewall review, and admitting it through the firewall is the Firewall page's own
   audited rule route ([game servers](../deployments/game-servers.md#ports-and-binds)).
   A database's port published from its Network page — on one of the host's own addresses, on every
   interface kept to a list, or to anywhere — is not the dashboard's socket either: it is the operator's
   explicit choice, in the destructive group and audited as `database.access.change`. A list is kept by
   the network module's gateway table, whose prerouting drop runs before Docker's translation, never by
   a host firewall rule that a published port's traffic does not meet
   ([databases](../backend/databases-proxy-platform.md)).
   Scale to zero's wake ([sleep when idle](../deployments/scale-to-zero.md#the-wake-socket)) binds no
   address at all: the proxy asks over a Unix socket beside host nginx's sites or inside the Docker Caddy
   ingress's own `/config` volume, and the dashboard answers only a peer whose process is nginx or Caddy
   (`SO_PEERCRED`). What it does — start an application asleep by design — is what any visitor's request
   to the domain does.
   A Cloudflare Tunnel's connector binds nothing at all: cloudflared runs as the managed container
   `just-dashboard-cloudflared` (never a process on the host) with no published port, dials out to
   Cloudflare's edge, and reaches the proxy over a Docker network only it and the ingress share; its
   metrics answer on that network alone. Its token is sealed with `auth.Sealer` as it arrives, never
   returned or logged, and handed to the container through the `docker` client's environment, never its
   argv ([Cloudflare Tunnel](../deployments/cloudflare-tunnel.md)).
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
   source, name an uncontained path, or take privilege without a written reason. An install-time file
   is written only inside a volume the definition declares, with generated secrets as references the
   helper fills, and a download is deployable only when a checksum pins it: both are written by a
   short-lived helper container that mounts that one volume (the release's own image for a file, a
   digest-pinned fetch image for a download), never by a host shell or into a host path, and fetched
   bytes are hashed by the dashboard and refused on a mismatch before anything starts. A multi-service
   (Compose) template publishes exactly one port — its public service's, on loopback — keeps every other
   service on private networks the ingress never joins (`internal: true`, with no route out unless the
   service declares egress), takes no privilege, device, host
   network or Docker socket, and carries values only as typed references (`{{secret.…}}`,
   `{{variable.…}}`) that render as interpolations of the deployment's sealed variables, so its document
   holds no value; commands and healthchecks are argv that are never templated, so neither an
   operator's input nor a secret reaches a process list or a shell. The stored document is the server's
   own render of a shipped version or of a template added on this dashboard, never one a client sent
   under a template's name. A template added on this dashboard is held to every one of these rules when
   it is saved, and to more: its id is in the `user-` namespace no built-in may use, so it can never
   stand in for a reviewed definition; it may take no privilege, host network, capability, device or
   Docker socket, since only a reviewed built-in may reach the host; and a source that names it is
   saved with the server's stored copy frozen in, never a definition the client sent. A template made
   from a deployment never opens a secret's value: each becomes an input the next deployment answers.
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
13. The dashboard's assistant is a person's, at the dashboard, and acts as them: no more than their role, and,
    since 0.8.5, no less. Its routes are a session's (a deployment's, a new deployment's, a database connection's,
    `POST /databases/{id}/assistant`, and the server's, `POST /assistant/chat`). Every call it makes is a request through the dashboard's own router under the
    person's own session principal (`s.assistantDispatch`, `httpx.WithAssistant`), so the route's capability
    check, handler rules, rate limit and audit entry apply exactly as to them; the entry records the person with
    actor `assistant`, so what it did reads apart from what they did. The session is read again before every
    step and every call (`Authenticator.Recheck`). **It is no longer narrowed to deploy-key rules**: there is no
    synthetic key, no `assistantRoutes` list and no handler guard that refuses it (`assistantDraftGuard`,
    `refuseAssistantSchedule` are gone). It reaches every route of `/api/v1` the person could call — databases,
    backups, Docker, files, the firewall, users and the rest, including what runs commands on the host
    (scheduled tasks, services, a container's command) — through `api_request`, `find_routes`, a deployment's
    `call_operation` and its shaped reads, and runs a shell line on the host as the terminal would
    (`run_command`, invariant 6), which takes the terminal capability. What stays out of its reach is a closed list in
    `api/handlers_assistant_routes.go` (`agentExclusion`), each line something no role changes: its own routes
    and those that ask the model; sign-in, password, second factor, passkeys, sessions, letting Just Hub sign the
    account in and minting an API key;
    uninstalling the dashboard, its account link, its hub link and its usage count (invariants 12, 16 and 17 want
    the person's own press); WebSockets and streams; webhooks, the MCP endpoint and other machines' routes. A database connection
    the operator protected is out of every chat's reach as well (`agentProtected`): its own chat is refused by the
    guard, and a call into its routes is answered with why, so nothing in it is sent to a model. What used to be
    refused is asked instead: how much runs without asking is the mode, which the server holds on every press —
    "ask" holds every read and change, "changes" (the default) holds every change, and **full access runs
    everything the person's role allows, unseen**; a draft's own saves never wait, but committing it, discarding
    it, or a save that changes its source, points a variable at a saved secret or reaches the host waits unless
    the mode is full. Text the model reads can try to talk it into a change, which is why "changes" stays the
    default and who answers takes a session. **What is kept from the provider is every secret's value**: every
    string of every answer is masked before it is sent, in every mode and scope (every deployment's secret
    values, credential shapes, fields named for a secret, key material; `api/handlers_assistant_mask.go`), a
    shell line's output included, and an answer that cannot be masked whole is not sent. What the person sends
    is theirs and goes as it is: a question's words, what they attach from the SQL editor, and the images pasted
    with it, which masking cannot read (the box says so while one waits). A malformed call never refuses the answer: it is answered
    with what was wrong (`assistant.CheckCalls`). Its saved chats, and the images pasted into them, are their
    owner's alone and sealed with the master key. See [the deployment assistant](../deployments/operator.md).

14. A vulnerability scanner reads an image; it never reaches the host. `imagescan.ScanSpec` runs Trivy
    with **no network** (`NetworkMode: none`), no capabilities, a read-only root, no new privileges and
    as nobody (65534), and mounts only the image's `docker save` archive and the database volume
    read-only plus its own report and scratch folders — never the Docker socket, a host path of the
    operator's or a credential. What needs a network is done by the dashboard itself, not the scanner:
    the database is fetched over https from its registry, checked against the digest its manifest
    names, repacked so that only `db/trivy.db` and `db/metadata.json` reach the volume, and copied in
    through a container that is created and never started; a base image is pulled by the daemon, by
    digest, as a build pulls it. The scanner image is pinned by tag and index digest. Scanning changes
    nothing a release runs: a rebuild is offered as the runs route's own `force_build`, gated and
    audited as any build is. See [image vulnerabilities](../deployments/image-vulnerabilities.md).

15. Railpack's planner reads a checkout; it never reaches the host. `deploy.railpackPrepareSpec` runs
    the pinned Railpack image (tag and index digest) with the build root mounted **read-only**, a fresh
    output folder of its own, no capabilities, no new privileges, a read-only root, bounded memory, CPU
    and processes, and never the Docker socket, a host path of the operator's or a credential. It has
    Docker's bridge network, as a build has, because Railpack downloads its pinned mise release and
    resolves language releases; it runs as root inside the container only because the checkout is a
    root-only 0700 tree, and with every capability dropped. Build variables reach it by name only,
    except plain `RAILPACK_*` ones, which configure the plan; their values reach the build as BuildKit
    secret mounts, as a recipe's do. A Railpack plan runs only in BuildKit, like a Dockerfile's steps.
    See [Railpack](../deployments/recipes.md#railpack).
16. The usage count reports nothing without an administrator's yes, given in a session and audited, and an API
    key cannot give it. The report is the three fields of `usageping.Report` with no identifier, at most once per
    UTC day, and a redirect is never followed. `JD_USAGE_PING=false` or `JD_ACCOUNT_LINK=false` stops it, and an
    agent has none. See [the usage count](../backend/usage-ping.md).
17. Connecting to a Just Hub adds no inbound path and no second authority, as invariant 12's link does not,
    except hub sign-in: with `JD_HUB_SIGNIN=true` (off by default) the hub signs single-use tickets that this
    server redeems at `POST /api/v1/auth/hub/enter` for accounts that opted in, within the limits of
    [the exception to invariant 2](#the-exception-hub-sign-in). Otherwise it is
    outbound only: every exchange is a request from this server to one machine on its own tailnet, dialled only
    inside `100.64.0.0/10`, over TLS verified for the hub's MagicDNS name, never following a redirect; the hub's
    one call the other way is the discovery probe `GET /api/v1/hub/hello`, behind the allowlist, which names
    nothing but whether this server can be connected. A command from the hub runs only if its signature verifies
    against the hub key pinned at approval, which no heartbeat reply can add to, end or replace
    (`IgnoreAnnouncedKeys`: a new hub key needs a new link), it has not expired or been seen before, and its kind
    is one of the closed set of protocol 7.1 within the scope chosen and enforced on this server; each is
    audited as `hub:<hub name>` whatever its outcome, and draws on its own destructive budget. **The hub's
    machine is pinned as well**: the `StableNodeID` WhoIs gives for its address at connect, checked before
    anything is sent (at each connector start, whenever the dialled address changes, at least every five
    minutes, and before every poll of a pending request); another machine at that name is sent nothing and
    the link goes idle, as does a stored address the tailnet rule refuses at start, and an idle link stays idle
    in the store until a person disconnects. Only that check, against the pinned node, lets a request through:
    the URL check before Connect compares the name alone and changes nothing, before anything is pinned the
    machine WhoIs names must bear the hub's name, and a check that cannot run for want of an address sends
    nothing. Connecting, cancelling, changing the scope and disconnecting are session-only in the browser (an
    API key is refused) and root-only at the terminal, where the hub's one-time
    token is read only from standard input. Everything the hub sends for display (notices, fleet names, its
    name, its error messages) is shown as plain text attributed to the hub, never as this dashboard's own
    prompt or where the dashboard speaks for itself (the tab's title and the rail's wordmark name the server by
    its own host name, not the hub's name for it), and an address it hands over (another server's endpoint, a
    notice's link) opens only in a new tab with `noopener noreferrer`, never in this dashboard's own. While the
    link is idle nothing links to the stored hub address at all, since it may answer on the machine that was
    refused. See [hub link](../backend/hub-link.md) and its [protocol](../backend/hub-link-protocol.md).

## Invariant 2: what two-factor still guarantees

Two-factor used to be unconditional, and the reasoning was sound for the install the product was first
written for. It was wrong for the one it is actually installed into: this dashboard is reachable only
over a tailnet or an ssh tunnel, both of which authenticate the network before a packet reaches the login
page, and making an authenticator app compulsory turned the first ninety seconds of a single-operator
install into a chore that could not be skipped.

What did **not** change, and must not:

- An account with `totp_enabled` is asked for a code at every sign-in on this server, whatever
  `JD_REQUIRE_2FA` says, the one documented exception being a hub sign-in ticket
  ([below](#the-exception-hub-sign-in)). `Service.Login` decides on the account, not on the policy.
- A session that owes a second factor is never elevated. `httpx.Authenticator.resolve` refuses it
  everywhere but the 2FA routes, exactly as before.
- Where `JD_REQUIRE_2FA` is true, an account with no authenticator gets a password-only session that
  reaches nothing but enrolment — the old behaviour, unchanged.
- Turning an authenticator off is the account holder's own action, requires their password, and is
  refused outright by `Service.DisableTOTP` where the policy demands one. Regenerating recovery codes
  takes the same password, since a recovery code passes for the authenticator.
- Setup, enrollment confirmation and code verification share one account-scoped attempt budget, and so
  does every form that asks a session for its password again. Five wrong codes for an account lock its
  second factor for fifteen minutes (`totp_failed_count`, `totp_locked_until`), whichever route they
  came through and whichever kind of code they were; while it is locked a right code is refused too. Only
  an accepted code ends the count: a password or passkey sign-in does not. An administrator's reset,
  turning the authenticator off and a password reset at the host clear it.
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

Two deliberate loosenings. The first: with the policy off, an account that has never enrolled signs in
on a password alone. `auth.Service.Login` elevates that session at creation, and `ResolveSession`
completes a session that was left half-authenticated when the policy changed under it — without that,
turning the setting off would strand everyone who was mid-flow with a session that can never be
elevated. The second is hub sign-in, [below](#the-exception-hub-sign-in), within its own limits.

### The exception: hub sign-in

Hub sign-in ([hub-link-protocol.md section 9](../backend/hub-link-protocol.md#9-hub-sign-in-version-1-off-by-default),
implemented as [hub-link.md "Hub sign-in"](../backend/hub-link.md#hub-sign-in)) lets a person open this
server from the Just Hub it is linked to and land on it signed in. A ticket the hub signs starts a
**complete** session (`twofa_passed = 1`, `auth_method = 'hub'`) for an account with an authenticator
without asking it for this server's code. That is a weakening of this invariant, made on purpose, and it
is allowed only while all six of these hold:

1. **The operator turned it on.** `JD_HUB_SIGNIN=true`, set by root in `.env`. It defaults to `false`,
   counts as off while `JD_HUB_LINK=false`, and with `JD_HUB_LINK_TEST_ORIGIN` set the backend refuses to
   start unless it was built with `-tags hublinktest`, which release builds never are (`config.go`).
2. **The account holder opted in**, from a complete session on this server that a ticket did not make,
   with the password and, where the account has an authenticator, a current code
   (`PUT /api/v1/account/hub-signin`, checked by `auth.Service.VerifyReauthentication` on the account's
   shared guess budget, the code's step used up). Each account decides for itself.
3. **One hub user.** The grant (`hub_signin_grants`) names one hub user, and only that hub user's tickets
   open the account. A hub user name names one person for the hub's whole life.
4. **The hub's own second factor is fresh.** The ticket's `mfaAt`, under the pinned hub key, is the hub
   user's last TOTP code in the hub session that asked, at most 600 seconds before the ticket was made. A
   recovery code on the hub does not count.
5. **Nothing on this server says no.** The account is not disabled, not locked (`locked_until`,
   `totp_locked_until`), does not owe a password change, and is not unenrolled while
   `JD_REQUIRE_2FA=true` (`auth.Service.StartHubSessionTx`).
6. **The device that asked uses it.** The ticket names the hub session's tailnet login and the
   `StableNodeID` of the device that asked; WhoIs of the browser's address must name both, unless the
   browser is on loopback (an SSH tunnel) or tailscaled cannot be asked (`tailnetgate.ErrUnavailable`: no
   answer, or an error status), which the audit entry records. A tailscaled that answered but named no
   node or no login (`ErrNoIdentity`) was asked, and the device is refused.

The ticket itself is checked in the order of protocol 9.7 (`hublink.checkTicket`, `checkDevice`,
`Service.redeem`): the shape, a pinned hub key not past its end, the Ed25519 signature over the exact
payload segment before any JSON is read, the payload's JSON rules (scanned token by token, so a key in
another case, a repeated key or a `null` is refused), this hub and this `serverId`, its time with 30
seconds of grace (accepted for at most 120 seconds), the hub's second factor, the device, and then its
nonce, spent in a statement that commits on its own (`hub_signin_tickets`), so it opens one session at
most. The grant, the account and the new session are one write transaction.

**What a hub session cannot do.** It lasts at most two hours (one hour idle), and
`httpx.RefuseHubSession` refuses it with `403 hub_session_not_allowed` on every route that creates,
changes, rotates, imports or exports a credential, or creates or changes a link, because what it made
would outlive the session and the grant. Exporting includes a read: a `GET` that shows a stored secret
is refused as an export is. `TestEveryCredentialAndLinkRouteRefusesAHubSession`
(`api/hub_session_routes_test.go`) walks the router and fails unless exactly these carry it, and
`TestTheDocumentedRouteTablesAreHubSessionRefused` reads this table and the protocol's and fails when
either names a route the router does not refuse, or leaves one out:

| What | Routes, under `/api/v1` |
| --- | --- |
| API, MCP and deploy keys | `POST /tokens/`, `POST /tokens/mcp`, `POST /tokens/deploy`, `PUT /tokens/{id}/connections` |
| Dashboard accounts | `POST /dashboard-users/`, `PATCH /dashboard-users/{id}`, `POST /dashboard-users/{id}/reset-totp`, `DELETE /dashboard-users/{id}` |
| The caller's own credentials | `POST /account/password`, `POST /account/recovery-codes`, `POST /account/2fa/disable`, `POST /auth/2fa/setup`, `POST /auth/2fa/enable`, `POST /auth/2fa/verify`, `POST /account/passkeys/register/begin`, `POST /account/passkeys/register/finish`, `PATCH /account/passkeys/{id}`, `DELETE /account/passkeys/{id}` |
| Linux accounts and their keys | `POST /system-users/`, `PATCH /system-users/{name}/`, `PUT /system-users/{name}/sudo`, `POST /system-users/{name}/password`, `POST /system-users/{name}/keys/generate`, `POST /system-users/{name}/keys`, `PATCH /system-users/{name}/keys`, `POST /system-users/{name}/keys/import`, `POST /system-users/{name}/keys/copy`, `POST /system-accounts/bulk`, `PATCH /system-accounts/groups/{group}` |
| Database users and roles | `POST /databases/{id}/server/roles`, `PUT /databases/{id}/server/roles/{name}`, `POST /databases/{id}/server/roles/{name}/grant`, `POST /databases/{id}/server/roles/{name}/privileges`, `POST /databases/{id}/mongo/users`, `PUT /databases/{id}/mongo/users`, `POST /databases/{id}/mongo/users/grant`, `PUT /databases/{id}/redis/acl/{name}`, `PUT /databases/{id}/redis/config`, `POST /databases/host/grant` |
| Database passwords and data copies | `GET /databases/{id}/url`, `POST /deploy/{id}/environments/{env}/releases/{release}/data-copies`, `GET /deploy/{id}/environments/{env}/data-copies/{copy}/url` |
| Proxy basic-auth users | `POST /proxy/auth-files/`, `POST /proxy/import/npm`, `POST /proxy/import/npm/apply` |
| Deployment secrets and saved credentials | `POST /deploy/`, `POST /deploy/{id}/environments/{env}/triggers`, `PUT /deploy/{id}/environments/{env}/triggers/{trigger}`, `POST /deploy/{id}/environments/{env}/triggers/{trigger}/rotate-secret`, `POST /deploy/{id}/rotate-secret`, `POST /deploy/credentials`, `PUT /deploy/credentials/{id}`, `POST /deploy/secret-stores`, `PUT /deploy/secret-stores/{store}`, `POST /deploy/notifications`, `PUT /deploy/notifications/{channel}`, `POST /deploy/log-drains`, `PUT /deploy/log-drains/{drain}` |
| Deployment secret variables | `GET /deploy/{id}/env/reveal`, `GET /deploy/{id}/environments/{env}/variables/{name}/reveal`, `POST /deploy/{id}/environments/{env}/variables/{name}/generate`, `POST /deploy/{id}/environments/{env}/variables/{name}/rotate`, `GET /deploy/shared-variables/{name}/reveal`, `GET /deploy/{id}/shared-variables/{name}/reveal` |
| Git and GitHub | `POST /git/github/auth/device`, `POST /git/github/auth/device/{id}`, `POST /git/github/auth/token`, `POST /git/github/auth/configure`, `POST /deploy/github-app/manifest`, `POST /deploy/github-app/callback`, `POST /git/forge/account` |
| DNS credentials, tunnels and TLS keys | `POST /certificates/dns-credentials`, `POST /certificates/import`, `POST /certificates/export`, `POST /network/dns/services/`, `PUT /network/dns/services/{id}`, `PUT /proxy/tunnel/`, `POST /proxy/tunnel/create` |
| Network peers and check machines | `POST /network/vpn/wireguard/{iface}/peers`, `PATCH /network/vpn/wireguard/{iface}/peers/{id}`, `GET /network/vpn/wireguard/{iface}/peers/{id}/config`, `POST /network/vpn/archive/{file}/restore`, `POST /network/external/enrollments` |
| Remote Desktop | `POST /remote-desktop/computers`, `PUT /remote-desktop/computers/{id}`, `POST /remote-desktop/computers/{id}/stream/pair` |
| Backups | `POST /backups/key/import`, `POST /backups/key/export`, `POST /backups/`, `PUT /backups/{id}`, `POST /backups/import` |
| The assistant's provider keys | `PUT /assistant/providers/{preset}` |
| The account link | `POST /dashboard/link/`, `DELETE /dashboard/link/request`, `PUT /dashboard/link/scope` |
| The hub link | `POST /dashboard/hub`, `DELETE /dashboard/hub/request`, `PUT /dashboard/hub/scope` |
| Hub sign-in | `PUT /account/hub-signin` |

This is the protocol's table (section 9.9), which its first version did not hold whole: the walk of every
write route found seventeen more that its rule covers, and the protocol lists them since (errata HL-3,
route walk): `POST /git/forge/account` (a saved Git host token), `POST /databases/host/grant` and
`POST /databases/{id}/server/roles/{name}/privileges` (a database login and its rights),
`POST /system-accounts/bulk` and `PATCH /system-accounts/groups/{group}` (Linux accounts, and group
membership such as `sudo` or `docker`), the two WireGuard peer routes and the restore of an archived tunnel
(a peer's key lets a device onto this server's network), `POST /network/external/enrollments` (the token a
machine that runs external checks enrols with, after which it keeps calling this server's probe-agent
routes), `PUT /proxy/tunnel/` and `POST /proxy/tunnel/create` (Cloudflare tunnel tokens), the two DNS
service routes (a DNS server's saved API credential), the backup job routes (a destination holds
credentials of its own) and `PUT /assistant/providers/{preset}` (a provider's API key). The two `DELETE`
rows the table lists are refused as well, although the rule leaves deletions open, because the table names
them. A second walk, of every route that shows a stored secret and of every write route again (errata
HL-3, second walk), found seventeen more: `GET /network/vpn/wireguard/{iface}/peers/{id}/config` (a peer's
private key: the key that adding the peer is refused for), `GET /databases/{id}/url` (the connection
string, with its password), `PUT /databases/{id}/redis/config` (`CONFIG SET` reaches `requirepass`,
`masterauth` and the ACL file, which the refused ACL route exists to keep), the production data copy's
two routes (making one makes a database login with a new password; its address is that password), the
six routes that reveal, generate or rotate a deployment's secret variable, `POST /deploy/` (a new
project's webhook secret, which starts its commands from anywhere), NPM's import and its preview (an NPM
database's access-list users and passwords, written as basic-auth password files), and Remote Desktop's
computer routes and stream pairing (a remote machine's saved sign-in, and a client certificate that
machine trusts). `TestEveryRefusedRouteAnswersAHubSessionWith403` sends a hub session to each of them. A
route added later that the rule covers joins the list, the test, the protocol's table and this table in
the same change. Every second-factor route is refused, `POST /auth/2fa/verify` with the rest, which the
end-to-end run of hub sign-in found still open: a hub session is made complete and never owes a code, and a
code given from one would turn it into an ordinary session that none of these limits reach. The
middleware refuses it before the handler spends a guess from the account's budget, so a right code from a
hub session is neither counted nor audited as a wrong one
(`TestAHubSessionIsRefusedTheCodeRouteBeforeAGuessIsSpent`); `auth.elevateTx` still leaves a hub session
as it is, should another caller ever reach it.

What stays open to a hub session, because it only takes power away: disconnecting either link,
`POST /dashboard/hub/sync`, removing a sign-in grant, revoking sessions, deleting a key, user, credential
or channel, and signing GitHub out.

**When it ends.** A hub session lives only while what made it still holds. Every one of these is a write
transaction that also revokes the hub sessions it ends, so a redeem either commits first and is revoked,
or finds nothing to stand on:

- the account's grant ends: the account or an administrator removes it, the account replaces it, its
  password changes or is reset, its authenticator is enrolled, replaced, reset or turned off, it is
  disabled or deleted, or all its sessions are revoked (`auth.endHubGrantTx`, inside each of those
  changes' own transaction);
- the hub's heartbeat reply no longer offers its hub user, or offers no sign-in at all (`hublink.onReply`,
  in the transaction that keeps the reply, and only for the link the reply came from);
- the link ends (disconnect, removal in the hub, a new link): every grant, every ticket record and every
  hub session go with it (`hublink.onClear`, in the engine's clearing transaction);
- the link goes idle, because its address fails the URL rule at start or another machine answers at the
  hub's name: every hub session is revoked in the write transaction that stores the idle flag, and the
  grants stay unusable (`hublink.goIdle`, `revokeHubSessionsTx`). The flag is kept in the stored link,
  so every later start finds the link idle without deciding again, and refuses hub sessions, redeems and
  new grants until a person disconnects, even once the address passes the rule again;
- `JD_HUB_SIGNIN` or `JD_HUB_LINK` is found off at start: every grant, ticket record and hub session is
  deleted before the API serves its first request, and if that cannot be written the backend does not
  start (`hublink.PrepareSignIn` from `api.Server.Start`).

And every time a hub session is resolved, `auth.Service.ResolveSession` asks `hublink.checkHubSession`
again: both switches on, the link stored and not idle, and a grant for this account and this link whose
hub user is the session's and that is not newer than the session. A revocation that was missed cannot
keep one working; without the check installed, no hub session resolves at all.

A socket is authenticated once, at the upgrade, and nothing in the request path looks at it again, so
the sockets a hub session opens are tied to it: every socket `wsx.Upgrader` opens goes through the API's
`wsx.Guard` (`api/hub_session_sockets.go`), as do the Remote Desktop stream bridge's proxied sockets
(`guardHubStream`). One a hub session opened (a terminal, a container's shell, a log or event tail, a
Remote Desktop session or stream) closes at the session's expiry, at once when a revocation of hub
sessions commits (hublink's `SessionsEnded`, after each event above), and otherwise at its next check,
every 15 seconds, through `auth.Service.CheckSession`, which runs every check `ResolveSession` does
without counting as use. `TestAHubSessionsSocketsEndWithIt` holds every event above to it.

**What it costs, plainly.** With hub sign-in on, whoever takes over the hub can open a session as every
account that opted in, from a device on the tailnet, and do what that account can. The route list above
stops the dashboard's own forms from minting, changing or revealing lasting credentials; it does not
contain the general tools. The Files page reads every file under `JD_FILE_ROOTS` (`/` by default) as
root, for every role, so any hub session can read every credential on disk (`/etc/shadow`, SSH private
keys, WireGuard configurations, a project's `.env`, this dashboard's own `.env` with `JD_MASTER_KEY` and
its database). With `file.write` (the `admin` and `limited` roles) it writes any file as root
(`files.WriteChecked` has no deny-list), so it can plant an SSH key in `authorized_keys` or a sudoers
rule; and an administrator's session has the terminal. For those roles a hub session is as good as root,
and for every role it reads what root reads. Ending hub sign-in ends the hub's sessions; it does not undo
what was done in them. Without it, a taken-over hub is limited to its scope's commands. That is why it is
off by default and needs the operator, the hub's owner and each account holder, and why the opt-in
screen says so.

## Invariant 3: which routes take a typed phrase

Typed confirmation is reserved for seven deletion operations:

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
- Deleting a host account along with its home directory and no archive (`DELETE /system-users/{name}`
  with `home=delete`) requires the account's name, because what a person keeps in a home exists nowhere
  else. Its preview (`GET /system-users/{name}/removal`) counts it first. Keeping the home, or archiving
  it to a root-only tarball first, takes ordinary confirmation, and bulk deletion only ever keeps homes.

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

The server enforces the seven phrases inside their handlers. The browser sends an
`encodeURIComponent`-encoded value in `X-Confirm` with `X-Confirm-Encoding: uri`; the backend decodes
it once, requires valid UTF-8, and compares the exact phrase. Legacy unencoded headers remain
supported. Only the Compose WebSocket action accepts a phrase in a query parameter because browsers
cannot set custom WebSocket headers; `wsx` still checks the origin. An ordinary handler must use the
header guard instead.
