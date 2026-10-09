# Routing, forwarding and host firewall acceptance (C024–C038)

This slice was built on `implement/network-routing-firewall-maturity`, branched from
`origin/research/network-capability-report` at `4338e6cc`. It covers the routing and forwarding
rows C024–C032 and the host firewall rows C033–C038.

All native evidence comes from network namespaces the tests create and delete. Some tests also
run ufw in a private mount namespace with its own `/etc/ufw`, `/lib/ufw` and `/run`. No test
changed the host's routes, rules, firewall, UFW state, DNS, interfaces or sysctls.

Before and after every live run, the following host state was recorded and compared:

- `ufw status verbose`;
- the md5 sums of `/etc/ufw/user.rules`, `user6.rules`, `ufw.conf` and `/etc/default/ufw`;
- the namespace list and the link count;
- `nft list tables`;
- `ip rule` and `ip -6 rule`.

All of these matched each time. The before and after files (`host-before.txt`, `host-after.txt`)
are kept with the raw logs. No namespace, veth or listener remained afterward.

The host route table is not a usable comparison. Other services and agents create and remove Docker
bridges on this host continuously. With no test running, a bridge was replaced within ten seconds
(`routes-churn-without-tests.diff`). After the runs, no route or rule held any address the tests use
(10.70–10.72.0.0/16, 192.168.60.0/24, 198.18.0.0/24), and no `jd_firewall` table existed.

The host lacks FRR, firewalld and the `vrf` kernel module. Evidence for those three features
comes only from fixtures; each row below names where that applies.

## Rows

### C024: All-table route inventory: history and target filtering

**Shipped**

- `netx.StartRouteHistory` reads every table in both families every 30 seconds. It diffs each
  reading against the last one by route identity. Additions, removals and changes are stored in
  `network_route_events`, along with the table, family, owner, source and a one-line summary.
- The last reading persists in `network_route_snapshot`, so the first reading after a restart
  records changes made while the dashboard was down. Those events are marked `across_restart`.
  Kernel countdowns, such as an RA route's `expires`, are not counted as changes.
- Retention is 30 days and at most 2,000 events. Anyone who can read the routing tables can read
  the history at `GET /network/routing/history?target=…`.
- On `/network/routing`, a **Target** field narrows each table to the routes covering an address
  or network. The route a lookup in that table would select is tagged. The history panel then
  lists only changes to routes covering the target.

**Tests**

- `TestRouteHistoryDiffsReadingsAndBoundsEachChange`, `TestRouteSummaryIgnoresAnRARoutesCountdown`,
  `TestRouteHistoryRecordsReadingsAcrossARestart` and
  `TestRouteHistoryLeavesDaemonRoutesOutAndBoundsAReading`.
- The store schema tests and the API refusal test.
- Bun tests: `route-reading.test.js` (`parseTarget`, `routeCovers`, `targetMatches`).
- Browser case "a target narrows every table to its covering routes and the history to its
  changes".

- The history is bounded:
  - Routes installed by routing daemons are left out: BGP, OSPF, IS-IS, RIP, Babel, BIRD and zebra,
    identified by name or by FRR's protocol number. A full BGP table cannot flood it.
  - A reading that finds more than 100 changes stores them as one event.
  - Past 20,000 routes, the observer stops and gives the reason, instead of rereading a routing table
    every 30 seconds.

**Limits**

History is sampled, not driven by netlink events. A change that is made and reverted within one
30-second interval is not recorded. Routes installed by routing daemons are not in this history; the
BGP route browser reads them on demand.

### C025: Managed unicast routes: ECMP and edit plans

**Shipped**

- Managed routes take 2 to 16 next hops, each with a gateway and/or device and a weight from 1 to
  256. Table, metric and source are rendered before the `nexthop` arguments, as iproute2
  requires. Every leg's device is checked.
- Drift compares multipath routes leg by leg.
- `POST /network/routing/routes/{id}/plan` returns the route before and after, and the exact
  commands. It uses `ip route replace` when the kernel identity (destination, table, metric,
  type) is unchanged. Otherwise it adds the new route before deleting the old one, with an undo.
  The plan also returns the modeled impact.
- `PUT /network/routing/routes/{id}` applies only that plan. It runs under `s.destructive`, the
  client-path guard and the temporary-apply journal.
