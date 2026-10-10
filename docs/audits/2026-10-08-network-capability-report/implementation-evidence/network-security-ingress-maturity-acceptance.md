# SSH, intrusion prevention, metrics and ingress maturity (C130–C135)

This checkpoint implements the requested improvements of ledger rows C130–C135 ("Networking
capabilities elsewhere in the dashboard": SSH configuration and sessions, Fail2ban, CrowdSec,
Suricata, network metrics and host-health findings, dashboard ingress, allowlist and private
previews) on branch `implement/network-security-ingress-maturity`, based on `c31e9329`. It does not
edit the ledger; the proposed statuses below are for the reviewer.

Nothing in these checks changed the production host's sshd configuration or service, fail2ban,
CrowdSec, Suricata, Caddy, the allowlist, its firewall or its tailnet. Every SSH recovery command
ran against a recorded transcript or, in the one fresh-process case, against a fake `systemctl` on a
`PATH` holding nothing else. Real tool output was recorded only from owned, uniquely named and
completely removed environments: a `python:3.11-slim` container running fail2ban 1.1.0
(`jd-c131-f2b-*`), one running Suricata 7.0.10 (`jd-c133-suricata-*`), and network namespaces
(`jdsec-c132-*`, `jdsec-c133-*`) for nft and iptables output. The only live kernel reads were
read-only: `/proc/net/snmp` and `/proc/net/netstat` (copied as fixtures) and a `sock_diag` dump that
found a loopback connection the test itself opened. The behaviour is described in
[observability-security](../../../internal/backend/observability-security.md) (sshd, fail2ban,
CrowdSec, Suricata, metrics and the access boundary sections), the
[recovery guide](../../../internal/backend/network-recovery.md), the
[request lifecycle](../../../internal/architecture/request-lifecycle.md#checks-that-depend-on-what-a-request-carries)
and [invariants](../../../internal/security/invariants.md).

## Per row

### C130 — SSH configuration and sessions

Requested: connect effective settings, staged guarded apply, keys, jump-host profile and session
handling to network confirmed-apply and recovery.

Shipped: an SSH apply can be a pending apply in the network recovery journal.
`X-JD-Network-Apply: pending` on `POST /ssh/config` is accepted only from an administrator's
interactive session (`session_required` otherwise). After `PlanSSHSettings` and every existing
lockout guard, `netx.BeginSSHChange` snapshots each file the plan will write
(`SSHApplyPlan.Files`: the managed file and, for a socket port move, the socket drop-in) into
`change.json` as subsystem `sshd`, arms the ninety-second `systemd-run` timer, and only then starts
the job. A reload or socket failure (`SSHApplyResult.Failure`) restores the snapshots at once; a
success waits in `awaiting_confirmation` for the existing account/session/source-bound verify and
confirm. The journal admits only `sshd_config`, `sshd_config.d/99-just-dashboard.conf` and the
`ssh|sshd.socket.d/10-just-dashboard.conf` drop-in, and only `reload` and `socket <unit>` undo; any
other file, command or subsystem is refused before recovery touches anything. The independent
helper restores and reloads sshd without the backend; at boot it uses `--no-block` and
`try-reload-or-restart`. The SSH page offers "Restore unless confirmed (90 s)" (on by default where
the watchdog is available), the global confirmation notice speaks of an SSH change, and the
staged change is judged against the access boundary (C135) before it is sent.

Tests: `TestSSHChangeWaitsForAFreshResponseAndKeepsAConfirmedConfiguration`,
`TestUnconfirmedSSHChangeIsRestoredByTheHostWithoutTheBackend`,
`TestBootRecoveryOfAnSSHChangeDoesNotWaitOnSSHJobs`, `TestFailedSSHApplyIsRestoredImmediately`,
`TestSSHChangeAcceptsOnlyAPendingOwnerAndAnArmedWatchdog`,
`TestSSHJournalRefusesForeignFilesAndCommands`, `TestSSHChangeStatusKeepsSnapshotsOutOfTheAPI`,
`TestSSHRecoveryRunsInTheStandaloneHelperAfterTheBackendDies` (the backend fixture dies with exit
84, the real `RecoverNetworkStandalone` entry point restores the drop-in and runs a fake
`systemctl`), `TestSSHPlanNamesEveryFileItWrites`, `TestSSHApplyResultFailureCoversWhatDidNotTakeEffect`,
`TestPendingSSHApplyNeedsAnAdministratorSession`; browser "an SSH change applies until confirmed, with
what it does to the tunnel shown first", "an SSH change on a host without the watchdog applies
immediately as before", "a pending SSH apply is confirmed through the same banner in SSH's words".

Limits: no real systemd timer fired against a real sshd here — doing so would reload the
production sshd. A pending SSH change and a pending network change share the one journal, so each
blocks the other until confirmed or recovered. Existing sessions survive any reload, so recovery
restores the configuration for the next login; it does not end sessions.

### C131 — Fail2ban

Requested: add policy explanation and cross-engine deduplication.

Shipped: `GET /fail2ban/{jail}/policy` (`JailPolicy`/`ExplainJailPolicy`) reads the running
values, what the jail watches, `ignoreself`, every action and each action's `port` and `type`, and
says the rule as a sentence, what each action does with a ban (firewall, blackhole route,
Cloudflare edge, report only, unknown), whether the sshd jail's ban covers the ports sshd actually
listens on (service names and ranges resolved), and which running values differ from what the
dashboard's drop-in will load at restart. Multi-action lists are now parsed (`parseActionList`; the
old address-list parser dropped a two-action line as prose, which also corrected
`JailConfig.Actions`). `GET /security/blocks` (`MergeBlocks`) folds fail2ban bans, CrowdSec ban
decisions and the firewall's inbound denies by canonical prefix, naming every source and the
broader blocks that already contain each entry, counting community-only decisions, and saying
which engines were not read. The Intrusion page opens on "Blocked across engines" with filters for
duplicates and covered entries; both ban forms say which engine already holds the typed address;
the jail sheet shows the policy.

