# The network module (`internal/netx`)

`netx` changes the host's network: its devices, routing, gateway table, protections, shaping, tunnels
and resolver. `netsec` keeps reading the network for the posture and keeps the firewall, sshd,
fail2ban, CrowdSec and Suricata; this package is the half that writes. The Network section of the
frontend (`/network/*`) is its only client. The plan it was built from, with the reasoning behind each
choice, is [`docs/audits/2026-10-07-network-section/`](../../audits/2026-10-07-network-section/README.md).

## Three rules

- **One spec, restored by the host.** Every device, address, route, rule, namespace, shaping entry,
  gateway entry and kernel setting the dashboard makes is written into
  `/etc/just-dashboard/network/spec.json` (`spec.go`). It is rendered (pure functions, golden-tested)
  into `links.batch` (`ip -force -batch`), `shaping.batch` (`tc -force -batch`), `gateway.nft`
  (`nft -f`, declare–delete–define so loading is an atomic replace) and
  `/etc/sysctl.d/90-just-dashboard.conf`. `just-dashboard-network.service`, a oneshot unit ordered after
  the network managers, Docker, ufw and firewalld, runs them at boot, so what was made survives a
  reboot whether or not the dashboard is running, and a reinstalled dashboard finds what the last one
  made. The spec is a file beside its renders rather than rows in SQLite because it describes the host.
- **The client path is guarded.** Every change is judged against how the kernel answers the address the
  request came from (`path.go`, `ip -j route get`): the device the reply leaves through, its gateway
  and its source. A route or rule is applied, the path is resolved again (`verifyPath`, and
  `verifyRouting`, which also asks `route get CLIENT from SOURCE` so a rule selecting on the server's own
  address is caught), and the change is taken back if the answer moved. Guards answer
  `409 would_lock_you_out` with the sentence saying why.
- **Only what was made here is removed.** Docker's bridges and veths, Tailscale's device, table 52 and
  rules, the provider's DHCP routes, the kernel's own routes and its fallback tunnel devices (`gre0`,
  `ip6tnl0`, …) are read and named with their owner, never edited. A change to a device the dashboard
  did not make (state, MTU, bridge membership) is runtime-only and says so (`persisted: false`).

## Applying a change

`Service.commit` (`persist.go`) is the proxy editor's order: render the new spec, check what can be
checked (`nft -c -f` on the gateway file whenever it changed), apply the one runtime change, verify it,
and only then write the boot files atomically and save the spec. A failure before the files are written
runs the change's `undo`, so the boot files never describe a network that did not work. `s.mu`
serialises every mutation. The unit is enabled, not started: everything it would restore was just
applied. Every host command is `hostexec` with explicit argv (the package variable `run`, which tests
replace with `record(t)` from `helpers_test.go`); the batch files are data for `ip`, `tc` and `nft`,
and every value written into them is parsed by type first (`validate.go`).

`ip -batch` cannot carry `-6`, so IPv6 policy rules are refused (IPv6 routes are fine: the family comes
from the address).

## Devices, namespaces, routing (`links*.go`, `namespaces.go`, `routes.go`, `forwarding.go`, `bgp.go`)

- `ReadLinks` joins `ip -j -d -s link` and `ip -j addr` with the default routes (the uplink), the client
  path, the spec, each running container's veth (read from `/proc/<pid>/root/sys/class/net/*/iflink`,
  no subprocess, with PIDs cached per container start in the api layer) and the sampler's rates. Each
  device carries a kind, a role (uplink, tunnel, bridge, container, vlan, virtual, physical, loopback),
  an owner and, where it may not be changed, a guard sentence.
- Create covers bridge, VLAN, VXLAN, GRE/GRETAP/IP6GRE, dummy, macvlan and veth (into a namespace the
  dashboard made). The uplink, the client-path device, loopback, Docker's and Tailscale's devices and
  any device holding an address cannot be set down, deleted or enslaved to a bridge; an MTU under 1280
  is refused on the client path and on any device with a global IPv6 address.
- Routing reads every table in both families with `rt_tables` names (the local table is counted, not
  listed), the policy rules with their owners, and the client path. Added rules take priorities in
  10000–19999, which no distribution or Tailscale uses; a rule with no selector, a second default route
  in main, and tables 52, 253 (for routes) and 255 are refused.