- Before an in-place replace, the kernel's route at that identity is read. If it is no longer the
  managed route, the replace is guarded instead of overwriting whatever drift put there.
- A second main-table default route is refused.
- The UI has a "Spread over several hops" switch with hop rows. Editing goes through **Review
  plan** and then **Apply plan**; changing the form after the review discards the plan.

**Tests**

- Unit: `TestMultipathRoutesValidateAndRenderWithEveryAttributeFirst`,
  `TestMultipathRoutesAreRecognisedLegByLeg`, `TestAddMultipathRouteChecksEveryLegsDevice`,
  `TestDriftComparesAMultipathRoutesLegs`, `TestRoutePlansReplaceInPlaceOrAddBeforeRemoving`,
  `TestEditRouteReplacesAndTakesItBackWhenTheReplyMoves`,
  `TestEditRouteDoesNotReplaceARouteThatDriftedToAnotherOwner` and
  `TestEditRouteRefusesASecondMainDefault`.
- Live: `TestLiveRoutingMaturity` creates IPv4 and IPv6 weighted multipath routes in a
  namespace. It reads them back as managed, finds no drift, replaces weights in place and re-keys
  the route by metric.
- Browser cases "a multipath route sends each leg…" and "a managed route is edited by applying
  exactly the plan that was reviewed".

### C026: Discard routes: impact preview

**Shipped**

- `POST /network/routing/routes/preview` models a candidate route against the live rules and
  tables, without changing anything.
- The model is checked against the kernel. It covers the client, the internet anchors, tunnel
  endpoints, port-forward targets, NAT sources, allowlist ranges, Docker networks and up to 32
  established peers.
- For each address whose path changes, the preview gives the path before and after. Addresses
  the model cannot decide are listed separately. The client is flagged.
- The add dialog requires **Check effect** before a discard route can be added.

**Tests**

- `TestRouteImpactListsWhatADiscardRouteWouldTakeAway` and
  `TestRouteImpactFlagsTheClientAndLeavesOtherTablesAlone`.
- The live test previews a blackhole over a tunnel endpoint and finds the tunnel discarded. The
  apply is then refused and leaves nothing behind.
- Browser case "…a discard route is added only after its preview".

**Limits**

The preview models only known addresses. Destinations the host has never talked to are not
listed. This is stated under the preview.

### C027: Policy rules: effective-match preview and UID/TOS/goto/VRF

**Shipped**

- Rules accept `uidrange`, `tos` (numeric or an `rt_dsfield` name, family-validated),
  `l3mdev` (VRF table lookup) and `goto` actions.
- The kernel's other selectors are read: `not`, `ipproto`, `sport`/`dport`,
  `suppress_prefixlength` and unresolved gotos. Rules using these selectors are never adopted
  as managed.
- Goto rules must be guarded:
  - The target is a later managed rule of the same family.
  - No foreign rule lies between the goto and its target.
  - The target is in the 10000–19999 range.
  - A rule that a goto jumps to cannot be deleted.
- `POST /network/routing/rules/preview` reports which existing rules shadow the candidate and
  which it shadows, and its modeled impact.
- A preview can also test one packet (target, source, iif/oif, mark, UID, TOS). That packet is
  modeled with and without the rule and checked against `ip route get` for the current rules.
- The rule guard (`shadowsReplies`) uses the UIDs of the sockets that actually answer the
  client, read from `ss`. A goto is not a discard and is verified after it applies.
- TOS 0 and the all-UID range select nothing in particular. They are refused, so they cannot pass
  the no-selector guard.

**Tests**

- `TestRuleExtensionsReadFromIPsJSON`, `TestRuleExtensionRequestsAreValidatedForTheirFamily`,
  `TestRuleExtensionsRenderAndMatchTheKernelsSpelling`,
  `TestAddRuleKeepsAGotoAmongTheDashboardsOwnRules`, `TestDeleteRuleRefusesARuleAGotoJumpsTo`,
  `TestShadowsRepliesReadsTheAnsweringSocketsOwners`,
  `TestRulePreviewFindsShadowsAndChecksItsModelAgainstTheKernel` and
  `TestRulePreviewCarriesTheGuardAnAddWouldAnswer`.
