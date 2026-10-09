# The network module (`internal/netx`)

`netx` changes the host's network: its devices, routing, gateway table, protections, shaping, tunnels
and resolver. `netsec` keeps reading the network for the posture and keeps the firewall, sshd,
fail2ban, CrowdSec and Suricata; this package is the half that writes. The Network section of the
frontend (`/network/*`) is its only client. The plan it was built from, with the reasoning behind each
choice, is [`docs/audits/2026-10-07-network-section/`](../../audits/2026-10-07-network-section/README.md).
The [2026-10-08 audit](../../audits/2026-10-08-network-audit/README.md) inventories every Network
page and diagnostic, fixed findings, competitor research and actual compatibility boundaries.
The [capability-report implementation ledger](../../audits/2026-10-08-network-capability-report/implementation-status.md)
tracks the additional work and its acceptance evidence.

The section also offers [connection investigation](network-investigator.md), [saved diagnostic
runs](network-diagnostics.md), [bounded packet captures](network-captures.md) and
[owned drift inspection](network-drift.md). The investigator pins a
typed host/container tuple and distinguishes observed, modeled, measured and unknown layers. The
diagnostic service retains bounded quick-tool and typed investigation artifacts with lifecycle state in SQLite; host-network configuration
remains in the managed spec. Drift compares desired/rendered/runtime identities and measured unit
activation. A separate reviewed, generation/identity-bound transaction can repair selected owned
render files and required admission chains; wider resource repairs remain advisory. Nothing
automatically reconciles a native owner. File recovery binds exact staged/original identities; see
[file durability](network-file-durability.md).

## Three rules

- **One spec, restored by the host.** Every device, address, route, rule, namespace, shaping entry,
  gateway entry and kernel setting the dashboard makes is written into
  `/etc/just-dashboard/network/spec.json` (`spec.go`). It is rendered (pure functions, golden-tested)
  into `links.batch` (`ip -force -batch`), `rules6.batch` (`ip -6 -force -batch`),
  `shaping.batch` (`tc -force -batch`), `gateway.nft`
  (`nft -f`, declare–delete–define so loading is an atomic replace) and
  `/etc/sysctl.d/90-just-dashboard.conf`, and, once the owned firewall table has been used,
  `firewall.nft` (see [the firewall](observability-security.md#which-firewall-which-rules-and-who-can-still-get-in)).
  `just-dashboard-network.service`, a oneshot unit ordered after
  the network managers, Docker, ufw and firewalld, runs them at boot, so what was made survives a
  reboot whether or not the dashboard is running, and a reinstalled dashboard finds what the last one
  made. The spec is a file beside its renders rather than rows in SQLite because it describes the host.
- **The client path is guarded.** Every change is judged against how the kernel answers the address the
  request came from (`path.go`, `ip -j route get`): the device the reply leaves through, its gateway
  and its source. A route or rule is applied, the path is resolved again (`verifyPath`, and
  `verifyRouting`, which also asks `route get CLIENT from SOURCE` so a rule selecting on the server's own
  address is caught), and the change is taken back if the answer moved. Anchors are read with
  it and compared the same way — the route to 1.1.1.1, the same with Tailscale's packet mark 0x80000,
  and to 2606:4700:4700::1111 (only asked of the kernel, never contacted), plus up to sixteen tunnel
  endpoints read when the change starts: each WireGuard peer's endpoint with its interface's fwmark and
  each Tailscale peer's direct address with tailscaled's mark — because the operator's
  way in rides on this server's own way out: tailscaled's packets, a WireGuard endpoint, the SSH
  session behind a tunnel. The check asks exactly the questions it read before the change, so an
  endpoint that roams meanwhile cannot become a missing anchor. A routing change also re-asks the
  route to up to 32 established connections' peers and records those it moved in the journal's
  `validation` evidence beside the guards it passed; moved connections are evidence for the operator
  to judge before confirming, not a refusal, since a route is often added to move them. A request on loopback is followed to the SSH session carrying it
  (`OperatorAddress`, `operator.go`: an sshd process holding a loopback-to-loopback connection and
  one from elsewhere on a listening port), so an `ssh -L` browser is guarded as the address it
  really comes from. Guards answer `409 would_lock_you_out` with the sentence saying why.
  Path equality compares local/device/gateway and deliberately permits a changed source address;
  source-selected routing checks add coverage but do not prove application reachability.
- **Only what was made here is removed.** Docker's bridges and veths, Tailscale's device, table 52 and
  rules, the provider's DHCP routes, the kernel's own routes and its fallback tunnel devices (`gre0`,
  `ip6tnl0`, …) are read and named with their owner and excluded from destructive ownership operations.
  Guarded runtime edits remain possible; non-destructive up is allowed even on protected devices. A change to a device the dashboard
  did not make (state, MTU, bridge membership) is runtime-only and says so (`persisted: false`).

## Applying a change

`Service.commit` (`persist.go`) renders and validates the candidate, snapshots every changed boot
file and the spec before applying the runtime change, then verifies the operator path. It writes the
renders in a deterministic order and the spec last. A synchronous write/verification failure attempts to restore
previous file contents and modes and undo runtime with a fresh, bounded thirty-second context,
independent of a canceled request. Partial link, namespace, gateway, shaping and sysctl changes also
recover with independent contexts. Before runtime mutation, `change.json` durably records changed-file
snapshots and typed undo commands. Fetched list data is staged until that journal exists. A separate
host executable can recover a pending journal without the backend, API, database or credentials.
File writes fsync the file and its containing directory; the journal makes an interrupted sequence
recoverable rather than pretending several filesystem renames are one atomic transaction. Recovery
collects failed writes and commands, exposes them as degraded, and blocks further journaled changes.
Native-owned state/MTU/bridge edits retain observed undo without creating managed boot files.
Selected persistent NetworkManager/networkd/netplan profiles have a separate
[native profile adapter](network-native-managers.md): it stages through the actual owner, requires
temporary apply, and keeps its own closed recovery vocabulary in the same serialized journal.
Native profile reads/writes require administrator sessions; profile bytes and private checkpoint
identities never reach the browser. Structural bond/VRF edits remain part of the open P11 scope.
See [network recovery](network-recovery.md) for host prerequisites, phases and acceptance boundaries.
Interactive covered mutations support a ninety-second temporary apply and account/session/source-bound
reconnection confirmation. The host recovers an unconfirmed journal independently; ordinary API
callers remain immediate. Boot recovery restores files and prior owned link/namespace dependencies
before undo, as described in [boot recovery](network-boot-recovery.md). Confirmation proves a fresh
dashboard response rather than general service or tunnel health.

`s.mu` serialises mutations. The unit is enabled, not started: everything it would restore was just
applied. A later unit-enable failure is a `persistenceError`: runtime and spec have committed, so callers
retain matching blocklist cache and WireGuard exit state and report the boot-restoration failure.
Host-wrapper commands terminate as a process group before recovery, preventing forked children
from continuing a mutation after cancellation. Every host command uses `hostexec` with explicit argv; batch files are parsed `ip`, `tc` and `nft` data.

IPv6 policy rules use a separate `rules6.batch`, since `ip -batch` cannot carry `-6` within a line.
Rules accept optional `family` (`inet`/`inet6`, with IPv4/IPv6 aliases), inferred from address selectors;
interface/mark-only rules default to IPv4. Mixed selectors are refused. Add, delete, rollback and boot
restore all select the rule's family explicitly. IPv4-mapped prefixes retain their correct IPv4 width.

## Devices, namespaces and routing

Files: `links*.go`, `namespaces.go`, `routes.go`, `forwarding.go`, `bgp.go`.

- `ReadLinks` joins `ip -j -d -s link` and `ip -j addr` with the default routes (the uplink), the client
  path, the spec, each running container's veth (read from `/proc/<pid>/root/sys/class/net/*/iflink`,
  no subprocess, with PIDs cached per container start in the api layer) and the sampler's rates. Each
  device carries a kind, a role (uplink, tunnel, bridge, container, vlan, virtual, physical, loopback),
  an owner and, where it may not be changed, a guard sentence.
  An incomplete Docker join is flagged rather than hidden: `dockerJoin: "unresolved"` on a Docker veth
  whose container was not found (with `dockerJoinReason` naming a failed container list, containers
  whose PID or `/proc/<pid>/root/sys/class/net` could not be read, or none of those), and `"unknown"`
  on a Docker-named bridge while Docker's network list could not be read. Addresses carry the
  kernel's `origin` (static, dhcp, slaac, temporary, dynamic, link-local) and remaining lifetimes; a
  managed veth carries its peer and namespace, a managed unicast VXLAN its further flood ends.
- `GET /links/{name}/detail` reads what the list leaves out: the driver (`ethtool -i`, falling back to
  the sysfs driver link), the meaningful top-level `ethtool -k` offloads and every
  `ip -s -s -j link` error counter. Each part has its own `Reading` (ok, unavailable with the package,
  not_applicable or failed), so a missing ethtool never reads as "no offloads".
- `GET /links/{name}/readiness` (`readiness.go`) checks what this host alone can establish, sending
  nothing to the other end: for VXLAN/GRE the underlay device, the local end's ownership, the kernel
  route to every end (failed when it recurses through the tunnel), MTU headroom for the
  encapsulation, the VXLAN flood list in the kernel's FDB and received-traffic liveness; for macvlan the
  parent, the mode's host isolation and siblings, and a warning when the parent is the uplink or a
  virtual machine's NIC whose provider filters unassigned MACs; for a dummy its address, the routes
  into it and the services bound to its addresses. Limits (no encryption, unchecked firewall/provider
  policy, no probe) are returned beside the checks.
- Create covers bridge, VLAN, VXLAN, GRE/GRETAP/IP6GRE/IP6GRETAP, dummy, macvlan and veth (into a namespace the
  dashboard made). The uplink, the client-path device, loopback, Docker's and Tailscale's devices and
  any device holding an address cannot be set down, deleted or enslaved to a bridge, and neither can
  the parent or bridge such a device rides on; a passthru macvlan on a device that carries one is
  refused (it takes every frame the parent receives). An MTU under 1280 is refused on the client path
  and on any device with a global IPv6 address.
  The create form exposes VXLAN unicast or multicast destinations with an explicit sender for
  multicast, and decimal uint32 GRE keys plus TTL/IPv6 hop limits. GRE remains unencrypted and the
  form does not claim verified multicast underlay or remote endpoint reachability.
- Bridges (`bridges.go`) can be made VLAN-filtering (`vlan_filtering 1`) and with multicast snooping
  explicitly off. `GET /links/{name}/bridge` reads settings, each port's VLANs beside the managed
  desired list, and the bounded forwarding database (the bridge's own multicast entries left out).
  `PUT /links/{name}/vlans` replaces a managed port's (or the managed bridge's own, `self`) VLAN
  memberships on a managed VLAN-filtering bridge — native VLAN untagged plus tagged VLANs; empty
  returns to the kernel default VLAN 1. It is refused when the port or bridge carries the client path
  or the uplink, applied from the observed membership with a full undo, recorded in the spec and
  restored at boot by failure-tolerant `ExecStart=-bridge vlan …` lines the unit gains only when a
  spec has memberships. A port leaving its bridge, or a deleted bridge, drops its memberships.
  `PUT /links/{name}/remotes` replaces a managed unicast VXLAN's further flood ends (same family as its
  remote, never multicast) as `bridge fdb append 00:00:00:00:00:00 … dst` entries, likewise in the
  spec and the unit. Both are journaled: `recoveryPlan` adds `bridge` commands that restore the prior
  memberships and ends. Taking a VLAN or an end away is destructive (`requireDestructive` by content).
