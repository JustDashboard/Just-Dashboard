# Gateway policy and blocklist health

`netx` distinguishes mutation compatibility, policy coverage and actual enforcement. `writable`
in `GET /network/gateway` means a gateway translation can be configured through supported local
policy owners. It does not prove source-to-destination connectivity. `capability.reachability` is
`unknown`; provider/upstream policy and measured end-to-end connectivity remain named unknowns.

## Local policy coverage

`capability.layers` records each relevant nftables base chain: family, table, chain, hook, default
policy, status and an explanation. Status is `owned`, `admitted`, `checked`, `blocked` or `unknown`.
The dashboard's own table and iptables-compatible **filter** `INPUT`/`FORWARD` chains are owned
admission paths. A `FORWARD` chain in an independent security or mangle table is not inferred to be
owned merely from its name. Foreign input, forward, prerouting and postrouting filters are checked.

Supported rule forms are unconditional verdicts and the documented masked connection-mark accept.
An explicit drop or reject in an accept-policy chain is retained as a blocker. A conditional drop,
jump, map or unsupported expression produces uncertainty and refuses new/enabled translations
until the foreign policy is reviewed. An unconditional base-chain return applies its default policy;
it cannot override a drop policy. A prerouting mark exemption is not counted as admission because a
new source-NAT flow is marked later at forward and a new port-forward flow is marked at destination
NAT. Complex policies require flow-specific trace or controlled probe evidence. No trace is inferred
from the absence of a recognized blocker, and an external firewall is never edited automatically.

A foreign chain of type `nat` is assessed by what it can do: translation statements (`dnat`, `snat`,
`masquerade`, `redirect`, and the iptables `DNAT`/`SNAT`/`MASQUERADE`/`REDIRECT`/`NETMAP` targets)
and returns cannot drop, so such a chain, followed through its jumps, is `checked` unless it holds an
explicit drop or reject. Each layer also reports its own rule count, its chain type, and `uncertain`:
the positions and forms (`rule 3: iptables match addrtype`) the generic check could not read.

Limits and blocklist drops keep working through independent filters. Removing or disabling managed
translations remains possible when the host has switched to an unsupported policy owner.

## Per-flow evaluation

`gateway_flows.go` models each enabled entry's flow exactly as the entry defines it and walks it
through every checked base chain at the hooks it crosses (prerouting, then input for a local target
or forward and postrouting otherwise), following jumps and gotos with continuations. A forward is
modeled before and after this table's destination translation at `dstnat - 10`: chains earlier at
prerouting (raw at -300, mangle at -150) see the visitor's packet addressed to one of this host's
non-loopback addresses and the public port; later chains see the target and target port with
`ct status dnat`. The admission mark is visible to a chain only after it is set — at the forward's
translation, or in this table's forward chain at -10 for a NAT entry's outbound flow. A NAT entry's
unknown destination is known not to be loopback or another local device's network. A mapping
(one-to-one or nptv6) adds its inbound half.

Supported matches are IPv4/IPv6 source and destination against literals, prefixes, ranges and
anonymous sets; TCP/UDP/`th` destination ports against values, ranges and sets; `meta l4proto`,
`nfproto`, `iifname`/`oifname` (with trailing wildcards); `ct state`, `ct status dnat`; `fib daddr type
local`; and the masked admission mark. A rule is decided only when every match is; an unsupported
match (a named set, an iptables `xt` match, `limit`, `meter`, `socket`, …) on a rule whose verdict
could change the outcome leaves the layer `unknown`, naming the expression. A verdict map (`vmap`),
`queue`, `synproxy`, `tproxy` or `fwd` is an unmodeled verdict, so a rule reaching one is `unknown`
rather than read as passing. When the only undecided
matches concern the visitor's source address and the branches differ between drop and pass, the
layer is `restricted`: some sources are dropped, the rest pass. Each layer's verdict is `clear`,
`restricted`, `blocked` or `unknown`, with the deciding rule's position and jump path. An evaluation
bound of 4096 rule visits per layer turns an oversized ruleset into `unknown`.

`requireWritable` uses the model when the generic check refuses for a reason other than firewalld,
a missing nft or an unreadable ruleset: a translation change goes ahead only when every enabled flow
of the spec about to be applied (forwards, NAT entries and their mapped inbound halves) is `clear` or
`restricted` at every checked layer. A `blocked` or `unknown` layer refuses with the table, chain,
rule position, reason and the accept to add. WireGuard exits, drift admission repair and direct
admission repair pass their spec too. On an ordinary Docker host the raw-table drops naming container
addresses, the nat chains and mark-only mangle rules no longer make the gateway read-only; a real
drop on the public port still does. `GET /network/gateway` returns every entry's flows as `flows`.

This is a model of supported rule forms, not a packet trace. Provider policy, routing decisions,
rate-limited rules and unsupported expressions stay unknown, and `capability.reachability` stays
`unknown`.

