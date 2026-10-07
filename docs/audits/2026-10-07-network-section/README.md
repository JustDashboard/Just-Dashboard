# The Network section: plan

The operator asked for the Security section's networking pages to leave it and become a section of
their own that turns the server into a router, firewall, VPN, proxy, DNS server, traffic monitor and
security gateway, all managed visually. This is the plan that work follows, written before any of it,
and kept as the record of what was decided and why.

## 1. What moves, what stays, what is new

**Security keeps what is about who may get in:** Overview (the posture and the ways onto the
machine), SSH (gaining a jump-host section), Intrusion (fail2ban, gaining CrowdSec and Suricata) and
Logins. Everything about the network leaves it.

**The Network section** is a new rail group between Workspace and Protection:

| Page | Route | What it is for |
| --- | --- | --- |
| Overview | `/network` | The live topology (internet → uplink → firewall → this server → every network behind it, and every tunnel out of it), live throughput per interface, the readings and an attention list. |
| Interfaces | `/network/interfaces` | Every device with its addresses, live rates, errors and owner; create bridges, VLANs, VXLAN, GRE/GRETAP, dummy and macvlan devices; addresses, MTU, up/down, bridge membership; network namespaces; the container behind every veth. |
| Routing | `/network/routing` | Every routing table, the policy rules that pick between them, IPv4/IPv6 forwarding, and BGP through FRR where it is installed. |
| Firewall | `/network/firewall` | The firewall page, moved unchanged in behaviour. |
| Gateway | `/network/gateway` | Port forwarding (DNAT), NAT/masquerade for a network or a tunnel, and the reverse proxy, TCP/UDP streams and load balancing as handoffs into Proxy & TLS. |
| Protection | `/network/protection` | Rate limits and per-source connection limits on a port, IP and country blocklists, and the kernel's DDoS-related settings, with the connection-tracking table's fullness. |
| VPN | `/network/vpn` | WireGuard (one-click server, devices with QR codes, site-to-site peers, exit node), Tailscale (peers, exit node and subnet routes advertised) and Headscale where it runs. |
| DNS | `/network/dns` | The resolver chain, custom upstreams with presets (including ad-blocking ones), DNS over TLS and DNSSEC, the host's own records, a resolver race, and what answers on port 53. |
| Traffic | `/network/traffic` | Bandwidth per interface live and over 1h/6h/24h/7d, per container, per process, traffic shaping and speed limits, and the eBPF programs attached to the machine. |
| Connections | `/network/connections` | The connection viewer, moved. |
| Tools | `/network/tools` | The probes and the port scanner, moved. |

**Proxy & TLS** (reverse proxy, automatic certificates, TCP/UDP streams, upstream load balancing)
moves from *Server configuration* into the Network group. Its pages do not change.

The four routes that moved answer with a redirect that keeps the query string, because the firewall
rule hand-off (`?rule=…`) and the tool hand-off (`?tool=…`) are links other pages already write:
`/security/firewall`, `/security/connections`, `/security/network` (→ `/network/interfaces`) and
`/security/tools`. Every internal link is updated as well, so the redirect is only for bookmarks.

## 2. How the server is changed: one spec, one boot unit

Everything Just Dashboard creates on the network is written down in one place and restored at boot
by the host itself, so it does not depend on the dashboard running.

- **The spec** is `/etc/just-dashboard/network/spec.json`: the links, extra addresses, routes, policy
  rules, namespaces, shaping, gateway entries (forwards, NAT, limits, blocklists) and kernel settings
  the dashboard owns. It lives beside what it renders rather than in SQLite because it describes the
  host: a reinstalled dashboard finds what the last one made.
- **Rendered from it** (pure functions, golden-tested):
  - `links.batch` — `ip -batch` lines (`link add`, `addr add`, `route add`, `rule add`, `netns add`);
  - `shaping.batch` — `tc -batch` lines;
  - `gateway.nft` — one `inet jd_gateway` table, written as *declare, delete, define* so loading it
    is an atomic replace;
  - `/etc/sysctl.d/90-just-dashboard.conf` — the kernel settings.
