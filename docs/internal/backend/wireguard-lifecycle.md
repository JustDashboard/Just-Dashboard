# WireGuard record, lifecycle and mesh inspection

This extends [the VPN section of the network module](network.md#vpn) and
[opt-in dual-stack WireGuard](wireguard-dual-stack.md). It covers what the dashboard now remembers
about each tunnel, what an administrator can change after a tunnel or peer exists, and what it can
honestly say about Tailscale and Headscale. Peer key rotation, expiring invitations, groups, route
approval and enrolment are the separate VPN identity lifecycle (ledger P16) and are not here.

## The record

`wg show` answers only "now". `wgrecord.go` keeps the history beside the sealed client
configurations, in five additive tables (`store/network_wireguard_schema.go`):

| Table | Holds | Bound |
| --- | --- | --- |
| `network_wg_samples` | per peer every five minutes: counters, reset-aware deltas, the seconds they cover, the handshake | metrics retention, at most 7 days |
| `network_wg_usage` | per peer and UTC day: bytes received and sent | 400 days |
| `network_wg_endpoints` | each address a peer was seen dialling from, first/last seen, readings | newest 20 per peer |
| `network_wg_events` | the lifecycle: what was done, by whom, its outcome and detail | newest 500 per tunnel, 365 days |
| `network_wg_quotas` | a peer's usage budget | until cleared or the peer is removed |

Rows carry a peer's public key at most; private and preshared keys never reach them. The sampler
starts with the module (`Service.Start`), reads `wg show all dump` through `hostexec`, and records
nothing on a host without `wg` or when the dump is unreadable rather than a run of zeros. A
counter that goes down is an interface that restarted, and its new value is what moved since; a
peer's first reading has no baseline and counts nothing. After a dashboard restart the previous
sample is read back, so an upgrade does not drop the traffic that moved while it restarted. A
metrics retention of zero (`JD_METRICS_RETENTION=0`) keeps no samples, usage or endpoints;
lifecycle events and handshake transitions are still recorded, and the trend says it is not
recording rather than drawing an idle tunnel.

`GET /network/vpn/wireguard/{iface}/history?window=6h|24h|7d[&peer=<public key>]` returns the
trend (five-minute buckets, hourly for a week), and for one peer its endpoints and thirty days of
daily usage, with the lifecycle events. It never touches the host and works for a removed tunnel.

## Alerts

Every page read classifies each peer's handshake: `online` (within three minutes), `stale` (a peer
with a persistent keepalive on this side — always-on, normally a site — that missed its rekey),
`idle` (a peer without one, typically a phone with its tunnel off, which is not a fault) and
`never`. The sampler records the moment an always-on peer goes stale and when it recovers.
`WGInterface.alerts` lists stale always-on peers on a running tunnel, transports routed into a
tunnel, and budgets at 80% or past. Alerts are readings; none acts.

## Transports

The sampler asks the kernel where each peer's encrypted packets go, the way WireGuard's socket
asks: `ip route get <endpoint> [mark <fwmark>]` with the interface's fwmark when it has one, so a
hand-written full tunnel's policy routing is modelled. A route into a WireGuard device is a loop —
the tunnel would carry its own transport — and is recorded as `transport_captured`, with
`transport_restored` when it leaves again. At most 32 endpoints are checked per pass.

The same endpoints are now client-path anchors (`path.go`, `wgTransportAnchors`): every guarded
change — routes, rules, devices, site routes — reads the route to each endpoint the kernel reports
(a resolved host name, a roaming phone where it is now) before the change and asks again exactly
the same question after it. A change that routes one into a WireGuard device, or leaves it with no
route, is put back with `409 would_lock_you_out`. A transport already captured is not anchored, so
the change that repairs it is not refused, and a move between native devices is left to the
existing internet anchors. Adding or editing a site also resolves a host-name endpoint before the
change and refuses networks that contain it, any endpoint in a tunnel file, or any endpoint the
kernel reports; a name that does not resolve is refused because WireGuard resolves it when the
tunnel loads its peers.

## Lifecycle and recovery history

Creation, up/down, exit changes, removal, restore, peer add/remove/edit, forgetting a saved copy,
budgets, site verification, firewall outcomes and the sampler's transitions each write an event.
Failures record what recovery achieved: a creation, restore or peer change whose rollback also
failed — the file not restored, the unit still running, the interface not reloaded — is `degraded`
with what was left; one put back cleanly is `failed`. Events are written with an independent
context after the host change, so a canceled request does not drop them, and a failed write is
logged rather than turned into a failure of the change. This is a durable history, not the network
module's independent recovery journal: WireGuard file/unit operations keep their synchronous
rollback as before.

