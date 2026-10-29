# Cloudflare Tunnel as an ingress

A host behind NAT, on a home connection or with no public address can still serve deployments: the
dashboard runs Cloudflare's connector, cloudflared, as a container of its own that dials out to
Cloudflare's edge, and a deployment domain marked **through Cloudflare Tunnel** is answered by
Cloudflare and brought to this host's proxy over that connection. Nothing listens on a public address
for it and no port is opened. Cloudflare terminates the visitor's HTTPS, so nothing is issued here for
such a name, and the proxy serves it over plain HTTP on a private Docker network that only the
connector shares with it.

Code: `cftunnel/` (the Cloudflare API client, the ingress configuration, the connector's driver and the
manager that keeps them in agreement), `proxysvc/tunnel_origin.go` (where the connector reaches the
proxy, and the saved Cloudflare credentials), `deploy/tunnel.go` (the plan field's effects: preflight,
the certificate step, routing at activation, the live releases' tunnel names),
`api/handlers_proxy_tunnel.go` (the routes, the audit, the host alert's reading),
`store/schema_cloudflare_tunnel.go`, and the Proxy › Tunnel page (`components/proxy/tunnel-page.tsx`).

## Connecting a tunnel

One tunnel per dashboard, stored in `proxy_cloudflare_tunnel` (a single row, `CHECK (id = 1)`), in one
of two modes:

- **A pasted token** (`PUT /proxy/tunnel {token}`, mode `token`). The operator creates a
  remotely-managed tunnel in Cloudflare's dashboard (Zero Trust › Networks › Tunnels) and pastes its
  token, or the whole install command it is shown in — `cloudflared service install eyJ…`, `docker run
  … --token eyJ…`: the page takes the last word that is one (`tokenFromPaste`). The server reads the
  account and tunnel IDs out of the token (`ParseToken`: base64 of `{"a","t","s"}`, the tunnel ID a
  UUID, the secret present) and refuses anything else before a container is started.
- **Created through the API** (`POST /proxy/tunnel/create {name, accountId?}`, mode `api`). The Cloudflare
  credentials saved under Certificates for the DNS-01 plugin (`/etc/letsencrypt/jd-dns/cloudflare.ini`:
  `dns_cloudflare_api_token`, or the e-mail and global key) create a remotely-configured tunnel
  (`POST /accounts/{id}/cfd_tunnel`, `config_src: cloudflare`) and fetch its token
  (`GET …/cfd_tunnel/{id}/token`). The account is the one the credentials reach; when they reach several
  the refusal lists them and the request names one. The token needs **Account › Cloudflare Tunnel ›
  Edit** beside the DNS plugin's **Zone › DNS › Edit**, and the page says so when it is missing.

Either way the token is sealed with `auth.Sealer` as it arrives and only the sealed form is stored
(`token_enc`). It is never returned by any route, never logged, and never put on a command line: the
connector is started with `docker run -e TUNNEL_TOKEN` (the name without a value, so the value comes
from the `docker` process's own environment), and its container carries a 16-character digest of it as a
label so a changed token is noticed without reading the environment back. Docker keeps a container's
environment in its configuration, so root on the host can read it with `docker inspect`, as it can
read the sealing key; nothing below root can.

`DELETE /proxy/tunnel` removes the connector and forgets the token; the `just-dashboard-tunnel` network
stays, with the ingress on it, for the next connection. The tunnel, its routes and the
records made for it stay at Cloudflare for their owner to remove there: a tunnel another server also runs
a connector for is not this dashboard's to delete, and a removed record would take the name off the
internet even if the operator meant to reconnect at once.

## The connector

cloudflared runs as `just-dashboard-cloudflared` from `cloudflare/cloudflared:2025.10.0` pinned by
digest, on the network `just-dashboard-tunnel`, as a managed container — never as a process on the
host — with `--restart unless-stopped`, `--memory 256m`, `--security-opt no-new-privileges`, no
published port and the command `tunnel --no-autoupdate --metrics 0.0.0.0:20241 run`. Its metrics server
answers on its address on the tunnel network only; the dashboard reads `/ready` there
(`{"status":200|503,"readyConnections":N}`), which is cloudflared's own word on whether it holds a
connection to the edge.

Where it sends requests (`proxysvc.TunnelOrigin`):

- **The Docker Caddy ingress** is joined to the tunnel network (`docker network connect`, a no-op
  when it already is) and asked by its container name: `http://just-dashboard-ingress:80`.
- **This host's nginx** is reached through Docker's gateway: the connector gets
  `--add-host host.docker.internal:host-gateway` and asks `http://host.docker.internal:80`.

Port 80 either way, plain HTTP. cloudflared keeps the visitor's `Host`, so the proxy picks the route by
name exactly as it does for a visitor at the door. An origin that is HTTPS (`https://…`) is rendered with
`noTLSVerify` and `originServerName` set to the name, so a Caddy internal certificate would be accepted;
nothing uses that today.

The manager's pass (`Manager.Start`, every 30 seconds, and once at each verb) keeps the connector as the
connection says: started when missing, `exited`, `created` or `dead`, run again when its token digest or
its origin no longer match, and left alone when a container of that name carries no label of this
dashboard's (`foreign`, said on the page). The same pass rejoins a recreated ingress to the tunnel
network. Each start and failed start is audited as `proxy.tunnel.connector`.

## A deployment domain through the tunnel

A plan's domain carries `tunnel: true` (stored only when set, so every other domain keeps its digest).
The Domains settings draw it as a third toggle beside HTTPS and Password, and the Add domain sheet as
**Serve through Cloudflare Tunnel**, which asks `GET /deploy/hostname?hostname=…&tunnel=1`: whether a
tunnel is connected and which, in place of the certificate and public-address questions.

- **Validation.** A wildcard is refused (`domains[i].tunnel`): each name is a route and a record of its
  own at Cloudflare. `runtime.allowFrom` is refused while any name goes through the tunnel
  (`runtime.allowFrom`): every request reaches the route from the connector's address, so the list
  would refuse every visitor or, with the connector listed, let all of them in. Fence such a name with
  Cloudflare Access or a WAF rule at Cloudflare.
- **Canonical form.** A tunnel domain is HTTPS, since Cloudflare's edge is: the page turns HTTPS on with
  it, and `canonicalConfiguration` does the same for an API client that did not.
- **Preflight** replaces the public-IP DNS check and the certificate findings for it with one finding:
  `tunnel_domain` (pass, naming the tunnel) or `tunnel_unavailable` (blocked: no tunnel is connected),
  both with a link to Proxy › Tunnel.
- **The certificate step** leaves the tunnel's names out; a release whose only names go through the
  tunnel issues nothing.
- **The route** serves the name over plain HTTP (`PlainHostnames`) beside any name served over HTTPS
  here, and does not redirect it to HTTPS: the connector speaks HTTP to it, and the visitor's redirect
  to HTTPS is Cloudflare's to make.
- **Activation** routes the release's tunnel names (`Manager.Route` with them as `extra`) after the
  route is applied and before the public-route checks, so a `public_route` check — which asks
  `https://<name>/` through public DNS, so through Cloudflare's edge and the tunnel — finds them routed.
  A tunnel that cannot be set (Cloudflare refused, no credentials) is a line in the transcript and does
  not fail the release; the pass tries again. A required public-route check through a tunnel that is
  down does fail the release and rolls it back, as it would for a route that is down.

## Routes and records

Each pass renders the tunnel's ingress rules from every live release's tunnel names
(`OrchestrationStore.TunnelDomains`, read from the releases' own snapshots, not from saved plans) plus a
release about to go live (`cftunnel.Render`): one rule per name to the origin, sorted and without
duplicates, and the catch-all `http_status:404` Cloudflare requires last. With credentials the rules are
sent (`PUT /accounts/{a}/cfd_tunnel/{t}/configurations`) only when their digest differs from the one last
applied (`applied_digest`), audited as `proxy.tunnel.routes`. Without credentials — a pasted token and
nothing saved under Certificates — the rules are shown on the page as the public hostnames to add in
Cloudflare's dashboard, each with its service.

Each name's record is a proxied `CNAME` to `<tunnel-id>.cfargotunnel.com` in the Cloudflare zone that
holds it (`ZoneFor` walks the name's suffixes). `EnsureCNAME` creates it (`created`), leaves one already
pointing there (`present`), proxies one that was not, or moves one this dashboard made for another
tunnel (`updated`) — it knows its own by the comment `Just Dashboard: routed through Cloudflare Tunnel`.
Any other record of the name — the operator's own A record, a CNAME elsewhere — is never replaced; the
refusal names it (`www.example.com already has a A record to 203.0.113.7 in Cloudflare, which is not this
dashboard's; …`). A name's answer is kept for 30 minutes, an error is asked again on the next pass, and
each change and each new error is audited as `proxy.tunnel.dns`. Without credentials every name reads
`manual`, with the record to create. Records are never deleted: a name leaving the tunnel keeps its
record until its owner removes it, and the tunnel's catch-all answers 404 for it meanwhile.
The Domains settings' DNS record sheet writes the same record on demand — before a release, or after
the pass was refused — through `POST /deploy/{id}/domains/{domain}/dns-record`, with the same comment, so
the pass and the sheet each know it as theirs ([DNS records](dns-records.md)); a tunnel's name whose zone
another provider holds is refused there rather than written where it would answer nothing.

Cloudflare's refusals are carried in its own words: the v4 envelope's `errors[].message` and `code`,
as `Cloudflare refused to <what was asked>: <message> (code N)` (`cftunnel.Error`), which the routes
answer as `502 cloudflare_refused`; the dashboard's own refusals (a malformed token, no credentials,
several accounts) are `422 tunnel_refused`.

## Health, the page and the alert

`GET /proxy/tunnel` is the last pass's reading with the connector inspected again, so a container
stopped by hand reads as stopped at once. The page leads with the tunnel, its edge connections, the
names routed and the origin; then, when the connector holds no connection, why in cloudflared's words —
the last `ERR` line of its log, or the bare line it prints before its logger starts (`Provided Tunnel
token is not valid.`), with anything token-shaped redacted; then each name with its project, whether
the tunnel's rules hold it, and its record; then the connector's container, version and address, and
that it publishes no port.

The host alert kind **Tunnel down** (`tunnel_down`, on the host Alerts page with the other host kinds)
fires when the connector is not running, or is running and holds no connection to the edge, for the
rule's minutes (2 by default, read twice before anybody is told, so a reconnect after an edge blip is not
an alert). Like every host kind it tells its channels once when it starts firing and once when it ends,
with cloudflared's own words in the detail. With no tunnel connected the rule is unjudged rather than
quiet.

## Routes

| Route | Gate | Audit |
| --- | --- | --- |
| `GET /proxy/tunnel` | any signed-in account | — |
| `POST /proxy/tunnel/sync` | `system.admin` | `proxy.tunnel.sync` |
| `PUT /proxy/tunnel` | `system.admin`, a session | `proxy.tunnel.connect` |
| `POST /proxy/tunnel/create` | `system.admin`, a session | `proxy.tunnel.create` |
| `DELETE /proxy/tunnel` | `system.admin`, `destructive` | `proxy.tunnel.disconnect` |

Connecting takes a credential, so an API key cannot; none of these are deployment routes, so no deploy
key reaches them. What the manager does on nobody's request is audited as `system`:
`proxy.tunnel.connector`, `proxy.tunnel.routes`, `proxy.tunnel.dns`.

## Verification

- `go test ./internal/cftunnel` — the ingress rendering (order, duplicates, the catch-all, an HTTPS
  origin, wildcards and bad names refused), the token's parsing and digest, reading cloudflared's
  complaint out of its log, and the API client against an `httptest` fake of Cloudflare's v4 API that
  answers in Cloudflare's envelope: creating a tunnel and its token, the configuration, the zone lookup,
  each CNAME outcome, a foreign record refused untouched, and errors in Cloudflare's words (`You already
  have a tunnel with this name. (code 1013)`, `Invalid request headers (code 6003)`). The manager's tests
  run a created tunnel and a pasted token without credentials against the same fake and a fake
  connector: sealed storage, an unchanged pass sending nothing, a release's name routed before it is
  live, and the manual records.
- `go test ./internal/deploy -run Tunnel` — the plan field, its validation, the route, preflight's
  findings and the activation's transcript line.
- `JD_DEPLOY_LIVE=1 go test ./internal/cftunnel -run TestLiveConnectorReportsCloudflaredsOwnFailure`
  (root, Docker) starts the real pinned cloudflared the way the dashboard does, with a token of the right
  shape for a tunnel that does not exist: it is running on its own network with no published port and the
  token absent from its command, its metrics say 503 with no connection, and the page's reason is
  cloudflared's own log line (`Serve tunnel error error="control stream encountered a failure while
  serving"` against the real edge at the time of writing); a token that is not one is refused by
  cloudflared itself (`Provided Tunnel token is not valid.`) and read in those words; disconnecting
  removes the container.
- `JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyServesATunnelNamePlainToItsConnector`
  (root, Docker) joins a real Caddy ingress to a tunnel network and asks it from a container on that
  network alone, as cloudflared would: the tunnel's name gets the application over plain HTTP, and the
  HTTPS name on the same route still redirects to HTTPS.

A real tunnel needs a Cloudflare account and a zone, which the tests do not have: no test carries a
request from Cloudflare's edge through a tunnel, and the API client is proven against the fake, not
against Cloudflare. The nginx origin through `host.docker.internal` is not exercised live, since the test
host's ports 80 and 443 belong to the running dashboard.

## Limits

- **The visitor's address.** Every request reaches the proxy from the connector, so the access log,
  the request readings and the application see the connector's address; Cloudflare sends the visitor's
  in `Cf-Connecting-IP` and `X-Forwarded-For`, which the routes do not trust (see the `allowFrom`
  refusal above).
- **One tunnel per dashboard**, and one connector for it. Cloudflare supports several connectors per
  tunnel for redundancy; a second server running one for the same tunnel works, but this page reads only
  its own.
- **Remotely-managed tunnels only.** A locally-configured tunnel (a `config.yml` with credentials file)
  is not read; its token-based equivalent is what Cloudflare's dashboard creates today.
- **Plain HTTP on the private network.** The hop between the connector and the proxy is unencrypted, on a
  Docker network on this host that only the connector and the ingress share.
