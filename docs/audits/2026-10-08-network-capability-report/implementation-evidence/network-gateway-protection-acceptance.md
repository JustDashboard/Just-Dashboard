# Gateway, NAT and protection maturity (C039–C055)

This checkpoint implements the requested improvements of ledger rows C039–C055 ("Gateway, port
forwarding and NAT" and "Protection, blocklists and kernel settings") on branch
`implement/network-gateway-protection-maturity`, based on `4338e6cc`. It does not edit the ledger;
the proposed statuses below are for the reviewer. Nothing in these checks changed the production
host's firewall, nft tables, NAT, routes, sysctls, blocklists or interfaces: kernel behavior was
exercised only inside network namespaces each test created and deleted, connection-table writes
only inside a namespace by a root copy of the test binary, and feed downloads only from local TLS
fixtures. The [backend contract](../../../internal/backend/gateway-health.md) and
[module guide](../../../internal/backend/network.md#gateway-and-protection) describe the behavior.

## Per row

### C039 — TCP/UDP/both DNAT forwards

Shipped: per-forward `readiness` that keeps installed policy apart from reachability; reachability
becomes `verified`/`failed` only from an enrolled external source's retained TCP measurement of one
of this host's addresses on a published port, completed after the forward's new `changedAt`
(administrators only, joined from the existing external checks module); `POST /gateway/verify`
checks the target from this server (`answering`, `refused`, `timeout`, `unreachable`, UDP
`not_measurable`) and retains the result, stale after a target change. The forward editor shows
both and links to External checks.

Tests: `TestVerifyForwardMeasuresTheTargetFromThisServer`,
`TestExternalEvidenceVerifiesOnlyAfterTheLastChange`, `TestExternalObservationsCarryTheTCPStageAndTheSourceName`,
API route contract and malformed cases; browser "a forward's row says what is installed and what was
measured" and "the editor checks the target, shows external evidence and re-decides".

Limits: no health-driven target failover (deliberately not automated). No real off-host enrolled
source measured a forward here; the join is exercised with retained check records. A local service
on the same port would satisfy an external TCP check; UDP is never measured.

### C040 — Masquerade/fixed-address SNAT

Shipped: NAT `mode` `one-to-one` (address or equal-width network up to 256 addresses, both
directions) and `nptv6` (IPv6 /16–/64 prefix mapping, both directions) rendered as nft prefix NAT
with a marked inbound `dnat` after every forward; destination-scoped masquerade/SNAT
(`destinations`, admission mark scoped the same way); overlap (not only equality) refusal; a guard
refusing a mapping of the address the requester's replies leave from. The NAT editor offers the four
modes, the public side and destinations.

Tests: `TestRenderGatewayModesGolden` (`testdata/gateway-modes.nft`), `TestNormNATModes`,
`TestNATCollisionsCoverOverlapsNotOnlyEqualSources`, `TestMappedNATTranslatesBothDirectionsAndMarksTheInboundHalf`,
`TestOneToOneCannotTakeTheAddressTheOperatorArrivesOn`; live `TestLiveOneToOneMappingTranslatesBothWays`
(visitor seen by the private host, private host seen as the mapped address, through a DROP forward
policy) and `TestLiveIPv6PrefixTranslationMapsBothWays` (host part kept both ways through an ip6tables
DROP policy); browser "a one-to-one NAT entry sends its mode and public side".

Limits: nft prefix NAT is a stateful netmap, not RFC 6296 checksum-neutral NPTv6. A routed public
prefix cannot be checked against provider routing.

### C041 — Forward source restrictions and lifecycle

Shipped: `POST /gateway/preview` for forwards and NAT entries: the save's validation and host checks
without lock, journal or host change, returning refused collisions/guards, partial overlaps and which
rule wins, local listeners a forward or mapping would take (`ss -Hlntup`), limits and blocklists that
also judge the traffic, the first admission insertion of a family, the auto decision, forwarding off,
the connections an edit or disable leaves on the old translation (ctnetlink by mark, translation and
reply source) and the candidate's modeled flows. The editors call it while the form changes.

Tests: `TestForwardPreviewNamesOverlapsListenersAndWhatElseJudgesIt` (also asserts no host change and
an unchanged spec), `TestEditingAForwardCountsTheConnectionsLeftOnTheOldTranslation`,
`TestNATPreviewNamesCollisionsMappingsAndPrecedence`; browser "a new forward's editor previews what
saving would do".

Limits: overlap with Docker's own published-port DNAT is reported only through the listener it
leaves (docker-proxy); the connection count is bounded at 200 000 entries.

### C042 — Automatic forward source-NAT decision

Shipped: every Gateway read re-evaluates each auto forward against the host's current addresses and
uplinks (`decision`: stored, current, drift, reason sentence); the row is tagged and the editor offers
"Decide again" (the existing save, which re-decides every auto forward).

Tests: `TestAutoNATDecisionIsReCheckedAgainstTheHostAsItIsNow`, `TestAGatewaySaveReDecidesADriftedAutoForward`;
browser cases for the tag and the re-decide PUT.

Limits: re-evaluation happens on reads and saves; nothing re-decides automatically in the background.

### C043 — Admission through UFW/Docker and foreign conflict checks

Shipped: per-flow evaluation (`gateway_flows.go`) of each enabled entry's exact flow through every
checked base chain and jumped chain, before and after translation at `dstnat - 10`, with mark
visibility and destination-locality modeling; per-layer verdicts `clear`/`restricted`/`blocked`/
`unknown` with rule position, path and reason; nat-chain awareness and per-layer `uncertain` forms in
the generic check; `requireWritable` re-judges a refused generic check per flow (forwards, NAT, mapped
inbound halves, WireGuard exits, admission repairs). The Policy evidence panel lists each entry's
modeled flow and the unread forms.

Tests: `gateway_flows_test.go` (Docker raw drops, translating nat chains and mark-only mangle rules
clear; explicit port drops block with their rule; source drops restrict; mark admission only after the
mark; jumps; `limit`/`xt` unknown; local targets cross input; mapped inbound halves), live
`TestLiveFlowModelReadsRealIptablesShapes` against iptables-nft's own JSON; browser "policy evidence
shows each entry's modeled flow".

Limits: a model of supported rule forms, not a packet trace; named sets, iptables matches, rate
limits and provider policy stay unknown or restricted; complex trace/probe correlation stays in P4.

### C044 — Per-forward/NAT counters

Shipped: totals per rule comment across table generations (`network_gateway_counters`, additive
schema), with the kernel table handle as the generation, in-place resets detected, vanished rules
carried, the dashboard's own reloads read first and a one-minute recorder; entries return `total`,
views `counters` (generation, last reading, persistence, the stated gap).

Tests: `TestCounterTotalsCarryAcrossGenerations`, `TestCounterTotalsBetweenReadingsIncludeAnUnfoldedReplacement`,
`TestAVanishedRuleOrTableIsCarriedIntoItsTotal`, `TestCounterTotalsPersistAndSeriesRecordDeltasAndGauges`,
`TestADashboardReloadReadsTheOldTableBeforeReplacingIt`, store schema tests; live
`TestLiveExceptionsExpireInTheKernelAndTotalsSurviveReloads` (real handle changes, total ≥ the carried
live count); browser generation line and totals in hints.

Limits: traffic between the last reading and a replacement made outside the dashboard is lost and
said so.

### C045 — Loaded/admission/forwarding readiness

Shipped: per-entry `readiness` (rules installed by comment count against what the entry renders,
partial/missing/drift/not loaded, family forwarding, family admission summed over needed chains,
`ready`) separate from `reachability`; the row and editor show both.

Tests: `TestReadinessSeparatesInstalledPolicyForwardingAndAdmission`, `TestGatewayViewCarriesReadinessTotalsAndFlows`,
live `TestLiveOneToOneMappingTranslatesBothWays` (installed readiness of a real mapping); browser
"Admission missing" and "reached from an external source" cases.

Limits: reachable still needs an enrolled external source; installed never implies reachable.

### C046 — New-flow packet-rate limits

Shipped: eight service profiles that prefill the limit editor (validated by the same rules); per-limit
meter occupancy (`meters`); one-minute refusal history per limit drawn as a sparkline and the
connection-table pressure panel's per-limit refusals.

Tests: `TestNormLimitGlobalCeilingAndProfiles` (every profile is a valid limit),
`TestPressureBreaksTheTableDownAndNamesIndications`; browser "a service profile fills a new limit and
a limit shows what is open now".

Limits: saturation graphs depend on the recorder having run; history is kept seven days.

### C047 — Per-source maximum concurrent connections

Shipped: an explicit ceiling for everyone (`globalConnections`, `ct count over N`, its own counter);
capacity evidence from the connection table: open connections to the limit's ports, busiest sources
against the per-address ceiling and the per-source counter's occupancy.

Tests: `TestNormLimitGlobalCeilingAndProfiles`, golden/`TestMappedNATTranslates…` render check, live
`TestLiveGlobalCeilingRefusesPastItsCount` (three of five connections through a ceiling of three, its
own counter non-zero); browser capacity case.

Limits: open counts approximate the kernel's connlimit accounting (TCP states up to ESTABLISHED).

### C048 — Manual IP/CIDR blocklists

Shipped: `POST /protection/preview` (size, coverage, diff, this host's networks and NAT sources it
covers, trusted networks and exceptions that keep passing, open connections that would continue);
expiring scoped exceptions (`all` or one list, reason, made-by, optional expiry enforced by a
`meta time` match in the rule, a per-list exception chain, a sweeper that tidies the spec, guards).

Tests: `TestBlocklistPreviewNamesLocalNetworksTrustedOverlapAndOpenConnections`,
`TestExceptionsAreRenderedScopedAndExpireOutOfTheSpec`, `TestRemovingTheExceptionThatLetsTheOperatorInIsRefused`,
`TestDeletingAListTakesItsOwnExceptionsWithIt`, `TestCheckExceptionsRefusesWhatWouldBeRendered`,
`TestExceptionScopedToOneListLeavesTheOthersJudging`, live
`TestLiveExceptionsExpireInTheKernelAndTotalsSurviveReloads` (the excepted visitor gets through, then
is refused once the kernel clock passes the expiry with no dashboard action) and
`TestLiveModesGoldenLoadsAndDriftReadsItAsOwned`; browser preview and exception cases.

Limits: kernel expiry needs nft `meta time` (Linux 5.4+); an older kernel refuses the load safely.

### C049 — IPv4/IPv6 country blocking

Shipped: per-zone provenance (URL, country, family, status including an absent IPv6 zone, bytes,
SHA-256, networks, skipped), coverage in addresses and share of the space, and the editor's
approximate-geography explanation (registry allocations, not location; cloud/VPN/mobile collateral);
the preview adds local overlap and open connections.

Tests: `TestCountryProvenanceRecordsAMissingIPv6Zone`, preview tests, `coverageOf` via views; browser
"lists say their schedule…" (coverage) and the preview case (geography).

Limits: geography stays approximate by nature; collateral is estimated from this host's networks and
current connections only.

### C050 — Spamhaus/FireHOL/custom HTTPS feeds

Shipped: `lastDiff` (counts and bounded samples), per-list schedules (6h–168h, manual), conditional
fetch with `ETag`/`Last-Modified` (304 keeps cache and kernel), optional detached Ed25519 signature
verification for custom feeds with a pinned key kept across edits.

Tests: `TestFeedRefreshRecordsProvenanceDiffsAndHonoursValidators`, `TestASignedFeedIsUsedOnlyWhenItsSignatureVerifies`,
`TestEditingASignedFeedKeepsItsPinnedKeyAndToggleKeepsTheSignature`, API refusals (signed preset,
missing key, unknown schedule); browser list row case.

Limits: the presets publish no signature; signing is available only for feeds that provide one.

### C051 — Feed cache/refresh lifecycle

Shipped: adjustable schedules, failure counting with doubling backoff capped by the schedule, a
fifteen-minute scheduler, `nextRefresh`, `stale`, `lastAttempt`, 304 "unchanged" refreshes, and
clock-skew tolerance; F3's cache/render/runtime separation is unchanged.

Tests: `TestRefreshSchedulesBackOffAndNeverFetchManualOnes`, the refresh tests above, existing
`TestRefresherKeepsTryingAListThatFailsAndSaysWhyOnIt` and loop tests.

Limits: refreshes still require the dashboard process; there is no independent host timer.

### C052 — Trusted operator/network exceptions

Shipped: `trustedNotes` (reason, made-by, optional expiry enforced in the rule, last confirmation);
expiring addresses leave the permanent set and the lockout guards; last session or successful sign-in
from inside each kept address (90 days) and `stale` after thirty days without use or confirmation;
`PUT /protection/trusted` for reason, expiry (destructive) and review; the list's confirm and edit
actions and a stale notice.

Tests: `TestTrustedNotesRecordWhoWhyUntilWhenAndStaleness`, `TestOperatorActivityReadsSessionsAndSignIns`;
browser "a stale kept address is said, and confirming it records a review".

Limits: sign-in addresses are evidence, not identity; NAT and VPNs share and change addresses.

### C053 — Established-session protection

Shipped: an explicit revocation workflow: count (`/sessions/preview`) then end (`/sessions/revoke`,
destructive, audited, not journaled) the tracked connections from a network inside an enabled, loaded
list, deleting exact entries over ctnetlink; guards for the requester, trusted networks and host
addresses.

Tests: `TestSessionRevocationEndsOnlyEntriesFromABlockedNetwork`, conntrack netlink unit tests, live
`TestLiveConntrackNetlinkInANamespace` (a real session survives the list, is counted, and stalls
after revocation, with no established entry left); browser count/confirm/end case.

Limits: sockets on this host close on their own timeouts; the revocation is bounded to 20 000
entries per request.

### C054 — Fifteen bounded kernel protections

Shipped: `interfaces` with each device's effective value for the five per-interface settings using
the kernel's combination rules, and four workload profiles staged into the existing apply bar (same
confirmation and weakening gate).

Tests: `TestPerInterfaceEffectiveValuesFollowTheKernelsCombination` (also validates every profile
value against the closed list); browser profile staging and per-interface case.

Limits: bounded to 96 devices (container veths last); profiles are starting points, not tuning.

### C055 — Conntrack occupancy

Shipped: one-minute history of count, maximum and table-full drops/early drops/failed inserts;
`GET /protection/pressure` breakdown by state, protocol, sources and ports over ctnetlink (bounded),
per-CPU statistics and indications with their numbers; the Connection table panel and a tile trend.

Tests: `TestPressureBreaksTheTableDownAndNamesIndications`, `TestCounterTotalsPersistAndSeriesRecordDeltasAndGauges`,
live netlink dump/statistics; browser pressure and read-only cases.

Limits: causes are indications, not diagnoses; the read is bounded at 200 000 entries.

## Check results

Raw logs are in [`network-gateway-protection/`](network-gateway-protection/). All Go commands ran
from `backend/` with `GOMAXPROCS=2 GOFLAGS=-p=2` and a private `TMPDIR`/`GOTMPDIR`; every build,
server and browser run held the shared heavy-work lock, served on loopback port 43214 with one
worker, and stopped its server.

| Check | Command | Duration | Result |
| --- | --- | --- | --- |
| netx unit | `go test ./internal/netx -count=1 -v` ([log](network-gateway-protection/netx-unit.log)) | 23.8 s | 634 passed, 37 skipped (live and environment-gated), 0 failed |
| netx, store | `go test ./internal/netx ./internal/store -count=1` | 30 s | both packages ok |
| API (network) | `go test ./internal/api -run 'Network\|Gateway\|Pending\|External\|Protection' -count=1` | 35 s | ok (route contract, capabilities, malformed requests, pending enrollment, evidence mapping) |
| New netx code under race | `go test -race ./internal/netx -run '<new tests>' -count=1` | 9.4 s | ok |
| Namespace lane, race | `JD_NETNS_LIVE=1 go test -race ./internal/netx -run Live -skip NativeManager -count=1 -v` ([log](network-gateway-protection/netx-live-race-2.log)) | 4 min 19 s | 32 passed, 3 skipped (nested helper, systemd timer and WireGuard dual-stack prerequisites), 0 failed; no namespace left |
| Namespace lane, first attempt | same without `-skip` | 7 min 19 s | all gateway/protection cases passed; `TestNativeManagerOwnerLive` and `TestNativeManagerAutomaticOwnerLive` failed on their missing `JD_NATIVE_MANAGER_NM_ROOT` prerequisite (unrelated to this change), retained as [`netx-live-race-1.log`](network-gateway-protection/netx-live-race-1.log) |
| Frontend unit | `bun test src` | 3.6 s | 3,273 passed |
| Format, lint, types | Prettier and ESLint on the 24 changed frontend files, `tsc --noEmit` | — | clean |

The first selected browser run of `network-gateway.spec.ts` ([log](network-gateway-protection/gateway-spec-1-initial-failed.log))
had 32 passes and 6 failures: the editor's preview POST was counted by a test that compares every
request, a new "Only to these networks" label collided with the existing `Network` field selector,
the policy panel did not render for a fixture without admission chains, list coverage was drawn only
beside cache evidence, and a stale manual-list preview hid the country explanation. The UI and the
tests were corrected (preview reads filtered from exact request assertions; the label renamed to
"Only to these destinations"; the panel shown when flows exist; coverage drawn on its own; a
preview bound to the body it was asked for). The final results follow.

### Final gate

The final session ran on commit `c8fc7448` (every later commit adds only this document and its
logs). Production build: 18 s (`run2-build.log`, warm cache). The gateway and policy-evidence specs,
with the six review-screenshot cases enabled, passed **55 of 55** in 1.8 min
([log](network-gateway-protection/run2-gateway-specs.log)); the screenshots at 390 and 1440 px of
both pages, the forward editor and the blocklist dialog were inspected.

`scripts/test-changed.sh 4338e6cc` exited **0** in 535 s
([log](network-gateway-protection/run2-test-changed.log)): 74 changed files; Prettier and ESLint on
the changed frontend files and `tsc --noEmit` clean; `bun test src` 3,273 passed; `go build ./...`,
`go vet` and the tests of `./internal/api`, `./internal/netx` and `./internal/store` ok; selected
browser specs `design-system`, `navigation`, `network-audit`, `network-gateway` and
`network-policy-evidence`: **151 passed**, 6 skipped (the screenshot cases, which need
`JD_NETWORK_SHOTS`), 0 failed, 5.9 min with one worker. The previous complete gate on `792ce9a6`
also passed with the same counts ([log](network-gateway-protection/run1-test-changed.log)).

## Proposed ledger statuses

| Row | Proposed | Reason |
| --- | --- | --- |
| C039 | implemented / acceptance pending | Readiness, target check and external-evidence join ship with tests and UI; no real off-host source measured a forward, and target failover is not built. |
| C040 | verified | One-to-one, IPv6 prefix and destination-scoped NAT carry live namespace traffic both ways, with regressions and UI; the prefix mapping is stateful, not RFC 6296. |
| C041 | verified | Overlap/conflict refusal and the change preview pass unit, API and browser acceptance. |
| C042 | verified | Drift is re-checked on every read and re-decided by a save, with unit and browser coverage. |
| C043 | verified | Supported explicit-rule inspection per flow and per-layer uncertainty pass unit, real-iptables namespace and browser checks; packet tracing stays in P4. |
| C044 | verified | Totals carry across real table generations; persistence, labels and UI pass. |
| C045 | verified | Installed policy, forwarding and admission are separate from measured reachability in API and UI; measuring reachability needs enrolled sources (P6). |
| C046 | verified | Service profiles, meter occupancy and refusal history pass unit and browser checks. |
| C047 | verified | The ceiling for everyone holds in a namespace; capacity evidence and UI pass. |
| C048 | verified | Preview and scoped exceptions pass; kernel-enforced expiry is proven live without dashboard action. |
| C049 | verified | Provenance, coverage, collateral and the geography explanation pass unit and browser checks. |
| C050 | verified | Diffs, schedules, conditional fetch and Ed25519 verification pass against local TLS fixtures and in the UI. |
| C051 | implemented / acceptance pending | Schedules, backoff, next/stale and 304 handling pass; refreshes still need the dashboard process. |
| C052 | verified | Notes, kernel-enforced expiry, staleness from sign-ins and review pass unit and browser checks. |
| C053 | verified | Explicit revocation ends a real session in a namespace after the list alone did not; API and UI pass. |
| C054 | verified | Per-interface effective values and workload profiles pass unit and browser checks. |
| C055 | verified | History, ctnetlink breakdown, statistics and indications pass unit, live netlink and browser checks. |

