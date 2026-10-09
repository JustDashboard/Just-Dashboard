# Native network-manager profiles

The native profile adapter keeps the host's existing NetworkManager, systemd-networkd or netplan
owner. It never adds a dashboard spec entry or installs a second persistent manager profile. Its
current surface edits IPv4/IPv6 addressing methods, per-family static addresses, DNS servers and
domains, and explicit unicast routes in an existing selected persistent profile. The full P11 scope
also includes structural bond and supported VRF editing; that work and its acceptance remain open.
Its prepared owner/member scope is described in [native structure preparation](network-native-structure.md).
Existing virtual/controller/member profiles remain refused until saved, loaded, applied and kernel
topology are verified together; their observed relationships are still reported.

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
The running NetworkManager image is also inspected for the Ubuntu Netplan writer integration.
That build migrates persistent origins while restoring a native checkpoint. Existing keyfile origins
on that build remain refused. A selected authored Netplan origin instead uses the independent
exact-origin journal: it restores the exact YAML and selected generated keyfile and reactivates the
same UUID through the pinned native owner, without calling the vendor checkpoint rollback writer.
Closed NetworkManager name/UUID metadata is supported; arbitrary passthrough remains refused.
A pass on an unpatched keyfile build does not cover this separate Ubuntu strategy.

The observed device index, MAC, kind, master, members, bond mode and VRF table are part of the transaction
contract. Observing those relationships does not prove that reactivating the saved profile will
retain them. Structural profile validation and transactions remain required before those controls
can be enabled; arbitrary native topology takeover is not authorized by an L3 intent.

## Temporary apply and durable cleanup

Native writes always require a positive authenticated pending owner and independently armed recovery.
Immediate requests are refused before taking a recovery lock or retrying prior terminal cleanup.
The browser sends pending apply even when its ordinary managed-network preference is off. Before
activation, the mode-0600 journal contains a closed native recovery command with exact selected file
snapshots, candidate/rollback staging identities, owner bus/boot identity and any native checkpoint.
The standalone recovery executable must advertise `jd-native-manager-v2` before admission. The new
closed strategy vocabulary cannot be admitted by a previously installed v1 helper. Recovery retains
the earlier v1 journal's checkpoint strategy and refuses a migrating writer rather than converting
that existing journal to a different recovery method.
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

`native_manager_recovery_test.go` uses bounded root-owned file fixtures and injected storage failures
to check durable confirmation before cleanup, cleanup retry after checkpoint release, foreign-stage
preservation, next-change refusal, old-bus object refusal and conservative boot-epoch recovery.
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