## Readiness, totals and measured reachability

Each forward and NAT entry carries `readiness`: `policy` (`installed` when every rule the entry renders
is in the loaded table by comment count, `partial`, `missing`, `drift` for extra copies, `not_loaded`,
`disabled`), the family's `forwarding` switch, `admission` summed over the family's needed chains,
`ready` for all three, and `reachability`. Reachability is `unverified` unless an enrolled external
source's retained TCP measurement of one of this host's addresses on a port the forward publishes
completed after the forward's `changedAt` (`verified` when it connected, `failed` when it did not).
The API layer attaches that evidence (`external`) for administrators only, from the external checks
module; a local service answering on the same port would look the same, and UDP is never measured.

`POST /network/gateway/verify` checks a forward's target from this server: a bounded TCP connect to
the target and port, reported `answering`, `refused`, `timeout`, `unreachable` or `error`, and
`not_measurable` for a UDP-only forward. The result is retained in memory for the page and marked
stale when the target changes. It does not pass through the translation or the forward path.

Auto source translation is re-checked on every read (`decision`): the stored choice, what the host's
addresses say now, and the sentence why. `drift` means the topology changed after the decision; any
gateway save re-decides every auto forward.

Counters survive table replacement (`gateway_telemetry.go`, `network_gateway_counters`). The loaded
table's kernel handle names a generation; each reading folds the live counters per rule comment into
a persistent row, carrying the last reading into the total when the handle changes or a counter falls.
The dashboard's own reloads read the old table immediately before loading the new one, and a recorder
reads every minute. Entries return `total` (packets, bytes, the first time the rule was seen and the
replacements carried across) beside the live since-load figures; views return `counters` with the
generation, last reading and the gap: traffic between the last reading and a replacement made outside
the dashboard is not recovered.

## Admission drift and repair

`admission.chains` reads `FORWARD`, `INPUT` and `DOCKER-USER` independently for every family used by
enabled forwards/NAT entries. Each row includes family (`inet`/`inet6`), tool, chain, `needed`, status
and reason; `checkedAt` timestamps the observation. Status is `present`, `absent`, `unsupported` or
`unreadable`. A rule placed after another chain rule is reported absent from its required first
position. Aggregate `present` requires every needed chain to be present; one successful IPv4
`FORWARD` check cannot establish IPv6, input or Docker health.

An absent Docker chain is unsupported and does not require a rule. An unavailable iptables tool is
optional only after the nftables inventory establishes that the corresponding compatible filter
chain does not exist. An unreadable inventory remains uncertainty. An active family whose admission
insertion fails causes rollback, including IPv6. The gateway verifies all required admission rules
before it reports a successful apply.

Owned-rule removal is limited to the families used before or after the change. A missing exact rule
is an expected deletion result; permission or tool failures are errors. An unavailable tool is
optional only when inventory establishes the required compatible chain is absent. Disabling the
last translation cannot report success after a failed admission deletion.

`Service.RepairGatewayAdmission` reasserts only the fixed connection-mark/comment rules and verifies
them afterwards, under the network mutation mutex, independent recovery file lock and current
gateway compatibility guard. An unresolved pending or degraded journal refuses a repair before
any host command runs, so reconciliation cannot race the independent helper. It does
not replace a table or foreign chain policy. The API route must use the existing destructive
capability, mutation budget and audit wrapper. Boot restoration uses the generated service's existing
admission commands; a runtime repair does not replace ownership or boot recovery.

Independent recovery snapshots only the observed chains in families affected by the change.
It records the original owned-rule positions/counts, deletes only the exact mark/comment match,
and restores that presence. An IPv4-only host or a host without Docker does not acquire fatal
recovery steps for unsupported chains. Missing-rule deletion is expected; permission failures
remain recovery errors. Failed gateway or sysctl undo is recorded as degraded rather than healthy.

## Backing data, saved render and runtime sets

Each protection blocklist exposes independent evidence:

- `refreshed`: the last successful fetch timestamp; fetch failures remain in `error`.
- `savedCount`: the count recorded at that fetch or manual edit.
- `count` and `cache`: actual readable prefix count, `ready`/`missing`/`unreadable`/`invalid`, data
  generation and any cache error. `count` becomes zero on unreadable data instead of displaying the
  former populated count as current protection.
- `renderedGeneration`: the address-union generation reconstructed from the saved `gateway.nft`
  blocklist sets; an absent or unreadable saved render has no verified generation.
- `runtime`: status, observed set-element count, address-union generation, observation timestamp and
  any read error. Unknown counts are JSON `null`, not fabricated zeroes. Runtime statuses are
  `present`, `absent`, `unreadable` or `not_required` for a disabled list.