- `GET /links/{name}/master/preview?master=` previews a bridge membership change without changing
  anything: the same guard verdict `SetLinkMaster` would give and every address, route, managed
  dependent, VLAN-filtering and STP effect and the runtime-only persistence of a foreign device. The
  dashboard does not move addresses or routes across a migration; the preview names them.
- Routing reads every table in both families with `rt_tables` names (the local table is counted, not
  listed), the policy rules with their owners, and the client path. Added rules take priorities in
  10000–19999, checking live foreign priorities as well as the spec in that family; a rule with no
  selector, a second default route
  in main, and tables 52, 253 (for routes) and 255 are refused. A zero packet-mark mask is refused,
  and unmarked discard rules are guarded as potential dashboard-reply selectors.
- Rules also select by socket owner (`uidrange`), TOS (IPv4 0x04–0x1c, IPv6 any DS field without ECN
  bits; `rt_dsfield` names and the shipped standard names compare as their values) and VRF (`l3mdev`,
  which names no table), and may `goto` a later priority. A goto must land on a rule made here in the
  same family, may not jump over a rule made elsewhere, and a rule a goto lands on cannot be removed
  first. A UID-selecting discard is checked against the owners of the sockets actually answering the
  client (`ss -tne`); without that evidence it is guarded as possibly selecting the replies. A goto is
  not a discard and is verified after it applies. TOS 0 and the all-UID range select nothing in
  particular and are refused, so they cannot pass the no-selector guard. Inverted, protocol/port and
  `suppress_prefixlength` selectors are read and modelled but not written.
- Routes may be equal-cost multipath (2–16 legs of gateway, device and weight 1–256); ip reads every
  attribute before the first `nexthop`, so table, metric and source are written first. A managed route
  is edited through a reviewed plan (`POST /routing/routes/{id}/plan`, then `PUT`): `ip route replace`
  while family, table, destination and metric — the kernel's identity — are unchanged, otherwise the
  new route is added before the old one is removed; the plan is recomputed under the lock at apply. An
  in-place replace first reads the kernel's route at that identity and is guarded when it is no longer
  the managed one, rather than overwriting what drift put there.
- `route_model.go` evaluates the rules as `fib_rules_lookup` does (selectors, goto, throw and
  suppressed answers, longest match then lowest metric), forking on a selector it cannot decide for the
  packet and naming an answer only when both branches agree. It backs `POST /routing/routes/preview`
  and the edit plan's impact (addresses this host depends on: the client, anchors, tunnel endpoints,
  forward targets, NAT sources, allowlisted networks, Docker networks and established peers, modelled as
  packets this host sends with the source the first lookup chooses), and `POST /routing/rules/preview`,
  which also places the rule among the others, finds rules that shadow it or that it shadows, and
  answers a named packet with the kernel's present `ip route get` beside the model's prediction. The
  routing read's `clientDecision` is the kernel's table for `route get CLIENT from SOURCE` with the
  evaluated rule only where model and kernel agree on table and device.
