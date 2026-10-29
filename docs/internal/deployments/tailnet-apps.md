# Tailnet-only deployments and identity-aware access

An environment can exist only on the tailnet: published on this host's tailnet address the way a pull
request preview is ([preview isolation](preview-isolation.md#tailnet-only-addresses)), with no public
domain and no public certificate. It can also name who may open it — Tailscale logins and ACL tags —
and then every request is checked against tailscaled's own answer about who sent it before the
application sees it. Either way, and for every preview's address too, the request passes through the
dashboard's own front on loopback first, which keeps the dashboard's session cookie from the
application ([below](#the-dashboards-session-cookie)).

## The plan

`runtime.tailnet` (`deploy.RouteTailnet`) is the publication; absent is an ordinary environment.
`runtime.tailnet.allow` is the list: logins (`alice@example.com`, `bob@github` — the part after the `@`
is the identity provider's, so it need not have a dot) and tags (`tag:ci`). `tailnetgate.Canonical`
reads an entry the one way the server, the UI (`components/deploy/settings/tailnet-access.ts`) and the
gate agree on: a login is compared without regard to case and kept in lower case; a tag is compared
exactly, as the tailnet's policy file compares it. At most 64 entries, none twice. An empty list admits
everyone on the tailnet, as a preview's address does — the tailnet is the fence.

`validateRouteTailnet` refuses, on `runtime.tailnet`: a public domain beside it; anything only a public
route carries (a sign-in, allowed addresses, response headers, HSTS, maintenance, error pages); and
anything that publishes the container past loopback — the host's network, a fixed host port, extra
published ports, a bind address other than `127.0.0.1`. A plan with nothing to forward to (no internal
port, outside Compose) is refused too. The Domains page's switch clears the public rules and a public
bind address in the same save and says so first (`tailnetSwitchClears`); the host network and the
ports, chosen on purpose under Runtime, are named and left to the operator (`tailnetBlockers`).

A preview's plan is a copy of the environment's (`createIsolatedPreviewPlansTx`), so a preview of an
environment that names its visitors names the same ones and is gated the same way. A staging copy of a
tailnet-only production is tailnet-only with the same list, on an address of its own, and a staging
domain is refused for it. A trigger's preview domain pattern under a tailnet-only environment is
refused at activation (`tailnet_only_has_domains`) rather than publishing the preview on a public name.

A deploy key cannot change the publication or the list (`sameTailnetPublication` in
`handlers_deploy_configuration.go`): who may open a deployment is a person's decision. A session's save
is audited as `deploy.configuration.update` with the list in its detail.

## The address

The address is a row of `deploy_preview_addresses`, from the previews' own range, 21000–21999, under
their rules: `AllocateTailnetAddress` is `AllocatePreviewAddress` for an environment that is not a
preview, called by activation the first time a tailnet-only release is published, and the row is kept
from release to release. Every served port is stepped around, and so is every port something on the host
already listens on at `127.0.0.1`, since the address's front answers there. The startup sweep, the withdrawals that act only on a mapping the row vouches for, and
the uninstall's withdrawal treat it exactly as a preview's (`preview_tailnet.go`).

`tailscale serve` decides the scheme: `https://<node>.<tailnet>.ts.net:<port>` with the tailnet's own
certificate where the tailnet has HTTPS certificates on, `http://…` inside the tailnet's encrypted
tunnel where it does not. No public certificate is ordered and nothing binds a routable address.

Leaving the tailnet is a release without `runtime.tailnet` replacing one with it, in an environment
that is not a preview (`leftTheTailnet`): once that release is live, the mapping is withdrawn and the
row deleted (`leaveTailnet`), and a failure to withdraw is logged and leaves the row for the next
release to try again.

## The gate

tailscale serve never maps a tailnet address straight to the container. It maps the port to the
environment's **front**, a route that answers on `127.0.0.1:<the same port number>` and nowhere else
and forwards to the container's loopback publication, taking the dashboard's session cookie out of the
request on the way ([below](#the-dashboards-session-cookie)). Without a list that is all the front does,
and the tailnet is the fence, as for a preview. With one the front is the environment's **tailnet
gate**, and asks the dashboard about every request before forwarding it (`proxysvc/tailnet_gate.go`,
`TailnetGateRoute.Check`):

- **nginx**, where this host's deployment routes are host nginx's (no Docker Caddy ingress found,
  named or about to be started, and nginx installed): a server `listen 127.0.0.1:<port>`, with
  `set_real_ip_from 127.0.0.1; real_ip_header X-Forwarded-For` so `$remote_addr` is the tailnet address
  tailscaled forwarded (tailscaled's serve drops whatever the visitor sent in that header and sets its
  own), the cookie maps, and `proxy_set_header Cookie` with what they leave. A gate adds
  `auth_request` to an internal location that asks the whois route below, `auth_request_set` for the
  answer, and `error_page 403`/`500` to short plain answers — an `auth_request` that is neither 2xx,
  401 nor 403 is nginx's 500, which the gate answers as 503; a front sends `X-Tailnet-Login` and
  `X-Tailnet-User` empty, whatever the visitor claimed. The file is the environment's own route name,
  `just-dashboard-env-<id>.conf`, marked `# Tailnet gate:`, and its request record is the same
  `/var/log/nginx/<route>.access.log` in the timed format, with `tl=<login>` after it on a gate.
- **The dashboard's own loopback Caddy** everywhere else, `just-dashboard-tailnet`: `caddy:2-alpine` on
  the host's network (label `com.just-dashboard.tailnet-gate=true`), its Caddyfile at
  `/etc/just-dashboard/tailnet/Caddyfile` importing `/config/just-dashboard/routes/*.caddy`, its admin
  API a unix socket inside its own volume, automatic HTTPS off, and tailscaled's `X-Forwarded-For`
  trusted from loopback so `{client_ip}` is the tailnet address. Each route is `http://:<port>` with
  `bind 127.0.0.1`, the visitor's `X-Tailnet-Login`/`X-Tailnet-User` removed, and a `reverse_proxy`
  whose two `header_up Cookie` replacements take the dashboard's cookies out. A gate adds
  `forward_auth` to the whois route, `copy_headers` of the answer, a `vars` mark set once the check let
  the request through so that `handle_errors` answers 503 only for a check that could not be made (the
  application being down keeps its own 502), and `log_append tailnet_login` for the request record,
  which the Logs page reads from this container — a front's record too. It is started the first time a
  deployment or a preview needs it — `ensureGateContainer`, from its stopped container when there is
  one — and never asked of the operator.

The Docker Caddy ingress cannot be the gate: it runs in a network of its own, where neither tailscaled's
loopback forward nor the dashboard's loopback API reaches, and it publishes only 80 and 443. Serving the
gate on its public listener, fenced by address, would put a tailnet-only application on a routable
address, which is what invariant 7 forbids.

Activation writes the front before the mapping moves to it (`applyTailnetGate`), verifies it, and
records `tailnetGate {name, listen, allowed, applied, verified}` in the activation evidence (`allowed` 0
for a front). A host whose proxy cannot gate, or a backend that does not answer on `127.0.0.1`
(`tailnetGateAPI`), fails a gated step as `tailnet_gate_unavailable`; a front or gate that cannot be
written or verified, or whose port is taken, fails it as `tailnet_gate_failed`. Either way the candidate
stops, nothing is published, and a front that was half written is put back from its snapshot (driver
`tailnet-caddy` for the gate Caddy). A failed release's mapping is pointed back at what reached the
predecessor (`predecessorFronted`): the front, which the recovery has just restored, when its snapshot
says one stood there, and the predecessor's container when that release was published before every
address had a front. A release without the list replaces the gate with a front; the front goes only
with the address — a release that leaves the tailnet (`leaveTailnet`), route removal
(`RemoveDeploymentRoute`, a preview's cleanup). Only an executor built without the proxy's half of a
front (`TailnetGateProxy`; unit tests build one) publishes an ungated address straight to its container.

An address an earlier release published straight to its container is moved behind its front when the
dashboard starts, after the sweep and before the engine (`FrontTailnetAddresses`, from
`Server.startDeployEngine`): the front is written for the container the row's mapping reaches, verified,
the mapping moved to it and recorded, audited as `deploy.tailnet.fronted` (actor `system`). One whose
front cannot be written, verified, published or recorded keeps its mapping, its front taken away again,
and is audited as a failure with the reason; its next release puts the front there.

## The dashboard's session cookie

A browser scopes a cookie to a host name and never to a port. A dashboard served on the node's MagicDNS
name (`JD_SITE=<node>.<tailnet>.ts.net`, the Tailscale install) therefore has its session cookie sent to
every tailnet address — every pull request preview and tailnet-only deployment, served on that name's
ports 21000–21999 — and a dashboard on any other name has it sent to a deployment route serving that
name. The application cannot act as the dashboard with it in a browser (the API wants `X-JD-CSRF` and a
JSON body, and the cookie is `SameSite=Strict`), but its server would hold a bearer credential for the
dashboard, and a preview runs a pull request's code. Two measures keep the cookie to the dashboard:

| Where the application is served | What keeps the cookie from it |
| --- | --- |
| Any other name: a preview domain (`pr-{number}.preview.example.com`), a deployment's own domain, a sibling of the dashboard's name | The cookie is `__Host-jd_session` (`internal/sessioncookie`): `Secure`, `Path=/`, no `Domain`. A browser sends it to no other name and refuses one a sibling name sets, so another name can neither read it nor plant one. |
| A tailnet address, on the dashboard's own MagicDNS name | The address's front ([above](#the-gate)) takes the dashboard's cookies out of the request, gated or not, on nginx and on the gate Caddy. |
| A deployment route on the name the dashboard is reached by (`JD_SITE`, or a wildcard one label above it) | The route drops them (`DeploymentRoute.DropCookies`, set by `Service.WithDashboardHost` from `JD_SITE` in `onDashboardName`), on nginx and on the Caddy ingress, from the request to the application and to its sign-in check. A route on any other name, and a name that only redirects, are written as before. |

What is dropped is every name the dashboard has issued (`proxysvc.DashboardCookies`): `__Host-jd_session`,
`jd_session` (the name on plain loopback HTTP, where a browser refuses the prefix) and `vpsd_session`
(before 0.8.2); every other cookie is forwarded untouched, and a name inside another (`my_jd_session`)
is not matched. On Caddy two `header_up Cookie` replacements take out every occurrence on every Cookie
field. On nginx a chain of maps, one a name, takes out the first occurrence of each from `$http_cookie`
(nginx joins the fields) and the location sends what is left, nothing when nothing is; the browser
keeps a `__Host-` cookie once per name, so that is all of the current one, and a second copy under one
of the other names, with a longer path, is one the application set itself. The maps' captures share
the names `jd_ckr<n>`/`jd_ckh<n>`/`jd_ckt<n>`, because PCRE2 refuses a capture name longer than 32
characters and a site's own ident can be longer; a map's value is made the moment its own expression
matched, so the sharing is safe, and nginx accepts a capture defined by more than one expression.

Not covered: a browser lets an application on the same name *set* a cookie under the dashboard's name
(it keys cookies by name and host, not port), so it can sign the browser out of the dashboard or into a
session of its own — never read the dashboard's; a site the operator writes by hand on the Proxy page
for the dashboard's own name, which is theirs; and a tailnet without HTTPS certificates, where the
addresses are plain HTTP and a `Secure` cookie is never sent to them anyway.

## The whois route

`GET /api/v1/tailnet/whois?environment=<id>` (`internal/tailnetgate`, mounted by
`api/handlers_tailnet_whois.go`), with the tailnet address in `X-Tailnet-Peer`:

1. It answers only the proxy: the connection's own peer is loopback and nothing says another proxy
   forwarded it (`X-Forwarded-For`, `X-Real-IP`, `Forwarded`). The dashboard's own Caddy forwards
   browsers to the API from loopback too and always says so, so somebody who can open the dashboard
   cannot ask who holds an address; such a caller is answered `403 network_denied` before anything is
   looked up. The address travels in a header of its own rather than `X-Forwarded-For` so that the API's
   network allowlist, which runs first as for every route, judges the proxy's own loopback address.
   There is no session, as a webhook has none, and no API rate limit: every request to the deployment is
   one call here, and the cache below is what bounds what reaches tailscaled.
2. The list is the environment's live release's (`OrchestrationStore.LiveTailnetAllow`); none is a
   refusal.
3. It asks tailscaled's local API, `GET /localapi/v0/whois?addr=<ip>` over the host's socket
   (`/run/tailscale/tailscaled.sock` or `/var/run/…`, reached through PID 1's root,
   `tailnetgate.LocalAPI`), which answers the node and the user profile holding the address, or 404. A
   tagged machine's profile is tailscaled's shared `tagged-devices` placeholder, so it is admitted by
   its tags alone and never by a login.
4. 204 with `X-Tailnet-Login` (the login, or a tagged machine's tags) and `X-Tailnet-User` (the display
   name, or the machine's name; RFC 2047-encoded where not printable ASCII) when the list admits the
   peer; 403 when it does not, or when no device on the tailnet holds the address; **503** when
   tailscaled cannot be asked — never 403, so a daemon restarting is an outage and not a refusal.

Answers from tailscaled, and an environment's list, are kept five seconds: a page and its assets are
one question rather than forty, and a login removed from the list stops working within the same few
seconds of its release going live. An outage is not kept, so the gate opens the moment tailscaled is
back, and neither is an environment with no list: a first release moves its address to the gate a
moment before it goes live, and a refusal remembered from that moment would turn its first visitors
away. Every refusal is audited as `deploy.tailnet.refused` (actor `tailnet`, the environment as target,
the address as IP, and the reason — `caller`, `no_peer`, `not_gated`, `not_on_tailnet`, `not_listed`,
`unavailable` — with the login, name, node and tags it was about); one environment, address and reason
is recorded at most once a minute, the rest counted into the next record as `repeated`. An admitted
request is not audited: it is in the request record, with its login.

The application is told `X-Tailnet-Login` and `X-Tailnet-User`, set by the proxy from the dashboard's
answer and never from what the visitor sent (both proxies drop the visitor's own first), along with
`X-Forwarded-For`/`X-Real-IP` as the tailnet address. The visitor's cookies and `Authorization` header
are not sent to the dashboard.

## What the pages say

Settings → Domains offers **Tailnet only** beside the public domains, with the address the environment
has (or that it gets one on the next deployment), who may open it — each login or tag read as the
server reads it as it is typed — and which proxy checks them; a saved tailnet-only environment shows
that section alone. The project overview draws the address in the Domains node's place with who may
open it (`operations.tailnet`), and the summary's endpoint is the tailnet address. The Logs page names
the login a request was admitted as (`accesslog.Entry.User`), with the tailnet address behind it.
Preflight says the address the application will have and over which scheme, who opens it and through
which proxy (`tailnet_address`, `tailnet_identity`), and refuses a host on no tailnet
(`tailnet_unavailable`). Release comparison lists the publication as `tailnet`.

## Verification

Unit: `tailnetgate` — the real local API client against an httptest server speaking the whois shape on
a unix socket (`TestLocalAPIWhoIsReadsTailscaledsAnswer`, `TestLocalAPIWithoutTailscaledIsUnavailable`),
the decision (`TestAdmitsComparesLoginsWithoutCaseAndTagsExactly`, `TestCanonicalReadsLoginsAndTags`)
and the route (an allowed login, a denied one, a tag, an unknown address, tailscaled down answering 503,
callers that are not the proxy, the cache, the audit fold); `proxysvc` — both renderers
(`TestNginxTailnetGateAsksTheWhoisRouteWithTheClientAddress`,
`TestCaddyTailnetGateAsksTheWhoisRouteWithTheClientAddress`, `TestValidateTailnetGateHoldsEveryAddressToLoopback`),
the cookie on a gate and a front (`TestTailnetFrontDropsTheDashboardsCookie`) and on a route serving the
dashboard's name (`TestOnlyARouteOnTheDashboardsNameDropsItsCookies`,
`TestNginxRouteOnTheDashboardsNameForwardsCookiesWithoutItsSession`,
`TestCaddyRouteOnTheDashboardsNameForwardsCookiesWithoutItsSession`); `api` — the cookie's name and
attributes, the legacy name renamed on its first request and both cleared at sign-out
(`session_cookie_test.go`); `deploy` — plan validation and canonical form, allocation from the previews' range, activation with and
without a list (a front either way, and straight to the container only without the proxy's half), never
live without its gate, a failed gated publish pointed back at the gate and a failed fronted one at what
reached the predecessor, an address moved behind its front at start, leaving the tailnet, preview
inheritance, a preview domain refused, the operations section and preflight (`tailnet_apps_test.go`); `api` — the mounted route behind the allowlist and its audit
(`TestTailnetWhoisRouteAnswersOnlyTheProxyAndAuditsRefusals`) and the deploy key's refusal
(`TestADeployKeyCannotChangeWhoMayOpenATailnetOnlyDeployment`); `accesslog` — the login in both
formats; `uninstall` — the gate Caddy, its volumes and its Caddyfile go with the deployments.

Live: `go test ./internal/proxysvc -run TestLiveTailnetGateOnNginx` (the host's nginx from a private
prefix) and `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveTailnetGateOnCaddy` (as root: a
real `caddy:2-alpine` started by `ensureGateContainer` under a test name, removed with its volumes). Each
puts the real whois handler over the real local API client against a fake tailscaled, sends what
tailscaled's serve sends, and checks that an allowed login passes with the headers forwarded and the
visitor's own claims dropped, a tag passes, a login not listed and an unknown address get 403,
tailscaled down and the dashboard down get 503, the gate listens on loopback alone, and the request
record names the login. Both also put a front beside the gate and send every request with the
dashboard's session under its current and legacy names between two cookies of the application's: the
application is sent the application's two and nothing else, through the gate and through the front.
`go test ./internal/proxysvc -run TestLiveNginxRouteOnTheDashboardsNameDropsItsSession` (the host's
nginx) and `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyRouteOnTheDashboardsNameDropsItsSession`
(as root: a real `caddy:2-alpine` ingress in front of `traefik/whoami`) do the same for a deployment
route on the dashboard's own name, and its sign-in check on nginx, beside a route on another name that
forwards the cookies untouched.

Browser (`deploy-settings-b.spec.ts`, "Tailnet only"; `deploy-requests.spec.ts`): the switch, what it
takes away, login validation, the saved plan, the address and the list on the overview, and the login
in the request log.

Not verified here: a real tailnet, and a real browser's cookie jar. No tailnet is available on the
development host, and the host's own tailscaled is never asked; `tailscale serve` is exercised through
its recorded status shapes as for previews, and tailscaled's whois through its documented answer shape.
The cookie a browser sends is written by hand in the live tests, as a browser signed in on the same
name would send it.
