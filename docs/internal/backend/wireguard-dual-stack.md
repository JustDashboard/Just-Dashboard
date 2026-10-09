# Opt-in dual-stack WireGuard

New dashboard-managed WireGuard servers can allocate both IPv4 and unique-local IPv6 addresses.
Existing servers and sealed client configurations keep their installed format. There is no automatic
retrofit, allocation change or regeneration of an old exported profile. Existing-server migration
needs a separate preview and explicit opt-in; it is outside this implementation.

## Request and persistence

`POST /api/v1/network/vpn/wireguard` accepts an optional `ipv6` object:

```json
{
  "endpoint": "vpn.example.org:51820",
  "subnet": "10.8.0.0/24",
  "exitNode": true,
  "ipv6": { "subnet": "fd42:8::/64", "exitNode": true }
}
```

Omitting the object preserves the legacy IPv4 format. An empty object opts into private IPv6
addressing without IPv6 internet egress. A supplied subnet must be a unique-local `/64`; omitted
subnets use a cryptographically random `fd` prefix. Allocations reject overlap with host addresses,
routes in every table, configured tunnel addresses/non-default peer networks, and native non-default
WireGuard AllowedIPs. New peer allocations also account for the interface's actual addresses and
its native peers' AllowedIPs, including covering prefixes and defaults. An unreadable native peer
inventory refuses allocation. Prefix gaps are skipped in bounded work rather than iterating an IPv6
address space.

Opted-in peer provisioning and family exit changes also refuse live/config peer drift before
mutation. Membership, AllowedIPs, preshared keys and keepalives must match saved peer blocks;
dynamic endpoints, handshakes and counters are allowed to change. A native peer present only in
the kernel is never silently adopted or removed by a whole-configuration reload. The refusal asks
the operator to review and synchronize the state through its native owner before retrying. Legacy
operations keep their installed behavior. Native addresses and matching configured/native peer
networks still participate in allocation rather than relying only on dashboard metadata.

The managed `wg-quick` file keeps both server addresses and `# jd:ipv6=ula64`. New peers receive
IPv4 `/32` and IPv6 `/128` addresses. The existing sealed client configuration stores both; the
existing database address column remains the IPv4 address. No schema migration, dependency,
environment variable or additional daemon is introduced. Files and keys retain the existing VPN
ownership and permissions, and configuration export remains admin-only and `Cache-Control: no-store`.

## Split and full routes

Split clients receive both tunnel networks plus the explicitly shared networks. Site peers can
advertise IPv6 remote networks through the existing family-aware route handling and overlap/client
path guards. Default remote networks remain refused. New full clients receive `0.0.0.0/0, ::/0`;
both default routes use the tunnel while its interface is up.

