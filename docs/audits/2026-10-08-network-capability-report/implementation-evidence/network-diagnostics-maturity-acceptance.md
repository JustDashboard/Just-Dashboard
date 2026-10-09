# Diagnostics maturity acceptance (C088–C114)

This records what shipped for the diagnostics package on branch
`implement/network-diagnostics-maturity` (from PR head `4338e6cc`), the tests that prove each row,
what remains outside the evidence, and the final scoped check. Raw logs are kept outside the
repository in `/home/ubuntu/Just-Dashboard-net-diagnostics-artifacts/`. It changes no ledger status;
the proposed status per row is for the ledger owner.

## What changed for every tool

All 26 server tools now return structured evidence beside their verbatim output: a verdict that
does not over-claim (`ok`, `findings`, `unknown`, `failed`), a summary, facts labelled with how they
were obtained, ordered stages, tables with optional in-product row links, owner-specific findings,
links to the page that owns the next question, comparable metrics and limitations
(`backend/internal/netsec/probe_evidence.go`). Saved runs bound that evidence, map `unknown` and
`findings` verdicts to `completed_with_unknowns`/`completed_with_findings`, compare structured lines
with timing columns ignored, expose per-request metric history (`GET /diagnostics/{id}/history`)
and metric changes, and can save a quick result the server still holds (`POST /diagnostics/results`)
without running the tool again. One frontend renderer
(`frontend/src/components/network/tools/probe-evidence.tsx`) serves the quick card and the saved-run
inspector. The [diagnostics document](../../../internal/backend/network-diagnostics.md#structured-evidence)
describes each tool's readings, bounds and routes.

Browser acceptance is `frontend/tests/browser/network-tools.spec.ts`: ten workflow cases, page
containment at 375 and 1280 pixels, and one visible-evidence case for each of nineteen tools driven by
[`fixtures/network-tool-results.ts`](../../../../frontend/tests/browser/fixtures/network-tool-results.ts),
which mirrors the backend shapes the Go tests produce.

## Per row

| Row | Shipped | Tests | Remaining limits | Proposed status |
| --- | --- | --- | --- | --- |
| C088 DNS lookup | NXDOMAIN and NODATA told apart from the wire response code; answers attributed to the hosts file (NSS `files`) or to each configured nameserver asked directly (UDP, TCP after truncation) with rcode/TTL/time; NSS order, nameservers and search domains as configured facts; hosts-override finding; loopback stub links to the DNS page. | `TestDNSWireReadsEverySectionAndRetriesTruncationOverTCP`, `TestResolverConfigurationReading`, `TestLookupReportsResolverProvenanceAndWireAnswers`, `TestLookupSeparatesNoDataFromNonexistentNames`, `TestHostsOnlyNamesAreNotCalledOverrides`; browser `dns shows its structured readings`. | Reads the dashboard process's own resolver files; encrypted transport used by a stub and NSS modules beyond `files`/`dns` are stated, not observed. | verified — provenance implemented, unit/loopback-wire tested, visible in UI. |
| C089 DNS authority | Zone apex discovery, parent referral with delegation set and glue (checked against the nameserver's records), every authoritative address asked SOA/NS with recursion off; lame, silent, glue, delegation, NS-set and serial findings. | `TestDNSAuthorityChecksDelegationGlueAndConsistency` (7 mutations), `TestDNSAuthorityLiveWireAgainstLoopbackAuthority`; browser `dnsauth …`. | Asked from this host only (anycast/region differences); 12-address and 40 s bounds. | verified — all three checks implemented and tested, visible. |
| C090 Ping | Loss, min/avg/max/mdev and consecutive-reply jitter metrics, reply table; silence is `unknown` with a port-check link; ICMP error replies distinguished. History via saved-run metric history. | `TestPingParsesLossJitterAndRepliesFromIPv4AndIPv6`, `TestPingVerdictsNeverCallUnansweredEchoDown`, `TestStructuredVerdictsDecideStatusAndOutcome`, `TestHistoryAndComparisonUseStructuredEvidence`; browser `an unanswered ping reads as inconclusive…`, `a saved run shows its structured evidence, metric history…`. | History is bounded by the retention policy; ICMP behaviour differs from service traffic (stated). | verified |
| C091 Traceroute | Hop table (address, round trips, annotations, asymmetry) for traceroute and tracepath, `hop N` records compared across saved runs, hop metrics in history; unreached destination is `unknown`. | `TestTracerouteBuildsHopTableAndComparableRecords`, `TestTracepathFallbackMergesDuplicateHopsAndReadsPMTU`, comparison/history tests above; browser `…hop tables render for traceroute`. | One probe per hop; ECMP/direction differences stated, not resolved. | verified |
| C092 Route lookup | Parsed kernel decision; with a port and protocol the dispatcher joins host-source policy rules, modeled firewall, NAT candidates and egress owner from the connection-path investigator (no measurement). | `TestRouteGetParsesKernelDecisionInAnyKeyOrder`, `TestJoinProbeKeepsLayerBasesAndOwnerLinks`, `TestRouteLookupJoinsPathLayersAndSupportReportsProbes` (real router, loopback); browser `a route lookup with a port…`. | Firewall layer is the investigator's modeled prediction (foreign chains, conntrack and provider policy remain unknown); no container source here. | verified — join implemented with stated model limits. |
| C093 Path MTU | Discovered MTU only on a Resume line after reaching the destination; otherwise unknown with the largest size seen, or filtered; smaller-than-first-hop finding. | `TestPathMTUSeparatesDiscoveredFromUnknownAndFiltered`, `TestPathMTUUsesIPv6AndReportsMissingTool`; browser `mtu …`. | Depends on routers returning ICMP (stated). | verified |
| C094 TCP port check | Connected/attempted address and local source; the same tuple's route, firewall, NAT, listener-owner and proxy layers joined; disagreement findings (connected without owner, listener not reached, model predicted block). | `TestCorrelatePortNamesDisagreementsWithoutChoosingACause`, `TestJoinProbeKeepsLayerBasesAndOwnerLinks`; browser `port …`. | Listener ownership only for local destinations; correlation never names the deciding layer. | verified |
| C095 Common-port scan | Name resolved once and pinned; every catalogue port listed open/closed/no reply/error with time; exact coverage, method and not-covered facts; dangerous open ports as findings. | `TestPortScanPinsOneAddressAndReportsExactCoverage`; browser `scan …`. | Still TCP catalogue only — no UDP or full-range scanner, by design (stated). | verified |
| C096 Banner grab | Grammar-based identification (SSH, RFB, MySQL handshake, POP3, IMAP, SMTP/FTP 220) with confidence and basis; product/version marked self-reported. | `TestBannerIdentificationLabelsConfidence` (14 cases), `TestBannerGrabReportsSelfReportedSoftware`; browser `banner …`. | Banners can be forged (stated); no active protocol probing. | verified |
| C097 SSH host keys | Saved trust per host:port (`observed` keys taken server-side from the requester's held SSH scan with its own host, or `entered` out of band) in the settings table; scans compare: matches, DIFFERS (critical), not saved, saved but not offered; without trust the verdict is unknown and an unreadable store says so. New trust is a PUT; replacing existing trust and forgetting are destructive routes with UI confirmation. | `TestSSHTrustValidationAndComparison`, `TestSSHScanFingerprintsOfferedKeys`, `TestSSHTrustPersistsReplacesAndForgets`, `TestSavedQuickResultHistoryTrustAndDevicesAreAdminAndAudited`, `TestUnreadableSSHTrustIsNotReportedAsNoneSaved`; browser `SSH host keys are trusted from the held scan…`. | A match is only as trustworthy as the first fingerprint (stated in UI and result). | verified |
| C098 HTTP inspection | Per-request DNS/connect/TLS/first-byte/total timing and first-request stages; transport trust verified separately; untrusted-200 and HTTPS→HTTP downgrade findings; failed stage named. | `TestHTTPCheckSeparatesTransportTrustFromTheResponseAndTimesStages`, `TestHTTPCheckFlagsDowngradeAndNamesTheFailedStage`, `TestHTTPCheckReportsStatusRedirectAndHeaders`; browser `http …`. | Timings are from this host; body is not read. | verified |
| C099 Header grade | Response kind from `Content-Type` (page/API/asset/unknown) or chosen profile; each header required/recommended/optional/not applicable; only applicable headers graded and advised. | `TestHeaderGradeAppliesOnlyWhatFitsTheResponseKind`, `TestHTTPSecurityGradesHeaders`; browser `httpsec …`. | Only the final response is graded (stated). | verified |
| C100 TLS certificate | Chain table (position, subject, issuer, validity, key, signature, SHA-256), verified root, trust and expiry facts, fingerprint/expiry records compared across saved runs, weak-crypto and expiry findings. | `TestTLSCertStructuresTheChainForComparison`, `TestTLSCertInspectsThePresentedCertificate`, structured comparison test; browser `tls …`. | Revocation is not checked (OCSP staple presence only). | verified |
| C101 TLS version survey | Each version on its own TCP connection; protocol rejection, no common parameters, closed during handshake, not TLS and timeout separated from TCP network failure (later versions not tested); links to the Proxy TLS report. | `TestTLSSurveySeparatesRejectionFromNetworkFailure`, `TestClassifyHandshake`, `TestTLSSurveyAgainstLocalServer`; browser `tlssurvey …`. | Go client cipher coverage for TLS 1.0/1.1 is limited (stated as a fact). | verified |
| C102 Combined site audit | HTTP/certificate/header stages; every finding carries owner and action; the dispatcher names the proxy site serving the host (certificate → proxy certificates, headers → that site). Any quick result saves from the server's 15-minute hold without rerunning. | `TestSiteAuditNamesOwnersAndStages`, `TestSiteAuditDoesNotFailAnAuthenticatedSite`, `TestSiteAuditMergesThreeSections`, `TestAdoptSavesTheServersOwnResultWithoutRunning`, `TestQuickProbeOffersAHeldResultAndSiteOwnersResolve`; browser `siteaudit …`, `a quick result is saved…`. | Owner attribution covers this dashboard's proxy sites; foreign hosts get a generic owner. | verified |
| C103 Mail path | Stages: MX (failed lookup ≠ none; null MX; implicit MX), exchanger addresses, SPF, DMARC, SMTP connect, greeting; port-25 timeout is unknown; not-checked list (DKIM, PTR, reputation, delivery). | `TestMailPathReportsEveryStageWithinTheEvidence` (9 variants); browser `mx …`. | One connection from this host; no DKIM or delivery test (stated). | verified |
| C104 STARTTLS | Connect, greeting, protocol capability listing (EHLO/CAPABILITY/CAPA/FEAT), upgrade, handshake and certificate stages with skip semantics. | `TestSTARTTLSReportsProtocolSpecificStages` (4 protocols, refusal, hidden capability, closed port), `TestSTARTTLSAgainstFakeSMTP`; browser `starttls …`. | Implicit-TLS ports remain the TLS tool's. | verified |
| C105 DNS blocklists | Per-list listed/not listed/query refused (Spamhaus 127.255.255.x)/query failed/unexpected answer with code, reason, time asked and duration; unanswered lists keep the verdict unknown; retained in saved runs. SORBS dropped (decommissioned). | `TestDNSBLSeparatesQueryFailuresFromNotListed`; browser `dnsbl …`. | Uses this host's resolver; IPv6 list coverage varies (stated). | verified |
| C106 ASN ownership | Named source (Team Cymru), announcements table, registration country labelled as not a location, multiple-origin finding, unannounced as unknown. | `TestASNLookupLabelsSourceAndGeographyUncertainty`; browser `asn …`. | Third-party source (stated). | verified |
| C107 Whois | Normalised fields per registry spelling, each present/redacted/absent; registry "no match" distinct from lookup failure (registration unknown); expiry finding. | `TestWhoisNormalisesAndSeparatesRedactedAbsentAndFailed`; browser `whois …`. | Heuristic key mapping; unusual registries fall back to raw output with an unknown verdict. | verified |
| C108 Listeners | Socket table with each row linking to `/proxy/ports?q=:PORT&socket=…` and a Ports link. | `TestListenersStructureRowsAndLinkToPorts`; browser `listeners link each socket to Ports…`. | Row link matches the Ports page's socket key; the exposure grade itself stays on that page. | verified |
| C109 Egress | Per-family route/next hop/interface/source/scope and default-route count, IPv6-only handled, private/CGNAT NAT findings, link to external checks, public-address non-claim. | `TestEgressClassifiesSourcesPerFamilyWithoutClaimingThePublicAddress`, `TestEgressSupportsIPv6OnlyHosts`; browser `egress …`. | The public address still needs an external vantage. | verified |
| C110 Neighbours | State meanings, router flag, per-interface summary with role and local addresses, passive-read and not-a-scan facts. | `TestNeighboursExplainCacheStatesAndInterfaces`; browser `neigh …`. | Cache only, by design. | verified |
| C111 Host support | Read-only capability probes (AF_PACKET open/close, `nft list tables`, iptables backend, per-interface IPv6, forwarding, WireGuard/CAKE modules, conntrack, bpffs, resolved) in `/capabilities` and the tool. | `TestHostSupportProbesCapabilitiesBeyondBinaries`, `TestHostSupportProbesWithoutBinariesAreUnknown`, `TestRouteLookupJoinsPathLayersAndSupportReportsProbes`; browser `capabilities …`. | Probes test the dashboard process and host kernel, not NIC/provider support (stated). | verified |
| C112 Packet snapshot | Structured snapshot (packet count, limits) with a link that opens retained PCAP capture setup prefilled; nothing captures on arrival. Gated owned-namespace cost measurement added. | `TestPacketSnapshotHandsOffToRetainedCaptures`, `TestLiveCaptureCostAtFullBounds` (see below); browser `a packet snapshot hands its settings…`. | Cost was measured on this host's capture path in an owned namespace, not on a production interface under real traffic. | implemented / acceptance pending — production cost acceptance on a live interface remains the ledger's open item. |
| C113 Wake-on-LAN | Saved devices (settings table, admin-only, audited, delete destructive) that fill the inputs; optional literal verification address and TCP port (ICMP otherwise) checked once before and then every 3 s for up to 60 s; neighbour-cache hint; silence, a device already answering before the packet, or a check that cannot run are all unknown. | `TestWakeOnLANMeasuresVerificationWithinItsWindow`, `TestWakeDevicesValidateCreateUpdateAndDelete`, `TestProbeRequestNormalisesNewOptions`, API route test; browser `saved Wake-on-LAN devices…`. | No real device was woken on this host; verification is proven against fixtures. | implemented / acceptance pending — needs a measured wake of a real LAN device. |
| C114 Subnet calculator | Exact local prefix comparison (same/contains/inside/separate/family) for every user; admin-only "Check shared IPAM" through the existing audited preview with conflicts, and a link to IPAM where reservation enforces overlap prevention; readers never query IPAM. | `subnet-math.test.js` prefix relations; browser `the subnet calculator compares prefixes locally and checks shared IPAM for admins`, `readers keep the local calculator…`. | The IPAM page is not prefilled from the calculator (left to avoid editing another package's page). | verified |

## Capture cost measurement (C112)

`JD_NETCAPTURE_LIVE=1 go test ./internal/netcapture -run TestLiveCaptureCostAtFullBounds -v` on
this host (owned namespace `jd-cap-*`, removed afterwards; none left):

| Scenario | Wall | Packets | Artifact | Stop | Native tree CPU | Native peak RSS | Backend CPU | Backend peak heap growth |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| UDP flood at 10,000 packets / 2 MiB / 512-byte snapshots | 103 ms | 4,578 | 2,096,748 B | byte_limit | 28 ms | 16,768 KiB | 36 ms | 3,952 KiB |
| Idle to a 10 s time bound | 10.038 s | 0 | 24 B | time_limit | 21 ms | 17,024 KiB | 149 ms | 240 KiB |

The idle backend CPU includes the test's own 20 ms heap sampler. Raw log:
`capture-cost.log` in the artifacts directory.

## Final checks

All on source `c5085d31` with `TMPDIR`/`GOTMPDIR` on disk, `GOMAXPROCS=2 GOFLAGS=-p=2` and one
browser worker against a production build of this tree served on port 43211. Browser and
`test-changed` runs held `/home/ubuntu/.jd-heavy.lock` for the build, server and run.

| Check | Duration | Result |
| --- | --- | --- |
| `scripts/test-changed.sh 4338e6cc` (73 changed files) | 825 s, after a 24 s build | exit 0: Prettier clean, ESLint and `tsc --noEmit` clean, `bun test src` 3,275 pass / 0 fail, `go build ./...` and `go vet` on changed packages clean, Go tests for `api`, `netcapture`, `netdiag`, `netpath`, `netsec`, `netx` ok, 386 browser cases passed and 60 skipped |
| `go test -race -count=1 ./internal/netdiag ./internal/netpath` | 86 s | ok |
| `go test -race` focused selections in `netsec`, `api` (new and existing diagnostic routes) and `netx` (`TestHostSupport`) | 6 s / 10 s / 1 s | ok |
| `JD_NETCAPTURE_LIVE=1 go test ./internal/netcapture -run TestLiveCaptureCostAtFullBounds` | 10.5 s | ok (table above) |

The 60 skipped browser cases are the optional screenshot captures in `network-dns`,
`network-gateway`, `network-ui`, `security-intrusion` and `security-ui`, which run only with
`JD_NETWORK_SHOTS` set; none belongs to this change. `network-tools.spec.ts` ran all 31 of its cases.

The first full run (on `6e088b40` plus the new per-tool cases) failed 11 browser cases: ten in
`network-runs.spec.ts`, whose fixture answered the new `/history` read with a run object and broke
the inspector, and the `mtu` evidence case, whose text matched a hidden card first. The history read
is now validated (an incomplete body becomes a failed read with retry), the fixture answers
`/history`, and the evidence cases ignore hidden cards. A read-only review of the same diff then
found trust-binding, replacement, NODATA, site-audit, wake and bounding issues; they are fixed in
`c5085d31` with regression tests before the passing run above. Raw logs: `test-changed-1.log`,
`test-changed-2.log`, `spec-run-1.log` and `capture-cost.log` in the artifacts directory.
