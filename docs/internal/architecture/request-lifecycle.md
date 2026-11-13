# Request lifecycle and handler architecture

## The request chain is the security contract

`backend/internal/api/routes.go` is the map of the whole API. Every `/api/v1` request passes:

```
network allowlist → authenticate → rate limit → CSRF (session mutations) → capability → handler
```

The rate limit of a route with a credential is the credential's, so it follows authentication. A
route with none (sign-in, the webhooks, the hub's discovery) has a per-address limiter in front
instead, and credentials that fail are counted per address (below).

- **Allowlist before auth** (`httpx.AllowlistCIDRs`): an off-network attacker cannot reach the login
  handler at all. `httpx.RealIP` trusts `X-Forwarded-For` only from `JD_TRUSTED_PROXIES`, or the
  allowlist could be spoofed past.
- **Three limiters**, `NewLimiter(perMinute, burst)`: `loginLim` (10/5, per address, on top of the
  per-account lockout), `apiLim` (600/120, per credential), `destrLim` (30/10, destructive routes).
  `apiLim` is `PerCredential`: a browser session and each API key have a bucket of their own
  (`httpx.credentialKey`), so a CI job's key cannot empty the bucket an open page polls from, even
  where an SSH tunnel puts them both on one address. The assistant is not a credential: it works
  under the person's session principal and spends that session's bucket, as the person's page does
  ([invariant 13](../security/invariants.md)). `destrLim` is not `PerCredential`: it stays the person's at their
  address, whichever
  credential spends it, so minting a key or asking the assistant buys no second allowance of
  irreversible actions. A refusal is `429 rate_limited` in the envelope with `Retry-After` set to
  the bucket's real wait in whole seconds (rounded up, never 0) and `RateLimit-Remaining: 0`; an
  admitted request carries `RateLimit-Remaining`. A handler that spends a budget by hand uses
  `Limiter.Spend(key, message)`, whose error carries the same wait. The clients wait a short
  `Retry-After` out and ask again, because the refusal came before anything ran: `jd` and both
  SDKs twice at most for a wait of ten seconds or less, the page (`fetchResponse`) once, for a
  read, for five seconds or less.
  TOTP setup, enrollment confirmation and verification share a separate account-keyed `loginLim`
  bucket; changing IP or switching between these routes does not reset the second-factor budget.
  A password asked for again from a session spends the same bucket (`Server.spendGuess`):
  `POST /account/password`, `/account/2fa/disable`, `/account/recovery-codes` and a passkey
  registration's begin. Five wrong codes for an account, authenticator or recovery, also lock its
  second factor for fifteen minutes (`auth.countFailure`), answered `429 second_factor_locked`.
  A passkey sign-in's finish (`POST /auth/passkey/finish`) sits in the password sign-in's group and
  spends the same per-address `loginLim` budget; its begin only hands out a sealed challenge and has
  a budget of its own, `passkeyLim` (60/20 per address), so a sign-in page's autofill cannot exhaust
  the password's. Registering a passkey re-checks the password and code on the account-keyed
  second-factor bucket.
- **Failed credentials are counted per address** (`httpx.FailureBudget` on `Authenticator.Failures`,
  60 a minute with a burst of 60). The limiters above are keyed by who is asking, so a key or a
  session that does not resolve never met them: 400 wrong keys used to get 400 answers of `401` in
  0.4 s and leave no trace. Each one now spends from its address's budget, in `Authenticate`,
  `AuthenticatePartial` and `AuthenticateMCP` alike. Past it, a wrong key is answered
  `429 rate_limited` with the wait in `Retry-After` instead of `401`, and the log gets one
  `repeated failed authentication` line a minute for the address. Nothing is refused before the
  lookup, and a session cookie that fails keeps its `401` (the page's cue to offer the sign-in): an
  address is shared wherever an SSH tunnel ends, so the count never locks a valid key or a
  signed-in browser out. Guessing a key is not feasible either way; this is what tells a script
  with a revoked key to stop, and what records that an address keeps failing.
- **Capabilities, not roles** (`auth/roles.go`): `read`, `service.control`, `file.write`, `terminal`,
  `destructive`, `system.admin`, `metrics.read`, held by `admin`/`limited`/`readonly`. Gate with
  `httpx.RequireCapability` so adding a role later cannot silently widen an endpoint.
  `httpx.RequireSession` additionally blocks API tokens from human-only routes (password change,
  minting tokens, account management). The one key it lets by is a deploy key that the deploy key
  table admitted to that very route (below); for every other key the rule is unchanged.
- **A scoped API key** (`api_tokens.scope`, minted with `scope: "metrics.read"` on `POST /tokens` or
  from Databases → Metrics export) holds one capability and nothing else. `Authenticate` refuses it
  on every group with `403 key_scope`; `AuthenticateScoped` admits it for the two scrapes alone,
  `GET /api/v1/metrics/databases` and `GET /api/v1/metrics/deployments`, which sit outside the
  `/databases` and `/deploy` trees for that reason and also refuse a browser session
  (`httpx.RequireToken`, `403 token_required`).
- **An MCP key** (`api_tokens.scope = 'mcp'`, minted on `POST /tokens/mcp` or from Databases → MCP
  server) narrows *where* a key goes rather than what its role may do. `Authenticate` and
  `AuthenticateScoped` both refuse it with `403 key_scope`, so it opens no route but
  `/api/v1/mcp`, whose group runs `httpx.AuthenticateMCP` instead: a bearer key only, no session
  cookie read (so no CSRF header is needed and no page can ride a cookie), any other key refused with
  `403 key_scope`, and a `WWW-Authenticate: Bearer` challenge naming no authorization server. Inside
  the endpoint the principal carries the key's role (narrowed to its owner's) with no capability
  scope, and every tool call
  is held to the databases `api_token_connections` grants the key, read again on each call, and to
  its deployment grant (`api_tokens.deploy_access`, narrowed to chosen deployments in
  `api_token_deployments` as a deploy key is), read with the key. A tool generated from the
  deployment API (offered only to a key whose owner turned them on, `api_tokens.mcp_operations`) is a
  second request made inside the process through the root router, with no
  credential and the MCP key's principal in its context (`httpx.WithMCPDelegate`): it passes the same
  layers, and the feature group's `AuthenticateFeatures` admits it only as the deploy key gate admits
  a deploy key, marking it `Principal.ViaMCP` (every other group refuses it); see
  [the MCP server](../backend/databases-proxy-platform.md#the-mcp-server).
- The dashboard's assistant makes its calls the same way, through the root router from inside the process, but as
  the person: their own session principal, read again for the call, is carried in the context
  (`httpx.WithAssistant`), and every group's authentication takes it in place of a credential, marked
  `Principal.Assistant` so the route's audit entry records actor `assistant`. It is not a key and the deploy key gate
  never sees it: the route's capability check and handler rules decide as they do for the person, its destructive
  actions spend the person's own `destrLim` budget, its other calls the budget of the session it works in, and
  the request carries the CSRF header a page sends. A WebSocket or a stream is not made this way, nor the short list
  in `agentExclusion`, nor a route into a protected database connection (`agentProtected`). A command it runs on the
  host (`run_command`) is not a request at all: it is the terminal's own way of running a line
  (`term.Manager.RunCommand`), checked against the terminal capability on every call; see
  [the deployment assistant](../deployments/operator.md).
- **A deploy key** (`api_tokens.scope = 'deploy'`, minted on `POST /tokens/deploy` by an administrator
  in a session, or from Account → API keys) is what `jd` and CI use, and it **deliberately reopens
  part of the session-only surface**: the deployment writes that were a session's alone — drafts,
  configuration, source, Git policy, triggers, variables and shared variables — answer it, and so do
  the operating verbs an ordinary key had (promote, the warm release's instant rollback and keep, the
  maintenance switch) with their reads. It holds
  the administrator role, narrowed to its owner's now. `Authenticate`, `AuthenticateScoped` and
  `AuthenticateMCP` refuse it with `403 key_scope`; the feature group authenticates with
  `httpx.AuthenticateFeatures(s.deployKeyGate(root), …)`, which asks the router itself which route
  the request will reach (`chi.Routes.Find`) and answers from `deployKeyRoutes` in
  `api/deploy_keys.go`: a listed route admits the key (setting `Principal.DeployRoute`, which is what
  `RequireSession` honours), a line marked session-only answers `403 session_required` with its
  reason, and anything unlisted — including any route added later until it is given a line — answers
  `403 key_scope`. A refusal is audited as `auth.deploy_key.refused` with the key, because it happens
  before the group's audit middleware. Reveals, saved credentials, the GitHub-speaking pull-request
  verbs, preview approval, the typed-phrase removals, the warm-release and rehearsal terms, a copy of a
  release's data, what a linked database gives previews and a preview's database branch refreshed,
  and a route's pages stay session-only for it. A key narrowed to
  chosen deployments (`api_token_deployments`, read with the key on every request) reaches only
  routes whose `{id}` it was given and whose `{env}` belongs to that deployment, and none marked
  `every` (server shared variables, fleet-wide reads); `GET /deploy/` lists only its deployments. A
  deploy key's drafts are its own (`deploy_drafts.key_id`; `deploymentDraftForPrincipal` holds the key
  to them), and the deployment a narrowed key's commit creates joins its grant in the commit's
  transaction. The handlers hold what a deploy key writes: generated values are withheld, and a
  plan or environment change that would reach the host, redirect a saved credential, or point a
  variable at a credential, database or secret store is refused with `session_required` (see
  [invariant 4](../security/invariants.md)).
- **`httpx.AuditMutations`** records every state-changing request, with the API key's id and name
  (`audit_log.key_id`, `key_name`) when the request carried one, and, for a session a Just Hub sign-in
  ticket opened, actor `hub-session` with its hub user and session id (`audit_log.hub_user`,
  `session_id`). Who acted is filled in by one builder (`httpx.principalEntry`; `httpx.AuditEntry` for a
  handler writing an entry of its own), so `AuditRead`, `s.recordAudit` and the handlers' own entries
  record the same. WebSocket routes are GET and
  long-lived, so they call `s.recordAudit(...)` at open time — the event is "a terminal was opened".
- **`httpx.RequireCSRF`** requires `X-JD-CSRF: 1` on every browser-session mutation, including login
  and partial 2FA sessions. The header makes a same-site sibling origin preflight, and this application
  grants no cross-origin browser access. Bearer tokens, agent mTLS and the HMAC webhook do not use
  ambient cookies and are deliberately outside that check. The one browser-session group without it
  is Remote Desktop's stream bridge (`/remote-desktop/bridge/*`), whose own page cannot know the
  header: there every write and every socket must carry this dashboard's `Origin` instead
  (`wsx.Upgrader.SameSite`), on a rate limit of its own.
- **WebSocket origins** are checked by `wsx` against the request host, port and the scheme Caddy serves:
  HTTPS for `JD_TLS=internal` or `tailscale`, HTTP for `JD_TLS=off` or local development. An explicit
  `JD_ALLOWED_ORIGINS` entry remains the only cross-origin exception. The backend's loopback HTTP hop
  does not determine the browser-facing scheme. A socket whose client names a subprotocol upgrades
  through `UpgradeProtocol`, which answers with it only when it was offered — Remote Desktop's
  Guacamole tunnel asks for `guacamole`, and a browser drops a handshake that does not name it.

A session made by a Just Hub sign-in ticket (`auth_method = 'hub'`, only with `JD_HUB_SIGNIN=true`) is
complete from the start and resolves like any other, with two differences: `ResolveSession` checks again on
every use that the switches, the link and the account's grant still hold, deleting the session if not, and
`httpx.RefuseHubSession` refuses it (`403 hub_session_not_allowed`) on every route that makes, changes or
reveals a credential, or makes or changes a link. A socket is authenticated once, at the upgrade, so every
socket `wsx.Upgrader` opens is handed to the API's guard (`wsx.Guard`, `hub_session_sockets.go`), which watches
the ones a hub session opened, as it watches the Remote Desktop stream bridge's proxied sockets: each closes at
the session's expiry, at once when a revocation of hub sessions commits, and at the next check (every 15 seconds,
`auth.Service.CheckSession`, which does not count as use) when anything else ended the session. Its redeem, `POST /api/v1/auth/hub/enter`, sits with `POST /auth/login`: no session, the
sign-in budget per address, the CSRF header and its own audit entry; the ticket is the credential
([invariants.md](../security/invariants.md#the-exception-hub-sign-in)).

After completing any required second factor, an account marked `must_change_pw` may use only
the session-only `POST /account/password` mutation and authentication status/logout routes. Feature
routes and API tokens return `password_change_required`. Changing the password requires the current
password and revokes existing sessions; the sign-in page then signs straight back in with the new
password, through any required second factor. Password change has its own
authentication middleware so it cannot bypass an outstanding second factor.

Four deliberate exceptions: `/healthz` (unauthenticated, fixed body, no version or hostname, and
deliberately blind to the store: [the dashboard's own health](runtime-boundaries.md#the-dashboards-own-health)),
`GET /api/v1/hub/hello` (the Just Hub discovery probe: unauthenticated, but behind the allowlist and the
per-address API limit, a fixed body naming only the product, the protocol and whether this server can be
connected, and not mounted with `JD_HUB_LINK=false` or in agent mode; [hub-link.md](../backend/hub-link.md#local-api)),
the webhooks, `/api/v1/hooks/*` and `POST /api/v1/deploy/{id}/hooks/{triggerID}` (each checked
against its hook's secret, an HMAC over the raw body or GitLab's token header, still allowlisted,
still audited so enumerating hook ids is not silent), and `GET /api/v1/tailnet/whois`, which a tailnet-only
deployment's gate asks about every request: still behind the allowlist, outside the API rate limit,
and answering only a loopback caller that no other proxy forwarded — the dashboard's own Caddy says it
did in `X-Forwarded-For` — with every refusal audited as `deploy.tailnet.refused`
([tailnet-only deployments](../deployments/tailnet-apps.md#the-whois-route)).

## Handler conventions

Handlers are `httpx.Handler` — `func(w, r) error`, rendered by its own `ServeHTTP`. `s.handle(...)` at
the mount site is only the conversion.

- Return `httpx.Err/BadRequest/Internal/Wrap`; never write an error body by hand. `httpx.WriteError` is
  the single renderer and is what keeps internal error strings off the wire.
- **Three failures are not about the request and have one answer each** (`httpx/failures.go`).
  *This server lacks X* is `503 not_installed` with the thing in `error.resource`
  (`httpx.NotInstalled("nginx", err)`, or `httpx.Absent` when the sentence is not "is not installed":
  systemd that is not running); the page shows that code as "not on this server", not as a fault.
  Docker keeps its own `503 docker_unavailable`, which also covers a daemon that is stopped. *A
  handler's own deadline* is `504 timeout`, retryable: `WriteError` answers it for any error that
  still carries `context.DeadlineExceeded` in its chain while the caller is still waiting, whatever
  status the mapper on the way chose, unless the handler already marked its answer retryable (the
  proxy's `busy` and `test_unfinished` keep their words). So wrap a cause (`Wrap`, `Internal`,
  `Refused`, `%w`) and do not test for the deadline in a handler. *The host failed* (a full disk is
  `507 disk_full`; I/O error, no descriptors, no memory are `500 host_failure` with the kernel's two
  words) is said and logged. A request whose caller left is logged at debug as `request abandoned`,
  not as an ERROR.
- **The last branch of an error mapper is `httpx.Refused(err)`**, never `httpx.BadRequest("%v", err)`.
  It refuses with the error's own sentence, `400 bad_request`, as before, except for the three above
  and for an error that is already an `APIError`, which keeps its status. The old spelling made a
  deadline, a missing program or `ENOSPC` under any of some 250 call sites the caller's mistake, and a
  400 is not logged. `TestNoHandlerCallsAServerFailureABadRequest` keeps it out of `internal/api`
  (the session and assistant handlers are on its list until they move). The default stays a 400
  because most packages return validation sentences as plain errors; a package that can tell its own
  failures apart should say so with a sentinel and let its mapper answer 5xx.
- **A group whose routes share a cause of failure says so at the mount** (`httpx.MapErrors`). The
  function sees the error a handler under it returned, only when one did. `nginxOnly`
  (`api/handlers_proxy.go`) is the one use: a route that only nginx can answer, failing on a server
  without nginx, is `503 not_installed` whatever its handler made of the failure (they used to say
  400, 404, 409, 502 and two different 503s). A route that also serves Caddy must not carry it.
- **A time that was never set is left out of the answer**, never sent as `0001-01-01T00:00:00Z`. A
  `time.Time` in a JSON struct carries `omitzero` (Go 1.24; `omitempty` does nothing on a struct and
  is refused), or is a `*time.Time` where `null` is the documented spelling. The page reads an absent
  time through `relativeTime`, `timestamp` and `calendarDate`, which print a dash, and tests for the
  field, not for the string `0001`. `TestAZeroTimeIsLeftOutNotSentAsYearOne` walks every struct under
  `backend/internal` and `backend/cmd`. Nine packages are still on its list with a count each
  (`deploy`, `netx`, `dbx`, `netvantage`, `dnsservice`, `backups`, `accountlink`, `hublink`,
  `assistant`): they digest, sign or re-render a JSON encoding of their own types, so a field there
  changes only after its struct is checked against what was stored before. The count goes down; a
  new field takes `omitzero`. Two places on the page still test for year one, and neither reads the
  server's own answer: `lib/docker-events.ts` reads a health check's times as Docker wrote them, and
  `components/deploy/run-evidence.tsx` reads a run's evidence as it was stored. On the page, a type
  whose time can be absent says so (`lastLoginAt?`, a certificate's `notBefore?` and `notAfter?`, a
  site file's `modified?`), so the compiler names the code that has to cope: a date that goes
  through `Date.parse` into `toISOString()` throws on an absent time where year one only looked
  wrong.
- **A body may keep the server waiting only so long** (`httpx.BodyDeadline`, on the root router). The
  server has a header timeout and no read or write timeout, because either would sever log tails,
  uploads and terminals; that left a body unbounded, and a client could send its headers and then a
  byte now and then. Only time spent blocked on the client is counted: 60 seconds (`BodyGrace`) plus
  one second for every 4 KB that has arrived (`BodyMinRate`), so a handler's own time is never
  charged and an upload is never cut for being large. A body that runs out of it reads as
  `408 body_timeout` (`DecodeJSON` answers exactly that; another reader answers its own refusal). The
  connection's read deadline stands only while one of the body's reads is in progress, never between
  them. `WriteError` reads what is left of a small unread body through the same allowance before it
  writes a refusal, since `net/http` would otherwise do that read itself with no bound. What is not
  bounded: a body under 256 KB that a handler never reads and then answers with a success, where
  `net/http`'s own read before the response still waits on the client.
- **Every request has an id** (`httpx.RequestID`, the first middleware): sixteen hex digits, or the
  caller's own `X-Request-Id` when it is 8 to 64 letters, digits, `.`, `_` or `-`. It is echoed as
  `X-Request-Id` on every answer, it is the `request` attribute of the `request failed`, panic and
  debug request log lines, and `WriteError` puts it in the envelope of a 5xx as `error.requestId`.
  `httpx.Internal` hides a 500's cause from the client on purpose, so the id is what the person
  holding it can quote: the page appends it to `internal error`, `jd` prints it, both SDKs carry it,
  and `grep <id>` in the backend's log finds the line with the cause. A request the dashboard makes
  of itself through the root router (the assistant's calls, an MCP tool's) keeps the id of the
  request that caused it.
- The router's own refusals are in the same envelope (`api/route_methods.go`): a path nothing is
  mounted at is `404 not_found`, and a method a path does not take is `405 method_not_allowed` with
  the methods it does take in `Allow` (`HEAD` and `OPTIONS` included: no route serves either). Both
  are set on the root router before anything is mounted, so every subrouter inherits them; under a
  group they are still answered after that group's authentication. A WebSocket route asked for
  without a handshake answers `400 websocket_upgrade_required` in the envelope too, a foreign origin
  `403 origin_not_allowed` (`wsx.handshakeRefused`).
- **A static path never answers for its `{param}` sibling.** chi tries the static node first and, when
  it has no handler for the method, backtracks to the parameter node beside it, so `GET
  /deploy/templates/import` (a `POST`) used to reach `GET /deploy/templates/{template}` as the
  template named `import`, and `DELETE /pm2/save` would have removed a process named `save`.
  `refuseShadowedMethods`, the last middleware of the feature group, asks the router which route the
  request will reach (`Find`) and answers `405` when that is a parameter route and a static route is
  registered at the very path without the method. The table is read off the finished router once
  (`routeMethodTable`), and `TestAStaticPathNeverAnswersForItsParameterSibling` walks every such pair.
  The cost is a reserved name: a resource called exactly what an action of its collection is called
  (a volume named `prune`, a site named `bulk`, a PM2 process named `save`) cannot be addressed with
  the methods that action lacks, as it never could with the methods it has.
- Decode with `httpx.DecodeJSON` (4 MB cap, `application/json` required, unknown fields and trailing
  values rejected). SQL row values use `DecodeJSONNumbers` with the same constraints, preserving exact
  numeric tokens until the database adapter binds them. Its refusals are in the request's terms, never
  `encoding/json`'s (`httpx.decodeRefusal`): a value of the wrong kind is `displayName must be a
  string, not a number` with `error.field` set to the JSON path (`runtime.internalPort`), an unknown
  field names itself in `error.field`, a missing body is `a JSON body is required`, and a body past
  the limit is `413 body_too_large` naming the limit rather than a `400`. No Go type or struct name
  reaches the client.
- Read a query parameter that is a flag, a number or a time with `httpx.QueryBool`/`QueryBoolOr`,
  `QueryInt` and `QueryTime`, or with a `httpx.Query(r)` reader when a handler fills a struct of
  options (ask its `Err()` before using what it read). A flag is `1`, `0`, `true` or `false` in any
  case on every route; a value that is there and cannot be read (`?preview=yes`, `?limit=abc`,
  `?since=tomorrow`, a flag given twice) is `400 bad_request` with `error.field` naming the
  parameter, never the default. The rule exists because the default was the change: a preview spelled
  the way a handler did not expect ran what it was meant to show. `requirePreviewFlag` applies it to
  `?preview=` in front of every route under `/databases`, and two tests in `api/query_strict_test.go`
  keep a handler from comparing a flag with a string again and hold every reader to its `Err()`. An
  empty number or time (`?limit=`) is read as absent, as a form that submits an unfilled box sends it;
  an empty flag is refused.
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
  its worst statement, and a statement the lexer cannot read with certainty is destructive. Where a
  statement ends is the engine's reading too — a stored program's body or a block is one statement,
  cut at its client's separators (`DELIMITER`, `GO`, a `/` line) — and one that holds statements of
  its own is at least destructive whatever word leads it.
  `POST …/export/query` takes only a statement that classifies as a read.
- **Rows.** `POST …/changes` classifies nothing: its handler counts the operations the set holds and
  asks for `destructive`, and spends `destrLim`, when one of them is a delete. A dry run, which
  renders the statements and runs none, asks for neither.
- **Schema forms.** A `/ddl/*` route that only adds asks for `service.control`. `api.runDDL` asks for
  `destructive` on top of it when the plan changes a column's type, which rewrites the column, or
  when the operator's own SQL in it (a CHECK condition, an index predicate, a USING conversion, a
  function default) calls anything `dbx` does not vouch for — for a preview as much as for a run.
  The routes that store code (`POST …/ddl/routine` and `…/ddl/trigger`: a function, a procedure or a
  trigger) ask for `destructive` on every request, whatever the body says, because the SQL console
  classifies the same `CREATE` as high risk; they sit at `service.control` only so a form can ask for
  its statement as it is filled in, and a run spends `destrLim`. Their drops are in `s.destructive`.
  `PATCH …/ddl/row-security` is `system.admin` and asks for `destructive`, and spends `destrLim`,
  when the body turns a table's row-level security off or stops forcing it on the owner
  (`dbx.RowSecurityChange.TurnsOff`): either shows rows to someone the policies were filtering.
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
- **Resources.** `PUT …/resources` is `system.admin`. A new or changed limit is applied in place; a
  limit the body lifts (`memoryMb` or `cpus` of zero where the container has one) replaces the
  container, so the handler asks for `destructive` and spends `destrLim` by hand once it has read the
  container's limits (`planDBLimits`).
- **Accounts.** The role routes check content too: an alter is refused for an attribute the engine
  cannot change (`dbx.CheckRoleRequest`), and for locking out or demoting the account the connection
  itself signs in with.
- **Redis** and **MongoDB** take any command on one route; the two sections below say how each is
  read.
- **Network.** The gateway and protection PUTs (`/network/gateway/forwards/{id}`, `/gateway/nat/{id}`,
  `/protection/limits/{id}`, `/protection/blocklists/{id}`) are `system.admin`, and ask for
  `destructive` and spend `destrLim` by hand when the body disables the entry; `POST
  /network/protection/settings` does the same when a value weakens a kernel protection, and `PUT
  /network/protection/trusted` when it sets an expiry on a kept address. Exceptions (both directions)
  and session revocation are inside `s.destructive`; previews, the target check and the connection-
  table pressure read are `system.admin` and change nothing on the host. Setting a
  device down and turning forwarding off are their own paths (`/down`, `/off`) inside `s.destructive`,
  so the two directions of one switch never share a route ([network module](../backend/network.md#routes)).
- **Native network profiles.** Capability/profile reads and writes require an administrator session.
  The native PUT accepts a typed generation/intent rather than filenames and always requires pending
  confirmation. Editing and terminal cleanup retries use `s.destructive` and audit the selected device
  or change ID. Cleanup is restricted to the current account's confirmed/recovered native journal and
  never turns a confirmed decision into rollback ([native profiles](../backend/network-native-managers.md)).
- **Container specs.** Container creation and recreation use `api.authoriseSpec`: privileged mode, added
  capabilities/devices, host/shared network namespaces and bind mounts require `system.admin`. Referenced
  network drivers and named-volume drivers/options are inspected too; a named volume cannot hide a host
  bind or plugin mount from this policy. Local filesystem volume backing paths must be absolute and pass
  the configured file-root check, including for administrators.
- **Docker network specs.** Creation requires `service.control`; a custom driver or any driver options
  additionally require `system.admin`. Manual creation refuses Compose/dashboard ownership labels and
  explicit address pools that contain the connection's observed client address before Engine I/O.
  Additive IPv4/IPv6 IPAM pools retain the legacy single-pool API; validation and bounded metadata are
  described in [Docker network creation](../backend/docker-files-logs.md#network-creation). A driver
  other than `bridge` is checked against the Engine's catalogue before Engine I/O, and refused when the
  catalogue cannot be read.
- **Docker network membership and removal.** Connect and disconnect stay `service.control`, removal and
  prune `s.destructive`, but each handler reads the network's dependents afresh and refuses what its
  preview blocks (`dockerx.PreviewConnect`, `PreviewDisconnect`, `PreviewRemove`, `PruneCandidates`),
  failing closed when the dependents cannot be read: the dashboard's own containers, the shared ingress
  and a deployment's database-link members are never detached, no container joins the dashboard's own
  network, and a live deployment's network is removed only through its removal plan. Prune removes only
  the reviewed IDs still removable, never through the Engine's own prune. See
  [guarded changes](../backend/docker-files-logs.md#network-ownership-dependencies-and-guarded-changes).
- **Stopping a job.** `POST /jobs/{id}/cancel` asks for `service.control` on the route, the least any
  job takes, and `handleJobCancel` then asks for what the job's own `jobs.Spec.Capability` names: the
  capability of the route that started it. A job naming none, such as a record from before they were
  named, takes `system.admin`. `TestEveryJobSpecNamesItsCapability` holds every spec in the backend to
  naming one. It stays outside `s.destructive` on purpose: stopping is how a bad outcome is avoided.
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
- **Native DNS services.** Every `/network/dns/services` connection, native inventory, retained review
  and owned setup route requires `system.admin` and returns private, no-store responses. Credentials
  and custom trust material remain sealed/request-only; retained history omits native query entries.
  Connection deletion, reviewed native apply, owned setup apply and owned resource removal use
  `s.destructive`. All mutations record redacted audit metadata; a nonverified HTTP 200 outcome must
  remain a retained review state ([native DNS services](../backend/network-dns-services.md)).
- **Controlled probe identities.** Optional `/probe-agent/{enroll,poll,result}` routes run behind
  the global allowlist and a dedicated limiter before human authentication. One-use enrollment
  proofs or durable sequence-bound machine signatures grant only the closed probe protocol;
  cookies/API tokens cannot replace them, and they grant no dashboard capabilities. Human
  `/network/external` management/evidence requires `system.admin`; enrollment/revocation also
  require a session and revocation is destructive. Secrets/raw proofs are excluded from audit.
- **Private native DNS evidence.** Every `/network/dns/evidence` route requires `system.admin`,
  including retained questions, answers and exports. Launch and deletion are audited, deletion uses
  `s.destructive`, and reload/history/export never issue a DNS query. Unsupported or unavailable
  private policy refuses before fallback. See [resolver evidence](../backend/network-dns-evidence.md).
- **Address plans and socket history.** `/network/ipam` and `/network/flows` require `system.admin`;
  release/retirement and history/policy erasure use `s.destructive`. A plan grants no native
  ownership. Socket collection needs explicit persisted opt-in; querying/exporting never collect.
  Failed native handoffs hold planning allocations for review without repeating native creation.
- **Selected drift repair and SQM.** Drift reads retain `read`. The generation-bound selected
  repair endpoint is admin/destructive/audited with mandatory pending reconnection and pre-effect
  independent recovery. It can repair only reviewed owned files/canonical required admission;
  other native repair remains advice. Explicit SQM set/clear also requires mandatory pending
  reconnection, with a closed private typed undo. Selected drift recovery rejects that vocabulary.
- **The access boundary.** A fail2ban ban, a CrowdSec decision and an SSH settings change are judged
  by `api.boundaryGate` against the dashboard's access boundary (Caddy's listener, the pre-auth
  allowlist, the tailnet, SSH for a tunnel, tailnet-only previews) after the request is validated
  and before anything is applied: an impact that `cuts` this session's own way in answers
  `409 would_lock_you_out`; one that `affects` the boundary for somebody else answers
  `409 boundary_acknowledgement_required` until the body carries `acknowledgeBoundary: true`, which
  the forms send only after showing the same sentences from `GET /security/boundary/check`. The
  check is a GET so judging a proposal is never audited as a change. The existing own-address
  guards are unchanged and still apply. A pending SSH apply (`X-JD-Network-Apply: pending` on
  `POST /ssh/config`) is refused to anything but an administrator's interactive session and enrols
  the apply in the network recovery journal before the first write.
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
- **The server map.** `GET /map` is one read open to every account that summarises many list routes,
  so it answers each account with what those routes would: the shells only for `terminal`; the
  computers, the dashboard's accounts, the Linux accounts, the alert rules and the VPN only for
  `system.admin` — neither read for nor sent to anybody else — and the API keys the account's own
  unless it holds `system.admin`, as `GET /tokens` decides (`filterServerMap`). It sends no value
  those routes keep back (environment, DSNs, channel URLs). Its lines come from reads open to every
  role — the databases topology, the ports list's socket owners, the deployments' configuration — and
  a line goes only between two things the account is shown; a deployment's sealed variables are opened
  server-side only to read a typed `${{database.…}}` or `${{domain.…}}` reference's target, and no
  value of one is sent. `GET`/`PUT`/`DELETE /map/layout` are the
  account's own arrangement behind `RequireSession`, open to every role and audited
  (`map.layout.save`, `map.layout.reset`). See [the server map](../server-map.md#who-sees-what).

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
  this machine; a major-version upgrade's check, which only reads, and finishing one, which removes
  the old server the connection no longer reaches; the dashboard's own state about it (saved
  queries, the diagram's arrangement); the
  POSTs that only read; and stopping work in flight, which changes no data and is what the operator
  of a production server most needs to be able to do.
- **What is a read or a write by what it carries** — the statement routes, a result as a file, a
  change set, maintenance, the Redis console and bulk action, MongoDB's pipeline, command and
  document counts — let through when the handler's own classification says read. Each check judges
  the body as the handler's decoder will read it: field names folded as `encoding/json` folds them
  and a repeated field refused, or the handler's own request type and decoder.
- **What is shown and not run**: a schema form asked only for its statement (`/ddl/*?preview=1`). The
  flag is in the address, so the middleware asks it itself; no other route is opened by it.

Everything else is refused: rows, structure, imports, a restore, a copy, the drop, a major-version
upgrade and its revert, accounts and privileges, settings, extensions, keys, documents, collections and
indexes — and the connection's assistant, which would send what is in it to a model; every other
chat's call into a protected connection is answered with why and reads nothing (`agentProtected`). Two things reach a
protected connection from outside its own path and are held to the same answer: restoring a backup
run into it (`POST /backups/runs/{runID}/restore-database`), and a request addressed to a
neighbouring connection on the same server that would reset the account it signs in with, or
replace or drop the database it is on.

The rule guards the dashboard's own controls and is exactly as strong as the classification: a
`SELECT` that calls a function of the operator's own which writes is a read to anything that judges
a statement by its text. An operator who needs the engine itself to refuse writes gives the connection an account that
cannot make them. The per-route answers, the neighbour rule and what it does not follow are in
[Databases](../backend/databases-proxy-platform.md#protected-connections).

Files: `api/handlers_*.go`, one per feature, each with `mount<Feature>Routes` called from `Routes()`.
`handlers_domains.go` and `handlers_docker_manage.go` own no mount function — they are mounted from the
proxy and Docker mounts so those route maps stay in one place. Shared plumbing (`atoiDefault`,
`timeoutCtx`, `recordAudit`, `detachedContext`) lives in `api/helpers.go`. `atoiDefault` reads the
default for a number that does not parse and is kept only for the routes not yet moved to
`httpx.QueryInt`; a new handler uses the strict reader.
