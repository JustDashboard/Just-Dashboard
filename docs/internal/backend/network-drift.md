# Network drift and boot evidence

`GET /api/v1/network/drift` is a read-only inspection for authenticated accounts
with `read`. It never installs or enables a unit, fetches a blocklist, creates a
recovery lock, applies a batch, or reconciles a kernel resource. There is no repair
mutation endpoint in this checkpoint.

The response keeps four sources separate:

- `savedGeneration` is SHA-256 of the exact saved `spec.json` bytes.
  `canonicalGeneration` identifies the normalized configuration serialized in the
  format used by managed commits. Both describe the saved configuration.
- `change.generation` describes the recovery journal's candidate. A recovered
  journal retains its candidate identity while its snapshots restore an earlier
  configuration; that difference is not evidence of drift.
- `files` compares each owned render with its expected bytes and reports expected
  and observed digests. An enabled fetched list with unreadable cache prevents
  gateway rendering; other renders remain independently inspectable.
- `runtime` compares actual managed namespaces, devices, addresses, routes,
  policy rules, saved sysctls, shaping, gateway structure and admission rules.
  `blocklists` carries the existing separate cache, rendered and runtime set
  generations. Additional native devices, addresses and routing entries are not
  selected for cleanup.

When independent recovery is configured, inspection also checks its owned boot
unit render and the required executable's presence, digest and mode. A missing
helper is a known boot dependency failure. It never executes an unfamiliar
replacement to test it; an executable's build identity and ability to run remain
unknown without separate installation/self-check evidence.

Each observation has an identity, domain, resource, coverage, reason, ownership
and repair eligibility. `matching` requires observed facts within the stated
coverage. `missing` requires a successful valid inventory proving absence.
`drift` is a known mismatch; `conflict` is an occupied identity or foreign file.
`unreadable` is a failed or malformed read; `unknown` is incomplete comparison
coverage. `not_required` describes an optional resource that is unnecessary.
An empty, null or malformed command response never proves absence.

Device checks include kind, configured MTU, administrative state, master and
exposed type-specific attributes. Veth peer connectivity and GRE key semantics
remain unknown when this inventory cannot establish them. Routes compare source
addresses and reject multipath or additional unhandled attributes as evidence of
exact equality. Policy rules with unsupported selectors remain unknown; several
rules at one managed priority conflict. A different kind of device, route or
policy rule occupying a saved identity is not proposed for replacement.

Gateway inspection detects missing tables, sets, chains, changed base-chain hook,
priority or policy, and changed rule counts or owned comments. Its `structure`
coverage does not certify arbitrary nft rule expressions: retaining a forward's
comment does not prove that its DNAT target is unchanged. Such a structurally
present gateway remains `unknown`. Exact blocklist set comparison and all active
admission families retain their existing stronger checks.

`checkedAt` and `finishedAt` bound the observation window. The service snapshots
the saved configuration and journal under its in-process mutation mutex, then
releases the mutex before reading the kernel. It rereads both sources at the end.
A change makes `consistent=false`, the aggregate result unknown, and the repair
plan blocked. Kernel observations are not an atomic snapshot across commands.
An absent, invalid or unsupported saved spec leaves desired state unknown.

## Boot evidence

`boot` reports systemd's loaded fragment, drop-ins, persistent enablement and
whether it needs a reload, separately from the on-disk unit digest. The owned boot
unit can be enabled while its file or loaded definition has drifted.

`boot.execution` reports systemd's measured last activation in the current boot:
raw timestamp strings retain their stated time zone, monotonic microseconds are
reported without conversion, and the boot ID and invocation ID identify the
evidence when available. An unrecorded activation has no invented timestamps,
exit code or result. In particular, systemd's default `Result=success` for a
never-run or missing unit proves nothing.

The boot unit deliberately ignores individual command errors so other domains
can continue restoring. Inspection checks available `ExecStart` command exit
results and reports a failed ignored command even if the overall unit result is
success. It reports success only with a measured finished activation and complete
command outcomes matching the number of commands in the owned file. Missing
outcomes remain unknown.

`bootTrigger=unknown` is deliberate: systemd's current activation properties do
not establish whether a boot transaction, daemon restart or operator started
the unit. A measured successful activation does not establish reboot acceptance.
Actual reboot and daemon restart acceptance remain pending until separately
exercised and recorded.

## Owned repair plan

`repairPlan` is generation-bound, typed advice with `executable=false`. It has
stable item and observation IDs, actions, reasons, managed dependency IDs,
preconditions, exclusions and global blockers. Known missing or changed owned
renders and runtime resources can be proposed. A missing native dependency must
be restored through its native owner; an unknown, unreadable or conflicting
resource is excluded. A pending or degraded recovery journal, unreadable journal,
unreadable spec or concurrent configuration change blocks the whole plan.

This plan authorizes no command. A future selected repair apply must revalidate
the saved generation, selected observations and ownership; enforce `system.admin`
and destructive confirmation where appropriate; audit the mutation; and use the
existing path guards, durable journal and pending-confirmation recovery model.
There is no automatic reconcile daemon or foreign-resource cleanup.

## Reporting page

`/network/drift` uses the reading register to show dated desired, rendered and
runtime evidence, ownership conflicts, and measured unit activation separately
from reboot attribution. Summary counts include spec, journal, boot-unit and
enabled blocklist readings. Failed refreshes retain dated evidence and disable
repair selection. Reviewed selections expire when generation, ownership,
selected comparison bytes or journal facts change; polling time alone does not
invalidate identical evidence. The initial page offers ordinary plan review and
no execution control while the plan is non-executable.

Focused regression checks:

```sh
cd backend
GOMAXPROCS=2 go test ./internal/netx ./internal/api -run '^(TestDrift|TestNetworkDrift)'
JD_NETNS_LIVE=1 GOMAXPROCS=2 go test -race ./internal/netx -run '^TestLiveDrift'
```

The live fixture changes and removes managed resources through real `ip` commands
inside a disposable namespace, verifies observed drift, and confirms inspection
neither recreates the managed resources nor changes a native device. It proves
read-only CLI drift detection, not reboot or daemon restart acceptance.