- The six `TestModel…` cases.
- `TestLiveRoutingMaturity` adds UID, TOS (IPv4 `0x10`, IPv6 `EF`), l3mdev and goto rules to a
  namespace kernel. The kernel's backward-goto refusal is caught before asking, and the goto
  target is protected from deletion. Two UID-selected probe packets agree with `ip route get`.
- Browser case "a policy rule takes UID, TOS and goto selectors…".

**Limits**

The host kernel lacks the `vrf` module, so an `l3mdev` rule is proven to be accepted and read
back, but no traffic through a VRF device was tested. VRF devices are listed from
`ip -d link show type vrf` only in fixtures.

### C028: Priority allocation and protected tables

**Shipped**

The managed range, the live and owned priority checks and the protections for the kernel and
Tailscale tables are unchanged. The new rule shapes go through the same `placeRule`. Goto targets
are also bound to the managed range, so a managed goto cannot jump into or over foreign rules.

**Tests**

- The existing priority tests pass unchanged.
- `TestAddRuleKeepsAGotoAmongTheDashboardsOwnRules`.
- The live backward-goto refusal.

### C029: Post-change route validation: application and tunnel anchors

**Shipped**

- WireGuard peer endpoints are read from `wg show all dump`, with the interface fwmark. Tailscale
  peers' current direct addresses are read from `tailscale status`, with mark `0x80000`. There
  are at most 16.
- Each endpoint becomes an anchor that is asked with its own mark before and after every route
  or rule change. A change that moves a tunnel endpoint off its path is refused and undone. An
  endpoint that has roamed since the reading is not treated as a failure.
- Established connections (`ss`) are modeled before and after the change. The journal's new
  `validation` records the client, the source-selected reply, the anchors and every connection
  the change moved. The pending-change sheet shows this record.

**Tests**

- `TestTunnelEndpointsAreAnchorsAskedWithTheirOwnMarks`,
  `TestATunnelEndpointMovedByAChangeIsRefusedAndRoamingIsNot` and
  `TestRoutingEvidenceListsFlowsTheChangeMoved`.
- The live journal assertion in `TestLiveRoutingMaturity`.
- Browser case "a pending routing change shows what it was checked against and the connections
  it moved".

**Limits**

Moved application connections are reported as evidence, not refused. Applications that have no
established connection at that moment are still not anchored.

### C030: Forwarding controls: Docker counting and measured health

**Shipped**

- Each family reports measured health from the kernel's forwarded-datagram counters: IPv4
  `ForwDatagrams` and IPv6 `Ip6OutForwDatagrams`.
- Two readings give a rate and a status: off, measuring, forwarding, idle, partial or unknown.
  Partial means some IPv4 devices have forwarding disabled; those devices are named. An IPv6
  device's switch only chooses host or router behaviour, so IPv6 is never reported as partial.
- `dockerBasis` explains how Docker's dependence on forwarding was counted.

**Tests**

- `TestForwardingHealthMeasuresTheKernelsCountersAndDevices`.
- The existing forwarding API tests.
- Browser case "forwarding shows its measured health and how Docker was counted".

### C031: FRR BGP: policy, route browser, OSPF

**Shipped**

- Each BGP neighbour shows its per-family inbound and outbound route-maps, prefix-lists and
  filter-lists, read from `show bgp neighbors json`.
- OSPFv2 and OSPFv3 neighbours are read, in both JSON shapes FRR has used.
- `GET /network/bgp/routes?family=&prefix=` reads a bounded table. Above 5,000 routes, it asks
  for a prefix instead of reading everything.
- The page states that the dashboard does not configure BGP.

**Tests**

- `TestBGPReadsNeighbourPoliciesAndOSPFAdjacencies` and
  `TestBGPRoutesListASmallTableAndOnlyAPrefixOfALargeOne`.
- The API refusal test.
- Browser case "BGP shows each neighbour's filters, OSPF adjacencies and a bounded route
  browser".

**Limits**

FRR is not installed on this host; all evidence comes from fixtures. No BGP policy
configuration was added, and no dynamic failover controller was added. Both would need an FRR
writer and their own recovery story.

### C032: Routing decision diagram

**Shipped**

- `Routing()` now returns a `clientDecision`. It runs `ip route get CLIENT from SOURCE` for the
  kernel's table and device. It runs the rule model, with the replying socket's UID when exactly
  one is known, to name the rule.
- The basis is `kernel_and_model` when the two agree, `kernel` when the model cannot decide, and
  `disagree` when they differ. Candidate rules are listed.
