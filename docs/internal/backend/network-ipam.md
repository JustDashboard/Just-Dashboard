# Shared address planning

The shared IPAM planner is additive planning metadata for this one server. It does not reserve
native Docker, WireGuard, interface, namespace or provider state, adopt a foreign resource, or
replace any native owner. All planner endpoints require `system.admin`. Changes are audited;
reservation release and pool retirement use the existing destructive-action gate.

## Pools, allocation and overlap

`/network/ipam` shows immutable IPv4/IPv6 pool prefixes and allocation widths, held reservations,
exact decimal block counts, current native observations and per-source coverage. SQLite transactions
serialize allocation and reservation handoffs. Allocation merges occupied intervals and jumps over
them; it never enumerates a large IPv6 space. Utilization counts the union of observed and planned
blocks, so an observed allocation with a held plan is counted once. Candidate counts describe the
readable snapshot and planning rows, not verified free native space.

The fresh inventory reads Docker Engine IPAM, host addresses and both families of all-table
non-default routes, configured WireGuard addresses and non-default peer selectors, kernel WireGuard
selectors, this host's Tailscale offers/approved routes and named namespace addresses. It does not
query WireGuard private keys. Prefixes from isolated namespaces may overlap intentionally; planning
conservatively avoids them without asserting connectivity. Default route/peer selectors are
forwarding policy rather than allocation evidence.

Every failed family or owner read remains unreadable evidence. Provider allocations, remote and
unnamed/container-only configuration, namespace routes and foreign manager scope remain unknown.
No known overlap proves neither provider nor foreign absence. Creating a reservation with incomplete
coverage requires explicit acknowledgement; the reservation retains the exact unknown source/state
set. Native creation rechecks current evidence and refuses a newly failed source that was not
acknowledged. Foreign writers can change native state after a snapshot; native owners still validate
at mutation time.

The retained bounds are 64 pools, 2,048 reservations, 4,096 observed prefixes and 128 coverage rows.
Released reservations are eligible for pruning after 30 days; active allocations are never pruned.
Native inspection bounds Docker networks to 512, named namespaces to 64, WireGuard files to 256, each regular non-symlink
file to 1 MiB and total inspected configuration to 8 MiB. Bounds create explicit unknown coverage.
Retired pool records remain immutable history, including their unique prefix.

## Native handoffs

A reservation records an exact owner, intended resource name and prefix. A supported creation form
sends selected reservation IDs with that unchanged native tuple. The backend checks the IDs, owner,
name, prefix, active state, newly unreadable coverage and fresh known overlaps before asking the
existing native owner to create. Explicit native prefixes also cannot borrow a held reservation by
omitting its ID. Existing automatic native allocation without explicit prefixes remains outside
planner guarantees.

The planner atomically moves a selected reservation to `handing_off` before native work. A returned
native identity becomes `observed`; an error, missing response or backend restart becomes
`review_required`. All three states still hold the allocation. Failed planning annotation after a
successful native create must not prompt automatic replay of creation. Review the native owner and
its current identity before explicitly releasing a plan; release never deletes or changes a native
resource. There is no automatic native retry, cleanup, reconciliation or adoption.

Docker and WireGuard creation use complete owner-specific handoffs. WireGuard reservations must
fit the native interface-name rules, private IPv4 /16–/29 bounds and unique-local IPv6 /64 bounds.
Picking an IPv6 plan opts addressing in and leaves IPv6 exit off. Random/default native allocation
cannot match an explicit selected reservation. Interface/namespace records remain advisory plans
and provider ranges remain declared plans. A form link or reservation by itself is not evidence that native configuration was applied.

Creation forms treat an incomplete planning response as a failed read. They retain a selected name,
prefix and planning identity through a later failed refresh and refuse the selected handoff until
current inventory is readable. The picker accepts only a current reservation or the explicit
unselected option; synthetic empty form events cannot erase a selected plan.

## Verification

Focused Go tests cover concurrent IPv4/IPv6 allocation, covering-prefix jumps over enormous IPv6
space, exact utilization unions, incomplete and changed coverage, owner/name/prefix mismatches,
interrupted and failed handoffs, held allocations, additive database upgrades, admin route gates,
audits and family/namespace/configuration read failures. Frontend logic tests verify scope/status
wording and typed reservation links. Browser acceptance requires a freshly built integrated server;
it is not inferred from static checks. No verification step changes production host interfaces.