- Forwarding is per family. Turning it off is refused while Docker networks, an enabled forward or NAT
  entry, a WireGuard exit or Tailscale's exit node or subnet routes need it; the api layer supplies the
  Docker count and Tailscale's state (`TailscaleNeedsForwarding`).
- BGP is read from FRR (`vtysh -c "show bgp summary json"`) where it runs; read-only.

## Traffic (`sampler.go`, `traffic.go`, `ebpf.go`)

The sampler reads `/proc/net/dev` every two seconds into a fifteen-minute ring per device (the live
figures and wires) and records a row per device every metrics interval into `metric_interface_samples`
(the interval's mean, its busiest two seconds, and its errors and drops), pruned by the metrics
retention. Docker's veths are not recorded; each container's traffic is in `metric_container_samples`,
which `/network/traffic/containers` differences per sample in SQL. Per-program traffic differences
`ss -tinpH`'s per-socket byte counters between reads (TCP only). eBPF is an inventory from `bpftool`
(programs, XDP and tc attachments), not a probe the dashboard loads.

## Gateway and protection (`gateway*.go`, `blocklists.go`, `protection.go`, `conntrack.go`)

`inet jd_gateway` only ever drops or translates. Chains: `pre` (raw priority: trusted returns, then
each blocklist set drops, counted), `input` and `forward` (−10: established and trusted return, then
the rate and connection limits; the forward chain also marks a NAT entry's new connections), `nat_pre`
(each port forward: `ct mark set` then `dnat`) and `nat_post` (masquerade or SNAT per NAT entry, and per
forward whose source NAT resolves to masquerade). Every rule carries a comment its counters are read
back by.

**A port forward admits its own traffic.** One table's `accept` cannot override another's `drop`, and
ufw and Docker drop forwarded traffic by default, so translated connections carry the mark
`0x4a000000/0xff000000` (outside Tailscale's packet-mark bits) and one rule per chain admits exactly
them: `-m connmark --mark … -j ACCEPT` at the top of iptables' and ip6tables' `FORWARD`, `INPUT` and
`DOCKER-USER`. They are re-asserted on every gateway change (a `ufw reload` or a Docker restart can
remove them), restored at boot by the unit (delete then insert, so idempotent), and removed when
nothing translates. Writes are refused (`409 network_read_only`) where firewalld is active or another
nftables table drops forwarded traffic, naming the chain and the accept to add there. A forward or NAT
entry that needs forwarding while it is off is refused with `409 forwarding_off`; the page offers the
switch.

Blocklists are manual, country (ipdeny.com aggregated zones, v4 and v6) or feed (Spamhaus DROP,
FireHOL level 1 or any http(s) URL); fetches are bounded (30 s, 16 MB), parsed with `netip`, stripped of
private and reserved ranges (FireHOL level 1 holds them, and dropping them would cut off every
container and tailnet peer), merged, cached to `lists/<id>.txt` and refreshed daily by
`StartBlocklistRefresh`, which `Server.Start` runs. Every drop is preceded by the `trusted` sets:
loopback, the allowlist ranges narrower than /8 (v4) and /16 (v6), and the addresses the spec keeps
(the requester's, added on their first protection entry). A manual entry holding the requester's
address is refused; a fetched list that holds it is reported (`containsYou`).

Kernel protections are a closed list of fifteen sysctls (SYN cookies, loose reverse-path filtering —
strict is not offered because it breaks policy routing and tunnels — redirects, source routing,
ICMP, SYN backlog and retries, RFC 1337, martians, the conntrack maximum), each with its recommendation
and why, written to the sysctl drop-in. Conntrack's count against its maximum is read from `/proc`.

## Shaping (`shaping.go`)

Per device a root discipline (fq_codel, cake, fq) and upload and download limits (htb with fq_codel
egress, ingress policing), and BBR as a switch (`tcp_congestion_control=bbr`, `default_qdisc=fq`).
A limit under 1 Mbit/s on the uplink or the client-path device is refused.

## VPN (`wireguard.go`, `wgconf.go`, `wgkeys.go`, `wgserver.go`, `wgpeers.go`, `qr.go`, `vpn_store.go`, `tailscale.go`, `headscale.go`)

- **WireGuard** is read from `wg show all dump` joined with `/etc/wireguard/*.conf`; private and
  preshared keys never leave the package. Only files whose first line is `# Managed by Just Dashboard`
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
  there.
- **Tailscale** is read from `tailscale status --json` and `debug prefs`. The only changes offered are
  what this server offers the tailnet — an exit node and subnet routes — through `tailscale set`; using
  another node as an exit, shields up, down and logout are never run, because each can cut off the
  browser reading the page. **Headscale** is read where its binary or container runs.

## DNS (`dns.go`, `dns_hosts.go`, `dns_lookup.go`)

The resolver chain is `/etc/resolv.conf`'s mode, systemd-resolved's global and per-link scopes and
statistics, what answers on port 53 (reusing `proxysvc.ListListeners`) and any AdGuard Home or Pi-hole.
Upstreams, DNS over TLS, DNSSEC, domains and the cache are written to
`/etc/systemd/resolved.conf.d/90-just-dashboard.conf`; resolved is restarted and a well-known name is
resolved through the stub, and the previous file is restored if either fails. The host's records are a
marked block in `/etc/hosts`; in the container that file is Docker's own bind mount, so the host's is
written through `/host/etc/hosts`. The lookup race asks every configured resolver and every preset, so
the name typed is sent to those public resolvers.

## Routes

All under `/api/v1/network` (`handlers_network*.go`). Reads are `read`, except `/vpn/*` and
`/traffic/processes`, which name who connects and are `system.admin`. Every mutation is `system.admin`;
removals, setting a device down, turning forwarding off, disabling a forward, NAT entry, limit or
blocklist, weakening a kernel protection and changing the resolver are inside `s.destructive` (by path,
or by content in the handler for the PUTs and the settings post). No route takes a typed phrase.

| Area | Routes |
| --- | --- |
| Overview | `GET /overview`, `GET /links`, `GET /traffic/live`, `GET /traffic/history` |
| Devices | `POST /links`, `DELETE /links/{name}`, `POST /links/{name}/up`, `/down`, `/mtu`, `/master`, `/addresses`, `DELETE /links/{name}/addresses?cidr=`; `GET`/`POST /namespaces`, `DELETE /namespaces/{name}` |
| Routing | `GET /routing`, `POST /routing/routes`, `DELETE /routing/routes/{id}`, `POST /routing/rules`, `DELETE /routing/rules/{id}`, `POST /forwarding/{ipv4,ipv6}/{on,off}`, `GET /bgp` |
| Gateway | `GET /gateway`, `POST`/`PUT`/`DELETE /gateway/forwards[/{id}]`, `/gateway/nat[/{id}]` |
| Protection | `GET /protection`, `/protection/limits[/{id}]`, `/protection/blocklists[/{id}]`, `POST /protection/blocklists/{id}/refresh`, `POST /protection/settings`, `DELETE /protection/settings/{key}`, `DELETE /protection/trusted?address=` |
| Shaping | `GET /shaping`, `POST`/`DELETE /shaping/{device}`, `POST /shaping/bbr` |
| VPN | `GET /vpn`, `POST /vpn/wireguard`, `DELETE /vpn/wireguard/{iface}`, `POST /vpn/wireguard/{iface}/up`, `/down`, `/exit`, `/peers`, `GET`/`DELETE /vpn/wireguard/{iface}/peers/{id}/config`, `DELETE /vpn/wireguard/{iface}/peers/{id}`, `POST /vpn/tailscale` |
| DNS | `GET`/`POST`/`DELETE /dns`, `GET`/`PUT /dns/hosts`, `POST /dns/lookup` |
| Traffic | `GET /traffic/processes`, `GET /traffic/containers`, `GET /ebpf` |

`GET /network` (the old interface summary) and `POST /network/probe` moved into the same `Route`, since a
`Method` and a `Route` on one prefix in chi make the first one disappear.

## Tests

Parsers and renderers are tested against sanitized fixtures copied from the tools (`testdata/`), every
apply's command order and rollback with the recorder, and every guard as a table. Live tests behind
`JD_NETNS_LIVE=1` build devices, routes, rules, a gateway table with forwards, limits and blocklists,
and shaping inside throwaway network namespaces — never on the host's own interfaces.
