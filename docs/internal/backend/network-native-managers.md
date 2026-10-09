# Native network-manager profiles

The native profile adapter keeps the host's existing NetworkManager, systemd-networkd or netplan
owner. It never adds a dashboard spec entry or installs a second persistent manager profile. Its
current surface edits IPv4/IPv6 addressing methods, per-family static addresses, DNS servers and
domains, and explicit unicast routes in an existing selected persistent profile. The full P11 scope
also includes structural bond and supported VRF editing; that work and its acceptance remain open.
Its prepared owner/member scope is described in [native structure preparation](network-native-structure.md).
The isolated shipped helper-v10 unchanged-profile fixture is tracked in
[profile inode acceptance](evidence/native-manager-profile-inodes-v10-2026-10-09.md).
Existing controller/member profiles remain refused until saved, loaded, applied and kernel
topology are verified together; their observed relationships are still reported. Existing standalone
veth Ethernet profiles additionally require a reciprocal peer in the same inspected namespace.

`GET /network/native/managers` reports version capability and observed devices.
`GET /network/native/profiles/{device}` reports the selected owner, renderer, supported intent and an
opaque generation. Configured, current runtime and persistent boot enablement are separate evidence.
Boot enablement explicitly does not claim a measured reboot. Reads require an administrator session;
raw profile bytes, file paths, secrets, process/bus identities and checkpoints are never returned.

`PUT /network/native/profiles/{device}` accepts `{generation, intent}`. The optional typed `structure`
field is reserved for bounded existing-controller preparation; every structural write currently
returns a refusal before any profile or recovery effect. Each family has a method,
address/DNS/domain arrays, automatic DNS/route preferences and typed destination/gateway/metric/table
routes. Unknown fields, arbitrary paths and shell source are rejected. Limits are 16 addresses,
8 DNS servers, 16 domains and 32 routes per family. Reserved/protected route tables remain refused.
The mutation uses the ordinary destructive capability, rate limit and audit wrapper.

## Ownership and refusal

The draft adapter version ranges are NetworkManager 1.42–1.54 even minor releases, networkd
255–257 and netplan 1.0–1.2. A version range is an eligibility gate, not evidence that every release
and host combination has passed native acceptance. Missing/unreadable version evidence refuses edits.

The existing selected profile must have canonical persistent `/etc` ownership, root-owned trusted
parents and a bounded regular file with no symlink or writable group/world provenance. Native runtime
intent must agree with the selected saved profile. Multiple active owners, cloud-init regeneration,
vendor/runtime-only sources, missing profiles, networkd drop-ins, unsupported family/route encodings
and conflicting netplan origins remain explicit refusals. Docker, Tailscale and dashboard-managed
devices retain their own controls. Adoption or creation of an unmanaged profile is not supported.

NetworkManager is inspected through its native selected connection and applied settings, including
its UUID and exact interface binding. Its offline client stages a profile; the selected native owner
loads and activates it. networkd stages the selected `.network` file and reconfigures the device.
Netplan preserves its exact YAML origin, generates in a private root, then replaces only the selected
source and generated renderer artifact. It does not run a global `netplan apply`.
Native INI rendering preserves comments and intentional blank lines in retained sections. Its
terminal LF ends the last line, so repeated rendering of unchanged networkd address, DNS, domain and
route intent does not create another blank line. A missing final LF is normalized once.
The running NetworkManager image is also inspected for the Ubuntu Netplan writer integration.
That build migrates persistent origins while restoring a native checkpoint. Existing keyfile origins
on that build remain refused. A selected authored Netplan origin instead uses the independent
exact-origin journal: it restores the exact YAML and selected generated keyfile and reactivates the
same UUID through the pinned native owner, without calling the vendor checkpoint rollback writer.
Closed NetworkManager name/UUID metadata is supported; arbitrary passthrough remains refused.
A pass on an unpatched keyfile build does not cover this separate Ubuntu strategy.