- **`just-dashboard-network.service`**, a oneshot unit ordered after `network-online.target`,
  `docker.service` and `ufw.service`, runs `ip -force -batch`, `tc -force -batch`, `nft -f` and the
  forward-admission rules (§4) at boot. It is installed and enabled the first time something is
  created, and its `ExecStop` deletes the gateway table, so stopping it takes the dashboard's
  network changes out of the way cleanly.
- **Every change is applied in the proxy editor's order:** validate the new spec → run the guards →
  render → check (`nft -c -f` for the gateway) → apply the one runtime change (a specific `ip`,
  `tc`, `nft -f` or `sysctl -w`) → verify → write the files atomically → save the spec. A failure
  before the files are written rolls the runtime change back, so the boot files never describe
  something that did not work. One mutex serialises every mutation.
- **Every command is `hostexec` with explicit argv** (invariant 4). The batch files are data read by
  `ip`/`tc`/`nft`, never a shell. Each value written into them is validated by type (interface names
  by the kernel's rules, addresses with `net/netip`, numbers bounded), so no request text reaches a
  file unparsed.

Objects the dashboard did not create (Docker's bridges and veths, Tailscale's device and table 52,
the provider's DHCP routes, the kernel's own routes) are read and shown with their owner, and are
never deleted or edited. Only `managed` objects can be removed.

## 3. Guards: the machine must stay reachable

This dashboard is usually reached over the network it is changing. Every guard is a pure function
over the spec, the live state and **the client path** — the address the request came from and the
device, gateway and source the kernel would use to answer it (`ip -j route get <client>`).

- **Links:** the uplink (a default route), the client-path device and loopback cannot be set down,
  deleted, renamed, enslaved to a bridge, or given an MTU under 1280. A link holding an address
  cannot be enslaved to a bridge (that removes its addresses from service).
- **Addresses:** the address the client path answers from, and the uplink's primary address, cannot
  be removed.
- **Routes and rules: test, then keep.** A route or policy rule is applied, the client path is
  resolved again, and if the device or gateway changed the change is rolled back and refused:
  "this route would take the reply to your browser off tailscale0". The default route, kernel and
  DHCP routes, and Tailscale's table 52 and its rules are read-only. Policy rules the dashboard writes
  use priorities 10000–19999, which no distribution or Tailscale uses.
- **Forwarding:** IPv4 forwarding cannot be turned off while Docker networks, a WireGuard exit or NAT
  entry, or a Tailscale exit node/subnet route need it; the refusal names which.
- **Blocklists and limits:** every drop in the gateway table is preceded by a `trusted` set holding
  loopback, the client's address, the dashboard's allowlist ranges and — when the client is on the
  tailnet — the tailnet's range, so neither a country list nor a rate limit can refuse the operator.
  A manual entry covering the client's address is refused outright.
- **Shaping:** no rate below 1 Mbit/s on the uplink or the client-path device.
- **Tailscale:** never `down`, `logout`, shields-up while the client is on the tailnet, or using an
  exit node on this server (which reroutes replies to the internet). Only what this server *offers*
  — an exit node, subnet routes — can be changed.
- **WireGuard:** this server's side never routes `0.0.0.0/0` into a tunnel; full tunnels are the
  client's configuration. A port already bound for UDP is not offered.

## 4. The gateway table and forward admission

`inet jd_gateway` only ever **drops or translates**; admitting traffic stays the firewall's job,
except for the flows the operator created a translation for:

- `prerouting` filter at raw priority (−300): `trusted` returns, then each blocklist set drops,
  counted per list.
- `input`/`forward` filter at −10: established returns, `trusted` returns, then per-port rate limits
  (`limit rate over`, or a dynamic per-source set with `limit` or `ct count over`), counted.
- `prerouting` nat at dstnat−10: each port forward is `iifname … dport … ct mark set … dnat to …`.
- `postrouting` nat at srcnat−10: masquerade for each NAT entry (a source network out an interface),
  and for forwards whose target is not on a local network (so replies come back through here).

**A port forward admits its own traffic.** nftables cannot let one table's `accept` override another
table's `drop`, and on a ufw or Docker host the iptables `FORWARD` chain drops by default. So every
translated flow is stamped with a connection mark (`0x4a000000/0xff000000`, outside the bits
Tailscale's packet marks use), and one rule per chain admits exactly those connections:
`iptables -I FORWARD 1 -m connmark --mark 0x4a000000/0xff000000 -j ACCEPT` (and `INPUT` for a
redirect to a local port, and `DOCKER-USER` where Docker exists, because Docker's own chain drops
traffic to container ports it did not publish before `FORWARD`'s later rules run). The same for
ip6tables. They are inserted at apply time and at boot (delete-then-insert, so it is idempotent),
and removed when nothing needs them. Tunnel traffic that NAT carries out (a WireGuard exit) is
marked in the prerouting filter chain by its incoming interface the same way.

**Capability per firewall**, declared like `FirewallCapabilities`:

| Firewall | Gateway writes |
| --- | --- |
| ufw (iptables-nft or legacy), plain iptables, none | yes |
| firewalld | read-only: firewalld filters forwarded traffic in its own nftables table, which an accept elsewhere cannot override. The page says so and points at zones. |
| another nftables table whose forward chain drops | read-only, naming the table and chain, with the accept for the mark to add there. |

**Blocklists** are named sets of CIDRs, v4 and v6, of three kinds: *manual* (typed), *country* (the
ipdeny.com aggregated zone files, fetched on demand and refreshed daily) and *feed* (Spamhaus DROP,
FireHOL level 1, or any URL). Fetches are bounded (16 MB, 30 s), parsed with `netip`, merged, guarded,
cached to `lists/<id>.txt`, and loaded as an atomic `flush set` + `add element` transaction. Each list
reports its entry count, last refresh, and packets dropped since it was loaded.

## 5. Each area's backend

A new package, `internal/netx`, owns everything here; netsec keeps the firewall, sshd, fail2ban, the
posture and connections it already owns. `netx.Service` has the same recorded-transcript `run`
variable netsec's firewall uses, so every parser and every apply is tested against output copied from
the tools themselves.

- **Links** (`links.go`): `ip -j -d link show` + `ip -j addr show` + `/sys/class/net/*/speed`,
  classified by the kernel's kind and owner (Docker bridges by `br-<id>` and their network name,
  Tailscale's `tun`, WireGuard, JD-managed), each veth's container found through
  `/proc/<pid>/root/sys/class/net/*/iflink` for every running container, XDP attachments from
  `bpftool`. Create: bridge, VLAN, VXLAN, GRE/GRETAP/IP6GRE, dummy, macvlan; addresses; MTU; state;
  master; namespaces (`ip netns`) and a veth pair into one.
- **Rates** (`sampler.go`): `/proc/net/dev` every 2 s into a 15-minute ring for the live figures,
  and every metrics interval into a new additive table `metric_interface_samples` (pruned by the
  metrics retention) for the 1h/6h/24h/7d charts.
- **Routing** (`routes.go`): every table in both families with `rt_tables` names, the rules, the
  forwarding sysctls; add/delete routes and rules under the test-then-keep guard. **BGP**
  (`bgp.go`): `vtysh -c "show bgp summary json"` and the neighbour list where FRR runs.
- **Gateway** (`gateway.go`, `nft.go`, `blocklists.go`): §4.
- **Protection** (`protection.go`): a closed list of kernel settings (SYN cookies, reverse-path
  filtering loose, redirects and source routing off, broadcast ICMP ignored, SYN backlog,
  SYN-ACK retries, RFC 1337, conntrack maximum) with the value each is at, the recommendation and why;
  conntrack count against its maximum.
- **Shaping** (`shaping.go`): `tc -j -s qdisc show`; per interface a queue discipline (fq_codel,
  cake, fq) and upload/download limits (htb + fq_codel egress, ingress policing); BBR as a switch.
- **WireGuard** (`wireguard.go`): `wg show all dump` joined with `/etc/wireguard/*.conf`; keys are
  made in Go (`x/crypto/curve25519`); the server is a `wg-quick@` unit with its native config; a
  device or site peer is added to the config and `systemctl reload` syncs it live; the client's
  config is returned with a QR code (`boombuler/barcode`, already in the module graph through the
  TOTP library, promoted to direct) and kept sealed by `auth.Sealer` in a new additive table
  `network_vpn_clients` so an administrator can show it again — or forget it. A removed interface's
  config is moved aside, not deleted. Installing `wireguard-tools` goes through the Packages
  install route.
- **Tailscale and Headscale** (`tailscale.go`): `tailscale status --json` and `debug prefs`;
  `tailscale set --advertise-exit-node` and `--advertise-routes`; a Headscale binary or container is
  read with `headscale nodes list -o json`.
- **DNS** (`dns.go`): `resolvectl status` and `statistics`, `/etc/resolv.conf`, who listens on 53;
  upstreams, DNS over TLS and DNSSEC written to `resolved.conf.d/90-just-dashboard.conf`; the host's
  records as a marked block in `/etc/hosts`; a lookup raced against every resolver with its latency.
- **Traffic** (`traffic.go`): per process from `ss -tinpH` (each socket's bytes acked and received,
  differenced between two reads, summed by program), per container from the recorder's container
  samples and the live Docker stats; eBPF from `bpftool -j prog show` and `net show`.
- **Intrusion additions** (`netsec`): CrowdSec through `cscli` JSON (decisions, alerts, bouncers;
  add and delete a decision, refusing the caller's address); Suricata's state and its last alerts
  from `eve.json` under the log roots.
- **SSH bastion** (`netsec/sshd.go`): `AllowTcpForwarding`, `GatewayPorts`,
  `AllowAgentForwarding`, `PermitTunnel` and `MaxSessions` join the closed directive list; the page
  writes a ProxyJump snippet for the hosts behind this one.

Every absent tool is information, not an error: the route answers `…_unavailable` and the page draws
a dashed placeholder with the install hand-off, as Security already does for fail2ban.

### Routes

All under `/api/v1`. Reads are `read` unless noted; every mutation is `system.admin`; every removal,
`down`, disable and replace that can cost access or traffic is inside `s.destructive` with ordinary
confirmation (invariant 3's five typed phrases are not extended).

```
GET  /network                         (unchanged: NetworkInfo, kept for compatibility)
POST /network/probe                   (unchanged, admin)
GET  /network/overview                topology, rates, readings, findings
GET  /network/traffic/live            the 2 s ring for every interface
GET  /network/traffic/history         ?window=1h|6h|24h|7d&iface=
GET  /network/traffic/processes       admin (program names and remotes)
GET  /network/traffic/containers
GET  /network/ebpf
GET  /network/links                   POST create; PATCH /{name} (mtu, master);
POST /network/links/{name}/state      destructive when down
POST /network/links/{name}/addresses  DELETE (destructive)
DELETE /network/links/{name}          destructive
GET  /network/namespaces              POST; DELETE /{name} destructive
GET  /network/routes                  POST /routes, DELETE /routes/{id}; POST /rules, DELETE /rules/{id}
POST /network/forwarding              destructive when turning off
GET  /network/bgp
GET  /network/gateway                 POST/DELETE /forwards, /nat
GET  /network/protection              POST /protection/settings; POST/DELETE /limits;
                                      POST/DELETE /blocklists, POST /blocklists/{id}/refresh
GET  /network/shaping                 POST /shaping/{iface}; DELETE destructive; POST /shaping/bbr
GET  /network/vpn                     admin: WireGuard + Tailscale + Headscale
POST /network/vpn/wireguard           one-click server; DELETE /{iface} destructive
POST /network/vpn/wireguard/{iface}/state | /exit
POST /network/vpn/wireguard/{iface}/peers; DELETE /peers/{id} destructive
GET  /network/vpn/wireguard/{iface}/peers/{id}/config   admin, the sealed config + QR
DELETE /network/vpn/wireguard/{iface}/peers/{id}/config forget the key
POST /network/vpn/tailscale           advertise exit node / routes
GET  /network/dns                     POST /dns (upstreams, DoT, DNSSEC); PUT /dns/hosts;
POST /network/dns/lookup
GET  /security/crowdsec               POST/DELETE /security/crowdsec/decisions
GET  /security/suricata
```

## 6. The pages

Every page is a reading page (§16 register A), redesigned by §15's passes with the host Overview and
the 0.7.1 overhauls (#156, #157, #158, #159, #162, #163) as the reference. What makes them alive is
the sanctioned five and nothing else: things drawn as themselves (WireGuard, Tailscale, Docker, the
distribution, a container's product), figures that move (`LiveFigure`/`NumberTicker`, `TileTrend`),
colour that names a kind (`--tag-*` for a device kind and a rule's action, the port hue, `--git-*`
for a pending change, `--chart-*` per series), the lit edge on what is taken, and work said as it
happens (a beam that carries while bytes flow, a `BorderBeam` while an apply runs).

- **Overview** opens on the host's identity line (public addresses, uplink and its speed, gateway,
  and *you arrive through tailscale0*), then the **topology**: three lanes in the wiring vocabulary
  — the outside (the internet, the tailnet with its peers online, each WireGuard tunnel with its
  peers), the edge (the uplink, the firewall with its inbound default, the gateway with its forwards),
  and the inside (each Docker network with its containers' marks, each bridge, VLAN and namespace) —
  with this server in the middle. A line carries (moves) while its device moves bytes and names its
  rate; dashed where the thing is not set up, with the placeholder's install or create hand-off.
  Under it the readings (throughput in and out, connections, ports open to anyone, VPN peers online,
  dropped by blocklists today), each live and with its last 15 minutes as a trend, and an attention
  list.
- **Interfaces** groups devices by role (uplink, tunnels, bridges with their members nested,
  containers, virtual), each row its kind's mark, addresses with the prefix in the port hue, a live
  in/out bar and figure, and its owner; create opens one sheet per kind; namespaces under them.
- **Routing** is one table per routing table, the rules drawn as the order a packet is decided in,
  forwarding as two switches with what needs them, and BGP's neighbours where FRR runs.
- **Gateway** opens on a picture of each forward (the internet's port → this server → the target
  drawn as what answers there), then the forwards and NAT entries as cards with their counters, and
  the reverse proxy, streams and load balancing as lit cards into Proxy & TLS.
- **Protection** opens on dropped-today readings per list, a world strip of blocked countries, the
  limits with their counters, and the kernel settings as `FormSection aside`s at or below
  recommendation, with the conntrack meter.
- **VPN** opens on a picture of each tunnel (this server and its peers, a peer's line moving while it
  handshook in the last three minutes, its bytes beside it), devices and sites as cards that open
  their QR sheet, and Tailscale's peers with direct/relayed and exit-node state.
- **DNS** opens on the resolver chain as a picture (this server → stub → each upstream drawn as its
  provider, DoT locked or not), the race tool, presets as product cards, the host's records as rows.
- **Traffic** opens on per-interface live charts with a window switch, then the workloads by bytes
  (containers and processes as spans of one bar, the Processes page's `workloads.tsx` shape),
  shaping per interface, and the eBPF inventory.

## 7. Verification

- Go: parsers and renderers against sanitized fixtures copied from real `ip`, `tc`, `nft`, `wg`,
  `tailscale`, `resolvectl`, `ss`, `bpftool`, `vtysh` and `cscli` output; guards as tables; applies
  with `run` replaced by a recorder that checks argv and order, including every rollback.
- Live: `netx/*_live_test.go` behind `JD_NETNS_LIVE=1` creates a throwaway network namespace and
  applies real links, routes, a gateway table and shaping inside it — never on the host's own
  interfaces. This server is the operator's production machine; nothing in this work mutates its
  real network.
- `bun test src` for the pure frontend logic (topology layout, rate formatting, the guards the
  forms mirror); `network-ui.spec.ts` against a mocked API for every page, the redirects, and the
  design system's structural rules; `security-ui.spec.ts` and `navigation.spec.ts` updated.
- `scripts/test-changed.sh`, a production build, and screenshots at 1440 and 390 of every page,
  in this directory.
