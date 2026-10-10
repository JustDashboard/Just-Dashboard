# Native manager acceptance record

P11 remains in progress. Typed persistent L3 adapters and durable cleanup tests are implemented;
existing bond mode/member and supported VRF table/member transactions and automatic-address native
acceptance are still required. The version eligibility ranges do not imply acceptance across all
versions. Creation/adoption remains refused for this adapter.

The local owner fixture runs its actual recovery executable and native daemons in private network,
mount and PID namespaces. Timer admission is stubbed in that fixture and is not timer-dispatch proof.
Each rerun preserves its raw bounded output outside the checkout.

The first owner run failed and exposed a fixture containment omission: NetworkManager's default
`/var/lib/NetworkManager` was not private. The only newly created directory and comment-only internal
configuration were verified by inode, creation time, root ownership, exact directory inventory and
expected bytes before their exact cleanup. No production native owner or unrelated file was removed.
The fixture was corrected to mount private `/var/lib` as well as `/etc` and `/run`, and to supply
explicit private NetworkManager state, internal configuration and PID paths before further runs.

Subsequent failures exposed real busctl property/method JSON framing, private-bus account admission,
networkd runtime-directory ownership, explicit offline boot-enablement inspection, and a recovery
helper too large for the private 64 MiB runtime mount. Those issues were corrected; the helper is now
the exact compiled artifact bound read-only into the private runtime directory.

The GUID experiment also disproved equality between the method-level `org.freedesktop.DBus.GetId`
and the authenticated GUID on the actual private daemon. Recovery records both separately and pins
the transport GUID obtained by `busctl status` during effect authentication. Actual mismatch/restart
acceptance passed in the actual private PID/mount/network namespace. The all-zero GUID is refused
because sd-bus treats it as an unspecified expected ID. Credential augmentation is disabled for
the status inspection; authenticated kernel credentials are sufficient for this GUID read.

The subsequent actual owner run passed networkd 257 and netplan 1.1.2 static dual-family addresses,
DNS/domains, metric/table routes, independently executed rollback, authenticated confirmation and
applying-backend death recovery. Its Ubuntu NetworkManager 1.52.0 build applied the candidate but
crashed during native checkpoint rollback in libnetplan YAML serialization. Inspection of the
Ubuntu source patch confirmed that this persistent writer migrates keyfile origins to Netplan YAML.
The adapter now refuses that running-image feature before admitting edits; covering authored
Netplan/NetworkManager profiles without origin migration required a separate strategy. The failure is retained and
is not counted as a successful restore.

Final core run 10 passed against the conservative field/ownership guards: the isolated child took
10.51 seconds and the complete wrapper/helper build took 30.84 seconds. networkd and netplan repeated
the full static transaction/death flow. The actual Ubuntu writer image was refused before any
profile, journal, checkpoint or authored-origin mutation; this is refusal acceptance, not supported
editing. All owned private children were absent afterwards, only the original production networkd
remained, and the host `/var/lib/NetworkManager` was still absent. Raw output is retained as
`native-owner-run-10.log` in the task's external acceptance artifacts.

Run 11 passed the separate actual Ubuntu 1.52.0/Netplan 1.1.2 generated-origin strategy in
28.75 seconds in the private namespace, 52.10 seconds including its helper build. The independent
journal creates no native checkpoint and therefore has no opaque timeout-zero creation-reply gap.
It restored actual loaded/applied/kernel dual-family intent after rollback and applying-process
death, retained pending recovery after a failed confirmed save, and preserved the candidate after
process death following a durable confirmed decision and after a lost terminal cleanup outcome.
The final verifier was then strengthened to compare exact authored/generated paths, bytes and
recorded inodes as well as UUID and native intent. That stronger final source still requires its
own native rerun; run 11 is not claimed as that rerun. Raw output is `native-owner-run-11.log`.

Strict final core run 12 passed against `20248233` with the prepared structural request still refused.
It took 23.39 seconds in its namespace, 44.25 seconds including the actual helper build. Explicit
assertions checked the fixed UUID, exact YAML/generated-file scope, original retained inodes and
prior bytes after fresh helper rollback, plus the unchanged one-file authored YAML inventory.
Loaded/applied/kernel intent and all confirmation/death/cleanup fault windows also passed. Owned
children were absent afterwards, production networkd PID 883 was unchanged, and the host
`/var/lib/NetworkManager` remained absent. The preserved `native-owner-run-12.log` SHA256 is
`545535ecabb74cc243ab780c048cd0adb0dfb5530d13c62c3e15d720f27534b6`.

Separate run 13 passed the actual unpatched Debian NetworkManager 1.52.1 keyfile strategy against
`20248233`: 15.49 seconds in its private namespace and 24.61 seconds including the helper build.
It exercised native checkpoint rollback, confirmation timeout hold/release, failed confirmed storage,
applying-process death, death after durable confirmation and lost terminal cleanup progress with the
actual fresh helper. All owned children were absent and the host NetworkManager state directory
remained absent. The official HTTPS packages were verified against their published SHA256/size;
archive-signature verification is not claimed. `native-owner-run-13.log` SHA256 is
`82fc2590554cb7ceedf0e692b3d64a7d82c6ea6ac3c8529497d3b67126fda1af`.

