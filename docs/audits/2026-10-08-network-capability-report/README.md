# Networking capability, quality and competitor report

Reviewed **8 October 2026**, against `patch/0.7.1` at **`7be11ba0`**. This is a research report;
it does not implement the proposed changes. The relevant merged changes are
[PR #169: Network section](https://github.com/JustDashboard/Just-Dashboard/pull/169) and
[PR #171: networking audit and recovery hardening](https://github.com/JustDashboard/Just-Dashboard/pull/171).

Implementation is now underway in this same PR. The [implementation ledger](implementation-status.md)
preserves every finding and proposed capability, records current evidence and identifies unfinished
acceptance. The assessment below describes the reviewed base; its historical scores are unchanged.

## The assessment

**Just Dashboard now has a substantial Linux networking control plane. My overall assessment is
7/10 for the current single-server networking experience.** Its strongest qualities are the breadth
of real host controls, ownership awareness, protection of the operator's route, and the connection
between networking, Docker, services and reverse proxies. The largest weakness is that many pages
show or change an individual component without proving that the complete intended connection works.

The next investment should make the existing controls dependable and explanatory: confirmed apply
with independent recovery, accurate applied-state reporting, a connection-path investigator, durable
diagnostic evidence, better flow attribution and complete dual-stack workflows. Another twenty
command wrappers would add less value than those improvements.

The ambition can be much larger: a dashboard where an operator says “this app may reach these
services through this tunnel,” sees the exact consequences, applies a reviewed plan, and gets a
measured answer about whether it worked. Multi-server networking can become a future edition, while
the current single-server product remains useful on its own.

## Scope, scores and evidence

“Every feature” here means the shipped networking features, including relevant capabilities in
Docker, Proxy & TLS, Security, metrics and dashboard configuration. It does not mean an inventory
of unrelated databases, backups, Git or file-management functions. The inventory is granular by
operator capability; closely related configuration options share a row.

The **135 scored feature/workflow entries** are also available as a [sortable CSV](features.csv),
with the same scores and implementation notes.

Scores are **reviewer judgments about implementation maturity**, not benchmark results or a security
certification. I consider correctness, failure/recovery behavior, persistence and compatibility,
visibility of the actual outcome, operator usability, and acceptance evidence. A small read-only
feature can score highly for doing its stated job; that does not give it the capabilities of a full
management product. Missing capabilities are listed separately rather than averaging zeroes into
working features.

| Score | Interpretation |
| --- | --- |
| 9–10 | Complete for its stated scope, with strong recovery and acceptance evidence. A 10 needs unusually comprehensive operational proof. |
| 7–8 | Useful and substantially implemented; identifiable limitations or missing operational evidence remain. |
| 5–6 | Works in a narrower setting or offers partial inspection; important workflows, compatibility or verification are incomplete. |
| 3–4 | Thin surface or significant operational ambiguity. |
| 1–2 | Stub or unusable for the intended task. |
| Not shipped | An addition, not a rating of an implementation that exists. |

**Evidence:** source and test inspection across `internal/netx`, `internal/netsec`, their API handlers,
the eleven Network pages, shared components and adjacent networking owners. The
[previous networking audit](../2026-10-08-network-audit/README.md) is useful historical evidence,
but the findings below were checked against the current source. Competitor claims use official
documentation fetched during this review; feature availability can vary by edition and deployment.

Fresh verification: `go test ./internal/netx ./internal/netsec` passed with Go **1.26.8** (`netx`
2.095 s; `netsec` 0.489 s). These are package tests, including fixtures and fakes; passing them does
not prove live host networking, provider compatibility or browser usability. No live namespace,
reboot, fault-injection, throughput or browser acceptance run was performed for this report. Prior
live-test evidence is attributed to the previous audit, not presented as a new run.

The UX review used the `ui-ux-pro-max` skill's focused error-recovery guidance. Recommendations
were checked against the repository's design system; this request does not redesign any page.

## Category assessment

| Area | Maturity /10 | Main reason it is not a 9 |
| --- | --- | --- |
| Ownership, guarded changes and persistence | 7.5 | Good synchronous recovery; no independent confirmed-apply watchdog or crash recovery journal. |
| Interfaces and namespaces | 7 | Strong managed virtual devices; no native physical-interface connection profiles. |
| Routes and forwarding | 7 | Real dual-stack rules; incomplete visualization and no active gateway failover. |
| Host firewall | 6.5 | Useful UFW/firewalld adapters; incomplete effective multi-zone/multi-manager policy. |
| Port forwarding and NAT | 7 | Real translation, marks and counters; admission health is narrower than the paths it changes. |
| Blocklists and kernel protections | 7 | Good bounded controls; cached and applied blocklist health can diverge from displayed metadata. |
| WireGuard and mesh integration | 7 | Useful provisioning and secret handling; IPv4 egress, limited mesh policy administration. |
| Resolver and DNS tools | 6.5 | Good host resolver controls; no complete DNS service, and lookup comparison is not split-DNS aware. |
| Traffic accounting and shaping | 6 | Useful interface/container rates; sampled TCP attribution and ingress policing limit depth. |
| Diagnostics | 7 | Broad bounded tools; little structured interpretation or durable run lifecycle. |
| Connections and workload handoffs | 7.5 | Valuable resource joins; no historical full-path/flow investigator. |
| Cross-page operator workflows | 7 | Strong page breadth; outcome recipes, change plans and sustained verification are missing. |

The overall 7/10 is a rounded product judgment, not an arithmetic promise that all hosts behave
equally. An Ubuntu host with UFW, systemd-resolved and the required tools has a more complete
experience than a host with firewalld, another resolver, restricted kernel modules or an upstream
provider firewall that the dashboard cannot see.

## Complete current feature map

### Ownership, permissions, persistence and recovery

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| Managed network spec and boot restoration | 7 | Deterministic link/rule/shaping/nft/sysctl renders and ordered systemd restore. Add generations, recovery journal and verified last-boot results; multi-file persistence is not crash-atomic. |
| Synchronous rollback/canceled-request recovery | 8 | Snapshots old files, writes spec last, uses an independent bounded context and terminates host-command descendants. Collect and expose every failed undo step. |
| Operator/client-path protection | 8 | Kernel reply-path checks, ordinary/Tailscale-marked/IPv6 anchors and recognizable SSH loopback-tunnel attribution. Add independent timed confirmation and application/transport verification. |
| Resource ownership/dependency protection | 9 | Managed/kernel/provider/Docker/Tailscale distinction; protected parents, bridges, addresses, peers and dependent rules. Add observed-state drift and adoption plans. |
| Backend capabilities, destructive gates, audit and secrets | 9 | Admin mutation gates, restricted VPN/process data and diagnostics, audited no-store secret exports. Extend role precision without weakening backend authorization. |
| Host capability/package detection | 7 | Tool/package hints, systemd reachability and IPv6 state. Add functional feature probes and kernel/NIC/provider constraints beside each operation. |

Sources: [commit and boot unit](../../../backend/internal/netx/persist.go),
[client path](../../../backend/internal/netx/path.go),
[operator attribution](../../../backend/internal/netx/operator.go),
[capabilities](../../../backend/internal/netx/capabilities.go),
[API ownership and authorization](../../../backend/internal/api/handlers_network.go),
[VPN API](../../../backend/internal/api/handlers_network_vpn.go).

### Overview and topology

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| Network identity and current path | 8 | Addresses, uplink, gateway, client path and persistence context. Show both forwarding families and distinguish NIC source from provider public/NAT identity. |
| Host/Docker/tunnel topology | 8 | Clickable owned-resource diagram and live traffic wires. Add flow-backed edges and effective paths; this is not external-switch discovery. |
| Live upload/download totals and trends | 8 | Two-second figures and fifteen-minute ring. Expose observation age and drill from a chart into its underlying resource. |
| Network attention findings and handoffs | 8 | Actionable destinations for local issues and capacity. Add incident history/correlation and distinguish partial failed observations from absence. |
| Retained-data warning and retry | 8 | Main page reads preserve data with a stale/retry warning. Apply the same contract to subsidiary polls and per-namespace/per-family reads. |

Sources: [overview backend](../../../backend/internal/netx/overview.go),
[Network overview](../../../frontend/src/app/%28dashboard%29/network/page.tsx),
[topology](../../../frontend/src/components/network/topology.tsx),
[read warning](../../../frontend/src/components/network/read-warning.tsx).

### Interfaces and namespaces

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| Rich interface inventory/telemetry | 8 | Kind/role/owner, state, addresses, MAC, MTU, parent/master, queue, speed and rates. Add driver/offload/link-error detail where meaningful. |
| Docker bridge/container-veth correlation | 8 | Engine inventory/PID/iflink joins avoid repeated inspection subprocesses. Explicitly flag incomplete/unreadable Docker joins. |
| Bridge creation and membership | 8 | Persistent owned bridges, STP and guarded membership. Add VLAN filtering/FDB/snooping and combined migration previews. |
| VLAN creation | 8 | Parent/ID/name/address validation and persistence. Add manager-backed profiles and tagged/untagged policy workflow. |
| VXLAN creation | 7 | VNI, endpoints/parent/group/UDP configuration with validation. Multicast group configuration is API-only. Add underlay readiness, FDB/remote-set management and later EVPN. |
| GRE/GRETAP and IPv6 GRE/GRETAP | 7 | Real typed family/endpoint creation, key/TTL support in API and persistence. Add missing advanced UI fields and endpoint/liveness checks; these tunnels are not encrypted. |
| Dummy interfaces | 8 | Typed creation, addressing, state and owned removal. Join them into routing/service recipes rather than adding unrelated complexity. |
| Macvlan modes | 7 | Bridge/private/vepa/passthru, including guarded passthru. Explain host isolation and provider MAC restrictions; verify supported connectivity. |
| Veth pairs into managed namespaces | 8 | Validated names/namespace ownership, peer lifecycle and dependency checks. Add namespace-specific inspection and target handoffs. |
| Device up/down, MTU and bridge edits | 7 | Strong path/address/parent guards; foreign-object edits report runtime-only persistence. Add native owner profile editing and independent recovery. |
| IPv4/IPv6 address addition/removal | 8 | Managed ownership, address validation and client-source protection. No complete DHCP/SLAAC/static-uplink provisioning transaction. |
| Named/container namespace inventory/lifecycle | 7 | Create/remove and bounded container reads with partial-failure recovery. Expose per-item failures and add namespace routes/DNS/tools rather than showing failed reads as empty. |

Sources: [interface read model](../../../backend/internal/netx/links.go),
[device apply/guards](../../../backend/internal/netx/links_apply.go),
[namespaces](../../../backend/internal/netx/namespaces.go),
[device UI](../../../frontend/src/components/network/interfaces/device-sheet.tsx),
[creation UI](../../../frontend/src/components/network/interfaces/create-device.tsx).

### Routing and forwarding

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| All-table IPv4/IPv6 route inventory | 8 | Table names, owners, metrics, preferred sources and multipath inspection; local table is counted separately. Add event/history and target filtering. |
| Managed unicast routes | 8 | Persistent family/table/device/gateway/source/metric validation and guarded apply. Managed routes have one next hop; add ECMP and edit plans later. |
| Blackhole/unreachable/prohibit routes | 8 | Typed discard routes and guarded deletion. Preview the destinations/services affected before applying. |
| IPv4/IPv6 policy rules | 8 | Prefix/interface/mark selectors, lookup/discard, family-aware add/delete/rollback/boot. Add effective-match preview and supported UID/TOS/goto/VRF extensions. |
| Priority allocation/protected tables | 9 | Checks live and owned priorities; reserved managed range and kernel/Tailscale table protections. Keep this safety when adding richer rules. |
| Post-change route validation | 8 | Reply, source-selected reply and internet-anchor checks with rollback. Finite anchors do not prove every app or hostname/roaming VPN endpoint. |
| IPv4/IPv6 forwarding controls | 8 | Docker/NAT/VPN dependencies, fail-closed unreadable inventory, RA and protection-reset handling. Explain conservative Docker IPv4 counts and add measured forwarding health. |
| FRR BGP summary/peers **(inspection)** | 7 | Family/state/prefix/message/uptime parsing, including older output. No BGP policy configuration, route browser, OSPF or dynamic failover controller. |
| Routing decision diagram | 5 | Omits IPv6 policy rules and infers the highlighted client rule. This limits the visualization; backend IPv6 policy rules are implemented. |

Sources: [routes/rules](../../../backend/internal/netx/routes.go),
[forwarding](../../../backend/internal/netx/forwarding.go),
[BGP](../../../backend/internal/netx/bgp.go),
[routing visualization](../../../frontend/src/components/network/routing/decision-map.tsx).

### Host firewall

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| UFW/firewalld detection and capability-aware inspection | 7 | Editable features and read-only reasons are explicit. Select by actual owner/activity as well as binary presence, and inspect effective interface zones. |
| Firewall rule add/edit/delete and placement | 8 | Protocol/source/ports, profiles, comments and ordered UFW placement with guards; destination-address matching is API-only. Add grouped plans, shadowing analysis and stable rule identity. |
| Firewall enable/disable | 7 | Existing service-aware control and destructive gating. Add staged verification and timed recovery for loss of access. |
| Inbound/outbound/routed defaults | 7 | UI exposes inbound and UFW outbound defaults; routed defaults use the supported backend API. Show effective per-interface/per-family policy and preserved access. |
| Logging/reset and inbound diagram | 7 | Capability-aware logging/reset, summaries and interpretable logs. Add per-rule history; firewalld lacks the UFW-style reset operation. |
| Raw iptables **(inspection)** | 6 | Deliberately read-only without a supported persistent manager. Add a scoped native nftables owner adapter; do not silently take over unrelated rules. |

Sources: [firewall abstraction](../../../backend/internal/netsec/firewall.go),
[UFW](../../../backend/internal/netsec/firewall_ufw.go),
[firewalld](../../../backend/internal/netsec/firewall_firewalld.go),
[iptables](../../../backend/internal/netsec/firewall_iptables.go),
[firewall UI](../../../frontend/src/components/network/firewall-panel.tsx).

### Gateway, port forwarding and NAT

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| TCP/UDP/both DNAT forwards | 8 | Port/range, IPv4/IPv6 target, arrival interface and local-destination match. Verify from an external source; no health-driven target failover. |
| Masquerade/fixed-address SNAT | 8 | Source-network/outgoing-device translation and tunnel-owned exits. No one-to-one NAT/NPTv6 or advanced translation-policy workflow. |
| Forward source restrictions and enable/disable/edit/delete | 8 | Validated ranges and owned lifecycle; managed VPN entries have proper handoffs. Add overlap/conflict and impact preview. |
| Automatic forward source-NAT decision | 7 | Uses the host's routing to choose and saves a deterministic choice. Reevaluate drift caused by external topology changes, not only gateway edits. |
| Admission through UFW/Docker and foreign conflict checks | 7 | Reserved connmark admission, Tailscale mark separation, boot/mutation reassertion and known incompatibility refusal. Add supported explicit-rule inspection and per-layer uncertainty. |
| Per-forward/NAT counters | 6 | Expanded rules aggregate by comment. Table replacement resets counts; persist deltas or stable counters and label generation/time. |
| Loaded/admission/forwarding readiness | 6 | Table presence plus basic admission/forwarding facts. Verify all required families/chains and distinguish policy installed from reachable. |

Sources: [gateway renderer](../../../backend/internal/netx/gateway.go),
[apply/readback](../../../backend/internal/netx/gateway_apply.go),
[compatibility](../../../backend/internal/netx/gateway_capability.go),
[forward UI](../../../frontend/src/components/network/gateway/forwards.tsx),
[NAT UI](../../../frontend/src/components/network/gateway/nat.tsx).

### Protection, blocklists and kernel settings

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| New-flow packet-rate limits, globally or per source | 8 | Protocol/rate/interval/burst/drop-or-reject and source meters match packets in conntrack's new state; established flows bypass them. Add saturation/impact graphs and service profiles. |
| Per-source maximum concurrent connections | 7 | Stateful source-keyed ceilings with translated-flow scoping. Packet-rate limits can be global or per-source, but the concurrent-connection ceiling is always per-source. Add capacity/impact evidence and an explicit global ceiling if needed. |
| Manual IP/CIDR blocklists | 9 | Validated merged bounded sets, protected operator and persistence. Add change-impact preview and expiring scoped exceptions. |
| IPv4/IPv6 country blocking | 7 | Aggregated feeds, status/counters and trusted bypass. Explain approximate geography, provenance and collateral impact. |
| Spamhaus/FireHOL/custom HTTPS feeds | 8 | Bounded secure fetch/redirect handling, reserved-range exclusions and overbroad-feed guards. Add feed diffs and adjustable/signed refresh where supported. |
| Feed cache/refresh lifecycle | 6 | Last-good cache and stale-fetch concurrency protection; daily refresh needs JD runtime. Actual missing-cache health can disagree with saved count/error. |
| Trusted operator/network exceptions | 8 | Loopback, bounded allowlist networks and saved operator addresses precede drops. Add owner/reason/expiry and revalidation of accumulated addresses. |
| Established-session protection | 8 | Established/related return avoids cutting current sessions/outbound replies when lists change. Add separate explicit session-revocation workflow; blocking a new connection does not terminate an existing one. |
| Fifteen bounded kernel protections | 8 | Closed settings, recommendations, bounds, readback, persistence and weakening gates; loose reverse-path filtering respects tunnels. Show per-interface effective values and workload profiles. |
| Conntrack occupancy | 7 | Count/max and local warning thresholds. Add time series, flow-state/source breakdown and evidence for the cause of pressure. |

Sources: [protection](../../../backend/internal/netx/protection.go),
[blocklists](../../../backend/internal/netx/blocklists.go),
[rules and counters](../../../backend/internal/netx/gateway.go),
[conntrack](../../../backend/internal/netx/conntrack.go),
[protection UI](../../../frontend/src/components/network/protection/kernel.tsx).

### VPN and mesh networks

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| WireGuard inventory/handshakes/transfers | 8 | Live/config join, down configured tunnels and foreign read-only ownership, no inventory secrets. Add trends, stale-handshake alerts and endpoint history. |
| One-step WireGuard server creation | 8 | Free name/UDP port/subnet, overlap validation, key/config/service creation and firewall outcome. Add dual-stack allocation, provider-public-endpoint evidence and advanced UI settings. |
| Key generation, sealed client config, QR/copy/download | 9 | Go keys/PSKs, sealed saved configs, restricted audited exports and correctly scaled QR. Add rotation/one-time expiring enrollment; forgetting a saved config is not revoking its peer. |
| Device peers and split/full client configuration | 8 | Address allocation, networks/keepalive, live sync and saved export. Add peer editing, expiry, groups and quotas. |
| Site-to-site peers and remote routes | 7 | Overlap/operator/literal endpoint guards and route cleanup. Add continuous protection for hostname/roaming transports and coordinated remote-site verification. |
| WireGuard internet exit | 6 | Owned IPv4 NAT/forwarding integration. Managed addressing and egress remain IPv4; add IPv6 egress and leak acceptance. |
| Full-tunnel IPv6 containment | 7 | Captures `::/0` and explains IPv4-only service to avoid native-path leaks. This is containment, not IPv6 internet access or a client kill switch. |
| Tunnel up/down/removal and archive preservation | 7 | Guarded lifecycle, archived server configuration and reported firewall cleanup. Add restore-from-archive and durable lifecycle/recovery history. |
| Tailscale self/peers/exit/subnet offers | 7 | Direct/DERP/health view and narrowly scoped locked advertisements. Policy/route approval remains external; failed frontend route submission loses its draft. |
| Headscale users/nodes **(inspection)** | 6 | Native/container detection; API exposes users/nodes, while UI shows node status/address/user. No enrollment, key, route-approval, user-lifecycle or policy-management workflow. |

Sources: [WireGuard inventory](../../../backend/internal/netx/wireguard.go),
[server/lifecycle](../../../backend/internal/netx/wgserver.go),
[peers/client text](../../../backend/internal/netx/wgpeers.go),
[sealed configurations](../../../backend/internal/netx/vpn_store.go),
[Tailscale](../../../backend/internal/netx/tailscale.go),
[Headscale](../../../backend/internal/netx/headscale.go).

### DNS and resolver configuration

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| Host resolver/resolv.conf/port-53 chain | 8 | Writer/symlink/global/per-link/listener/service inspection. Add a cross-distribution owner adapter rather than treating resolved as universal. |
| resolved per-link/scopes/statistics **(inspection)** | 7 | Servers/domains/cache/DNSSEC/DoT visibility. Writes remain global; add supported per-link split-DNS profiles via the native owner. |
| Presets/custom upstreams/fallback/domains/cache | 8 | Six presets, ports/scoped IPv6/TLS names and managed drop-in semantics. Empty lists inherit global defaults rather than explicitly clearing them. |
| Host DNS-over-TLS setting | 7 | Validated resolved mode and TLS names. Add actual transport/trust/fallback verification; no managed DoH/DoQ server is supplied. |
| Host DNSSEC mode/statistics | 7 | Configuration/support/result counters. Direct comparisons do not cryptographically validate signatures; add trust-chain diagnostic evidence. |
| Resolver apply/reset/private-name verification/recovery | 8 | Readable baseline, restart, A-or-AAAA/private verification and reported undo failure. One verified name cannot establish every relevant DNS scope; show the verification plan/results. |
| Managed hosts-file IPv4/IPv6/alias records | 9 | Marked ownership, foreign byte/mode/symlink preservation and malformed-marker refusal. Add duplicate/conflict preview and local-resolution evidence. |
| Multi-resolver answer/latency comparison | 6 | Eight record types, response/question/chain checks, truncated-UDP TCP retry, public preset opt-in and bounded fan-out. Add split-DNS-safe default, encrypted transport/DNSSEC tests and explicit target omissions. |
| AdGuard Home/Pi-hole detection and handoff | 5 | Discovers existing native/container services and management ports. Thin integration: no native query/filter/client/DHCP management or DNS-service provisioner. |

Sources: [resolver read/write](../../../backend/internal/netx/dns.go),
[hosts-file ownership](../../../backend/internal/netx/dns_hosts.go),
[comparison targets](../../../backend/internal/netx/dns_lookup.go),
[DNS wire validation](../../../backend/internal/netx/dns_wire.go),
[upstream UI](../../../frontend/src/components/network/dns/upstreams.tsx).

### Traffic, bandwidth and shaping

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| Two-second live per-interface traffic | 8 | Counter/reset handling and fifteen-minute ring. Add richer packet/latency/retransmit context and clear observation age. |
| Retained interface mean/peak/errors/drops history | 8 | SQL buckets, bounded windows/points and retention-disabled handling. Add custom-range UI, quotas, percentiles and incident annotations. |
| Container network history | 8 | Differences recorded Docker counters; avoids retaining duplicate host veth series. Add all-container drill-down and service/flow/peer attribution. |
| Per-program TCP traffic/peers | 6 | Bounded cached ss deltas and explicit warm-up. Misses UDP/QUIC and short-lived sockets between polls; no complete historical process accounting. |
| fq_codel/CAKE/fq selection and queue stats | 7 | Useful native queue choices and dashboard-owned rollback. Verify effective parameters and preserve/refuse unknown foreign trees. |
| Upload rate limits | 7 | CAKE bandwidth or HTB/fair leaf and protected-path minimum. Add traffic classes, DSCP/overhead profiles and measured latency benefit. |
| Download rate limits | 5 | Ingress policing with clsact protection. It drops traffic; add managed IFB queueing for download SQM/fairness. |
| BBR setting and supported-algorithm readback | 7 | Applies congestion control/default qdisc and reports support. Add workload comparison; flipping BBR is not proof of improved performance. |
| Existing eBPF/XDP/tc programs **(inspection)** | 7 | Inventory and attachments, not a loaded flow observer. Add useful detail UI and an optional budgeted telemetry adapter. |

Sources: [sampler/history](../../../backend/internal/netx/sampler.go),
[process/container accounting](../../../backend/internal/netx/traffic.go),
[shaping/BBR](../../../backend/internal/netx/shaping.go),
[eBPF inventory](../../../backend/internal/netx/ebpf.go),
[charts](../../../frontend/src/components/network/traffic/interface-charts.tsx).

### Connections and shared operator workflows

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| TCP/connected-UDP peer summaries | 8 | Remote-address summaries with local ports, processes and socket/established counts. Full protocol/remote-port/individual-state detail is discarded by aggregation; unconnected UDP is excluded. Add full tuples, duration/bytes/close events and quality limits. |
| Connection graph/search/filters/held updates | 8 | Shareable filtering and stable rows while new data arrives. Add historical flow selection and cross-layer evidence. |
| Block a remote address from its connection | 7 | Delegates to guarded persistent firewall rules. Add expiry/reason and an incident handoff rather than only a permanent deny. |
| Network package-install handoffs | 8 | Shared streamed jobs and refresh after installation. Follow installation with explicit configured/active/verified status; a package is not a working service. |

Sources: [connection model](../../../backend/internal/netsec/connections.go),
[connection UI](../../../frontend/src/components/network/connections-panel.tsx),
[graph](../../../frontend/src/components/network/connections-map.tsx),
[package-install handoff](../../../frontend/src/components/network/install.tsx).

### Diagnostics: all 26 server tools, plus the local subnet calculator

The server diagnostics are admin-only and audited. They have bounded inputs/runtime; opening a
prefilled link does not automatically send traffic. These ratings include the current shared result
and run-history experience, not just whether a host binary is invoked successfully.

| Feature | /10 | What it actually does; improvement needed |
| --- | --- | --- |
| DNS lookup | 7 | Host-resolver record lookup; add resolver/transport provenance and distinguish hosts/NSS from wire answers. |
| DNS authority | 6 | Nameserver ownership and addresses; add delegation, glue and consistency checks across authoritative servers. |
| Ping | 7 | Bounded ICMP echo; add structured loss/jitter history and explain filtered ICMP without declaring the service down. |
| Traceroute | 7 | Bounded hops with tracepath fallback and IPv6 literal support; add hop tables and comparisons over time. |
| Route lookup | 8 | Kernel route/interface/source/gateway selection without sending packets; join it to policy, NAT and effective firewall decisions. |
| Path MTU | 7 | Bounded tracepath; add a structured result with discovered MTU versus unknown/filtered outcomes. |
| TCP port check | 8 | Connect to one port and report duration/failure; correlate the answer with local listener ownership and firewall evidence. |
| TCP common-port scan | 6 | Bounded catalogue scan; expose exact coverage and structured results. No UDP or full-port scanner is shipped. |
| Banner grab | 6 | Bounded greeting; add protocol-aware parsing and clearly label identification confidence. |
| SSH host keys | 7 | Host keys/fingerprints; add comparison with a saved trusted fingerprint. Reading a key alone does not authenticate it. |
| HTTP inspection | 7 | Status, redirects and selected headers; separate transport trust from successful HTTP response and show request timing stages. |
| HTTP security-header grade | 6 | Browser security-header presence/grade; make advice application-aware rather than implying every header fits every service. |
| TLS certificate | 8 | Chain, names, expiry, negotiation and trust verdict; improve structured chain presentation and saved comparisons. |
| TLS version survey | 7 | Probe supported protocol versions; distinguish protocol rejection from network failure and link the deeper Proxy TLS tooling. |
| Combined site audit | 7 | HTTP, certificate and header inspection together; turn findings into owner-specific actions and save the report. |
| Mail path | 7 | MX/SPF/DMARC and SMTP reachability; show per-stage results and keep deliverability claims within the evidence. |
| STARTTLS | 7 | SMTP/IMAP/POP3/FTP upgrades and certificate inspection; add protocol-specific failure stages. |
| DNS blocklist reputation | 6 | Query supported reputation lists; show query failures separately from “not listed” and retain evidence/time. |
| ASN/prefix ownership | 7 | Registry, autonomous system, prefix and country information; label the lookup source and geography uncertainty. |
| Whois | 6 | Registration information; normalize results and distinguish lookup failure from absent/redacted registry data. |
| TCP/UDP listeners | 8 | Listening addresses and processes via host tools; link directly to the richer Ports ownership and exposure views. |
| IPv4/IPv6 egress inspection | 8 | Independent kernel routes/source/defaults, including IPv6-only hosts; selected source is not proof of the provider-NAT public IP. |
| ARP/NDP neighbours | 7 | Existing neighbour cache; add cache state/interface explanation. This is not active subnet discovery. |
| Host networking support | 7 | Tool/package hints, service-manager and IPv6 state; add capability probes beyond binary existence. |
| Packet snapshot | 6 | Fixed-argument summaries on one interface/protocol, up to 50 packets/15 s; add bounded capture jobs and PCAP artifacts. Decoded fields can contain sensitive data. |
| Wake-on-LAN | 7 | Send one magic Ethernet frame on a selected local broadcast interface; add saved devices and optional measured wake verification. Sending is not proof of waking. |
| Local IPv4/IPv6 subnet calculator | 8 | Exact browser-side arithmetic, including large IPv6 prefixes; add IPAM/overlap workflow and let appropriate read users use it. It is currently inside an admin-gated page. |

No MTR, iperf, managed speed-test service, active discovery sweep, LLDP/SNMP inventory, arbitrary BPF
filter editor or persistent PCAP library is shipped in this diagnostic surface.

Sources: [diagnostic dispatcher](../../../backend/internal/api/handlers_network.go),
[diagnostic implementations](../../../backend/internal/netsec/diag.go),
[extended probes](../../../backend/internal/netsec/probe_extended.go),
[LAN probes](../../../backend/internal/netsec/diag_lan.go),
[tool catalogue](../../../frontend/src/components/network/tools/tool-defs.ts),
[subnet arithmetic](../../../frontend/src/components/network/tools/subnet-math.ts).

### Networking capabilities elsewhere in the dashboard

These already exist and should be joined into Network workflows before equivalents are rebuilt.
The rows rate their networking role within this review; they are not a complete audit of Proxy,
Security or deployment lifecycle.

| Feature | /10 | Implementation and next improvement |
| --- | --- | --- |
| Docker network inventory/detail/topology | 8 | Networks, members and properties from the Engine; preserve Docker ownership and distinguish a failed candidate read from an empty list. |
| Docker network creation | 7 | API supports driver, subnet/gateway/range, internal/attachable/IPv6/options/labels. UI creation exposes name/internal/subnet only; surface a safe advanced editor. |
| Docker attachment/disconnection | 7 | Attach containers with aliases; disconnect with guards. Add conflict previews and retain failed-read/draft context. |
| Docker network deletion/pruning | 8 | Existing destructive lifecycle and system-network protections; add a dependency preview for affected deployments and connectivity. |
| Container port publishing | 7 | Real bindings and exposure controls; join Docker's NAT path to UFW/gateway/provider policy in one investigator. |
| HTTP/WebSocket reverse proxies and path routing | 8 | Native nginx sites, validation and ownership-aware deployment handoffs; improve proof that a successful reload actually loaded the candidate. |
| Upstream pools/load balancing | 7 | Existing proxy upstream pools; expand network-level outcome visibility and distinguish native proxy balancing from provider-managed HA. |
| TCP/UDP stream proxies | 7 | Native nginx streams already have an owner; join stream configuration, listeners and connection evidence into Network. |
| Proxy access lists/authentication/SSO | 7 | Access controls belong to the Proxy owner; provide cross-layer effective-access explanations. |
| Proxy rate limits, caching and HTTP/2–HTTP/3 controls | 7 | Existing application-layer controls, subject to native engine support; reuse them in service policies and verify their measured effect. |
| Certificates, ACME and renewal | 8 | Issuance, DNS challenges, inventory, renewal/trust diagnostics and private certificate tooling; link network failures to the precise issuance stage/owner. |
| Endpoint/TLS watches and proxy traffic/logs | 8 | Existing recurring observations and request/error evidence; reuse this machinery for network probes instead of creating a second generic monitor. |
| Port inventory, ownership, exposure and history | 8 | TCP/UDP, socket activation, Docker publications/NAT-only rows, firewall verdicts, owner actions and recording; add external-vantage proof of reachability. |
| Free-port selection and service identification | 8 | Bounded allocation and local-listener identification; include provider reservations and future policy conflicts where adapters supply them. |
| Host exposure and security posture | 8 | Allowlist, interface/binding and firewall evidence; explicitly mark unseen provider policy and complex foreign nftables decisions as unknown. |
| SSH configuration and sessions | 8 | Effective settings, staged guarded apply, keys, jump-host profile and session handling; connect these to network confirmed-apply and recovery. |
| Fail2ban | 8 | Jails, persistent tuning, bans/unbans, allowlists and history; add policy explanation and cross-engine deduplication. |
| CrowdSec | 7 | Existing decisions, alerts, bouncers and guarded ban/release; verify that an enforcing bouncer is actually active before claiming protection. |
| Suricata | 6 | IDS/IPS configuration-derived state, bounded alerts and rule counts; no full installation/interface/rule-update/inline-policy management workflow. |
| Network metrics and host-health findings | 8 | Historical interface/container counters and windowed host loss/error findings; add latency, retransmits, connection failures and probe correlation. |
| Dashboard ingress, allowlist and private previews | 8 | Caddy ingress, pre-auth allowlist, tailnet/SSH access and isolated deployment preview boundaries; preserve them in every proposed network change. |

Sources: [Docker network implementation](../../../backend/internal/dockerx/resources.go),
[Docker network UI](../../../frontend/src/components/docker/networks-tab.tsx),
[Proxy implementation guide](../../internal/backend/databases-proxy-platform.md#proxy),
[security/observability implementation](../../internal/backend/observability-security.md),
[security invariants](../../internal/security/invariants.md).

## Concrete implementation gaps to improve first

These are source-backed findings, not hypothetical missing buttons. Items F1–F9 were checked
directly during this review. They were not reproduced through live host mutations. F10 is an
existing documented limitation. “Priority” expresses engineering importance for this product, not
a CVSS severity or a claim that an attacker can exploit it.

| ID / priority | Current behavior and evidence | Required improvement and acceptance criterion |
| --- | --- | --- |
| **F1 / first** | `GatewayCapability` detects foreign forward chains whose **default policy** is `drop`; it does not model explicit `drop`/`reject` rules in accept-policy chains. `gateway_capability.go:127`. Writable means a known blocker was not found, not that the requested flow is admitted. | Show the checked policy layers and unknown layers. Model supported rule forms; use trace/probe evidence for complex rules. A foreign accept-policy chain with an explicit matching drop must not produce an unqualified reachable verdict. |
| **F2 / first** | Admission changes involve IPv4/IPv6 `FORWARD`, `INPUT` and `DOCKER-USER`, but health checks only test IPv4 `FORWARD`. `gateway_apply.go:988`. A partial external reload can escape this check. | Read every chain/family required by the active entries. Report absent, unsupported, unreadable and present separately, then offer an owned-rule repair. Tests must remove each required rule independently and detect the drift. |
| **F3 / first** | A missing/unreadable fetched blocklist cache becomes an empty prefix list (`gateway.go:136`, `:194`), while the visible count/error come from saved metadata (`protection.go:429`). A subsequent gateway render can install an empty set while the row retains the former count. Existing kernel rules do not necessarily change at the instant the file disappears. | Expose last successful fetch, actual cache readability/content, rendered generation and observed set size. Preserve the last known valid list or explicitly report degraded enforcement. Cache loss, permission failure and malformed contents must never look like healthy populated protection. |
| **F4 / first** | `commit` recovers synchronous failures, but several rendered files and the spec are not a crash-atomic transaction. Recovery is best-effort, and a later boot-unit enable failure can occur after runtime/spec commit. `persist.go`; implementation guide. | Add a durable pending-change journal, a host-side recovery watchdog and phase-specific outcomes: runtime applied, persisted, boot restore verified, confirmed, recovered/degraded. Kill the backend between phases and verify recovery without relying on that backend being alive. |
| **F5 / high** | `samePath` deliberately compares local/device/gateway, allowing a source address change (`path.go:100`). Source-selected route queries add coverage, but none of this proves the browser, tunnel transport or application still works. | Retain the existing kernel guards and add measured reconnect/service verification. Test a source change that preserves device/gateway but breaks the actual allowed path. Kernel-route stability must not be presented as end-to-end confirmation. |
| **F6 / high** | `verifyShaping` checks root qdisc kind and existence of ingress, not the requested rate/classes/filter parameters (`shaping.go:383`). Undo restores a prior dashboard spec; first-time shaping does not preserve an arbitrary foreign tc tree (`:274`). | Verify configured rate, class, filter and policer parameters. Refuse unmanaged queue replacement until its ownership/recovery is understood, or snapshot supported tc structures. A failed first apply must not silently destroy a foreign queue configuration. |
| **F7 / high** | The routing decision diagram filters to `family === "inet"` (`routing/decision-map.tsx:29`), although IPv6 policy rules are supported. Client rule highlighting is an inference, not a complete evaluator. | Add a family selector and kernel-backed target explanation. An IPv6-only fixture must display its relevant rules/table/client path, and the interface must label inferred highlights. |
| **F8 / high** | Tailscale subnet `apply` catches an error and resolves; its caller always clears the input (`vpn/tailscale.tsx:59`, `:182`). A rejected write loses the operator's draft. | Return success explicitly and clear only after success. Test a 409/500 response, retain the text and expose a retry that does not duplicate accepted routes. |
| **F9 / high** | Later poll errors can be hidden while retained data exists in workload traffic (`traffic/workloads.tsx:181`, `:254`), link inventory (`traffic/bandwidth.tsx:49`), eBPF (`traffic/ebpf.tsx:46`) and Firewall (`firewall-panel.tsx:211`). The separate live traffic sampler can still succeed while link inventory is stale. Docker candidate fetch swallows failure (`docker/networks-tab.tsx:581`). | Add last-success time and stale/error/retry states to each retained reading. A failed second poll must show stale data; failed candidate loading must not claim there are no containers to attach. |
| **F10 / high** | DNS comparison fans out to configured global/per-link/fallback resolvers without implementing resolved's split-DNS routing. Public presets are opt-in, but an already configured public upstream can still receive a private name. `dns_lookup.go`; [documented boundary](../../internal/backend/network.md#dns). | Default to policy-aware effective-resolution tests. Offer explicit cross-resolver comparison with named destinations and private-name disclosure notice. A private routing domain must not reach a public upstream in the policy-aware mode. |

Additional concrete usability work:

- Replace the universal **“no answer”** diagnostic failure headline with stage-specific outcomes:
  timed out, refused, unsupported, permission denied, DNS failure, invalid certificate, or completed
  with findings. `tools/tool-result.tsx:24` currently cannot express those distinctions in its headline.
- Store diagnostic runs rather than only three previous results in mounted React state.
  `tools/use-tool-run.ts:35`, `:69`. Add cancellation, repeatable comparison and export.
- Align capability checks: the local subnet calculator is hidden behind the admin probe page gate
  (`tools-panel.tsx:48`), while DNS comparison's UI gate is stricter than its `read` API
  (`dns/lookup-race.tsx:44`). Decide the intended visibility and apply it consistently.
- Carry protocol, port, family, source and interface through cross-page target links. Current tool
  prefill mostly carries target/record; a copied IP alone loses the question being investigated.
- Extend the existing UFW/firewalld model with actual manager state and effective zones. Backend
  selection currently prioritizes installed binaries; firewalld inspection asks the default zone
  (`netsec/firewall.go:165`, `firewall_firewalld.go:51`). An installed UFW binary and an active
  firewalld zone are not interchangeable evidence about who controls an interface.

Findings source links: [gateway capability](../../../backend/internal/netx/gateway_capability.go),
[gateway apply/readback](../../../backend/internal/netx/gateway_apply.go),
[blocklist loading](../../../backend/internal/netx/gateway.go),
[protection view](../../../backend/internal/netx/protection.go),
[commit/recovery](../../../backend/internal/netx/persist.go),
[path guard](../../../backend/internal/netx/path.go),
[shaping](../../../backend/internal/netx/shaping.go),
[routing diagram](../../../frontend/src/components/network/routing/decision-map.tsx),
[Tailscale form](../../../frontend/src/components/network/vpn/tailscale.tsx),
[traffic workloads](../../../frontend/src/components/network/traffic/workloads.tsx),
[tool result](../../../frontend/src/components/network/tools/tool-result.tsx),
[tool run state](../../../frontend/src/components/network/tools/use-tool-run.ts).

## Competitor comparison: which products to learn from

There is no single fair competitor that covers all of this. Cockpit/Webmin are host dashboards;
OPNsense/pfSense/RouterOS are router operating systems; NetBird/Tailscale are mesh specialists;
DNS and flow tools specialize further; cloud consoles control provider infrastructure. Their
strengths are useful references, but a feature in a router appliance or provider fabric is not
automatically feasible as an extra form on an existing VPS.

The following are paraphrases of current official documentation, not independent performance tests.
Paid editions, plugins, kernel support and provider topology may affect availability.

| Product | Documented networking strengths | JD comparison and feature to borrow |
| --- | --- | --- |
| **Cockpit** | Native NetworkManager and firewalld integration with host permissions. [NetworkManager](https://docs.cockpit-project.org/cockpit-guide/latest/guide/feature-networkmanager.html), [firewall](https://docs.cockpit-project.org/cockpit-guide/latest/guide/feature-firewall.html). | JD has richer local gateway/tunnel primitives, but does not edit full native connection profiles. Borrow persistent manager-backed host networking rather than competing with the provider's manager. |
| **Webmin** | Persistent interfaces/routing/resolver controls, historical bandwidth reports, and a native nftables module for tables/chains/sets/rules/profiles/import/apply/boot configuration. [Network configuration](https://webmin.com/docs/modules/network-configuration/), [bandwidth](https://webmin.com/docs/modules/bandwidth-monitoring/), [nftables](https://webmin.com/docs/modules/nftables/). | Raw firewall control and historical peer/service accounting are deeper than JD's current fallback inspection. Borrow scoped nftables administration and reusable policy profiles, with explicit ownership. |
| **OPNsense** | Monitored gateway groups, latency/loss failover, weighted balancing and sticky connections; historical NetFlow Insight and CSV export. [Multi-WAN](https://docs.opnsense.org/manual/how-tos/multiwan.html), [Insight](https://docs.opnsense.org/manual/how-tos/insight.html). | JD has individual routes/rules/NAT, without the health-driven orchestration. Borrow a guided egress-group workflow and retained flow investigation. |
| **OPNsense security/routing** | Suricata configuration, interface selection, downloadable rulesets and enforcement policies; FRR plugin for dynamic routing. [IPS](https://docs.opnsense.org/manual/ips.html), [FRR](https://docs.opnsense.org/manual/dynamic_routing.html). | JD's Suricata/FRR surfaces mostly inspect existing services. Borrow complete setup/update/validation/enforcement workflows. Plugins and commercial rulesets must be labeled. |
| **pfSense** | Multi-WAN policy routing/failover/balancing with explicit interface-specific NAT relationships. [Multi-WAN](https://docs.netgate.com/pfsense/en/latest/multiwan/index.html), [NAT](https://docs.netgate.com/pfsense/en/latest/multiwan/nat.html). | Borrow the sequence: configure usable uplinks, monitor, choose routing policy, preview NAT/return path, test failure. A second NIC is not necessarily an independent upstream. |
| **MikroTik RouterOS / CHR** | Safe Mode rolls back changes after abnormal control-session termination; configuration history, VRFs and BFD; CHR is a router VM. [Configuration management](https://help.mikrotik.com/docs/spaces/ROS/pages/328155/Configuration%2BManagement), [VRF](https://help.mikrotik.com/docs/spaces/ROS/pages/328206/Virtual%2BRouting%2Band%2BForwarding%2B-%2BVRF), [BFD](https://help.mikrotik.com/docs/spaces/ROS/pages/191299691/BFD), [CHR](https://help.mikrotik.com/docs/spaces/ROS/pages/18350234/Cloud%2BHosted%2BRouter%2BCHR). | JD's route guards and synchronous undo are useful; borrow independent confirmed apply and durable revisions. CHR licensing/router ownership differs from a dashboard on an existing application host. |
| **Tailscale** | Identity-based grants, posture/routing conditions, SSH authorization, private Serve and public Funnel. [Grants](https://tailscale.com/docs/reference/syntax/grants), [access control](https://tailscale.com/docs/features/access-control), [Funnel](https://tailscale.com/docs/features/tailscale-funnel). | JD changes this host's advertisements, not complete tailnet policy. Borrow route-approval visibility, “who can reach this?” and identity-aware adapter workflows. Funnel has documented beta/port/bandwidth constraints; it is not arbitrary public port forwarding. |
| **NetBird** | Group/resource/protocol policies, posture conditions and an interactive Control Center graph. [Access policies](https://docs.netbird.io/manage/access-control/manage-network-access), [posture](https://docs.netbird.io/manage/access-control/posture-checks), [Control Center](https://docs.netbird.io/manage/control-center). | JD's topology describes local resources. Borrow an effective-access graph where clicking an edge explains permission and routing. Route installation and packet permission remain separate decisions. |
| **Headscale** | Self-hosted node registration, subnet/exit routing, split DNS, dual stack, DERP and policies. Official feature documentation lists grants/SSH policy support but not full Tailscale parity. [Features](https://headscale.net/stable/about/features/), [current feature checklist](https://github.com/juanfont/headscale/blob/main/docs/about/features.md). | JD currently inspects users/nodes. Borrow version-aware enrollment, key lifecycle, route approval and policy tests. Serve/Funnel/flow-log parity must not be assumed. |
| **AdGuard Home** | Encrypted DNS transports, client-specific filtering/upstreams and DHCP. [Encryption](https://adguard-dns.io/kb/adguard-home/encryption/), [clients](https://adguard-dns.io/kb/adguard-home/clients/), [DHCP](https://adguard-dns.io/kb/adguard-home/dhcp/). | JD discovers AdGuard and edits the host resolver. Borrow an adapter for query logs, client policies and service provisioning; a host DoT setting is not a managed encrypted DNS server. |
| **Pi-hole** | Group/client filtering, query logging, local records, reverse forwarding, DNSSEC settings and DHCP. [Groups](https://docs.pi-hole.net/group_management/), [FTL configuration](https://docs.pi-hole.net/ftldns/configfile/). | JD's hosts block only affects local host resolution. Borrow synchronized local names, client/group policy and query evidence through an adapter. Do not infer native encrypted transport from integration with external helpers. |
| **Technitium DNS Server** | Authoritative/recursive zones, conditional forwarding, DNSSEC signing, DoH/DoT/DoQ, split-horizon apps, clustering and DNS64. [Official features](https://technitium.com/dns/). | JD lacks a complete DNS-service/zone workflow. Borrow zone/view/query/transport administration. Some capabilities require apps; IPv6 clients need NAT64 to reach IPv4 destinations through synthesized DNS64 answers. |
| **ntopng** | Hosts/flows/top talkers, protocol classification, RTT/retransmits, alerts and historical analysis; advanced features differ by edition and collection setup. [Official product and edition comparison](https://www.ntop.org/products/traffic-analysis/ntopng/). | JD has useful counters and sampled TCP socket deltas. Borrow a retained flow explorer, per-peer/service accounting and clear measurement-quality labels, or integrate ntopng. |
| **Netdata** | eBPF socket collection for TCP/UDP, process/cgroup traffic, connections/errors/retransmits. [Socket collector](https://learn.netdata.cloud/docs/collecting-metrics/operating-systems/ebpf-socket). | JD's eBPF page inventories existing programs. Borrow optional kernel telemetry with a low-cost fallback; inventory is not traffic instrumentation. |
| **Proxmox VE SDN** | Zones/VNets with VLAN/VXLAN/EVPN, IPAM and controller domains. [Official VE administration guide, SDN chapter](https://pve.proxmox.com/pve-docs-8/pve-admin-guide.pdf). | JD manages individual local links. Borrow fabric blueprints and global allocation only after a multi-host control architecture exists. The cited guide is versioned; this is evidence for those capabilities, not a latest-release benchmark. |
| **NetBox** | IPv4/IPv6 prefixes, ranges, addresses, hierarchy and VRF-aware IPAM. [IPAM](https://netbox.readthedocs.io/en/stable/features/ipam/). | JD has a calculator and allocations in separate subsystems. Borrow a shared pool/overlap/utilization model. NetBox is a source of truth, not a packet-enforcement engine. |
| **Cilium Hubble** | Service dependency maps and individual flow inspection in a Cilium-managed Kubernetes deployment. [Service map](https://docs.cilium.io/en/stable/observability/hubble/hubble-ui/), [observability](https://docs.cilium.io/en/stable/observability/hubble/index.html). | Borrow flow-backed workload edges and reasons. JD is a Linux/Docker host dashboard; these docs do not imply Hubble is a universal drop-in collector for it. |
| **DigitalOcean / Hetzner consoles** | DigitalOcean supplies upstream stateful/tag-applied cloud firewalls; Hetzner supplies private networks/subnets/routes and managed load balancers. [DO firewall](https://docs.digitalocean.com/products/networking/firewalls/details/features/), [Hetzner networks](https://docs.hetzner.com/networking/networks/overview/), [load balancers](https://docs.hetzner.com/networking/load-balancers/overview/). | Borrow read-first provider adapters, public/private address mapping and layered policy explanations. Local nftables cannot create a provider VPC or upstream capacity. Provider writes need explicit scoped credentials and a reviewed plan. |
| **AWS VPC console** | Reachability Analyzer models the configuration path and identifies blockers without sending packets; Flow Logs retain interface traffic metadata. [Analyzer behavior](https://docs.aws.amazon.com/vpc/latest/reachability/how-reachability-analyzer-works.html), [Flow Logs](https://docs.aws.amazon.com/vpc/latest/userguide/flow-logs.html). | Borrow the “explain this source→destination” interface plus flow evidence. Keep static analysis distinct from an active reachability measurement. |
| **Cloudflare Tunnel / Load Balancing** | Publish local services through a tunnel, and steer monitored public/private origins with appropriate networking setup. [Published applications](https://developers.cloudflare.com/cloudflare-one/networks/connectors/cloudflare-tunnel/routing-to-tunnel/), [private load balancing](https://developers.cloudflare.com/load-balancing/private-network/). | Borrow an optional service-publishing adapter and health-aware external ingress. This relies on Cloudflare infrastructure and product constraints; local host filtering cannot provide equivalent upstream DDoS absorption. |

For safe native uplink editing, NetworkManager's checkpoint API offers timeout rollback as an
independent building block. It is still only a checkpoint of the state NetworkManager controls,
not a transaction covering all of JD's subsystems. [Official checkpoint API](https://networkmanager.pages.freedesktop.org/NetworkManager/NetworkManager/gdbus-org.freedesktop.NetworkManager.html).

## What to build: prioritized projects with concrete outcomes

These are proposed designs inferred from the review. Effort is relative: **S** is a focused change
in existing owners; **M** is a substantial subsystem extension; **L** is a new cross-owner service;
**XL** changes platform architecture. These are not delivery-date estimates.

| Order | Project / effort | Operator outcome | Completion criterion and dependency |
| --- | --- | --- | --- |
| **1** | Applied-state correctness / M | “The desired policy exists, its backing data is readable, and these exact runtime objects enforce it.” | Fix F1–F3/F6/F9; show spec/render/runtime/boot phases and unknowns. Test partial reloads, lost caches and wrong shaping parameters. |
| **2** | Confirmed network changes / L | Review a diff, apply temporarily, reconnect and confirm; an independent host process recovers an unconfirmed change. | Journal phases durably; test backend death, canceled requests and recovery failures. Keep existing guards, capabilities, audit and Caddy-only dashboard ingress. Recovery must work when JD is down. |
| **3** | IPv6 and draft consistency / S–M | Every supported family is visible, rejected writes retain input, read-only utilities remain usable. | Fix F7/F8 and gates; add IPv6-only/source-sensitive fixtures. This is existing-feature repair, not a new network engine. |
| **4** | Connection-path investigator / L | Choose a source/container, destination, family, port and protocol; see DNS → rules → route → firewall → NAT/tunnel/proxy → owner. | Explain supported layers using named evidence; show unknown provider/foreign rules. Compare model predictions with bounded probes. Never equate a drawn edge with measured connectivity. |
| **5** | Durable diagnostic runs / M | Save, cancel, rerun, compare and export a named test with stages, timestamps and scope. | Typed results replace generic failure labels; canceled host processes stop completely; results survive browser/backend restarts within configured retention. Reuse jobs and existing watches. |
| **6** | External reachability checks / L | Verify a service from another controlled host and compare IPv4/IPv6, DNS, TCP and TLS outcomes. | Authenticated probe agent, bounded target scopes, retained evidence and measured/unknown distinctions. An outbound test from the server is not accepted as inbound proof. |
| **7** | Drift and boot health / M | See changes made by Docker, UFW reloads, CLI writers or reboot, with an owned-resource repair plan. | Compare active objects with generations; detect missing unit/renders/admission; reconcile only resources JD owns. Live acceptance includes reboot and daemon restart. |
| **8** | Flow accounting / L | Answer who used bandwidth yesterday, which container contacted an address, and whether loss/retransmits rose. | Low-overhead socket baseline plus optional kernel observer; TCP/UDP/short-flow quality indicators; capped retention. Benchmark overhead, dropped events and attribution accuracy. |
| **9** | Policy-aware DNS investigation / M | Test the actual split-DNS route, trust/transport and DNSSEC outcome before comparing other resolvers. | Fix F10; private names remain in the intended scope. Distinguish wire/NSS answers and configured encryption from verified encryption. |
| **10** | Dual-stack WireGuard / M | IPv6 addressing, peer allocation, routes and exit egress accompany working IPv4. | Verify split/full-tunnel leak behavior and endpoint reachability in both families. The current IPv6 capture/block behavior is not counted as IPv6 egress. |
| **11** | Persistent manager adapters / L | Edit static/DHCP addresses, DNS, routes, bonds and supported VRFs through their actual host owner. | Detect NetworkManager/networkd/netplan ownership; stage supported native profiles using checkpoints/recovery. Never install competing manager configuration. |
| **12** | IPAM and overlap prevention / M | Allocate from shared pools; preview conflicts across Docker, VPN, interfaces and provider networks. | Validate IPv4/IPv6 overlap, utilization, reservations and ownership before apply. Useful locally before a fleet exists. |
| **13** | PCAP capture lifecycle / M | Select a bounded capture, follow progress, download an authorized artifact and compare it with an incident. | Typed filter builder; packet/time/byte caps; cancellation, access controls, retention and redacted support export. Preserve the small summary snapshot as a quick tool. |
| **14** | True download SQM / M | Keep interactive traffic responsive under load, with before/after bufferbloat evidence. | Managed IFB + CAKE/fq_codel queueing, clsact preservation, rate/overhead profiles and live load acceptance. Do not describe policing as scheduled ingress. |
| **15** | Monitored egress groups / L | Latency/loss-aware failover, policy-selected gateways/tunnels and visible failback decisions. | Independent usable paths; hysteresis, stickiness/connection handling and recovery. Simulate flap/loss/failure before enabling automation. |
| **16** | VPN identity/access lifecycle / M–L | Expiring invitations, peer key rotation, route approval, groups and “who can reach this?” | Version-aware Tailscale/Headscale/NetBird adapters; one-time enrollment and secret lifecycle. Preserve the current refusal of changes that can sever the operator's control path. |
| **17** | Managed DNS service adapters / L | Connect/provision AdGuard, Pi-hole or Technitium; inspect queries, zones, views and client policy. | Use mature engines; validate listeners, ownership and access scope. Native recursive/authoritative DNS are separate roles, not another hosts-file edit. |
| **18** | Enforced protection lifecycle / M–L | Understand which security engine blocks a flow and safely move observation → canary → enforcement. | Suricata rule/interface/update adapters, actual bouncer/enforcement tests, exceptions with expiry and performance evidence. Presence of a daemon is not sufficient. |
| **19** | Provider-network adapters / L | See upstream firewall, NAT/public IP, private subnet and load-balancer policy beside host policy. | Read-only first for selected providers; label unreachable/unavailable APIs. Any later writes show credentials scope, exact diff, ownership and recovery boundaries. |
| **20** | Incident recorder / L | A timeline links DNS latency, route/firewall changes, tunnel handshakes, drops and deploy events. | Deterministic evidence first; bounded storage; drill-down into existing logs/metrics/runs. Assisted diagnosis cites evidence and proposes reviewable typed repair plans. |
| **21** | Fleet network fabric / XL | Join JD nodes across providers into encrypted private networks with shared pools and service policy. | A separately designed authenticated multi-host control plane, per-node ownership, revision reconciliation and canary rollout. This expands the current single-server architecture; it is not assumed to be authorized implementation work. |
| **22** | Digital twin and chaos lab / XL | Preview policies and test MTU, tunnel, DNS and upstream failures in disposable namespaces/VMs. | Declare model coverage and compare with controlled probes. Simulation cannot prove provider behavior; production fault injection is a distinct explicit operation. |

The recommended release sequence is **1–7 first**, **8–14 next**, then the advanced integrations and
multi-host work. Small repairs can proceed alongside the transaction design. State correctness and
independent recovery must precede autonomous routing or policy automation.

## Bigger ideas: twenty ways to make networking a defining feature

These are deliberately ambitious proposals, not existing features or claims of competitor parity.
Each needs a supported adapter and measurable result; none implies that an AI may bypass the
dashboard's authorization, audit or ownership contracts.

| Idea | Concrete experience | Hard dependency / limit |
| --- | --- | --- |
| **Network intent recipes** | “Expose this app privately,” “route this container through VPN,” “connect these sites,” each produces one editable plan. | Cross-owner transactions and verification; reuse existing proxy/Docker/VPN implementations. |
| **Per-workload egress identity** | Each project/container gets a selected tunnel/provider egress, allowed destinations and bandwidth budget. | Namespace/mark/source routing, stable workload identity and policy ownership. |
| **Fail-closed tunnel policies** | A protected workload loses egress when its tunnel fails, while JD's control connection keeps working. | Separate routing/policy domains and tested endpoint exemptions; no global kill switch. |
| **Temporary access links** | Invite a contractor to one service for an hour with an expiring peer or identity grant. | Mesh/provider identity adapter, revocation verification and sealed credentials. |
| **Click a flow to explain it** | A live graph edge opens DNS answers, selected route, matched policy, NAT tuple, tunnel and listening owner. | Flow collection plus uncertainty-aware correlation. |
| **Network time travel** | Select yesterday at 03:12 and reconstruct the policy, service ownership and sampled flows around an outage. | Retained revisions/events; gaps must stay visible rather than inventing history. |
| **Internet vantage map** | Test your service from controlled nodes in multiple regions; distinguish regional failure from local failure. | A trusted probe fleet and bounded testing budget. Geography is useful evidence, not an exact explanation. |
| **“What changed?” incident diff** | Compare before/after a deploy: new destinations, changed DNS, connection failures and bandwidth. | Workload identity joins, recorded events and consistent timestamps. |
| **Automatic support evidence bundle** | One export includes redacted routes, policy generations, selected logs, probe results and compatibility facts. | Explicit artifact access/retention and predictable redaction; captures can contain user traffic. |
| **Policy shadowing assistant** | Show redundant/never-used rules, conflicting layers and the traffic a proposed deletion may affect. | Per-rule telemetry and supported semantics; low observed use is not proof a rule is unnecessary. |
| **Service-aware bandwidth quotas** | Project/peer monthly transfer budgets, forecasts and optional throttling with visible exceptions. | Reliable accounting without double-counting bridges/tunnels; provider billing units may differ. |
| **Route optimizer with hysteresis** | Suggest or select a healthier tunnel/egress from measured latency/loss/cost policy. | Stable probes, independent paths and tested recovery. Do not optimize the current operator's path blindly. |
| **DNS route debugger** | Explain exactly which suffix/link/upstream/transport answered and why another client got a different answer. | Native resolver/DNS service adapters and client-vantage probes. |
| **Cross-provider private networks** | Bring Hetzner, DigitalOcean and another host into one encrypted service-address space. | Fleet architecture, global IPAM, NAT traversal/relay strategy and identity. |
| **HA gateway edition** | Two nodes share egress/service failover and clearly report which owns traffic and state. | Provider floating-IP or routing support, fencing/elections and state synchronization. VRRP/CARP multicast cannot be assumed on a VPS. |
| **BGP/EVPN lab and controller** | Design an overlay, validate advertisements, then canary it to capable hosts. | Native FRR configuration, ASN/address rights, provider peering and multi-host ownership. Not every VPS can advertise prefixes. |
| **NAT64/DNS64 gateway recipe** | Let IPv6-only clients reach IPv4 services with a visible translation/DNS path. | A managed NAT64 implementation plus DNS64; DNS rewriting alone is insufficient. |
| **Privacy-aware network anomaly detector** | Detect new destinations, unusual upload growth, DNS anomalies and unexpected open listeners. | Baselines, metadata retention and explainable evidence; alerting does not establish compromise. |
| **Optional upstream protection integrations** | Show and configure appropriate provider/Cloudflare protection beside local limits and application policy. | Upstream service capacity and supported APIs. A host firewall cannot absorb a volumetric attack that saturates its uplink first. |
| **Evidence-led network copilot** | “Why can't the app reach Postgres?” produces cited hypotheses, bounded tests and a typed repair plan. | Deterministic investigator and approved operation vocabulary; no request-built shell commands or autonomous privilege expansion. |

## Platform and product boundaries

For a VPS-focused roadmap, DHCP servers, Wi-Fi controllers, physical switch configuration,
LLDP/SNMP device discovery, NIC offload/duplex controls, OpenVPN/IPsec and additional bridge/tunnel
knobs are valid optional expansions. They are less urgent than making current VPS/Docker/private
networking reliable. Put them behind host capability and owner adapters instead of treating every
virtual NIC as a physical router port.

Build a capability model with four separate questions: **can be read**, **can be configured**,
**can be applied here**, and **has been verified**. Installed tools, kernel support, service ownership,
provider constraints and actual enforcement must have separate evidence. This resolves much of the
current ambiguity without removing useful inspection surfaces.

Preserve the established single-host architecture for the immediate work: `netx` owns its spec and
managed primitives, `netsec` owns host firewall/security adapters, Docker and Proxy keep their own
resources, and cross-owner workflows call those owners through existing backend authorization.
An optional fleet product needs its own design. Additive schema changes, explicit host argv,
destructive-action gates, audit entries, ownership checks, pre-auth allowlist and private dashboard
ingress remain requirements for every proposed addition.

## Documentation and verification of this report

This report adds an engineering reference, its CSV feature map and an index entry. The network guide
clarifies the existing source-address tolerance in the path guard and new-flow limit semantics. The
earlier audit corrects concurrent ceilings, API-only VXLAN multicast and peer-summary detail.
No runtime behavior, API, configuration, dependencies, licence headers, release notes or CI changes
are included.

Pre-push documentation review covers `docs/internal/`, `AGENTS.md`, `README.md` and
`CONTRIBUTING.md`. The new report/CSV, internal index and network guide, and earlier audit are the
affected documents. The operator and contributor guides need no change for a research-only report.
Verification uses
`scripts/test-changed.sh patch/0.7.1`, `git diff --check`, and local report-link/inventory checks.
