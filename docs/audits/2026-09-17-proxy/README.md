# Proxy & TLS audit and competitor comparison

**Audit date:** 2026-09-17 UTC. **Repository:** Just Dashboard, branch `patch/0.6.7`.
**Scope:** the six pages under `/proxy`, `backend/internal/proxysvc`, `handlers_proxy*.go`, and the
Docker Caddy ingress as it appears on those pages. Deployment routing has
[its own audit](../2026-09-16-deployments/README.md) and is not repeated here.

This report answers four questions: what the proxy section demonstrably does, what was wrong with it
under the hood, what competing products offer, and what to build next. It was produced alongside the
0.6.7 redesign of the pages; the fixes and features it lists as **Shipped** were made in the same change
and are covered by the tests named below. Competitor features are **documented**, from each product's
current documentation and source, not installed and acceptance-tested here.

## Verdict

**The proxy section already covers more than Nginx Proxy Manager on the questions that matter for one
server, and less on the ones that only matter with many.** A domain in front of a port, with TLS,
HTTP/2, WebSockets, access control, basic auth and extra paths, rendered on the server as ordinary nginx
that can be committed and edited by hand; streams for the services that do not speak HTTP; certbot
driven from the page including wildcards over eight DNS plugins; imported certificates checked against
their key; a TLS grade that reads what a visitor actually gets rather than a file. No competitor in the
self-hosted panel class checks the renewal *timer*, reads the live handshake of a watched domain, or
grades the deployed configuration.

What it lacked was the layer above the files: whether the engine was running, what needed doing, which
site a certificate belonged to, and a way from a finding to its fix. Those are shipped now. What it still
lacks, against the field, is what is listed under [Gaps](#gaps-ranked): per-site rate limiting and a
maintenance switch, a static asset cache, certificate expiry notifications through the channels the
deployment engine already has, and a site builder for Caddy hosts.

### What "confirmed" means here

| Label | Meaning |
| --- | --- |
| **Shipped** | Implemented in this change, with the test that covers it named. |
| **Component** | Existed before this change; covered by unit or browser tests with fake hosts. |
| **Live** | Exercised against a real nginx/certbot in an earlier live test in this repository. |
| **Partial** | Implemented, with a reproduced defect or a significant unsupported case, described. |
| **Absent** | No implementation found. A source finding, not a claim about unpublished work. |

## Defects found under the hood

Each was reproduced by reading the code path, then fixed with a regression test in
`backend/internal/proxysvc/proxy_edge_test.go`, `sites_parse_edge_test.go` or
`frontend/tests/browser/proxy-ui.spec.ts`.

| # | Defect | Effect | Fix |
| --- | --- | --- | --- |
| 1 | `ParseSiteSpec` read a line, and a `location / { proxy_pass http://x; }` line is an opener followed by statements it never saw. | A compact hand-written site loaded into the form with **no upstream**, and saving it wrote a site that proxied nowhere. | `splitInline` breaks a line into statements before the reader sees it, respecting quotes so a `Content-Security-Policy` value's semicolons stay in the value. `TestSplitInlineStatements`, `TestParseSiteSpecKeepsLoggingOnForHandWrittenFiles`. |
| 2 | `listen` was read as TLS when the value *started with* `443` or *contained* `ssl`. | `listen 4430` and `listen 127.0.0.1:8443` loaded as TLS sites demanding a certificate; `listen 443 ssl http2` (the pre-1.25 form) loaded as HTTP/2 off, and saving turned it off for real. | `listenIsTLS` reads the address's port and the `ssl` parameter; `http2` in `listen` is honoured. `TestListenIsTLSReadsTheAddressAndTheParameters`, `TestParseSiteSpecReadsOldStyleHTTP2`. |
| 3 | A file with no `access_log` line parsed as logging **off**. | The first save from the form wrote `access_log off;` into a hand-written site that had been logging all along. | Unmanaged files without the directive round-trip as on; managed files keep the form's own choice. |
| 4 | `caddySites` treated every block opener as a site address. | `handle`, `header`, `tls` and `encode` appeared as server names of every Caddyfile that used them. | `parseCaddyfile` tracks brace depth; only top-level blocks are addresses, and comma-separated addresses are split. `TestParseCaddyfileReadsOnlyTopLevelAddresses`. |
| 5 | `SetVHostEnabled(…, true)` returned success when *any* `sites-enabled` entry existed, whatever it pointed at. | A stale or dangling link left the switch saying "on" while nginx read nothing. | Enabling goes through `linkEnabled`, which replaces a link pointing elsewhere. `TestSetVHostEnabledReplacesAStaleLink`. |
| 6 | `GET /proxy/config` (held by every signed-in account) accepted any path under the nginx directory, including `jd-auth/*` and `.htpasswd`. | A read-only account could fetch bcrypt hashes for every basic-auth login, which are crackable offline. | `ReadConfig` refuses password files with `ErrProtectedFile` → 403 `protected_file`. `TestReadConfigRefusesPasswordFiles`. |
| 7 | `nginx -t` passes a file it does not include, and `Validate` said "valid" about a disabled site's candidate. | A dry run on a disabled site was a test of nothing, reported as a pass. | `ValidationResult.Note` names the reason when the file is outside the include tree; the editor shows it. `TestIncludeNoteSaysWhenTheTestCouldNotSeeTheFile`. |
| 8 | A shared Docker Caddy route has no file on the host, and the Sites table keyed rows on `path` and offered "Edit the raw config" on every row. | Every such route shared the key `""` (React collision) and its editor opened on a 400. | Rows key on kind and name; verbs are declared once in `site-verbs.tsx` and the raw editor is offered only where there is a file. Browser test "a Docker Caddy route is listed without an editor it cannot use". |
| 9 | A closed-and-reopened "New site" or "New stream" form kept the previous draft. | A second site inherited the first one's fields. | The form remounts per open (`session`). |
| 10 | The overview's "Needs attention" listed every socket bound to a wildcard address. | sshd and nginx on every server made the list permanently non-empty, so it said nothing. | `attention.ts` folds the conditions an operator acts on (below), with the database-port catalogue the stream form already used. Browser test "the overview's attention list is folded from conditions worth acting on". |
| 11 | A certificate found at `/etc/nginx/ssl/site.crt` was named "ssl". | Two certificates in the same directory were indistinguishable in the list. | `certificateName` names a file in a generic directory after itself. `TestCertificateNameForGenericDirectories`. |
| 12 | The Issue dialog defaulted to the nginx authenticator on hosts without nginx. | certbot failed at runtime with a plugin error. | The default follows `/proxy/status`; the nginx method is offered only where nginx is. |

Not changed, and worth knowing: `ListVHosts` returns an error, and the Sites page an error state, when Docker
ingress discovery fails. That is deliberate in the deployment engine's ownership model (a listing must
not claim a route it cannot see) and is left as is.