Tests: `TestJailPolicyReadsTheRunningServer` (Fail2Ban 1.1.0's own answers, including an sshd
moved to 2222 with a ban on `ssh`), `TestJailPolicyExplainsActionsCoverageAndDrift`,
`TestParseActionListReadsEveryAction`, `TestJailDropInIsReadForOneSection`,
`TestJailPolicyRefusesABadJailName`, `TestMergeBlocksFoldsEnginesByAddressAndNamesCoveringRanges`,
`TestMergeBlocksSaysWhichEnginesWereNotRead`, `TestBlockPrefixCanonicalisesMappedAndHostForms`;
`bun test` `blocks.test.js`; browser "every engine's refused addresses are folded into one list by
address", "both ban forms say which engine already holds the address", "the sshd jail's policy says
its ban misses the port sshd moved to".

Limits: the drift comparison reads only the dashboard's own drop-in, not the full
jail.conf/jail.local merge. `cscli decisions list` returns local decisions unless asked for the
community list, so community overlap is only as complete as that listing. Action classification is
by name; a custom action is "unknown", never assumed to block.

### C132 — CrowdSec

Requested: verify that an enforcing bouncer is actually active before claiming protection.

Shipped: `CrowdSecView.enforcement` (`AssessEnforcement`): a bouncer counts only with a valid key, a
pull within three minutes and, for the firewall bouncer, an active `crowdsec-firewall-bouncer`
unit; a fresh firewall bouncer is `enforcing` only when the kernel holds a CrowdSec set with a drop
or reject rule in a hooked chain (nftables, or ipset with the iptables rule) and the sets are not
empty while ban decisions are in force, otherwise `degraded`. Proxy-only is `partial`, an
unreadable kernel or unknown bouncer is `unverified`, no fresh pull `stale`, none registered
`unenforced`, a stopped engine `stopped`, an unreadable bouncer list `unverified`. The panel's head,
tile and notice speak the verdict, with each bouncer's pull age and unit and each kernel set's
entries and hook; the posture raises `intrusion.crowdsec-unenforced` (warning) or a partial/unverified
notice from the same verdict.

