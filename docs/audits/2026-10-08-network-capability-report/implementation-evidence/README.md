# Networking implementation evidence

This directory records scoped acceptance for successive implementation checkpoints in PR #173.
The [implementation ledger](../implementation-status.md) preserves the full outstanding scope;
these checks do not turn the historical report scores into 10/10 ratings.

[Native/DNS combined acceptance](native-dns-integrated-acceptance.md) records the assembled
production application, passing selected API/netx/store/DNS checks and all 410 executable browser
cases with 60 optional skips. Native ancestry, earlier failures and remaining full-scope criteria
are attributed separately.

[Integrated native editor acceptance](native-editor-integrated-acceptance.md) records its matching
production build, selected API/Go tests, all 898 executable browser cases, the preserved original
nonzero invocation and exact unchanged-source timeout rerun. The subsequent v6 recovery checkpoint
and DNS service integration have separate final-source evidence; full P11 remains open.

[Native DNS service UI acceptance](dns-services-ui-acceptance.md) records the mounted private
connection/inventory and retained review controls, matching production build, 13 browser interactions,
lost-response single-use behavior and phone/desktop inspection. The native engine record and combined
reachable gate remain separate evidence; broader P17 query/zone/view/client controls remain open.

[Reviewed DNS record/client acceptance](dns-service-policy-ui-acceptance.md) extends the mounted
forms with bounded A/AAAA overrides, exact unsigned primary-zone records/TTL and existing Pi-hole
group assignment. It records selected-policy current-read freshness, immutable confirmations,
native null/empty comments and separate actual three-engine current-read/cleanup proof. Broader
P17 controls remain open; the original failed browser runs remain attributed separately.

[Reviewed custom-domain filter acceptance](dns-service-domain-filter-ui-acceptance.md) adds mounted
AdGuard suffix and Pi-hole exact-domain controls, explicit native group memberships, removable
disappeared selections, ambiguity counts and immutable current-read confirmation holds. It records
the matching production build, all 205 reachable executable browser cases, twelve captures and
separate final assembled AdGuard/Pi-hole configuration/restart/cleanup proof. Original browser and
native preflight failures remain preserved; complete P17/C074 and client filtering remain open.

[Native query-history decoding acceptance](dns-query-history-shapes-acceptance.md) corrects AdGuard's
question-name mapping and rejects fabricated or partial rows from malformed native collections.
It records explicit empty/zero/native-null distinctions, private retained-review exclusion, the
changed-file gate, selected query/API races and separately attributed current-source three-engine
owned lifecycle proof. Actual installed Technitium logger rows and broader P17/C074 remain open.

[Kernel observer acceptance](observer-kernel-acceptance.md) records the bounded native observer,
owned detach/retry, durable batch receipts and actual Docker/process-death fixtures.
[Observer UI acceptance](observer-ui-acceptance.md) records its separate measurement channels,
reviewed controls, source-matched checks, exact timeout retries and mobile/desktop inspection.
[Mounted route acceptance](observer-mounted-stop-prerequisite.md) verifies the explicit stop
prerequisite through `Server.Routes`. Combined-build acceptance and broader P8 limits remain open.

[IPAM reservation lifecycle acceptance](ipam-reservation-lifecycle.md) records initialization,
detachment and retained-draft behavior with all 20 IPAM browser cases passing. Its original selected
gate and exact unrelated retries are disclosed separately from the upcoming combined gate.

[Native DNS alias safety](dns-alias-safety.md) records P9/F10's bounded explicit walker, signed
isolated resolver acceptance, exact v257 flag audit, preserved failed iterations and focused races.
Its per-question UI still requires the integrated production-build/browser gate.

[Mandatory selected-repair confirmation](drift-required-confirmation.md) records refusal before
inspection or metadata creation and the final-source native selected repair/recovery fixture.
Its global-preference-off browser regression remains pending the integrated build at this checkpoint.

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


## Integrated investigator, saved runs, drift inspection and Docker editor

The next production build includes the investigator, Saved runs and read-only Drift pages and the
advanced Docker network editor. All 33 new feature cases passed at the tested desktop/mobile widths
after correcting API availability fixtures and modal/field locators. Those corrections change only
tests; the application build is unchanged. They are also included in the selected checks below.

`scripts/test-changed.sh 58e1089c` passed formatting, lint, TypeScript, 3,104 Bun tests with 13,632
assertions, Go build/vet, and tests in all seven selected packages. Its 11 selected browser specs
contained 331 cases: 292 passed, 38 optional evidence cases skipped and one database Query alignment
case failed while the editor was still loading. The exact failed case passed against the unchanged
production build in 40.1 seconds. All 293 executable cases therefore passed. The original script
exited 1; this is scoped acceptance with an exact rerun, not a zero exit for that invocation.
[`integration-selected-checks.txt`](integration-selected-checks.txt) records commands and results.

This checkpoint adds advisory drift inspection; the reviewed executor, dual-stack WireGuard,
external-vantage agent, shared IPAM and retained path artifacts still require their next integrated
build and acceptance. The ledger keeps those projects open. Actual reboot and off-host/provider
proof remain separate acceptance requirements.

Saved runs' reading layout was also inspected at [375 px](saved-runs-375.png),
[1280 px](saved-runs-1280.png) and [1720 px](saved-runs-1720.png). The focused screenshot cases all
pass; the desktop workbench separates independently scrolling history and evidence, and the mobile
layout keeps controls and retained scope within the viewport.
