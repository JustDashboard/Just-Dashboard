# Recovering network changes

`Service.commit` protects managed links, addresses, routes, rules, namespaces, forwarding, gateway,
protection, shaping, owned firewall-table and egress group changes with
`/etc/just-dashboard/network/change.json`. Managed bridge-port VLAN memberships and VXLAN flood ends
are part of the spec; their undo is typed `bridge vlan`/`bridge fdb` commands derived from the
previous spec, and the boot unit restores them with conditional failure-tolerant `bridge` lines.
Native-owned link state, MTU and bridge membership changes use the same journal but remain
runtime-only. An [egress group](network-egress.md) change — creation, edit, enable, disable, removal
and every switch, the monitor's included — journals typed `ip route replace`/`rule add`/`rule del`
commands that take each changed group's slot back to its previous decision, and reloads (or
removes) the previous `egress.nft` pinning from the restored file. ufw and firewalld changes are
enrolled through `ProtectFirewallChange`: a check pass runs first under the lock, so a refused
request opens no journal; then the journal names the tool (`firewall`), snapshots only that tool's
own files and records a closed set of commands that put its switch back (`ufw --force
enable|disable`, `ufw reload`, `systemctl start|stop|enable|disable firewalld`, `firewall-cmd
--reload`; the unit command only when its prior state read as plainly enabled or disabled); a
journal naming any other file or command is refused before recovery starts. DNS and WireGuard
configuration and Tailscale preferences have separate owners and are not covered by this journal.
Their existing synchronous rollback does not imply independent recovery.

[Selected persistent native profiles](network-native-managers.md) also use this journal with a
closed native owner/checkpoint recovery payload. Their addressing, DNS/domain and explicit-route
edits always require pending confirmation; they do not create dashboard-managed boot files.
Netplan/networkd recovery validates the exact authored and generated snapshots together. It retains
dormant authored automatic-domain/MTU policy across a manual transition and refuses unrelated YAML
changes before any restoration effect; the existing private undo versions and file snapshots carry
this scope without additional serialized fields.

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
- `runtime`, `persistence`, `boot` and `watchdog` remain separate. For native-owned runtime edits and
  host firewall changes, persistence and boot are `not_applicable`.
- `validation` (routing changes) lists what the change was checked against once applied — the
  client reply, the source-selected reply and every anchor and tunnel endpoint label — with the
  established connections it re-asked and those that now leave differently.
- Native profile changes expose `cleanup: pending | failed | complete` separately. A confirmed or
  recovered decision is saved before checkpoint release or rollback-stage deletion. Failed cleanup
  remains visible, preserves foreign stages and blocks replacement by the next journaled change.

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
change because its random ID must match; completed ordinary journals are no-ops, while terminal native
journals retry their idempotent owned cleanup. The standalone helper stays
in the namespaces in which systemd starts it; API-side host commands use `hostexec`.
Boot uses `--network-recover-boot`: after restoring files it reconstructs the prior managed link and
namespace dependencies before targeted undo. The [boot recovery guide](network-boot-recovery.md)
explains the ordering and cold-runtime acceptance. The ordinary timer never replays those unchanged
dependencies over a running host.

## Reconnect and confirm

An interactive administrator can request `X-JD-Network-Apply: pending` on covered link, routing,
forwarding, shaping, forward/NAT, protection and egress group mutations, including exceptions and
trusted-address notes. Previews, the forward target check, the pressure read and session revocation are not journaled
and do not accept it; revocation deletes connection-tracking entries, which no recovery can restore. The browser enables this after reading
independent-recovery availability; its preference appears in Network, while pending/recovery notices
remain visible throughout the dashboard. The response body stays compatible and the
`X-JD-Network-Change` / `X-JD-Network-Expires` headers identify the journal. Ordinary covered
API/no-header callers keep immediate saved behavior. Selected drift repairs and explicit download
SQM always require pending mode; their UI sends it even when the global preference is off, and
the backend refuses an immediate request before effects. Native-owned link runtime edits are covered;
Host firewall rule, switch, default, reset and plan changes accept it through their own journal.
DNS, namespaces, firewall logging, WireGuard/Tailscale and direct gateway admission-rule repair do not
accept pending opt-in. Selected drift admission repair uses its separately enrolled transaction.

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

`POST /network/changes/{id}/cleanup` requires the same account's administrator session and destructive
capability, and audits a retry of terminal native cleanup. Failed cleanup never authorizes rollback
of a durable confirmed candidate. Native checkpoint timeout holds and owner bus/boot identities are
private journal evidence; the native adapter guide explains their failure/restart boundaries.

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