The subsequent standalone-veth gate records an exact reciprocal same-namespace peer privately and
requires the v3 helper before admission. Focused root-owned fixtures preserve the selected candidate
and failed journal when a peer disappears or changes index/MAC, block the next journal, and restore
only after the original exact pair returns. Runtime refusal fixtures also reject forbidden DHCP/RA
routes and extra manual-family DNS. Real automatic-address acquisition remains open; these tests
do not substitute for that native fixture. The required changed gate against `f5b3e9c` passed
build/vet and selected netx tests (0.634 seconds); targeted final-source recovery/peer/runtime race
tests passed in 8.331 seconds. No native resources were started by these gates.

The helper-v3 production source at `10ba5ccd` passed separate final-source native static reruns
using the updated fixture (its source SHA256 is
`881817c20782d281792af6d43270f21b5d67fdc35f9d15f62f6032daebaacebe`).
Ubuntu exact-origin run 14 passed in 20.54 seconds in its private namespace, 39.14 seconds overall;
raw log SHA256 is `2f39ffbee91613558d2e5f0e16e8e81fef5d2217a2afb0cd6e45f27a75dd88dd`.
Unpatched Debian keyfile run 15 passed in 11.99 seconds in its namespace, 18.84 seconds overall;
raw log SHA256 is `8f4c4b1701ee1a401c04c7a77068be627d580819ee0322db23246fef48a4bcef`.
Both repeated actual static apply/rollback, dual-family DNS/explicit routes, confirmation and all
actual process/storage/cleanup fault windows. Every owned child was absent afterwards, the original
production networkd remained, and host `/var/lib/NetworkManager` was absent. Veth automatic
acquisition and structural transactions are still separately open.

Separate native run 16 passed actual keyfile checkpoint-create reply loss in 122.37 seconds inside
its namespace, 129.06 seconds overall, on the same frozen v3 source/fixture as runs 14/15. The worker
discarded the real reply before recording an object path or activating a profile. The fresh helper
and next native change refused the still-present opaque inventory and retained its identity-less
creating journal. Only measured disappearance after the production 120-second native timeout
permitted the fresh helper to finish durable exact recovery and cleanup. Prior real owner/kernel
intent remained intact; owned children were absent and the host NM state directory remained absent.
Raw `native-owner-run-16.log` SHA256 is
`47a153e57e746b2f1c076e37e8f82f9eed38dd7857b7fef6760e1354bffafa99`.

A subsequent minimal owner-name epoch fix (`630dd6d3`) also requires the recorded unique owner to
still hold its well-known renderer service before checkpoint/recovery effects. Dedicated fixtures
keep the prior unique destination accessible while returning a different service owner, and prove
no native effect or file replacement, retained journal/next-change refusal and exact retry only
when the original owner returns. Targeted race passed in 7.774 seconds; the required changed gate
passed build/vet/netx in 0.679 seconds. Earlier native logs are evidence of their recorded source,
not this later fix. Final static run 17 on frozen `630dd6d3` production plus the separately
recorded lost-create fixture passed networkd 257 (3.41 seconds), Netplan/networkd (7.65 seconds),
Ubuntu authored exact-origin (19.23 seconds), Ubuntu keyfile safe refusal (0.91 seconds), and actual
GUID restart refusal. Child total was 31.27 seconds, wrapper 52.74 seconds; the actual fresh helper
passed all covered static rollback/confirmation/process/storage/cleanup windows. Log SHA256 is
`00344575d587f8ce35d25bb3b50635574a88c4450cafc7e70776c4ff9d3921e6`.

Separate final run 18 repeated unpatched Debian keyfile native checkpoint rollback/hold/release
and the actual helper fault windows on the same frozen `630dd6d3` source. It passed in 10.88 seconds
in the namespace, 16.65 seconds overall; its log SHA256 is
`de50483c177772a59fcf9a385e932e2dbc61412952c98ad24c14ea0ebf693e55`. Both final runs verified owned children absent, the original production networkd
unchanged and host `/var/lib/NetworkManager` absent. The root integration has a separate negative
pending-owner admission test; these fixtures exercise the positive-owner transaction path, not
a different integration commit. Actual automatic acquisition and structural activation remain open. The separate fixture/evidence
handoff required changed gate against `630dd6d3` passed build/vet and selected netx tests in
0.024 seconds. Its native opt-in proofs are the separately recorded runs above.

The required changed gate for the backend checkpoint, against its prior separately committed
frontend draft `3018e120`, passed build/vet and the selected API/netx tests (9.811/1.411 seconds).
The complete P11 frontend/backend selection is still reserved for the final integrated tree.

Root-owned fault/race fixtures pass for failed durable confirmation, failure after checkpoint
destruction, retry without rollback after confirmation, foreign stage preservation, next ordinary
and native journal refusal, boot-epoch revalidation, profile-exchange races and cleanup-claim races.
They retain failed evidence and verify the confirmed candidate survives cleanup failure. These
fixtures do not substitute for actual native DHCP/SLAAC, structural owner activation or a reboot.