The observed device index, MAC, kind, master, members, bond mode and VRF table are part of the transaction
contract. Observing those relationships does not prove that reactivating the saved profile will
retain them. A standalone veth profile records the peer name, index and MAC privately; both endpoints
must identify each other within one unique name/index inventory. When iproute2 emits reciprocal
resolved `link` names instead of `link_index`, those kernel-provided names resolve only to the exact
observed endpoint indices ([iproute2's link serializer](https://git.kernel.org/pub/scm/network/iproute2/iproute2.git/tree/lib/utils.c?h=v6.14.0#n1219)).
Any `link_netnsid` marker, duplicate name/index, nonreciprocal relation
or disagreement between a numeric index and resolved name refuses the pair. Generation, runtime inspection
and every recovery epoch check fence that pair. Missing or foreign peers preserve the journal and
refuse activation/restoration. Only a different verified kernel boot can rebind indices after exact
peer-name/MAC and reciprocal-relationship proof; this is not a measured reboot claim.
Structural profile validation and transactions remain required before those controls
can be enabled; arbitrary native topology takeover is not authorized by an L3 intent.

Runtime evidence also refuses DHCP/RA routes when automatic routes are disabled, and extra per-family
DNS servers when automatic DNS is disabled or the family is manual/disabled. Automatic acquisition
acceptance remains separate from these refusal checks.

networkd's existing per-protocol `UseDomains` policy is preserved independently of `UseDNS`: explicit
`yes` retains acquired search domains, `route` retains acquired routing domains and `no` refuses
acquired domains. The editor changes configured domain lists; it does not expose automatic-domain
policy controls. Duplicate, unknown or shared/inherited profile policy refuses admission. Unset
protocol policy is preserved, but a received domain cannot be attributed to an uncaptured daemon
default. Runtime inspection verifies the domain's DHCPv4/DHCPv6/NDisc source, provider address family,
enabled addressing method and search-versus-route placement; foreign/runtime-only and extra static
domains refuse agreement. This uses [systemd's separate domain policy](https://github.com/systemd/systemd/blob/v257/man/systemd.network.xml)
and [per-domain source/provider evidence](https://github.com/systemd/systemd/blob/v257/src/network/networkd-json.c).
Raw policy remains fenced by the exact prior/candidate snapshots and ownership generation. Staging
and recovery decoding refuse a change to that retained policy; the public intent is unchanged.

Authored standalone Netplan/networkd origins have a separate closed policy check. Their generated
shared `[DHCP]` block is accepted only when it agrees with that exact YAML origin, has the captured
`RouteMetric=100` and explicitly disables DHCP-provided MTU. Direct networkd shared policy, default
Netplan DHCP MTU, unknown overrides and per-protocol MTU/route/metric aliases remain refused. The
authored DHCP/RA automatic-domain and MTU values survive a manual transition even when generation
removes the inactive DHCP block. Staging and recovery compare the full remaining YAML semantics,
including every unselected definition, and verify both authored/generated before and candidate
intent. Netplan's separate RA DNS override is written for DNS suppression; this supported Netplan
contract cannot persist IPv6 automatic-route suppression, so that request is refused before staging
or applying a new candidate. IPv6 RA routes remain acquired when automatic DNS is suppressed.
The [v8 authored-policy proof](evidence/native-manager-netplan-v8-2026-10-09.md) records the measured
DHCPv4/SLAAC slice and its remaining platform limits.

NetworkManager's active [IPv4 domain and search properties](https://github.com/NetworkManager/NetworkManager/blob/1.52.1/introspection/org.freedesktop.NetworkManager.IP4Config.xml)
and corresponding IPv6 properties are read from the pinned unique owner and exact selected IP-config
object. Their bounded union covers DHCP domain-name (option 15) and search-list/RA domains without
duplicating one suffix. Invalid properties, object-family mismatches and malformed domains refuse
agreement. Extra active domains also refuse agreement for manual/disabled families or when automatic
DNS is disabled, matching the existing NetworkManager DNS policy.

## Temporary apply and durable cleanup

Native writes always require a positive authenticated pending owner and independently armed recovery.
Immediate requests are refused before taking a recovery lock or retrying prior terminal cleanup.
The browser sends pending apply even when its ordinary managed-network preference is off. Before
activation, the mode-0600 journal contains a closed native recovery command with exact selected file
snapshots, candidate/rollback staging identities, owner bus/boot identity and any native checkpoint.
The standalone recovery executable must advertise `jd-native-manager-v10` before admission, including
networkd automatic-domain preservation/provenance, both NetworkManager active domain properties and
selected-file/stage ownership checks before checkpoint rollback or restored-profile activation,
and exact authored/generated Netplan policy validation across automatic/manual transitions.
An older helper is refused before journaling
or arming a new native change. The private undo payload remains at v3 with its peer vocabulary;
the domain policy is already captured in existing file snapshots, so no additional raw policy is serialized.
Recovery preserves earlier v1/v2/v3 journal scope and strategies; a legacy checkpoint journal refuses a migrating writer
rather than converting that existing journal to a different recovery method.
Selected files are exchanged atomically with their staged candidates, retaining the displaced inode
in private staging. A concurrent native writer is verified after the exchange and preserved; it is
never overwritten by a candidate rename. Files and containing directories are synced before progress
is recorded. Terminal deletion first claims a stage through a no-replace rename to its deterministic
cleanup name, then verifies that captured inode and its bytes. A crash during that claim is retryable.

Verified non-migrating NetworkManager keyfile profiles add a checkpoint for the exact selected
device, with a native timeout in addition to the independent ninety-second journal watchdog.
Netplan/NetworkManager exact-origin recovery creates no native checkpoint: a lost reply to a
timeout-zero checkpoint would leave an opaque permanent object whose ownership cannot safely be
guessed from inventory. The independent watchdog must already be armed before any profile effect.
Exact-origin admission and recovery refuse foreign native checkpoints, a changed writer/strategy,
or any selected origin, UUID, byte or inode change outside the recorded transaction.
When the displaced authored inode is still retained in staging, rollback exchanges that exact inode
back into its selected name; reconstructed runtime artifacts retain their separately recorded boot proof.
Prior and candidate state are classified by their exact recorded inode and bytes together. Identical
bytes alone cannot identify a prior state: an unchanged candidate still has its captured candidate
inode and must return the retained authored inode during rollback. Equal-byte foreign inodes and
changed restore stages remain refusals before owner effects, including persistent new-boot rebinding.
Helper v10 supplies this correction while preserving the shipped undo versions 1/2/3 and their
owner/domain-policy interpretation. An older helper cannot satisfy the new admission capability.
Existing v8 source-specific native evidence remains attributed to v8; actual v10 owner/no-op
acceptance is a separate required check. Private structural v9 journals are outside this adapter.
Pending recovery verifies every selected candidate's recorded inode and bytes, both captured restore
stages and the absence of unexpected cleanup claims before asking a native checkpoint writer to act.
It repeats those checks at the checkpoint effect boundary and after that writer returns, before
independent restore or activation. Refusal also durably arms release of the exact saved, epoch-pinned,
device-scoped checkpoint and verifies its disappearance, preventing its automatic deadline from
later overwriting foreign edits. It retains every profile/stage and the degraded independent journal;
this release does not confirm or adopt the changed profile. A lost release reply remains `releasing`
until an exact inventory retry proves disappearance. An unverified scope, changed owner, or failed
release/storage operation reports an unresolved native deadline and preserves the available evidence;
foreign-edit containment is not claimed while that native writer may still act.
The pinned [NetworkManager checkpoint manager](https://github.com/NetworkManager/NetworkManager/blob/1.52.1/src/core/nm-checkpoint-manager.c)
dispatches timeout expiry through its rollback writer; destroying the exact object clears its timeout
callback without restoring profiles. The local native fixture must measure that deadline containment
separately from explicit rollback refusal.
A native timeout or lost rollback reply may already have
restored prior bytes with a new inode. That inode permits only read-only completion after complete
saved, loaded, applied, kernel and owner agreement; it cannot authorize another rollback, exchange
or activation. Independent exact-origin recovery and persistent prior profiles after a boot change
require their captured original or restore inode.
Confirming first verifies current native intent and
the returned dashboard challenge. It durably records the checkpoint timeout hold while independent
pending recovery still owns the change, then records `confirmed`. Only after that decision reaches
stable storage may the helper release the checkpoint or remove rollback stages.

`cleanup: pending | failed | complete` is independent of the confirmed/recovered decision. Cleanup
checks exact inode ownership and expected bytes, saves progress, and is idempotent across process
death or failed journal writes. A foreign stage is preserved. Startup, an old timer with the matching
change ID, and admission of the next ordinary or native journal all retry terminal cleanup. Failed
cleanup preserves its journal/evidence and blocks journal replacement. A confirmed candidate is
never changed into rollback merely because cleanup failed.

`POST /network/changes/{id}/cleanup` retries only the current account's native confirmed/recovered
change under the destructive audit wrapper. Its persistent notice follows dashboard navigation and
cannot be dismissed while cleanup remains incomplete.

Native unique D-Bus names are pinned within a saved bus/boot epoch. The method-level bus ID and the
authenticated transport GUID are recorded separately; they are not assumed equal. Every
transaction-effect transport also requires that saved authentication GUID before sending any method. Restarting the
system bus between preflight and an effect therefore refuses even if names or object paths are reused.
The epoch also verifies that the recorded unique owner still holds its well-known renderer service
before any checkpoint, activation or recovery effect. A still-live prior unique destination is
insufficient after service handoff; the journal and selected files are retained for owner review.
An observed new kernel boot
permits conservative revalidation of the same persistent owner/device contract before rollback;
old checkpoint object paths are never used on that new boot. Netplan's generated `/run` artifact
and rollback staging may be reconstructed from the durable journal only after this boot proof and
exact selected-origin/byte checks. A changed bus within the same boot remains a refusal that preserves
the evidence for native-owner review.

## Local acceptance

The [assembled editor/DNS checkpoint](../../audits/2026-10-08-network-capability-report/implementation-evidence/native-dns-integrated-acceptance.md)
records the matching production build, selected package tests and retained UI controls. The
[automatic owner record](evidence/native-manager-automatic-2026-10-09.md) separately attributes
actual DHCPv4/SLAAC and final-helper rollback/deadline proof to frozen source. The
[exact production assembly proof](evidence/native-manager-v6-assembly-2026-10-09.md) passes those
direct NetworkManager/networkd fixtures against the assembled owner-reader and v6 recovery files.
The [v8 authored Netplan proof](evidence/native-manager-netplan-v8-2026-10-09.md) measures the explicit
standalone policy above. Default Netplan DHCP MTU, DHCPv6 acquisition, wider platform owners and host
reboot remain open; source ancestry is not interchangeable.

Current v8 recovery has a separately observed equal-byte ownership defect. When a staged candidate
has the same bytes as the saved prior profile but a different captured inode, byte-first
classification can mistake the candidate for the prior file. Exact-origin recovery then refuses
the known candidate and retains a degraded journal; checkpoint recovery can instead treat it as an
unrecorded restored inode and skip restoration of the retained authored inode. Foreign-file guards
remain enforced. The correction and unchanged-intent native acceptance are still pending; the
nonidentical-profile proofs above do not cover this case. Direct networkd also currently adds a
synthetic trailing blank line on each INI parse/render, so repeated unchanged renders are not byte
stable. Its parser correction and source-matched native rerun remain required.

`native_manager_recovery_test.go` uses bounded root-owned file fixtures and injected storage failures
to check durable confirmation before cleanup, cleanup retry after checkpoint release, foreign-stage
preservation, next-change refusal, old-bus object refusal and conservative boot-epoch recovery.
`native_manager_recovery_preflight_test.go` also checks foreign selected/staged bytes and inodes,
missing restore stages, unexpected claims, a foreign write during checkpoint inspection and
read-only restrictions on an unrecorded restored inode before rollback/activation, owned deadline
release after refusal, uncertain release retry and foreign checkpoint-scope refusal.
`native_manager_io_test.go` checks the real busctl distinction between property values and method
return argument arrays, and that effect argv pins both the transport GUID and unique daemon owner.
Admission fixtures also verify that a refused immediate request creates no recovery state and leaves
a prior confirmed native journal, selected profile and staging bytes intact. Mounted `Server.Routes`
fixtures enforce administrator-session reads/writes, token refusal, unknown-path-field rejection and
the pending header's positive authenticated owner before intent validation.

The opt-in `TestNativeManagerOwnerLive` fixture builds the actual standalone recovery executable and
runs selected native daemons in its own net/mount/PID namespace. `/etc`, `/run` and `/var/lib` are
private tmpfs mounts, sysfs reflects its network namespace, and its private system bus accepts only
fixture processes. NetworkManager receives explicit private state/intern/pid paths. It tests
the actual transport GUID and mismatched/prior-bus authentication refusal across an actual restart,
owner activation, static dual-family addressing/DNS/explicit-route evidence, independent executable
rollback, reconnection confirmation and applying-backend process death. The separate generated-origin
Ubuntu case also injects failed durable confirmation storage, process death after durable confirmation
and a lost terminal cleanup outcome, then invokes the actual fresh recovery executable. Timer admission is stubbed
in this owner-data fixture; real systemd timer dispatch has separate acceptance. DHCP/SLAAC acquisition,
cold runtime reconstruction and structural bond/VRF native acceptance remain required separately.

Run a bounded owner fixture from `backend/internal/netx` after compiling the race-enabled netx test
binary. `JD_NETNS_LIVE=1` opts in, and `JD_NATIVE_MANAGER_NM_ROOT` identifies the independently verified
NetworkManager/nmcli userland used for this local fixture. No package installation or production owner
restart is part of the fixture. Use a workspace-local `TMPDIR` for its helper build.
`JD_NATIVE_MANAGER_CASE=netplan-NetworkManager` selects authored Netplan with that renderer.
`JD_NATIVE_MANAGER_NM_ORIGIN=refuse-migration` explicitly requests refusal acceptance for an Ubuntu
keyfile origin; this is not counted as supported editing.
`JD_NATIVE_MANAGER_CHECKPOINT_CREATE_LOST=1` with `JD_NATIVE_MANAGER_CASE=NetworkManager`
selects a separate real keyfile create-reply-loss fixture. Its worker discards the actual create reply
before any profile replacement/activation; helper and next-change attempts must preserve the opaque
inventory and journal. The native object has the production 120-second timeout. Only its measured
automatic disappearance permits the fresh helper to finish exact rollback/cleanup; no inventory
entry is guessed or destroyed by the adapter. Use the verified nonmigrating NM userland for this case.
