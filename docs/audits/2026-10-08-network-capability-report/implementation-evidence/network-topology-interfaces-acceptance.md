# Overview/topology and interfaces/namespaces acceptance (C007–C023)

This record covers ledger rows C007 through C023 of the [implementation ledger](../implementation-status.md):
what shipped per row, the tests that prove it and what remains. It changes no ledger status; the
proposed statuses are in the final report of the change, for the ledger owner to apply.

## Source and environment

Worktree `/home/ubuntu/Just-Dashboard-net-topology`, branch
`implement/network-topology-interfaces-maturity`, based on `origin/research/network-capability-report`
at `4338e6cc`. Raw logs are in `/home/ubuntu/Just-Dashboard-net-topology-artifacts/`.

The host is a live production server. No host interface, route, firewall rule, DNS setting or sysctl
was changed. Kernel-backed tests ran only in throwaway namespaces held by `unshare --net --mount`
(the existing `newLiveNS` harness) and were removed with their holder; process-death fixtures run
their commands directly inside the namespace, never through the host wrapper. The only host
interaction outside a throwaway namespace is read-only: the API contract test reads the host's own
Overview (`ip`, `nft`, `ss`, the netlink conntrack dump without privilege, which reports
`failed`), and read-only `ip`/`bridge`/`ethtool` inspection while designing parsers. Browser cases
run against a production build served on `127.0.0.1:43212` with the API mocked
(`tests/browser/network-fixture.ts`); they establish what the pages do with each answer, not host
behaviour.

## Per row

### C007 Network identity and current path

Shipped: `netx.EgressIdentities` (`identity.go`) asks the kernel `route get` for each family's anchor
and returns device, gateway, the NIC source with its scope and the family's forwarding switch. The
provider-facing identity is a separate verdict — `nic` (public NIC source; provider NAT/firewall not
visible), `translated` (private/shared/unique-local source rewritten upstream to an unobserved
address), `unobserved`, `no_route`, `unknown` — with a per-family error. The Overview identity line
names both forwarding families (unreadable rather than "off" when the switch cannot be read) and a
translated source; a "Ways out" table shows each family's device, source, the internet's view and
forwarding.

Tests: `TestEgressIdentitiesSeparateNICSourceFromProviderIdentity`,
`TestEgressIdentityFamilyFailuresStayPerFamily`, `TestJudgeIdentityKeepsUnreadableRoutesUnknown`,
`TestNetworkOverviewReportsEachReadingItsIdentityFlowsAndHistory`; `identityVerdict` in
`topology-reading.test.js`; browser "the identity says both forwarding families and keeps a translated
source from posing as public".

Limits: the provider's public address behind a translated source is not measured; doing so needs an
external vantage (P6), which this read does not contact.

### C008 Host/Docker/tunnel topology

Shipped: `TopologyFlows` (`topology_flows*.go`) dumps connection tracking in-process over ctnetlink
(read-only, bounded to 65,536 entries) and classifies each entry's initiator and responder onto the
topology's nodes (Docker subnets, managed/addressed device subnets, the tailnet range, host addresses,
the internet). Edges carry flow counts, protocols, translation (masquerade/port forward from the reply
tuple), the translating device and an effective hop-by-hop path; bytes only when `nf_conntrack_acct`
is on. The Overview adds "Paths in use", per-node tracked-flow counts, and pointing at a node keeps
lit the nodes it trades flows with. Failed and unavailable reads are said.

Tests: `TestConntrackMessagesParseTuplesTranslationAndCounters`,
`TestConntrackMessagesStopAtTheLimitAndReportKernelErrors`,
`TestClassifyFlowsDrawsEffectivePathsBetweenTopologyNodes`,
`TestTopologyFlowsReportUnavailableAndFailedReads`; kernel acceptance
`TestLiveConntrackDumpReadsAnOwnedNamespace` (root, throwaway namespace with an owned nft ct rule);
`flowNeighbours`/`nodeFlows` bun tests; browser "paths in use come from tracked flows and pointing at a
node lights who it trades with" and "a failed connection-tracking read is said, not drawn as no
traffic".

Limits: a moment's reading of flows open now, not history or a capture; no switch discovery beyond
this host; byte counts absent while accounting is off; routing-table-exact placement is approximated by
longest matching subnet.

### C009 Live upload/download totals and trends

Shipped: the Throughput head counts the newest two-second reading's age on the browser clock and turns
stale past three intervals; the live hook carries last-success and retry. The In/Out tiles open the
uplink's sheet; the chart lists the devices each line is drawn from (each opening its sheet) and a
dragged selection lists what carried that stretch, busiest first.

