# Shipped native profile inode acceptance

This fixture checkpoint extends the existing accepted owner lifecycles with a canonical unchanged
intent. It is based on the isolated shipped helper-v10 fix, not the private ordered controller
adapter. All four established owner paths passed on the final assembled source, including direct
networkd after the separately reviewed terminal-LF parser correction. The initial frozen-source
successes and failures below retain their original attribution.

The automatic fixture covers direct networkd, Debian NetworkManager keyfiles and explicit-policy
Netplan/networkd. The static fixture covers Ubuntu Netplan/NetworkManager exact-origin recovery.
Debian Netplan/NetworkManager automatic RUN-profile admission remains a separate open boundary.
Each selected owner keeps its existing acquisition, DNS/routes, process-death, confirmation and
cleanup checks. Real systemd timer dispatch and reboot remain outside these namespace fixtures.

After an ordinary intent has been saved and confirmed canonically, both fixtures apply that same
intent twice. Every selected authored/generated file must have literally equal captured prior and
candidate bytes, distinct captured inodes, the exact candidate inode at the selected path and the
exact displaced authored inode in staging. A new helper exposing exactly `jd-native-manager-v10`
must restore the original authored inode during rollback, retain full configured/runtime/boot
agreement, and remove owned stages/checkpoints. The second unchanged transaction is confirmed;
a terminal fresh-helper retry must retain the exact candidate inode and the durable decision.

Existing v8 evidence keeps its original source/helper attribution. This document does not claim
structural, cold, or timer acceptance.

## Initial frozen source

The tested tree was `66ad7cf8cd3ef74d18daf8c1fd355fd386888a8c`, based on shipped fix
`b744765cf57cde35f9e6c032967a12b2294faf76`. The required changed gates passed build/vet/selected
netx tests in 20.439 seconds, followed by the helper-hash logging gate in 20.610 seconds. A distinct
race binary was compiled from the clean final source. All successful runs used the actual bound
helper digest `f1cf8427ca23c86353fe19914f13476988235c5cdaba036ba69e3b514976ec9a` and verified the
literal `jd-native-manager-v10` capability.

| Run | Owner / result | Wrapper / child | Exact unchanged-file proof |
| --- | --- | --- | --- |
| 01 | networkd: harness failure before native startup | 0.104s / none | Wrong initial test cwd prevented the relative helper build. |
| 02 | networkd: honest equality refusal | 27.644s / 9.29s | Prior/candidate inodes 20/22 differed, but captured bytes differed too. |
| 03 | Debian NetworkManager: PASS, no skip | 35.450s / 28.52s | Authored inode 23 returned after candidate 25; confirmed candidate 27 retained. |
| 04 | Explicit-policy Netplan/networkd: PASS, no skip | 36.112s / 28.95s | Authored/generated inodes 21/247 returned after candidates 23/295; confirmed 25/370 retained. |
| 05 | Ubuntu Netplan/NetworkManager: PASS, no skip | 31.695s / 25.29s | Authored/generated inodes 17/101 returned after candidates 19/137; confirmed 21/179 retained. |

Run 02 kept all equality assertions intact. It reached ordinary automatic acquisition, manual
rollback, suppression, cleanup and confirmation, but stopped before the later applying-process
death case. Inspection found that `parseNativeINI` includes the synthetic empty element after a
terminal LF and `render` emits another LF for it. Repeated direct-networkd rendering therefore has
no stable byte fixed point. The captured hashes differ; the failed private namespace was removed,
so a literal captured byte diff is not claimed. This initial source required a separate
parser/idempotence correction; the final assembled source below includes it.

The passing automatic cases retained actual DHCPv4/SLAAC, acquired routes/DNS/domains, suppression,
manual transitions, applying-process death, durable confirmation and cleanup retry. Direct NM also
passed the real foreign-file three-second checkpoint deadline containment. Ubuntu exact-origin
kept authored YAML/generated UUID and independent recovery without a native checkpoint, plus its
existing transport-epoch, route and death-window checks.