Tests: `TestEnforcementIsClaimedOnlyFromAFreshPullAndAKernelDrop` (14 states),
`TestBouncerEvidenceDatesPullsAndClassifiesKinds`, `TestParseCrowdSecTableCountsSetsAndHookedDrops`
(nft's own JSON from a namespace, including a drop in an unhooked chain that must not count),
`TestIPSetModeReadsEntriesAndTheDropRule`, `TestCrowdSecViewReportsEnforcementFromTheKernel`,
`TestCrowdSecViewDoesNotClaimProtectionWhenTheBouncerListFails`,
`TestPostureTakesCrowdSecEnforcementNotItsRunningState`; `bun test` `enforcement.test.js`; browser
"CrowdSec reads its decisions, alerts and bouncers" (now asserting the verdict and kernel sets), "a
bouncer pulling into a flushed kernel table is not called protection", "with no valid bouncer
CrowdSec says nothing is enforcing its decisions".

Limits: no real CrowdSec engine or bouncer ran; the bouncer listing is the existing recorded
`cscli` fixture and the ipset fixture follows the documented `ipset list -t` format (ipset is not
installed here). A proxy bouncer's own drop cannot be read from the host, which is why it is
`partial`, not `enforcing`.

### C133 — Suricata

Requested: beyond configuration-derived state, bounded alerts and rule counts — an installation,
interface, rule-update and inline-policy management workflow.

Shipped: after the existing install handoff, a Setup section: the capture interface
(`POST /security/suricata/interface` rewrites the first non-default af-packet line in place, tests
with `suricata -T`, restores on failure, restarts a running Suricata and restores the previous
interface if it does not come back; refused in inline mode, when the unit names its own interface,
or for an interface that is not up), whether the capture sees packets (the newest `stats` event's
kernel packets, drops and decoder packets), the rules (enabled count, age, `suricata-update`
sources, `POST …/rules/update` running `suricata-update` and reloading), and starting the service
(`POST …/start`, `systemctl enable --now`, checked to stay up). Inline policy is evidence only: every
NFQUEUE rule from `iptables-save`, `ip6tables-save` and `nft -j list ruleset` with whether it
bypasses (fails open), and the page explains why queue rules are not changed from here. The mode is
now read from the service's command line first, because Debian's package ships
`LISTENMODE=nfqueue` beside a unit that runs `--af-packet` and never reads that file — the old order
called every stock Debian install an IPS.

Tests: `TestLatestCaptureReadsTheNewestStatsEvent` (Suricata 7.0.10's own stats event),
`TestAfPacketInterfacesAreReadAndRewrittenLineForLine` (Debian's suricata.yaml section; exactly one
line changes), `TestQueueRulesAreReadWithTheirFailOpenFlag` (real iptables-save and nft JSON),
`TestInlineEvidenceSaysWhetherAStoppedSuricataCutsTraffic`, `TestSuricataViewCarriesItsSetup`,
`TestPlanSuricataInterfaceRefusesWhatCannotCapture`,
`TestApplySuricataInterfaceTestsRestartsAndRestores` (applied, rejected by `-T`, did not come back),
`TestUpdateRulesAndStartReportWhatTheHostDid`, `TestSuricataMode` (Debian's real defaults beside its
af-packet unit now reads IDS), `TestSuricataInterfaceIsValidatedBeforeTheHost`,
`TestIntrusionCapabilities` (viewer refused on the three new routes); browser "Suricata's setup
moves the capture, fetches rules and reads the queue it depends on".

Limits: switching between IDS and IPS, and writing queue rules, is deliberately not offered (a
fail-closed queue cuts every queued port, the dashboard's included); the workflow is af-packet IDS.
`suricata-update` was not run (it downloads the rule set); its job is tested against a transcript.

### C134 — Network metrics and host-health findings

Requested: add latency, retransmits, connection failures and probe correlation.

Shipped: the collector reads TCP's MIB (`/proc/net/snmp`, `/proc/net/netstat`) into per-second
rates and the kernel's smoothed RTT of established connections to external peers over `sock_diag`
(median and 90th percentile; loopback, link-local and private peers excluded, the tailnet kept;
"none" when nothing is connected). The recorder stores them in additive nullable columns, so older
rows read as unmeasured; `Range` returns the bucket's resent share with the worst sample's as
peak, failed attempts and accept-queue drops with peaks, and the mean median RTT with the highest
p90. Health judges a window like the link checks: `tcp:retransmits` (notice ≥2%/≥200 resent,
warning ≥5%/≥1,000), `tcp:listen-drops` (warning ≥10), `tcp:attempt-fails` (notice ≥50 and ≥20% of
opens), `tcp:latency` (notice when the median is ≥150 ms and ≥3× the hour's recorded mean); the
network area's summary adds the resent share and RTT. For an administrator, saved diagnostic runs
that met trouble in the last half hour are attached to network findings as `correlated` (and alone
raise `probes:beyond-host`). The metrics page charts Resent segments, Connection RTT and Failed
connections, and the advisor sheet lists the correlated runs with links to `/network/runs?run=`.

Tests: `TestTCPCountersAreReadByColumnName` (this kernel's own MIB files),
`TestTCPRatesAreDeltasAndRefuseCountersThatWentBack`,
`TestLatencySummaryIsNearestRankAndSaysWhenNothingWasMeasured`,
`TestSockDiagFindsAnEstablishedConnectionsRTT` (the netlink request and `tcpi_rtt` offset against
this kernel, on a loopback connection the test holds), `TestTCPWindowJudgesRetransmitsDropsAndFailures`,
`TestTCPWindowStaysQuietForNormalTraffic`, `TestLatencyIsANoticeOnlyWhenSlowAndWellAboveTheHour`,
`TestNetworkAreaCarriesTheTCPReading`, `TestRecordedTCPSeriesAreNullBeforeTheyWereSampled`,
`TestProbesAreCitedBesideNetworkFindingsAndAloneSayTheTroubleIsBeyond`, store schema tests;
`bun test` `server-advisor.test.js`; browser "connections are charted by how they fared and a
network finding cites its probes".

Limits: latency is passive — the RTT of connections the host already has; nothing is probed, so a
quiet host reports none. The TCP series are recorded, not streamed (live mode says so). Failed
attempts include inbound half-open scans, which is why they are a notice. Correlation is by time
window and outcome, not by target or path.

### C135 — Dashboard ingress, allowlist and private previews

Requested: preserve Caddy ingress, the pre-auth allowlist, tailnet/SSH access and isolated preview
boundaries in every proposed network change.

Shipped: `GET /security/boundary` (`DescribeBoundary`) reads five boundaries as held, broken or
unknown: Caddy alone on the configured port with no other dashboard socket on a routable address,
this request inside the running allowlist (or behind the SSH session carrying a tunnel), a
tailscale interface up where the allowlist admits the tailnet, a socket on sshd's port, and every
preview port in 21000–21999 served to a loopback upstream and not funnelled. `BoundaryImpacts`
judges bans, firewall rules and defaults, and SSH changes against it (`cuts` this session's way in,
or `affects` an allowlisted network, the tailnet's previews, DERP fallback, or who can tunnel);
`GET /security/boundary/check` answers forms without being audited as a change, and
`api.boundaryGate` enforces it on the fail2ban ban, CrowdSec decision and SSH routes (`409
would_lock_you_out` for a cut, `409 boundary_acknowledgement_required` until an effect is
acknowledged). Every pending network change — any journaled network route and the pending SSH apply
— has the boundary read before it is applied, and the reconnection verification returns it now
beside that picture; the confirmation notice lists the boundaries and words its button "Confirm
anyway" when one was lost. The Security overview shows the access boundary.

Tests: `TestBoundaryHoldsOnATailnetHost`, `TestBoundaryNamesEachWayItBreaks` (nine breaks),
`TestABanIsJudgedAgainstTheSessionTheAllowlistAndPreviews`,
`TestAFirewallRuleIsJudgedByThePortsItReaches`, `TestAnSSHChangeIsJudgedForTheTunnel`,
`TestBoundaryComparisonNamesWhatWasLost`, `TestAccessBoundaryAndItsCheckAreReads`,
`TestABanInsideTheAllowlistNeedsAcknowledgement`,
`TestVerificationComparesTheBoundaryWithItsBeforePicture`; `bun test` `boundary.test.js`; browser
"the overview reads the access boundary and names the part that broke", "a ban across the access
boundary is shown first and sent acknowledged", "a ban that would cut this session cannot be sent",
"verification shows the access boundary after the change and names what was lost".

Limits: enforcement covers this package's mutations (bans, decisions, SSH). The firewall, gateway
and other Network forms belong to other packages; they can call the check route, and their pending
applies already get the before/after comparison, but their forms do not yet show impacts before
the request. The before-picture lives in memory for ten minutes and is lost on a backend restart
(the verification then says so). No change was proposed against this host's real Caddy, allowlist
or tailnet; the boundary was read from it only by the API tests' read-only routes.

## Check results

Raw logs are in [`network-security-ingress-maturity/`](network-security-ingress-maturity/). All Go
commands ran from `backend/` with `GOMAXPROCS=2 GOFLAGS=-p=2` and a private `TMPDIR`/`GOTMPDIR`;
every build, server and browser run held a shared heavy-work lock (`flock -o`, lane B once it
opened), served on loopback port 43214 with one worker, and stopped its server.

| Check | Command | Result |
| --- | --- | --- |
| New and touched Go tests | `go test -count=1 -v` on the selected `netx`, `netsec`, `metrics` and `sysinfo` tests ([log](network-security-ingress-maturity/go-unit-selected.log)) | 79 passed, 0 skipped, 0 failed |
| Changed Go packages | `go test ./internal/netx ./internal/netsec ./internal/metrics ./internal/sysinfo ./internal/store -count=1` | all ok |
| API tests beside the changed handlers | the 43 tests of `handlers_security*`, `handlers_netsec*`, `handlers_network_changes*`, `handlers_system*` | ok |
| Frontend unit | `bun test src` | 3,309 passed |
| Format, lint, types | Prettier and ESLint on the changed frontend files, `tsc --noEmit` | clean |
| Screenshots | `security-intrusion.spec.ts -g screenshots` with `JD_NETWORK_SHOTS` ([log](network-security-ingress-maturity/shots-specs.log)) | 8 passed; Intrusion and SSH at 390 and 1440 inspected (merged blocks, enforcement evidence, Suricata setup and inline queue) |

### Final gate

`scripts/test-changed.sh c31e9329` reads 103 changed files and selects Prettier, ESLint, `tsc`,
`bun test src`, `go build ./...`, `go vet` and the tests of `./internal/api`, `./internal/sysinfo`,
`./internal/metrics`, `./internal/netx`, `./internal/store` and `./internal/netsec`, and 33 browser
specs (815 cases: the security, metrics, server-advisor and network-confirmation specs, plus every
spec importing the changed host fixture and the shell's `design-system` and `navigation`).

| Run | Commit | Result | Log |
| --- | --- | --- | --- |
| gate 1 | `49a4658d` | Prettier, ESLint, `tsc`, `bun test src` (3,309) clean; `go build`, `go vet` and the six packages' tests ok (api 163 s, uncached); browser 782 passed, 32 skipped, **1 failed**: this package's own "a bouncer pulling into a flushed kernel table" case asserted text that appears twice (strict-mode violation). The assertion was pointed at the cause beside its bouncer (`530b4c12`). | [gate1](network-security-ingress-maturity/gate1-test-changed.log) |
| gate 2 | `530b4c12` | the corrected case passed; browser 782 passed, 32 skipped, **1 failed**: `logs-views.spec.ts` "Back returns from a unit run's question" (a line clicked before the history view settles). Repeated alone on this build it failed 1 of 5 ([log](network-security-ingress-maturity/flake-logs-views-specs.log)); on a build of the base `c31e9329` in a throwaway worktree it failed 4 of 10 ([log](network-security-ingress-maturity/flake-logs-views-base.log)) — a flake that predates this package, which touches no logs code. | [gate2](network-security-ingress-maturity/gate2-test-changed.log) |
| gate 3 | `530b4c12` | browser 782 passed, 32 skipped, **1 failed**: `database-query.spec.ts` "a failed statement is marked at the word the engine names" (the Monaco squiggle not yet laid out; passed in gates 1, 2 and 4). | [gate3](network-security-ingress-maturity/gate3-test-changed.log) |
| **gate 4 (final)** | `530b4c12` | **exit 0.** Prettier, ESLint, `tsc` clean; `bun test src` 3,309 passed; `go build`, `go vet` and `./internal/{api,sysinfo,metrics,netx,store,netsec}` ok (cached from the identical Go sources of gates 1–3); browser **783 passed, 32 skipped, 0 failed, 0 flaky** in 33.6 min with one worker. Run with `CI=1`, which gives Playwright one retry for the environment flakes above; none was needed. | [gate4](network-security-ingress-maturity/gate4-test-changed.log), [build](network-security-ingress-maturity/gate4-build.log) |

The 32 skipped cases are the Intrusion/SSH and Security screenshot cases, which run only with
`JD_NETWORK_SHOTS`; the eight Intrusion/SSH ones were run separately above. Every server was stopped
after its run; no test process, container, namespace or worktree of this package is left.


## Proposed ledger statuses

| Row | Proposed | Reason |
| --- | --- | --- |
| C130 | implemented / acceptance pending | Pending SSH applies enrol in the journal with a closed recovery vocabulary and pass unit, fresh-process standalone-helper, API and browser acceptance; no real systemd timer was fired against a real sshd, which on this host would reload the production daemon. |
| C131 | verified | Policy explanation (against Fail2Ban 1.1.0's own output) and cross-engine folding by address pass unit, API and browser acceptance; drift reads only the dashboard's drop-in. |
| C132 | verified | Protection is claimed only from a fresh pull plus a hooked kernel drop, read from nft's own JSON; unit, posture and browser acceptance pass. No live CrowdSec engine ran; proxy enforcement stays `partial`. |
| C133 | in progress | Install-to-running setup (interface move with test and restore, rule update, start, capture evidence) and read-only inline queue evidence pass unit, API and browser checks, and the Debian mode misreading is fixed; inline-mode management is deliberately not offered and no real Suricata ran the jobs. |
| C134 | verified | TCP retransmits, failed attempts, accept drops and connection RTT are read from this kernel (MIB files and a live `sock_diag` dump), recorded additively, judged over windows and correlated with saved probes; unit, store and browser acceptance pass. Latency is passive. |
| C135 | in progress | The boundary model, check route, gates on bans, decisions and SSH, and the before/after comparison on every pending network change pass unit, API and browser acceptance; firewall and gateway forms (other packages) do not yet show impacts before a request, so "every proposed network change" is not complete. |
