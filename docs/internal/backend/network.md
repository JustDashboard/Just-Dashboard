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
  `/etc/sysctl.d/90-just-dashboard.conf`. `just-dashboard-network.service`, a oneshot unit ordered after
  the network managers, Docker, ufw and firewalld, runs them at boot, so what was made survives a
  reboot whether or not the dashboard is running, and a reinstalled dashboard finds what the last one
  made. The spec is a file beside its renders rather than rows in SQLite because it describes the host.
- **The client path is guarded.** Every change is judged against how the kernel answers the address the
  request came from (`path.go`, `ip -j route get`): the device the reply leaves through, its gateway
  and its source. A route or rule is applied, the path is resolved again (`verifyPath`, and
  `verifyRouting`, which also asks `route get CLIENT from SOURCE` so a rule selecting on the server's own
  address is caught), and the change is taken back if the answer moved. Three anchors are read with
  it and compared the same way — the route to 1.1.1.1, the same with Tailscale's packet mark 0x80000,
  and to 2606:4700:4700::1111 (only asked of the kernel, never contacted) — because the operator's
  way in rides on this server's own way out: tailscaled's packets, a WireGuard endpoint, the SSH
  session behind a tunnel. A request on loopback is followed to the SSH session carrying it
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
- Create covers bridge, VLAN, VXLAN, GRE/GRETAP/IP6GRE/IP6GRETAP, dummy, macvlan and veth (into a namespace the
  dashboard made). The uplink, the client-path device, loopback, Docker's and Tailscale's devices and
  any device holding an address cannot be set down, deleted or enslaved to a bridge, and neither can
  the parent or bridge such a device rides on; a passthru macvlan on a device that carries one is
  refused (it takes every frame the parent receives). An MTU under 1280 is refused on the client path
  and on any device with a global IPv6 address.
  The create form exposes VXLAN unicast or multicast destinations with an explicit sender for
  multicast, and decimal uint32 GRE keys plus TTL/IPv6 hop limits. GRE remains unencrypted and the
  form does not claim verified multicast underlay or remote endpoint reachability.
- Routing reads every table in both families with `rt_tables` names (the local table is counted, not
  listed), the policy rules with their owners, and the client path. Added rules take priorities in
  10000–19999, checking live foreign priorities as well as the spec in that family; a rule with no
  selector, a second default route
  in main, and tables 52, 253 (for routes) and 255 are refused. A zero packet-mark mask is refused,
  and unmarked discard rules are guarded as potential dashboard-reply selectors.
- Device/namespace removal checks shaping, NAT/forward ingress/egress, routes/rules, foreign children
  and veth-peer dependencies. A partial namespace failure restores already removed pairs.
- Forwarding is per family. Turning it off is refused while Docker networks, an enabled forward or NAT
  entry, a WireGuard exit or Tailscale's exit node or subnet routes need it. The API supplies separate
  Docker IPv4/IPv6 bridge counts: IPv6 uses its flag/subnet evidence; IPv4 conservatively counts all
  bridges because the Docker inventory lacks reliable IPv4-disable/custom-IPAM evidence. A failed
  Docker network listing blocks shutdown for both families until dependencies can be read.
  `TailscaleNeedsForwarding` also answers "needed" when Tailscale cannot be read.
  Turning IPv6 forwarding on is refused while the IPv6 default route was
  learned from a router advertisement on a device whose `accept_ra` is not 2: with forwarding on, the
  kernel ignores those advertisements and the route would expire.
- IPv4 forwarding resets kernel host settings; managed redirect protections are reasserted after
  changes and rendered after forwarding in the boot sysctl file.
- BGP is read from FRR (`vtysh -c "show bgp summary json"`) where it runs; read-only.

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

Files: `gateway*.go`, `blocklists.go`, `protection.go`, `conntrack.go`.

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
ceilings are always keyed by source address. Established traffic returns before these limits.

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
reachability; foreign expressions and provider policy can remain unknown. The full response and
fail-closed cache contract are in [gateway health](gateway-health.md).

Cache and kernel rollback retain the prior list under the mutation lock; fetches begun before a
URL/country edit cannot overwrite newer configuration. Failed boot-unit setup retains the committed
cache, matching the committed spec and kernel.

