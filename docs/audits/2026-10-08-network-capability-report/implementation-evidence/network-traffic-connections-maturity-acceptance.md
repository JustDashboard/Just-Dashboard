# Traffic and connections maturity acceptance (C075–C087)

This records the Traffic, bandwidth and shaping package and the Connections and shared operator
workflows package of the feature-by-feature maturity work, branch
`implement/network-traffic-connections-maturity` from local checkpoint `465480ba` (PR head
`4338e6cc` plus the integrated VPN package). The behaviour and its boundaries are documented in
[the network module](../../../internal/backend/network.md#traffic) (traffic, shaping, routes),
[download SQM](../../../internal/backend/network-sqm.md), [socket history](../../../internal/backend/network-flow-accounting.md),
[the kernel observer](../../../internal/backend/network-flow-observer.md),
[the firewall](../../../internal/backend/observability-security.md#firewall-one-page-three-backends) and
[the Connections page](../../../internal/frontend/features-terminal.md). P14 (true download SQM:
reboot, provider queues, offload and production performance) and P8 (complete flow accounting) are
separate projects; the parts of C078, C081, C083 and C084 that belong to them stay open. This
evidence changes no ledger status; proposed statuses are listed at the end.

Raw logs are in [`network-traffic-connections-maturity/`](network-traffic-connections-maturity/),
force-added beside this document.

No host qdisc, interface, route, firewall rule, sysctl or congestion control was changed. Every
queue, veth, macvlan and dummy device in the native fixtures lived in task-owned `jdt<pid>-*`
namespaces that the fixtures delete; after each run `ip netns list` showed none left. The browser
runs used mocked API shapes; the firewall in the block tests is a recorded rule list, never the
host's ufw.

## Per row

### C075 Two-second live per-interface traffic

Shipped: every live step carries packets a second and the errors and drops it added; the same tick
reads the host's TCP counters (`/proc/net/snmp`) into a fifteen-minute TCP ring (segments sent and
resent, opens, failed attempts, resets, established). `GET /traffic/live` returns `sampledAt` and
`stepSeconds`, and with `latency=1` TCP's own RTT distribution over non-loopback sockets (`ss -tinH`,
at most every ten seconds whoever polls). The Traffic page's live window opens on the newest
reading's age (stale past three missed steps or a failed poll), uplink packets, TCP resent share over
the last minute and the round trip; each device chart states its packets and its fifteen-minute
faults (`traffic/live-context.tsx`, `use-live-traffic.ts`).

Tests: `TestParseTCPCountersReadsTheHeaderedLine`, `TestLiveStepsCarryPacketsFaultsAndTCPContext`,
`TestParseSSReadsLatencyRetransmissionsAndCongestion`, `TestLatencySummaryLeavesLoopbackOut`,
`TestTCPLatencyIsReadAtMostEveryTenSeconds`, `TestTrafficHistoryTakesAnExplicitWindow` (live shape,
API), pure `network-traffic.test.js` (age, resent share, packet/latency formatting), browser cases
"the live window says how old its reading is, with packets, TCP resent and round trip" and "a stopped
sampler reads as stale, not as a quiet link".

Limits: the RTT is TCP's smoothed estimate on whatever sockets exist, not a probe; the TCP ring is
host-wide (no per-device retransmissions); packets are counter rates at the two-second grain.

### C076 Retained interface history

Shipped: recorded rows keep the exact counter growth of their interval (`rx_bytes`, `tx_bytes`,
`rx_packets`, `tx_packets`, `span`; additive NULL-default columns, older rows unknown). `GET
/traffic/history` takes an explicit `from`/`to` (31 days at most) and returns p50/p95/p99 of the raw
interval means per device and `retainedFrom`. Alert-only transfer budgets per device (UTC
day/week/month, both/in/out; `network_interface_quotas`) are measured from the exact bytes with
older rows' estimate apart, period coverage and short-retention flags; setting is admin and audited,
clearing is destructive. `GET /traffic/annotations` (admin) returns the window's network and firewall
changes and saved diagnostic runs. The page adds a Range choice with typed bounds, drag-to-zoom on
recorded charts, p95 lines and a percentile footer per chart, change/incident marks, a pruned-start
notice and a Transfer budgets panel (`interface-charts.tsx`, `traffic/budgets.tsx`).

