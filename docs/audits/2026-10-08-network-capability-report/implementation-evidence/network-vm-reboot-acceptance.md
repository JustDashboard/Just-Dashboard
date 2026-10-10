# Network reboot acceptance in a disposable guest

This checkpoint closes the "actual reboot" gaps of ledger rows C001, F4, P7, P11 and P14, with
supporting evidence for C002, C003, C004 and the VRF part of C027, by rebooting a disposable
QEMU/KVM guest rather than the production host. It does not edit the ledger; the proposed statuses
at the end are for the reviewer. The production host was never rebooted and its network, firewall,
packages, groups and services were not changed (see [host isolation](#host-isolation-and-cleanup)).

Branch `implement/network-vm-reboot-acceptance`, based on PR head `c31e9329`. The guest exposed seven
product defects; each is fixed with a regression test (see [product defects](#product-defects-found-and-fixed)).
The final full run passed all 61 steps against the fixed source; the audit fix, made after it, was
rerun on its own.

## The guest

| Item | Value |
| --- | --- |
| Image | Ubuntu 24.04 minimal cloud image 20261008, SHA256 `2f119f50…a777df` (re-verified before use), read-only base under a qcow2 overlay |
| Hypervisor | QEMU 9.2.1 (`9.2.1+ds-1ubuntu5.2`) and genisoimage 1.1.11 extracted in the prerequisites tree; KVM, `q35`, `-cpu host`, 2 vCPU, 2 GiB |
| Launch | `sudo -n setpriv --reuid=1000 --regid=1000 --groups=992,1000` (the existing `kvm` group for the QEMU process only); `-daemonize`, stopped by its PID file. Exact argv: [`final/qemu-argv.log`](network-vm-reboot-acceptance/final/qemu-argv.log) |
| Network | User-mode (slirp) only: `enp0s3` with one loopback-only SSH forward `127.0.0.1:43250`; `enp0s4`–`enp0s6` on restricted slirp segments. No bridge, tap or host route |
| Guest software | Linux 6.8.0-146, systemd/networkd 255.4-1ubuntu8.17, Netplan 1.1.2-8ubuntu1~24.04.3, NetworkManager 1.46.0-1ubuntu2.8, iproute2 6.1.0, nftables 1.0.9, iptables 1.8.10 (nft), Docker (`docker.io`), `vrf`/`ifb`/`sch_cake`/`tcp_bbr` modules |
| Packages | The 19 archives of the verified offline closure, installed with `dpkg -i`; their in-guest SHA256 matched the preparation record ([`final/guest-debs-sha256.log`](network-vm-reboot-acceptance/final/guest-debs-sha256.log)). nftables, iptables, Docker and ping came from the guest's own signed archive over its user-mode network |
| Native owners | `enp0s4`: authored Netplan, networkd renderer. `jdnm0`: authored Netplan dummy device, NetworkManager renderer. `enp0s5`: authored Netplan ethernet, NetworkManager renderer (refusal witness). `enp0s6`: direct networkd `.network` |
| Backend | The static server built from this branch, laid out as production does (`/etc/just-dashboard/network`, `/var/lib/just-dashboard`, the generated `just-dashboard-network.service` and `just-dashboard-network-recovery.service`, the packaged `network-recovery` helper copied by the backend itself). It runs as a guest systemd unit that is never enabled, so every boot is inspected before it starts |

Production runs the same binary in the Compose backend container with the host's PID and network
namespaces and `/etc` mounted; here it runs directly on the guest host with the same environment.
The boot units, the helper and every restore path are host-side in both layouts; container-only
behavior (nsenter crossing, the container's own tools) is not exercised here.

The harness is committed under
[`backend/internal/netx/testdata/vm-reboot-acceptance/`](../../../../backend/internal/netx/testdata/vm-reboot-acceptance/vm.sh):
`vm.sh` (prepare, start, wait, provision, reboot, collect, stop, cleanup), `guest-setup.sh`,
`guest.py` (one phase per invocation; each drives the real API with session login, CSRF, pending
apply and reconnection confirmation, or reads the kernel and systemd, and appends a JSON record),
`fault-shim.sh` (a guest-only shim in front of `ip`/`systemctl` that SIGKILLs the backend when the
durable journal reaches a named phase) and `run-acceptance.sh` (the 61-step sequence). To repeat:

```sh
export JD_VM_ARTIFACTS=<task dir> JD_VM_PREREQ=<verified prerequisites> JD_VM_BACKEND=<static server>
export JD_VM_ROUTING=<routing-branch server, netx test binary and testdata>   # optional VRF step
vm.sh prepare && vm.sh start && vm.sh wait && vm.sh provision packages,owners,backend,routing
vm.sh reboot && run-acceptance.sh && vm.sh collect && vm.sh stop && vm.sh cleanup
```

## Runs

| Run | Backend | Result | Logs |
| --- | --- | --- | --- |
| Exploration | `ff3802c0…` from `c31e9329`, then fixed builds `6ba0e141…` and `af4fde00…` | Found the defects below; phases run individually | [`explore/`](network-vm-reboot-acceptance/explore/) |
| Run 1 | `af4fde00…` (source `05b6cc91`) | 52 of 61 passed; all 9 failures were HTB verification under traffic (defect 6) | [`run1/`](network-vm-reboot-acceptance/run1/summary.log) |
| Final | `df13e52a…` (source `e35d1a03`) | **61 of 61 passed**, 15 guest boots | [`final/summary.log`](network-vm-reboot-acceptance/final/summary.log) |
| Audit rerun | `a134ebcd…` (source `5fa60c71`) | Disconnected-client case with defect 7 fixed: passed | [`audit-rerun/`](network-vm-reboot-acceptance/audit-rerun/01-c002-cancel-2.log) |

Every step's JSON record (failures, boot ID, evidence) is in its `final/NN-*.log`; all records are in
[`final/results.log`](network-vm-reboot-acceptance/final/results.log). The guest's unit and owner
journals across every boot are in [`final/guest-journal.log`](network-vm-reboot-acceptance/final/guest-journal.log),
the boot list in [`final/guest-boots.log`](network-vm-reboot-acceptance/final/guest-boots.log) and the
serial console in `final/serial.log.raw.gz` (deterministic gzip). A reboot is accepted only when the
kernel boot ID changes; the same QEMU process continues across guest reboots.

## Per row

### C001 — managed spec and boot restoration

**Ran.** Through the API (immediate mode): a named namespace with a veth pair and addresses on both
ends, a bridge with a dummy port, a dummy with MTU 1400, a VLAN on it, a separate address, routes in
main and table 231 (IPv4 gateway, IPv6 device, blackhole, a default in 231), IPv4/IPv6/fwmark policy
rules, IPv4 forwarding, a masquerade NAT, a port forward, a rate limit, a manual blocklist, five kernel
protections, an HTB upload limit with an ingress policer, a CAKE root and BBR — 22 accepted requests
([`final/02-c001-apply.log`](network-vm-reboot-acceptance/final/02-c001-apply.log)). Then `systemctl reboot`.

**After the reboot** (boot `47775ab0…`, [`final/04-c001-verify.log`](network-vm-reboot-acceptance/final/04-c001-verify.log)),
before the backend ever started: `just-dashboard-network-recovery.service` and
`just-dashboard-network.service` both ran in this boot with result `success`; all 11 `ExecStart`
commands exited 0; every saved object was found in the kernel by direct `ip`/`tc`/`nft`/`iptables`/
`sysctl` reads (namespaces, links with kind/master/MTU/up, both veth ends, addresses, routes per table,
rules per family and priority, root and ingress queues, gateway rule comments, admission rule,
sysctls). Then the backend's drift report: 35 runtime and 8 file observations matching apart from
three documented `unknown`s (veth peer connectivity, nft rule-expression equality, helper build
identity), boot unit `matching`, and `boot.execution.status = succeeded` with this boot's ID and the
unit's invocation ID — the verified last-boot result.

**Limits.** The boot result is measured from systemd for the current boot when read; it is not a
persisted phase. `bootTrigger` stays unknown. Physical NIC identity, provider DHCP/RA and real
traffic beyond the guest are not exercised.

### F4 and C002 — backend death between phases, independent recovery

**Ran.** One added route per case, killed with SIGKILL at three journal phases: `runtime_applied`
(the shim fired on the verifier's `ip -j route get 1.1.1.1`), `persisted` (fired on
`systemctl is-enabled`), and `awaiting_confirmation` (pending apply, then the kill). Each phase was
recovered twice: once by the real transient systemd timer with the backend dead and no reboot
(92.4 s after the kill, unit `just-dashboard-network-recover-<id>.service`), once by rebooting before
the ninety-second deadline so only the boot recovery unit could act. Steps 07–21.

**Result.** All six recovered: journal `recovered` with no errors, watchdog `recovered`, the exact prior
`spec.json` and `links.batch` bytes restored, the candidate route gone, every prior C001 object in the
kernel and the backend never started during recovery. After each recovered boot the drift report
matched; its measured activation reads `failed` because the ordinary unit's `links.batch` creation
lines report devices the recovery already recreated (documented limit).

A client that disconnected right after sending a mutation (step 06) left a terminal journal and an
identical spec and kernel. Its audit row was lost until defect 7 was fixed; the rerun keeps it.

### C003 — reconnection confirmation

Every pending change in this run (F4, P7 repair, P11 edits, P14) was confirmed through the real HTTP
protocol — a fresh session- and source-bound challenge from `/verify`, returned to `/confirm` — or left
unconfirmed and recovered. This adds real-host evidence for the confirmation path; it does not
establish application, tunnel or provider reachability.

### C004 — ownership

The guest's four NICs read as owned by the system; deleting `enp0s4` was refused with `409 not_managed`;
deleting the managed bridge that carries routes, rules and NAT was refused naming its route
([`final/05-c004-ownership.log`](network-vm-reboot-acceptance/final/05-c004-ownership.log)).
Adoption and migration remain out of scope.

### P7 — drift and boot health

**Ran.** (a) Runtime drift: an address, a route, the bridge port and the bridge's root queue were
removed with `ip`/`tc`. Drift reported exactly those four (`missing`/`drift`) and offered only
non-executable advice for them; a reboot restored all four from the unchanged boot inputs and the
next drift read matched with a succeeded activation. (b) Boot-input drift: one address line was removed
from `links.batch` on disk and the guest rebooted. The unit ran successfully from the edited file;
drift reported the render as `drift` and the omitted address as `missing`. The reviewed repair was
applied through `/drift/repairs` in pending mode and confirmed: the file matched again, the
address stayed missing (file repairs do not replay kernel objects), and the following reboot
restored it with everything matching. (c) `systemctl restart systemd-networkd`: the managed unit
restarted with it (`PartOf`) and put back the routes, rules, gateway table and admission rules
networkd had removed. Steps 22–30.

**Observed.** In both full runs networkd 255.4 itself aborted once during that restart
(`Assertion 'e->key == i->next_key' failed at src/basic/hashmap.c:670`), was restarted by systemd and
restarted the managed unit again; restoration converged and drift matched. The restart's measured
activation reads `failed` because `links.batch` finds its devices and addresses still present
(documented limit).

### P11 — native manager adapters

**Ran** with real owners in a booted guest: authored Netplan/networkd (`enp0s4`), authored
Netplan/NetworkManager on a dummy device (`jdnm0`, Ubuntu's exact-origin strategy, no checkpoint) and
direct networkd (`enp0s6`). For each owner: a pending owner-path edit (one address and one route),
fresh reconnection confirmation and complete cleanup with new selected-file inodes; a reboot after
which the owner restored the confirmed profile by itself (bytes unchanged, intent equal, runtime and
boot `matching`, backend not yet started); an unconfirmed edit recovered by the real systemd timer
(about 90 s, backend dead); and an unconfirmed edit followed by an immediate reboot, recovered by the
boot unit on the new boot (the conservative new-boot rebind path) with the prior bytes restored and
cleanup complete. Steps 31–54: all passed.

**Refused or limited.**

- Netplan renders `[ethernet] wake-on-lan=0` into every NetworkManager ethernet profile; the adapter
  refuses that unverified property, so `enp0s5` is not editable
  ([`final/31-p11-read.log`](network-vm-reboot-acceptance/final/31-p11-read.log)). The NetworkManager owner
  path was therefore exercised on a dummy device, not on Ethernet.
- The first networkd edit after a boot was refused by the IPv6 anchor guard and rolled back
  (step 39): networkd's global reload also reconfigured the uplink, whose Netplan artifact had been
  regenerated by a systemd reload during boot (NetworkManager's start), dropping its DHCP lease and
  router-advertisement route for about half a second
  ([trace](network-vm-reboot-acceptance/explore/transcript-first-edit-ipv6-anchor.log)). The transaction cases settle networkd first with
  one `networkctl reload`. This remains an open limit of the global reload.
- Structural bond/VRF editing, DHCPv6 and default Netplan DHCP MTU were not exercised.

### P14 — download SQM

**Ran.** An explicit `diffserv4`/`triple-isolate` CAKE profile at 9 Mbit/s on `enp0s6`, with overhead 22,
MPU 64 and 80 ms RTT, in pending mode and confirmed; reboot. **After the reboot** the unit's
non-ignored `network-recovery --network-sqm-restore` step exited 0; the IFB existed and was up with its
`jd-sqm:` nonce alias and derived MAC; the source MAC was unchanged; every CAKE parameter read back
exactly (bandwidth 1 125 000 B/s, diffserv4, triple-isolate, no NAT, wash, ingress, overhead 22, MPU 64,
noatm, RTT 80 000 µs, split GSO, ACK filter off); the redirect matched its cookie and target; the
shaping view reported kernel, helper and boot evidence `verified` and drift matched with a succeeded
activation. Steps 55–57. Before defect 1 was fixed, every SQM apply on this Ubuntu release was refused
and left a degraded journal.

**Limits.** Provider queues, NIC offloads and performance under load were not measured here. Leaving
the root queue untouched (`qdisc: ""`) was required: with BBR on, `default_qdisc=fq` makes every NIC's
root `fq` after a reboot, which the shaping baseline refuses as an unmanaged queue.

### C027 — VRF policy rules

The guest kernel loads `vrf`. In disposable namespaces, traffic crossed a real VRF (table 1100) through
the kernel's l3mdev rule while the same peer was unreachable outside it. The routing package (branch
`implement/network-routing-firewall-maturity`, `e86fb58b`, not part of this branch) was then run in the
guest: its `TestLiveRoutingMaturity` passed with the module present; its server read the real VRF
device and table, added and read back a managed `l3mdev` rule, and its preview reported
`model_unknown` for a VRF lookup rather than guessing; after a reboot its boot unit restored the rule.
Steps 58–61. This is evidence for that branch's package in a VRF-capable kernel, attributed to that
source; it is not part of this branch's code.

## Product defects found and fixed

| # | Defect (found only in the booted guest) | Fix | Regression tests |
| --- | --- | --- | --- |
| 1 | iproute2 6.1 (Ubuntu 24.04) prints `tc -j class show` as text, or nothing. Every HTB upload limit was refused and rolled back; every download SQM apply was refused as "foreign classs", and its synchronous and independent recovery stayed degraded on the same check, blocking further changes | Read the text form too (`parseTCClasses`) | `TestTCClassesReadJSONAndTheOlderTextForm`, iproute2 6.1 rows in `TestShapingHealthDistinguishesRateDriftFromUnreadableKernel`, `TestSQMReadsTheOlderTextClassForm`; the unfixed source fails and the fixed source passes the live SQM/shaping tests on the guest's iproute2 ([`gates/guest-live-tests-unfixed.log`](network-vm-reboot-acceptance/gates/guest-live-tests-unfixed.log), [`gates/guest-live-tests-fixed.log`](network-vm-reboot-acceptance/gates/guest-live-tests-fixed.log)). The fixed backend also recovered the degraded journal the unfixed one had left ([transcript](network-vm-reboot-acceptance/explore/transcript-fixed-backend-recovers-degraded-sqm.log)) |
| 2 | The installed helper (the 80 MB packaged backend) was read against the 32 MiB render limit: drift always reported it `unreadable` and every selected repair was blocked as "helper must already be installed" | Hash it as a stream under a 256 MiB bound | `TestDriftHashesAnExecutableSizedRecoveryHelper`, `TestDriftRepairReadinessHashesAnExecutableSizedHelper` |
| 3 | systemd prints one `ExecStart=` line per command; drift kept only the last, so a measured boot was always `unknown`. With every line read, the unit's clearing deletes (admission `-D`, shaping `del`) fail by design on a fresh boot, so a correct boot would always read `failed` | Read every line; render those deletes as `ExecStartPre=-` | `TestDriftBootNeedsMeasuredExecutionAndDetectsIgnoredFailure/one line per command…`, `TestUnitClearsShapingBeforeItsBatchAndOutsideTheRestoration`, admission assertions in `TestUnitRestoresEverythingAndAdmitsOnlyWhenNeeded`, `TestLiveShapingBatchLoadsAndReadsBack` (run on the guest) |
| 4 | `netplan generate --root-dir` still reloads udev and systemd on the live host; the reload regenerated the live artifacts with new inodes mid-transaction, so every Netplan edit was refused as an ownership change | Run `/usr/libexec/netplan/generate --root-dir` directly | `TestNetplanStagingRunsOnlyTheGeneratorInItsPrivateRoot`; [transcript](network-vm-reboot-acceptance/explore/transcript-netplan-root-dir-regenerates-live.log) |
| 5 | `systemctl enable` of the already enabled recovery unit, on every journaled change, reloads systemd; Netplan then rewrites its networkd artifacts and the next networkd reload reconfigures the uplink | Enable only when the `multi-user.target.wants` link is missing | `TestEnabledRecoveryUnitIsNotEnabledAgain`; [transcript](network-vm-reboot-acceptance/explore/transcript-systemctl-enable-reloads.log) |
| 6 | fq_codel lists each queued flow as a class under the HTB leaf; with any traffic (here IPv6 neighbour discovery after boot) HTB verification read `drift` | Ignore the leaf's own classes | Leaf-flow and foreign-class rows in `TestShapingHealthDistinguishesRateDriftFromUnreadableKernel` |
| 7 | The audit row was written with the request context: a client that disconnected after sending a mutation lost its `audit_log` row (`audit write failed: context canceled`) | Write without the request's cancellation, bounded at 5 s | `TestRecordOutlivesACanceledRequest` (fails before, passes after: [`gates/audit-regression.log`](network-vm-reboot-acceptance/gates/audit-regression.log)) |

The changed-file gate against `c31e9329` passed (`internal/netx`, `internal/audit`):
[`gates/test-changed-final.log`](network-vm-reboot-acceptance/gates/test-changed-final.log). Security
invariants are unchanged: no capability, destructive gate, typed-phrase, `hostexec` argv or migration
was altered; the audit fix strengthens "every mutation is audited".

## Open findings not fixed here

- Netplan's NetworkManager ethernet profiles are refused (`wake-on-lan=0`, above).
- networkd's global reload can reconfigure unrelated links whose generated files changed since its
  last load (above).
- After a boot recovery, and after a restart of the unit, the measured activation reads `failed`
  because `links.batch` creation lines find existing objects; runtime observations still match.
- BBR's `default_qdisc=fq` makes NIC roots `fq` after a reboot, which root shaping then refuses.
- networkd 255.4 aborted during a restart while the managed unit was restoring routes and rules.
- NetworkManager 1.46 on Ubuntu reports every dashboard-made device as "connected (externally)";
  no interference was observed.

## Host isolation and cleanup

Read-only witnesses were taken before the first VM start and after the last stop
([`witness/compare.log`](network-vm-reboot-acceptance/witness/compare.log); the raw readings name
this production host's addresses, rules and services, so only the comparison is published):
installed packages,
the account's groups, links, addresses, IPv4/IPv6 routes and rules in every table, nft tables and the
normalized ruleset, normalized iptables, unit files, running services, selected sysctls,
`/etc/systemd/system`, `/etc/just-dashboard` and QEMU processes are identical. The loaded-module list
gained `nft_queue`, `sch_netem` and `xt_NFQUEUE`; this lane ran no host-side live test, no host
namespace fixture and nothing that loads those modules (all live tests ran inside the guest), and other
lanes' fixtures were running concurrently, so they are attributed to those lanes, not proven absent
from this one. Every QEMU process was stopped by its own PID; the overlays, seeds and ephemeral keys
were removed; logs were kept. No QEMU process and no listener on 43250–43259 remained.

## What the guest does and does not prove

It proves, on a real systemd 255 / Netplan 1.1 / NetworkManager 1.46 / Linux 6.8 / iproute2 6.1
userland with actual kernel reboots: boot restoration of the managed spec and its measured result,
recovery of interrupted changes by the real timer and by the boot unit without the backend, drift
reporting and reviewed repair across reboots and a networkd restart, persistence and recovery of
three native owners across reboots, and packaged SQM restoration with exact parameter readback.

It does not prove the production host's behavior: physical NIC identity and firmware, provider
DHCP/RA timing and filtering, the Compose container layout, other distributions or owner versions,
traffic under load, or Tailscale/WireGuard transport paths. A disposable guest on this host is not
the production host.

## Proposed ledger statuses

| Row | Proposed | Reason |
| --- | --- | --- |
| C001 | implemented / acceptance pending → **verified** for reboot acceptance; keep production-host note | Actual guest reboot restored every owned object with a measured succeeded result |
| F4 | **verified** | Kills at three phases recovered by the real timer and by the boot unit after a reboot, backend dead |
| P7 | **verified** | Drift accurate after reboots and a networkd restart; reviewed repair confirmed and restored at the next boot |
| P11 | in progress | Three real owners pass persistence, timer and boot recovery; Ethernet NetworkManager refusal, global-reload limit and structural edits remain |
| P14 | **verified** for packaged boot restore | Real reboot restored IFB/CAKE with exact readback; provider/performance remain outside |
| C002 | in progress | Real process-death recovery and disconnected-client consistency pass; wider owner coverage remains |
| C003 | in progress | Real confirmation protocol exercised; application/tunnel verification remains |
| C004 | in progress | Native ownership refusals confirmed; adoption/migration remain |
| C027 | pending (separate branch) | VRF traffic and l3mdev rule restore pass for the routing branch's package in a VRF kernel |
