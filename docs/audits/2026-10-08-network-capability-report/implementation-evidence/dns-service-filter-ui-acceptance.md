# Read-only native DNS filter metadata acceptance

This P17 checkpoint mounts a separate native filter reading before reviewed change controls on
managed or dashboard read-only connections. It reports configured subscription/custom-rule metadata,
redacted identities and native group memberships. Loaded rules, subscription downloads, filtering
priority and client decisions remain separate and unmeasured. The full 187-requirement ledger stays
open; this does not close P17 or the detector-linked C074 handoff.

## Application source and build

The final UI correction is `cf09046f`; the assembled build source is
`2dc3d86cd26a8c016721bad4259236f922247ce0`. The
[production build](dns-service-filter-ui/build-final.log) exits zero in 23.71 seconds and generates
all 73 static pages. Its managed server listens only on `127.0.0.1:43149`; the DNS page returns
HTTP 200. This is frontend availability, not evidence of a running production API or real filtering.

The [source manifest](dns-service-filter-ui/final-source-validation.json) hashes all 1,536 tracked
frontend source/package/lock files and the two browser fixture/spec files. It also independently
checks all 1,858 backend Go and both module hashes against the three earlier native manifests at
that assembled checkpoint, without relabeling their `dab8f9d7` source or binary. Later backend
corrections and their actual native checks are separately attributed below; these UI files and
the build remain unchanged.

Text copies remove only carriage returns and trailing line/EOF whitespace. Each original also has
a deterministic gzip copy that decompresses byte-for-byte; the
[normalization manifest](dns-service-filter-ui/log-copy-normalization.json) records both hashes.
Original workspace logs are retained under `/home/ubuntu/jd-network-validation-tmp`.

## Reachable local checks

The [final required gate](dns-service-filter-ui/required-final.log),
`scripts/test-changed.sh 6ebbf2d744c2680e5b163e9010b97e1659275f4d`, exits zero in 496.84 seconds.
Formatting, lint, TypeScript, 3,255 Bun tests / 14,323 assertions and selected Go build/vet pass.
The selected API and DNS packages return cached successful test results. Browser selection covers
188 cases in the design-system, network-audit, DNS evidence, filter, service and DNS specs: 180 pass,
and eight optional `JD_NETWORK_SHOTS` captures remain skipped and unverified. This is the changed
surface gate, not the whole browser suite.

The ten focused filter decoder tests cover 70 assertions, including false switches, zero metadata,
explicitly empty memberships, same-origin distinct identities, full connection replacement,
configured/unknown consistency, engine-specific entry kinds and malformed secret-bearing origins.
Technitium accepts exactly 255 subscription entries and refuses 256, matching its backend bound.

All 17 new filter browser cases pass. They cover all three engine presentations on read-only
connections; failed and native-unavailable refreshes retain dated same-owner rows; partial reads
replace unreadable sections without a zero-policy count; changed generations clear old rows;
malformed origins expose no returned secrets; reader accounts make no native filter requests.
All cases assert no mutation. The existing 45 DNS service interactions also pass on this build.

The [results record](dns-service-filter-ui/browser-build-results.json) retains the exact command
base, counts, source, build, capture scope and inspected examples.

## Settled phone and desktop inspection

Nine engine/width cases cover AdGuard, Pi-hole and Technitium at 390, 1280 and 1720 pixels. They wait
for the sheet's actual rectangle to fit the viewport, then require left at least zero, right at most
viewport width plus one pixel and internal horizontal overflow at most one pixel. Counts sit beside
their Reading-register sections; fingerprints wrap; longer native inventories scroll vertically.
The phone cases also capture custom-rule sections, retaining
[all 12 final captures and hashes](dns-service-filter-ui/screenshots.json).

Inspected examples include the [phone Pi-hole inventory](dns-service-filter-ui/native-filters-pihole-390.png),
[phone AdGuard rules](dns-service-filter-ui/native-filter-rules-adguard-390.png),
[phone Technitium inventory](dns-service-filter-ui/native-filters-technitium-390.png),
[desktop Pi-hole inventory](dns-service-filter-ui/native-filters-pihole-1280.png),
[wide AdGuard inventory](dns-service-filter-ui/native-filters-adguard-1720.png) and
[desktop Technitium inventory](dns-service-filter-ui/native-filters-technitium-1280.png).
These are mocked metadata and browser layout evidence, separate from actual engine observations.

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

The [native backend records](../../../internal/backend/evidence/dns-service-filters-2026-10-09.md)
retain their own original AdGuard failure and corrected three-engine passes. Known empty
subscriptions and AdGuard/Pi-hole local-rule metadata persistence are actual native evidence;
nonempty source secrecy, malformed responses and these viewport cases are controlled fixtures.
Filter mutation, manual/app rules, broader query/zone/view/client/DHCP controls, complete native
lifecycle/platform coverage, host reboot and measured effective filtering remain open.