Blocklists are manual, country (ipdeny.com aggregated zones, v4 and v6) or feed (Spamhaus DROP,
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
and why, written to the sysctl drop-in. Conntrack's count against its maximum is read from `/proc`.

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

Files: `wireguard.go`, `wgconf.go`, `wgkeys.go`, `wgserver.go`, `wgpeers.go`, `qr.go`, `vpn_store.go`, `tailscale.go`, `headscale.go`.

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
- **Tailscale** is read from `tailscale status --json` and `debug prefs`. The only changes offered are
  what this server offers the tailnet — an exit node and subnet routes — through `tailscale set`; using
  another node as an exit, shields up, down and logout are never run, because each can cut off the
  browser reading the page. Offer updates serialize under the module lock, and unreadable preferences
  and status block forwarding shutdown. `SetTailscaleChecked` evaluates withdrawal authorization
  against those same locked preferences before writing; the separate classifier is advisory only.
  External CLI writers are outside this process lock. **Headscale** is read where its binary or container runs.
  `prefsReadable` explicitly distinguishes failed preference reads from empty advertised routes; the
  editor retains a rejected subnet draft and retries against refreshed authoritative preferences.

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

`GET /capabilities` reports host tool availability/package hints, systemd manager reachability and the
literal IPv6 sysctl with its per-interface caveat. An installed binary does not prove kernel/NIC/provider
support. `POST /probe` has [all 26 tools](../../audits/2026-10-08-network-audit/README.md#tools--networktools-all-26-server-diagnostics).
New tools are route lookup, path MTU, host support, bounded packet snapshot and Wake-on-LAN.

Route lookup emits no traffic; egress independently inspects IPv4/IPv6 kernel routes, including
IPv6-only hosts, without claiming the selected NIC address equals a provider-NAT public address.
Listeners includes TCP and UDP. Tracepath availability and ICMP filtering constrain path-MTU results.
TCP refusal can indicate a closed port or firewall rejection; a timeout does not identify its cause.

Packet snapshot uses fixed, validated tcpdump argv on one up interface: at most fifty packets or
fifteen seconds, no promiscuous mode, output file or explicit hex/ASCII dump. Summary decoders can
include sensitive protocol fields. Process output is drained while retained text is capped at 256 KiB. Deadlines terminate the entire
host-wrapper process group with bounded cleanup, including descendants holding output pipes.
Wake-on-LAN sends one Ethernet magic frame to a validated unicast MAC on a selected broadcast LAN
interface. Success confirms the send, not that a remote firmware/NIC woke. Neither tool crosses an
upstream router/provider restriction. All probes remain admin-only and audited.

## Routes

All under `/api/v1/network` (`handlers_network*.go`). Reads are `read`, except `/vpn/*` and
`/traffic/processes`, which name who connects and are `system.admin`. Every mutation is `system.admin`;
removals, setting a device down, turning forwarding off, disabling a forward, NAT entry, limit or
blocklist, weakening a kernel protection, turning a WireGuard exit off, withdrawing what this server
offers the tailnet and changing the resolver are inside `s.destructive` (by path, or by content in the
handler for the PUTs and posts). No route takes a typed phrase.

| Area | Routes |
| --- | --- |
| Overview | `GET /`, `GET /capabilities`, `GET /overview`, `GET /links`, `GET /traffic/live`, `GET /traffic/history` |
| Devices | `POST /links`, `DELETE /links/{name}`, `POST /links/{name}/up`, `/down`, `/mtu`, `/master`, `/addresses`, `DELETE /links/{name}/addresses?cidr=`; `GET`/`POST /namespaces`, `DELETE /namespaces/{name}` |
| Changes | `GET /changes/current`, `POST /changes/{id}/verify`, `/confirm` (admin session), `/recover` (also destructive) |
| Routing | `GET /routing`, `GET /routing/lookup?target=<literal>&source=<optional literal>&mark=<optional value>`, `POST /routing/routes`, `DELETE /routing/routes/{id}`, `POST /routing/rules`, `DELETE /routing/rules/{id}`, `POST /forwarding/{ipv4,ipv6}/{on,off}`, `GET /bgp` |
| Gateway | `GET /gateway`, `POST /gateway/admission/repair` (destructive), `POST /gateway/forwards`, `PUT`/`DELETE /gateway/forwards/{id}`, `POST /gateway/nat`, `PUT`/`DELETE /gateway/nat/{id}` |
| Protection | `GET /protection`, `POST /protection/limits`, `PUT`/`DELETE /protection/limits/{id}`, `POST /protection/blocklists`, `PUT`/`DELETE /protection/blocklists/{id}`, `POST /protection/blocklists/{id}/refresh`, `POST /protection/settings`, `DELETE /protection/settings/{key}`, `DELETE /protection/trusted?address=` |
| Shaping | `GET /shaping`, `POST`/`DELETE /shaping/{device}`, `POST /shaping/bbr` |
| VPN | `GET /vpn`, `POST /vpn/wireguard`, `DELETE /vpn/wireguard/{iface}`, `POST /vpn/wireguard/{iface}/up`, `/down`, `/exit`, `/peers`, `GET`/`DELETE /vpn/wireguard/{iface}/peers/{id}/config`, `DELETE /vpn/wireguard/{iface}/peers/{id}`, `POST /vpn/tailscale` |
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
