# Read-only native DNS filter metadata acceptance

This P17 checkpoint mounts a separate native filter reading before reviewed change controls on
managed or dashboard read-only connections. It reports configured subscription/custom-rule metadata,
redacted identities and native group memberships. Loaded rules, subscription downloads, filtering
priority and client decisions remain separate and unmeasured. The full 187-requirement ledger stays
open; this does not close P17 or the detector-linked C074 handoff.

## Application source and build

The final assembled application source is `e945fcea61f56ace9dfee48135bb1a9fc14f838f`. Its
[production build](dns-service-filter-ui/closed-groups-final/build.log) exits zero in 21.85 seconds and generates
all 73 static pages. Its managed server listens only on `127.0.0.1:43149`; the DNS page returns
HTTP 200. This is frontend availability, not evidence of a running production API or real filtering.

The [final source manifest](dns-service-filter-ui/closed-groups-final/source-build-validation.json)
hashes all 1,536 tracked frontend source/package/lock files, both browser fixture/spec files,
1,860 backend Go files and both module files. All three final native manifests independently match
those backend/module hashes and the same compiled race binary. The earlier `2dc3d86c` application,
its build, captures and 1,858-file comparison with `dab8f9d7` remain separately attributed below.
The final source includes stricter native/request group parsing and the unknown client/group
count correction; it has its own fresh build and complete reachable gate.

Text copies remove only carriage returns and trailing line/EOF whitespace. Each original also has
a deterministic gzip copy that decompresses byte-for-byte; the
[final normalization manifest](dns-service-filter-ui/closed-groups-final/log-copy-normalization.json)
records both hashes; the [earlier manifest](dns-service-filter-ui/log-copy-normalization.json) is retained.
Original workspace logs are retained under `/home/ubuntu/jd-network-validation-tmp`.

## Reachable local checks

The [final required gate](dns-service-filter-ui/closed-groups-final/required.log),
`scripts/test-changed.sh 6ebbf2d744c2680e5b163e9010b97e1659275f4d`, exits zero in 521.43 seconds.
Formatting, lint, TypeScript, 3,255 Bun tests / 14,323 assertions and selected Go build/vet pass.
The selected API and DNS packages pass in 0.591s and 25.063s. Browser selection covers
189 cases in the design-system, network-audit, DNS evidence, filter, service and DNS specs: 181 pass,
and eight optional `JD_NETWORK_SHOTS` captures remain skipped and unverified. This is the changed
surface gate, not the whole browser suite.

The ten focused filter decoder tests cover 70 assertions, including false switches, zero metadata,
explicitly empty memberships, same-origin distinct identities, full connection replacement,
configured/unknown consistency, engine-specific entry kinds and malformed secret-bearing origins.
Technitium accepts exactly 255 subscription entries and refuses 256, matching its backend bound.

All 18 filter browser cases pass. They cover all three engine presentations on read-only
connections; failed and native-unavailable refreshes retain dated same-owner rows; partial reads
replace unreadable sections without a zero-policy count; changed generations clear old rows;
malformed origins expose no returned secrets; reader accounts make no native filter requests.
Unknown ordinary client/group evidence also withholds section counts rather than inventing zero.
All filter cases assert no mutation. The existing 45 DNS service interactions also pass on this build.

The [final results record](dns-service-filter-ui/closed-groups-final/results.json) retains the exact command
base, counts, source, build, capture scope and inspected examples.

## Settled phone and desktop inspection

Nine engine/width cases cover AdGuard, Pi-hole and Technitium at 390, 1280 and 1720 pixels. They wait
for the sheet's actual rectangle to fit the viewport, then require left at least zero, right at most
viewport width plus one pixel and internal horizontal overflow at most one pixel. Counts sit beside
their Reading-register sections; fingerprints wrap; longer native inventories scroll vertically.
The phone cases also capture custom-rule sections, retaining
[all 12 final captures and hashes](dns-service-filter-ui/closed-groups-final/screenshots.json).

Inspected examples include the [phone Pi-hole inventory](dns-service-filter-ui/closed-groups-final/native-filters-pihole-390.png),
[phone AdGuard rules](dns-service-filter-ui/closed-groups-final/native-filter-rules-adguard-390.png),
[phone Technitium inventory](dns-service-filter-ui/closed-groups-final/native-filters-technitium-390.png),
[desktop Pi-hole inventory](dns-service-filter-ui/closed-groups-final/native-filters-pihole-1280.png),
[wide AdGuard inventory](dns-service-filter-ui/closed-groups-final/native-filters-adguard-1720.png) and
[desktop Technitium inventory](dns-service-filter-ui/closed-groups-final/native-filters-technitium-1280.png).
These are mocked metadata and browser layout evidence, separate from actual engine observations.

## Closed native shapes and final engine proof

AdGuard null or non-string rule elements remain unknown, while an actual empty string stays a
distinct native rule entry. The exact pinned AdGuard whole-collection null compatibility remains
separate. Pi-hole source destinations must be nonempty. Raw group arrays refuse null, non-integer,
duplicate and out-of-range elements; explicit empty arrays and native ID zero remain valid.
The same closed group contract covers client-change requests before connection selection and
native client/group inventories before they can become a review baseline. Malformed memberships,
group identities or enable fields withhold the affected inventory and its ordinary UI count.

The [isolated client correction manifest](dns-service-filter-ui/closed-groups-final/client-group-shapes-validation.json)
retains its `05859bbd` source, original regression failure, corrected checks, required gate and
focused API/DNS races (4.034s / 69.007s). Its backend differs from the final assembled source only
in `filters.go` and `filters_test.go`; its results are not relabeled as an assembled engine run.
The [Pi-hole filter-group races](dns-service-filter-ui/closed-groups-final/filter-groups-race.log)
and intermediate [rule-element](dns-service-filter-ui/closed-groups-final/required-rule-elements.log)
and [group/destination gates](dns-service-filter-ui/closed-groups-final/required-groups-destination.log)
retain their earlier phases. The final gate above runs the assembled API/DNS packages afresh.