Tests: `observationAge`, `newestPoint`, `ageWords`, `windowBreakdown` bun tests; browser "throughput
says how old its newest reading is and turns stale when the ring stops" and "the throughput tiles and
the chart drill into the devices under them".

Limits: the drill covers the fifteen-minute live ring; recorded history drill-down stays on the Traffic
page.

### C010 Network attention findings and handoffs

Shipped: every Overview reading records an observation (`ok`, `failed`, `unavailable`); a failed one is
its own `observation.<source>` finding and the findings judged from it are skipped (a failed firewall,
error-history or forwarding read no longer reads as absent firewall, zero errors or forwarding off;
`RecentErrors` now returns its database error). Findings name their source and the uplink's link
findings open its sheet. `RecordFindings` keeps incident history in the additive `network_incidents`
table with two-minute correlation, resolution only on a successful reading, `unobservedSince` on a
failed one, 30-day/1,000-row retention, and the server judges the list every five minutes as well as
on page reads. The Attention panel lists history with open/unobserved/resolved state, span and
correlated incidents.

Tests: `TestOverviewFailedReadingsAreNotJudgedAsAbsence`,
`TestIncidentsResolveOnlyWhenTheirReadingSucceeded`, `TestIncidentsPruneOldResolvedHistory`,
`TestIncidentsWithoutADatabaseAreEmpty`, `TestNetworkIncidentSchemaOnlyAddsTablesAndIndexes`,
`TestNetworkIncidentSchemaReachesFreshAndUpgradedInstalls`,
`TestNetworkOverviewScheduledReadRecordsWithoutABrowser`; `incidentSpan` bun test; browser "a failed
reading is its own finding, and history keeps unobserved incidents open".

Limits: history exists for observed moments only (page reads and the five-minute schedule);
correlation is temporal, not causal.

### C011 Retained-data warning and retry

Shipped beyond the earlier subsidiary polls: the live throughput ring keeps its data with a dated
"live throughput" warning and Refresh on the Overview, Interfaces and the live Traffic window; a later
namespaces poll failure keeps the list with its own dated warning; each namespace whose devices could
not be read says so in place (`readError`) instead of looking empty; identity reads fail per family;
the device detail, bridge, readiness and namespace sheets retain their last answer with a dated
warning, and their unreadable parts (`Reading` states) are worded, never empty.

Tests: `TestNamespacesKeepPerItemReadFailures`, `TestNamespaceDetailKeepsEachFailedPartVisible`,
`TestEgressIdentityFamilyFailuresStayPerFamily`; browser "a failed live poll keeps the ring with a
dated retry", "a later namespaces poll failure keeps the list with a dated retry", "namespaces keep a
failed item visible and open as their own network", plus the existing `network-report.spec.ts`
retained-poll cases in the final gate.

Limits: none known within the overview/interfaces pages; other sections' reads are owned elsewhere.

### C012 Rich interface inventory/telemetry

Shipped: `GET /links/{name}/detail` — driver (ethtool, sysfs fallback), meaningful top-level offloads
with fixed flags and every detailed `ip -s -s` error counter, each part with its own reading state. The
device sheet's "Driver and errors" panel shows them; only nonzero counters get a row.

Tests: `TestLinkDetailReadsDriverOffloadsAndErrorCounters`,
`TestLinkDetailKeepsCountersWhenEthtoolIsMissing`,
`TestLinkDetailRefusesBadNamesAndReportsMissingDevices`; `errorRows` bun test; browser "a device sheet
reads its driver, offloads, error counters and address origins".

Limits: per-queue/ring statistics (`ethtool -S`) and NIC firmware details beyond `ethtool -i` are not
read.

### C013 Docker bridge/container-veth correlation

Shipped: the inventory carries container-list, PID and network-list failures; `containerVeths` names
containers whose devices could not be read. A Docker veth without a resolved container is
`dockerJoin: "unresolved"` with the reason; a Docker-named bridge without a network list is
`"unknown"`. The Overview reports Docker readings as observations; Interfaces shows a notice and the
row says "container not joined".

Tests: `TestAnnotateFlagsIncompleteDockerJoins`, `TestContainerVethsNameUnreadableContainers`;
browser "interfaces flag Docker devices that could not be joined instead of hiding them".

### C014 Bridge creation and membership

Shipped: VLAN filtering and multicast-snooping options at creation; `GET /links/{name}/bridge` (settings,
VLANs by port beside managed desired lists, bounded FDB); managed port VLAN policy (see C015);
`GET /links/{name}/master/preview` shows the guard verdict and every address, route, dependent, VLAN
and STP effect before a membership change, and the sheet disables Apply when it is refused and lists
the effects in the confirmation.

