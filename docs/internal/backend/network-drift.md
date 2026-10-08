# Network drift and boot evidence

`GET /api/v1/network/drift` is a read-only inspection for authenticated accounts
with `read`. It never installs or enables a unit, fetches a blocklist, creates a
recovery lock, applies a batch, or reconciles a kernel resource. Selected repairs
use a separate administrator mutation endpoint described below.

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

`repairPlan` is generation-bound, typed advice with per-item execution scope. It has
stable item and observation IDs, actions, reasons, managed dependency IDs,
preconditions, exclusions and global blockers. Known missing or changed owned
renders and runtime resources can be proposed. A missing native dependency must
be restored through its native owner; an unknown, unreadable or conflicting
resource is excluded. A pending or degraded recovery journal, unreadable journal,
unreadable spec or concurrent configuration change blocks the whole plan.

`POST /api/v1/network/drift/repairs` accepts `generation` and `selections`, each
containing the server-produced item `id` and `reviewToken`. It requires
`system.admin` and `s.destructive`, and audits `network.drift.repair`. Neither
client paths nor command argv are accepted. A token grants no authority: apply
rereads generation, exact expected/observed bytes, file mode/device/inode,
ownership, unit fragment/overrides and selected chain contents while holding
both the service mutex and independent recovery flock. Stale, unreadable,
foreign or duplicate selections are refused.

Execution covers only the six core render files (links, IPv6 rules, shaping,
gateway, sysctl and ordinary network unit) and selected required admission
chains. File repairs preserve modes and refuse symlink path components. Their
exact before/after contents are included in the review. They change saved boot
inputs; they do not replay the corresponding kernel objects. A selected ordinary
unit file triggers only a verified daemon reload; enablement remains separately
observed. Boot-file writes require established ordinary-unit ownership and a
settled execution; an executing or unreadable unit cannot authorize them.
Admission repairs remove only the canonical owned mark/comment rule
and put exactly one at the selected chain's beginning, with authoritative
gateway-policy and client/anchor-path checks. Unselected chains and families are
unchanged. Other plan items remain non-executable advice.

An already installed independent recovery helper matching the current backend,
its private mode, and its loaded enabled owned recovery unit without overrides
are prerequisites. Apply reuses the existing helper self-check and arms the
independent watchdog before any attempted selected effect. Snapshots contain
only selected files and exact selected admission presence/positions. An
attempted rename followed by a reported sync failure is restored; an
unattempted foreign replacement detected during preflight is preserved.
Admission-only selection records `persistence=not_applicable` and
`boot=not_applicable`. Render selection records written persistence and keeps
boot execution unverified. The exact saved spec generation is unchanged.

This endpoint alone is added to pending-apply eligibility; direct gateway
admission repair, DNS, VPN and firewall retain their existing contracts. When
pending apply is requested, confirmation still requires a fresh authenticated
reconnection response and the existing deadline recovery. There is no automatic
reconcile daemon or foreign-resource cleanup. Both locks cover dashboard and
independent recovery writers; native writers do not take these locks, so
inspection and immediate pre-effect checks describe an observation window.

## Reporting page

`/network/drift` uses the reading register to show dated desired, rendered and
runtime evidence, ownership conflicts, and measured unit activation separately
from reboot attribution. Summary counts include spec, journal, boot-unit and
enabled blocklist readings. Failed refreshes retain dated evidence and disable
repair selection. Reviewed selections expire when generation, ownership,
selected comparison bytes or journal facts change; polling time alone does not
invalidate identical evidence. The initial reporting page offers ordinary plan
review; execution controls require the separately integrated selected repair UI.

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
Selected repair regressions also prove exact file identity/mode binding,
attempted-write rollback, fresh-process file recovery after writer death, cache
candidate review invalidation, and real selected-chain admission/undo in a
disposable namespace. The latter records watchdog prerequisites and expires the
fixture journal explicitly; it does not prove a real systemd timer or reboot.