The exact final race binary is
`5d78c5a84b7cd39f3647d0e457f7a22f1b54574bee3f92e89ffb5bd1bf39ee06`.
All three final native runs use clean `e945fcea`, Go 1.26.8, `GOMAXPROCS=2`, `GOFLAGS=-p=2`,
the same race binary and the [owned serial runner](dns-service-filter-ui/closed-groups-final/native-runner.py).
Each [source/binary manifest](dns-service-filter-ui/closed-groups-final/native-adguard.source-binary.json)
records full hashes, argv, environment and host baseline; the corresponding Pi-hole and Technitium
manifests are retained beside it. All three exit zero without skips or image pulls.

| Engine / authenticated version | Wrapper / test child | Raw byte record / cleanup |
| --- | --- | --- |
| AdGuard Home `v0.107.71` | 43.324s / 42.29s | [raw](dns-service-filter-ui/closed-groups-final/native-adguard.log.raw.gz), [result](dns-service-filter-ui/closed-groups-final/native-adguard.result.json) |
| Pi-hole FTL `v6.7.1` | 72.962s / 71.92s | [raw](dns-service-filter-ui/closed-groups-final/native-pihole.log.raw.gz), [result](dns-service-filter-ui/closed-groups-final/native-pihole.result.json) |
| Technitium `15.6` | 26.033s / 25.00s | [raw](dns-service-filter-ui/closed-groups-final/native-technitium.log.raw.gz), [result](dns-service-filter-ui/closed-groups-final/native-technitium.result.json) |

Actual native proof includes read-only known empty subscriptions, AdGuard/Pi-hole local-rule
metadata changing and persisting across restart, explicit valid Pi-hole client memberships,
selected-policy current reads, existing DNS record/override operations, real UDP/TCP answers,
restart and interrupted-predecessor no-replay cleanup. Technitium manual/app rules stay unsupported.
Each run leaves zero owned processes, containers, networks, volumes or temporary entries; host
resolver hashes, networkd PID/start and absent NetworkManager paths match before/after witnesses.
Malformed JSON and nonempty redacted sources remain controlled HTTP/API evidence. These runs do
not fetch subscriptions or measure effective client filtering. The earlier `379a43fb` compiled
binary is explicitly [preparation only](dns-service-filter-ui/closed-groups-final/preparation-379a43fb.json)
and was never executed against an engine.

## Preserved initial results

The [initial build](dns-service-filter-ui/build-initial-failed.log) exits one because Turbopack
refuses a dependency-directory symlink outside the project filesystem root. Only that verified
task-owned symlink was removed; its target was preserved. The
[frozen Bun install](dns-service-filter-ui/install-frozen.log) installs the existing dependency
lock without changes, and the [next build](dns-service-filter-ui/build-initial-passed.log) succeeds.
No package, dependency or lockfile change is part of this checkpoint.

The [initial direct browser run](dns-service-filter-ui/browser-initial.log) passes all 17 cases in
29.3 seconds. Its width assertion checked internal overflow but did not wait for the opening
transition or verify actual viewport containment. Review finds clipped opening-transition captures
at [390 pixels](dns-service-filter-ui/initial-native-filters-pihole-390.png) and
[1720 pixels](dns-service-filter-ui/initial-native-filters-pihole-1720.png); the later
[phone custom-rule capture](dns-service-filter-ui/initial-native-filter-rules-pihole-390.png)
has already settled. The [initial capture manifest](dns-service-filter-ui/initial-screenshots.json)
retains all 12 original hashes and workspace locations. They are not relabeled as corrected layout
proof. The later rectangle wait and viewport assertions produce the final captures above.

The [earlier integrated gate](dns-service-filter-ui/required-initial.log) also exits zero in
590.25 seconds, with 3,254 Bun tests / 14,321 assertions, API 0.478s, DNS 0.621s and 180 browser
passes / eight optional skips. Its `cd6bacf5` source precedes the Technitium bound and capture
corrections; the final command above verifies those changes on the rebuilt application.

The intermediate `cf09046f` capture correction was assembled at
`2dc3d86cd26a8c016721bad4259236f922247ce0`: its [build](dns-service-filter-ui/build-final.log)
passed in 23.71s and its [gate](dns-service-filter-ui/required-final.log) passed in 496.84s,
with 3,255 Bun tests / 14,323 assertions and 180 browser passes / eight optional skips.
Its [1,536-frontend/1,858-backend source manifest](dns-service-filter-ui/final-source-validation.json),
[results](dns-service-filter-ui/browser-build-results.json) and
[12 captures](dns-service-filter-ui/screenshots.json) retain that historical source.
The later final build and three engine runs above include the group/count corrections.

The [native backend records](../../../internal/backend/evidence/dns-service-filters-2026-10-09.md)
retain their own original AdGuard failure and corrected three-engine passes. Known empty
subscriptions and AdGuard/Pi-hole local-rule metadata persistence are actual native evidence;
nonempty source secrecy, malformed responses and these viewport cases are controlled fixtures.
This read-only checkpoint does not establish filter mutation. The separate
[reviewed custom-domain checkpoint](dns-service-domain-filter-ui-acceptance.md) adds bounded AdGuard
suffix and Pi-hole exact-domain controls with its own mounted/native proof. Subscription and
manual/app controls, broader query/zone/view/client/DHCP coverage, complete native lifecycle/platform
coverage, host reboot and measured effective filtering remain open.