## Capability map

| Capability | Status | Evidence |
| --- | --- | --- |
| Engine detected with version (nginx, Caddy, Docker Caddy ingress) | Component | `Availability`, `TestParseNginxVersion` |
| Engine unit state on the overview; test config, reload, restart, start, stop | **Shipped** | `engine.tsx`, `POST /proxy/test`, `/systemd/{unit}` routes; browser test "the reverse proxy is named once" |
| Site builder: proxy, static, redirect; TLS, force HTTPS, HSTS, HTTP/2, WebSockets, gzip, probes, headers, body limit, timeout, allow/deny, basic auth, extra paths, custom block | Component / Live | `sites_render.go`, `sites_test.go`; live in the cutover suite |
| Server-rendered preview with warnings | Component | `handleSitePreview` |
| Round-trip of hand-written files | **Shipped** (was Partial) | defects 1–3 |
| Enable/disable (sites-available layout), conf.d layout without a toggle | Component / **Shipped** | defect 5 |
| Raw config editor with validate, save, save-and-reload, discard, include note | **Shipped** (was Component) | defects 6–7 |
| Site verbs as words: edit, raw, open, TLS report, access log, duplicate, enable/disable, delete | **Shipped** | `site-verbs.tsx` |
| Password files (bcrypt in process, worker-readable mode) | Component | `htpasswd_test.go` |
| Streams (TCP/UDP, PROXY protocol, timeout, allow list) with include detection | Component | `streams_test.go`; browser tests |
| certbot: issue (nginx, webroot, standalone, DNS), renew, dry run, force, revoke, streamed as jobs | Component | `certbot_test.go`, `import_test.go` |
| Wildcards over eight DNS plugins with saved credentials | Component | `dns01.go` |
| DNS credential state and removal | **Shipped** | `DELETE /certificates/dns-credentials/{provider}` |
| Renewal timer detection | Component | `renewalScheduled` |
| Renewal timer switched on from the page | **Shipped** | `RenewUnit`, `RenewalNotice`; browser test "the certificates page offers to turn the renewal timer on" |
| Staging issuance handed over to the real one | **Shipped** | `certs-panel.tsx` |
| Certificate import with key match and chain check | Component | `import_test.go` |
| Installed certificates with which site uses each | **Shipped** | `TestListCertificatesNamesTheSitesThatUseEach` |
| Watched domains checked by live handshake, with a port | Component / **Shipped** | `handlers_domains.go` |
| TLS report: grade, protocols, chain, trust, key, OCSP, redirect, HSTS, headers; opened from a site or certificate | Component / **Shipped** | `tlsscan_test.go`; `?domain=` |
| DNS check for a domain, Cloudflare and provider-NAT aware | Component | `dns_test.go` |
| Listening ports with owner, filters, database catalogue, process link | Component / **Shipped** | `ports.go`; browser test "the ports page filters" |
| Attention list folded from certificates, renewal, sites, streams and ports | **Shipped** | `attention.ts` |
| Caddy site builder | Absent | Caddy hosts get the raw editor only |
| Per-site rate limiting | Absent | needs a zone in the `http` context |
| Maintenance mode / custom error page per site | Absent | |
| Static asset caching rule | Absent | |
| Certificate expiry notifications through channels | Absent | the deployment engine's channels exist, unwired |
| nginx request metrics (`stub_status`) | Absent | |
| Access log summary per site (status codes, top paths) | Absent | the log viewer opens the file; nothing aggregates it |