- The diagram lights the kernel's table, and names the rule only when it was evaluated to that
  table. Its label says whether the highlight was evaluated or inferred.
- Pages served by an older backend, which sends no decision, keep the earlier inference.
- IPv6 rules were already drawn.

**Tests**

- `TestClientDecisionNamesTheKernelsTableAndTheEvaluatedRule`,
  `TestClientDecisionShowsTheKernelWhenTheModelDisagreesOrCannotDecide` and
  `TestClientDecisionIsAbsentForALocalClient`.
- The live client decision in `TestLiveRoutingMaturity`.
- Bun `replyHighlight` test.
- Browser case "the reply decision names the kernel's table and the evaluated rule, and says
  when it cannot".

### C033: Firewall detection by activity, and zones

**Shipped**

- `selectBackend` considers ufw, firewalld, the owned nftables table and iptables. It chooses by
  whether each one is active: ufw status, the firewalld unit or the owned table being enabled.
  Presence alone does not decide it.
- When two front ends are active, the result is a conflict and read-only. The detection list
  says which firewall is in charge and why each other candidate is not.
- firewalld active zones are read with their interfaces, sources and targets. Each rule carries
  its zone.
- The page shows the detection list, policy by family and by interface, active zones and
  foreign tables.

**Tests**

- `TestBackendIsChosenByActivityNotOnlyPresence` and
  `TestFirewalldZonesAndLandingZoneSemantics`.
- `TestLiveUFWParsesGuardsAndPlansAgainstTheRealTool` runs real ufw in a sandbox.
- Browser case "the firewall page says which firewall is in charge, its zones, policy by family
  and preserved access".

**Limits**

firewalld is not installed on the host, so zones were tested only with recorded `firewall-cmd`
output.

### C034: Rule plans, shadowing analysis and stable identity

**Shipped**

- Every rule has an identity (`fw-` plus 12 hex characters) derived from its content, zone and
  interface.
- Deletes and edits name both the number and the identity. A stale identity is refused with
  `409 rule_changed`.
- `analyzeRules` finds two kinds of problem in ufw and the owned table. A rule is *shadowed*
  when an earlier rule decides everything it selects the other way. It is *redundant* when an
  earlier rule already does the same. Findings are tagged on rules.
- Plans of up to 20 operations can be previewed (`POST /firewall/plans/preview`) and applied
  (`POST /firewall/plans`). They run adds, then replaces, then deletes. A failure undoes the
  steps already taken. Each plan is guarded as one change.
- A plan cannot remove a device-scoped or forwarding rule, because the form could not write it
  back if a later step failed.
- The rule form gained a destination-address field and **Add to plan**. Rules have **Remove in
  a plan**, and a tray shows the staged plan.

**Tests**

- `TestRulesGetStableIdentitiesAndFindings`, `TestStaleIdentitiesAreRefused`,
  `TestPlansRunAddsBeforeRemovalsAndTakeBackAFailure`, `TestReplaceRefusesToWidenAnInterfaceRule`
  and `TestAPlanCannotRemoveARuleItCouldNotPutBack`.
- The live ufw sandbox applies a plan and recovers from a failed one.
- Bun `firewall-reading` tests.
- Browser cases "rules carry their identity, findings and history…" and "several rule changes
  are staged, reviewed and applied as one plan".

### C035: Enable/disable: staged verification and timed recovery

**Shipped**

- Every host firewall mutation is checked for access before it runs. The checks are evaluated
  against the current rules and the simulated result, for the dashboard port reached through
  the operator's arrival interface, SSH and public ingress on 80 and 443.
- A change that would refuse a check that admits now returns `409 would_lock_you_out` and names
  the check. The same applies when the check could no longer be judged afterwards.
- A rule the evaluator cannot read is followed both ways, up to four deep. If both paths decide
  the same way, that is the verdict. An allow that may or may not apply, in front of a rule that
  admits anyway, changes nothing.
- A firewall whose status cannot be read refuses a guarded change (`503 firewall_unreadable`).
  It is never applied without the guard and the recovery.
