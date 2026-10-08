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

Limits and blocklist drops keep working through independent filters. Removing or disabling managed
translations remains possible when the host has switched to an unsupported policy owner.

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

`Service.RepairGatewayAdmission` reasserts only the fixed connection-mark/comment rules and verifies
them afterwards, under the network mutation lock and current gateway compatibility guard. It does
not replace a table or foreign chain policy. The API route must use the existing destructive
capability, mutation budget and audit wrapper. Boot restoration uses the generated service's existing
admission commands; a runtime repair does not replace ownership or boot recovery.

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

These checks establish the supported local policy and resource behavior. They do not measure
provider filtering, application reachability, or traffic outside the controlled namespace.
