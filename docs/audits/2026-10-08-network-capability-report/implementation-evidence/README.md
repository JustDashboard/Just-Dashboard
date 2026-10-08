# Networking implementation evidence

This directory records scoped acceptance for the first implementation checkpoint in PR #173.
The [implementation ledger](../implementation-status.md) preserves the full outstanding scope;
these checks do not turn the historical report scores into 10/10 ratings.

## Native network and independent recovery

[`native-network-race.txt`](native-network-race.txt) is the successful integrated invocation from
the implementation worktree on 2026-10-08:

```bash
cd backend
GOMAXPROCS=2 JD_NETNS_LIVE=1 JD_SYSTEMD_RECOVERY_LIVE=1 go test -race ./internal/netx -run Live -count=1 -v
```

The lane passed in 180.747 seconds. It includes real nftables and both admission families, cache
loss and cache/runtime crash recovery, exact shaping parameters and first-apply baseline recovery,
backend death between journal phases, cold-runtime dependency reconstruction, and real transient
systemd timers with a standalone helper. The pending-change fixture keeps the kernel route stable
while the actual HTTP path fails, kills the applying backend and observes recovery at the real
ninety-second deadline.

Every mutation is inside disposable network/mount namespaces and temporary state. The real timers
are uniquely named, use `NetworkNamespacePath`, and are cleaned by the fixture. This establishes
timer dispatch and cold-runtime reconstruction, not an actual host reboot, provider reachability,
general service/tunnel verification, throughput, bufferbloat, or all native-manager combinations.

## Frontend evidence

The production build includes the type-check pass and serves on an explicitly managed loopback
port. Browser fixtures use documentation addresses and simulated API responses. They establish
what the UI displays and submits; the native lane above independently establishes supported kernel
and recovery behavior. Matching IPv6 routing screenshots compare the active base `7be11ba0` with
the implementation using the same read-only IPv6-only fixture at desktop and mobile widths.

The final production `bun run build` passed, including TypeScript. The screenshots are:

| Width | Base before implementation | Implementation |
| --- | --- | --- |
| Mobile, 390 px | [Before](routing-ipv6-before-390.png) | [After](routing-ipv6-after-390.png) |
| Desktop, 1440 px | [Before](routing-ipv6-before-1440.png) | [After](routing-ipv6-after-1440.png) |

The same IPv6-only fixture leaves the old diagram empty; the new diagram selects IPv6, shows its
rules/table/browser path, labels the highlight as inferred and offers a read-only kernel lookup.
This comparison is about rendered behavior, not measured Internet connectivity.

## Changed-file validation

`scripts/test-changed.sh origin/research/network-capability-report` selected the packages and 33
browser specs reachable through the shared frontend API changes. Prettier, ESLint, TypeScript,
3,082 Bun tests, Go build and selected-package vet passed. The script's Go test stage encountered
five existing deployment preflight fixtures because its temporary data directory was on a `/tmp`
filesystem with less than 1 GiB available. The exact selected packages then passed with `TMPDIR`
on the workspace filesystem; [`selected-go.txt`](selected-go.txt) records that command and result.

The unchanged production build completed the selected 955 browser cases: 880 passed, 60 optional
evidence cases skipped and 15 failed while the shared host was under memory pressure. A one-worker
rerun with temporary files on the workspace filesystem passed 14 of those 15; the remaining WebGL
fixture initially lacked its renderer, then passed unchanged in a focused one-case rerun (21.9s).
All 895 selected executable cases therefore passed, with 60 optional evidence cases skipped.
[`browser-reruns.txt`](browser-reruns.txt) records the exact focused commands and results.

The original script exited nonzero. Its failing Go fixtures and browser cases were rerun without
source changes; the independently successful stages above establish the selected acceptance, not
a fabricated zero exit for that invocation. All current-SHA hosted checks on `58e1089c`, including
eight browser shards, also passed. Missing broader acceptance remains pending in the ledger.
