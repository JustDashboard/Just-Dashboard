# Native DNS filter metadata acceptance, 2026-10-09

The bounded read-only filter inventory passes focused service/private API checks and actual
AdGuard Home, Pi-hole and Technitium owned-engine acceptance. This proves native configured
metadata and local-rule persistence, not subscription loading, compiled rules or effective client
filtering. Filter edits, manual/app rule contents and broader P17 acceptance remain open. The
matching frontend has [separate assembled acceptance](../../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-filter-ui-acceptance.md);
these backend records do not validate its browser behavior.

## Frozen sources and records

The isolated branch starts at public `6ebbf2d744c2680e5b163e9010b97e1659275f4d`. The initial
implementation is `b0f803b235a2ccfff6c10f85511578bcf7b2c819`; the corrected final product is
`dab8f9d7ee66d9b455e677317c2f0499e1032028`. Subsequent evidence changes do not change its Go
source. Both native race binaries were compiled with Go 1.26.8, `GOMAXPROCS=2`, `GOFLAGS=-p=2`
and an explicit short workspace `TMPDIR`. The final binary reports `-race=true`, `CGO_ENABLED=1`
and Linux/amd64.

The checked-in [records](dns-service-filters-2026-10-09/) retain the original failure separately
from the three corrected passes. Each source/binary manifest contains all 1,858 backend Go-file
hashes, `go.mod`/`go.sum` hashes, race-binary and wrapper hashes, exact child argv, working directory,
selected environment and host/resource baseline. The [verification manifest](dns-service-filters-2026-10-09/verification.json)
records byte-identical copies, source/hash checks against the named Git commit, native raw/result
agreement and cleanup. Original logs and both compiled binaries remain under
`/home/ubuntu/Just-Dashboard-network-dns-filter-inventory-artifacts`.

| Native artifact | SHA256 |
| --- | --- |
| Initial race binary | `82555eff7b9d09181a00b1584e0e767b2f401985021a51c2f82d43ef3bea155d` |
| Corrected race binary | `d1042378096846e2c18d6670ebea58aca163e3a8ff2d7a34c3607b202b84d33e` |

## Corrected actual engine proof

Each corrected run uses the same clean final checkout and compiled race binary. All three exit
zero with no skips. The immutable reviewed images were already cached; none was pulled.

| Engine / authenticated version | Wrapper / test child | Raw record |
| --- | --- | --- |
| AdGuard Home `v0.107.71` | 38.815s / 37.79s | [corrected AdGuard](dns-service-filters-2026-10-09/native-adguard-corrected.log) |
| Pi-hole FTL `v6.7.1` | 72.472s / 71.43s | [corrected Pi-hole](dns-service-filters-2026-10-09/native-pihole-corrected.log) |
| Technitium `15.6` | 14.404s / 13.37s | [corrected Technitium](dns-service-filters-2026-10-09/native-technitium-corrected.log) |

The fixture reads native inventory while the connection is dashboard read-only. Each engine
reports a known empty built-in subscription collection. AdGuard and Pi-hole additionally report
known empty custom rules, then one fixture-owned local rule after a closed native setup request.
The rule's metadata fingerprint changes and remains equal after container restart. Technitium
keeps its unsupported manual/app rule inventory explicit; no rule content is invented from empty
built-in subscription settings. The existing auth, local override/authoritative record/client
review, exact-selection current reads, UDP/TCP answer, restart, owned removal and cold interrupted
predecessor cleanup assertions also pass. The runs retain 8, 12 and 8 selection-current reads
respectively, without claiming or replaying reviews.

No subscription URL is added, refreshed or fetched by these acceptance steps. Nonempty source
metadata, URL/rule/comment secrecy, groups and malformed/oversized responses are covered by
controlled HTTP tests, not by a real subscription download. Native filter inventory itself makes
only management reads; the fixture's local-rule setup and pre-existing reviewed DNS operations
are separately owned test effects.

The exact executed wrappers are retained as [initial](dns-service-filters-2026-10-09/filter-native-runner.py)
and [corrected](dns-service-filters-2026-10-09/filter-native-runner-corrected.py) records. Each invokes
the selected binary with:

```text
timeout --kill-after=5s 240s <race-binary> -test.run ^TestDNSServiceNativeOwnedEngine$ -test.count=1 -test.timeout=235s -test.v
```

The fixture's native work has its own 180-second context. Runs are serial, use only high
loopback ports and labeled dedicated Docker resources, and leave the host resolver unchanged.
Every result records no owned test process, container, network, volume or temporary entry.
The task temporary directory is removed. Production networkd remains PID 883/start time 452,
host NetworkManager paths remain absent, and the before/after resolver SHA256 matches. These are
owned-resource and host-state checks, not reboot or independent timer proof.

## Original failure and narrow correction

The initial AdGuard run exits one after 8.790s wrapper / 8.76s child. Its
[raw record](dns-service-filters-2026-10-09/native-adguard.log) reports partial unknown source/rule
evidence where the fixture expected a complete supported empty collection. No native filter
response body was captured in that run; its log is not evidence of a captured JSON null value.
Owned-resource cleanup and unchanged host state are recorded independently of its failure.

The [pinned 0.107.71 writer](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.71/internal/filtering/http.go)
serializes empty subscription slices and a nil custom-rule slice as explicit null. The correction
accepts only present null fields from that exact authenticated version as zero entries; other
versions' null and missing fields stay unknown. Corrected shape-only diagnostics confirm the
three initial field kinds without logging their values. Source and custom-rule failures are
independent: malformed or missing rules do not erase valid source evidence, and malformed or
missing sources do not erase valid custom-rule evidence. An invalid shared native envelope or
enable field still leaves both unknown. The original log, binary and source manifest are retained
unchanged; the corrected passes do not overwrite or reclassify it.

## Scoped checks and boundaries

The corrected product passed [DNS filter races](dns-service-filters-2026-10-09/filter-race-pinned-null.log)
in 6.021s. They cover exact pinned-null compatibility, independent sections, full selected raw
metadata drift, same-origin distinct entries, missing/malformed fields, duplicate IDs/groups,
entry/rule/body bounds, redacted URL and rule identities, unsupported versions, actual cancellation,
unavailable read-only reads and connection-generation replacement during a blocked native read.
The [private API race](dns-service-filters-2026-10-09/filter-api-race-final.log) passes in 3.961s on
the initial implementation phase; its API source and test are byte-identical in the corrected
product. It checks system-admin capability, private/no-store responses, narrowed/viewer refusal,
method/unknown-connection refusal and unavailable/partial responses without native body secrets or
mutation. The corrected required gate also runs that API package.

The [required scoped gate](dns-service-filters-2026-10-09/test-changed-pinned-null.log),
`scripts/test-changed.sh 6ebbf2d744c2680e5b163e9010b97e1659275f4d`, exits zero with bounded Go
build/vet and package tests: API 0.431s, DNS services 0.502s. No frontend or whole-browser suite is
selected. Ordinary tests run with the live-engine opt-in unset; their opt-in fixture skip is not
native acceptance. The three selected native invocations above have no skips.

The [owning contract](../network-dns-services.md#read-only-native-filter-metadata) defines the
closed DTO and pinned primary sources. Reads retain the sealed literal-origin/TLS/auth boundary,
20-second deadline, generation/owned-resource recheck and `system.admin` private/no-store route.
Section metadata is intentionally redacted and fingerprinted; missing evidence is not an empty or
healthy policy. Runtime status is separate from loaded contents and client decisions. There is
no filter mutation, arbitrary payload/proxy, subscription fetch or frontend-completion claim in
this slice.
