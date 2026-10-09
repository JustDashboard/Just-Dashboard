# VPN and mesh maturity acceptance (C056–C065)

This records the VPN and mesh package of the feature-by-feature maturity work, branch
`implement/network-vpn-maturity` from PR checkpoint `4338e6cc`. The behaviour and its boundaries
are documented in [the WireGuard record and lifecycle](../../../internal/backend/wireguard-lifecycle.md).
P16 (peer key rotation, expiring invitations, groups, route approval, enrolment) is a separate
package and was not implemented here, so the parts of C058, C059, C064 and C065 that belong to it
remain open. This evidence changes no ledger status; proposed statuses are listed at the end.

Raw logs are kept beside this record in [`network-vpn-maturity/`](network-vpn-maturity/), listed
under each check.

## Per row

### C056 WireGuard inventory, handshakes and transfers

Shipped: a five-minute record of per-peer counters (reset-aware deltas), daily usage and the
endpoints each peer was seen from (`wgrecord.go`, additive `network_wg_*` tables); handshake
classification (online, stale for always-on peers, idle, never) with stale/recovered transitions
in the lifecycle; alerts on the tunnel; `GET …/history` trends per tunnel or peer over 6 h, 24 h or
7 d; the peer sheet's trend chart, "Seen from" list and history, and stale marks in the tunnel
picture.

Tests: `TestWireGuardRecordKeepsTrendsUsageEndpointsAndStaleTransitions`, `…WithoutRetention…`,
`…PrunesAndBounds…`, `TestWireGuardHandshakeStates`, `TestWireGuardStaleSitesRaiseAlerts`,
the store schema fresh/upgrade test, `TestWireGuardRecordRoutes` (API), the native site fixture's
real-kernel record pass, and browser cases "stale handshakes, captured transports and passed
budgets are called out" and "a peer's record shows its trend, where it dialled from and its
history".

Limits: five-minute grain; samples kept no longer than the metrics retention and at most a week;
traffic between a peer's last sample and an interface restart is counted from the new counter;
endpoints are bounded to the newest 20 per peer.

### C057 One-step WireGuard server creation