- `GET /firewall/preflight` shows these checks before the confirm.
- ufw and firewalld changes run under `ProtectFirewallChange`, which works as follows:
  1. Under the journal's lock, the change first runs only as far as its validation and access
     guard (`netsec.Scoped`, `ErrChecked`). A refused or invalid request opens no journal, and
     sets off no recovery that would rewrite and reload an unchanged firewall.
     - Both passes are bound to the firewall the journal was prepared for. A firewall that
       changed hands in between returns `409 firewall_changed`.
     - A rule named by identity is resolved inside this lock.
  2. The tool's state files are snapshotted into the network change journal.
  3. After the change, access is verified against the new ruleset.
  4. If verification fails, the files and the service state are restored.
  5. With temporary apply on, the independent recovery helper restores the change at the deadline
     unless the operator confirms it.
- The recovery vocabulary is closed: eight ufw files, or the firewalld unit and zone, plus
  `ufw --force enable` and `ufw reload`. firewalld's boot unit is restored only from a plain
  `enabled` or `disabled`. Any other `systemctl is-enabled` answer leaves the unit alone.

**Tests**

- `TestEnablingIsRefusedWhenItWouldShutTheOperatorOut`,
  `TestPolicyRuleAndDeleteChangesKeepPublicIngressThatWorksNow`,
  `TestPreflightShowsEachCheckBeforeAndAfter`,
  `TestProtectFirewallChangeRestoresTheToolsFilesWhenVerificationFails`,
  `TestProtectFirewallChangeSavesAndLeavesTheFilesItChanged`,
  `TestIndependentRecoveryRestoresAPendingFirewallChange` and
  `TestFirewalldRecoveryRestoresUnitServiceAndZone`.
- `TestAChangeThatLeavesAWayInUnjudgeableIsRefused`, `TestAnUnreadableFirewallRefusesAGuardedChange`,
  `TestAScopedChangeStopsBeforeTheHostAndKeepsItsFirewall`, `TestOnlyAPlainUnitStateIsRestored` and
  `TestARefusedFirewallChangeOpensNoJournalAndRunsNoRecovery`.
- `TestLiveUFWChangesAreRestoredByTheJournal` makes a real ufw change in a sandbox and recovers
  it with the independent helper. A client in a namespace regains access afterward.
- Browser cases "enabling shows the preflight's refusal before anything is sent…" and "with
  temporary apply on, a firewall change asks the host to recover it unless confirmed".

**Limits**

Access is checked against rules, not by sending packets. The check covers the operator's
current address and interface; other operators' addresses are not modeled. A browser that
reaches the dashboard through an SSH tunnel is judged by the SSH session's address.

### C036: Effective per-interface and per-family policy, and preserved access

**Shipped**

- `effectivePolicy` reports incoming, outgoing and routed defaults per family. For ufw, it
  includes whether IPv6 is managed and, while ufw is inactive, the configured defaults from
  `/etc/default/ufw`.
- With firewalld, it reports the effective target per interface, from the interface's zone.
- The routed default is editable on the page.
- A "Preserved access" panel shows each access check and whether the current rules admit it.
  Any reader can see it at `GET /firewall/access`.

**Tests**

- `TestAccessChecksFollowFirstMatchDefaultsAndInterfaces` and
  `TestFirewallAccessNamesTheDashboardPortAndSSH`.
- `TestFirewallAccessIsReadableByAnyReader`.
- The live ufw sandbox.
- The browser enforcement case.

### C037: Per-rule history and firewalld reset

**Shipped**

- Every audited firewall mutation records a rule event in `firewall_rule_events`. The event
  holds the actor, operation, identity, the previous identity for edits, the rule text and the
  change ID when the change was applied pending. Retention is 2,000 events.
- `GET /firewall/history?rule=fw-…` follows a rule through its replacements. The page has
  **Rule history** per rule and a recent-changes panel.
- firewalld reset reloads the default zone's shipped settings (`--load-zone-defaults`). A
  zone without a shipped definition is refused. The reset is guarded and journaled like other
  changes.

**Tests**

- `TestRuleHistoryFollowsARuleThroughItsReplacements` and
  `TestFirewalldResetReloadsAShippedZoneAndIsGuarded`.
- The store schema tests and `TestFirewallHistoryRefusesAMalformedIdentityBeforeReading`.
- The browser history case.

**Limits**

firewalld reset was tested only with recorded output, because firewalld is not installed.
History starts when this version is installed; earlier changes are not reconstructed.

### C038: Scoped nftables owner adapter

**Shipped**

