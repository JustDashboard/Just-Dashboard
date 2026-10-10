# DNS and resolver configuration maturity acceptance

This records the implementation of ledger rows C066–C074 ("DNS and resolver configuration") and the
completion of concrete finding F10 on branch `implement/network-dns-resolver-maturity`, started from
PR #173 head `4338e6cc`. It changes no ledger status; the proposed statuses at the end are for the
ledger owner to apply. The raw logs sit beside this document as `network-dns-resolver-maturity-*.log`
files, copied byte for byte except that this host's tailnet search domain and hostname are replaced
by `[redacted-tailnet]` and `[redacted-hostname]`. Review screenshots and the original files remain in
the task's artifact directory outside the tree.

Every live check ran on this production host without touching its resolver: the resolver fixtures
run actual systemd-resolved 257 in new network and mount namespaces with a private `/etc`,
`/run/systemd` and D-Bus, their `systemctl` calls answered inside the namespace and any other
`systemctl` call failing the fixture. The engine fixtures use owned containers from the pinned,
already-cached AdGuard Home, Pi-hole and Technitium images. No test queries a public resolver. After
the runs the host still had no `/etc/systemd/resolved.conf.d`, resolved active on its stub, and no
fixture link.

## Per row

### C066 — host resolver, resolv.conf and the port-53 chain

Shipped: `dns_owner.go`, a read-only owner adapter in `DNSView.owner`. It reads the host's
`/etc/resolv.conf` through `/host` (resolving links component by component inside that root, so an
absolute `/run/NetworkManager/resolv.conf` or a `/var/run` link is the host's, not the container's),
the file's signed header, NetworkManager's merged `[main] dns=`/`rc-manager=` (`NetworkManager.conf`
then `/usr/lib`, `/run`, `/etc` `conf.d` by name, `/etc` shadowing), Debian resolvconf and openresolv
configuration, SUSE `NETCONFIG_DNS_POLICY`, port-53 listeners and running services. It names the
writer (systemd-resolved, NetworkManager incl. its dnsmasq plugin, resolvconf, openresolv, netconfig,
dhcpcd, dhclient, Tailscale, WSL, a loopback cache, a plain file, missing, unrecognised), the chain
(stub, uplink, local-cache, direct, missing), a confidence (confirmed, declared, inferred, unknown),
whether the page's resolved drop-in reaches programs, conflicts between sources, and the owner's own
place to change DNS (with a dashboard link where one exists). Refusals on hosts without resolved and
the post-apply warning name the owner and its handoff. The DNS page draws it as **Resolver owner**.

Tests: `TestResolverOwnerAcrossDistributions` (20 staged host trees: Ubuntu stub, absolute stub link
read inside the root, Fedora NetworkManager→resolved, RHEL NetworkManager writing the file,
`rc-manager=symlink` through `/var/run`, NetworkManager's dnsmasq plugin, resolved bypassed by
NetworkManager, Debian resolvconf, openresolv, SUSE netconfig, dhcpcd, Tailscale, WSL, Unbound on
loopback, plain file, missing file, uplink, stub link without resolved, unrecognised link, link loop),
`TestNetworkManagerDNSConfigurationPrecedence`, `TestDNSViewAndRefusalNameTheOwner`, updated
refusal/warning tests in `dns_test.go`; read-only live verdict for this Ubuntu host
(`TestResolverOwnerReadsThisHost`, `dns-owner-this-host.txt`: systemd-resolved, stub, confirmed);
namespace fixture owner checks for the stub chain and for a foreign static chain with resolved still
running (conflict reported). Browser: "names the resolver owner…", "a NetworkManager-owned file
says the drop-in does not reach programs and hands off".

Limits: other distributions are covered by staged file trees, not by live hosts of those
distributions; owners outside the list are reported as unrecognised. The adapter reads and hands off;
it does not edit NetworkManager, resolvconf, netconfig or dhcpcd configuration.

