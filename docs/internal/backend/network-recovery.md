# Recovering network changes

`Service.commit` protects managed links, addresses, routes, rules, namespaces, forwarding, gateway,
protection and shaping changes with `/etc/just-dashboard/network/change.json`. Native-owned link
state, MTU and bridge membership changes use the same journal but remain runtime-only. DNS and
WireGuard configuration, Tailscale preferences and netsec firewall changes have separate owners and
are not covered by this journal. Their existing synchronous rollback does not imply independent
recovery.

## Durable phases

The journal is a private mode-0600 regular file containing the candidate generation, random change
ID, prior changed-file contents/modes, observed runtime undo argv and reached phases. A file lock
serializes the API process with the recovery process; process death releases it. Candidate renders
are validated before mutation. Fetched blocklist caches are staged, included in the snapshot, and
written only after the journal exists. Every atomic file replacement fsyncs its containing directory.
The serialized snapshot has a 32 MiB limit and refuses an oversized change before runtime mutation.

`PersistenceStatus.change` exposes phase evidence without the private snapshots or argv:

- `prepared`, `runtime_applied`, `persisted`, `saved` record actual apply/save progress.
- `awaiting_confirmation` means a temporary apply is waiting for a fresh dashboard response and an
  explicit owner confirmation; `confirmed` records that interactive transport confirmation.
- `recovering`, `recovered`, `degraded` describe recovery attempts; failed steps remain visible and an
  unresolved journal prevents another journaled apply.
- `boot_degraded` means the immediate-mode candidate was applied and saved but enabling its normal
  boot unit failed. This is not a successful boot test.
- `runtime`, `persistence`, `boot` and `watchdog` remain separate. For native-owned runtime edits,
  persistence and boot are `not_applicable`.

`saved` does not prove that a browser or application can reach the host. The kernel path guards still
check client/source/anchor routes, and deliberately permit source changes that preserve device and
gateway. Those checks cannot establish the actual allowlist, tunnel transport or provider path.

## Independent execution

The production network module opts into independent recovery. On systemd hosts the backend copies
its own static executable to `network-recovery` (0700), checks that it runs in the host mount
namespace, installs an owned recovery unit and enables it before runtime mutation. A foreign unit
at that path is refused. The unit is checked/enabled again on subsequent applies. A transient
`systemd-run` timer invokes the helper after ninety seconds using explicit argv. Failure to install
or arm it refuses the change before applying it. Non-systemd hosts report independent recovery as
unsupported; immediate synchronous recovery is still available.

`--network-recovery-check` only checks executable startup. `--network-recover <absolute-directory>
<change-id|pending>` starts before normal server configuration and operates without the API,
database, credentials or container. It reads the private journal, validates snapshot paths/tools,
restores prior files, runs typed undo and records all failures. An old timer cannot undo a later
change because its random ID must match; completed journals are no-ops. The standalone helper stays
in the namespaces in which systemd starts it; API-side host commands use `hostexec`.
Boot uses `--network-recover-boot`: after restoring files it reconstructs the prior managed link and
namespace dependencies before targeted undo. The [boot recovery guide](network-boot-recovery.md)
explains the ordering and cold-runtime acceptance. The ordinary timer never replays those unchanged
dependencies over a running host.

## Reconnect and confirm

An interactive administrator can request `X-JD-Network-Apply: pending` on covered link, routing,
forwarding, shaping, forward/NAT and protection mutations. The browser enables this after reading
independent-recovery availability; its preference appears in Network, while pending/recovery notices
remain visible throughout the dashboard. The response body stays compatible and the
`X-JD-Network-Change` / `X-JD-Network-Expires` headers identify the journal. API/no-header callers keep
immediate saved behavior. Native-owned link runtime edits are covered; DNS, namespaces, firewall,
WireGuard/Tailscale and admission-rule repair do not accept pending opt-in.

An opted apply refuses before kernel mutation unless the independent helper and watchdog arm.
Its ninety-second deadline is recorded before apply, and a late apply rolls back. A pending journal
blocks another journaled mutation. Boot-unit failure in pending mode also rolls back rather than
retaining an unconfirmed candidate.

`GET /network/changes/current` and `POST /network/changes/{id}/verify` / `/confirm` require an admin
session. Verify issues a fresh random challenge after apply, stores only its digest, and binds it to
the owner, authenticated session and observed source IP. Confirm must return that received challenge
within thirty seconds and before the apply deadline. Another account, stale challenge, changed
session/source or old ID cannot confirm. A changed session/source can obtain its own new response.
Only explicit confirmation terminates the pending watchdog; polling does not confirm. The
`/recover` route additionally requires the destructive capability and restores the owned pending
generation with a bounded cancellation-independent context.

Confirmation establishes a returned dashboard response, not application, tunnel or provider health.
The UI retains pending evidence during disconnection and waits for recovered/degraded host evidence
after the deadline instead of treating a browser countdown as successful recovery.

Recovery attempts all valid steps. Missing tools, lost permissions, removed native dependencies or
failed filesystem writes can leave it degraded. Native ownership remains external; the journal
does not promise to reconstruct an arbitrary foreign tc hierarchy or network-manager profile.

## Local evidence

Package tests kill an applying child process after runtime mutation, first render, spec replacement
and boot setup, then recover from a fresh process. Namespace tests exercise real device/cache/nft
changes after process death; they use temporary namespaces and directories. Run the required lane
from `backend/`: `JD_NETNS_LIVE=1 go test -race ./internal/netx -run Live -count=1`.

These checks do not establish provider reachability, a production reboot or compatibility with every
systemd/native-manager configuration. The [report ledger](../../audits/2026-10-08-network-capability-report/implementation-status.md)
retains those acceptance requirements. No API-only success or green unit test raises the historical
report score to 10/10.
