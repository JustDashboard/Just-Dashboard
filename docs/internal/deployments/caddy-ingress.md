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

## Certificates and application networking

Caddy owns issuance and renewal on its existing public listener. A deterministic, temporary site
requests one hostname's certificate before activation. Unchanged preparation files can resume after a
dashboard interruption and are removed before success. Certificate discovery checks hostname, validity,
public trust chain and matching key; an internal-CA certificate cannot satisfy public issuance. A private
import under `/etc/ssl/just-dashboard/caddy-*` supplies release evidence. Traffic continues using Caddy's
renewed certificate storage. These evidence copies refresh when issuing again; they are not a live
certificate-expiry measurement. The TLS scanner reports what a visitor actually receives.

Existing imported certificates use immutable, content-addressed private certificate/key copies in Caddy's
volume, so rotation cannot overwrite the keypair needed by a previous route snapshot. Automatic Caddy
orders currently support one non-wildcard hostname per deployment. Multi-name existing imported pairs
remain usable. Wildcards require DNS-01 credentials for a domain the operator controls.

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
not order a public production certificate. The deployment C5 live gate and deployment/API/Proxy race
tests remain required. The browser regression covers automatic Caddy HTTPS without a Certbot warning.

References: [Caddy commands](https://caddyserver.com/docs/command-line),
[Caddyfile imports](https://caddyserver.com/docs/caddyfile/directives/import), and
[the configuration API](https://caddyserver.com/docs/api).

## External routes recovered by existing-workload import

Recovered external routes use server-owned `existing_proxy_route` dependencies, separate from
managed deployment domains. Import only reads their authoritative hostname/path/service bindings;
it does not create managed routes, issue certificates or regenerate their authentication. Settings
retain those frozen links, and duplication, previews and managed deletion never acquire their
original proxy authority.

A stable published host port stays unchanged. A Docker alias requires the exact shared network and
one verified running owner. An intentionally stopped original can be captured and restored only
when direct inspection verifies its frozen container ID, retained network and alias; a replacement
still requires its own running owner. Static literal IP handoff changes only an isolated nginx
upstream directive or a conventional Caddyfile with one `reverse_proxy`, no imports or snippets.
Multiple nginx directives in the same file are journalled and changed together. Multiple stable
Caddy hostname/path/service routes are supported; shared, dynamic or conditional upstreams require
review. Adapted Caddyfile JSON must equal active local admin configuration. Conventional host Caddy
uses the local admin endpoint; custom or disabled admin layouts do not authorize replacement.

Private durable journals retain exact before/after bytes for failed reload, rollback and interrupted
writes. File-descriptor and pathname identities, expected bytes and post-write verification provide
bounded conflict detection while preserving the inode required by single-file Docker bind mounts.
Dashboard locking serializes dashboard operations; it cannot make an in-place write atomic against
an external writer after comparison. Operators must avoid concurrent edits during handoff, and
later unrelated bytes or replaced inodes are rejected rather than overwritten. An installed binary or stopped/unassociated nginx does not establish an existing route. Known
uninspectable active routes remain unverified deployment dependencies and block before runtime stop. Unassociated proxy
manager presence is a hint, not permission to take over its configuration.

`JD_DEPLOY_LIVE=1 go test -race ./internal/proxysvc -run '^TestLiveExisting' -count=1 -v`
uses only owned loopback nginx and Docker Caddy fixtures. It verifies per-path services, alias
ownership and stopped originals, static-IP handoff, TLS/auth/URI/WebSocket byte preservation,
single-file mount identity, failed reload compensation, rollback, active-only drift and conflicting
operator edits. It does not claim public certificate issuance or support arbitrary proxy managers.