Shipped (on top of P10's dual-stack allocation): endpoint evidence at creation and on demand
(on-host public, provider-mapped, elsewhere, private, unresolved; listening socket; reachability
always "not tested"), an Advanced fold with MTU and hand-typed resolvers, and automatic IPv4 /24
and random ULA /64 allocation that steers around every held shared IPAM reservation
(`netipam.HeldPrefixes`).

Tests: `TestWireGuardEndpointEvidenceVerdicts`, `TestWireGuardEndpointOfATunnel`,
`TestCreateWireGuardAvoidsHeldPlanningSpace`, the netipam held-prefix assertions, browser cases
"setup's advanced settings send the MTU and own resolvers it checked" and "the tunnel history
checks its endpoint".

Limits: evidence is local; the provider's UDP admission and an off-host client remain untested.
A name is resolved with the dashboard's own resolver.

### C058 Key generation, sealed client config, QR, copy, download

Shipped: forgetting is now worded and recorded as distinct from revoking ("This does not revoke…;
to cut it off, remove the peer"), a `config_forgotten` lifecycle event, and a stored configuration
is only shown, forgotten or edited when its row's public key matches the peer of that id in the
tunnel's file (`peerClient`).

Tests: `TestStoredConfigurationOfAnotherPeerIsNeverShown`, the adapted
`TestWireGuardPeerConfigRoute`, browser case "forgetting a saved copy says it does not revoke the
peer".

Not done: rotation and one-time expiring enrollment, which are P16.

### C059 Device peers and split/full client configuration

Shipped: `PATCH …/peers/{id}` editing (name, keepalive; a site's networks, endpoint and keepalive
on this server under every add-time guard; a device's full-tunnel/shared routes regenerated from
the sealed copy with the same keys and returned once), destructive budget for withdrawals,
refusal of edits that would cut the operator's own path, rollback with recorded outcome; recorded
device routes (`client_routes`, an added column); alert-only usage budgets per UTC day/week/month.

Tests: `TestEditWireGuardDeviceRoutesRegeneratesItsConfiguration`,
`TestEditWireGuardSiteNetworksAndName`, `TestEditWireGuardPeerRefusals`,
`TestEditWireGuardSiteEndpointResolvesNamesAgainstItsNetworks`,
`TestEditWireGuardPeerFailedReloadIsPutBackAndRecorded`,
`TestEditWireGuardSiteWithoutItsConfigurationChangesThisServer`,
`TestWireGuardQuotasMeasureUsageAndOnlyAlert`, `TestWireGuardPeriodStarts`,
`TestWireGuardSiteEditSpendsTheBudgetOnlyToWithdraw` and `TestWireGuardRecordRoutes` (API), the
pure `record-logic` tests, browser cases for device editing, site withdrawal confirmation and
budgets.

Not done: expiry and groups (P16). Budgets never enforce.

### C060 Site-to-site peers and remote routes

Shipped: continuous transport protection — every WireGuard endpoint the kernel reports (resolved
names, roaming peers) is a client-path anchor, so a guarded change that would route one into a
tunnel or nowhere is put back; the record detects and records captured transports made outside
the dashboard; site add/edit resolves host-name endpoints and refuses networks containing them.
Site verification (`POST …/verify`): handshake, routes, transport, tunnel-address answer and an
optional LAN round trip sourced from this server's tunnel address, with the far side's own checks
listed and the outcome recorded.

Tests: `TestGuardedChangesAnchorWireGuardTransports`,
`TestWireGuardTransportChecksFollowTheSocketAndRecordCapture`, `TestVerifyWireGuardSiteEndToEnd`,
`TestVerifyWireGuardSiteThatIsQuietFails`, the native site fixture (real end-to-end verification,
degraded verification when the site stops forwarding, refused capturing route with no route or
spec left behind), browser case "a site is verified end to end".

Limits: the far side's checks are a list for its operator; there is no remote agent. At most 32
endpoints are anchored or checked.

### C061 WireGuard internet exit

Shipped: each family's exit reports `translated`, the owned masquerade rule's count of client
connections sent through it since the rules loaded; leak behaviour of full tunnels is measured by
the kill-switch fixture below.

Tests: `TestWireGuardExitCounterIsTheOwnedMasqueradeRule`, the native kill-switch fixture's
both-family counters after real client traffic, browser case "exits show how many client
connections they translated", the unchanged P10 dual-stack native fixture.

Limits: provider/public reachability beyond the host remains unproved.

### C062 Full-tunnel IPv6 containment

Shipped: an opt-in Linux wg-quick kill-switch export of a full-tunnel device (nftables output
filter; tunnel, loopback, WireGuard's marked transport, DHCP and neighbour discovery only), text
download only, audited, refused for split tunnels and sites.

Tests: `TestWireGuardKillSwitchVariant`, `TestWireGuardKillSwitchVariantIsANoStoreAuditedText`
(API), the native fixture (plain profile leaks through a specific native route and after an abrupt
interface loss in both families; the variant holds through both, resolver traffic works inside the
tunnel, a deliberate down restores the native path), browser case "a full-tunnel device offers its
Linux kill-switch file as a download".

Limits: Linux wg-quick with nftables only; phone and Windows clients rely on their own platform
switches; real-client production leak acceptance remains pending.

### C063 Tunnel up/down/removal and archive preservation

Shipped: a durable lifecycle history with recovery outcomes for every tunnel and peer operation
(failed vs degraded rollback), firewall outcomes, and restore from the archive with the checks a
new tunnel passes, client-path comparison and rollback into the archive; the archive panel and the
tunnel's history sheet.

Tests: `TestWireGuardArchiveListsWithoutKeys`, `TestWireGuardArchiveRefusesWhatNoLongerFits`,
`TestRestoreWireGuardPutsTheTunnelBack`, `TestRestoreWireGuardThatWillNotStartReturnsToTheArchive`,
`TestRemoveWireGuardRecordsTheArchiveAndWithdrawnExit`, the creation rollback history assertions,
the native site fixture's remove/archive/restore with the site's unchanged configuration
reconnecting and the exact recorded history, browser cases for restore and the lifecycle list.

Limits: saved client configurations and the exit are not restored; WireGuard file/unit operations
keep their synchronous rollback rather than the network module's independent recovery journal.

### C064 Tailscale self, peers, exit and subnet offers

Shipped: approval state of each advertised offer from this node's own status (served or not),
this node's key expiry with a warning within fourteen days, and Tailscale's health messages.

Tests: `TestTailscaleApprovalOfWhatThisServerAdvertises`,
`TestTailscaleUnreadablePreferencesClaimNoApproval`, `TestTailscaleWarnsBeforeThisNodesKeyExpires`,
browser case "the tailnet says which offers it serves…".

Not done: approving routes and reading tailnet policy (control server / P16). The live Tailscale
on this host was not touched; readings are exercised against recorded status shapes.

### C065 Headscale users and nodes (inspection)

Shipped: key matching without regard to case or underscores (Headscale's CLI prints snake_case
through `encoding/json`, which the earlier camelCase decoder mostly missed), node expiry, valid
tags, advertised/approved routes from the node (0.26+) or the older `routes list`, unknown routes
kept unknown, and the panel for a Headscale in a container (read but never shown before).

Tests: `TestHeadscaleSnakeCaseNodesWithTheirOwnRoutes`, the updated binary and container tests,
browser case "a Headscale in a container shows its users, expired keys and routes awaiting
approval".

Not done: enrolment, keys, approval, user lifecycle and policy (P16). No Headscale was available
to run; shapes follow its CLI's encoding of the protobuf messages.

## Checks

All on application source `9b77e305` unless noted.

| Check | Result | Raw log |
| --- | --- | --- |
| `JD_BROWSER_BASE_URL=http://127.0.0.1:43215 JD_BROWSER_WORKERS=1 scripts/test-changed.sh 4338e6cc` against a production build of the same tree | **exit 0 in 590 s**: Prettier, ESLint and `tsc` clean; 3,276 Bun tests; `go build`/`go vet`; `internal/api`, `internal/netipam`, `internal/netx`, `internal/store` passed; **217 browser passes, 28 optional screenshot skips, 0 failures** across ten selected specs including all 14 `network-vpn-maturity` cases | [log](network-vpn-maturity/test-changed-final.log) |
| `JD_NETNS_LIVE=1 go test -race ./internal/netx -run '^TestLiveWireGuard'` with task-owned `wireguard-tools` 1.0.20210914 on `PATH` | **PASS in 77.8 s**: P10 dual-stack fixture (both transports), kill-switch fixture (IPv4 and IPv6 exits each translated 2 client connections), site fixture (verified, degraded, refused capture, removed/restored, exact history) | [log](network-vpn-maturity/wireguard-live-race-final.log) |
| Earlier selected browser run: maturity, network-ui/audit/report/wireguard-dual/ipam and design-system specs | 164 passed, 14 optional skips | [log](network-vpn-maturity/browser-vpn-selected-attempt2.log) |
| Whole `internal/netx` package (non-live), first integration | ok, 25.6 s | [log](network-vpn-maturity/netx-package-initial.log) |

No host interface, route, firewall rule, Tailscale state or service was changed. Every WireGuard
interface, route, nftables table and process in the native fixtures lived in task-owned
namespaces; after each run no `jdt*` namespace and no host WireGuard link remained.

### Failed iterations retained

- [`browser-maturity-attempt1`](network-vpn-maturity/browser-maturity-attempt1.log): the first run of the new spec, 11 passed and 3 failed. All
  three were test locators — a label matching both the budget block and its meter, a history GET
  the spec itself recorded among the mutations, and a text matching both a status and a sentence.
  Only the locators were changed.
- [`site-live-attempt1`](network-vpn-maturity/site-live-attempt1.log): the first site fixture failed after its verification and guard steps
  because the test read `Device "jdst" does not exist.` as the interface still existing; the
  check now uses the command's exit status. [`site-live-attempt2`](network-vpn-maturity/site-live-attempt2.log), [`site-live-attempt3`](network-vpn-maturity/site-live-attempt3.log) and the final run pass; the separate [kill-switch run](network-vpn-maturity/killswitch-live-attempt1.log) passed first time.

### Screenshots

Captured from the same production build with the maturity fixture (settled after the sheet's
entry animation): [VPN page, 1440](network-vpn-maturity/vpn-page-1440.png),
[site sheet, 390](network-vpn-maturity/vpn-site-sheet-390.png),
[budget and history, 1440](network-vpn-maturity/vpn-site-budget-1440.png) and
[tunnel history, 1440](network-vpn-maturity/vpn-tunnel-history-1440.png).

## Proposed ledger statuses

| Row | Proposed | Reason |
| --- | --- | --- |
| C056 | verified | Trends, stale-handshake alerts and endpoint history ship with unit, API, real-kernel record and browser acceptance. |
| C057 | implemented / acceptance pending | Endpoint evidence, advanced settings and IPAM-aware defaults pass; provider-public reachability is inferred locally, never measured off-host. |
| C058 | in progress | Forget-versus-revoke and stored-copy identity shipped; rotation and one-time expiring enrollment belong to P16. |
| C059 | in progress | Peer editing and alert-only budgets pass; expiry and groups belong to P16. |
| C060 | implemented / acceptance pending | Transport anchors, capture detection and end-to-end site verification pass natively; the far side's confirmation is a checklist, with no remote agent. |
| C061 | implemented / acceptance pending | Measured per-family exit use and fixture leak behaviour pass; provider/public reachability remains unproved. |
| C062 | implemented / acceptance pending | The Linux kill switch is proven in namespaces; other platforms and real-client production leak acceptance remain. |
| C063 | verified | Restore from archive and durable lifecycle/recovery history pass unit, API, native and browser acceptance. |
| C064 | implemented / acceptance pending | Approval, key expiry and health readings pass against recorded shapes; not exercised against a live control server; approval actions are P16. |
| C065 | implemented / acceptance pending | Version-tolerant inspection and the container panel pass fixtures; no real Headscale was run; workflows are P16. |