## Competitor comparison

Products a single-server operator would compare this section with, from their current documentation.

| Capability | Just Dashboard | Nginx Proxy Manager | Nginx UI | Zoraxy | Caddy (Caddyfile) | CloudPanel / HestiaCP |
| --- | --- | --- | --- | --- | --- | --- |
| Domain → port with TLS in one form | Yes, rendered as editable nginx | Yes, opaque config | Yes, config-first | Yes | By hand, automatic HTTPS | Yes |
| Let's Encrypt HTTP-01 | Yes (nginx, webroot, standalone) | Yes | Yes | Yes | Automatic | Yes |
| DNS-01 / wildcards | 8 plugins | Many plugins | Many providers | Some | Modules | Cloudflare (CloudPanel) |
| Staging-first issuance | Yes, with handover | No | No | No | n/a | No |
| Renewal timer detected **and repaired** | Yes | n/a (own scheduler) | n/a (own scheduler) | n/a | n/a | n/a |
| Import bought certificates, key checked first | Yes | Yes (no check) | Yes | Yes | By hand | Yes |
| Which site uses a certificate | Yes | Yes | Partial | Yes | n/a | Yes |
| Live TLS grade of what a visitor gets | Yes | No | No | No | No | No |
| Live handshake watch of any domain | Yes | No | No | Uptime monitor | No | No |
| Streams (TCP/UDP) | Yes, with include detection | Yes | Yes | Yes | Layer-4 app | No |
| Access lists / basic auth | Per site, files managed | Reusable access lists | Per site | Per rule, with GeoIP | By hand | Per site |
| Extra paths / custom locations | Yes | Yes | Yes (config) | Virtual directories | By hand | Yes |
| Rate limiting | **No** | No | Config only | Yes | Plugin | No |
| Maintenance page | **No** | No | No | Yes | By hand | No |
| Static cache rule | **No** | "Cache assets" toggle | Config only | No | By hand | Per site |
| Raw editor with validation | Yes, validated and rolled back | Advanced tab, unvalidated | Yes, validated | No | n/a | Yes |
| Engine status and control | Yes | No | Yes | Yes | n/a | Yes |
| Request metrics | **No** | No | Yes (stub_status) | Yes | Metrics endpoint | Partial |
| Access log per site | Opened in the log viewer | No | Yes | Yes | By hand | Yes |
| Notifications on expiry | **No** | Email via certbot | Yes | No | n/a | Email |
| Listening ports inventory | Yes, with owner and database catalogue | No | No | No | No | No |
| Multi-server | No | No | Yes (nodes) | No | n/a | No |

