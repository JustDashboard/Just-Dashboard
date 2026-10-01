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

Some routes apply additional checks to request **content**, because the path cannot know:
`POST /databases/{id}/query` uses `dbx.Classify`, fails closed and applies capability + budget in the
handler. Container creation and recreation use `api.authoriseSpec`: privileged mode, added
capabilities/devices, host/shared network namespaces and bind mounts require `system.admin`.
Referenced network drivers and named-volume drivers/options are inspected too; a named volume cannot hide a
host bind or plugin mount from this policy. Local filesystem volume backing paths must be absolute and
pass the configured file-root check, including for administrators.

The log routes decide on the source, not the path. `/logs/stream`, `/search`, `/download`,
`/retention` and `/source` are `read`, but every one parses its `source` through `logTargetFor`, which
refuses auth data — `auth.log` and `secure` with their generations and anything resolving to them, a
file whose rotated generations include such a link, a PM2 process whose out or error file is one, the
`ssh`/`sshd` units, a `journal-id:` naming `sshd`, `sshd-session`, `sshd-auth`, `sudo`, `su`, `login` or
`systemd-logind` — with a 403 to anyone without `system.admin`, and `/logs/sources` leaves those out of
its answer for them. The whole journal (`journal:`) is not among them: it stays `read`, as before the
gate, so a reader can still find those lines, and their facets, in it unfiltered. Whatever the gate,
a rotated generation is read only when it resolves inside `JD_LOG_ROOTS` itself: the live file passing
says nothing about where `app.log.1` leads. The reads the service logs added sit at `read` beside their siblings:
`GET /logs/source`, `GET /databases/{id}/logs/sources` and `/querylog`, `GET /proxy/sites/{name}/requests`
with `/stream` and `/export`, `agent=` and `referer=` on a deployment's request routes, and
`container=`/`stack=` on `GET /docker/events` and its socket (refused with a 400 before the upgrade). The
per-feature log reads they replaced — `/docker/containers/{id}/logs` and its `/stream`,
`/docker/stacks/{name}/logs/stream`, `/systemd/{name}/journal` and its `/stream`, and
`/pm2/{name}/logs/stream` — are gone, so no second path reaches a log around that check.

Compose creation, configuration edits, validation and execution all require `system.admin` until the
complete resolved Compose model has a shared policy. Stack details evaluate Compose only for
administrators; other accounts retain static YAML service names without interpolation or includes.
`GET /docker/stacks/{name}/run?action=…` also
derives destructive capability and confirmation from the action using the same `composeIsDestructive`
set the POST routes use. That socket is the one place a phrase may arrive as a query parameter
(`RequireTypedConfirmationWS`) — a browser cannot set a header on a WS handshake, and `wsx`'s origin
check replaces what the header guarded.

`POST /databases/{id}/maintenance` is `service.control` and names its action in the body. An action
that locks a table against the application while it runs, or can lose rows (`vacuum_full`, `reindex`,
MySQL `optimize` and `repair`, SQLite `vacuum`), asks for `destructive` and spends `destrLim` in the
handler, before anything is dialled: those are statements the SQL console refuses the same account.
Which actions those are is a property of the closed action list (`dbx.MaintenanceAction.NeedsDestructive`),
published to the page as `requires`. The role routes check content too: an alter is refused for an
attribute the engine cannot change (`dbx.CheckRoleRequest`), and for locking out or demoting the
account the connection itself signs in with.

Two deployment reads open to every account answer more for an administrator. A webhook trigger list
(`GET /deploy/{id}/environments/{env}/triggers`) carries each trigger's delivery summary
(`lastDelivery`, `recent`) only for a session holding `system.admin`, because the delivery log it
summarises is that caller's route and a summary is not a way around it. `GET /deploy/hostname?hostname=`
resolves the typed name and answers `resolves` only for `system.admin`: resolving a name the caller
chose is traffic the caller directs, and the reverse proxy's own DNS check, which it reuses, is kept at
that capability. For anyone else the lookup never runs and the field is absent, never `false`.

Files: `api/handlers_*.go`, one per feature, each with `mount<Feature>Routes` called from `Routes()`.
`handlers_domains.go` and `handlers_docker_manage.go` own no mount function — they are mounted from the
proxy and Docker mounts so those route maps stay in one place. Shared plumbing (`atoiDefault`,
`timeoutCtx`, `recordAudit`, `detachedContext`) lives in `api/helpers.go`.
