# Edge rules

A deployment's public route can decide three things at the proxy, before the application sees a
request: a **rate limit** per client address, **CrowdSec's bans**, and an ordered list of **edge
rules**. They are the route's — the proxy serves every domain of a deployment from one site — so,
like the address list, headers and sign-in of [#312](implementation.md), they live in the plan's
runtime (`runtime.rateLimit`, `runtime.edgeRules`, `runtime.crowdSec`), are saved through the
configuration PUT, and are edited from Settings → Domains → **Edge rules**. The proxy-side types,
bounds and renderers are `proxysvc/deployment_edge.go`; the CrowdSec list is
`proxysvc/deployment_crowdsec.go` and its refresh `api/deployment_crowdsec.go`.

## Edge rules

A rule is `{name, match, action}`. The first rule whose match a request meets decides it; a request
that meets none goes on as it would have.

- **match** — every condition at once, at least one set: `pathPrefix` (`/admin` matches `/admin` and
  `/administrator`, case-sensitive on both proxies) *or* `pathRegex`, `methods` (any of GET, HEAD,
  POST, PUT, PATCH, DELETE, OPTIONS), `userAgent` (a pattern, matched ignoring case) and `clientFrom`
  (addresses and ranges, any of them). Country is not a match: neither nginx nor caddy:2-alpine
  carries a GeoIP database here, and a match one proxy could not answer would mean different things
  on different hosts. Host-wide country blocking is the network gateway's (Network → Protection).
- **action** — `block` (403), `status` (an answer with that status and nothing else, 200–599 other
  than 3xx: no challenge page), `allow` (the request goes on and no later rule is asked) or
  `redirect` (`location`, an http(s) address or a path, with `code` 301, 302, 307 or 308).

Patterns are held to what nginx's PCRE and Caddy's RE2 read alike and what both configuration files
can carry as text: they must compile as RE2 (so no lookaround or backreference), and carry no quote,
brace (write a counted repetition out), escaped backslash, Unicode class, inline flag or control
character. Names are lower-case letters, digits and dashes, unique per route. At most 32 rules and
64 addresses per rule. The plan keeps each rule in one spelling (`proxysvc.CanonicalEdgeRules`, run
by `canonicalConfiguration`: trimmed text, methods upper-cased, sorted and once each, addresses
canonical, a block's 403 and a redirect's 302 filled in), and validation refuses any other, so two
plans that mean the same rules have the same bytes and digest. Refusals name `runtime.edgeRules`
and the rule (`edge rule <name>: …`), which the page shows on that rule's row.

**nginx** (`renderEdgeObjects`, `renderEdgeChecks`): every condition is an http-level `map` (or a
`geo` for addresses) that is 1 when the request meets it, every rule a `map` of its conditions, and
the decision — `$jd_<site>_rules` — a `map` over the rules' variables whose first `1` names the
rule. The returns are `if ($…_rules = <name>) { return …; }` at server level, after the maintenance
check and before the address list and the password, so a refused bot is never asked to sign in; an
allow writes no return. When the route has an address fence, the fence is the first key of the
decision, so a request outside it is decided by the fence's 403 — as on Caddy — and not by a rule.

**Caddy** (`renderCaddyEdgeRules`): each rule is a named matcher in the route block that carries
its conditions and, for every rule before it, a `not { … }` of that rule's conditions, so a request
meets at most one: the first. The rule's name is set with `vars` before anything answers, then
`respond` or `redir` acts. The block runs fence → edge rules → maintenance → password or sign-in →
application, the order nginx decides them in. A prefix is rendered as an anchored `path_regexp`, not
Caddy's `path` matcher, which ignores case where nginx's prefix does not.

**The request record** carries the deciding rule's name: nginx appends `er=<name>` to the route's
timed log format (empty when no rule decided), Caddy appends `edge_rule` with `log_append`, read
when the record is written (its default; `log_append <key` would read it before any rule ran).
`accesslog.Entry.EdgeRule` reads either, and the requests summary counts each rule
(`summary.edgeRules`, every rule that decided anything), which is the readout beside each rule on
the page: how many requests it decided in the last 24 hours. `crowdsec` is the name of a request the
CrowdSec list refused.

## Rate limit

`{requests, per: second|minute, burst}` — one allowance per client address.

- **nginx** renders the site's existing limits (`sites_limits.go`): `limit_req_zone` keyed by the
  client address, `limit_req … burst=<burst> nodelay`, `limit_req_status 429`, and `error_page 429`
  to a location that answers `429` with `Retry-After` — the seconds one request's allowance takes
  to return, at least one (`DeploymentRateLimit.RetryAfter`). The client address is nginx's
  `$remote_addr`; when the dashboard has `JD_TRUSTED_PROXIES`, a route with a rate limit, edge rules
  or the CrowdSec switch believes `X-Forwarded-For` from those proxies alone (`set_real_ip_from`,
  `real_ip_recursive on`), so the limit, the rules' address matches and the ban list count the
  visitor rather than the proxy in front.
- **The Docker Caddy ingress** renders nothing for it: caddy:2-alpine 2.11 has no rate-limit module
  among its standard ones, and none of them can express one. The limit is enforced on the host
  instead, through the network gateway's existing per-source limits (`netx` limits, the audited
  `POST /network/protection/limits`, `network.limit.add`): the Domains page says so and offers
  **Apply on the host**, which adds a limit of the route's rate and burst on TCP ports 80 and 443,
  per source address, refused with a reset. That limit is **host-level, not per domain** — it
  counts every domain the ingress serves together — and it counts **new connections, not
  requests**: a client holding one keep-alive or HTTP/2 connection is limited only as it reconnects.
  The gateway's rules run in the forward chain on Docker-published ports (`ct status dnat`), before
  Caddy sees the connection.

