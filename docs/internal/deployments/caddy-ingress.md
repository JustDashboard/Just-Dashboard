# Decision: share the public Docker Caddy ingress

## Problem and decision

`docker-proxy` owning TCP 80 does not identify the HTTP server. Running standalone Certbot against
that socket fails; selecting another host port cannot move an authority's HTTP-01 request away from
public port 80. Issuing a certificate alone also leaves deployment activation unable to claim 443.

Proxy now resolves Docker's published port mappings and reuses a supported public Caddy container for
both certificate automation and deployment routing. When TCP 80 and 443 are unclaimed, the first
authorized deployment provisions `just-dashboard-ingress` using the same `caddy:2-alpine` image family
as the dashboard. Readiness/preflight only observes; it never installs or starts a service. The installer
also provisions missing Certbot dependencies for the existing host nginx/standalone certificate paths.

The dashboard's own private Caddy, network allowlist, authentication and private backend/frontend
publications are unchanged. The new public ingress serves deployment hostnames only.

## Ownership and persistence

- Discovery requires one running Caddy with wildcard public TCP 80 and 443 mappings, a bind-mounted
  `/etc/caddy/Caddyfile`, and writable persistent `/config` and `/data` mounts. Unsupported owners are
  left running. Docker inspection failures do not become permission to run standalone Certbot.
- The fresh-host container has an explicit ownership label, restart policy, a persisted Caddyfile at
  `/etc/just-dashboard/ingress/Caddyfile`, and `just-dashboard-ingress-config` and
  `just-dashboard-ingress-data` volumes. Restart checks its label and exact persistent storage.
- Integration appends one import to the existing Caddyfile, preserving its original site definitions
  and single-file bind-mount inode. Generated files under `/config/just-dashboard` are private and
  atomically replaced. The import remains after the last deployment is removed; an empty glob is valid.
- Before configuration changes, the saved Caddyfile's adapted JSON must match the active admin API
  configuration. Unsaved API-only edits are reported instead of overwritten. Caddy validates before
  reload. This is serialized within Proxy; operators should not edit the same managed files during
  activation.
- Route snapshots add an optional driver, container ID and digest of the ingress's persisted mount
  identity. Old nginx snapshots remain compatible. A recreated Caddy may restore a snapshot only when
  that storage identity matches. Apply, verification and removal failures restore and reload the old
  route; recovery verifies the restored bytes.

## Access logging

A managed route asks Caddy to record what it served, into `/config/just-dashboard/access/<route>.log`
inside the ingress's own persistent `/config` volume, `format json`, rolled by Caddy itself
(`roll_size 16MiB`, `roll_keep 4`, `roll_keep_for 336h`, `roll_uncompressed` so a rolled generation
can be read from an offset). The file is inside the container rather than on the host because the
ingress may be one the operator already owned, and adding a bind mount would mean recreating their
running web server to turn on a log. Reads go through one `docker exec` each, the same mechanism
route files use, addressing a generation by inode so a roll under the reader loses nothing. The record is removed with its route — it holds
client addresses. Host nginx routes have always written `/var/log/nginx/<route>.access.log`; only
this driver was silent. See
[`request-observability.md`](request-observability.md).

## Request body limit

A managed route carries the plan's `runtime.maxRequestBodyMb` as `request_body { max_size <N>MB }` when
the plan names one. Caddy itself sets no request-body limit, so a route whose plan names none writes no
directive and behaves as before; the host-nginx driver, whose own default is 1 MB, always writes
`client_max_body_size` (64 MB unless the plan says otherwise). A route written before the limit existed
keeps its bytes until the next activation rewrites it; the reconcile pass does not add the directive.
Because zero means different things on the two drivers, preflight's `request_body_limit`, release
comparison and the settings hints state it as "64 MB on nginx, no limit on Caddy" rather than one number.

## Redirects, response headers and allowed addresses

A route's redirecting names (`DeploymentRoute.Redirects`) are site blocks of their own after the
route's, holding the route's certificate handling and `redir <scheme>://<target>{uri} <code>` and
nothing else: no password, fence or request record, because they serve no application. Response headers
are a `header` block with `defer`, so each is set as the answer is written and replaces the
application's header of that name. nginx's `add_header` only appends, so its routes also
`proxy_hide_header` each name. Allowed addresses are `@outside not remote_ip …` and a 403 `respond`.
Caddy sorts a site's directives itself and puts `basic_auth` before `respond`, which would ask a refused
address for the password first, so a route with an allow list wraps its handling in a `route` block,
which runs in the order written. A route with none of these renders byte for byte what it did before
they existed, so no live route reads as changed after an upgrade.

## Sign-in

