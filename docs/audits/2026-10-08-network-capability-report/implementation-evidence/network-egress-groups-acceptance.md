# Monitored egress groups (P15)

This checkpoint implements ledger row **P15: Monitored egress groups** on branch
`implement/network-egress-groups`, based on `c31e9329` (`origin/research/network-capability-report`).
It does not edit the ledger; the proposed status below is for the reviewer. Nothing in these checks
changed the production host's routes, rules, interfaces, queues or firewall: every kernel effect
happened inside network namespaces that each test created under a private `/run/netns` mount and
removed with its holder process. The behavior is described in the
[egress guide](../../../internal/backend/network-egress.md) and the
[module guide](../../../internal/backend/network.md#devices-namespaces-and-routing).

## What shipped

**Groups and owned objects.** An egress group (`internal/netx/egress.go`) is an ordered, weighted
set of two to eight candidate next hops — gateways (literal gateway and device) or tunnels this
dashboard owns (managed WireGuard files, managed GRE devices) — for one policy: all of this host's
default-route traffic, a source/destination selector, or a packet-mark/incoming-interface rule. Each
of six slots reserves member tables `7700+10·slot+id`, a group table, rule priorities from 19000 and
packet marks `0x100–0x3000` within `0x7f00`; hand-made routes and rules are refused there, and a slot
holding foreign objects or a foreign rule selecting those marks is refused. Member tables and mark
rules are always installed; the selector, operator exclusions (`to <protected> lookup main`) and, for
the whole-host policy, `suppress_prefixlength 0` only while the group is enabled.

**Measurement.** Every member is probed on its own interval through its own path: ICMP echo on a raw
socket, a completed TCP handshake, or any DNS answer to a named question, each socket carrying the
member's mark, bound to its device and source (`egress_probe.go`). Samples are judged by loss over a
window and median latency; `FailAfter`/`RecoverAfter` consecutive samples change a member's state;
evidence older than three intervals does not prove a member. History is bounded in memory (120) and
SQLite (`network_egress_samples`, one day).

**Decisions.** `decideEgress` (pure) fails over at once from a member declared down, holds failback
until the recovered member has been up for the stable window and the hold time has passed (or, for a
manual group, records that failback is available), keeps the route when nothing is proven, and does
not leave a member on a lack of evidence. Every state change, switch, hold, refusal, verification,
flush, simulation and automation change is a row of `network_egress_events` with the member
measurements, the route before and after, the connection report and the journal change id.

**Guarded switching.** Automated and manual ("Fail over now"/"Fail back now") switches share one path:
fresh proof of every target member; proof from each carried operator path's own reply source through
each target member (an upstream filtering foreign sources fails it, and the switch is refused with
`409 would_lock_you_out`); the switch made as one `ip route replace` through `Service.commit` — journal
before effect, independent recovery, boot batches rewritten with the decided member, pending
reconnection confirmation for interactive callers; verification that the group table holds the route
and every anchor and operator path stayed or moved only onto a proven decided member, with WireGuard
transports never moved into a tunnel. Enabling auto-protects a requester the group would carry.
Changes applied pending confirmation are recorded as pending and settled from their journal.

**Connections.** Each switch reports tracked connections (ctnetlink) attributed to the members it
leaves: moved, pinned, flushed, spared. Sticky groups pin new connections to the member they leave
through (`inet jd_egress`, connection-mark bits, original direction only, never a packet that already
carries a mark), so a failback moves only new connections. Flushing groups delete the tracked
connections of a member declared down, never those of protected or requesting operators.

**Boot and start.** The boot unit restores the objects and the last decided member from the batches
and loads `egress.nft`. The monitor's first round reads every object back, records a verified restore
(and whether its probes have proven the decided members yet), and re-adds missing objects through a
journaled change. Switches a dead process left `applying` are settled from the spec the journal left.

**Simulation and automation.** `POST /network/egress/{id}/simulate` builds disposable namespaces for
this host (the group's own tables, rules and marks), each member's upstream (dropping foreign
sources) and the internet (probe targets with TCP and DNS responders), injects tolerable latency,
total failure, flapping, recovery through the stable and hold windows, a latency breach and an
outage with `tc netem`, runs the real probes and rules on a virtual clock, judges ten expectations,
and removes every namespace (a stopped process's are removed at the next start from their record).
Automation can be turned on only for an enabled group with a passed run of its exact configuration
fingerprint, and any edit turns it off.

**API and UI.** Routes under `/api/v1/network/egress`: reads `read`; create, edit, simulate and
automation off `system.admin`; enable, disable, remove, switch and automation on in `s.destructive`;
all mutations audited; journaled ones enrolled for pending apply. `/network/egress` ("Egress groups"
in the Network section) is a reading page: headline counts, then per group its policy and carrying
member, every member's measured state, round trip, loss, sample strip and binding, the rules' advice,
runtime drift and readiness, the decision timeline with its evidence, and the last simulation's
expectations, phases, topology and limits; destructive confirmations guard every command that moves
traffic, and the group form keeps its draft after a refusal.

## Tests

| Layer | What | Result |
| --- | --- | --- |
| Go unit (`egress_test.go`) | validation, owned object rendering (IPv4/IPv6, multipath), boot batch and unit lines, switch/undo/disable/delete commands, journal recovery plan, pinning ruleset, reply selection, read-back comparison, hysteresis, decision table, simulation judgement (default passes; no hysteresis fails flap suppression; manual never fails back), automation gate, reserved tables/priorities | pass ([log](network-egress-groups/unit-netx.log)) |
| Go unit (`egress_apply_test.go`) | create installs member paths without moving traffic; foreign priorities/marks and unowned tunnels refused; enable proves members and protects the operator; switch replaces the group route, rewrites the boot file and records evidence; a path not proven from the operator's source is refused with nothing applied; edits and automation gated; interrupted switches settled from SQLite; pending changes settled from their journal | pass |
| Go API (`handlers_network_egress_test.go`) | exact route contract; every mutation refused below its capability before the module; only path withdrawals spend the destructive budget; audit actions; refusal mapping; pending enrollment | pass ([log](network-egress-groups/unit-api.log)) |
| Store | additive schema on fresh and 0.6.6 installs | pass ([log](network-egress-groups/unit-store.log)) |
| Bun | policy/hop/member/group/event/simulation readings, manual actions, phase summary, draft checks and request body, edit drafts, durations; pending enrollment | 14 pass ([log](network-egress-groups/bun.log)) |
| Browser (`network-egress.spec.ts`) | read user without commands; evidence disclosure; confirmed failover body; nothing to fail over to; automation gate on a stale/missing simulation; confirmed automation on; simulation progress; exact create body and draft retained after 409; client checks before sending; phone width; 390/1280/1720 screenshots | 13 pass ([log](network-egress-groups/browser.log)) |

### Native namespace lane

`go test -race -c` then, as root, `JD_NETNS_LIVE=1 netx.test -test.run '^TestLiveEgress'`
([final log](network-egress-groups/live-3-final.log), all seven pass, about 29 s; its header names
`3d951306` plus the then-uncommitted backend fixes committed as `581b381e`). A throwaway
namespace stands for this host; two provider namespaces forward only their own sources; an
internet namespace holds the targets, a TCP echo server and an internet operator; a connected
operator namespace reaches the host directly. The monitor is driven round by round on a controlled
clock with real probes, real switches and the real journal:

| Test | Shows |
| --- | --- |
| `FailoverAndFailbackWithHysteresis` | one lost sample moves nothing; the second fails over through the journal with the decision, routes and change id recorded; traffic follows; after recovery the stable-window hold is recorded and failback comes only after six rounds (2 to recover + 3 stable + hold); the boot file follows each decision |
| `FlapSuppression` | a primary alternating loss and clean never strings three good samples together; one switch only, no failback |
| `RefusesAnOperatorPathThroughAnUnprovenMember` | an internet operator whose replies leave from provider-a's address: a switch to provider-b is refused ("a probe from 10.201.1.2 through provider-b failed"), the route and decision unchanged and the refusal recorded; the same switch from the protected connected operator is made and that operator keeps the host |
| `BootRestoresTheDecidedMemberAndReverifies` | after a cold loss of every route and rule, replaying `links.batch` restores the decided member (provider-b) and traffic; a fresh process records the verified restore; a missing group route is put back by a journaled change and recorded |
| `RecoveryAfterBackendDeathMidSwitch` | a process dies after the kernel took the new route and the spec was being saved; a fresh recovery process with only the journal restores the previous member, the spec and the boot file |
| `StickyConnectionsSurviveFailbackAndFlushWithTheirMember` | a TCP connection opened through provider-b keeps echoing after failback (it would be dropped by provider-a's upstream unpinned); the switch reports it pinned; when provider-b goes down its connections are flushed and none stays pinned |
| `SimulationRunsInDisposableNamespaces` | a full run passes all ten expectations with failover, held failback and latency failover at the expected samples, leaves no namespace, record file or routing change; automation is then allowed; the same group without hysteresis fails flap suppression and automation is refused |

Two earlier attempts are retained: [`live-1-nft-keyword-failed.log`](network-egress-groups/live-1-nft-keyword-failed.log)
(the upstream anti-spoofing chain was named `fwd`, an nft keyword; renamed `spoof` in the lane and the
simulation) and [`live-2-simulation-compare-failed.log`](network-egress-groups/live-2-simulation-compare-failed.log)
(six tests passed; the simulation test compared IPv6 local-table routes, which the kernel adds when
link-local duplicate address detection completes; it now compares rules and IPv4 routes, the
dashboard's concern). Both fixes are in `581b381e`.

## Limits

- **No real second uplink.** This host has one provider uplink. Independent paths, provider
  anti-spoofing and failover were exercised only between namespaces; real provider failover,
  real tunnel members and a production reboot were not.
- The monitor was driven round by round in the lane; it was not run as the deployed backend's
  background loop against the host, and the unit's `egress.nft` line was not exercised by systemd.
- The simulation models every member as a gateway on a veth link, with netem on the reply side; it
  shows this configuration's decisions, not a tunnel's encapsulation, MTU or a provider's behaviour.
- Operator-source proof binds the probe to the member's device, so an asymmetric reply path fails
  it: the guard fails closed rather than proving more than it measured.
- Connection attribution by local address cannot separate members sharing an address or see
  forwarded connections that are not translated.
- Hand-made dashboard rules at priorities below 19000 are consulted before a group's rules; drift
  inspection does not yet list egress objects (the start check and the page's runtime reading do).
- Process note: early focused `go test`/`go vet` runs of the changed packages compiled without the
  shared heavy-work lock; every later build, browser run, native lane and the final gate ran inside
  a held `flock -o` section.

## Final checks

All Go commands ran from `backend/` with `GOMAXPROCS=2 GOFLAGS=-p=2` and a private
`TMPDIR`/`GOTMPDIR`; builds, the native lane, browser runs and the gate ran inside one held
`flock -o /home/ubuntu/.jd-heavy.lock` section; the frontend was served on 127.0.0.1:43216 with one
browser worker and stopped by its own process group.

The first gate ([`test-changed-1-api-timeout-failed.log`](network-egress-groups/test-changed-1-api-timeout-failed.log),
commit `581b381e`) passed formatting, `tsc`, `bun test src` (3,310), netx and store and all 90
executable browser cases, but `./internal/api` timed out at ten minutes: every test server's
shutdown waited five seconds for an egress monitor loop that had never been started. `c4f6f24f`
returns at once when the loop never started (the gateway and egress API tests went from over a
minute to 1.4 s).

`scripts/test-changed.sh c31e9329` on `c4f6f24f` exited **0** in 413 s
([`test-changed-2-final.log`](network-egress-groups/test-changed-2-final.log)): 45 changed files;
Prettier and ESLint on the changed frontend files and `tsc --noEmit` clean; `bun test src` 3,310
passed; `go build ./...`, `go vet` and the selected tests of `./internal/api` (22 s),
`./internal/netx` (36 s) and `./internal/store` ok; browser specs `design-system`, `navigation` and
`network-egress`: **90 passed**, 3 skipped (the review screenshots, which need `JD_EGRESS_SHOTS`),
0 failed. The screenshots were then taken from the same build
([390](network-egress-groups/egress-390.png), [1280](network-egress-groups/egress-1280.png),
[1720](network-egress-groups/egress-1720.png)) and inspected. No namespace, server or lock holder of
this work was left running.

## Proposed ledger status

| Row | Proposed | Reason |
| --- | --- | --- |
| P15 | implemented / acceptance pending | Implementation, regression, native namespace and browser acceptance pass, including failover, held failback, flap suppression, operator-path refusal, boot restore, recovery after death, sticky connections and a gating simulation. Failover between real independent provider uplinks (this host has one), real tunnel members, the deployed monitor loop and a production reboot remain unverified. |