Legacy full-client exports still capture `::/0` without allocating a working IPv6 tunnel address,
retaining the established IPv6 containment. Old sealed exports are never rewritten. Disabling IPv6
exit on a new dual-stack server leaves the client's IPv6 default captured rather than redirecting it
onto a native default route. `wg-quick` can retain more-specific local routes outside either full
tunnel. These profiles do not add a client kill switch: taking the interface down restores ordinary
client routing. The native test checks failure while the interface remains up, not a system-wide
client firewall guarantee. A separate opt-in Linux kill-switch export of a full-tunnel profile
closes the local-route and lost-interface leaks; see
[the WireGuard lifecycle](wireguard-lifecycle.md#linux-kill-switch). See the upstream [wg-quick route behavior](https://git.zx2c4.com/wireguard-tools/about/src/man/wg-quick.8)
and [WireGuard routing guidance](https://www.wireguard.com/netns/).

## Exit prerequisites and transaction ownership

IPv6 exit accompanies IPv4 exit. It requires an opted-in ULA `/64`, both forwarding families already
enabled, the existing router-advertisement forwarding guard, usable native uplink addresses, and
writable gateway policy. It does not change forwarding implicitly. IPv6 router-advertisement routes
still require `accept_ra=2` when forwarding is enabled; see the [kernel sysctl documentation](https://www.kernel.org/doc/html/latest/networking/ip-sysctl.html).

Each family performs a fresh `ip -j route get` with that family's first client address and tunnel
ingress interface. The IPv6 lookup uses `-6`; it never falls back to the IPv4 default card. IPv4 and
IPv6 uplinks may differ. A route into another tunnel, an absent/unreadable route, or an IPv6 uplink
without a global unicast address refuses exit enablement. The lookup models a sample forwarded
packet; other clients' source policy and other destination-specific routes can differ.

Both owned NAT entries use `wireguard:<interface>` and the existing gateway renderer, admission
rules and durable guarded transaction. Verification checks forwarding, a fresh source route, exact
source/interface masquerade and connection-mark expressions, chain hooks, and required admission
rules. Missing, narrowed, unreadable or partially applied IPv6 rules refuse the combined change.
Failure restores the previous gateway intent through the existing transaction; failed recovery is
exposed as uncertain/degraded and blocks further covered changes. The VPN family view marks both
exit outcomes unverified while a pending/unreadable network journal makes runtime intent uncertain.

`POST /api/v1/network/vpn/wireguard/{iface}/exit` retains `on` and accepts optional `ipv6`:

- `{ "on": true, "ipv6": true }` enables both exits after verifying both families.
- `{ "on": true, "ipv6": false }` withdraws IPv6 exit and retains IPv4.
- `{ "on": true }` preserves the currently saved IPv6 exit choice for older callers.
- `{ "on": false }` withdraws both exits.

All changes use the existing admin/audit boundary. Withdrawal consumes the destructive budget and
uses ordinary UI confirmation. Gateway/NAT state uses durable recovery; WireGuard file/peer/unit
operations retain their existing synchronous rollback. This does not extend the independent host
watchdog to WireGuard provisioning or prove tunnel recovery after process loss.

## Reading evidence and acceptance

The VPN response exposes `ipv6Enabled`, `endpointReachability: "not_tested"`, and independent
`families.ipv4`/`families.ipv6` records: configured subnet, actual-address runtime state, configured
exit/uplink, runtime rule outcome, capability, refusal reason and, once the rules verify, the
owned masquerade rule's count of translated client connections. `exitNode` remains the IPv4 exit
field for older clients. Peers add optional `address6`. The page distinguishes configured intent,
local rules and unknown provider reachability, and retains the dated last successful read after a
later poll fails. Creation failures retain the opt-in/subnet draft.

Focused unit/API tests cover ULA validation, native allocation conflicts, bounded prefix exhaustion,
exact rule evidence, failed combined apply retaining prior intent, admin-only routes, destructive IPv6
withdrawal, peer drift refusal without changing native peers/saved bytes, sealed exports and the
legacy containment format. The browser fixture covers IPv6
payloads, absent legacy opt-in, independent family outcomes, ordinary withdrawal confirmation,
409/500 draft retention and failed later polls.

The existing opt-in native-test switch runs the disposable server/client/observer acceptance:

```bash
cd backend
GOMAXPROCS=2 TMPDIR=/absolute/task-owned/workspace/tmp JD_NETNS_LIVE=1 \
  go test -race ./internal/netx -run '^TestLiveWireGuardDual' -count=1 -v
```

It needs `ip`, `nft`, `iptables`, `ip6tables`, `wg`, `wg-quick`, `curl`, `python3` and namespace privileges.
Every link, address, route, forwarding setting, firewall rule and WireGuard process stays inside
task-owned disposable namespaces. Systemd operations are represented by namespace `wg-quick`
commands; no physical host device or real host service is changed. It proves generated split/full
profiles over IPv4 and IPv6 UDP transport, different family uplinks, both-family TCP and UDP-to-
declared-resolver routing, handshake evidence, failure containment, admission-failure rollback and a
legacy IPv4 full profile's IPv6 containment. UDP resolver tests use echo evidence, not actual DNS
answers. Resolver-manager integration is excluded by removing only the client `DNS` setting before
starting the fixture profile. Public endpoint/provider acceptance, external peer interoperability,
actual DNS answers and reboot restoration remain untested by this fixture; P10 as a whole remains
partial until those acceptance boundaries and explicit existing-server migration are addressed.