Every terminal wrapper verified zero owned native children, an empty removed task TMPDIR,
unchanged production networkd PID 883 start identity and unchanged absent host NetworkManager
configuration/state paths. Full source hashes, binary hash, argv/environment, raw log digests and
cleanup outcomes are preserved in [the initial manifest](native-manager-profile-inodes-v10-2026-10-09/initial-source-binary.json).
The [runner](native-manager-profile-inodes-v10-2026-10-09/runner.py.txt) captures the bounded execution
and containment checks. Raw logs/results retain each run's exact failed or passed outcome.

## Final assembled source

All four selections passed without skips on clean source
`c3c82bf36f5619cb23b5d2b6dfdbc21f253a77c7`. It assembles the public inode classification fix,
unchanged-intent fixtures and the separate INI correction `5f6215c8e8081fdc4c1ee4b471bc56a4bdbc78b4`.
The source-frozen race binary has SHA256
`b3b7ef9e9d63d33d60bc37462ced44fa1962af888fda85401f878659fe9761dc`. Every actual bound recovery
helper has SHA256 `8f5c829b1a63022236fde72b34f8e642c757127322770b37c70c42ca63807643` and advertises
exactly `jd-native-manager-v10`. All 1,856 captured backend Go files were independently compared
with the tested tree and the later documentation-only root assembly; every digest matches.

The root required changed gate passed build/vet/selected netx tests in 20.554 seconds
(73.12-second wrapper). The targeted `-race '^TestNative'` gate passed in 29.315 seconds
(57.54-second wrapper), with native live opt-ins explicitly unset. Those checks precede the
separate actual owner runs below.

| Run | Owner / result | Wrapper / private child | Exact unchanged-file proof |
| --- | --- | --- | --- |
| 06 | Direct networkd: PASS, no skip | 34.722s / 14.51s | Original inode 20 restored after candidate 22; confirmed candidate 24 retained. |
| 07 | Debian NetworkManager: PASS, no skip | 36.511s / 28.82s | Original inode 23 restored after candidate 25; confirmed candidate 27 retained. |
| 08 | Explicit-policy Netplan/networkd: PASS, no skip | 35.557s / 28.27s | Original authored/generated inodes 21/247 restored after candidates 23/292; confirmed 25/367 retained. |
| 09 | Ubuntu Netplan/NetworkManager: PASS, no skip | 32.977s / 26.61s | Original authored/generated inodes 17/101 restored after candidates 19/137; confirmed 21/179 retained. |

Every unchanged transaction independently required literal prior/candidate byte equality, distinct
captured inodes, exact selected candidate and displaced original identities, then exact original
inode restoration by a newly executed standalone helper. The confirmed transaction retained the
exact candidate identity through terminal fresh-helper retry. Full configured/runtime/boot readings
and native owner epochs remained mandatory.

The automatic runs kept actual DHCPv4/SLAAC, acquired default routes, DNS and domains, manual
transitions, DNS/routes suppression, applying-process death, durable confirmation and cleanup
retry. Direct NetworkManager also passed actual foreign-file preservation beyond its owned
three-second checkpoint deadline. Explicit Netplan/networkd kept authored `use-mtu: false`, DHCP/RA
domain policy and the refusal of unrepresentable RA route suppression before any selected-file,
stage or journal effect. Ubuntu static exact-origin recovery kept the selected authored YAML,
generated UUID, independent recovery without a native checkpoint, transport-epoch checks and
existing confirmation/death/cleanup fault windows.

Every run ended with zero owned native processes and task TMPDIR entries, unchanged production
networkd PID 883/start identity and absent host NetworkManager configuration/state paths.
The [final manifest](native-manager-profile-inodes-v10-2026-10-09/final-source-binary.json)
contains complete source hashes, argv/environment, same-source helper/race digests, gate logs and
each cleanup result. Its [runner](native-manager-profile-inodes-v10-2026-10-09/final-runner.py.txt)
records the bounded invocation. Raw logs use deterministic gzip with both original and compressed
SHA256 digests; decompression was checked against each untouched workspace log.

These namespace proofs establish unchanged-intent acceptance for the four measured owner paths.
Real systemd timer dispatch, reboot, public bond/VRF writes, default Netplan DHCP MTU, DHCPv6
acquisition and wider owner/platform acceptance remain open. Debian generated
Netplan/NetworkManager automatic RUN-profile closure is a separate boundary. No private v9/undo5
structural semantics are included.
