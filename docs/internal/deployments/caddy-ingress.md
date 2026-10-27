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

A host whose Caddy publishes 80 and 443 on one address rather than every address, or an isolated
acceptance stack whose ingress is published on loopback, names the container instead:
`JD_INGRESS_CONTAINER` (`proxysvc.NewWithIngressContainer`). That container — inspected by name alone,
never as part of a listing a vanished neighbour can fail — must still be a running
`caddy run --config /etc/caddy/Caddyfile` with a bind-mounted Caddyfile and writable `/config` and
`/data`; only the public ports are not asked of it. A named ingress is the only one used: nothing is
provisioned beside it and nothing falls back to host nginx while it is missing, so a deployment that
needs a route fails rather than writing one somewhere else.

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
address for the password first, so a deployment's route wraps its handling in a `route` block, which
runs in the order written: the fence, the maintenance switch, the password or the sign-in, then the
application. A route asking for none of the rules carries the switch and nothing else of theirs.

## Maintenance and error pages

Every deployment route carries its maintenance switch, so maintenance can be turned on at once, from
the Domains settings, without a release and without the route being written again outside activation:

```
@jd_maintenance {
  file {
    root /config/just-dashboard/pages/<route>
    try_files /maintenance.on
  }
  not remote_ip <the plan's bypassFrom>
  not path /.well-known/acme-challenge/*
}
route @jd_maintenance {
  header Retry-After <retryAfter>
  root * /config/just-dashboard/pages/<route>
  rewrite * /maintenance.html
  file_server {
    status 503
  }
}
```

The `file` matcher looks for `maintenance.on` on every request, so writing that file into the
ingress (`docker exec`, as route files are written) turns maintenance on and removing it turns it off;
Caddy is not reloaded and the route's bytes do not change, so the switch neither meets a cutover's
snapshot and restore nor is undone by the next release. The `<route>` folder holds the route's pages
too, written with the shipped ones by activation when it has none and removed with the route. The
nameless route a certificate is issued through has no switch. The route's own error pages are a
`handle_errors` block, `@jd_page expression {err.status_code} in [...]` rewriting to
`/{err.status_code}.html` and served by `file_server`, which keeps the error's status, so Caddy's own
502 for an application that is down is still a 502 with the route's page. With `interceptErrors` the
`reverse_proxy` turns the application's answers with those codes into Caddy errors
(`@jd_error status …` and `handle_response @jd_error { error … {rp.status_code} }`), as nginx's
`proxy_intercept_errors` does. A password's 401 keeps its `WWW-Authenticate` under `handle_errors`.
A route written before the switch existed has no matcher, and the switch says so until its next
deployment; the reconcile pass does not add it.

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

Existing certbot lineages and imported certificates are served from immutable, content-addressed
private certificate/key copies in Caddy's volume (`/config/just-dashboard/certs/<sha256>.crt` and
`.key`, named after the certificate's bytes), so a rotated import or a renewed lineage cannot overwrite
the keypair a previous route snapshot names. Caddy orders and renews one certificate per name, so a
deployment with several non-wildcard names — the www. that redirects to the bare domain — has each
name prepared in turn, and the first name's copy is the release's evidence. Activation then accepts, on
a Docker Caddy host only, one Caddy copy per name in place of one certificate covering them all
(`caddyEvidenceForEach`): a Caddy route never reads that path, while nginx would serve it for every
name. Multi-name existing imported pairs remain usable.

### Wildcards

A wildcard plan name is served on this ingress exactly as on nginx. The ingress image is the floating
`caddy:2-alpine` tag (2.11.7 at the time of writing), which lists `tls.issuance.acme` and no
`dns.providers.*` module, so Caddy itself cannot answer a DNS challenge; and the provider's token is
kept for certbot and not handed to another program. So nothing asks Caddy to: the run proves the
wildcard — and the zone's apex when the plan has it — through certbot over DNS-01 with the credentials
saved on the Certificates page (`deployment_certificate_dns.go`, the same path nginx hosts take), the
lineage is copied into Caddy's volume at activation, and the route is a `*.zone` site (plus the apex)
with an explicit `tls <copy.crt> <copy.key>`, which keeps Caddy's automatic HTTPS from ordering for it.
A wildcard route without such a certificate is refused by the renderer rather than left for Caddy to
fail at. A name under the wildcard — a preview — reuses the lineage (`ResolveDeploymentCertificate`
finds it) and gets a site of its own beside the wildcard's: a site claims the name it is written for,
not the names under its wildcard (`domainClaimed`), since Caddy answers from its most specific site as
nginx does from an exact `server_name`. Preflight and the Domains sheet ask the same question they ask
on nginx — which saved provider proves it, `certificate_automatic` naming it, or
`certificate_wildcard_unavailable` with where to save one — and no longer refuse for the ingress.

