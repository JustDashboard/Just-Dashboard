# Networking inventory, fixes and compatibility audit

This audit follows networking PR [#169](https://github.com/JustDashboard/Just-Dashboard/pull/169),
starting from `patch/0.7.1` at `0f276d8c`. It checks the actual Network pages, their API routes, the
`netx` network module and the `netsec` diagnostics. Related Proxy, Docker and Security features are
listed as handoffs. Their entire subsystems were not retested by this networking change.

The dashboard has substantial host networking controls, but it is not a complete router operating
system. Kernel privileges, physical hardware, upstream routers and VPS providers impose constraints
that dashboard code cannot remove. The inventory below distinguishes editable features from
inspection, integrations and missing functionality. No claim of universal VPS or Linux support is made.

## Complete inventory of the Network section

### Overview — `/network`

- Hostname and the kernel route back to the operator, including the source, gateway and interface.
- IPv4 and IPv6 default routes, metrics and addresses assigned to uplinks.
- Topology joining the host, internet, firewall, physical and virtual links, tunnels, Docker bridges
  and containers; live interface traffic animates its connections.
- Firewall backend, enabled state, inbound policy and rule count.
- Socket and peer counts, internet-origin peers and listeners.
- IPv4/IPv6 forwarding state.
- Counts of managed interfaces, namespaces, routes, rules, forwards, NAT entries, blocklists, limits
  and shaping entries.
- VPN and resolver summaries.
- Network persistence unit status and managed spec location.
- Attention findings for disabled persistence, required forwarding that is off (by address family),
  unavailable/disabled managed firewall, conntrack pressure, recent interface errors/drops, missing
  carrier and unencrypted upstream DNS.
- Custom kernel/provider filtering is not inferred absent just because ufw/firewalld is unavailable.

### Interfaces — `/network/interfaces`

- Inventory of every visible host interface, its kind, role, owner, operational/admin state, carrier,
  MTU, MAC, negotiated speed, qdisc, addresses, bytes, packets, errors, drops and live rates.
- Default-route uplink and operator-path labels; bridge/bond membership and virtual-link parents.
- Container/veth correlation using Docker init PIDs and `iflink`; Docker network/bridge ownership.
- Create bridge, VLAN, VXLAN, GRE, GRETAP, IP6GRE, IP6GRETAP, dummy, macvlan and veth devices.
- Bridge STP; VLAN ID and parent; VXLAN VNI, parent, local/remote or multicast group and UDP port;
  GRE-family local/remote endpoint (key/TTL are API-only); macvlan parent and mode; veth peer and managed namespace.
- Optional initial MTU and address in the creation form, which starts devices up. The API accepts
  multiple initial addresses and an explicit admin-up state. Standalone veth creation is API-only;
  the UI creates veth connections through the namespace form.
- Bring a device up/down, edit MTU, add/delete addresses and select/clear bridge membership.
- Create, inspect and remove named managed network namespaces, including their interfaces and
  addresses; associate namespace processes with Docker containers where known.
- Remove dashboard-owned devices, addresses and namespaces; refuse removals while dependencies remain.
- Runtime-only changes to externally managed interface state/MTU/membership are identified as such.
- Protected uplinks, operator-path interfaces, loopback, provider/Docker/Tailscale objects, addressed
  bridge ports and protected parents cannot be taken away to make a form succeed.

### Routing — `/network/routing`

- Read routes across IPv4/IPv6 routing tables and table names; count the local table separately.
- Display destination, type, gateway, interface, preferred source, metric, table and owner.
- Read multipath next hops in the table; the API additionally exposes weights, scope and flags.
  Managed route creation uses one next hop/device rather than an ECMP editor.
- Add/remove owned IPv4 and IPv6 unicast, blackhole, unreachable and prohibit routes; unicast forms
  include optional preferred source with address-family validation.
- IPv4 **and IPv6** policy rules, with explicit family or family inferred from selectors.
- Source/destination prefix, incoming/outgoing interface, packet mark/mask, priority and lookup or
  discard action; table selection and optional comments.
- Use priorities 10000–19999, checking live rules as well as managed records before allocating one.
- Preserve foreign rules, Tailscale table 52, local/kernel routes and provider DHCP state.
- Compare operator, source-selected reply and internet-anchor routes after a change; undo a change
  that moves the protected path.
- IPv4/IPv6 forwarding switches with dependency checks for Docker, NAT/forwards, WireGuard and Tailscale.
- Docker IPv6 flag/subnet evidence guards IPv6 shutdown; unreadable Docker network inventory guards
  both families. IPv4 counting conservatively includes every Docker bridge.
- Guard IPv6 forwarding against expiring RA-learned routes when `accept_ra` is not 2.
- Read FRR BGP summaries, families and peers when `vtysh` is available; BGP configuration is read-only.

### Firewall — `/network/firewall`

- Detect ufw, firewalld or raw iptables; show backend capabilities and a reason for read-only controls.
- Read rules, policies, enabled state, logging level and supported service/application profiles.
- With a writable backend: add, replace and delete rules; protocol, port/range, source, interface,
  action and comment controls as supported by that backend.
- Enable/disable the firewall, change default policies and logging, and use service-aware presets.
- ufw ordering and rich-rule/service/zone handling for firewalld; backend differences remain explicit.
- Rule replacement installs the replacement before removing the old rule.
- Guards against banning the current operator; ordinary confirmation for destructive changes.
- Firewall logs and insights through the shared log viewer, including blocked/rate-limited sources.
- Raw iptables inspection remains read-only because this editor does not own its boot persistence.

### Gateway — `/network/gateway`

- Read/edit/delete owned DNAT forwards: name, TCP/UDP/both, incoming interface, port/range, destination
  address/port, allowed source ranges, enabled state and automatic/always/never source NAT.
- IPv4 and IPv6 destinations; forwards match destination addresses local to this host.
- Masquerade or fixed-address SNAT for an owned source network and outgoing interface, including VPN exits.
- Per-entry counters and translated-flow diagrams.
- Require forwarding for the corresponding address family before enabling a translation.
- Admit translated connections through compatible ufw/Docker iptables chains using a reserved
  connection mark, including `DOCKER-USER`; restore admission at boot and remove it when unused.
- Report firewalld/custom nftables forwarding-chain conflicts rather than claiming a translation is
  reachable through a firewall that still drops it.
- Handoffs to reverse proxy, TCP/UDP streams, load balancing and TLS in Proxy & TLS.

### Protection — `/network/protection`

- Per-port TCP/UDP rate limits and maximum concurrent connections, globally or per source, with
  rate interval, burst, drop/reject action and enabled state.
- Forwarded-flow limits apply to translated traffic, rather than every transit packet.
- Manual IP/CIDR blocklists; country IPv4/IPv6 lists; Spamhaus DROP, FireHOL level 1 and custom HTTPS feeds.
- Refresh on demand and daily; show entries, status/errors, fetch time and drop counters.
- Bounded HTTPS fetches, redirect validation, parsing/merging, broad-feed size guards and exclusion of
  private/reserved/multicast networks; retained cache and failed-change restoration.
- Trusted loopback, sufficiently narrow dashboard allowlist ranges and saved operator addresses.
- Refuse a manual list covering the operator; report fetched lists containing the operator.
- Established/related flows and trusted traffic survive blocklist application.
- Conntrack count, maximum and pressure readings.
- Fifteen editable kernel protections: SYN cookies; loose reverse-path filtering for existing/new
  interfaces; IPv4/IPv6 ICMP redirects; send redirects; IPv4/IPv6 source routing; broadcast pings;
  bogus ICMP errors; RFC 1337 protection; SYN backlog; SYN-ACK retries; martian logging; conntrack maximum.
- Each setting has bounds, recommendation and explanation; weaken/reset controls use the destructive
  route budget. Settings persist in the managed sysctl drop-in.

### VPN — `/network/vpn`

- Read managed and handwritten WireGuard interfaces, state, addresses, listen port, peers, endpoint,
  handshake, allowed networks and transfer counters; handwritten tunnels remain read-only.
- Create a managed IPv4 WireGuard server with an available name, UDP port and subnet; generate keys,
  write a managed wg-quick file, enable its service and request the matching firewall opening.
- Automatic defaults or explicit interface name, private IPv4 /16–/29 subnet, endpoint, client DNS
  and MTU. Client DNS defaults to Cloudflare `1.1.1.1`/`1.0.0.1` when none is chosen.
  Name, MTU and arbitrary client DNS are API-only; the creation form has four resolver presets.
- Bring managed tunnels up/down, remove them, and enable/disable IPv4 exit NAT via Gateway.
- Add device peers or site-to-site peers with remote networks, split/full tunnel settings and
  keepalive. Device DNS and client server endpoint inherit tunnel settings; a site's peer endpoint
  identifies the remote WireGuard transport.
- Generate private/preshared keys, downloadable client configuration and QR image; seal saved client
  configurations in the dashboard store; reopen or forget a saved client configuration.
- Remove managed peers and their explicit routes; check operator path for site routing changes.
- Refuse site routes capturing known literal transport endpoints of this or another WireGuard tunnel.
  Dynamic hostname/roaming-endpoint changes still need monitoring beyond this pre-apply check.
- Removed server configurations are archived, preserving their server key; report firewall cleanup
  results and failures rather than silently discarding them.
- Full tunnel has IPv4 egress. Routing client IPv6 into the tunnel prevents a native IPv6 leak, but
  the server creator does not configure IPv6 tunnel addressing/egress. This is stated in the UI/config.
- Read Tailscale identity, addresses, backend state, peers, routes and exit-node advertisements.
- Direct/DERP relay transport, current endpoint, transfer counters, last-seen/handshake/expiry and
  health/warnings. API preferences also inspect foreign exit use, accept-routes, DNS and shields-up
  without offering those potentially disconnecting mutations.
- Offer/withdraw this server as an exit node and advertise/withdraw IPv4/IPv6 subnets through
  `tailscale set`; preserve the other offer and serialize concurrent changes.
- Authorize route/exit withdrawals against the same current preferences while holding the mutation
  lock; another dashboard request cannot change the authorization classification between read/write.
  External CLI/control-server writers are outside this dashboard lock.
- Show Headscale users/nodes for supported native/container installations, and the route-approval handoff.
- Tailnet/control-server approval and ACL/grant policy still govern access.

### DNS — `/network/dns`

- Inspect host `resolv.conf`, symlink/mode, nameservers and search domains.
- Inspect systemd-resolved global/per-link servers, domains, selected server, DNSSEC, DNS over TLS,
  cache/statistics and host/container DNS listeners.
- Detect existing AdGuard Home/Pi-hole installations and link to their own management interfaces.
- Six upstream presets: Cloudflare, Google, Quad9, AdGuard, Cloudflare malware filtering and Mullvad ad blocking.
- Set/reset systemd-resolved global upstreams, including custom ports and scoped IPv6 link-local
  resolvers, fallback servers, DNS-over-TLS modes, DNSSEC modes, domains and cache settings.
- Nonempty managed resolver lists replace preceding lists. Empty lists inherit host defaults; explicit
  clearing of host-global lists is not supported. The forms expose fallback servers and cache mode,
  preserving existing managed values when other fields change.
- Set restarts/tests resolution and attempts restoration after failure with a fresh rollback context;
  failed recovery is explicit. Reset restores on restart failure, but keeps the managed file removed
  if default DNS fails verification after a successful restart, returning `verified: false`.
- Optional private verification name for an isolated LAN, including AAAA-only names; verify through
  resolved when its stub is disabled. Refuse writes without a readable rollback baseline.
- Inspect and edit the managed `/etc/hosts` block, with IPv4/IPv6 addresses and hostname aliases;
  preserve records outside that block and refuse malformed markers.
- Compare A, AAAA, CNAME, MX, TXT, NS, PTR and SRV answers/latency from up to sixteen resolver targets,
  including the stub and configured global/fallback/per-link servers, then optional public presets.
  Excess targets are omitted. This compares scopes rather than following split-DNS routing;
  an already-configured public server can receive a private name.
- Public preset comparison is explicit opt-in; retain resolver port and link-local scope instead of
  merging different LAN interfaces' identically named link-local resolvers. Direct wire queries bypass
  hosts-file/NSS contamination, validate replies/answer ownership/canonical chains and retry UDP truncation over TCP.

### Traffic and shaping — `/network/traffic`

- Interface live rates at two-second grain and a fifteen-minute in-memory ring.
- Retained per-interface traffic history: mean rate, peak rate, errors/drops; API windows up to 31 days
  with 2–1000 requested points, and standard UI windows of 1h/6h/24h/7d.
- Container traffic derived from recorded Docker samples, without double-counting host veth history;
  future samples are excluded and stale observations do not appear as a current rate. Inclusive
  window ends still honor the promised interface/container point counts.
- Per-program TCP traffic from `ss` per-socket byte-counter deltas, with warm-up state and limitations;
  retain at most 4,000 sockets per read and show at most five remote peers per program.
- Interface upload/download shaping: fq_codel, cake or fq; CAKE bandwidth or HTB with fq/fq_codel upload ceilings and ingress policing.
- BBR/default-fq setting and readback of available congestion control.
- Keep minimum bandwidth on protected paths; preserve `clsact`/attached tc-BPF state.
- Read loaded eBPF programs and their XDP/tc attachments through `bpftool`; no dashboard probe is loaded.
  Type counts/filtering, names, owner, load time, memory and optional run counts appear in the UI.
  The API also provides tags, UID, translated/JIT sizes, map IDs, pins and optional runtime; the
  listed programs are capped at 1,000.
- Report failed historical/BGP/namespace reads as errors with recovery, rather than empty/forever-loading data.

### Connections — `/network/connections`

- Group active sockets by remote peer, with local destinations, protocol/state and socket counts.
- Address/network origin labels and connection topology.
- Search/filter and handoff a selected address to a diagnostic without automatically running a probe.

### Tools — `/network/tools`: all 26 server diagnostics

| Key | Tool | Actual operation |
| --- | --- | --- |
| `dns` | DNS | Host resolver A/AAAA/CNAME/MX/TXT/NS/PTR lookup. |
| `dnsauth` | DNS authority | NS ownership and nameserver addresses. |
| `ping` | Ping | Bounded ICMP echo from this server. |
| `traceroute` | Traceroute | Bounded hop discovery, tracepath fallback; IPv6 literals select IPv6. |
| `route` | Route lookup | Kernel route/source/interface/gateway selection for an IPv4/IPv6 literal, without sending traffic. |
| `mtu` | Path MTU | Bounded tracepath diagnostic; missing/filtered ICMP remains visible. |
| `port` | Port check | TCP connect to one port, with elapsed time and failure classification. |
| `scan` | Port scan | Bounded scan of common TCP catalogue ports; this is not a UDP or all-port scan. |
| `banner` | Banner grab | Read a bounded service greeting. |
| `ssh` | SSH keys | Read SSH host keys/fingerprints; this does not establish out-of-band trust. |
| `http` | HTTP | Status, redirects and selected headers, with explicit certificate-verification caveat. |
| `httpsec` | Header grade | Report browser security headers. |
| `tls` | TLS certificate | Presented chain, names, expiry, negotiation and trust verdict. |
| `tlssurvey` | TLS versions | Probe supported TLS versions. |
| `siteaudit` | Site audit | Combined HTTP, certificate and header inspection. |
| `mx` | Mail path | MX/SPF/DMARC and SMTP reachability. |
| `starttls` | STARTTLS | SMTP/IMAP/POP3/FTP upgrade and certificate inspection. |
| `dnsbl` | Blocklists | Address reputation in supported DNS blocklists. |
| `asn` | Ownership | Autonomous system/prefix/registry/country lookup. |
| `whois` | Whois | Domain/address registration information. |
| `listeners` | Listeners | TCP **and UDP** listener addresses/processes via ss/netstat. |
| `egress` | Egress | Independent IPv4/IPv6 kernel route/source/default inspection; works on IPv6-only hosts. |
| `neigh` | Neighbours | Known ARP/NDP cache; no active LAN sweep. |
| `capabilities` | Host support | Installed host tools, IPv6 state, service-manager reachability and external constraints. |
| `capture` | Packet snapshot | Bounded packet summaries on one interface/protocol, at most 50 packets/15 seconds; no file, promiscuous mode or explicit hex/ASCII dump. Decoded fields can be sensitive. |
| `wol` | Wake-on-LAN | One magic Ethernet frame on a chosen local broadcast interface; confirms send, not successful wake. |

- The separate browser-local subnet calculator supports exact IPv4/IPv6 arithmetic, including
  IPv4 /31 and /32 (no broadcast) and IPv6 /127 and /128, network bounds and address counts.
- Searchable tool chooser, independent drafts/results/history/in-flight requests, deep-link prefills
  that never execute automatically, and validated ports/MAC/interface/protocol inputs.
- All `/network/probe` tools require `system.admin`; results are plain escaped text, target/protocol is audited,
  process output is drained with a 256 KiB memory cap and commands have time bounds, including
  bounded process-group cleanup for host-wrapper descendants.
- Outward tests establish reachability **from this server**. They cannot prove public inbound
  reachability. A timeout cannot establish that a firewall caused it; a refusal can also come from
  a firewall rejection rather than a closed port.

## Related networking features outside the section

- **Proxy & TLS:** native nginx reverse-proxy sites, upstream pools/load balancing, path routing,
  HTTP/WebSocket proxying, TCP/UDP streams, access lists, TLS/certbot/DNS challenges, certificate
  inventory/renewal and trust diagnostics, endpoint watches, traffic/logs, listener inventory and
  free-port discovery. These remain owned by Proxy & TLS; see its
  [current implementation guide](../../internal/backend/databases-proxy-platform.md).
- **Docker:** network inventory/create/remove, driver/IPAM/subnet/gateway configuration as supported
  by Docker, container network attachments/disconnections and port publications. The Network topology
  joins these resources rather than taking ownership of them.
- **Security:** host exposure/posture, SSH effective configuration and jump-host profile, fail2ban
  jail tuning/bans/allowlist/logs, CrowdSec decisions/alerts/bouncers and guarded manual ban/release,
  Suricata IDS/IPS state/recent bounded alerts/rule counts, login history and SSH-session handling.
  CrowdSec needs an enforcing bouncer; reading Suricata alerts does not configure an IPS.
- The network allowlist, authentication/2FA, Caddy ingress and deployment/preview network boundaries
  remain governed by the [security invariants](../../internal/security/invariants.md).

## API inventory and authorization

The [network implementation guide](../../internal/backend/network.md#routes) lists every mounted
route family under `/api/v1/network`. The frontend calls the corresponding routes for its forms/readings;
API-only fields are identified above. Host support uses `POST /probe`; `GET /capabilities` is also
available to API readers.
This change adds `GET /capabilities`, five cases on `POST /probe`, IPv6 policy-rule support and DNS
lookup/private-verification options; it does not replace existing routes.

Network pages also use these established routes under `/api/v1` outside the `/network` prefix:

| Page/context | Mounted methods |
| --- | --- |
| Firewall | `GET /firewall`, `GET /firewall/apps`, `POST /firewall/rules`, `PUT /firewall/rules/{number}`, `DELETE /firewall/rules/{number}`, `POST /firewall/enabled`, `POST /firewall/policy`, `POST /firewall/logging`, `POST /firewall/reset` |
| Connections | `GET /connections`; blocking delegates to the guarded firewall rules API. |
| Shared posture | `GET /exposure`, `GET /security/posture`, `GET /security/services`; listeners and logs use their existing Proxy/Logs owners. |

General inventory reads require `read`. The restricted `/dns/lookup` API also requires `read`; its
current UI lookup control requires `system.admin`, while the API permits the configured/preset-resolver
comparison to readers. VPN, per-process traffic and server probes require
`system.admin`. Every network mutation requires `system.admin`; destructive operations additionally
use `s.destructive` or the existing content-dependent gate, audit and destructive limiter. IPv6
rules retain the same ownership/path guards as IPv4. No typed-confirmation policy was widened.

## Fixed findings

The code-level findings and regression evidence are in [findings.md](findings.md). All reported fixed
items are implemented in this change. Remaining design/hardware/provider gaps below are not labelled
fixed simply because a detection message or an external handoff exists.

## Competitor research and decisions

| Primary source | Relevant capabilities | Decision for this change |
| --- | --- | --- |
| [Cockpit feature internals](https://docs.cockpit-project.org/cockpit-guide/latest/guide/features.html) | Native NetworkManager/systemd/firewalld integration and permission-aware host management. | Fix permission affordances; preserve external network-manager ownership. Bond/Wi-Fi/DHCP profile editing needs native manager integration. |
| [NetworkManager settings](https://www.networkmanager.dev/docs/api/latest/ch01.html) | Bonds, VRFs, Wi-Fi, ipvlan, MACsec, OVS, WireGuard and other connection profiles. | Record these as unimplemented domains, rather than expose creation controls with incomplete ownership/recovery. |
| [OPNsense diagnostics](https://docs.opnsense.org/manual/diagnostics_interfaces.html) | ARP/NDP, DNS, packet capture, ping, port probes and traceroute. | Retain neighbour inspection and add route/MTU diagnostics and bounded packet summaries. Full capture jobs/PCAP download remain a gap. |
| [Webmin network configuration](https://webmin.com/docs/modules/network-configuration/) | Native interface/routing and resolver configuration. | Preserve manager-owned profiles; add bridge and IPv6 controls only where the API already provides guarded lifecycle. |
| [OpenWrt SQM](https://openwrt.org/docs/guide-user/network/traffic-shaping/sqm_configuration) | Queue management for home WAN bottlenecks. | Describe policing versus scheduled ingress; IFB/CAKE download shaping remains an implementation gap. |
| [Linux ethtool interface](https://docs.kernel.org/networking/ethtool-netlink.html) | Wake modes and capabilities depend on the NIC/driver. | Add local magic-packet sending and state the recipient firmware/NIC requirement. |
| [iputils tracepath](https://github.com/iputils/iputils/blob/master/doc/tracepath.xml) | Path-MTU discovery with IPv4/IPv6 and bounded hops. | Use the host's tracepath rather than claim an ICMP timeout proves a particular MTU. |
| [AWS VPC addressing](https://docs.aws.amazon.com/vpc/latest/userguide/vpc-ip-addressing.html) | A provider public IPv4 address can map to a private NIC address through NAT. | Egress reports kernel routing/source facts; it does not equate the NIC address with the public internet address. |
| [Linux IP sysctls](https://docs.kernel.org/networking/ip-sysctl.html) | Forwarding changes IPv4 host settings and IPv6 RA behaviour. | Preserve managed protections after forwarding changes and retain RA lockout checks. |

This comparison identifies useful feature families; it is not a claim of feature parity with any
competitor. OPNsense is a firewall/router OS with different service and platform ownership.

## Remaining limits and missing feature families

| Area | Actual boundary | What full support would require |
| --- | --- | --- |
| Linux/VPS variants | Linux host namespaces, root-equivalent privileges, suitable kernel features and host tools. | Per-provider and per-kernel acceptance; restricted containers/OpenVZ guests can prohibit networking despite root. |
| Upstream reachability | Provider firewalls/security groups, source/destination checks, assigned IP/MAC limits, CGNAT and home-router mappings are outside this host. | Separate authenticated provider/router integrations and hardware-specific acceptance. No host code can unlock provider restrictions. |
| Persistence | Managed boot restoration uses systemd. Runtime edits of external objects are not made persistent. | Native OpenRC/runit/init adapters and ownership-aware NetworkManager/networkd/netplan configuration. Recovery of file/kernel changes is best-effort when host commands or storage fail; multi-file commits are not crash-atomic. |
| WireGuard IPv6 | Managed server/client address allocation and exit NAT are IPv4; IPv6 full-tunnel traffic is contained to prevent leaks. | Dual-stack allocation, peer validation, route/forwarding/NAT or routed-prefix design and rollback/live tests. |
| Tailscale | Local exit/subnet advertisements only; no down/logout/shields-up/foreign-exit controls that can remove operator access. | A tested connectivity transaction and recovery channel before expanding actions; tailnet policy/approval remains external. |
| Gateway/firewalld | Translation writes cannot override a foreign nftables chain that drops them. | Native owner-approved firewalld policy integration or an explicit operator admission rule. |
| Docker dependency detection | IPv6 bridges use flag/subnet evidence; IPv4 conservatively counts every bridge. An unreadable inventory blocks forwarding shutdown for both families. | Reliable per-family Docker IPAM/EnableIPv4 metadata before permitting narrower shutdown decisions. |
| Address provisioning | Provider DHCP, Wi-Fi credentials, static manager profiles and physical uplink replacement remain external. | Network-manager APIs, staged confirmation and automatic recovery across network restarts. |
| Bonds/VRFs/ipvlan | Read existing devices; do not provide complete creation/member/policy lifecycle. ipvlan can change a parent mode shared by slaves in other namespaces. | Full namespace/manager ownership and dependency-aware lifecycle. See [kernel ipvlan implementation](https://github.com/torvalds/linux/blob/master/drivers/net/ipvlan/ipvlan_main.c). |
| Home-router services | No native DHCP server/leases/reservations, RA/DHCPv6 prefix delegation, PPPoE, Wi-Fi AP, UPnP/NAT-PMP or captive portal. | Service configuration, ownership/conflict detection, persistent leases/secrets and safe ingress migration. |
| DNS comparison | Classic UDP/TCP queries; TLS metadata does not enable DoT/DoH or DNSSEC validation, and a TLS-only custom port can fail. Public presets use IPv4 endpoints. Up to sixteen targets are compared without split-DNS domain selection; excess targets are omitted. | Encrypted diagnostic transports, IPv6 preset selection, DNSSEC validation and explicit interface binding for every upstream syntax. |
| Resolver server | Host resolver settings and existing Pi-hole/AdGuard handoffs; no authoritative DNS, zone editor, DHCP-integrated DNS or native adblock service provisioning. Empty managed lists inherit defaults and cannot explicitly clear host-global lists. | Separate service adapters and lifecycle; resolver race is a DNS diagnostic, not proof of encrypted transport. |
| Routing daemons | FRR BGP is read-only; no BGP/OSPF/IS-IS/RIP editor or multi-WAN failover orchestration. | Native daemon configuration validation, transactions and protocol-specific acceptance. |
| Advanced policy routing | Managed routes use one next hop; rules expose prefix/interface/mark selectors and lookup/discard actions. No ECMP, VRF policy or UID/TOS/goto-rule editor. | Expanded typed kernel/spec contracts, dependency/path guards and per-family replay/live tests. |
| Other VPNs | No OpenVPN, IPsec, GRE encryption or VPN client-import orchestration. | Dedicated validated protocol/lifecycle adapters; GRE/VXLAN are not encryption. |
| Packet capture | Summary snapshot only; decoded fields can contain sensitive data. No PCAP artifacts, explicit payload dumps or arbitrary BPF expressions, capture job history or indefinite capture. | Bounded job/artifact lifecycle, storage/retention and access controls. |
| Traffic accounting | Per-process deltas are TCP only; closed/short-lived sockets can be missed; reads retain 4,000 sockets/five peers per program. Retention limits history; eBPF lists 1,000 programs. Ingress policing drops rather than schedules download packets. | Purpose-built kernel accounting, UDP/short-flow attribution and managed IFB/CAKE shaping. |
| Security inspection | eBPF, Headscale and Suricata are inventory/inspection; CrowdSec enforcement needs an existing bouncer. | Separate native configuration and update adapters; application-layer/DDoS protection also needs upstream capacity. |
| Network discovery | Neighbour cache only; no subnet-wide scan, LLDP/SNMP inventory or automatic topology of external switches. | Bounded authenticated discovery with explicit target scopes and device adapters. |
| Hardware | Negotiated speed and Wake-on-LAN depend on NIC/driver/firmware; a VPS virtual NIC often has no meaningful physical controls. | Device-specific ethtool/Wi-Fi adapters and supported hardware testing. |
| Router resilience | No HA/VRRP/CARP, state synchronization, policy-based health failover or distributed configuration. | Multi-host control, elections/state synchronization and provider multicast/floating-IP integration. |

## Verification

Final integrated validation and any environment constraints are recorded in [verification.md](verification.md).
Parser/renderer/rollback tests do not establish that every provider, kernel, NIC, distribution or
upstream firewall has been tested. Network live tests use disposable namespaces; no production
interface, firewall, tunnel or resolver was changed to run this audit.
