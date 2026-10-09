# Shipped native profile inode acceptance

This fixture checkpoint extends the existing accepted owner lifecycles with a canonical unchanged
intent. It is based on the isolated shipped helper-v10 fix, not the private ordered controller
adapter. Three established owner paths passed on the initial frozen source. Complete acceptance
remains pending the separate direct-networkd parser correction and its source-matched native proof.

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
so a literal captured byte diff is not claimed. A separate parser/idempotence correction is pending.

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
