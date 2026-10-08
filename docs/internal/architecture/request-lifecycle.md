# Request lifecycle and handler architecture

## The request chain is the security contract

`backend/internal/api/routes.go` is the map of the whole API. Every `/api/v1` request passes:

```
network allowlist → rate limit → authenticate → CSRF (session mutations) → capability → handler
```

- **Allowlist before auth** (`httpx.AllowlistCIDRs`): an off-network attacker cannot reach the login
  handler at all. `httpx.RealIP` trusts `X-Forwarded-For` only from `JD_TRUSTED_PROXIES`, or the
  allowlist could be spoofed past.
- **Three limiters**, `NewLimiter(perMinute, burst)`: `loginLim` (10/5, per address, on top of the
  per-account lockout), `apiLim` (600/120, per principal), `destrLim` (30/10, destructive routes).
  TOTP setup, enrollment confirmation and verification share a separate account-keyed `loginLim`
  bucket; changing IP or switching between these routes does not reset the second-factor budget.
- **Capabilities, not roles** (`auth/roles.go`): `read`, `service.control`, `file.write`, `terminal`,
  `destructive`, `system.admin`, held by `admin`/`limited`/`readonly`. Gate with
  `httpx.RequireCapability` so adding a role later cannot silently widen an endpoint.
  `httpx.RequireSession` additionally blocks API tokens from human-only routes (password change,
  minting tokens, account management).
- **`httpx.AuditMutations`** records every state-changing request. WebSocket routes are GET and
  long-lived, so they call `s.recordAudit(...)` at open time — the event is "a terminal was opened".
- **`httpx.RequireCSRF`** requires `X-JD-CSRF: 1` on every browser-session mutation, including login
  and partial 2FA sessions. The header makes a same-site sibling origin preflight, and this application
  grants no cross-origin browser access. Bearer tokens, agent mTLS and the HMAC webhook do not use
  ambient cookies and are deliberately outside that check.
- **WebSocket origins** are checked by `wsx` against the request host, port and the scheme Caddy serves:
  HTTPS for `JD_TLS=internal` or `tailscale`, HTTP for `JD_TLS=off` or local development. An explicit
  `JD_ALLOWED_ORIGINS` entry remains the only cross-origin exception. The backend's loopback HTTP hop
  does not determine the browser-facing scheme.

After completing any required second factor, an account marked `must_change_pw` may use only
the session-only `POST /account/password` mutation and authentication status/logout routes. Feature
routes and API tokens return `password_change_required`. Changing the password requires the current
password and revokes existing sessions; the client returns to sign-in. Password change has its own
authentication middleware so it cannot bypass an outstanding second factor.

Two deliberate exceptions: `/healthz` (unauthenticated, fixed body, no version or hostname) and
`/api/v1/hooks/deploy/{hookID}` (HMAC over the raw body, still allowlisted, still audited so
enumerating hook ids is not silent).

## Handler conventions

Handlers are `httpx.Handler` — `func(w, r) error`, rendered by its own `ServeHTTP`. `s.handle(...)` at
the mount site is only the conversion.

- Return `httpx.Err/BadRequest/Internal/Wrap`; never write an error body by hand. `httpx.WriteError` is
  the single renderer and is what keeps internal error strings off the wire.
- Decode with `httpx.DecodeJSON` (4 MB cap, `application/json` required, unknown fields and trailing
  values rejected). SQL row values use `DecodeJSONNumbers` with the same constraints, preserving exact
  numeric tokens until the database adapter binds them.
- `s.destructive(r, ...)` = capability check + `destrLim` + audit. It does **not** enforce confirmation;
  the typed-phrase subset calls `httpx.RequireTypedConfirmation(w, r, phrase)` **inside** the handler,
  where the phrase is known, and it reaches the client as `error.phrase`. See [invariant 3](../security/invariants.md#invariant-3-which-routes-take-a-typed-phrase).
- `s.destructive` nests inside stricter groups too (admin holds every capability), so "which routes are
  destructive" has one answer.

### Checks that depend on what a request carries

Some routes serve a routine request and a destructive one on the same path, because the path cannot
know which it was handed. Their handlers read the request, decide, and apply the capability and the
`destrLim` budget by hand, as `s.destructive` would have. Every one fails closed: a request its reader
cannot classify is the destructive kind. The Databases routes among them are marked `by content` in
the [route table](../backend/databases-proxy-platform.md#routes).

- **SQL statements.** `POST /databases/{id}/query`, `…/script` and an analysed `…/explain` classify
  their SQL by the rules of the connection's own engine (`dbx.ParseScript`, which gives every
  statement its `Risk`) and ask for what the verdict requires (`authoriseSQL`); a script is judged by
  its worst statement, and a statement the lexer cannot read with certainty is destructive.
  `POST …/export/query` takes only a statement that classifies as a read.
- **Rows.** `POST …/changes` classifies nothing: its handler counts the operations the set holds and
  asks for `destructive`, and spends `destrLim`, when one of them is a delete. A dry run, which
  renders the statements and runs none, asks for neither.
- **Schema forms.** A `/ddl/*` route that only adds asks for `service.control`. `api.runDDL` asks for
  `destructive` on top of it when the plan changes a column's type, which rewrites the column, or
  when the operator's own SQL in it (a CHECK condition, an index predicate, a USING conversion, a
  function default) calls anything `dbx` does not vouch for — for a preview as much as for a run.
