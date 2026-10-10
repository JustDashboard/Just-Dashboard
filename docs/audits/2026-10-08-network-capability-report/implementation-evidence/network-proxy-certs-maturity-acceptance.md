# Proxy, streams, access, controls, certificates and watches maturity acceptance (C120–C126)

This records the proxy and certificates package of the feature-by-feature maturity work, branch
`implement/network-proxy-certs-maturity` from PR head `122c153a`. The behaviour and its boundaries are
documented in [the Proxy guide](../../../internal/backend/databases-proxy-platform.md#proxy), [the
connection investigator](../../../internal/backend/network-investigator.md#native-streams-on-the-path)
and [watched probes](../../../internal/backend/network-diagnostics.md#watched-probes); the pages that
show them are named in the [feature map](../../../internal/frontend/feature-map.md). It changes no
ledger status; the proposed status per row is listed at the end for the ledger owner.

Raw logs are kept beside this record in
[`network-proxy-certs-maturity/`](network-proxy-certs-maturity/), linked under [Checks](#checks).

Every native test below runs the host's own binaries (nginx 1.26.3, its dynamic stream module, certbot
2.11.0) as the unprivileged contributor, on private prefixes in temporary directories, on loopback
ports, against listeners the test owns and a certificate authority on loopback. Each finds its own nginx
master by the configuration it started it on. None of them signals, reloads or rewrites the host's nginx
or Caddy, requests a certificate from a real authority or touches DNS, and each removes its processes
and directories when it ends.

## Per row

### C120 HTTP/WebSocket reverse proxies — proof that a reload loaded the candidate

Shipped: every nginx reload a save, a switch, a link removal, a bulk change, a settings change, a
renewal or the engine's own Reload asks for goes through `reloadProven` (`proxysvc/reload_proof.go`).
Before the signal it notes the running master (found by the configuration it was started on), its
workers, the end of its error log and the SHA-256 of each file the change wrote (the site file and its
`sites-enabled` link). After it, `awaitLoad` reports `loaded` only when every worker serving was started
since (one replaced worker beside old ones is a crash, not a load), each file still holds what the
change wrote, and nginx holds every socket the site's `listen` lines ask for; `refused` with the
master's own `[emerg]` line; `unconfirmed` with a note when none of that was seen in three seconds, a
file was written again first, the socket is missing or the error log could not be read; `unchecked` when
no running nginx could be read. A save whose reload the master refused over **one of the site's own
sockets** (a port another program holds, which `nginx -t` passes) is put back exactly as a refused test
is (file, link and `.bak`), and answered 409 `load_refused` naming the program holding the port; any
other refusal keeps the valid file and is `reloaded: false` with nginx's words. A `bind()` failure is a
refusal only once the master gives up (`still could not bind()`), which the watch reads on for, so its
later attempts are not read as the next reload's; a port freed between attempts is bound on the next and
the reload is read as any other load, never put back (a review of the package found the earlier version
putting a site back under a load that had happened). The engine's reload answers a refused load as 502
`load_refused`. The site form says "is live" only for a proven load and shows the proof ("nginx loaded
it on 2 new workers, holding port 80/tcp."); every other reload toast (engine, pending strip, command
palette, served-certificate and TLS-report reloads, import outcome) reads the proof too.

Tests: `TestSaveSiteProvesNginxLoadedIt`, `TestSaveSitePutsBackASiteNginxCouldNotBind` (new and edit),
`TestSaveSiteKeepsASiteNginxBoundOnALaterAttempt` (bound on a later attempt: loaded; neither seen:
unconfirmed; the site kept in both), `TestSaveSiteReportsAReloadNginxRefusedForSomethingElse`,
`TestSaveSiteDoesNotCallAnUnprovenReloadLoaded` (no new workers, one replaced worker, file rewritten,
socket missing), `TestSaveSiteSaysWhenItCouldNotProveTheLoad`, `TestEngineReloadIsProvenFromTheMaster`,
`TestSiteBindsReadEveryServerBlock`; native `TestLiveReloadIsProvenFromTheMaster` (the host's nginx on a
private prefix: a loaded save proven from the real master's workers and sockets; a save onto a port the
test holds refused by the master, put back, the test process named as holder, the previous site still
answering; the next reload loaded); bun `load-proof.test.js`, `site-save.test.js`; browser `a save is
live only when nginx's master was seen loading it` (loaded, unconfirmed, and a 409 refusal that keeps
the draft).

Limits: proof depends on `/proc/<pid>/task/<pid>/children` and the error log being readable (as the
stream watch already does); without the log a refusal reads as unconfirmed, without the children as
unchecked/unconfirmed. Deployment cutovers receive a refused load as their failure and recover as they
already do. Caddy reloads answer with their own outcome and are unchanged.

### C121 Upstream pools and load balancing

Shipped: `GET /proxy/upstreams` carries `pools` (`proxysvc/upstream_pools.go`): each upstream block a
route uses (method, keepalive, every server with weight, max_fails, fail_timeout, backup and down — down
ones listed, never dialled) and each direct address, with the routes and files using it. `balancing`
tells native balancing (`native`, a block of several servers; `native-dns`, one name resolving to
several addresses, which nginx resolves at load) apart from a single endpoint (`single`), whose
spreading — a provider's balancer, a floating address — happens outside nginx, with a `provider` hint
from managed-balancer host-name suffixes. Network-level outcomes come from nginx itself: per server,
what its error logs (every `error_log` the loaded tree names, confined to `/var/log/nginx`) recorded
over the last hour — refused, timed out, reset, closed early, set aside — and `no live upstreams` per
block; the verdict is serving, degraded, on its backup, down or unknown. A site's page draws each of its
pools as a **Balancing** panel.

Tests: `TestUpstreamPoolsSayWhoBalances`, `TestUpstreamPoolVerdicts`,
`TestUpstreamPoolsCountWhatNginxLogged`, `TestRequestErrorLogsStayInsideTheLogDirectory`; native
`TestLivePoolOutcomesComeFromNginxItself` (the host's nginx balancing a private pool with a refusing
member; the report reads the refusals from nginx's own log and calls the pool degraded); bun
`upstream-pools.test.js`; browser `/proxy/sites/app.example.com says who balances its requests and what
nginx logged of each server`.

Limits: "set aside" is logged at warn, so it is absent where the error log level is `error`; host names
are resolved from here now, not as nginx resolved them at load; a provider hint is a suffix match, never
proof; provider-side health (a cloud balancer's own targets) is not visible.

### C122 TCP/UDP stream proxies joined into Network

Shipped: a local listener the proxy inventory attributes to a native nginx stream joins the stream as
its own **observed** evidence in the connection investigator (`netpath/stream.go`, through
`proxysvc.StreamPath`): forward, listens, backends with roles, balancing, TLS ends and the access list
as a sentence; the stream's state; the client sessions established now; the TCP connections nginx holds
to each backend now; and the last hour from the stream's log by the backend each session ended on. When
the investigation measures a TCP connection through the stream, the session nginx logged for it is found
by client and time, and a **measured** backend leg says which backend nginx forwarded it to
(`forwarded`), or that the access list refused it (`denied`) or no backend took it (`failed`). The
Streams page's **Trace in Network** verb (TCP streams, administrators) opens the investigator on the
stream's tuple, and `GET /proxy/streams/{name}/path` serves the reading.

Tests: `TestStreamPathJoinsConfigurationSocketsAndLog`, `TestStreamPathSaysWhatItCannotCount`,
`TestAwaitStreamSessionFindsTheMeasuredConnection`, `TestStreamListenerJoinsTheStreamAndItsBackendLeg`,
`TestStreamBackendLegIsOnlyClaimedFromTheLog`, `TestStreamPathIsReadByEveryAccount`; native
`TestLiveStreamPathReadsNginxsBackendLeg` (the host's nginx with the host's stream module on a private
prefix: an open client session and nginx's backend connection counted, then the closed session found in
the log as 200 to the test's backend); bun `network-investigator.test.js`; browser `a stream's link
opens the tuple, and the report joins the stream and the backend its connection reached` and `a TCP
stream traces its connection path on the Network page`.

Limits: the backend leg is correlated by client address and time (nginx logs no client port), so another
connection from the same address in the same second could be the one logged; it needs the stream to log
its sessions; UDP streams have no per-session socket to count and the probe sends no UDP; the backend's
own protocol and application are not measured.

### C123 Proxy access lists, authentication and SSO — cross-layer effective access

Shipped: `GET /proxy/resolve/access?url=&source=` (`proxysvc/effective_access.go`, administrators)
resolves the route as the URL resolver does and judges every layer for one address in nginx's order:
outside this host (always unknown), the host firewall (`netsec.JudgeFirewallFrom`, the firewall's rule
order for that source; a rule its listing cannot judge for an address — a ufw rule on an interface, as
`ufw allow in on tailscale0` prints it, a profile the host does not define, an iptables rule on another
input interface, with a match an address does not decide, or jumping to an unread chain — is passed
over, and leaves the layer unknown when it would act otherwise than the verdict; defined ufw profiles
are read through their ports), the site form's rewrite-phase checks (maintenance with its bypass list,
Cloudflare only, the crawler block; any other server-level `if` is unknown, never a pass), the
allow/deny lines in effect (innermost level, included access lists read where nginx includes them, first
match, the deciding line and its shared list named), sign-in (`auth_basic`, `auth_request` single
sign-on), the request limit and client certificates, combined with `satisfy any` exactly as nginx
applies it — including an address no line names, which is declined and still asked. The Sites page's URL
resolver takes an optional **From address** and draws each layer with its verdict, deciding lines and
owner link.

Tests: `TestExplainAccessFollowsNginxsOrderAndInheritance`, `TestExplainAccessJudgesTheFormsOwnChecks`
(rendered maintenance, Cloudflare-only, redirect and SSO sites), `TestExplainAccessEdges`,
`TestExplainAccessDoesNotGuessAnIf`, `TestExplainAccessSatisfyAnyAsksAnAddressNoLineAllows`,
`TestLimitIdentStripsOnlyTheZonesSuffix`, `TestJudgeFirewallFromOneSource`,
`TestJudgeFirewallFromReadsUFWProfiles`, `TestJudgeFirewallFromReadsIPTablesListing` (ufw's and
iptables' own listings: an interface rule, an established-only rule, loopback, a portless range drop, a
fail2ban chain, a REJECT with its answer and a commented rule),
`TestRouteAccessExplainsLayersForAdmins`; native `TestLiveAccessExplanationAgreesWithNginx` (the host's
nginx answers 127.0.0.1 exactly as predicted: 200, 403, 401 and 200 for an open path, a deny ahead of an
allow, and `satisfy any` without and with the address listed); bun `access-explain.test.js`; browser
`the URL resolver says who may take the route from an address, layer by layer`.

Limits: provider firewalls, CDNs and NAT are unknown by design; the source is the address nginx sees
(after real_ip), and the firewall layer judges it as the connecting address, without following the
chains INPUT jumps to or knowing which interface a packet from the address arrives on (both leave the
layer unknown where they could decide); hand-written `if` conditions, `geo` blocks using `ranges`, and
`map`-driven access are reported as unknown; stream access lists appear in the stream evidence of C122,
not in this per-URL explanation.

### C124 Rate limits, caching and HTTP/2–HTTP/3 — service policy and measured effect

Shipped: `SitePolicy` (`proxysvc/site_controls.go`, `GET /proxy/sites/{name}/policy`) reads a site's
request limit, connection limit, response cache, asset caching, HTTP/2 and HTTP/3 from its file with the
running build's support for each (`nginx -V`, now including `--without-` modules). The connection
investigator reuses it as the **service policy** of each site forwarding to the destination port. `POST
/proxy/sites/{name}/controls/verify` (administrators, audited) measures them against this nginx on
loopback as a visitor to the site — or, for a site listening on one address of this host only, at that
address, so a catch-all on the wildcard cannot answer in its place (found by the package review); an
address that is not this host's is never dialled. HTTP/2 is measured by ALPN, HTTP/3 by a QUIC Version
Negotiation answer plus an `h3` Alt-Svc, the cache by two requests and `X-Cache-Status` (with the reason
it did not store), asset caching from an asset's `Cache-Control`, and last the request limit by its
burst plus two requests at once (at most 40). The site page's **Controls** panel lists the policy and,
for administrators, measures it.

Tests: `TestSitePolicyReadsTheFormsControlsAndTheBuild`, `TestVerifySiteControlsRefusesBeforeSending`,
`TestSiteControlsSurviveOddFilesAndRefuseAnotherSitesName`, `TestControlHelpers` (including the address
dialled for each kind of listen), `TestProxyLayerCarriesTheSitesServicePolicy`; native
`TestLiveSiteControlsAreMeasured` (the host's nginx on a private prefix with TLS, HTTP/2, a QUIC listen
advertised by Alt-Svc, a response cache and CSS caching, and a one-a-minute limit: HTTP/2, HTTP/3,
cache, asset caching and the limit all verified from nginx's answers); bun `site-controls.test.js`;
browser `/proxy/sites/app.example.com reads its controls and measures what each one does`.

Limits: the connection limit is not measured (it counts requests in flight); a limit whose burst needs
more than 40 requests, or that would hold the check over five seconds without `nodelay`, or that exempts
the address the requests come from, is not measured; measurement requests reach the application; HTTP/3
is not offered by the site form (hand-written QUIC listens are read and measured); there is no QUIC
client in the module graph and no dependency may be added, so HTTP/3 is shown reachable and advertised,
not exercised end to end. There is no separate Network "service policy" object to attach controls to:
the reuse is the policy reading in the investigator.

### C125 Certificates, ACME and renewal — failures linked to stage and owner

Shipped: `DiagnoseIssuance` (`proxysvc/issue_diagnosis.go`) reads certbot's output — the authority's
per-domain report, or a run's meaningful failure lines — into problems with a validation stage (name
resolution, CAA, reaching port 80, serving the challenge file, the DNS-01 record, the DNS plugin's
access, rate limits, account, certbot's own port 80, the nginx installer) and that stage's owner (DNS
provider or nameservers, a firewall in front of port 80, whatever answers port 80 here, whoever holds an
address that is not this host's, the web server answering the name, the provider credentials, the
authority), the address the authority reached and whether it is this host's (only where that can be
said), an action, and links to the page holding the owner's evidence (Network DNS and delegation tools,
firewall, external checks, ports on 80, the URL resolver, the HTTP tool on the challenge path, the TLS
page's DNS/CAA). `GET /certificates/jobs/{id}/diagnosis` (administrators) reads a failed certbot job;
the renewal health carries the last failed run's problems (none once recovered) and the `renewal_failed`
alert names each stage and owner. The Certificates page's issuance console, a site form's certificate
step and the renewal record show **Where it failed**.

Tests: `TestDiagnoseIssuanceReadsEachDomainsStageAndOwner`, `TestDiagnoseIssuanceReadsRunFailures`,
`TestFailedRenewalRunCarriesItsProblems`, `TestRecoveredRenewalCarriesNoProblems`,
`TestCertJobDiagnosisReadsTheFailedRun`; native `TestLiveCertbotFailureIsReadByStage` (the host's
certbot 2.11 ordering over webroot, into private directories, from a certificate authority on loopback
that fails the challenge with Let's Encrypt's own connection, DNS, unauthorized and CAA problem wording;
certbot's real output read into each stage — no request leaves the host and no certificate exists);
browser `a failed renewal says where validation failed and whose it is to fix` and, added while
finishing the package, `a failed issuance says where validation failed, read from the job's own output`
(a failed test issuance in the console asks the job's diagnosis once and shows the stage, owner, the
authority's address as not this host's and the owner's links).

Limits: classification follows the authority's and certbot's wording (Let's Encrypt/Boulder problem
types and certbot 2.x output); an unrecognised message is said to be unrecognised, never guessed; "this
host" is judged from interface addresses, so behind provider NAT the placement is left unknown; no
failure against a real authority on a real domain was produced on this host, by design.

### C126 Endpoint/TLS watches reused for network probes

Shipped: network probes are watched endpoints of their own kind rather than a second monitor:
`watched_endpoints.kind` (`tls` or `tcp`, additive, every existing row `tls`), checked by the same
`TLSMonitor` pass and interval through `CheckTCP` (one connection, nothing sent), kept in the same
history (`watched_checks.ms` for connect time) and judged by the same `watch_unreachable` alert; the
grade rule and fleet scan leave probes out; a probe never replaces a TLS watch, and a TLS watch takes a
probe over, dropping its connect times from the history in the same transaction. The watch tables moved
from `proxySchema` into `schema` so `addedColumns` can extend older installs. The Network Tools TCP port
check offers **Watch on a schedule** (administrators), the Network runs page lists **Watched probes**
(check now and stop for administrators), and the Proxy watch list draws a probe's connection and
connect-time trend with no TLS report.

Tests: `TestWatchedEndpointsGainProbeColumns` (an older-shape table upgraded in place),
`TestWatchedEndpointsKeepTheOldListAndAllowSeveralPorts`, `TestTheWatchMonitorProbesNetworkEndpoints`,
`TestCheckTCPConnectsOrSaysWhyNot`, `TestNetworkProbesAreWatchedEndpoints` (a real loopback listener
watched, checked, its history, the unreachable alert once it closes, the kind rules and an empty history
after a TLS watch takes the probe over), `TestAProbeResultDoesNotLandOnARowATLSWatchTookOver`; bun
`watched-probes.test.js`; browser `a port check's endpoint is watched as a probe on the watch list's
schedule`, `watched probes are the watch list's own, checked and stopped from the runs page`, `a network
probe on the watch list reads its connection, not a certificate`.

Limits: probes are TCP connects only (no DNS, HTTP or UDP probe kinds); they are checked from this host,
which says nothing about reachability from outside; they share the TLS watch's single interval.

## Checks

All with `TMPDIR`/`GOTMPDIR` on disk, `GOMAXPROCS=2 GOFLAGS=-p=2` and one browser worker (three for
the processes rerun) against a production build of the tree served on port 43212. Builds, browser runs and Go runs held a shared
heavy-work lock for the build, server and run; test helpers left by the API package's terminal tests
were ended by PID afterwards.

| Check | Source | Result |
| --- | --- | --- |
| `scripts/test-changed.sh 122c153a` (112 changed files) | `ea2e7b14` | 3,234 s after a 13 s build: Prettier clean, ESLint and `tsc --noEmit` clean, `bun test src` 3,303 pass / 0 fail, `go build ./...` and `go vet` clean, Go tests for `api`, `netpath`, `proxysvc` (native nginx and certbot tests included), `store` and `netsec` ok, 1,081 browser cases passed, 1 failed, 44 skipped. Every browser case this package added passed. [Log](network-proxy-certs-maturity/test-changed-2.log) |
| The one failure, `processes-ui.spec.ts` `a unit's journal reads its runs, folds a loop and opens one run's own lines`, three times on the same build | `ea2e7b14` | 3 passed. [Log](network-proxy-certs-maturity/processes-rerun.log) |
| `go test -race` on every Go test named above (`api`, `netpath`, `netsec`, `proxysvc`, `store`) | `ea2e7b14` | 50 top-level tests passed, no race reported. [Log](network-proxy-certs-maturity/go-race-final.log) |
| The six native tests, verbose (nginx 1.26.3, certbot 2.11.0) | `ea2e7b14` | all passed, with certbot's output for each failed challenge. [Log](network-proxy-certs-maturity/native-final.log) |
| Targeted Go tests and the changed browser cases after the review fixes | working tree before `c8d065f3` | all passed. [Log](network-proxy-certs-maturity/review-fixes-precheck.log) |

The 44 skipped browser cases are optional screenshot captures that run only with an environment flag;
none belongs to this package. The processes case asserts that a run's journal window is under ten
seconds and received exactly 10,000 ms once; nothing in this package touches Processes or the log
viewer, it passed in the first full run below, and it passed three times on the final build.

### Failed and earlier runs retained

The first full run (`6868753c`) failed two browser cases: `a port check's endpoint is watched as a
probe on the watch list's schedule`, whose fixture expected the refusal toast to name port 5432 while
the form sends its default 443 (fixed in the spec, `ea336461`), and `database-query.spec.ts` `a
failed statement is marked at the word the engine names`, which read a Monaco decoration's box while
it re-rendered and passed in the final run. [Log](network-proxy-certs-maturity/test-changed-1-failed.log).
The first `go test -race` run of the package's tests failed `TestLiveCertbotFailureIsReadByStage`: the test
wrote the loopback authority's address after starting the server whose handlers read it; the address
is now known before the server starts (`6868753c`) and the rerun passed.
[Failed](network-proxy-certs-maturity/go-race-tests-1-failed.log),
[passing](network-proxy-certs-maturity/go-race-tests.log). Earlier passing iterations:
[native tests at `863690fc`](network-proxy-certs-maturity/native-live-tests.log),
[selected browser specs](network-proxy-certs-maturity/browser-iteration-1.log) and
[the API package](network-proxy-certs-maturity/api-selected-tests.log).

### Review

A read-only review of the whole backend diff found no security-invariant regression (capability
gates, audited mutations, no destructive route, no new host command, no client path, additive schema
with the watch tables' move upgrading older installs, put-back restoring a new site's absence and an
edited site's file, link and `.bak`). It found three over-claims and a history mix-up, each fixed with
a regression test before the final run: the firewall layer dropping a ufw interface rule and reading
the tailnet as refused (`c8d065f3`), a site put back under a load nginx made after retrying a bind
(`fd92af6a`), controls measured on loopback for a site that listens on one address, where a catch-all
answers instead (`368226da`), and a probe's connect times left in the history of the TLS watch that
took its endpoint over (`cf263b39`). Its remark that the reload proof reads the error log under the
roots the stream watch allows (`/var/log` and the nginx directory) rather than `/var/log/nginx` was
left as it is: the path comes from nginx's own configuration, and only `[emerg]` text reaches an
administrator.

## Proposed status

| Row | Proposed status | Reason |
| --- | --- | --- |
| C120 | verified | The reload is proven from nginx's master and a refused own-socket load is put back; Go, native nginx and browser acceptance pass. |
| C121 | verified | Pools tell native balancing from a single endpoint and carry nginx's own logged outcomes per server; Go, native nginx and browser acceptance pass. |
| C122 | verified | Stream configuration, sockets, backend legs and logged sessions are joined into the investigator with a measured backend leg; Go, native nginx stream and browser acceptance pass. |
| C123 | verified | Per-address effective access across firewall, edge, maintenance, allow lists, sign-in and `satisfy any`; native nginx answers match the explanation; browser acceptance passes. |
| C124 | implemented / acceptance pending | Policy reading, Network reuse and measured verification pass against native nginx, but the connection limit is unmeasured and HTTP/3 is shown reachable and advertised, not exercised by a QUIC client. |
| C125 | verified | The host's real certbot output, failed in Let's Encrypt's problem wording, is read into stage and owner for issuance and renewal, and both browser paths show it; no real-authority failure was produced, by design. |
| C126 | verified | TCP probes reuse the watch monitor, history and alerts; a real loopback listener is watched, checked and alerted on; browser acceptance passes on Network and Proxy. |

## Integration

Merged after the routing package, whose ufw parser moves `on tailscale0` from the destination
column into the rule's `Interface`, the per-address firewall judgement no longer saw interface-limited
rules: `TestJudgeFirewallFromOneSource` failed on the assembled source, counting a port-22 rule
on `tailscale0` against a port-443 check. The judgement now reads the interface from either place.
On the assembled source `netsec`, `netpath`, `proxysvc`, `store`, `netx` and the whole `api`
package, 3,374 Bun tests, `tsc`, ESLint and Prettier passed.
