# Native structural ownership and preparation

This is a preparatory contract for editing an existing native bond or VRF. Structural writes remain
refused before host execution, journal cleanup or any L3 effect until the saved, loaded, applied and
kernel baseline collector and independent recovery consumer are verified. An omitted `structure`
continues to use the existing L3 contract. Creation, adoption, foreign members and arbitrary native
profile fields remain outside the adapter.

`NativeEditRequest.structure` is an optional closed object with an explicit `members` array and
either a canonical `bondMode` or a supported, unprotected `vrfTable`. Missing/null membership is
refused; an explicit empty array is distinct. The union of old/new selected members is limited to
32. A member cannot be the controller, duplicated, malformed, or owned by Docker/Tailscale.
Nested controllers are currently refused. VRFs cannot use tables 52 or 253–255, or a table outside
the supported signed route range. This request is currently a refusal, not an available transaction.

The private `nativeStructurePlan` records before/candidate controller contracts and controller,
retained, added and removed member roles. Each profile carries exact device/kind/index/MAC,
owner/renderer/version, native UUID/object identities, before/candidate intent and contracts,
selected file snapshots/candidates, generated origin and Netplan source identities. Recovery strategy
and measured NetworkManager writer classification are private metadata, never client-selected knobs.
They must agree with the verified native owner.

`nativeStructureFiles` deduplicates identical authored origins before any exchange. A shared path
with different inode, baseline bytes, role or candidate bytes is refused. Snapshots must have canonical
owner-specific origins and root provenance without group/world write access. Each snapshot/candidate
is bounded to 256 KiB; unique baseline/candidate bytes, including unchanged Netplan authored sources,
are limited to 2 MiB. There are at most 33 profiles, three files per profile and 99 deduplicated files.
The recovery consumer must separately validate role/device/member scope, epochs, intent and native
checkpoint scope, and serialize deduplicated data within its independent journal limit.

Baseline verification must compare kind, controller/port binding, all old/new selected member profiles,
bond mode and preserved options, and the VRF table across their saved, loaded/applied and kernel layers.
Reactivating a whole profile also reapplies dormant properties; agreeing on its L3 subset alone is
insufficient. NetworkManager documents controller bindings as interface names or UUIDs and VRF tables
as typed properties. Its [connection](https://networkmanager.dev/docs/api/latest/settings-connection.html),
[bond](https://networkmanager.dev/docs/api/latest/settings-bond.html) and
[VRF](https://networkmanager.dev/docs/api/latest/settings-vrf.html) settings define these native fields.

networkd's [netdev documentation](https://www.freedesktop.org/software/systemd/man/systemd.netdev.html)
allows updating an existing matching kind but says some settings need device removal and recreation.
Parsing a candidate or running reload therefore does not prove that an existing bond mode or VRF table
changed. Those operations require isolated actual-manager proof before admission; a global service
restart or foreign-device deletion is not part of this preparation.

`native_manager_structure_plan_test.go` checks omitted-versus-explicit membership, generation-independent
baseline immutability, foreign/reserved scope refusal, old/new union bounds, shared Netplan origin
conflicts, closed file provenance, aggregate authored-source limits and an explicit structural request
refusing before any L3/host effect. On the prepared-contract source, focused tests passed in
0.054 seconds, and the required `scripts/test-changed.sh 8b65fa7f` gate passed build/vet and the netx
package tests in 0.553 seconds; targeted final-source race tests passed in 1.168 seconds. Checks used
workspace TMPDIR and Go concurrency limited to two. These tests
do not complete structural native acceptance.

The documentation review covers the complete preparatory diff against the internal guides, AGENTS,
README and CONTRIBUTING. This adds an explicitly refused request and private preparation types; it
adds no dependency, CI, deployment, release, license or available structural operation. The native
manager guide remains the owner of the current L3/recovery surface, and this guide owns the
structural contract. Broader operator documentation must change together with verified admission.
