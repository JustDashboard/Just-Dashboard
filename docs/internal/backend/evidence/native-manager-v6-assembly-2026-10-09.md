# Native manager v6 production assembly proof

The direct NetworkManager and networkd automatic-owner fixtures pass on the exact production
assembly from `3e0c20161b721de9f769a13439b9813f3f7dfed3`. This proof closes the source mismatch
between that assembly and the stricter experimental structural ancestry used by the
[earlier automatic fixture evidence](native-manager-automatic-2026-10-09.md). It adds no product
changes or admission exceptions.

## Frozen source

The isolated branch `verify/network-native-v6-assembly` is based on exact `3e0c2016`, with only
fixture/documentation commit `1d63d9646625846043da8345e149a024c4d36c0e` cherry-picked as
`b1840c2fc2e52d01e80618a09ed4c33d94694c25`. Its complete backend diff contains only the new
`native_manager_auto_live_test.go`; the production read, loaded-profile, field guards and v6
recovery files match the base. Both native cases use one frozen race binary and independently
build the standalone recovery helper from this same worktree.

| Artifact | SHA256 |
| --- | --- |
| Race fixture binary | `63c41e0cbdab33b18f89a96b1fdbe80282c9c0e71df5d3d69a7a371ea4bda0cf` |
| Fixture source | `543f7f1cc53be196ad0caabac013bc4d59073ace26ec495334d1c5f265b8df6f` |
| Actual standalone helper, both cases | `8a6b5ad325bccacad61952506c6de28cc9e1309d592c3d566cd3d1aa6f6cf02e` |
| Source/binary manifest | `77228403e1735bfa0f48456701b52958cbe048c8cc9baf01682a18f7eebf5245` |
| Direct NM raw log | `a0a3fa1eda3c6955f164dd9f794201651a58522d704b25925d64834682722d5c` |
| Networkd raw log | `52b4d3674081016dfd10d81cc1d2c2acc9899bc70c49b3fc55783bee1ba00eec` |

The actual helper advertises `jd-native-manager-v6`. The read-only Debian NM 1.52.1 userland is
the independently verified bundle described in the earlier evidence; no packages, host services
or kernel modules were installed or changed.

## Actual results and cleanup

| Case | Result | Wrapper / private child |
| --- | --- | --- |
| Direct Debian NetworkManager 1.52.1 | PASS, no skip | 30.69s / 23.18s |
| Direct systemd-networkd | PASS, no skip | 17.08s / 10.45s |

Both cases measure real DHCPv4 leases, IPv6 SLAAC, acquired defaults, active-owner DNS/domains,
automatic-to-manual activation, automatic DNS/routes suppression, independent rollback,
applying-backend process death, fresh standalone-helper recovery, durable confirmation and
cleanup retry. The NM case also creates a real three-second native checkpoint deadline,
replaces a selected file with an independent foreign inode, and verifies that recovery refuses
the foreign write, releases the exact owned checkpoint, and retains the degraded manual-review
journal. The foreign file and both restore stages remain byte-for-byte and inode-identical
past the actual deadline. Fixture cleanup alone restores its captured candidate.

Each bounded private PID/mount/network namespace closes its owned RA socket, drains its sender,
reaps DHCP/manager/private-bus children, and removes lease/PID files. After both wrappers exit,
an anchored process search finds no owned fixture, NM, private-bus or DHCP process; their task
temporary directories are removed. The original host networkd process remains PID 883, and
host `/run/NetworkManager` and `/var/lib/NetworkManager` remain absent. The shared native lane
was released only after these checks.

This is direct-owner production assembly proof. Actual Ubuntu generated-origin compatibility
has a separate exact-source proof. The stricter experimental generated-profile closure refusal
is a separate investigation. Cold boot, actual independent systemd timer dispatch, unsupported
creation/adoption and the unfinished existing bond/VRF transactions are not established here.

## Reproduction and retained records

Compile from `backend/` with `GOMAXPROCS=2`, `GOFLAGS=-p=2` and a workspace `TMPDIR`:

```bash
go test -race -c -o ../out/native-v6-assembly/netx-auto.test ./internal/netx
```

From `backend/internal/netx`, after the shared native lane is clean, run the two selected cases
serially with the same binary:

```bash
timeout --kill-after=5s 240s env \
  TMPDIR=/home/ubuntu/Just-Dashboard-network-native-v6-assembly/out/native-v6-assembly \
  GOMAXPROCS=2 GOFLAGS=-p=2 JD_NETNS_LIVE=1 JD_NATIVE_AUTO_CASE=NetworkManager \
  JD_NATIVE_MANAGER_NM_ROOT=/home/ubuntu/Just-Dashboard-network-native-managers-artifacts/native-tools/debian/extracted \
  /home/ubuntu/Just-Dashboard-network-native-v6-assembly/out/native-v6-assembly/netx-auto.test \
  -test.run '^TestNativeManagerAutomaticOwnerLive$' -test.count=1 -test.timeout=235s -test.v
```

Use `JD_NATIVE_AUTO_CASE=networkd` for the second case. Both wrappers and their children are
bounded. There is no full test suite or unselected native owner run.

Raw logs and the source/binary manifest are checked in under
`docs/audits/2026-10-08-network-capability-report/implementation-evidence/native-v6-assembly/`.
The original logs, frozen binary and mandatory gate record are retained under
`/home/ubuntu/Just-Dashboard-network-native-v6-assembly/out/native-v6-assembly/`.

The mandatory `scripts/test-changed.sh 3e0c2016` gate passes with native opt-in variables unset:
backend build, netx vet and the new fixture's ordinary compilation/opt-in skip pass (`netx 0.031s`).
Its retained record has SHA256
`7ec582b7d1004d978c7a5733c4ac92109792122ec4157fa668557a8266288111`. This skip is preparation
evidence; both actual native cases above pass without a skip. The subsequent documentation-only
gate and `git diff --check` also pass.

The documentation review covers `docs/internal/`, `AGENTS.md`, `README.md` and `CONTRIBUTING.md`.
This fixture/evidence-only change adds no dependencies, product behavior or contributor workflow.
The existing native-manager guides continue to state the uncompleted structural and cold-runtime
boundaries.