Tests: `TestBridgeCreationCarriesVLANFilteringAndSnooping`, `TestBridgeViewReadsSettingsPortsAndLearnedEntries`,
`TestPreviewMasterNamesWhatAMigrationLeavesBehind`; kernel
`TestLiveBridgeVLANsFloodEndsAndReadinessAgainstARealKernel`; browser "a managed bridge reads as a
switch …" and "a refused bridge move is previewed …".

Limits: the preview does not move addresses or routes for the operator; IGMP/MLD querier settings and
per-port FDB editing are not offered.

### C015 VLAN creation

Shipped: the tagged/untagged policy workflow — a managed port (or the managed VLAN-filtering bridge
itself) carries a native untagged VLAN plus tagged VLANs/ranges, applied from the observed membership
with full undo, refused when the port or bridge carries the operator's path or the uplink, destructive
when removing a VLAN, recorded in the spec, restored by conditional `bridge vlan` unit lines and
journaled for independent recovery. The VLAN create form points to the workflow.

Tests: `TestBridgeUnitCommandsRenderOwnedVLANsAndFloodEnds`, `TestSetPortVLANsAppliesRecordsAndRendersTheMembership`,
`TestSetPortVLANsRefusals`, `TestSetPortVLANsIsTakenBackWhenThePathMoves`,
`TestBridgeRecoveryRestoresMembershipsAndFloodEnds`, `TestRemovalsDecideTheDestructiveGate`; kernel
`TestLiveBridgeVLANsFloodEndsAndReadinessAgainstARealKernel` (applied, read back, boot lines replayed into
a fresh namespace) and `TestLiveBridgeVLANRecoveryAfterProcessDeath`; `vlanPolicy`/`describeVlans` bun
tests; browser "a managed bridge reads as a switch and a port's VLAN policy is applied and confirmed".

Remaining: manager-backed VLAN profiles (NetworkManager/networkd/netplan) are not implemented; the
native adapter does not create profiles.

### C016 VXLAN creation

Shipped: readiness (underlay device and multicast capability, local-end ownership, kernel route per end
including recursion through the tunnel, MTU headroom, the kernel's flood list against the managed one,
received-traffic liveness, encryption/firewall limits) and flood-end (remote-set) management for managed
unicast VXLANs via `bridge fdb append`, in the spec, boot unit and recovery journal.

Tests: `TestReadinessOfAVXLANReadsUnderlayEndsMTUAndFloodList`, `TestSetVXLANRemotesAddsAndRemovesFloodEnds`,
`TestBridgeRecoveryRestoresMembershipsAndFloodEnds`; kernel
`TestLiveBridgeVLANsFloodEndsAndReadinessAgainstARealKernel`; `parseRemotes` bun test; browser "a
VXLAN's readiness names its checks and limits and edits its flood ends".

Remaining: EVPN (named "later" in the row); multicast underlay forwarding and remote VTEP reachability
are not probed.

### C017 GRE/GRETAP and IPv6 GRE/GRETAP

Shipped: endpoint and liveness readiness — kernel route to the remote (recursion fails), local-end
ownership, keyed/unkeyed per-family MTU overhead, received-traffic liveness — with the unencrypted
transport and unchecked protocol-47 policy stated as limits.

Tests: `TestReadinessOfGREChecksItsLocalEndAndKeyedOverhead`; browser "a gre sheet shows what this host
can establish about it".

Limits: liveness is received counters, not an active probe of the remote end.

### C018 Dummy interfaces

Shipped: the create form explains the service-address and route-sink recipes; a dummy's readiness
shows its address, the routes into it and the services bound to its addresses; its sheet hands "Route a
destination into" it to the Routing page, which opens the route form with that device chosen
(`?route=new&device=`).

Tests: `TestReadinessOfADummyShowsItsRoutesAndServices`; browser "a dummy's sheet hands a route into it
to the routing page" and "bridges, macvlans and dummies explain themselves before they are made".

### C019 Macvlan modes

Shipped: per-mode host-isolation explanations at creation and in the sheet's readiness (siblings,
VEPA reflective relay, passthru), a provider-MAC warning when the parent is the uplink or a virtual
machine NIC driver, and an address check.