- `enforcement`: `disabled`, `verified`, `degraded` or `unknown`. Verification requires matching cache,
  saved render and observed kernel generations. Cache loss or set/content drift is degraded;
  unreadable runtime evidence remains unknown.

The generation hashes the normalized address union, so nftables auto-merging adjacent prefixes into
a range does not invent drift. Cache count is the validated prefix count; runtime count is the
kernel's observed set-element count and can differ after auto-merge. Both IPv4 and IPv6 sets are
read. Runtime evidence comes from `nft -j list set`; capability and counter inventories stay terse.

Cache parsing is bounded by the existing feed size and entry limits. Every nonempty line must be a
valid bounded prefix; an empty, partially malformed, unreadable, private or reserved fetched cache
is rejected whole. Parsed data is memoized by file identity, modification/change timestamps, size
and mode. Replacements and permission changes cannot silently reuse a prior cache result.

An enabled unhealthy list refuses a candidate render before a kernel reload. The existing kernel
sets and saved boot render remain intact. A repair/refresh/removal can use the last committed render
as its rollback source when the old cache has disappeared. If both the old data and saved render are
unavailable, mutation is refused because recovery cannot be established. Refresh errors persist only
fetch metadata; recording a failure neither reloads the table nor depends on the lost cache.

Fetched replacements are rendered from explicit candidate prefixes and staged in memory before
changing the cache. Their previous bytes, mode and existence are included in the durable journal;
the cache write begins only after that journal and its configured watchdog are prepared. A failed
write is restored even if rename succeeded before the writer returned an error. Recovery restores
cache, saved spec/render and kernel sets together, so a later render cannot activate a fetched
candidate from an interrupted change. Cache snapshots accept only numeric `lists/<id>.txt` paths
and refuse symlinks. Oversized recovery journals are rejected before an apply. Fetch-error metadata
shares the host recovery lock and does not overwrite an unresolved recovery journal.

## Protection evidence

The recorder also keeps one row per minute of each rule's packet and byte increase and the
connection table's count, maximum and the kernel's drop, early-drop, failed-insert and error
increments (`network_protection_samples`, seven days). The table itself is read over ctnetlink
(`conntrack_netlink.go`): a bounded dump (200 000 entries), per-CPU statistics and, for session
revocation only, deletion of one entry by its exact original tuple, zone and id. No `conntrack` tool
or `/proc/net/nf_conntrack` is needed.

`GET /network/protection/pressure` (administrators: it names sources) returns the breakdown by state,
protocol, top sources and destination ports, the statistics, the last day's series, per-limit open
counts and busiest sources against their ceilings with recorded refusals, and indications: a nearly
full table, recent table-full drops, a large SYN_RECV share, closing-connection churn, unreplied flows,
one dominant source or port. Each cause carries its numbers; none is a diagnosis.

## Verification

`gateway_correctness_test.go` covers supported/unknown foreign policies, independent family/chain
drift, unreadable and unsupported admission, rule order, owned repair, active IPv6 insertion failures,
cache permission/content loss, replacements preserving timestamps, refresh/removal after cache loss,
and agreement between cached, rendered and observed generations. `gateway_live_test.go` verifies the
documented mark exemption and explicit accept-policy drop against real nft JSON, deletes and repairs
all six admission rules independently, and proves that cache loss preserves loaded kernel data while
partial family-set drift is detected. Live tests run only in disposable namespaces:

```bash
cd backend
JD_NETNS_LIVE=1 go test -race ./internal/netx -run Live -count=1
```

`gateway_recovery_test.go` kills a separate applying process after cache replacement and checks
restoration from a fresh process. `gateway_recovery_live_test.go` repeats that interruption both
after cache replacement and after the actual candidate nft table loads, then requires the recovered
cache/render/kernel generations to agree. The unit fixtures cover writer failures after rename and check
that failed kernel restoration produces a degraded phase.

`gateway_flows_test.go` runs the model against a sanitized Docker/ufw ruleset: container raw drops,
translating nat chains and mark-only mangle rules clear a forward to a container; explicit port drops
block with their rule position; named and literal source drops restrict; a mark accept admits only
after the mark is set; jumps are followed; `limit` and `xt` matches stay unknown; a local target
crosses input, not forward; a mapping's inbound half is modeled. `TestLiveFlowModelReadsRealIptablesShapes`
builds the same iptables-nft shapes in a namespace and checks the gate against nft's own JSON.
`gateway_telemetry_test.go` and the live `TestLiveExceptionsExpireInTheKernelAndTotalsSurviveReloads`
cover generations, in-place resets, vanished rules, persistence across a restarted recorder and real
handle changes; `conntrack_netlink_test.go` decodes kernel-shaped messages and
`TestLiveConntrackNetlinkInANamespace` dumps, reads statistics and revokes a real session as root
inside its own namespace.

These checks establish the supported local policy and resource behavior. They do not measure
provider filtering, application reachability, or traffic outside the controlled namespace.