Tests: `TestRecordedRowsCarryExactBytesPacketsAndSpan`, `TestHistoryRangeRanksIntervalMeans`,
`TestInterfaceQuotasMeasureExactAndEstimatedUsage`, `TestSetAndClearInterfaceQuota`,
`TestTrafficAnnotationsAreChangesAndIncidents`, `TestTrafficSchemaFreshAndUpgradeKeepHistory`,
`TestTrafficSchemaOnlyAddsTablesAndIndexes`, `TestTrafficBudgetAndAnnotationRoutesAreGatedAndAudited`,
`TestTrafficHistoryTakesAnExplicitWindow`, pure range/budget tests, browser cases "a typed range
reads that window with its percentiles and the incidents in it" and "a transfer budget reads exact
and estimated bytes, alerts, and is set and cleared".

Limits: percentiles rank recording-interval means (15 s by default), not five-minute billing samples;
time the dashboard was stopped is reported as missing coverage, never filled; budgets alert and never
limit.

### C077 Container network history

Shipped: `GET /traffic/containers/{name}` (one container at twice the list's grain, with Docker's id,
image, compose project/service, state and networks); every container can be listed; pressing one
opens its chart, service and the peers the socket history attributes to its exact id
(`traffic/container-detail.tsx`, through the existing admin `GET /flows/?containerId=`).

Tests: `TestContainerDetailIsOneContainerAtAFinerGrain`,
`TestContainerTrafficDetailAnswersForRecordedNamesOnly`, pure `foldHistoricalFlows`/`hourBounds`
tests, browser case "every container can be listed and one opens into its chart, service and peers".

Limits: peer attribution exists only while socket history records (off by default), inherits its
sampling gaps and has no UDP bytes; a removed container keeps its traffic but loses its Docker half.

### C078 Per-program TCP traffic and peers

Shipped: the program read joins `ss -uanpH`: connected UDP peers are listed with `bytesKnown: false`
and unconnected UDP sockets (HTTP/3 listeners) counted per program; each read reports how many TCP
connections the kernel opened that no read saw (`ActiveOpens + PassiveOpens` against newly seen
sockets; unknown on a truncated or unreadable read) and how many sockets closed since the last read;
programs carry median RTT and resent/sent segments; the answer says whether the socket history
records, with a link, since the live read keeps nothing.

Tests: `TestParseUDPReadsStatesOwnersAndPeers`, `TestProgramsCountUDPAndConnectionsNoReadSaw`,
`TestProgramsCarryTheirMedianRTTAndRetransmissions`, the updated
`TestProcessesRunsSSAndAnswersABurstOfPollsOnce`, `TestNetworkProcessesAreAdminOnly`, browser case
"programs say what a read could not see and list UDP peers without byte counters".

Limits: UDP/QUIC bytes remain unknown without the kernel observer; short-lived connections are
counted, not attributed or measured; complete historical per-process accounting is the socket
history's (P8) and not a new store.

### C079 fq_codel/CAKE/fq selection and queue stats

Shipped: the next change's verdict per device before Apply (`ownership`: managed; kernel — `noqueue`,
a single queue of the current default with the kernel's `0:` handle, or a multiqueue `mq` whose
per-ring children are all the current default, which deleting the replacement brings back; preserved
— a classless fq_codel captured and restored; refused — with the reason), the queue tree and the
effective parameters. The parameter queue is read back after every successful apply into
`network_shaping_applied`, bound to the exact entry; the page's verification names each parameter
changed in place (`tc qdisc change`) or a replaced handle, while drift inspection keeps its plain
saved-entry comparison. A real-kernel run found that restoring a captured fq_codel lost one
microsecond of `target`/`interval` per round trip (1024 ns units printed truncated); the baseline now
writes one microsecond more and lands in the original unit.