A route that signs in (`DeploymentRoute.ForwardAuth`) asks the auth server what nginx's `auth_request`
asks it on a Proxy site, so one check address answers on either proxy: `forward_auth` to the check
address's origin with `uri` its path, `header_up X-Original-URL {scheme}://{hostport}{uri}` and
`X-Original-Method`, and `Host` set to the auth server's own as nginx's `proxy_pass` sends it. A 2xx
copies the provider's user headers into `Remote-User` and `Remote-Email` (`copy_headers
X-Auth-Request-User>Remote-User …` for oauth2-proxy, Authentik's `X-Authentik-*` likewise); a 401 is
answered by a `handle_response` with `redir "<signIn>?rd={scheme}://{hostport}{uri}" 302`; anything
else, Caddy's own 502 when the server does not answer included, is refused. The two headers are removed
from the visitor's request first (`request_header -Remote-User`): Caddy 2.11's `forward_auth` drops
them itself when the server sends none, and removing them first holds on whichever Caddy the operator's
ingress runs. The handling is a `route` block, so the fence, the check and the application run in the
order written; the redirecting names' blocks carry none of it.

Caddy's loopback is its own container's, so a check address on `127.0.0.1` (or `localhost`) is found
the way the application is: the container publishing that port, joined over its network and asked at its
address there (`resolveForwardAuth`; it has to be `http://` with a port). Unlike the application's
publication, which the dashboard makes and always binds to loopback, an auth server's is the operator's,
so a port published on every interface (`9091:9091`) counts too, since it answers on loopback as well
(`publishedTarget` with `everywhere`). The renderer
refuses a loopback check address outright, so an unresolved one can never be written. The route records
that container as a second metadata line, `# Just Dashboard auth target: {…}`, and the lifecycle worker
repairs it as it does the application's: a Caddy recreated without the auth server's network, or an auth
container with a new address, is joined and rewritten within a pass, since without it every visitor is
refused. A check address anywhere else — a name or address Caddy can reach — is asked as written.
Activation and verification build the content through one function (`routeContent`), so what is
verified is what was written.

## Certificates and application networking

Caddy owns issuance and renewal on its existing public listener. A deterministic, temporary site
requests one hostname's certificate before activation. Unchanged preparation files can resume after a
dashboard interruption and are removed before success. Certificate discovery checks hostname, validity,
public trust chain and matching key; an internal-CA certificate cannot satisfy public issuance. A private
import under `/etc/ssl/just-dashboard/caddy-*` supplies release evidence. Traffic continues using Caddy's
renewed certificate storage. These evidence copies refresh when issuing again; they are not a live
certificate-expiry measurement. The TLS scanner reports what a visitor actually receives.

Existing imported certificates use immutable, content-addressed private certificate/key copies in Caddy's
volume, so rotation cannot overwrite the keypair needed by a previous route snapshot. Caddy orders and
renews one certificate per name, so a deployment with several non-wildcard names — the www. that
redirects to the bare domain — has each name prepared in turn, and the first name's copy is the
release's evidence. Activation then accepts, on a Docker Caddy host only, one Caddy copy per name in
place of one certificate covering them all (`caddyEvidenceForEach`): a Caddy route never reads that
path, while nginx would serve it for every name. Multi-name existing imported pairs remain usable.
Wildcards require DNS-01 credentials for a domain the operator controls.

The application remains published on loopback. Proxy resolves that publication to a running Docker
container and an internal network address, then connects Caddy to that network. Managed route comments
persist the exact candidate container ID, network, internal port and address. The lifecycle worker checks
every 30 seconds and repairs a lost Caddy network attachment or changed candidate address; the same pass
adds the access-log block to a managed route written before request recording existed, so an upgrade
turns recording on for every existing deployment within a tick rather than at its next deploy. It never
follows a host port to a replacement container. Repairs are audited as `proxy.ingress.reconcile` with
route name and success only. Read-only verification does not reconnect networks. Recovery can have up
to one polling interval of interruption after container recreation; missing/replaced candidates require
the normal deployment activation path. Network memberships remain available for other managed routes.

## Boundaries and verification

This adapter covers conventional Docker Caddy and Docker HTTP applications. Existing managed host
nginx uses the shared webroot adapter. Arbitrary Apache, Traefik, custom Caddy command/storage/admin
layouts, independently owned 443 listeners, host-network applications and inaccessible public DNS are
not silently taken over. Public validation still requires public DNS and reachable standard ports;
changing an application port cannot bypass those requirements.

`JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddy -count=1 -v` provisions isolated
containers on loopback test ports. It checks fresh provisioning, persisted restart, original-site
preservation, hostname collision refusal, private imported certificates, API-only configuration
protection, Caddy recreation/network repair, storage-identity fencing and snapshot restoration, and — since the reconcile pass upgrades a route written before request recording
existed — that the first pass after recreation both repairs the network and adds the access-log block
as one recorded repair, and that the next pass leaves the route alone. It does
not order a public production certificate.
`JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyServesRouteRules -count=1 -v`
checks what a real Caddy answers for a route's rules: the redirecting name's 308 keeps the path and
query, an address off the list gets 403, an allowed one gets the application with the route's header in
place of the application's own, and the inventory lists both names under the route.
`JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyGatesRouteBehindForwardAuth -count=1 -v`
puts a route that signs in in front of `traefik/whoami`, with a real Authelia 4.39 (one file user, SQLite)
published on loopback on a network of its own and a private certificate authority for the names. It
checks that the written route names Authelia's container address and its target rather than
loopback; that a visitor without a session gets 302 to the sign-in page with `rd=` set; that after
signing in at Authelia the application is told `Remote-User: alice` and `Remote-Email` even though the
request claimed another user; that the redirecting name answers 308 without asking; that a Caddy
disconnected from Authelia's network refuses everyone until one maintenance pass rejoins it; and that
with Authelia stopped the route refuses rather than passes. `TestLiveDeploymentRouteSignsInOnNginx`
checks the nginx spelling against the host's real nginx without Docker. The deployment C5 live gate and
deployment/API/Proxy race tests remain required. The browser regression covers automatic Caddy HTTPS without a Certbot warning.

References: [Caddy commands](https://caddyserver.com/docs/command-line),
[Caddyfile imports](https://caddyserver.com/docs/caddyfile/directives/import), and
[the configuration API](https://caddyserver.com/docs/api).