## Gaps, ranked

Ordered by what a single-server operator reaches for next, weighted by how much of each already exists
in the codebase.

1. **Certificate expiry and renewal-failure notifications.** The deployment engine already delivers to
   Discord, Slack, Telegram, e-mail and signed webhooks (`deployment-notifications.tsx`). A daily check
   of `ListCertificates` and `renewalScheduled` posting to the same channels is a small job on top of
   existing code and closes the one gap every competitor's e-mail covers.
2. **Per-site rate limiting.** `limit_req_zone` must live in the `http` context, which a site file cannot
   reach; but `conf.d/*.conf` *is* included from `http`, so the dashboard can own one
   `conf.d/jd-limits.conf` of zones and render `limit_req zone=… burst=…` into the site. A `RateLimit`
   field on `SiteSpec` with the same round-trip test the other fields have.
3. **Maintenance switch per site.** A `Maintenance bool` that renders `return 503` with an inline page,
   toggled from the site's verbs and shown as a state on the row. Cheap, and the thing people do by hand
   at 2 a.m.
4. **Static asset cache rule.** NPM's "Cache assets" toggle: a `location ~* \.(css|js|png|…)$` block with
   `expires` and `Cache-Control`, which for a proxy site also needs the same `proxy_pass`. One field, one
   renderer function.
5. **Access log summary per site.** The log viewer already opens `/var/log/nginx/<site>.access.log`; a
   `GET /proxy/sites/{name}/traffic` that tails the last N lines and counts status classes, top paths and
   top clients would give the Sites row a reading — requests in the last hour, error rate — that the
   `stub_status` module cannot give per site.
6. **Reusable access lists.** NPM's one structural advantage: a named list of addresses and logins
   applied to several sites. The site form's allow/deny and password file are per site; a saved list
   would be a small table and a `Select` in the form.
7. **Caddy site builder.** Hosts running Caddy natively get the raw editor only. A Caddyfile renderer
   for the same `SiteSpec` (most fields have a direct equivalent; `blockExploits` and `securityHeaders`
   map to `respond` and `header`) would make the form engine-agnostic. The Docker Caddy ingress already
   renders managed routes, so the shape exists.
8. **nginx `stub_status` metrics.** A `conf.d/jd-status.conf` serving `stub_status` on a loopback port
   and a poll from the host side would put active connections and requests per second on the overview.
   The backend runs in a container with the host's network namespace, so the loopback port is reachable.
9. **Certificate details sheet.** SANs, serial, fingerprint, key type, chain and the `openssl` commands
   to reproduce them, from the file rather than a scan. All fields exist in `tlsscan.go`'s chain code.
10. **Scan history.** A watched domain's grade over time, so a regression is a change in a column rather
    than a memory.

## Verification results

Run against this change on 2026-09-17, on the host used for development. Every command below was run
from the repository's own scripts.

| Check | Result |
| --- | --- |
| `go build ./... && go vet ./... && go test ./...` (backend) | **Pass**, 33 packages, including the new `proxysvc` regression tests. |
| `bun run lint` (project-wide) | **Pass**, exit 0. |
| `bun run build` | Pass. |
| `tests/browser/proxy-ui.spec.ts` (7 tests) and `design-system.spec.ts` (9 tests) | **Pass**, 16 of 16, against a fresh production build. |
| `bun run test:browser` (full suite) | 133 passed, 16 skipped, 21 failed. The failures were in the deploy, security, terminal and — for two tests — proxy specs, and every one of them post-dates the moment another session's `bun run build` replaced `.next/` under the server the suite was using (the served build manifest returned 404 from then on). The proxy and design-system specs were rerun on a fresh build immediately afterwards and passed; the other specs belong to pages being changed in the same worktree by other work and were not re-verified here. |
| Screenshots at 390, 1280 and 1720 against a mocked API | Reviewed for the six pages and the site form: no horizontal scrollbar, tiles top-aligned, hints truncating rather than wrapping. |

A worktree shared with other in-progress work means the project-wide lint and test results include
files outside this change; the pull request description states them as observed.