Tests: `TestUnmanagedRootVerdicts`, `TestEffectiveOptionsAndWhatMovedSinceApply`,
`TestVerifyShapeEntryComparesTheAppliedParameters`, `TestShapingViewSaysWhatTheNextChangeDoes`, the
updated baseline expectation in `shaping_test.go`, native
`TestLiveQueueOwnershipAndAppliedParameters` (kernel/preserved/refused verdicts on real queues, exact
restore of a tuned fq_codel, applied CAKE verified, changed-in-place and replaced queues named) with
the existing F6 natives, browser case "shaping says what the next change does with each device's
queues, and refuses a foreign tree".

Limits: a multiqueue `mq` default is supported by predicate and unit test only — no owned namespace
device here has more than one transmit queue; arbitrary classful foreign trees stay refused rather
than snapshotted.

### C080 Upload rate limits

Shipped: an explicit CAKE upload profile under an upload limit (classes besteffort/diffserv3/diffserv4,
dual-srchost/triple-isolate/flows fairness, NAT lookup, DSCP wash, ACK filter, overhead, MPU, ATM/PTM
framing, RTT), rendered into the boot batch, verified option by option and kept as `ShapeSpec.upload`
(older entries keep the bare bandwidth); CAKE's per-class delay, drops and ECN marks are read from the
kernel and shown per device and in the editor (`upload-fields.tsx`, `queue-evidence.tsx`).

Tests: `TestUploadProfileIsValidatedRenderedAndVerified`, `TestCakeTinsCarryMeasuredQueueDelay`,
pure `upload-profile.test.js`, native `TestLiveUploadProfileMeasuresQueueDelayUnderDeclaredLoad`,
browser case "an upload profile goes with CAKE and the delay CAKE measured is shown".

Measured (two runs, owned namespaces, 10 Mbit/s HTB + 256000-byte FIFO access link, four saturated
uploads, 20 ms UDP echo): FIFO median round trip 187.2/197.6 ms (p95 221.5/220.8 ms) against CAKE
upload profile at 9 Mbit/s 1.0/0.9 ms (p95 1.9/1.9 ms), CAKE reporting 7.6 ms average and 17 ms peak
delay in its best-effort class. In the same fixture the CAKE runs carried 4.4/4.5 Mbit/s of upload
against 9.7/9.6 Mbit/s for the FIFO, with CAKE dropping about a quarter of best-effort packets — a
throughput cost in this fixture that is recorded, not characterised further.

Limits: one declared fixture, not a provider link; the profile's overhead is the operator's
estimate; no production latency claim.

### C081 Download rate limits

Shipped in this package: the SQM IFB queue's per-class delay is shown beside the profile, and the
shaped device's GRO/LRO/GSO/TSO offloads are read with `ethtool -k` and explained (LRO merges in
hardware before the IFB). The existing explicit IFB/CAKE lifecycle is unchanged.

Tests: `TestParseOffloadReadsTheFeaturesThatChangeADownloadQueue`, `TestCakeTinsCarryMeasuredQueueDelay`,
the existing SQM natives and browser cases (run by the gate).

Limits: actual reboot, provider queues, hardware offload behaviour and production performance remain
open in P14.

### C082 BBR setting and supported-algorithm readback

Shipped: `GET /shaping/congestion` groups live non-loopback TCP sockets by congestion control
(sockets, median/p90 RTT, resent share, median delivery rate, bytes); an actual switch keeps the
comparison as it stood just before it (`network_congestion_snapshots`, newest twenty); the Shaping
section shows now and before the last switch, with the caveat that each group is whatever its sockets
carried (`traffic/congestion.tsx`).

Tests: `TestCongestionGroupsCompareAlgorithmsOnTheSameTraffic`, `TestBBRSwitchKeepsTheComparisonBeforeIt`,
`TestCongestionWithoutSSSaysSo`, the updated `TestBBR`, the shaping route contract, browser case
"congestion control is compared on live sockets, now and before the last switch".

Limits: a comparison of this host's own workload, not a controlled experiment; BBR was never switched
on this production host.

### C083 Existing eBPF/XDP/tc programs (inspection)

Shipped: the platform from the kernel's own files (release, JIT and hardening, unprivileged loading,
BTF, bpffs, run statistics), a count of cgroup attachments, and `GET /ebpf/{id}`: one program's maps,
device/cgroup/link attachments, GPL/JIT/BTF/verified instructions and average cost per run where
statistics are on; the dashboard's own observer programs are marked (`netflows.Standing`). Read-only.