- On hosts with no ufw or firewalld, the dashboard can own one table, `inet jd_firewall`. It is
  kept in `firewall.nft`, loaded at boot by the existing unit, and checked with `nft -c` before
  every commit.
- The table always accepts the following before the operator's rules:
  - established and related traffic, loopback and ICMP;
  - DHCP replies;
  - the dashboard's admission mark;
  - the operator's trusted addresses.
- Changes are journaled. Recovery reloads the previous file, or deletes the table if it did not
  exist before.
- Other changes can alter the table's render, such as a trusted address added or revoked on the
  Protection page. Those changes load the table alongside their own runtime change, and both are
  taken back together. Otherwise a revoked address would stay admitted until the next boot.
- Other nft tables are listed as foreign and never touched. The owned table counts as an owned
  gateway layer.

**Tests**

- `TestOwnedFirewallRendersProtectionsBeforeRulesAndOnlyItsTable`,
  `TestOwnedFirewallRequestsAreValidated`, `TestChangeOwnedFirewallLoadsVerifiesAndPersists`,
  `TestOwnedFirewallChangesAreJournaledWithTheirUndo`,
  `TestOwnedFirewallReadsTheKernelAndForeignTables`,
  `TestGatewayCapabilityCountsTheOwnedFirewallAsOwned`,
  `TestOwnedTableIsABackendForHostsWithoutAFrontEnd`,
  `TestOwnedFirewallAdapterCarriesRulesBothWays` and
  `TestRevokingATrustedAddressReloadsTheOwnedTable`.
- `TestLiveOwnedFirewallFiltersRecoversAndLeavesOtherTables` runs in a namespace. A drop policy
  blocks a client, while an allow rule and the trusted range admit it. A foreign table survives,
  and the independent helper recovers the change.
- Browser case "an inactive ufw's configured rules and the owned nftables table say what is
  enforced".

## Final checks

Every command ran from the worktree with `TMPDIR`/`GOTMPDIR` on disk, `GOMAXPROCS=2` and
`GOFLAGS=-p=2`. Builds, servers and browser runs held the shared heavy lock and used port 43213 with
one worker. The raw logs sit beside this document.

| Check | Result | Log |
| --- | --- | --- |
| `go test ./internal/netx/ ./internal/netsec/ ./internal/store/ -count=1` | PASS: netx 25.4s, netsec 1.1s, store 10.8s (26.9s wall) | [go-packages](network-routing-firewall-go-packages.log) |
| `go test ./internal/api/ -run 'Firewall\|Routing\|Network\|Security\|Netsec\|Docker\|BGP\|Kernel\|Pending\|Forwarding'` | PASS: 12.8s (39.2s wall) | [go-api](network-routing-firewall-go-api.log) |
| `JD_NETNS_LIVE=1 go test ./internal/netx/ -run 'TestLiveRoutingMaturity\|TestLiveOwnedFirewall\|TestLiveUFWChangesAreRestored' -v` | 3 of 3 PASS: 4.7s, 5.5s and 9.0s (20.7s wall) | [go-live-netx](network-routing-firewall-go-live-netx.log) |
| `JD_NETNS_LIVE=1 go test ./internal/netsec/ -run TestLiveUFW -v` | 1 of 1 PASS: 5.7s (6.7s wall) | [go-live-netsec](network-routing-firewall-go-live-netsec.log) |
| Host state before and after the live runs | All matched except the route table, which Docker churns | [host-state](network-routing-firewall-host-state.log) |
| `network-routing-firewall.spec.ts` against a production build of `15afe802` | 14 of 14 passed in 25.9s | [browser-new-spec](network-routing-firewall-browser-new-spec.log) |
| First `scripts/test-changed.sh 4338e6cc` (at `8733c95f`) | Exit 1 after 2,845s. Prettier, ESLint, `tsc` and `bun test src` passed (3,277 tests). Go api, netx, store and netsec passed. Browser: 1,078 passed, 60 skipped, 3 failed | [test-changed-first](network-routing-firewall-test-changed-first.log) |
| `network-ui`, `security-ui` and `network-routing-firewall` specs at `86657f98` | 103 passed, 38 skipped, 0 failed in 2.6m | [browser-focused-rerun](network-routing-firewall-browser-focused-rerun.log) |
| Final `scripts/test-changed.sh 4338e6cc` (at `86657f98`) | PASS, exit 0 after 2,707s. Prettier, ESLint, `tsc` and `bun test src` passed (3,277 tests). Go api, netx, store and netsec passed; their results were cached from the identical Go source of the first gate. Browser: 1,081 passed, 60 skipped, 0 failed in 42.2m | [test-changed-final](network-routing-firewall-test-changed-final.log) |

