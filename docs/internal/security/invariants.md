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
     connection's own engine (`dbx.ParseScript`): the query, script and analysed-plan routes;
   - the handler of `POST …/changes` for a change set, by the operations it holds: one that deletes
     rows needs the destructive capability;
   - `api.runDDL` for a schema form that changes a column's type or whose own SQL (a CHECK condition,
     an index predicate, a USING conversion, a function default) calls anything `dbx` does not vouch
     for — either needs the destructive capability on a route that otherwise asks for
     `service.control`, for a preview as much as for a run;
   - `dbx.MaintenanceAction.NeedsDestructive` for a maintenance action that locks a table against the
     application or can lose rows, and the body of the power route, where `stop` and `restart` are
     what make the request destructive;
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

   A database connection marked read-only adds one rule in front of all of these, and it fails closed
   the other way round: `protectReadOnlyConnections` refuses every request under `/databases/{id}` that
   is not a `GET` unless its route is on an allowlist, so a mutating route added later is refused until
   somebody decides it belongs there. The routes on the list that are a read or a write by what they
   carry are judged by the same classifiers as above. It guards the dashboard's own controls; it is
   not a sandbox around the server. Each check and the rule are stated in
   [request lifecycle](../architecture/request-lifecycle.md#checks-that-depend-on-what-a-request-carries).
5. Every state-changing request lands in the audit log.
6. Client-supplied paths go through `files.Resolve` — including the ones that do not look like file
   operations (bind-mount source, build context, a new stack's directory). Host commands go through
   `hostexec` with an argv, never a shell string. Request-defined shell source is confined to `deploy.Deployer.shell`, deliberately:
   those are pipelines an admin stored for their own project, not anything supplied per request. Do not add
   a second request-defined shell, and do not "fix" that one into an argv. A git clone's parent and an
   `init` target go through `gitx.ResolveDir` against `JD_GIT_ROOTS` — the Git page's own boundary — and a
   clone can only create a directory that does not exist. Terminal startup also uses
   a bundled constant bootstrap to load the native prompt; paths remain separate positional arguments. `dockerx` invokes the `docker` binary in three places
   (compose, the streaming runner, `Build`) because the Engine API has no equivalent; all three build argv
   explicitly. A database dump that is a SQL script is replayed by `dbx` over the dashboard's own
   connection (`dump_script.go`) and never piped to `psql` or `mysql`: each of those runs a shell for a
   line of what it is fed (`\!`, `system`), which would make an uploaded dump that second shell.
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
   source, name an uncontained path, or take privilege without a written reason.
10. The game console is not a shell. `gameserver.ValidateCommand` refuses anything carrying a shell
    metacharacter, newline or NUL, the command reaches Docker exec as separate argv elements, and player
    actions come from a closed set with the account name validated before interpolation. The same
    validator gates the console route and the scheduler, so a schedule cannot send what the console
    refuses.
11. Diagnosis claims only what an owner returned. An unavailable module is recorded as a *silence* with a
    reason — never as a healthy result, and never as a problem. A finding that cannot name an action or a
    deep link says which external action is required instead.

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
- A temporary password does not grant feature access. After required factors, only authentication
  status/logout and the session-only password change are available until `must_change_pw` is
  cleared. API tokens cannot bypass this restriction.

The one deliberate loosening: with the policy off, an account that has never enrolled signs in on a
password alone. `auth.Service.Login` elevates that session at creation, and `ResolveSession` completes a
session that was left half-authenticated when the policy changed under it — without that, turning the
setting off would strand everyone who was mid-flow with a session that can never be elevated.

## Invariant 3: which routes take a typed phrase

Typed confirmation is reserved for five deletion operations:

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

These are the only places the UI asks the operator to type a phrase. Other destructive operations,
including table and collection deletion, data restores, Docker volume removal and pruning, account and
board deletion, Git discard/reset, firewall and SSH changes, package changes, certificate export and
revocation, and self-update, use an ordinary confirmation in the UI. Read-only actions and routine
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

The server enforces the five phrases inside their handlers. The browser sends an
`encodeURIComponent`-encoded value in `X-Confirm` with `X-Confirm-Encoding: uri`; the backend decodes
it once, requires valid UTF-8, and compares the exact phrase. Legacy unencoded headers remain
supported. Only the Compose WebSocket action accepts a phrase in a query parameter because browsers
cannot set custom WebSocket headers; `wsx` still checks the origin. An ordinary handler must use the
header guard instead.