Tests: `TestEBPFPlatformAndCgroupAttachments`, `TestEBPFProgramDetail` (sanitised fixtures from this
host's bpftool 7.6), updated inventory tests, browser case "a loaded program opens into its maps,
attachments and cost, and the observer is marked".

Limits: run cost appears only where `kernel.bpf_stats_enabled` is already on (the dashboard does not
enable it), so production observer overhead remains unmeasured; platform acceptance is this kernel.

### C084 TCP/connected-UDP peer summaries

Shipped: folding keeps each peer's transports, far-end ports (bounded) and per-state counts; the
connection table is remembered between reads (`connTracker`): each tuple carries the first read that
held it, tuples missing from the next read are recorded as closes (fifteen minutes, 512 at most) with
the interval they closed in; `GET /connections/{address}` (admin) returns the live tuples with TCP
bytes, RTT, retransmissions and congestion control joined from `ss -tinH ... dst`, the closes and the
read's quality; the summary carries the interval since the last read, closes noticed and unconnected
UDP.

Tests: `TestSummarisePeersKeepsProtocolsRemotePortsAndStates`,
`TestConnectionTrackerAgesTuplesAndNoticesCloses`, `TestPeerDetailRefusesWhatIsNotAnAddress`,
`TestTCPSocketsToFiltersByAddress`, `TestConnectionDetailReadsTheKernelsTableAndNoticesTheClose`
(read-only against the host's own table: a self-connection's age, counters and the accepting side's
close), browser case "a peer opens into its tuples, ages, closes and the layers it crosses".

Limits: ages are lower bounds from the first read that saw a tuple; closes are noticed only between
page reads, and connections shorter than the read interval are not seen; UDP has no counters; the
kernel observer's packet subtotals remain on the Socket history page.

### C085 Connection graph/search/filters/held updates

Shipped: "A past hour" reads the socket history for a chosen UTC hour in the live table's shape, with
the hour's sample coverage and truncation (`connections/recorded.tsx`, shareable `when`/`hour`
filters); the peer sheet adds cross-layer evidence — firewall rules naming the address or its network
and its block, the kernel route that answers it, the socket history's last day — and hands off to the
tools, investigator, captures and history pages.

Tests: pure `sourceCovers`/`addressBytes`/`foldHistoricalFlows`/`recentHours` tests, browser cases "a
past hour is read from the socket history in the live table's shape" and the peer case above; the
existing workspace filter case in `security-ui.spec.ts` (run by the gate).

Limits: history exists only while socket history records; the investigator and capture pages are
linked, not prefilled.

### C086 Block a remote address from its connection

Shipped: `POST /firewall/blocks` with a reason, a length (an hour to thirty days, or until lifted) and
an optional incident (an existing saved run, or a new one that saves who owns the address), written
through the guarded `AddRule` path with the operator's address — so the lockout guard refuses it —
commented `jd-block <id>`; a record that cannot be written takes its rule back; a duplicate block or
an existing plain deny is refused; a server loop lifts ended blocks every minute, removing exactly
the block's rule (by comment, or by exact shape on a firewall without comments), retrying a failed
removal and auditing each outcome as `system`; `GET /firewall/blocks` reports whether each rule is
still listed; `DELETE /firewall/blocks/{id}` (destructive) lifts one. The Connections page uses a
dialog and lists the blocks with their end and incident.

Tests: `TestBlockRequestsAreValidatedBeforeTheFirewallIsTouched`,
`TestBlockingTheOperatorsOwnAddressIsRefused`, `TestATemporaryBlockIsLiftedWhenItEnds`,
`TestExpiryWithoutCommentsAndAfterAFailedRemoval`, `TestLiftingAPermanentBlockByHand`,
`TestFirewallBlocksAreGuardedRecordedAndAudited` (capability, destructive lift, lockout and incident
refusal, audit entries), browser case "a block asks why and until when, opens an incident, and is
listed with its end".

Limits: exercised against a recorded rule list; the host's own ufw/firewalld were never written to.
An expired block's rule is removed only while the dashboard runs (the loop lives in the server).

