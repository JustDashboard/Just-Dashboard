# Monitored egress groups

An egress group is a policy-selected set of candidate next hops — gateways, or tunnels this dashboard
owns — measured continuously through each member's own path, with failover, failback and every hold
decided by explicit hysteresis rules and recorded with the evidence behind them. It is project P15 of
the [capability report](../../audits/2026-10-08-network-capability-report/implementation-status.md).
Files: `internal/netx/egress*.go`; routes in `internal/api/handlers_network_egress.go`; the page is
`/network/egress` (`components/network/egress/`, `lib/network-egress.ts`).

## What a group owns

A group takes one of six slots. A slot reserves, in the group's family:

| Object | Where | Purpose |
| --- | --- | --- |
| Member tables | `7700 + 10·slot + member id` | One default route through the member (`via GW dev DEV`, or `dev TUN` for a tunnel). Always installed. |
| Member rules | priority `19000 + 16·slot + (id − 1)` | `fwmark 0xID00/0x7f00 lookup <member table>`. Probes carry the member's mark; a pinned connection is restored onto it. Always installed. |
| Group table | `7700 + 10·slot` | Default route through the decided members: one next hop, or weighted `nexthop` legs of one tier. |
| Operator exclusions | `+8 … +11` | `to <protected> lookup main`, while enabled. |
| Suppress rule | `+12` | `lookup main suppress_prefixlength 0` for the whole-host policy, while enabled: connected networks, Docker, the tailnet and every specific main-table route keep answering first. |
| Selector rule | `+13` | The policy's selector `lookup <group table>`, while enabled. |
| Pinning | `inet jd_egress` (`egress.nft`) | Sticky groups only, while enabled. |

Tables 7700–7759 and priorities 19000–19095 are refused to hand-made routes and rules
(`checkTable`, `checkRuleTable`, `RuleRequest.spec`). Creating a group refuses a slot whose tables
or priorities already hold anything, and any foreign policy rule selecting marks inside `0x7f00`.
Member marks run from `0x100` to `0x3000`: clear of Tailscale's `0xff0000`, the gateway's
`0xff000000`, and the `0x4a00` and `0x4000` that wg-quick (`0xca6c`) and kube-proxy leave in that
space.

Policies are `all` (everything the main table would send to its default route), `selector` (source
and/or destination network) or `rule` (a packet mark outside `0x7f00` and/or an incoming interface).
A group has two to eight members (`gateway` with a literal gateway and device, or `tunnel` — a
managed WireGuard file or a managed GRE device), one to four literal probe targets (ICMP echo, a
completed TCP handshake, or any DNS answer to a named question) and thresholds: interval, timeout,
latency, loss over a window, consecutive samples to fail and to recover, hold time between switches
and stable time before failback. The latency threshold must be below the timeout, and the timeout
below the interval. Members of one tier share it by weight; the lowest tier with a proven member
carries traffic.

## Measuring

`egressMonitor` (`egress_monitor.go`) starts with the network module. Each group is sampled on its
own interval; each member is probed in-process on a socket carrying its mark, bound to its device
(`SO_BINDTODEVICE`) and to its source address, whichever member carries traffic. A missing member
route therefore fails rather than leaking through another path. A sample is bad when no probe
answered, when loss over the window exceeds the threshold, or when the median round trip exceeds the
latency threshold (`judgeSample`). `FailAfter` consecutive bad samples declare a member down;
`RecoverAfter` consecutive good ones declare it up. A member is *proven* only when up with a sample no
older than three intervals. A restarted monitor resumes the loss window from the record but earns
every verdict again. Each round's samples are kept in `network_egress_samples` for a day; the view
draws the last sixty.

## Deciding

`decideEgress` (`egress_decide.go`) is pure and shared by the monitor, the manual switch's
advice and the simulation:

- Moving away from a decided member that is down is a **failover** (or a **rebalance** within a
  multipath tier). It happens as soon as the member is declared down; the hold time never keeps
  traffic on a dead path.
- Moving to a better tier, or adding a recovered member back to the tier, is a **failback**. It waits
  until the recovered member has been up for `StableSeconds` and the last switch is `HoldSeconds`
  old. A `manual` group records that failback is available and leaves it to the operator.
- With no proven member the group **keeps its route**: withdrawing it would strand the traffic anyway.
- A decided member that is unproven but not down is not left on a lack of evidence.

A flapping member never strings `RecoverAfter` good samples together, and when it does, the stable
window holds the failback. Every state change, switch, hold (once per situation), refusal and
failure is a row of `network_egress_events` with the measured members, the route before and after,
the connection report and the journal change id. Events are kept 90 days, at most 2,000 per group.

## Switching

A switch — the monitor's, or an operator's **Fail over now** / **Fail back now** — is
`switchEgress` (`egress_apply.go`):

1. Every target member must answer a fresh probe round now, all probes within the thresholds.
2. Every operator path the group would carry after the switch — the requester's, and for an automated
   switch each protected address — must be proven through each target member **from its own reply
   source**. An upstream that filters foreign sources drops exactly these replies, so a member
   proven only from its own address is not proven for the operator. A failure is a
   `409 would_lock_you_out` and nothing is applied.
3. The tracked connections are attributed and counted (below).
4. The change is one `ip route replace` of the group table, made through `Service.commit`: the
   journal records the previous decision before the effect, the independent recovery executable can
   put it back without the backend, the boot files are rewritten with the decided member, and a
   pending interactive apply needs reconnection confirmation like any other covered mutation.
5. Verification reads the group table back, then asks the kernel again for every anchor and operator
   path: each must be unchanged, or moved onto a decided member — and an operator path only if step 2
   proved it. A WireGuard transport may never move into a tunnel, including a decided tunnel member.
   Anything else is put back.