### Renewal

A route that serves a copy records where the copy came from, as a metadata line after the target's
(`# Just Dashboard certificate: {"certPath":…,"keyPath":…}`, the lineage's `live/` paths or the
import's). certbot renewing the lineage changes nothing Caddy reads, so the dashboard moves the routes
on (`docker_caddy_copies.go`): the renewed pair is copied under its own hash, every `tls` line of the
route and its redirecting names is rewritten to it, and Caddy is reloaded — a graceful reload keeps the
listener and finishes requests in flight. Copies stay content-addressed rather than being replaced in
place for two reasons: a snapshot taken before the renewal restores exactly the bytes it names, and a
changed path is a changed configuration, which `caddy reload` applies, while a reload of an unchanged
Caddyfile is a no-op that would have left the old certificate loaded. The dashboard's own renewal and
issuance jobs refresh at once after certbot has run (`RefreshIngressCertificates`, reported in the job
beside the nginx reload; an ingress that cannot be inspected is a status line, a refresh the ingress
refused fails the job). The lifecycle worker's 30-second pass does the same for what certbot's timer
renewed, so no deploy hook has to reach back into the dashboard, and the same pass records the source
for a route written before this existed — found by hashing the host's lineages, their archived
generations and the imports against the copy — so an upgrade turns refreshing on for every existing
route within a tick. A copy no route names any more is removed once it is older than an activation
could still be holding a snapshot of (an hour). A rollback does not depend on any of this: a retained
release's route is rendered again from the lineage's current files at activation.

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
checks the nginx spelling against the host's real nginx without Docker.
`JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyServesMaintenanceAndErrorPages -count=1 -v`
switches a real Caddy's route by its file alone: a visitor gets the maintenance page with a 503 and the
route's `Retry-After`, the application answers again once it is off, the switch is still on after the
route is written again by the next release, an address let past reaches the application while a
visitor from another container on the network gets the 503, the application's own 404 gets the route's
page, and with the application stopped Caddy's 502 gets the page the operator wrote.
`TestLiveDeploymentRouteMaintenanceOnNginx` checks the same on the host's real nginx without Docker,
the ACME challenge served through maintenance included.
`JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyServesAWildcardProvedOverDNS01 -count=1 -v`
is the wildcard journey on this ingress against a Pebble that really validates DNS-01 in a BIND
(`proxysvc/acmetest`; it needs certbot's `dns-rfc2136` plugin, as the nginx one does): the run proves
`*.preview.jd-live.test` and the apex through certbot, a real `caddy:2-alpine` used by name serves both
and a preview's own route beside them with the lineage's serial, chaining to that Pebble's root, with
nothing ordered into Caddy's own storage; then `certbot renew --force-renewal` and
`RefreshIngressCertificates` move both routes to the renewed serial, prune the old copy, and leave each
route what activation would write.
`JD_DEPLOY_LIVE=1 go test ./internal/proxysvc -run TestLiveDockerCaddyRefreshesRenewedCertificateCopies -count=1 -v`
is the renewal refresh on a real Caddy without an authority: a lineage laid out as certbot keeps it is
renewed under the route, the lifecycle pass moves the route on and Caddy serves the new serial, a route
written without a recorded source is found through the archive and refreshed the same way, a snapshot
restored after a renewal still finds its copy within the grace and is moved on by the next pass, and
only the copies nothing names are removed. The deployment C5 live gate and
deployment/API/Proxy race tests remain required. The browser regression covers automatic Caddy HTTPS without a Certbot warning.

References: [Caddy commands](https://caddyserver.com/docs/command-line),
[Caddyfile imports](https://caddyserver.com/docs/caddyfile/directives/import), and
[the configuration API](https://caddyserver.com/docs/api).