## Archive and restore

Removal still moves the file into `/etc/wireguard/.just-dashboard-removed/<name>.conf.<unix>`,
because the server key exists nowhere else, and records which exit was withdrawn.
`GET /network/vpn/archive` lists archived files without keys, each with whether it is restorable
now. `POST /network/vpn/archive/{file}/restore` takes only names matching what removal writes and
refuses a file the dashboard did not write or without a usable key. It runs the checks a new
tunnel passes — a free name (file and device), a free UDP port, tunnel networks that overlap
nothing on the host or in another tunnel, and peer networks (wg-quick routes every AllowedIPs
entry) that neither collide with the host, contain the operator's address nor capture a WireGuard
endpoint — then moves the file back, starts `wg-quick@<name>` and compares the operator's path.
A failed start or moved path moves the file back into the archive and records the outcome. The
same key and peers come back, so every client's existing configuration connects again. Saved
client configurations were deleted with the tunnel and stay gone (rows left under the name are
dropped first), the exit is not restored, and the handler reopens the firewall port as creation
does. These routes are the archive's own (`/vpn/archive`), so a tunnel may still be called
`archive`.

A stored configuration is now shown, forgotten or edited only when its row's public key matches
the peer of that id in the tunnel's file, so a row left by a tunnel removed by hand or a restored
file can never be shown as another peer's.

## Editing a peer

`PATCH /network/vpn/wireguard/{iface}/peers/{id}` (`wgsite.go`) changes a peer the dashboard
made; a nil field is left alone. A name changes in the file's metadata and the store. A site's
networks, endpoint and keepalive change on this server: networks pass every guard a new site's do
(the tunnel's own networks, other peers, host networks, the operator's address, transports), new
networks are routed under the client-path check, withdrawn ones are deleted, and the interface is
reloaded with `systemctl reload`. A device's routes (full tunnel, shared networks) and keepalive,
and what a site may reach here, exist only in the peer's own configuration; it is regenerated from
the sealed copy with the same keys, resealed, returned once with its QR code (`Cache-Control:
no-store`) and must be imported again. Without the copy such an edit is refused rather than half
applied; a site's endpoint change without a copy is applied and names the listen port to set on
the site. A failed write or reload restores the file, the stored copy and name, and records the
outcome. Withdrawing a site's network or changing where it is dialled spends the destructive
budget, decided against the peer under the mutation lock; other edits are routine.
`network_vpn_clients.client_routes` (an added column) keeps a device's routes unsealed so an edit
can start from them; it is empty, and reads as unknown, for peers made before it existed.

## Usage budgets

`PUT /network/vpn/wireguard/{iface}/peers/{id}/quota` sets a budget of 1 MiB to 1 PiB per UTC
calendar day, ISO week or month; `DELETE` clears it (destructive budget, as every removal).
Usage is the recorded daily sums since the period began, so it is `unmeasured` without a record
and undercounts anything that moved before recording started. A budget is an alert at 80% and
when passed; it never disconnects, because a background cut could take the operator's own way in
with it. Removing a peer removes its budget; its recorded usage ages out with retention.

## Site verification

`POST /network/vpn/wireguard/{iface}/peers/{id}/verify` checks a site from this end: a handshake
within three minutes, each network routed into the tunnel, the site's transport outside every
tunnel, an ICMP answer from its tunnel address and, when `target` names a host inside its
networks, a round trip into its LAN sourced from this server's tunnel address — which proves the
site forwards and its LAN routes the tunnel back. Pings use fixed argv
(`ping -n -q -c 3 -W 1 -I <source> <target>`) with validated addresses. No answer is a warning,
because ICMP filtering also causes it. The outcome is `verified` only with a target that answered;
without one it is `partial`. The result lists the checks the far side runs for itself, which this
server cannot, and is recorded in the history. It is audited because packets leave the host.

## Endpoint evidence

