// Package httpx is what every route shares: the error envelope and JSON
// decoding (respond.go, failures.go, query.go), the Principal a request is
// made as (context.go), and the middleware Routes composes: the network
// allowlist (middleware.go), authentication and capability checks (authmw.go),
// CSRF, rate limits, the audit of every mutation (auditmw.go) and the typed
// confirmation (confirm.go). It mounts no route and owns no feature: api
// chooses a route's middleware, and auth decides whether a credential is
// good. See docs/internal/architecture/request-lifecycle.md.
package httpx