Tests: `TestReadinessOfAMacvlanExplainsIsolationAndProviderMACs`; kernel
`TestLiveMacvlanBridgeModeConnectivity` (bridge-mode siblings in two namespaces reach each other; the
parent's own stack does not reach them); browser "a macvlan sheet shows what this host can establish
about it" and the create-form case.

Limits: connectivity beyond this host (provider MAC acceptance) is not probed.

### C020 Veth pairs into managed namespaces

Shipped: managed veths carry peer and namespace; the device sheet opens the namespace it leads into
(or a container's) and hands a container to the investigator (`/network/investigate?container=`,
resolved against the investigator's own container list); the namespace sheet opens the host end.

Tests: `TestNamespaceDetailReadsANamedNamespaceAsItsOwnNetwork`; browser "a veth hands off to the
namespace it leads into and a container's to the investigator".

### C021 Device up/down, MTU and bridge edits

Shipped in this change: the bridge-membership preview (C014) before a membership edit. Existing
runtime-only native-owned state/MTU/bridge edits already use the serialized journal and independent
recovery.

Remaining: native owner profile editing of state/MTU/bridge membership is not implemented; the native
adapter's intent covers addressing, DNS, domains and routes only.

### C022 IPv4/IPv6 address addition/removal

Shipped: each address carries the kernel's origin (static, DHCP, router advertisement, temporary,
dynamic, link-local) and remaining lifetimes; the sheet says added addresses are static and points to
the native profile for how the device acquires its own.

Tests: `TestParseLinksNamesWhereEachAddressCameFrom`; `addressProvenance`/`lifetimeWords` bun tests;
browser address-origin assertion in the device-sheet case.

Remaining: no combined DHCP/SLAAC/static-uplink provisioning transaction in the address workflow; method
changes stay in the native profile adapter's supported owners.

### C023 Named/container namespace inventory/lifecycle

Shipped: per-namespace read errors; `GET /namespaces/{name}?kind=` reads devices, both families'
routes, the resolver its processes read and its listeners, each with its own reading state;
`GET /namespaces/{name}/lookup` asks the namespace's kernel `route get`. The namespace sheet shows them
with the route question. Containers are resolved from the Docker inventory by name.

Tests: `TestNamespaceDetailReadsANamedNamespaceAsItsOwnNetwork`,
`TestNamespaceDetailKeepsEachFailedPartVisible`, `TestNamespaceTargetsComeFromTheHostNotTheRequest`,
`TestNamespaceLookupAsksTheNamespaceKernel`, `TestNamespacesKeepPerItemReadFailures`,
`TestNetworkInventoryReadsAreOpenToReadersAndValidated`; browser "namespaces keep a failed item visible
and open as their own network".

Limits: namespace policy rules and firewall tables are not read.

## Security boundaries

New reads are `read`; `PUT /links/{name}/vlans` and `/remotes` are `system.admin`, audited
(`network.link.vlans`, `network.link.remotes`) and destructive when they remove a VLAN or an end
(`requireDestructive`); both are covered by the pending-apply journal prefix. Host commands use explicit
argv through `run`; the conntrack read is an in-process netlink dump, not a command. Client values are
validated before use (interface/namespace names, literal addresses, VLAN ids); containers resolve from
the inventory, never a client PID. The only schema change is the additive `network_incidents` table.

## Checks

All Go commands used `GOMAXPROCS=2 GOFLAGS=-p=2` and an on-disk `TMPDIR`; every build, server and
browser run held `/home/ubuntu/.jd-heavy.lock` as one section, served on `127.0.0.1:43212` with one
browser worker, and stopped the server afterwards.

| Check | Command | Result |
| --- | --- | --- |
| netx unit tests | `go test ./internal/netx -count=1` | pass, 25.8 s |
| Owned-namespace kernel acceptance | `sudo -n env JD_NETNS_LIVE=1 netx.test -test.run 'TestLiveConntrack\|TestLiveBridgeVLANs\|TestLiveMacvlanBridgeMode\|TestLiveBootFile\|TestLiveMutationsAgainst'` | 5 pass, 7 s (`netx-live-final.txt`) |
| Process-death recovery | `JD_NETNS_LIVE=1 go test ./internal/netx -run 'TestLiveBridgeVLANRecovery\|TestBridgeRecoveryProcessFixture'` | 2 pass (`netx-live-bridge-recovery.txt`) |
| First focused browser run | new spec + `network-ui.spec.ts`, against a fresh 140 s build | 38 pass, 14 optional screenshot skips (`first-run.txt`) |
| Changed-file gate (validation run) | `scripts/test-changed.sh 4338e6cc` | pass: Prettier, ESLint, `tsc`, 3,277 Bun tests (14,776 assertions); `go build ./...`, `go vet` and tests of `internal/api`, `internal/netx`, `internal/store`; 29 browser specs, 516 cases passed, 60 optional skips, 17.8 min (`test-changed-2.txt`, 1,199 s command, 1,856 s with the lock wait) |

The 19 cases of `network-topology-interfaces.spec.ts` are part of that gate. Review screenshots at
390 and 1440 pixels of the Overview, the ens3/jd-lab/vlan30/vx42 device sheets and the lab namespace
sheet are in `shots/` (fixture data, not committed).

The final gate on the committed head is recorded below.