- **Maintenance.** `POST …/maintenance` is `service.control` and names its action in the body. An
  action that locks a table against the application while it runs, or can lose rows (`vacuum_full`,
  `reindex`, MySQL `optimize` and `repair`, SQLite `vacuum`, SQL Server `rebuild`), asks for
  `destructive` and spends `destrLim` in the handler, before anything is dialled: those are
  statements the SQL console refuses the same account. Which actions those are is a property of the
  closed action list (`dbx.MaintenanceAction.NeedsDestructive`), published to the page as `requires`.
- **Moving data.** `POST …/import` and `…/import/upload` require `destructive` and its budget when
  the options ask for the table's contents to be replaced, and `POST …/restore` requires
  `system.admin` when its target is a database to be created.
- **Power.** `POST …/power` starts, stops or restarts by its body, so it asks for `destructive` and
  spends `destrLim` by hand for `stop` and `restart`.
- **Accounts.** The role routes check content too: an alter is refused for an attribute the engine
  cannot change (`dbx.CheckRoleRequest`), and for locking out or demoting the account the connection
  itself signs in with.
- **Redis** and **MongoDB** take any command on one route; the two sections below say how each is
  read.
- **Network.** The gateway and protection PUTs (`/network/gateway/forwards/{id}`, `/gateway/nat/{id}`,
  `/protection/limits/{id}`, `/protection/blocklists/{id}`) are `system.admin`, and ask for
  `destructive` and spend `destrLim` by hand when the body disables the entry; `POST
  /network/protection/settings` does the same when a value weakens a kernel protection. Setting a
  device down and turning forwarding off are their own paths (`/down`, `/off`) inside `s.destructive`,
  so the two directions of one switch never share a route ([network module](../backend/network.md#routes)).
- **Container specs.** Container creation and recreation use `api.authoriseSpec`: privileged mode, added
  capabilities/devices, host/shared network namespaces and bind mounts require `system.admin`. Referenced
  network drivers and named-volume drivers/options are inspected too; a named volume cannot hide a host
  bind or plugin mount from this policy. Local filesystem volume backing paths must be absolute and pass
  the configured file-root check, including for administrators.
- **Docker network specs.** Creation requires `service.control`; a custom driver or any driver options
  additionally require `system.admin`. Manual creation refuses Compose/dashboard ownership labels and
  explicit address pools that contain the connection's observed client address before Engine I/O.
  Additive IPv4/IPv6 IPAM pools retain the legacy single-pool API; validation and bounded metadata are
  described in [Docker network creation](../backend/docker-files-logs.md#network-creation).
- **Saved network diagnostics.** Launch, read, export, compare and cancel require `system.admin`.
  The generic job list/get/stream/cancel routes apply that same gate to `network.diagnostic.*` jobs,
  so artifact access cannot bypass the feature route. Deletion and retention changes additionally
  use `s.destructive`; all mutations are audited. The connection investigator likewise requires
  `system.admin` and accepts a closed source/target tuple rather than a PID, executable or argv.
  Retained investigations use that same adapter and privilege boundary; comparison requires the
  same requested source, family, protocol, port, address, mark and measurement choice.
- **Private packet captures.** Every `/network/captures` route and generic `network.capture.*`
  job view/cancel requires `system.admin`; deletion additionally uses `s.destructive`. Closed
  typed filters produce fixed native argv without client paths or shell expressions. Mutations
  are audited, and successful private PCAP/support downloads use `httpx.AuditRead`. Original bytes
  remain sensitive; only the separate support metadata is redacted. See
  [capture lifecycle](../backend/network-captures.md).
- **Log sources.** The log routes decide on the source, not the path. `/logs/stream`, `/search`,
  `/download`, `/retention` and `/source` are `read`, but every one parses its `source` through
  `logTargetFor`, which refuses auth data — `auth.log` and `secure` with their generations and anything
  resolving to them, a file whose rotated generations include such a link, a PM2 process whose out or
  error file is one, the `ssh`/`sshd` units, a `journal-id:` naming `sshd`, `sshd-session`, `sshd-auth`,
  `sudo`, `su`, `login` or `systemd-logind` — with a 403 to anyone without `system.admin`, and
  `/logs/sources` leaves those out of its answer for them. The whole journal (`journal:`) is not among
  them: it stays `read`, as before the gate, so a reader can still find those lines, and their facets, in
  it unfiltered. Whatever the gate, a rotated generation is read only when it resolves inside
  `JD_LOG_ROOTS` itself: the live file passing says nothing about where `app.log.1` leads. The reads the
  service logs added sit at `read` beside their siblings: `GET /logs/source`, `GET
  /databases/{id}/logs/sources` and `/querylog`, `GET /proxy/sites/{name}/requests` with `/stream` and
  `/export`, `agent=` and `referer=` on a deployment's request routes, and `container=`/`stack=` on `GET
  /docker/events` and its socket (refused with a 400 before the upgrade). The per-feature log reads they
  replaced — `/docker/containers/{id}/logs` and its `/stream`, `/docker/stacks/{name}/logs/stream`,
  `/systemd/{name}/journal` and its `/stream`, and `/pm2/{name}/logs/stream` — are gone, so no second
  path reaches a log around that check.
- **Compose.** Compose creation, configuration edits, validation and execution all require `system.admin`
  until the complete resolved Compose model has a shared policy. Stack details evaluate Compose only for
  administrators; other accounts retain static YAML service names without interpolation or includes. `GET
  /docker/stacks/{name}/run?action=…` also derives destructive capability and confirmation from the
  action using the same `composeIsDestructive` set the POST routes use. That socket is the one place a
  phrase may arrive as a query parameter (`RequireTypedConfirmationWS`) — a browser cannot set a header
  on a WS handshake, and `wsx`'s origin check replaces what the header guarded.
- **Deployment reads.** Two deployment reads open to every account answer more for an administrator. A
  webhook trigger list (`GET /deploy/{id}/environments/{env}/triggers`) carries each trigger's delivery
  summary (`lastDelivery`, `recent`) only for a session holding `system.admin`, because the delivery log
  it summarises is that caller's route and a summary is not a way around it. `GET
  /deploy/hostname?hostname=` resolves the typed name and answers `resolves` only for `system.admin`:
  resolving a name the caller chose is traffic the caller directs, and the reverse proxy's own DNS check,
  which it reuses, is kept at that capability. For anyone else the lookup never runs and the field is
  absent, never `false`.

#### Redis: one route, any command

`POST /databases/{id}/redis/command` is the query route's position again: one route, any command. The
line is split into arguments by `dbx.RedisParseCommand` and classified by `dbx.RedisClassify` — a table
of the commands the dashboard knows, and the server's own `COMMAND INFO` flags for the ones it does not —
before anything runs. The route asks for `service.control`; a command that removes data, runs a script or
changes the server is `dangerous` and needs the destructive capability and `destrLim`; one that reads or
changes the server's configuration or accounts (`CONFIG`, `ACL`, `MODULE`, `DEBUG`, replication, and
anything the server itself flags `admin`) also needs `system.admin`; one that cannot be a request and a
reply (`SUBSCRIBE`, `MONITOR`, `MULTI`, a read that waits forever) or would take the server down
(`SHUTDOWN`) is refused. A command nobody listed is `dangerous` unless the server itself describes it as
a plain read, and one quoted word that spells a subcommand (`"CONFIG GET"`) is such a command, not that
subcommand. The classes are drawn so the console is never the cheaper way to do what a form gates:
`DELETE /keys` is destructive, so `DEL` is; so is a command that stores its result over another key
(`SINTERSTORE`, `ZRANGESTORE`, `SORT … STORE`, `BITOP`), because an empty result deletes that key; so is
an expiry that has already passed, whichever command sets it (`EXPIRE k 0`, `SET k v PXAT 1`, `HEXPIRE h
0 …`). `PUT /redis/config` is `system.admin`, so `CONFIG SET` is. An expiry still to come is a write
however soon it falls, as setting one key's expiry from the form has always been, and `SET` over a key is
a write whatever the key held. The forms for one hash field's expiry (`POST /keys/field/expire`,
`/persist`) and for claiming a group's pending entries (`POST /keys/stream/claim`) are `service.control`
for the same reason `HEXPIRE` and `XCLAIM` are writes in the console: the first sets an expiry still to
come or removes one and refuses the one that has passed, and the second moves who an entry is pending for
and removes nothing. `POST /keys/bulk` (`delete` and `expire`, not a dry run), and `overwrite` on
`/keys/rename` and `/keys/copy`, apply the same capability and budget by hand for the same reason.

Two Redis routes are WebSockets, both `system.admin`: `GET /databases/{id}/redis/monitor` (MONITOR shows
every client's arguments) and `GET /databases/{id}/redis/subscribe` (a pattern of `*` is every message any
application publishes). Each records an audit entry at open, runs for a bounded time and a bounded number
of events, sends an `end` frame saying which bound ended it, and closes.

#### MongoDB: a pipeline or a command is classified after it is parsed

`POST /databases/{id}/aggregate` parses the pipeline
and needs the destructive capability when a stage writes (`$out`, `$merge`) or is not known to be a
read; `POST /databases/{id}/mongo/command` classifies the command by its first key and its arguments
(`dbx.MongoClassifyCommand`), refuses what is never run, and asks for `system.admin` or the destructive
capability and budget as the class demands, with anything unlisted treated as destructive and a flag
taken as set unless it is absent, null, `false` or a zero (a profile level the server reads as 0,
`NaN` among them, is a write). A pipeline field given twice is read at every occurrence. The update,
rename, index and `collMod` routes are `service.control` and check by hand for the one option each has
that removes data (`mongoNeedsDestructive`).

### Protected connections

A connection marked read-only (`readOnly` on `PUT /databases/{id}`) is one the dashboard may look at
and may not change. One middleware, `protectReadOnlyConnections`, stands in front of every route under
`/databases/{id}` and refuses each request that is not a `GET`, `HEAD` or `OPTIONS` with
`409 connection_read_only`, for every role, unless its route is on the allowlist in
`api/handlers_db_protect.go`. It is an allowlist so that the rule fails closed: a mutating route added
tomorrow is refused until somebody decides it belongs on the list, and
`TestProtectionCoversEveryMutatingRoute` walks the real router against a second list, written out by
hand, that gives every mutating route its answer.

What the list lets through is of three kinds:

- **What does not change the database**: the connection's own record (which is how the mark is taken
  off again), its power and its reachability; a dump taken, uploaded or deleted, which are files on
  this machine; the dashboard's own state about it (saved queries, the diagram's arrangement); the
  POSTs that only read; and stopping work in flight, which changes no data and is what the operator
  of a production server most needs to be able to do.
- **What is a read or a write by what it carries** — the statement routes, a result as a file, a
  change set, maintenance, the Redis console and bulk action, MongoDB's pipeline, command and
  document counts — let through when the handler's own classification says read. Each check judges
  the body as the handler's decoder will read it: field names folded as `encoding/json` folds them
  and a repeated field refused, or the handler's own request type and decoder.
- **What is shown and not run**: a schema form asked only for its statement (`/ddl/*?preview=1`). The
  flag is in the address, so the middleware asks it itself; no other route is opened by it.

Everything else is refused: rows, structure, imports, a restore, a copy, the drop, accounts and
privileges, settings, extensions, keys, documents, collections and indexes. Two things reach a
protected connection from outside its own path and are held to the same answer: restoring a backup
run into it (`POST /backups/runs/{runID}/restore-database`), and a request addressed to a
neighbouring connection on the same server that would reset the account it signs in with, or
replace or drop the database it is on.

The rule guards the dashboard's own controls and is exactly as strong as the classification: a
`SELECT` that calls a function which writes is a read to anything that judges a statement by its
text. An operator who needs the engine itself to refuse writes gives the connection an account that
cannot make them. The per-route answers, the neighbour rule and what it does not follow are in
[Databases](../backend/databases-proxy-platform.md#protected-connections).

Files: `api/handlers_*.go`, one per feature, each with `mount<Feature>Routes` called from `Routes()`.
`handlers_domains.go` and `handlers_docker_manage.go` own no mount function — they are mounted from the
proxy and Docker mounts so those route maps stay in one place. Shared plumbing (`atoiDefault`,
`timeoutCtx`, `recordAudit`, `detachedContext`) lives in `api/helpers.go`.
