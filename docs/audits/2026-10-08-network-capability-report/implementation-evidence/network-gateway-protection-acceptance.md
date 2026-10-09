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

Recorded in the final section below with commands, durations and counts; raw logs are kept beside
the worktree in `Just-Dashboard-net-gateway-artifacts/logs/`.