### C087 Network package-install handoffs

Shipped: `GET /network/handoffs/{package}` reads installed, configured, active and verified phases
for wireguard-tools, bpftool, crowdsec and suricata from the modules that own them (a package on disk
is never reported as working); the hand-off shows the phases after the job succeeds and the section
that replaces it keeps them until done or dismissed (`install.tsx`).

Tests: `TestHandoffsSayWhatFollowedTheInstall` (twelve cases), `TestHandoffRouteReadsOnlyThePackagesItOffers`,
`TestHandoffRouteReadsThisHost` (read-only phases of bpftool and wireguard-tools on this machine),
pure `phaseReading` test, browser case "an install is followed by configured, active and verified
phases, not by a claim".

Limits: phases are read, not repaired; verification is local (a running CrowdSec with a valid bouncer,
a readable Suricata event log, bpftool allowed to list, the WireGuard module present).

## Checks

| Check | Result | Raw log |
| --- | --- | --- |
| `JD_BROWSER_BASE_URL=http://127.0.0.1:43211 JD_BROWSER_WORKERS=1 scripts/test-changed.sh 465480ba` against a production build of the same tree (final) | **exit 0 in 1586 s** on source `389e1896` (the only uncommitted path was this document): Prettier, ESLint and `tsc` clean; 3,290 Bun tests; `go build`/`go vet` and the tests of `internal/api`, `internal/netflows`, `internal/netx`, `internal/store`, `internal/netsec` passed (reported from Go's cache: the same binaries and inputs ran uncached and passed in the first gate); **560 browser passes, 60 optional skips, 0 failures** across 31 selected specs, including all 15 `network-traffic-maturity` cases and the existing network, security, proxy-ports, VPN, design-system and navigation specs | [`test-changed-final.log`](network-traffic-connections-maturity/test-changed-final.log) |
| `JD_NETNS_LIVE=1 go test ./internal/netx -run '^TestLiveUploadProfileMeasuresQueueDelayUnderDeclaredLoad$'`, twice | **PASS** both (15.1 s each): FIFO median 187.2/197.6 ms, CAKE upload profile 1.0/0.9 ms; profile verified against the kernel before measuring | [`upload-profile-live-attempt1.log`](network-traffic-connections-maturity/upload-profile-live-attempt1.log), [`attempt2`](network-traffic-connections-maturity/upload-profile-live-attempt2.log) |
| `JD_NETNS_LIVE=1 go test ./internal/netx -run '^(TestLiveQueueOwnershipAndAppliedParameters|TestLiveShapingRestoresSupportedBaselineAndRefusesForeignTree|TestLiveShapingVerifiesEveryRequestedParameter)$'` | **PASS** (the new ownership fixture with the existing F6 natives) | [`queue-ownership-live-attempt2.log`](network-traffic-connections-maturity/queue-ownership-live-attempt2.log) |
| `go test ./internal/netx/ ./internal/netsec/ ./internal/store/ ./internal/netflows/` (whole packages, non-live) | ok | inside the final gate log |

### Failed iterations retained

- [`queue-ownership-live-attempt1.log`](network-traffic-connections-maturity/queue-ownership-live-attempt1.log):
  the first real-kernel run of the ownership fixture failed on the exact restore of a tuned
  fq_codel: `target` came back 6998 µs for 6999 µs (and `interval` 99998 for 99999). fq_codel keeps
  times in 1024 ns units and prints them truncated, so writing the printed figure back loses one unit
  per restore — a defect in the existing baseline capture that the earlier F6 native did not compare.
  The baseline now writes one microsecond more; attempt 2 and the F6 natives pass.
- [`browser-maturity-attempt1-interrupted.log`](network-traffic-connections-maturity/browser-maturity-attempt1-interrupted.log):
  the first run of the new spec. Two cases failed on their own terms (the TCP resent share of a
  regenerating fixture ring was not deterministic; pressing the chosen Range a second time did not
  reopen its form, a real UI defect, now fixed), then the coordinator stopped the orphaned server
  after an interruption and the remaining cases could not connect.
- [`browser-maturity-attempt2.log`](network-traffic-connections-maturity/browser-maturity-attempt2.log):
  12 of 14 passed; two locators were wrong (a count-up figure read before it scrolled into view, and
  a label that matched both the tuple table and the closed list).
- [`test-changed-attempt1.log`](network-traffic-connections-maturity/test-changed-attempt1.log):
  the first full gate, exit 1 — 558 browser passes, 60 optional skips and the same two locator
  failures, because the locator fix had not been written to the spec (a scripted edit failed and the
  commit that claimed it carried only the phone-width case). Go, Bun, Prettier, ESLint and `tsc` were
  clean. The fix is commit `12bf9297`.
- [`test-changed-attempt2.log`](network-traffic-connections-maturity/test-changed-attempt2.log):
  the second full gate, exit 1 — 558 passes, 60 optional skips and two test faults: a tuple age read
  as exactly "1h 30m" from a fixture whose times are fixed when it loads (the gate ran for twenty
  minutes, so it read "1h 31m"), and a wait for every animation to finish before the phone
  screenshots, which never ends on a page with an infinite live pulse. Both assertions were
  corrected (`f83bf539`); the product was unchanged.
- [`browser-maturity-attempt4.log`](network-traffic-connections-maturity/browser-maturity-attempt4.log):
  the spec alone on that build, 14 of 15; the congestion panel's request started 3.6 s into the
  assertion's 5 s window on the shared, busy host (the panel reads after the shaping view) and
  answered after it. Each case's first reading now allows 15 s (`389e1896`).

### Screenshots

From the final gate's production build, at 390 px with the maturity fixture:
[Traffic live context](network-traffic-connections-maturity/traffic-390.png),
[upload profile and CAKE delay](network-traffic-connections-maturity/upload-profile-390.png) and
[a peer's tuples and layers](network-traffic-connections-maturity/peer-sheet-390.png).

## Proposed ledger statuses

| Row | Proposed | Reason |
| --- | --- | --- |
| C075 | verified | Packet, TCP-resent, latency and observation-age context ship with unit, API and browser acceptance; the figures are kernel measurements with stated scope. |
| C076 | verified | Typed/dragged ranges, percentiles, alert-only budgets on exact interval bytes and change/incident marks pass unit, schema, API and browser acceptance. |
| C077 | verified | Every container opens into its chart, Docker service and socket-history peers, with unit, API and browser acceptance; attribution needs the opt-in history, as stated. |
| C078 | implemented / acceptance pending | UDP peers, missed-open and close counts and per-program RTT pass; UDP/QUIC bytes and complete historical process accounting remain P8's. |
| C079 | verified | Ownership verdicts, exact restore of a captured fq_codel and applied-parameter drift pass unit, native and browser acceptance; the `mq` default is covered by predicate and unit test only. |
| C080 | implemented / acceptance pending | CAKE upload classes, DSCP and overhead profiles verify against a real kernel and cut the fixture's loaded round trip from ~190 ms to ~1 ms; the benefit is one declared fixture (with a recorded throughput cost), not a production measurement. |
| C081 | in progress | SQM delay and offload evidence added; reboot, provider queues, offload behaviour and production performance stay in P14. |
| C082 | implemented / acceptance pending | The live per-algorithm comparison and the kept "before" pass unit, API and browser tests; no real switch was made on this host. |
| C083 | implemented / acceptance pending | Platform and per-program detail with the observer marked pass unit and browser acceptance; production observer overhead stays unmeasured without run statistics. |
| C084 | implemented / acceptance pending | Full tuples, ages, closes, counters and quality pass unit, real-kernel API and browser acceptance; general production flow accounting stays P8's. |
| C085 | verified | Past-hour selection from socket history and cross-layer evidence with hand-offs pass pure, browser and existing filter acceptance. |
| C086 | implemented / acceptance pending | Reason, end, incident, guarded add and exact expiry pass unit, API and browser tests against a recorded rule list; never exercised against this host's real firewall. |
| C087 | verified | Installed/configured/active/verified phases are read from the owning modules (this host's included) and shown after an install, with unit, API and browser acceptance. |

## Spec timing

`frontend/tests/browser/network-traffic-maturity.spec.ts` passed 15/15 when it was written and then failed 5–7 of 15 on a busy shared host. The traces showed every expected value rendering; the cases were out of time, not wrong. No product code was involved and none was changed. The fix is in the spec and its fixture (`network-traffic-maturity-fixture.ts`) only; every assertion keeps its meaning and no case, retry or weaker check was added.

The slowdown was reproduced on this host by running the spec against a production build with busy-loop processes competing for the CPU (`spec-timing-before-cpu-stress.log`: 11 competing loops, 12 of 15 failed; 8 loops still passed 15/15, so the host has a cliff rather than a slope).

| Case | Cause | Fix |
| --- | --- | --- |
| Every Traffic case (all but the three Connections ones) | The page shell took 10–15 s to appear and each later action 5–13 s, against the default 5 s expectation and 30 s test timeout. Most cases never waited for the page at all; a click or fill that runs before the page has hydrated and loaded races it. | One `visit()` helper opens the page and waits for `loaded()` (shell present, no skeleton left); every case uses it. `expect` is configured at 15 s and the cases' timeout at 120 s for the file, the way the database specs do, in place of the scattered per-assertion `{ timeout: 15_000 }`. |
| Live window, "3 drops in 15 min" | Fixture data tied to time, not to the poll. The live handler ignored `since` and returned a whole ring every poll with the drop on "the fifth point from the end". The page appends points newer than the last it holds, so a slow poll minted a second drop and the page summed them: `6 drops in 15 min` was received. The whole-ring answer was also five devices of 450 points parsed and merged every two seconds. | `liveRoute()` fixes the drop's instant once per page (`dropAt`) and honours `since` as the API does. |
| Stopped sampler, `/4\ds old/` | Asserted at 5 s while the context still read "Newest reading … measuring…". | Readiness wait and the longer expectation, as above. |
| Typed range, transfer budget, programs, containers, shaping, upload profile, congestion, install, phone width | Timeouts only: the budget case's PUT was "not recorded" because its clicks and fills were still queued behind a starved page when the 5 s poll ran out; the others waited on elements the page had not yet drawn. | As above. |
| Loaded program, `41` read as `40` | The `NumberTicker` is an overdamped spring that counts up once on screen, so any read before it settles sees a partial count (`13` seen in a 300 ms check without the fix). | That case emulates reduced motion (`page.emulateMedia({ reducedMotion: "reduce" })`), under which the ticker writes the value at once; checked that it reads `41` within 300 ms with it and `13` without. |

Checks that the spec still bites: with the budget fixture's used bytes changed from 790 to 700 GiB the budget case fails on `830.0 GB of 1.0 TB`; with the reduced-motion line removed the program case fails on the partial count.

Runs against a production build of this tree (`bun run start --hostname 127.0.0.1 --port 43218`), one section under the heavy lock each:

| Run | Result | Log |
| --- | --- | --- |
| `JD_BROWSER_WORKERS=1`, run 1 | 15 passed (1.7 m) | `network-traffic-connections-maturity/spec-timing-workers1-run1.log` |
| `JD_BROWSER_WORKERS=1`, run 2 | 15 passed (1.7 m) | `.../spec-timing-workers1-run2.log` |
| `JD_BROWSER_WORKERS=1`, run 3 | 15 passed (1.7 m) | `.../spec-timing-workers1-run3.log` |
| `JD_BROWSER_WORKERS=3` | 15 passed (55 s) | `.../spec-timing-workers3.log` |
| `JD_BROWSER_WORKERS=1`, 11 competing CPU loops (the condition that failed 12 of 15 before) | 15 passed (9.6 m) | `.../spec-timing-workers1-cpu-stress.log` |
| `network-ui.spec.ts` and `network-topology-interfaces.spec.ts`, one worker | 40 passed, 14 skipped (1.2 m) | `.../spec-timing-network-ui-and-topology.log` |

The 14 skips are `network-ui.spec.ts`'s screenshot cases, which run only when `JD_NETWORK_SHOTS` names a directory. The fixture's other time-relative values (quota periods, block expiry, ages) are already built from `Date.now()` and read loosely by the spec; they were not the cause of any failure.