- `StartRouteHistory` reads the same inventory every thirty seconds and records added, removed and
  changed routes and rules (local table excluded) in `network_route_events`, each bounded by the two
  readings it fell between; a restarted process diffs against the stored snapshot and marks those
  changes as found across a restart. Thirty days or 2000 events are kept; `GET /routing/history`
  filters by family, object and a covering target. Routes a routing daemon installs (BGP, OSPF, IS-IS,
  RIP, Babel, BIRD, zebra by name or FRR's protocol number) follow its sessions and are left out; a
  reading with more than 100 changes is recorded as one event; and past 20000 routes the observer stops
  with the reason in `lastError` rather than rereading a routing table every thirty seconds.
- Device/namespace removal checks shaping, NAT/forward ingress/egress, routes/rules, foreign children
  and veth-peer dependencies. A partial namespace failure restores already removed pairs.
- The namespace list reports a per-namespace `readError` instead of an empty device list.
  `GET /namespaces/{name}?kind=named|container` (`namespace_detail.go`) reads one namespace's devices,
  both families' routes (local/broadcast entries left out), the resolv.conf its processes read
  (`ip netns exec` for a named namespace, `/proc/<pid>/root/etc/resolv.conf` for a container) and
  its TCP/UDP listeners, each with its own `Reading`. `GET /namespaces/{name}/lookup?target=` asks
  the namespace's kernel `route get` for a literal address. A container is resolved from the Docker
  inventory by name; the client never supplies a PID.
- Forwarding is per family. Turning it off is refused while Docker networks, an enabled forward or NAT
  entry, a WireGuard exit or Tailscale's exit node or subnet routes need it. The API supplies separate
  Docker IPv4/IPv6 bridge counts: IPv6 uses its flag/subnet evidence; IPv4 conservatively counts all
  bridges because the Docker inventory lacks reliable IPv4-disable/custom-IPAM evidence. A failed
  Docker network listing blocks shutdown for both families until dependencies can be read.
  `TailscaleNeedsForwarding` also answers "needed" when Tailscale cannot be read. Each family carries
  `dockerBasis`, saying how the Docker count was reached, and measured `health`: the kernel's forwarded
  datagram counter (`Ip ForwDatagrams`, `Ip6OutForwDatagrams`), its rate between reads at least a
  second apart, and IPv4 devices whose own `forwarding` switch is off while the family's is on
  (`partial`). An IPv6 device's switch only chooses host or router behaviour, so IPv6 is never partial.
  A rate shows forwarding happens; it does not show a particular flow passed.
  Turning IPv6 forwarding on is refused while the IPv6 default route was
  learned from a router advertisement on a device whose `accept_ra` is not 2: with forwarding on, the
  kernel ignores those advertisements and the route would expire.
- IPv4 forwarding resets kernel host settings; managed redirect protections are reasserted after
  changes and rendered after forwarding in the boot sysctl file.
- BGP is read from FRR (`vtysh -c "show bgp summary json"`) where it runs; read-only. Each neighbour
  carries the route-map, prefix-list and filter-list names `show bgp neighbors json` reports, OSPFv2 and
  v3 adjacencies are read from `show ip ospf neighbor json` and `show ipv6 ospf6 neighbor json` (both
  releases' field names), and `GET /bgp/routes?family=&prefix=` lists a family's paths only while its
  neighbours sent at most 5000 prefixes, or one parsed prefix's paths in either FRR shape. Policy,
  areas and failover remain FRR's configuration.

## Overview

Files: `overview.go`, `identity.go`, `topology_flows*.go`, `incidents.go`; the api's
`handlers_network_overview.go` gathers the readings concurrently.

- `identity` is each family's way out from `ip route get` of the same anchors the guards use (only the
  kernel is asked): device, gateway, the NIC source and its scope, and the family's forwarding switch
  (an unreadable switch says so rather than "off"). `public` keeps the NIC source apart from the
  provider identity: `nic` (a public source; a provider firewall or 1:1 NAT is not visible), `translated`
  (a private/shared/unique-local source rewritten upstream to an address this host never observes),
  `unobserved`, `no_route` or `unknown`.
- `flows` reads the kernel's connection tracking in-process over ctnetlink (`NETLINK_NETFILTER`, a
  read-only dump in the backend's host network namespace, bounded to 65,536 entries; no subprocess)
  and places each entry's initiator and responder on a topology node by Docker subnet, managed or
  addressed device subnet, tailnet range, host address or the internet. Edges carry flow counts,
  protocols, translation (masquerade from the reply tuple, port forward from the rewritten
  destination), the translating device and an effective hop-by-hop path. Bytes appear only when
  `nf_conntrack_acct` is on. It is a moment's reading of this host, not a capture or switch discovery;
  `unavailable` and `failed` (e.g. missing `CAP_NET_ADMIN`) are reported as such.
- `observations` records whether each reading the attention list depends on arrived (`ok`, `failed`,
  or `unavailable` for a missing tool). A failed reading becomes an `observation.<source>` finding and
  the findings judged from it are skipped, so a failed firewall, error-history or forwarding read is
  never reported as an absent firewall, zero errors or forwarding off. `Sampler.RecentErrors` returns
  its database error for this.
- Every finding names its `source`. `RecordFindings` folds each Overview read into the additive
  `network_incidents` table: a new finding opens an incident (correlated with others opened within
  two minutes), a repeated one extends it, and an open incident resolves only when its own reading
  succeeded without it; a failed reading marks it `unobservedSince` and keeps it open. Resolved history
  is kept 30 days, at most 1,000 rows. `Server.Start` also judges the list every five minutes with no
  browser (`startNetworkIncidents`); between those reads and page reads nothing is observed.

## Traffic

Files: `sampler.go`, `traffic.go`, `ebpf.go`.

The sampler reads `/proc/net/dev` every two seconds into a fifteen-minute ring per device (the live
figures and wires) and records a row per device every metrics interval into `metric_interface_samples`
(the interval's mean, its busiest two seconds, and its errors and drops), pruned by the metrics
retention. Disabled retention does not accumulate pending samples; stopping the sampler is idempotent.
Docker's veths are not recorded; each container's traffic is in `metric_container_samples`,
which `/network/traffic/containers` differences per sample in SQL. Per-program traffic differences
`ss -tinpH`'s per-socket byte counters between reads (TCP only). eBPF is an inventory from `bpftool`
(programs, XDP and tc attachments). The separate administrator Socket History recorder can explicitly
attach the fixed bounded cgroup observer for TCP/UDP and short socket header evidence; see
[observer ownership, byte subtotals and quality](network-flow-observer.md). Reading traffic or history
does not attach it, and restart requires a new explicit opt-in.

## Gateway and protection

Files: `gateway*.go`, `blocklists.go`, `protection*.go`, `conntrack*.go`.

`inet jd_gateway` only ever drops or translates, and a chain exists only while it holds a rule (an
idle hooked chain still costs every packet a traversal). Chains: `pre` (prerouting at mangle priority,
after conntrack: established and related return, then loopback and the trusted sets, then each
blocklist set drops new connections, counted — so a list taking an address never cuts a session
already open, nor the replies to this server's own outbound connections), `input` and `forward` (−10:
established and trusted return, then the rate and connection limits; a limit in the forward chain
counts only translated flows, `ct status dnat`, and the forward chain also marks a NAT entry's new
connections), `nat_pre` (each port forward: `fib daddr type local`, so a connection passing through to
another host's same port is not captured, then `ct mark set` and `dnat`) and `nat_post` (masquerade or
SNAT per NAT entry, and per forward whose source NAT resolves to masquerade). Every rule carries a
comment its counters are read back by; the capability and counter reads use `nft -t -j`.
Packet-rate limits match new-flow packets and can be global or per source; concurrent-connection
ceilings are keyed by source address, and a separate optional ceiling for everyone together
(`globalConnections`, `ct count over N`, comment `limit-global:<id>`) refuses past a total. Established
traffic returns before these limits. Limits may start from a service profile (SSH, HTTPS, HTTP, DNS,
SMTP, WireGuard handshakes, an exposed database, a game server); the profile only fills the form and
is kept for display, and every figure is validated like any other. The Protection read reports each
limit's per-source meter occupancy (`meters`, elements of its dynamic sets against their 65 535 size)
and the ceiling's own refusals (`globalPackets`).

NAT entries have four modes. Masquerade and a fixed SNAT address may be scoped to destination networks
(`destinations`, up to sixteen; the admission mark is scoped the same way, so untranslated traffic is
not admitted). `one-to-one` maps a private address or network of at most 256 addresses to a public one
of the same width in both directions; `nptv6` maps an IPv6 network between /16 and /64 to a public one
of the same length. A mapping renders `snat … to` / `snat … prefix to` in `nat_post` and a marked
`dnat` for the public side in `nat_pre` (comment `nat-in:<id>`), after every forward, so a forward's
port takes precedence. nft's prefix NAT is a stateful netmap: connections are tracked and checksums
recalculated, not RFC 6296's checksum-neutral translation. A single public address must be one of the
device's own; a routed prefix cannot be checked. A mapping whose public side contains the address the
requester's own replies leave from is refused with `would_lock_you_out`. Overlapping enabled entries on
one device (not only equal sources) and two mappings of one public side are refused.

**A port forward admits its own traffic.** One table's `accept` cannot override another's `drop`, and
ufw and Docker drop forwarded traffic by default, so translated connections carry the mark
`0x4a000000/0xff000000` (outside Tailscale's packet-mark bits) and one rule per chain admits exactly
them: `-m connmark --mark … -j ACCEPT` at the top of iptables' and ip6tables' `FORWARD`, `INPUT` and
`DOCKER-USER`. They are re-asserted on every gateway change (a `ufw reload` or a Docker restart can
remove them), restored at boot by the unit (delete then insert, so idempotent), and removed when
nothing translates (each `-D` repeats until the kernel has none left, at most eight times). Writes are refused (`409 network_read_only`) where firewalld is active or another
nftables table drops forwarded traffic, naming the chain and the accept to add there. A forward or NAT
entry that needs forwarding while it is off is refused with `409 forwarding_off`; the page offers the
switch.
Health checks inspect every required family and chain and distinguish absent, unsupported and
unreadable rules. The gateway page shows checked policy layers and offers an admin/destructive-gated
repair of owned admission rules. Writable policy and owned-rule presence are separate from measured
reachability; foreign expressions and provider policy can remain unknown. A refused generic check
is re-judged per translated flow before a translation is saved; ordinary Docker shapes pass and a real
drop names its rule. Entries report installed readiness, totals across table replacements, a
re-checked auto source-translation decision, an optional target check from this server and, for
administrators, external evidence of the public port. The full response, the flow model and the
fail-closed cache contract are in [gateway health](gateway-health.md).

`POST /gateway/preview` runs a forward's or NAT entry's save checks without the lock, journal or any
host change and returns `impacts` (refused collisions and guards, overlapping entries whose rule would
win, local listeners a forward or mapping would take, limits and blocklists that also judge it, the
first admission insertion of a family, the auto decision, forwarding off), the connections an edit or
disable leaves on the old translation (read over ctnetlink by mark, translation and reply source), and
the candidate's modeled flows. The editors call it as the form changes.

Cache and kernel rollback retain the prior list under the mutation lock; fetches begun before a
URL/country edit cannot overwrite newer configuration. Failed boot-unit setup retains the committed
cache, matching the committed spec and kernel.

Fetched lists keep a schedule (`refresh`: 6h, 12h, 24h — the default — 72h, 168h or manual). The
refresher checks every fifteen minutes; after a failure it retries after fifteen minutes doubling per
failure, never longer than the schedule, and counts `failures` with `lastAttempt`. Each successful
fetch records provenance per URL (`sources`: URL, country and family for a zone, status, bytes,
SHA-256, networks kept and lines skipped, validators, signature) and `lastDiff` (added/removed counts
and up to eight of each against the previous readable cache). A feed with a healthy cache sends its
`ETag`/`Last-Modified`; a 304 records the refresh without touching the cache or the kernel. A custom
feed may pin a detached Ed25519 signature (`signatureUrl` over https and a 32-byte base64 `publicKey`);
every fetch is used only when the signature over the exact body verifies, and an edit naming the same
signature keeps the pinned key. The presets publish no signature, so they cannot be marked signed. The
view adds `nextRefresh`, `stale` (older than twice the schedule), `integrity` (signed, https, local)
and `coverage` (IPv4 addresses and share of the space, IPv6 in /48s). Country lists are registry
allocations, not physical location; the editor says so with the collateral it can cause.

`POST /protection/preview` builds a list from its proposed body (fetching a country or feed list within
the usual bounds, saving and loading nothing) and returns its size and coverage, the diff against an
existing list, this host's own networks and NAT sources it covers, the trusted networks and exceptions
that keep passing, the connections open now from inside it (they continue: a list refuses new
connections only) and, for a country list, what its geography is.

Exceptions (`exceptions`: address, scope `all` or `blocklist:<id>`, reason, optional expiry, made-by)
let a network past the drops. An `all` exception and an expiring kept trusted address are rules of
their own after the trusted sets in `pre`, `input` and `forward`; a list with exceptions jumps to a
chain of its own (`bx_<id>`) whose returns go back to the next list. An expiry is an absolute
`meta time < <unix seconds>` match in the rule, so the kernel stops honouring it at that instant
whether or not the dashboard runs and the boot file stays a pure function of the spec; a half-minute
sweeper removes expired entries from the spec afterwards (it waits while a change awaits
confirmation). Exceptions are bounded to 256 and to /8 (IPv4) or /16 (IPv6); creating and removing one
are both destructive, and removing the one that lets the requester through a list holding them is
refused. Kept trusted addresses carry notes (`trustedNotes`: reason, made-by, optional expiry, last
confirmation); an expiring kept address leaves the permanent trusted set and no longer counts as
trusted for the lockout guards. For administrators, each kept address reports its last dashboard
session or successful sign-in from inside it (90 days) and is `stale` when neither that nor a
confirmation is within thirty days; readers without `system.admin` do not receive that sign-in
evidence, since the audit log and sessions are administrators' reading; `PUT /protection/trusted` records a reason, an expiry (destructive, refused on the requester's
only coverage) or a review.

A list never cuts an open session. `POST /protection/sessions/preview` counts the tracked connections
from a network inside an enabled, loaded list, and `POST /protection/sessions/revoke` (destructive,
audited, not journaled and not undoable) deletes exactly those entries by tuple, zone and id over
ctnetlink: their next packets meet the list as new connections and are dropped. It refuses a network
outside the list, a list that is off or not loaded, one wider than /8 (/16), and any network containing
the requester, a trusted network or this host's own address.
FireHOL level 1 or any https URL — http, and a redirect down to it, is refused, since anyone on the way
could answer with a list of your own networks); fetches are bounded (30 s, 16 MB), parsed with `netip`,
stripped of private and reserved ranges (FireHOL level 1 holds them, and dropping them would cut off
every container and tailnet peer) and of multicast and the reserved blocks, merged, cached to
`lists/<id>.txt` and refreshed daily by `StartBlocklistRefresh`, which `Server.Start` runs. A feed's
entries shorter than /8 (v4) or /19 (v6) are dropped and a feed covering more than 2^28 IPv4 addresses
is refused: that is a region, not a blocklist. Parsed lists are memoised by path, modification time,
size, mode, change time and inode, so replacing the file or changing its permissions invalidates the
reading without reparsing an unchanged file on each poll. Every drop is preceded by the `trusted` sets:
loopback, the allowlist ranges narrower than /8 (v4) and /16 (v6), and the addresses the spec keeps
(the requester's, added on their first protection entry). A manual entry holding the requester's
address is refused; a fetched list that holds it is reported (`containsYou`).

Kernel protections are a closed list of fifteen sysctls (SYN cookies, loose reverse-path filtering —
strict is not offered because it breaks policy routing and tunnels — redirects, source routing,
ICMP, SYN backlog and retries, RFC 1337, martians, the conntrack maximum), each with its recommendation
and why, written to the sysctl drop-in. The read adds `interfaces`: each device's effective value for
the five per-interface settings, combined as the kernel does (reverse-path filter: the higher of all
and the device; IPv4 accept_redirects: both when forwarding, either when not; send_redirects and
log_martians: either; IPv4 source routing: both; IPv6 redirects: the device's own; IPv6 source routing:
the lower), bounded to 96 devices with container veths last. `kernelProfiles` (internet-facing server,
VPN or container gateway, busy web server, routing investigation) stage values the closed list allows;
they are applied through the same confirmation and weakening gate. Conntrack's count against its
maximum is read from `/proc`; its history, breakdown and pressure indications are described in
[gateway health](gateway-health.md#protection-evidence).

## Shaping

Files: `shaping.go`.

Per device a root discipline (fq_codel, cake, fq) and upload and download limits (CAKE bandwidth or
HTB with fq/fq_codel egress, ingress policing), and BBR as a switch (`tcp_congestion_control=bbr`, `default_qdisc=fq`).
A limit under 1 Mbit/s on the uplink or the client-path device is refused. The ingress queue is only
ever replaced or, at runtime, deleted when it is the plain one; a `clsact` queue (tc-BPF programs) is
never touched, and a download limit on a device that has one is refused.
Apply verification reads the exact HTB class/default/leaf and rate/ceil, CAKE bandwidth, and ingress
matchall/drop policer rate/burst. Detailed policer output supplements iproute2 JSON where its fields
are absent, with a small allowance for kernel clock quantization. First replacement refuses foreign
hierarchies/filters unless a supported classless fq_codel baseline can be captured and restored.
Managed-device reads report `verification` as verified, observed drift or unreadable/unknown; saved
limits remain desired values when external commands change the kernel. This verifies configured
objects, not bandwidth or latency under load. Existing download limits remain policing; the
explicit [download SQM](network-sqm.md) profile redirects ingress to a provenance-bound IFB/CAKE
queue, preserves native clsact egress and always requires independent pending recovery plus
positive reconnection confirmation. Its nonignored packaged boot restore runs after ordinary
resources. Measured fixture latency is separate from a general production performance claim.

## VPN

Files: `wireguard.go`, `wgconf.go`, `wgkeys.go`, `wgserver.go`, `wgpeers.go`, `wgdual.go`, `wgrecord.go`, `wgsite.go`, `wgendpoint.go`, `wgarchive.go`, `wgkillswitch.go`, `qr.go`, `vpn_store.go`, `tailscale.go`, `headscale.go`.

- **WireGuard** is read from `wg show all dump` joined with `/etc/wireguard/*.conf`; inventories omit private and
  preshared keys. Generated client secrets leave through explicit admin-only config/QR exports and
  are sealed at rest. Only files whose first line is `# Managed by Just Dashboard`
  are edited; a hand-written tunnel is read-only. A one-step server picks the first free name, UDP port
  and /24, makes keys in Go (`x/crypto/curve25519`), writes the wg-quick file with no `PostUp` (NAT is
  the gateway's job) and enables `wg-quick@<name>`; the handler opens the UDP port in the firewall when
  its inbound default would refuse it. A peer (a device, or a site with its networks) gets the next
  address, keys and a preshared key, is written into the file and synced live with
  `systemctl reload wg-quick@<name>` (a site's routes are added with `ip route replace`, under the
  client-path check). The client's configuration is returned with a QR code (`boombuler/barcode`,
  drawn at whole pixels per module with a quiet zone) and kept sealed (`auth.Sealer`) in
  `network_vpn_clients` so an administrator can show it again or forget it. An exit node is a NAT entry
  owned by the tunnel (`Owner: "wireguard:<name>"`), applied through the gateway like any other. A
  removed tunnel's file is moved to `.just-dashboard-removed/`, never deleted: the server key exists only
  there; the firewall rule its creation opened is removed with it, and `DELETE` answers
  `{firewall: {removed, reason}}` so the page can say when it was not.
  Scoped IPv6 endpoints validate the interface suffix before generating any wg-quick text. Peer
  site routes cannot capture a known literal endpoint of their own or another tunnel. Peer
  removal cleans site host routes too; failed reloads use independent recovery contexts. Managed
  servers can explicitly opt in to unique-local IPv6 /64 addressing and /128 peer allocation, with
  IPv4 preserved. IPv6 internet egress is a separate explicit owned NAT66 intent, with native
  per-family route/forwarding/admission evidence. Legacy IPv4 full tunnels keep IPv6 capture/block
  containment; this is neither IPv6 egress nor a general client kill switch. Existing native peer
  drift prevents unsafe opt-in edits. See [dual-stack WireGuard](wireguard-dual-stack.md).
  A five-minute record keeps per-peer trends, daily usage, endpoint history and a lifecycle with
  recovery outcomes; stale always-on handshakes, transports routed into a tunnel and passed usage
  budgets are alerts. Peers can be edited (a device's own routes regenerated from its sealed copy
  with the same keys), given alert-only budgets and, for sites, verified end to end. Removed
  tunnels are restorable from their archive. WireGuard endpoints the kernel reports are client-path
  anchors, so a guarded change that would route one into a tunnel is put back. Creation reports
  endpoint evidence and steers automatic allocations around held IPAM reservations; each exit
  reports its translated connections; full-tunnel devices have an opt-in Linux kill-switch
  export. See [the WireGuard record and lifecycle](wireguard-lifecycle.md).
- **Tailscale** is read from `tailscale status --json` and `debug prefs`. The only changes offered are
  what this server offers the tailnet — an exit node and subnet routes — through `tailscale set`; using
  another node as an exit, shields up, down and logout are never run, because each can cut off the
  browser reading the page. Offer updates serialize under the module lock, and unreadable preferences
  and status block forwarding shutdown. `SetTailscaleChecked` evaluates withdrawal authorization
  against those same locked preferences before writing; the separate classifier is advisory only.
  External CLI writers are outside this process lock. **Headscale** is read where its binary or container runs.
  `prefsReadable` explicitly distinguishes failed preference reads from empty advertised routes; the
  editor retains a rejected subnet draft and retries against refreshed authoritative preferences.
  `approval` says which advertised offers the tailnet serves from this node's own status, and the
  node's key expiry warns before it takes the server off the tailnet. Headscale reads snake_case
  and camelCase output, node expiry and advertised/approved routes (node-level or the older
  `routes list`), including a Headscale in a container.

## DNS

Files: `dns.go`, `dns_owner.go`, `dns_verify.go`, `dns_hosts.go`, `dns_hosts_evidence.go`,
`dns_lookup.go`, `dns_private.go`, `dns_wire.go`, `dns_tls.go`, `dns_dnssec.go`, `dns_evidence*.go`,
`dns_alias.go`.

The resolver chain includes `/etc/resolv.conf` (ordinary whitespace, including tabs), global/per-link
systemd-resolved scopes/statistics, port-53 listeners and existing AdGuard Home, Pi-hole and
Technitium services. The view and the owner adapter read the host's file through `dnsHostRoot`
(`/host` in the dashboard's container), following symbolic links component by component inside that
root, so an absolute target such as `/run/NetworkManager/resolv.conf` or a `/var/run` link is read on
the host's side and Docker's overlaid copy is never mistaken for the host's.

### Resolver owner

`DNSView.owner` (`dns_owner.go`) names who writes `/etc/resolv.conf` and what programs ask, instead
of treating systemd-resolved as universal. It classifies systemd-resolved (stub or uplink links, or a
plain copy of its file), NetworkManager (link to `/run/NetworkManager/…`, its header, its dnsmasq
plugin), Debian resolvconf, openresolv, SUSE netconfig, dhcpcd, dhclient, Tailscale, WSL, a loopback
cache answering on port 53, a plain file, a missing file and unrecognised links. Evidence comes from
the link target, the file's first signed header line, `[main] dns=`/`rc-manager=` merged from
`NetworkManager.conf` and the `/usr/lib`, `/run` and `/etc` `conf.d` snippets (an `/etc` snippet shadows
one of the same name, then name order), `/etc/resolvconf/resolv.conf.d`, `/etc/resolvconf.conf`,
`NETCONFIG_DNS_POLICY`, the port-53 listeners and the running services; `systemctl is-active
NetworkManager` is asked only on a host that shows signs of NetworkManager. `chain` is stub, uplink,
local-cache, direct or missing; `confidence` is confirmed (file and owner's own service or
configuration agree), declared (only the file says so), inferred or unknown; `dashboardWrites` says
whether the resolved drop-in reaches what programs ask. Disagreements — resolved running while
NetworkManager writes the file, `dns=systemd-resolved` without the stub link, a stale header with
`rc-manager=unmanaged`, the uplink mode's loss of routing domains/DoT/DNSSEC, Tailscale's rewrites —
are listed as `conflicts`. `handoff` says where DNS is changed for that owner, with a dashboard route
where one exists (`#dns-links` for resolved's per-link scopes, `/network/interfaces` for a native
profile). A refused change on a host without resolved and the post-apply warning name the owner and
its handoff. The adapter only reads; it never edits an owner's configuration.

### Upstreams, clearing and the verification plan

Upstreams support custom ports, scoped IPv6 and TLS names. Nonempty upstream/fallback/domain lists
reset preceding list assignments, then replace them in
`/etc/systemd/resolved.conf.d/90-just-dashboard.conf`. An empty list omits the key and inherits the
host's list. `clear` (any of `servers`, `fallback`, `domains`) instead writes only the empty
assignment: it removes what `resolved.conf` and earlier drop-ins set, and an empty `FallbackDNS=` turns
off resolved's compiled-in fallback. A list cannot be both given values and cleared; `ManagedDNS.cleared`
reports cleared lists on read-back. Each set rewrites the managed drop-in: omitted managed
DNSSEC/DoT/cache settings inherit defaults, while unrelated host/per-link files are untouched.

`POST /dns/verification-plan` returns, without changing anything, the `DNSVerification` a change will
be held to; the UI shows it in the confirmation. Checks are: `readback` (required: `resolvectl status`
must report the servers, fallback, domains, DNSSEC and DoT written, compared in the drop-in's server
normalisation — a later drop-in or `resolved.conf` that overrides them fails the change);
`resolution` for each of up to eight `verificationNames` (or the older single `verificationName`, or
`cloudflare.com`/`example.com` when none is given), each labelled with the scope resolved routes it
to — `~corp.example on wg0`, `default route via global upstreams` — computed from the request's global
scope and the links as they are; `transport` when DoT is `yes` (required) or `opportunistic`
(reported); and `dnssec` when DNSSEC is `yes` or `allow-downgrade` (reported). `unverified` names what
no check reaches: a routing domain the request sets with no verification name under it, the default
route when every name is link-routed, fallback servers, links inheriting the global DNSSEC/DoT setting
and the unreported cache mode.

The module restarts resolved and runs the plan. Resolution accepts A or AAAA within a short per-query
budget; with a disabled port-53 stub it verifies via `resolvectl query`. Transport and DNSSEC read the
first answer back through the [native policy adapter](network-dns-evidence.md) with fresh-network
flags: required DoT passes only on native-reported confidential transport (with strict TLS identity
policy reported as such) and an unencrypted fresh answer fails it; opportunistic DoT reports a fallback
to classic DNS as a warning; a native DNSSEC failure fails the change, an unauthenticated answer is a
warning. Where the native adapter is unavailable (a foreign chain, resolved older than 256) those two
checks are `unknown`, never passed. Any failed required check restores the old file and restarts with
an independent thirty-second context; the 409 refusal carries the complete `verification` beside its
error and failed recovery is returned to the caller. Unreadable old drop-ins refuse writes before
mutation. Reset removes the managed drop-in, restores it if restart fails, and reports its default-route
check without rolling back.

### Per-link split DNS

Global settings stay in the drop-in; a link's servers and domains belong to its network manager. The
DNS page lists every link resolved holds DNS for (servers, search and `~routing` domains, default-route
state, DoT and DNSSEC) and, for an administrator, edits a link's split DNS through the existing
[native profile adapter](network-native-managers.md): it reads `GET /network/native/profiles/{device}`,
shows the owner and any refusal, and writes `PUT /network/native/profiles/{device}` with the complete
current intent where only the DNS servers (split by family), the shared link-level routing and search
domains (the same list on every enabled family, as networkd requires) and the ignore-automatic-DNS
choice change. The write is always a temporary apply with the account/session-bound reconnection
confirmation and independent watchdog recovery; the native adapter's generation, ownership, runtime
agreement and recovery rules apply unchanged. The page states resolved's automatic default-route rule
for the draft (routing-only domains without `~.` stop a link answering unclaimed names) without claiming
a manager that sets the default route itself follows it.

### Host records

Host records occupy a marked `/etc/hosts` block. In Docker, `/host/etc/hosts` reaches the real host;
bytes outside the block, symlinks and permissions are preserved. Malformed ownership markers refuse
writes. `POST /dns/hosts/preview` validates records as a save would and, changing nothing, returns the
block it would write, added/removed records and overlaps: `duplicate` (same name and address twice in
the block), `conflict` (one name, two addresses of a family in the block), `shadowed` (a line before the
block gives the name another address of the family — returned first), `overrides` (a later foreign line
disagrees) and `repeated` (a foreign line already says the same). The UI previews before saving and
stops on a conflict or shadowing until the reader saves again. `GET /dns/hosts/resolution` asks the
host's NSS (`getent ahostsv4`/`ahostsv6` through `hostexec` in the host namespace, explicit argv, at most
sixteen names, three seconds each) what each managed name resolves to and reports `matches`,
`includes` (another address first), `differs`, `unresolved` or `unknown`, with the `hosts:` line of
`nsswitch.conf`. A name missing from the files source goes on to DNS like any program's lookup. This is
not a DNS server or an AdGuard/Pi-hole configuration adapter.

The separate [native DNS services module](network-dns-services.md) provides sealed connections to
AdGuard Home, Pi-hole FTL and Technitium, private configured-policy/query inventory, read-only DHCP
inventory, retained reviewed native changes and bounded owned Docker provisions. These default to
dashboard read-only and do not redirect the host resolver or treat local overrides as authoritative
zones. `GET /dns/services/handoffs` joins the servers this page detects to those connections.

### Lookups and the private-name guard

`POST /dns/lookup` defaults to `mode: "effective"`. When the actual host chain delegates to a supported
systemd-resolved stub, it uses the [native policy adapter](network-dns-evidence.md), including explicit
CNAME checks before each target question. Native stub ownership, version and complete split policy
must be readable; failure never tries another resolver. Merely running resolved does not establish
the host chain's ownership. The chain and comparison inventory are read in the host namespace,
avoiding Docker's overlaid resolver file; unreadable/malformed host chains stop effective lookup.
Other resolv.conf chains use their configured servers sequentially. The response includes
routing-domain evidence, named comparison targets, omissions and limits. Both paths exclude hosts/NSS
and search expansion; the native path additionally excludes DNAME and local/cache answers.

The effective test never hands a private name to a public resolver (`dns_private.go`). A private name
is one under the chain's search domains or resolved's link routing/search domains, a single-label
name, a special-use or commonly private suffix (`local`, `localhost`, `localdomain`, `home.arpa`,
`internal`, `intranet`, `lan`, `home`, `corp`, `private`, `test`, `invalid`, `onion`, `alt`) or the
reverse name of an RFC 1918, unique-local, link-local, loopback or shared CGNAT address. On the native
path, such a name that no link claims would go to resolved's default route; when a server there is
public the lookup is refused (`409 dns_private_name_public_upstream`) before any question. On a
foreign chain the first public server refuses it the same way; when every server is private its own
forwarding is unseen, so the lookup needs `acknowledgeForwarding: true` (`409
dns_private_name_unknown_forwarding` otherwise) and then goes only to the configured resolver.

`mode: "compare"` requires explicit configured/preset `destinations` and
`acknowledgeDisclosure: true`; the UI names those destinations and explains that private names leave
their normal policy scope. The old `includePublic` fan-out flag no longer authorizes comparison.
Direct classic DNS supports A/AAAA/CNAME/MX/TXT/NS/PTR/SRV, retaining custom ports and IPv6 scope,
validating response identity/question/answer ownership/canonical chains and retrying truncated UDP
over TCP. `transport: "tls"` asks each selected destination over DNS over TLS to its published or
configured identity (presets carry theirs; a configured server its `#name`), verifying the certificate
against the host's trust store; a selected destination without an identity is listed in
`omittedTargets` with the reason and not asked. `dnssec: true` sets the EDNS DO bit and reports each
destination's AD claim and returned RRSIG count; neither is validation by the dashboard. Fan-out is
bounded at sixteen and each query at three seconds. CNAME queries return the immediate alias; other
types use terminal data in that response without a follow-up query. Transport, AD and TLS version are
per-answer fields; effective lookups leave them empty because resolved chooses its own transport.

### Certificates and the DNSSEC chain

`POST /dns/tls-check` (`read`) opens the dashboard's own TLS session to each configured DNS-over-TLS
server (global, fallback, per-link and the drop-in's) on its DoT port, or to named presets, verifies the
certificate for the configured name against the host's trust store and asks the root's NS records —
which carry nothing private — over the session. Each check is `trusted`, `untrusted` (hostname,
authority or validity failure), `no-answer` or `unreachable`, with TLS version, subject, issuer,
names, expiry, SHA-256 fingerprint and verified chain length. Unnamed servers are listed as omitted:
opportunistic TLS cannot authenticate them. Only inventory servers can be selected, at most sixteen.
`POST /dns/dnssec-chain` (`system.admin`) walks a name's chain of trust through the native adapter, as
[documented with the evidence adapter](network-dns-evidence.md#dnssec-chain-of-trust). Neither lookup
mode nor the certificate check claims verified resolver transport for a particular answer; retained
investigations separately label native-reported encryption, strict TLS policy and DNSSEC.

## Diagnostics and host support

`GET /capabilities` reports host tool availability/package hints, systemd manager reachability, the
literal IPv6 sysctl with its per-interface caveat and read-only capability probes (`probes`): an
AF_PACKET open-and-close for CAP_NET_RAW, `nft list tables`, the iptables backend, per-interface
IPv6 state, forwarding sysctls, WireGuard/CAKE module presence (sysfs, then `modinfo`), conntrack
counters, a mounted bpffs and an active systemd-resolved. A probe whose binary is missing is
`unknown`, not `unsupported`; no probe writes host state. An installed binary still does not prove
NIC or provider support. `POST /probe` has [all 26 tools](../../audits/2026-10-08-network-audit/README.md#tools--networktools-all-26-server-diagnostics).
New tools are route lookup, path MTU, host support, bounded packet snapshot and Wake-on-LAN.

Every tool returns structured evidence beside its verbatim output — a verdict that does not
over-claim, labelled facts with their basis, ordered stages, tables, owner-specific findings, links
and comparable metrics. [Retained diagnostics](network-diagnostics.md#structured-evidence) documents
the shape, each tool's readings and the joins the dispatcher adds (route and port-check path layers,
saved SSH trust, the proxy site behind a site audit).

Route lookup emits no traffic; with a port it also joins the host-source connection-path layers
(policy rules, modeled firewall, NAT candidates, egress owner) without measuring. Egress independently
inspects IPv4/IPv6 kernel routes, including IPv6-only hosts, classifies each selected source's scope
and never claims the selected NIC address equals a provider-NAT public address.
Listeners includes TCP and UDP. Tracepath availability and ICMP filtering constrain path-MTU results.
TCP refusal can indicate a closed port or firewall rejection; a timeout does not identify its cause.

Packet snapshot uses fixed, validated tcpdump argv on one up interface: at most fifty packets or
fifteen seconds, no promiscuous mode, output file or explicit hex/ASCII dump. Summary decoders can
include sensitive protocol fields. Process output is drained while retained text is capped at 256 KiB. Deadlines terminate the entire
host-wrapper process group with bounded cleanup, including descendants holding output pipes.
Wake-on-LAN sends one Ethernet magic frame to a validated unicast MAC on a selected broadcast LAN
interface. Success confirms the send, not that a remote firmware/NIC woke; an optional literal
verification address (TCP port, or ICMP when none) is checked once before sending and then every
three seconds for up to sixty, and silence is reported as unknown rather than asleep; a device that
already answered before sending is not counted as woken. A quick
snapshot links to a retained capture job prefilled with its interface and protocol. Neither tool crosses an
upstream router/provider restriction. All probes remain admin-only and audited.

## Routes

All under `/api/v1/network` (`handlers_network*.go`). Reads are `read`, except `/vpn/*` and
`/traffic/processes`, which name who connects and are `system.admin`. Every mutation is `system.admin`;
removals, setting a device down, turning forwarding off, disabling a forward, NAT entry, limit or
blocklist, weakening a kernel protection, setting a trusted address to expire, making or removing an
exception, ending sessions, turning a WireGuard exit off, withdrawing a site's network or changing
where it is dialled, clearing a peer's usage budget, withdrawing what this server offers the tailnet
and changing the resolver are inside `s.destructive` (by path, or by content in the handler for the
PUTs, PATCHes and posts). No route takes a typed phrase.

| Area | Routes |
| --- | --- |
| Overview | `GET /`, `GET /capabilities`, `GET /overview` (identity, flows, observations, incidents), `GET /links`, `GET /traffic/live`, `GET /traffic/history` |
| Devices | `POST /links`, `DELETE /links/{name}`, `POST /links/{name}/up`, `/down`, `/mtu`, `/master`, `/addresses`, `DELETE /links/{name}/addresses?cidr=`, `PUT /links/{name}/vlans`, `PUT /links/{name}/remotes` (destructive by content when removing); reads `GET /links/{name}/detail`, `/bridge`, `/readiness`, `/master/preview?master=`; `GET`/`POST /namespaces`, `GET /namespaces/{name}?kind=`, `GET /namespaces/{name}/lookup?kind=&target=`, `DELETE /namespaces/{name}` |
| Changes | `GET /changes/current`, `POST /changes/{id}/verify`, `/confirm` (admin session), `/recover` (also destructive) |
| Routing | `GET /routing`, `GET /routing/lookup?target=<literal>&source=<optional literal>&mark=<optional value>`, `GET /routing/history?family=&object=&target=`, `POST /routing/routes/preview`, `POST /routing/routes/{id}/plan`, `POST /routing/rules/preview` (admin, change nothing), `POST /routing/routes`, `PUT /routing/routes/{id}` (destructive), `DELETE /routing/routes/{id}`, `POST /routing/rules`, `DELETE /routing/rules/{id}`, `POST /forwarding/{ipv4,ipv6}/{on,off}`, `GET /bgp`, `GET /bgp/routes?family=&prefix=` |
| Gateway | `GET /gateway`, `POST /gateway/admission/repair` (destructive), `POST /gateway/forwards`, `PUT`/`DELETE /gateway/forwards/{id}`, `POST /gateway/nat`, `PUT`/`DELETE /gateway/nat/{id}`, `POST /gateway/preview`, `POST /gateway/verify` (admin; change nothing on the host) |
| Protection | `GET /protection`, `GET /protection/pressure` (admin), `POST /protection/limits`, `PUT`/`DELETE /protection/limits/{id}`, `POST /protection/blocklists`, `PUT`/`DELETE /protection/blocklists/{id}`, `POST /protection/blocklists/{id}/refresh`, `POST /protection/preview` (admin), `POST /protection/settings`, `DELETE /protection/settings/{key}`, `PUT /protection/trusted` (destructive with an expiry), `DELETE /protection/trusted?address=`, `POST /protection/exceptions`, `DELETE /protection/exceptions/{id}` (both destructive), `POST /protection/sessions/preview` (admin), `POST /protection/sessions/revoke` (destructive) |
| Shaping | `GET /shaping`, `POST`/`DELETE /shaping/{device}`, `POST /shaping/bbr` |
| VPN | `GET /vpn`, `POST /vpn/wireguard`, `DELETE /vpn/wireguard/{iface}`, `POST /vpn/wireguard/{iface}/up`, `/down`, `/exit`, `/peers`, `GET /vpn/wireguard/{iface}/history`, `/endpoint`, `PATCH`/`DELETE /vpn/wireguard/{iface}/peers/{id}`, `GET`/`DELETE /vpn/wireguard/{iface}/peers/{id}/config` (`?variant=linux-killswitch`), `PUT`/`DELETE /vpn/wireguard/{iface}/peers/{id}/quota`, `POST /vpn/wireguard/{iface}/peers/{id}/verify`, `GET /vpn/archive`, `POST /vpn/archive/{file}/restore`, `POST /vpn/tailscale` |
| DNS | `GET`/`POST`/`DELETE /dns`, `GET`/`PUT /dns/hosts`, `POST /dns/lookup` and `POST /dns/tls-check` (read), `GET /dns/hosts/resolution` (read), `POST /dns/verification-plan`, `POST /dns/hosts/preview` and `POST /dns/dnssec-chain` (admin, unaudited reads) |
| Private DNS evidence | `GET`/`POST /dns/evidence/`, `GET`/`DELETE /dns/evidence/{id}`, `GET /dns/evidence/{id}/export` (admin; deletion destructive) |
| Native DNS services | `/dns/services/` connections, `/handoffs` detected-server joins, `/{id}/dhcp` read-only DHCP, `/{id}/zones/{zone}/records` authority inventory, `/{id}/changes` review, `/changes/{id}/current` exact current selection and `/changes/{id}/apply`; `/provisions` review and `/provisions/{id}/apply`/removal (admin, private; apply/removal destructive) |
| Traffic | `GET /traffic/processes`, `GET /traffic/containers`, `GET /ebpf` |
| Diagnostics | `POST /probe` (26 tools) |

`GET /network` (the old interface summary) and `POST /network/probe` moved into the same `Route`, since a
`Method` and a `Route` on one prefix in chi make the first one disappear.

## Tests

Parsers and renderers are tested against sanitized fixtures copied from the tools (`testdata/`), every
apply's command order and rollback with the recorder, and every guard as a table. Live tests behind
`JD_NETNS_LIVE=1` build devices, routes, rules, a gateway table with forwards, limits and blocklists,
and shaping inside throwaway network namespaces — never on the host's own interfaces.
`TestLiveBridgeVLANsFloodEndsAndReadinessAgainstARealKernel` applies port VLANs and VXLAN flood ends,
reads the bridge view and readiness from the real kernel and replays the unit's bridge lines into a
second fresh namespace; `TestLiveBridgeVLANRecoveryAfterProcessDeath` kills the applying process
after its first `bridge vlan` change and recovers the previous membership from a fresh process with
only the journal; `TestLiveMacvlanBridgeModeConnectivity` measures sibling reachability and parent
isolation. `TestLiveConntrackDumpReadsAnOwnedNamespace` must run as root
(`sudo -E JD_NETNS_LIVE=1 go test ./internal/netx -run TestLiveConntrack`) because the netlink reader
enters the throwaway namespace in-process.

### Retained capture routes

`/network/captures` is owned by `netcapture`, separate from the network configuration spec.
`GET /captures/`, `/captures/interfaces` and `/captures/{id}` read private records and native choices;
`POST /captures/` queues an immutable bounded request, and `POST /captures/{id}/cancel` waits for
process cleanup. `GET /captures/{id}/pcap` downloads original bytes after integrity validation;
`GET /captures/{id}/support` exports separately redacted metadata. `DELETE /captures/{id}` removes a
terminal record with the destructive gate. Every route requires `system.admin`; generic capture
job access has the same gate. See [capture lifecycle](network-captures.md) for hard limits and scope.

## Additional retained observations and planning

[Native DNS investigations](network-dns-evidence.md) pin an active supported systemd-resolved
owner, retain longest-suffix policy candidates and request fresh network-only record evidence.
Answering-link, native encryption, strict TLS policy and DNSSEC validation have separate provenance;
application/NSS, exact upstream and provider scope remain unknown. Unreadable, inactive or empty
declared private scopes refuse querying before fallback, including in the existing effective lookup.
`Store.Open` initializes the additive evidence schema; `Server.Start` reconciles lost running rows
without rerunning questions. History, launch and export require admin; reads never investigate.

[Controlled external checks](network-external-checks.md) enroll rootless outbound-only sources with
closed immutable target/address/family/port scopes. Management stays admin-only; dedicated signed
machine requests run behind the network allowlist and authenticate independently. Agent-reported
DNS/TCP/TLS observations do not establish geography or universal provider reachability.

[Shared IPAM](network-ipam.md) retains exact pools/reservations and fresh per-owner overlap coverage.
Docker and WireGuard creation atomically claim matching reservations before native work. Lost
outcomes hold the allocation for explicit review without automatic native retry or deletion.
Provider/foreign coverage stays unknown; planning does not replace the native owner.

[Native socket history](network-flow-accounting.md) records only after explicit admin opt-in.
Identity-safe TCP counter deltas, UDP peers, fresh Docker descriptor attribution and period quality
are bounded and retained. Snapshot gaps, UDP byte counts and short-flow completeness remain unknown;
page reads and export never collect. The separate [kernel observer](network-flow-observer.md)
requires a reviewed explicit opt-in and retains TCP/UDP packet subtotals and declared coverage gaps;
its figures are never added to the native TCP channel and do not establish complete host traffic.