`GET /network/vpn/wireguard/{iface}/endpoint`, and every creation's response, resolve the endpoint
clients were given (with the dashboard's resolver) and compare it with the host's addresses:
`on_host_public`, `provider_mapped` (the host holds no public address of that family, so the
endpoint is presumably the provider's one-to-one NAT), `elsewhere` (a public address this host does
not hold while its uplink has one — a warning at creation), `private` or `unresolved` (a warning).
It also says whether a UDP socket holds the port here. Nothing dials in from outside, so
`reachability` stays `not_tested`.

Automatic allocations also avoid the shared IPAM plan: when no reservation is selected, the
handler passes every unreleased reservation to `CreateWireGuard`, whose default IPv4 /24 and random
unique-local /64 steer around them. An unreadable plan proceeds as before with a warning.

## Exit evidence

Each family's exit now carries `translated`: the owned masquerade rule's packet counter. A NAT
chain sees only a connection's first packet, so it counts client connections sent out through the
exit since the rules were loaded — measured use, not proof of reachability beyond the provider.

## Linux kill switch

`GET .../peers/{id}/config?variant=linux-killswitch` returns a full-tunnel device's configuration
with an nftables output filter (`wgkillswitch.go`): only the tunnel, loopback, WireGuard's own
marked transport packets (`meta mark $(wg show %i fwmark)`), DHCP and IPv6 neighbour discovery
leave. It closes the two leaks a full tunnel leaves — a more specific native route, and an
interface lost without wg-quick's teardown (its policy rules then point at an empty table and
traffic falls back to the native default). `PreDown` removes the filter, so a deliberate
`wg-quick down` restores the native path; an interface lost any other way leaves the device offline
until `nft delete table inet jd_killswitch`. The phone apps refuse PostUp lines, so the variant is a
separate text download, never the QR code; Windows' client has its own kill switch for this shape,
and phones have the system's "block connections without VPN". The hooks are constants with
wg-quick's own `%i`; nothing comes from a request. Split tunnels and sites are refused. Viewing it
is audited like any configuration view.

## Tailscale and Headscale

`TailscaleView.approval` compares what the preferences advertise with this node's own status:
an advertised subnet in `Self.PrimaryRoutes` is `serving`; one not there is `not_serving` — awaiting
approval, or another router of the same subnet is primary. The exit is `serving` once
`Self.ExitNodeOption` is set. Tailnet policy is not readable from a node. `Self.keyExpiry` is read
from the status and warns within fourteen days and once expired, because an expired key takes the
server, and a dashboard reached through it, off the tailnet. Nothing new is written to Tailscale.

Headscale stays inspection only. Its CLI prints protobuf messages through Go's `encoding/json`, so
keys are snake_case (`given_name`, `ip_addresses`) with numeric ids; the reader now matches keys
without regard to case or underscores, so snake_case and camelCase builds both read. Nodes add
`expiry`, `validTags`, and `availableRoutes`/`approvedRoutes` from the node itself (0.26+) or, when
no node reports routes, from the older `routes list -o json`; a Headscale without that command
leaves routes unknown (`routesKnown: false`) rather than reported as none. The page now also shows a
Headscale running in a container, which it previously read but never displayed.

## Tests and native acceptance

Unit and API tests cover the record (deltas, resets, retention, bounds, transitions), alerts,
transport checks and anchors, endpoint verdicts, IPAM avoidance, peer edits and their refusals and
rollback, budgets, site verification, the kill-switch variant, archive listing/restore/refusals,
the stored-configuration key match, Tailscale approval and key expiry, Headscale snake_case/legacy
routes, route gates and destructive budgets. Two owned-namespace fixtures run with the existing
switch and a task-owned `wireguard-tools` on `PATH`:

```bash
cd backend
GOMAXPROCS=2 TMPDIR=/absolute/task-owned/tmp JD_NETNS_LIVE=1 \
  go test -race ./internal/netx -run '^TestLiveWireGuard(KillSwitch|Site)' -count=1 -v
```

The kill-switch fixture first shows the plain profile leaking through a specific native route and
after an abrupt interface loss (both families), then the variant of the same peer holding through
both, resolver traffic still working inside the tunnel, a deliberate down restoring the native
path, and both exits' translation counters after real client traffic. The site fixture verifies a
real site end to end, sees the verification degrade when the site stops forwarding, records real
traffic, endpoint and transport, refuses a guarded route that would capture the site's transport
(and leaves no route or spec behind), and removes and restores the tunnel with the site's unchanged
configuration reconnecting. Everything stays in disposable namespaces; no host interface, route,
firewall or service is touched. Provider/public reachability, real phone/Windows clients, other
operating systems' kill switches, Headscale and Tailscale control-server behaviour and reboot
restoration remain untested by these fixtures.