Enabling a group proves its decided members the same way. If the group would carry the requester's
replies, their address is added to the protected list (kept on the main table) rather than proven,
and enabling from a fifth operator address is refused. Disabling and removing hand the traffic back
to the main table: paths may move, but none may be left without a route. Editing is refused while a
group carries traffic, and a changed configuration turns automation off.

## Connections

A route change moves new packets, not what the connection table holds. Each switch reports the
tracked connections attributed to the members it leaves (`egress_conntrack.go`, ctnetlink): by the
pinning mark for a sticky group, otherwise by the member's local address — the source of a connection
this host opened, or the translated address of one it masqueraded. Members sharing an address are not
told apart, and forwarded connections without translation are not attributed.

A **sticky** group pins a new connection, as it leaves, to the member it leaves through (device and,
for a gateway, `rt nexthop`), in the connection mark's `0x7f00` bits. Later packets in the original
direction carry that mark, so the member rule keeps them on their member after a failback. Packets
already carrying a mark — probes, Tailscale, wg-quick — are never pinned or rewritten. With
`connections: flush` the connections of a member leaving because it is down are deleted by tuple, so
clients reconnect at once; a sticky group must flush, and the monitor also flushes a down member's
pinned connections when it was not carrying traffic. Connections involving a protected address or the
requester are spared and counted.

## Boot and start

The group's objects are lines of the managed boot batches (`links.batch` for IPv4, `rules6.batch`
for IPv6) with the last decided member, and the unit loads `egress.nft`. On its first round the
monitor reads every object back (`egressReadback`). A complete restore is recorded as verified, with
whether this process's probes have proven the decided members yet; missing objects (a tunnel that
came up after the unit ran) are put back through a journaled change and recorded. Switches recorded
as `applying` by a process that died are settled from the spec the recovery journal left: applied,
or interrupted with the previous decision restored.

## Simulation and automation

Automation is off until a **simulation** of the group's exact configuration (its fingerprint:
family, policy, members, probes, thresholds, stickiness, connection and failback policy) has passed.
`SimulateEgress` (`egress_sim.go`) records the namespace names in `egress-sim.json`, then builds
disposable namespaces: one standing for this host with the group's own tables, rules and marks, one
per member standing for its gateway (which drops forwarded sources not its own), and one holding the
probe targets with TCP and DNS responders. It runs the real probes and the real hysteresis and
decision rules through phases derived from the thresholds — warmup, tolerable latency, total failure,
flapping, recovery through the stable and hold windows, a latency breach and an outage of every
member — with `tc netem` on each member's link, and with time standing still between samples so the
windows pass at the group's own interval. It checks that every member proves its path, latency under
the threshold moves nothing, failure fails over after `FailAfter` samples, a flapping member is not
failed back to, failback waits for stability (or for the operator), latency over the threshold fails
over, nothing proven keeps the route, no switch goes to an unproven member, traffic through the group
table follows each decision, and the decided members recover. Namespaces are removed whatever the
outcome; a stopped process's are removed at the next start. One simulation runs at a time; runs are
kept, ten per group, in `network_egress_simulations`.

Turning automation on needs an enabled group and a passed run of its current fingerprint, and records
which run authorised it. An automated switch takes exactly the manual path, with the protected
addresses as the operators it must not strand; a refusal (for example while another change awaits
confirmation) is recorded once per situation and retried on the next round.

## Routes

All under `/api/v1/network`. Reads are `read`; everything else needs `system.admin` and is audited.

| Route | Gate |
| --- | --- |
| `GET /egress`, `GET /egress/{id}/events`, `GET /egress/{id}/simulations`, `GET /egress/simulations/{sim}` | read |
| `POST /egress`, `PUT /egress/{id}`, `POST /egress/{id}/simulate`, `POST /egress/{id}/automation/off` | system.admin |
| `DELETE /egress/{id}`, `POST /egress/{id}/enable`, `/disable`, `/switch`, `/automation/on` | destructive |

Create, edit, removal, enable, disable and switch accept `X-JD-Network-Apply: pending`; simulation and
the automation switch change no route and do not. The schema is additive (`store/network_egress_schema.go`).

## Limits

- A simulation models members as gateways on veth links; it does not model a tunnel's encapsulation,
  a provider's upstream beyond loss and delay, or prove that two members are independent upstreams.
- This host has one provider uplink; the native acceptance runs both providers in namespaces.
- Probes measure the configured targets through each member, not the reachability of every
  destination, and a TCP probe needs a completed handshake.
- Attribution by local address cannot separate members that share one, and cannot see forwarded
  connections that are not translated.
- Hand-made dashboard rules at priorities below 19000 are consulted before a group's rules.
- Drift inspection does not yet list the group objects; the monitor's start check and the page's
  runtime reading compare them with the decision instead.

## Tests

`egress_test.go`, `egress_apply_test.go` (transcripts) and `handlers_network_egress_test.go` cover
validation, rendering, the journal plan, the decision rules, the simulation judgement, guarded
operations and the route contract. The native lane builds a namespace standing for this host, two
provider namespaces that drop foreign sources, an internet namespace and a connected operator; the
probes and connection tracking enter it in-process, so it runs as root:

```sh
go test -race -c -o netx.test ./internal/netx
sudo -n env JD_NETNS_LIVE=1 TMPDIR="$TMPDIR" ./netx.test -test.run '^TestLiveEgress' -test.v
```

It covers failover and failback with hysteresis, flap suppression, refusal of an operator path
through an unproven member, boot restore of the decided member with the start check and its
journaled repair, recovery by a fresh process after the switching process dies, sticky connections
surviving a failback and being flushed with their member, and a full simulation run. `bun test
src/lib/network-egress.test.js` covers the page's pure logic and `network-egress.spec.ts` the page.