## CrowdSec

With `crowdSec` on, the route refuses the addresses CrowdSec's local engine has banned, whether or
not a firewall bouncer is installed. The list is one file per proxy that every such route takes in:
`<nginx dir>/jd-edge/crowdsec.conf`, included in a `geo` of the route's own, on nginx;
`/config/just-dashboard/shared/crowdsec.caddy`, imported into the route block, on the Docker Caddy
ingress (beside the routes, so the routes' glob does not read it as a site). Applying a route with the
switch writes an empty list first if there is none.

`startCrowdSecRefresh` reads the engine every minute with `netsec.CrowdSecBans` — one
`cscli decisions list -o json --limit 10000`, the dashboard's existing read of CrowdSec — and keeps
the bans on an address or range (`proxysvc.CrowdSecBanned`: a captcha is not a ban, and a country
or AS scope is not something either proxy can match), each once, newest first, capped at 10,000
with the rest counted as omitted. The file is rewritten only when it changed, and the proxy is
reloaded (`nginx -t` then reload; `caddy validate` then reload) only when a route reads it. On a
host without cscli nothing is read or written; an unreadable engine leaves the list as it was,
since emptying it would let every banned address back in.

## Where else they appear

- A tailnet-only environment has no public route, so it refuses all three (`runtime.tailnet`).
- The release comparison lists them (`edge rules`, `rate limit`, `crowdsec`).
- The Proxy page reads a deployment's route like any site; it cannot represent these maps, so a save
  there would drop them until the next release writes the route again.

## Tests

- `go test ./internal/proxysvc -run 'Edge|RateLimit|CrowdSec'` — validation and canonical form,
  both renderings (nginx maps, the decision's order with the fence and CrowdSec first, returns
  between maintenance and the fence, `er=`, the limit with `Retry-After`, `set_real_ip_from` for
  trusted proxies; Caddy's exclusion chain, `vars`, `log_append`, the block's order), `Retry-After`
  arithmetic, the ban list and its refresh.
- `go test ./internal/deploy -run 'EdgeRules|Edge'`, `go test ./internal/accesslog -run EdgeRule`,
  and `bun test src/components/deploy/settings/edge-rules-model.test.js` (the editor's canonical
  form, problems, order and the host-level limit it posts).
- Live, the host's nginx in a private prefix: `go test ./internal/proxysvc -run TestLiveDeploymentRouteEdgeRulesOnNginx`
  — a blocked path, an allow ahead of it, a user agent's status, a redirect, a CrowdSec ban reloaded
  in and lifted with no release, three requests served within one a second and a burst of two, the
  fourth answered 429 with `Retry-After: 1`, another address served on its own allowance, served
  again after a second, and the record naming every deciding rule.
- Live, Docker: `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyServesRouteRules`
  now also asks a real Caddy for a blocked path (and the same path in capitals, which nginx and Caddy
  both serve), an allow ahead of a broader block, a user agent's status, a ban of every address and
  its lifting, and reads the rule names back from the record.
- `tests/browser/deploy-edge-rules.spec.ts` — the editor (mocked).

The gateway limit itself is the network module's and is tested there; nothing here measures it
against a real Caddy, and no CrowdSec engine runs in these tests (the list is written as the refresh
writes it).