### C067 — resolved per-link scopes and split DNS through the native owner

Shipped: **Per-link DNS** (`link-scopes.tsx`) lists every link resolved holds DNS for — servers,
search and routing domains, default-route state, DoT and DNSSEC — and, for an administrator, edits a
link's split DNS through the existing native profile adapter (`GET`/`PUT
/network/native/profiles/{device}`). `split-dns.ts` builds the write from the profile's complete
current intent, changing only per-family DNS servers, the shared link-level routing and search
domains (the same list on every enabled family, as networkd requires) and ignore-automatic-DNS. The
write is always a temporary apply with reconnection confirmation and independent watchdog recovery,
and the editor states resolved's automatic default-route rule for the draft. A link whose owner the
adapter does not edit shows the native refusal instead of a form.

Tests: `split-dns.test.js` (draft extraction, family split, disabled families, suffix validation,
limits, effect sentences); browser "split DNS for a link goes through its native profile as a
temporary apply" (asserts the PUT body keeps addresses/routes, carries both families' servers and the
routing domain, and sends `X-JD-Network-Apply: pending`) and "a link whose native owner is not edited
here says why…". Live: `TestNativeManagerOwnerLive` gained a split-DNS step after its existing
confirmed edit: the profile's own intent with `~corp.test` and `lab.test` on both families and
per-family servers, applied temporarily through the native owner, confirmed through the reconnection
challenge with complete cleanup, then verified as configured/runtime agreement and in networkd's own
JSON report. The networkd owner and the Netplan owner rendering to networkd both passed in isolated
namespaces with the freshly built recovery helper ([networkd](network-dns-resolver-maturity-split-dns-networkd.log),
[Netplan](network-dns-resolver-maturity-split-dns-netplan.log)).

Limits: NetworkManager and Netplan/NetworkManager owners are not runnable on this host (no verified
NetworkManager userland root), so their split-DNS acceptance rests on the existing P11 native owner
evidence for the same DNS/domain fields. Owners the native adapter refuses keep their own tools.

### C068 — presets, custom upstreams, fallback, domains and cache

Shipped: `clear` (servers, fallback, domains) writes only the empty assignment, removing what
`resolved.conf` and earlier drop-ins set; an empty `FallbackDNS=` turns off resolved's compiled-in
fallback. A list cannot be both given values and cleared. `ManagedDNS.cleared` reports cleared lists,
and the editor offers clearing each list while it is empty, preserving an existing clear as the
baseline. The read-back check (C071) proves the running state.

Tests: `TestCleanDNSSettingsClearsAndVerificationNames`, `TestRenderResolvedClearsLists`,
`TestSetDNSRollsBackWhenAnotherDropInOverridesIt` (a cleared fallback that resolved still reports
fails); namespace fixture: a strict change with `clear: [fallback]` over a base `FallbackDNS=192.0.2.250`
reads back with no fallback in actual resolved; browser "clearing the fallback list sends it as
cleared…".

Limits: the cache mode is written but `resolvectl status` does not report it, so it is listed as
not read back.

### C069 — host DNS over TLS

Shipped: three pieces. (1) After a change, a `transport` check reads the first answer back through the
native adapter: required DoT passes only on native-reported confidential transport, an unencrypted
fresh answer fails and rolls back, and opportunistic DoT reports a fallback to classic DNS as a
warning. (2) Global-scope replies report the interface they arrived on rather than scope zero; when
the global scope is the only candidate its strict policy now covers them, so strict trust is no longer
impossible for global upstreams. (3) `POST /network/dns/tls-check` opens the dashboard's own TLS
session to each configured DoT server (global, fallback, per-link, drop-in) or named preset on its
DoT port, verifies the certificate for the configured name against the host's trust store and asks
the root's NS records over it; results are trusted, untrusted, no-answer or unreachable with version,
issuer, names, expiry, fingerprint and chain length, and unnamed servers are listed with why. The page
shows it as **Upstream certificates**.

Tests: `TestSetDNSTransportCheck` (strict passed, strict unencrypted rolled back, opportunistic
warning), `TestDNSEvidenceGlobalScopeReplyInterface`, `TestCheckDNSTLSVerifiesEachConfiguredServer`
(trusted, wrong name, unknown authority, unreachable, no DNS answer, unnamed omitted, preset by name,
arbitrary server refused); namespace fixture: strict DoT to a public-looking upstream verified with
"strict mode", opportunistic fallback to a classic-only server reported, a wrong TLS identity rolled
back, two servers trusted by the independent check; browser "the certificate check lists each
server's trust…".

Limits: the certificate check is evidence of what a server presents now, not packet inspection of
resolved's own connection. systemd-resolved supports no DoH/DoQ and no managed DoH/DoQ server is
supplied.

### C070 — host DNSSEC mode and statistics

Shipped: `POST /network/dns/dnssec-chain` and **DNSSEC chain**. The walk asks the native resolver
for each candidate zone's DNSKEY and each apex's DS with fresh-network flags, staying inside the
name's own policy scope, then independently recomputes every DS digest (SHA-1/256/384) from the
child's canonical DNSKEY RDATA, matches by tag, algorithm and digest, and hashes the root's keys
against the IANA anchors (KSK-2017 20326 and KSK-2024 38696, the pair systemd-resolved 257 carries).
Verdicts: secure, anchored, insecure, broken, unknown. The root's keys are asked with trust anchors in
play (excluding them makes resolved demand a DS for the root itself); a reply from the anchor store
still fails the fresh-origin check.

Tests: `TestDNSSECChainRecomputesEveryLinkToTheRoot` (secure, digest mismatch, rejected DS,
unauthenticated, cached flags), `TestDNSSECChainStaysInsideThePrivateScope`,
`TestDNSSECChainRefusesWithoutANativeOwner`, `TestDNSSECRootAnchorsMatchByDigestOnly`,
`TestDNSSECChainAsksTheRootWithAnchorsInPlay`; namespace fixture over a signed hierarchy (a DS-anchored
namespace root, `example`, `corp.example`): resolved authenticated the DS sets and the dashboard's
recomputed digests matched them, the namespace root was refused as an IANA anchor, and a private
name's chain ended at its scope with its parents not asked; browser "the DNSSEC chain shows each
delegation and the root anchor".

Limits: RRSIGs are verified only by resolved (it refuses explicit RRSIG questions); its API does not
say whether an absent DS was proven by NSEC/NSEC3. Direct comparisons with the DO bit report the
destination's AD claim and RRSIG count, not validation.

### C071 — resolver apply, reset, private-name verification and recovery

Shipped: `POST /network/dns/verification-plan` and a plan-then-result flow. Checks: required
`readback` (resolved runs with what was written; a later drop-in overriding it fails the change;
link loopback servers that resolvectl also lists under Global are discounted), `resolution` for up
to eight verification names each labelled with the scope resolved routes it to, `transport` and
`dnssec`; `unverified` lists routing domains with no name under them, an unchecked default route,
fallback servers, inherited link security settings and the unread cache mode. The confirmation
shows the plan; the result shows every check, and a rollback's 409 carries every check beside the
error. Reset reports its default-route check.

Tests: `TestPlanDNSVerificationNamesEveryScope`, `TestSetDNSReadsBackTheRunningSettings`,
`TestSetDNSRollsBackWhenAnotherDropInOverridesIt`, `TestSetDNSChecksEveryVerificationName`,
`TestResetDNSReportsItsCheck`, `TestPlanDNSValidatesFirst`, existing rollback/recovery tests;
`TestNetworkDNSDiagnosticRoutesCapabilitiesAndValidation`, `TestMapDNSErrorCodes`; namespace fixture:
names in the default route and in `~corp.example on dnspriv0` verified, a wrong identity and an
overriding drop-in each rolled back with resolved restarted onto the previous drop-in (nine namespace
restarts in all); browser "applying shows the server's verification plan first…", "a change put
back by a failed check says which check failed".

Limits: the plan checks the names it is given; scopes without a name are listed, not inferred.

### C072 — managed hosts-file records

Shipped: `POST /network/dns/hosts/preview` (duplicate, conflict, shadowed, overrides, repeated, the
exact block, added/removed) and `GET /network/dns/hosts/resolution` (`getent ahostsv4`/`ahostsv6` in
the host namespace with explicit argv, sixteen names, three seconds each; matches, includes, differs,
unresolved, unknown; the `hosts:` line of `nsswitch.conf`). Save previews first and stops on a
conflict or shadowing until saved again; a save then checks local resolution.

Tests: `TestPreviewHostRecordsNamesOverlaps`, `TestHostResolutionAsksTheHostsNSS`, API preview and
resolution routes; namespace fixture: the namespace's NSS resolved a managed name as written and
reported a name shadowed by an earlier foreign line, and the preview named that shadowing line;
browser "a host record that a line before the block shadows is previewed before it is saved".

Limits: NSS evidence describes programs that resolve through NSS; static binaries, browsers with
their own DNS and containers with their own hosts file can differ.

### C073 — multi-resolver answer and latency comparison

Shipped: comparison `transport: "tls"` (DNS over TLS to each destination's published or configured
identity with certificate verification) and `dnssec: true` (DO bit; AD claim and RRSIG count per
answer); a selected destination without a TLS identity is listed in `omittedTargets` with the reason
instead of being dropped. Presets now carry their TLS identity in the inventory. The split-DNS-safe
default (effective mode) and named, acknowledged destinations remain.

Tests: `TestCompareOverTLSWithDNSSECAndOmissions`, `TestCompareOverTLSRefusesAnUntrustedCertificate`,
existing comparison tests, API validation of comparison-only options; browser "comparing over DNS
over TLS with the DO bit sends both and shows transport and AD".

Limits: AD is the destination's claim; signatures are not verified by the dashboard. DoH/DoQ
comparison is not offered.

### C074 — AdGuard Home and Pi-hole detection and handoff

Shipped: detection also finds Technitium containers and keeps the container ID and the web port's
bind address. `GET /network/dns/services/handoffs` joins each detected server to existing
`dnsservice` connections (owned container identity, or same-engine origin on its web port) and offers
the loopback origin for a new connection, or says why there is none. The ad-blocking rows show
"connected as …" with **Inspect** (opens the connection's inventory, queries, filters, clients) or
**Connect** (opens the connection form seeded with engine, origin and name). Connections gain
`GET /network/dns/services/{id}/dhcp`, a read-only native DHCP reading (enablement, ranges/scopes,
the engine's lease table) for AdGuard Home 0.107, Pi-hole FTL 6 and Technitium 15, with field names
taken from the pinned images' binaries, shown in the connection sheet as **Native DHCP**.

Tests: `TestDNSServiceHandoffsJoinDetectionAndConnections`, `TestDNSServiceHandoffRouteIsPrivate`,
`TestDNSServiceDHCPRouteIsPrivate`, `TestNativeDHCPInventoryAcrossEngines`,
`TestNativeDHCPMalformedSectionsStayUnknown`, `network-dns-dhcp.test.js`; browser "a detected server
without a connection opens the connection form with its origin", "a detected server's connection
opens its inventory and reads its native DHCP". Live: `TestDNSServiceNativeOwnedEngine` now reads DHCP
on each freshly provisioned owned engine. All three pinned engines passed serially under the shared
lock with their owned containers, networks and volumes removed: AdGuard Home v0.107.71 (disabled, no
range, empty readable table), Pi-hole FTL v6.7.1 (disabled, one configured range, empty table) and
Technitium 15.6 (one disabled scope, empty table)
([AdGuard](network-dns-resolver-maturity-dhcp-adguard.log),
[Pi-hole](network-dns-resolver-maturity-dhcp-pihole.log),
[Technitium](network-dns-resolver-maturity-dhcp-technitium.log)). Lease-row parsing is covered by the
unit fixtures only: an owned engine on a bridge network has no DHCP clients.

Limits: DHCP is read-only; nothing starts, stops or reserves leases. The handoff points at
connections; it creates none on its own. Measured filtering decisions remain the separate P17
evidence.

### F10 — policy-aware default, explicit comparison, no private routing domain to a public upstream

Shipped: `dns_private.go`. A private name — under the chain's search domains or resolved's link
domains, single-label, a special-use or commonly private suffix, or a private address's reverse name
— that no link claims is refused (`dns_private_name_public_upstream`) when resolved's default route
reaches a public server, before any question. On a foreign chain the first public server refuses it
the same way; private servers whose own forwarding is unseen need `acknowledgeForwarding`
(`dns_private_name_unknown_forwarding`) and then receive it alone. The lookup UI shows the refusal and
offers the acknowledgement. Comparison still requires named destinations and disclosure
acknowledgement.

Tests: `TestEffectiveLookupRefusesPrivateNamesOnAPublicDefaultRoute`,
`TestLookupDoesNotSendPrivateNamesToPublicPresetsByDefault` (updated: the public static resolver is now
refused), `TestLookupAsksBeforeSendingPrivateNamesToUnseenForwarding`; namespace fixture with a
public-looking default-route server: an unclaimed private name refused with zero public questions, a
link-claimed name answered by its link, a foreign public chain refused, a foreign private chain
refused until acknowledged; browser "a private name with unseen forwarding is sent only after it is
acknowledged". The existing alias-safety and native evidence fixtures still pass.

Limits: container clients (Docker's embedded resolver) are not a modelled vantage; a private
resolver's onward forwarding is acknowledged, not measured.

## Final checks

| Check | Result | Log |
| --- | --- | --- |
| `scripts/test-changed.sh 4338e6cc` (final, against tree `fb523d18` plus this document) | exit 0 in 4,138 s wall (most of it waiting for the shared lock): Prettier, ESLint, `tsc`, 3,277 `bun test` cases, `go build`/`go vet`, the Go tests beside every changed file in `api`, `netx` and `dnsservice`, and 251 browser cases passed with 10 skipped (gated screenshot and optional cases) | [final](network-dns-resolver-maturity-test-changed-final.log) |
| Resolver maturity and evidence namespace fixtures, `-race`, root, actual systemd-resolved 257 | both pass; nine namespace resolver restarts, zero public questions for refused or private names | [pass](network-dns-resolver-maturity-namespace-race-pass.log), [evidence regression](network-dns-resolver-maturity-evidence-fixture-regression.log) |
| Native split DNS through networkd and Netplan/networkd, isolated namespaces, fresh recovery helper | both pass with temporary apply, confirmation, complete cleanup and runtime agreement | [networkd](network-dns-resolver-maturity-split-dns-networkd.log), [Netplan](network-dns-resolver-maturity-split-dns-netplan.log) |
| Owned engine fixtures with the DHCP reading, serial, shared lock | AdGuard Home, Pi-hole and Technitium pass; owned resources removed | [AdGuard](network-dns-resolver-maturity-dhcp-adguard.log), [Pi-hole](network-dns-resolver-maturity-dhcp-pihole.log), [Technitium](network-dns-resolver-maturity-dhcp-technitium.log) |
| Read-only owner verdict for this host | systemd-resolved, stub, confirmed, drop-in reaches programs | [log](network-dns-resolver-maturity-owner-this-host.log) |
| DNS page review screenshots at 390 and 1440 px (gated `screenshots` cases) | owner, per-link rows, plan dialog, results, certificates, chain and split-DNS panel reviewed; per-link rows reflowed for phones after the first capture | artifact directory |

## Corrected failures

The fixtures found real defects, each fixed in source before the passes above; the failing runs are
kept.

- The first namespace run failed the strict change's read-back: `resolvectl status` prints a link's
  loopback-addressed server under Global too, so the read-back saw an extra global server
  ([log](network-dns-resolver-maturity-namespace-first-failure-readback.log)). The read-back now
  discounts link loopback servers it was not asked to write (`TestReadbackDiscountsALinksLoopbackServer`).
- The next run showed strict DoT never earning native strict trust for global upstreams: resolved
  reports the reply's arrival interface (7) rather than scope zero
  ([log](network-dns-resolver-maturity-namespace-failure-global-trust.log)). With the global scope as
  the only candidate its policy now covers that reply (`TestDNSEvidenceGlobalScopeReplyInterface`);
  the existing evidence fixture still passes.
- The chain walk then failed at the root with `DNSSEC validation failed: no-signature`: excluding trust
  anchors made resolved ask a DS for the root itself
  ([log](network-dns-resolver-maturity-namespace-failure-root-dnskey.log)). The root's keys are now
  asked with anchors in play, and the fixture anchors its root in the DS form resolved's built-in
  anchors take. One intermediate fixture run panicked in its own answer function and left that
  namespace's D-Bus and resolved running; both were stopped by PID (the host resolver, PID 487 in the
  host namespace, was never touched) and the fixture now recovers answer panics.
- The first browser run failed three cases ([log](network-dns-resolver-maturity-browser-initial.log)):
  the cache tile's ticker stayed at 0 because the owner panel pushed it below the fold (the panel now
  follows the readings), the editable profile fixture lacked `matching` lifecycle evidence, and a
  preset selector named two cards. The rerun ([log](network-dns-resolver-maturity-browser-rerun.log))
  then exposed a real defect: the split-DNS confirmation compared a freshly derived intent by identity
  and refused every apply; it now checks the retained draft, as the native profile editor does.
- The first `test-changed` run ([log](network-dns-resolver-maturity-test-changed-first.log)) failed two
  existing audit cases written for the single verification name and an apply with no plan request;
  they now assert the plan-then-apply contract and the `verificationNames` body.

## Proposed ledger statuses

| Row | Proposal | Reason |
| --- | --- | --- |
| C066 | implemented / acceptance pending | The adapter, UI and tests pass and this Ubuntu host is verified live, but other distributions are proven by staged trees, not live hosts. |
| C067 | verified | Per-link scopes and split DNS through the native owner pass unit, browser and live networkd and Netplan/networkd acceptance with temporary apply and confirmation. |
| C068 | verified | Explicit clearing is implemented, read back from actual resolved in the namespace fixture and covered by unit and browser cases. |
| C069 | verified | Transport, fallback and trust verification and the independent certificate check pass unit, browser and actual-resolved acceptance; DoH/DoQ is out of resolved's scope and stated. |
| C070 | verified | The chain diagnostic recomputes DS digests that actual resolved also accepted and refuses a non-IANA root, with unit and browser coverage; signature checking stays resolved's, as stated. |
| C071 | verified | The plan/result flow, read-back and rollbacks pass unit, API, browser and actual-resolved acceptance. |
| C072 | verified | Overlap preview and NSS resolution evidence pass unit, API, browser and namespace NSS acceptance. |
| C073 | verified | DoT/DNSSEC comparison and explicit omissions pass unit, API and browser cases with a local verified DoT server. |
| C074 | implemented / acceptance pending | Detection-to-connection handoff and read-only DHCP pass unit, browser and three-engine live reads, but lease rows were only unit-tested (owned engines have no clients) and DHCP stays read-only. |
| F10 | verified | Private names are refused before any public question on native and foreign chains, with acknowledgement for unseen forwarding, in unit, browser and actual-resolved acceptance; container client vantages remain outside this finding's modelled scope. |