**Why the first gate failed.** The three failures were the design system's structural rule that only
a table may be framed. The rule failed on `/network/routing` and `/network/firewall` in
`network-ui.spec.ts`, and on `/network/firewall` in `security-ui.spec.ts`. The new route history,
firewall history, plan and enforcement sections were framed `Panel`s. Commit `86657f98` draws them
`plain`. No assertion was changed.

**Selection.** The first gate selected 1,141 browser cases across 49 specs. The changed fixtures and
modules pull in most of the network, security and shell specs.

**Review.** A code review of the backend diff was run before the final gates. It raised nine
important findings, all addressed in `8733c95f`:

- a refused request set off a full host firewall recovery;
- an unreadable `systemctl is-enabled` answer could disable firewalld at boot;
- revoking a trusted address did not reload the owned table;
- an unreadable status skipped both safety nets;
- a rule identity was resolved outside the lock;
- a change from admitted to unknown was accepted;
- a possible deadlock if the firewall changed hands during a request;
- route history had no bound on full BGP tables;
- the review also claimed that SSH-tunnel operators lose their SSH check. This is not the case:
  `firewallAccess` already uses `netx.OperatorAddress`, the SSH session's address. The docs now say
  so.

The five minor findings were fixed in the same commit: TOS 0 and all-UID selectors, goto in the
discard guard, in-place replace over drift, IPv6 per-device forwarding, and plan compensation of
device rules.

## Not done

- **BGP and FRR.** FRR is not installed on this host. BGP policy, OSPF and the route browser have
  only fixture evidence. No BGP policy writer or dynamic failover controller was built.
- **firewalld.** firewalld is not installed on this host. Zones, the zone-defaults reset and
  firewalld recovery were tested only against recorded command output.
- **VRF.** The host kernel lacks the `vrf` module. `l3mdev` rules were accepted and read back by a
  real kernel, but no VRF device or traffic through one was tested.
- **Reboot.** Restoring the owned table at boot is covered by the unit render and its unit test, not
  by a reboot of a real host.
- **Route history** is sampled every 30 seconds, not event-driven.
- **Application connections** moved by a routing change are reported, not refused.

## Integration with the earlier packages

Merging this package after the VPN, diagnostics, topology and gateway packages (`86bd86a4`) needed
these corrections beyond text conflicts:

- **One owner for WireGuard transport anchors.** The VPN package had already anchored every
  kernel-reported WireGuard transport (`wgTransportAnchors`): its route may move between native
  devices but never into a tunnel, and an already captured transport is not anchored so moving it
  back out stays possible. This package's endpoint discovery also anchored WireGuard endpoints,
  but compared them strictly, which would refuse that repair and double-read the dump.
  `tunnelAnchorTargets` now discovers Tailscale direct paths only; WireGuard transports keep the VPN
  guard. `TestTailscaleDirectPathsAreAnchorsAskedWithTailscaledsMark` and the VPN transport-guard
  cases cover both.
- **One three-valued type.** The routing decision model and the gateway flow model each declared
  `tri`; `matchNo`/`matchYes`/`matchUnknown` are now names for the gateway model's values.
- **One boot unit renderer.** The owned firewall table is restored through the topology package's
  `unitFor`, which also carries bridge commands, instead of a second renderer call.
- **The route form** keeps both the topology package's device handoff (`?route=new&device=`) and
  this package's reviewed route edits.

On the assembled source, `go build ./...`, `go vet`, the `netx`, `store`, `netsec` and whole `api`
tests, 3,310 Bun tests, `tsc`, ESLint and Prettier passed. Twelve live namespace tests from the
VPN, topology, gateway and routing packages plus the real-ufw test passed with the host's
namespaces, WireGuard links, nft tables and ip rules unchanged
([log](network-routing-firewall-integration-live.log)). A production build of the assembled source
passed the routing, network UI, topology/interface and audit specs: 76 passed, 14 optional
captures skipped ([log](network-routing-firewall-integration-browser.log)).
