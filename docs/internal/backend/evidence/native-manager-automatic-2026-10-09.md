# Actual automatic native-manager acceptance, 2026-10-09

Direct networkd and verified Debian NetworkManager automatic lifecycles pass on the final v6
recovery source. Authored Netplan cases remain refused and acceptance pending. This fixture
measures actual acquisition, activation and fresh-executable recovery; it does not claim host
reboot convergence, independent systemd timer dispatch or public internet connectivity.

## Scope and frozen source

`TestNativeManagerAutomaticOwnerLive` in `native_manager_auto_live_test.go` opts in with
`JD_NETNS_LIVE=1`. Each selected run uses a private root net/mount/PID namespace, private system
bus, `/etc`, `/run`, `/var/lib` and network-namespace sysfs. Fixed-MAC reciprocal veth endpoints
are `d0` (native client) and `d1` (verified unmanaged server). Busybox 1.37.0 `udhcpd` supplies a
real IPv4 address, default, DNS and option-15 domain. A bounded raw Go ICMPv6 sender supplies
SLAAC prefix/default/RDNSS/DNSSL according to [RFC 4861](https://www.rfc-editor.org/rfc/rfc4861.html#section-4.2)
and [RFC 8106](https://www.rfc-editor.org/rfc/rfc8106.html#section-5), without repository dependencies.
NM IPv6 `auto` here acquires SLAAC from RA; DHCPv6 acquisition is unmeasured.

The isolated base was `630dd6d3ff32138698ad21092dfc24dee4a94a68`. Only these independent owner
fixes were cherry-picked; unrelated structural work was not merged:

| Owner commit | Isolated cherry | Purpose |
| --- | --- | --- |
| `c0d1e08d` | `030c6e6e` | Exact reciprocal kernel peer names |
| `6e350ac7` | `a097c636` | Preserved networkd domain policy/provenance |
| `d3a2c4ea` | `89ced539` | Pinned NM domain-name/search union, helper v5 |
| `d09666fe` | `48f0d05c` | Recovery ownership/deadline containment, helper v6 |

Runs 16–17 use frozen product `48f0d05c2fd1fd5ef1ef0d2af8196fcb4389152f` and the same fixture/race
binary. The wrapper builds the actual standalone recovery executable from that backend checkout
and logs its hash/capability before namespace entry. Final hashes:

| Artifact | SHA256 |
| --- | --- |
| Final fixture source | `543f7f1cc53be196ad0caabac013bc4d59073ace26ec495334d1c5f265b8df6f` |
| Final race binary | `1e00df9a7dd6967521f72ba74a2e0a06d9405d64422d789e0212cb3b34183186` |
| Actual v6 helper | `f7d1ebda899a05e35168e807265f030c3eeddbdcad9b634f4022286125a03ad5` |
| Earlier actual v5 helper | `b07e10ef94dc577ac0aa73a4c50a43f320e3ab97f675ae7195551cc66f3d022f` |

## Passing actual runs

The compact passing records and original source/binary manifests are also checked in under
[`native-manager-automatic-2026-10-09/`](native-manager-automatic-2026-10-09/):
[v5 networkd](native-manager-automatic-2026-10-09/run11-networkd-v5-suppression.log),
[v5 NetworkManager](native-manager-automatic-2026-10-09/run12-debian-nm-v5-lifecycle.log),
[v6 NetworkManager/deadline](native-manager-automatic-2026-10-09/run16-debian-nm-v6-foreign-deadline.log),
[v6 networkd](native-manager-automatic-2026-10-09/run17-networkd-v6-lifecycle.log), and
[v6 source/binary hashes](native-manager-automatic-2026-10-09/run16-source-binary.sha256).

All records below have zero skips. Earlier v5 proof remains separate from final v6 preflight proof.

| Run | Owner/source | Wrapper / child | Raw record |
| --- | --- | --- | --- |
| 11 | networkd / v5 | 32.34s / 11.63s | `run11-networkd-v5-suppression.log` |
| 12 | Debian NM 1.52.1 / v5 | 26.17s / 18.97s | `run12-debian-nm-v5-lifecycle.log` |
| 16 | Debian NM 1.52.1 / v6 | 59.57s / 30.96s | `run16-debian-nm-v6-foreign-deadline.log` |
| 17 | networkd / v6 | 22.18s / 12.61s | `run17-networkd-v6-lifecycle.log` |

Each verifies saved/loaded/applied supported intent, real dynamic IPv4/IPv6 addresses, DHCP/RA
default routes and active-owner DNS/domains. Actual automatic/manual activation and rollback,
automatic DNS/routes suppression, applying-backend process death, fresh helper recovery,
durable reconnection confirmation and lost cleanup outcome retry pass. Exact stage/claim paths
are absent afterward; NM checkpoint inventory is empty.

Manual intent has explicit replacement defaults. Suppression uses a real preferred default on
the private unmanaged peer before editing, preserving that same preferred uplink throughout
apply/rollback. The client must retain zero DHCP/RA protocol routes and exactly configured DNS.
networkd's independent retained `UseDomains=yes` policy still provides verified provider domains.
These peer routes are withdrawn afterward; no client evidence is substituted by them.

Run 16 additionally shortens an actual owned NM checkpoint to three seconds, holds the selected
candidate's exact inode and inserts fixture foreign bytes with a new inode. A fresh helper refuses
into `phase=degraded`, `cleanup=pending`, releases the exact owned checkpoint and verifies its
disappearance. Past the original deadline, foreign bytes/inode and both restore stages remain
unchanged. Only fixture cleanup restores the held candidate inode, then fresh recovery succeeds.
The final native record explicitly reports:

```text
Fresh helper refused foreign selected bytes/inode, released the exact owned checkpoint,
retained phase=degraded cleanup=pending; foreign file and both stages unchanged past actual
3-second rollback deadline. Fixture cleanup alone restores captured candidate inode.
--- PASS: TestNativeManagerAutomaticOwnerLive (30.96s)
PASS
```

Every passing child proves its RA descriptor closed/sender drained, DHCP/manager/bus children
reaped, leases/pidfiles withdrawn and no remaining PID-namespace child. Outside every terminal
run, owned test/NM/DHCP/private-bus processes are absent, original production networkd PID 883
is unchanged, host `/var/lib/NetworkManager` remains absent and task temp returns to empty.
Timer admission is explicitly stubbed; owner-unit enablement is configuration evidence only.

## Preserved failures and open Netplan boundaries

Original failures were preserved separately, never replaced by passing logs:

| Runs | Cause and outcome |
| --- | --- |
| 1 | Actual iproute2 supplies reciprocal `link` names without numeric `link_index`; peer guard refused before mutation. Corrected by `c0d1e08d`. |
| 2 | Explicit networkd `UseDomains=yes` was outside the closed field guard. Corrected by `6e350ac7`. |
| 3, 6 | Real NM DHCP option 15 appears in IPv4 `Domains`, not `Searches`; reader omitted it. Corrected by `d3a2c4ea`. Run 6 retained exact IP/DHCP provenance. |
| 4 | Helper link failed with exhausted workspace storage; no namespace or native daemon started. Only the task's redundant helper was removed; source/evidence remained. |
| 5 | Fixture diagnostic requested `IP4Config` instead of `Ip4Config`; corrected without a product change. |
| 7 | Manual fixture omitted replacement defaults; connectivity guard correctly rolled it back. |
| 8–9 | Compound suppression/static-gateway intent left networkd failed with no configured IPv4 default. Exact source/runtime captured; suppression was then measured independently with an unchanged preferred peer route. |
| 10 | Backup metric left the client preferred before suppression; preferred-uplink guard correctly refused the move. Fixture now selects its backup before editing. |
| 13 | Authored Netplan/networkd runtime acquired addresses/routes/DNS, but generated shared `[DHCP] RouteMetric=100, UseMTU=true` failed closed admission and provided no explicit domain policy. |
| 14–15 | Authored Netplan/Debian NM reports `Unsaved=true` despite matching loaded/applied supported intent. Its generated profile also has explicit wake-on-lan/privacy properties requiring full closed comparison. |

Netplan 1.1.2 offline generation in task-private roots captured exact authored/generated files,
without global apply or native daemons. Explicit DHCP `use-domains=true`, `use-mtu=false` and
RA `use-domains=true` generate shared DHCP policy plus `[IPv6AcceptRA] UseDomains=true`; that
preserved scope still needs production admission/provenance work. See [Netplan's DHCP/RA policy](https://netplan.readthedocs.io/en/latest/netplan-yaml/).

Run 15 measured both supported L3 projections matching authored auto/auto, and `Unsaved=true`.
[The pinned NM source](https://github.com/NetworkManager/NetworkManager/blob/1.52.1/src/core/settings/nm-settings.c#L1134)
sets UNSAVED for RUN keyfile storage. This does not authorize L3-only or blanket Unsaved admission:
exact generated origin, complete loaded/applied fields and retained policy still need verification.
No Save/Update2 workaround, extra persistent origin or guard relaxation was introduced.
The default all-owner opt-in invocation includes these still-failing Netplan cases; select an
explicit owner to reproduce the verified direct-manager slices. Full P11 remains open.

## Reproduction and evidence locations

Artifact root: `/home/ubuntu/Just-Dashboard/.network-worktrees/native-auto-artifacts`.
Raw passing records above and all `run1`–`run15` failures remain there. Frozen manifests are
`run11-source-binary.sha256` and `run16-source-binary.sha256`; runs 12/17 reuse their respective
frozen binary. Diagnostics include `run1-reciprocal-link-schema.log`, `run9-candidate-runtime.json`,
`run13-generated-networkd.txt`, `run14-generated-nm.txt`, and `run15-netplan-debian-nm-projection.log`.
The earlier detailed draft is retained as `automatic-evidence-draft-before-handoff.md`.

| Passing raw log | SHA256 |
| --- | --- |
| Run 11 | `305a9b3ec4ff1f72fc9466003c1816935d0a5062377c63b65622cf2121413e2e` |
| Run 12 | `945a0c8658cf36a4d0a051d739d1a3a76bc14e334a3162b56a7bc0259d1e6597` |
| Run 16 | `45c2e480fd8d2b7a77dd39d8a8467bc5b073e3f1f113d1f15932f35d144c1771` |
| Run 17 | `d79871254ac96f077cd04ae5ac8c9b3cdee5bd8dd8b5bdaa6bbbbbcae0ca93d7` |

The independently verified Debian userland is
`/home/ubuntu/Just-Dashboard-network-native-managers-artifacts/native-tools/debian/extracted`.
Official HTTPS package hashes: network-manager 1.52.1-1, 2,247,236 bytes,
`1e1f7aab8916b1346d39c844688409184a589a9e7f8377f9932ad95ebb7aafea`;
libnm0, 458,548 bytes, `6d394ef5bf05c82e10abc5a79c96f9aeb5719601212a9728b43c006e4d4ad146`.
This is no archive-signature claim and does not substitute Debian proof for Ubuntu writer proof.

Compile from `backend/`, then run from `backend/internal/netx`, after the serial lane is clean:

```bash
env TMPDIR=/home/ubuntu/Just-Dashboard/a11-t.XBfuVE GOMAXPROCS=2 \
  go test -race -p 2 -c \
  -o /home/ubuntu/Just-Dashboard/.network-worktrees/native-auto-artifacts/native-manager-auto.test \
  ./internal/netx

timeout --kill-after=5s 240s env TMPDIR=/home/ubuntu/Just-Dashboard/a11-t.XBfuVE GOMAXPROCS=2 \
  JD_NETNS_LIVE=1 JD_NATIVE_AUTO_CASE=networkd \
  JD_NATIVE_MANAGER_NM_ROOT=/home/ubuntu/Just-Dashboard-network-native-managers-artifacts/native-tools/debian/extracted \
  /home/ubuntu/Just-Dashboard/.network-worktrees/native-auto-artifacts/native-manager-auto.test \
  -test.run '^TestNativeManagerAutomaticOwnerLive$' -test.count=1 -test.timeout=235s -test.v
```

Use `JD_NATIVE_AUTO_CASE=NetworkManager` for the verified NM lifecycle/deadline case. Each child
has a three-minute deadline; the external wrapper has four minutes. The short, nonhidden task
TMPDIR is outside the tested checkout; system `/tmp` remains constrained. No package installation,
image pull, production owner restart or host network/configuration change is part of these runs.

Final required gate passed from the worktree root:

```bash
env -u JD_NETNS_LIVE -u JD_NATIVE_AUTO_NS -u JD_NATIVE_AUTO_WORKER \
  TMPDIR=/home/ubuntu/Just-Dashboard/a11-t.XBfuVE GOMAXPROCS=2 GOFLAGS=-p=2 \
  scripts/test-changed.sh 48f0d05c2fd1fd5ef1ef0d2af8196fcb4389152f
```

It selected exactly the two owned new files; backend build, netx vet and the ordinary fixture
compilation/opt-in skip passed (`netx 0.022s`). The skip is preparation evidence only; the actual
native passes above have no skip. Raw `fixture-test-changed.log` has SHA256
`d38fbe7325e85fb291b1c28b84bd5d292285829743a6d690b6a4bda7b0502c83`.
Final fixture hash still matches run 16's manifest, and `git diff --check` passes.

Documentation review covered `docs/internal/`, `AGENTS.md`, `README.md` and `CONTRIBUTING.md`.
This change adds an opt-in fixture and its commands/evidence only; existing product behavior,
dependencies and contributor workflow are unchanged. The current native-owner documentation
continues to require separate automatic/Netplan/cold-runtime/structural acceptance; this evidence
records the verified slices and open boundaries without implying broader completion.
