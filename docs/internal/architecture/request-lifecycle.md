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

Compose creation, configuration edits, validation and execution all require `system.admin` until the
complete resolved Compose model has a shared policy. Stack details evaluate Compose only for
administrators; other accounts retain static YAML service names without interpolation or includes.
`GET /docker/stacks/{name}/run?action=…` also
derives destructive capability and confirmation from the action using the same `composeIsDestructive`
set the POST routes use. That socket is the one place a phrase may arrive as a query parameter
(`RequireTypedConfirmationWS`) — a browser cannot set a header on a WS handshake, and `wsx`'s origin
check replaces what the header guarded.

Files: `api/handlers_*.go`, one per feature, each with `mount<Feature>Routes` called from `Routes()`.
`handlers_domains.go` and `handlers_docker_manage.go` own no mount function — they are mounted from the
proxy and Docker mounts so those route maps stay in one place. Shared plumbing (`atoiDefault`,
`timeoutCtx`, `recordAudit`, `detachedContext`) lives in `api/helpers.go`.
