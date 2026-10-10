# Reviewed native custom-domain filter acceptance, 2026-10-09

The bounded backend/API controls pass scoped checks, focused races and actual owned AdGuard Home
and Pi-hole acceptance. AdGuard uses generated allow/deny domain-suffix rules; Pi-hole uses exact
domain rows with explicit existing group memberships. This verifies native configuration, retained
selection/readback, preservation and container restart persistence. Effective client filtering,
native policy precedence and complete P17 acceptance remain open. Matching mounted acceptance was
pending at the isolated checkpoint below; the later
[assembled application record](../../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-domain-filter-ui-acceptance.md)
now retains its separate fresh build, reachable checks and viewport evidence.

## Frozen product and records

The isolated task starts at `1c624cd883adda9e6f069adbf4349ea68db9d4db`. The narrow inventory
correction is `bf698a83`; the separate controls are `b8103fe3`. The task then carries the shared
strict Pi-hole membership decoder and older client correction before reusing that decoder in the
new controls. Final product source is `f3fe06aac9ffd4921a3740925650f442cc1a6988`; later evidence
changes do not change its Go source. The preceding isolated inventory and root's assembled
read-only inventory proofs keep their own source/binary attribution.

The [records](dns-service-domain-filters-2026-10-09/) retain exact commands, all 1,863 backend Go
hashes, both module hashes, binary/runner hashes, native raw/result records and scoped checks.
The [verification manifest](dns-service-domain-filters-2026-10-09/verification.json) independently
matches both native source maps against the named Git commit and current files, the compiled
binary and raw results, and byte-identical copies against workspace originals. Original logs and
the compiled binary remain under
`/home/ubuntu/Just-Dashboard-network-dns-domain-filter-controls-artifacts`.

The [compiler record](dns-service-domain-filters-2026-10-09/domain-native-compile.json) retains
Go 1.26.8, race instrumentation, CGO enabled, Linux/amd64 and binary SHA256
`2c3125e20fc193f618be1b4c368c6d6965ebb663121a27a2b0b5f6716455ceb0`.
Both actual engines use that same binary and clean frozen source. Images were already cached;
neither run pulls an image or adds/fetches a subscription URL.

## Actual owned engine proof

| Engine / authenticated version | Wrapper / test child | Raw / source / cleanup |
| --- | --- | --- |
| AdGuard Home `v0.107.71` | 65.532s / 64.49s | [raw](dns-service-domain-filters-2026-10-09/native-adguard-first.log), [source](dns-service-domain-filters-2026-10-09/native-adguard-first.source-binary.json), [result](dns-service-domain-filters-2026-10-09/native-adguard-first.result.json) |
| Pi-hole FTL `v6.7.1` | 87.575s / 86.55s | [raw](dns-service-domain-filters-2026-10-09/native-pihole-first.log), [source](dns-service-domain-filters-2026-10-09/native-pihole-first.source-binary.json), [result](dns-service-domain-filters-2026-10-09/native-pihole-first.result.json) |

Both selected fixtures exit zero without skips. They stage and apply allow and deny rules, reject
duplicate staging and consumed replay, inspect exact retained selections before and after effects,
restart the owned engine and remove each reviewed rule. AdGuard restores the original ordered
comment, blank-string entry and unrelated deny rule fingerprint. Pi-hole verifies allow group
`[0]` and deny membership `[]`, then restores the original foreign row/comment/group fingerprint.
Source section fingerprints remain unchanged. Native selected/unselected full fingerprints also
cover ordinary policy and existing native group metadata. The runs retain 16 and 20 exact-selection
current reads respectively, without claiming or replacing retained reviews.

The existing local A/AAAA override, Pi-hole client-membership, UDP/TCP answer, credential/config
restart, separately owned removal and cold interrupted-predecessor cleanup checks also pass.
Those DNS questions verify local answer overrides, not filtering decisions for the new domains.
The fixture deliberately keeps protection disabled; configured suffix/exact semantics follow the
pinned native contracts. Nonempty subscription/group metadata, malformed shapes and secret-bearing
source preservation are controlled HTTP/API evidence, not an actual subscription download.

The [serial runner](dns-service-domain-filters-2026-10-09/domain-native-runner-first.py) invokes:

```text
timeout --kill-after=5s 240s <race-binary> -test.run ^TestDNSServiceNativeOwnedEngine$ -test.count=1 -test.timeout=235s -test.v
```

Native work has its own 180-second context. Each result records zero owned test processes,
containers, networks, volumes and task temporary entries; the task directory is removed. Host
resolver SHA256 stays `9b5b6ef96476a3c090e8e5d1b98ae55802c227581143800dcde7c075f0ff5a02`,
production networkd stays PID 883/start time 452, and host NetworkManager paths stay absent.
No host manager/configuration changes, independent timer or host reboot acceptance is claimed.
The native lane was released after both terminal cleanup checks.

## Scoped checks and preserved preparation failure

The [final scoped gate](dns-service-domain-filters-2026-10-09/domain-filter-required-final.log),
`scripts/test-changed.sh bf698a83b0ec47f54ae50abee3e0c9c6b77db532`, exits zero on the final source:
Go build/vet, selected API tests 0.715s and DNS service tests 35.204s. The
[focused races](dns-service-domain-filters-2026-10-09/domain-filter-race-final.log) exit zero with
DNS services 135.658s and API 4.335s:

```text
go test -race ./internal/dnsservice ./internal/api -run '^TestDNS(DomainFilter|Filter)' -count=1 -timeout=180s
```

Checks use `TMPDIR=/home/ubuntu/jd-fi-t`, `GOMAXPROCS=2` and `GOFLAGS=-p=2`. They cover closed
engine-specific intents, explicit empty groups, null/noninteger memberships and identities,
duplicate/modified/disabled/missing targets, entry/count/version bounds, cancellation, raw drift
at staging and immediately before effects, unselected rules/comments/groups/sources, generation
and expiry, unreadable retained intent, lost/failed native responses, foreign readback changes,
single-use refusal, private administrator/token capability, destructive apply and audit redaction.
Ordinary package checks keep the live-engine opt-in unset; their opt-in skip is not native proof.
No frontend or whole browser suite is selected by this backend change.

The [first unit/API preparation log](dns-service-domain-filters-2026-10-09/domain-filter-unit-first.log)
exits one because its test-only API fixture rejected the existing bounded query-history limit
query. The guard was narrowed to filter paths; production query/transport behavior was unchanged.
Corrected and expanded preparation logs remain separate from that failure and the final source
gate/races. The [validation manifest](dns-service-domain-filters-2026-10-09/domain-filter-validation.json)
retains this qualification and their original hashes. No native attempt failed in this control
batch; the earlier inventory compatibility failure remains in its own owning record.

The [owning contract](../network-dns-services.md#reviewed-custom-domain-filters) defines the DTO,
complete selected raw baseline and pinned APIs. Credentials, raw rule bodies and unselected
native metadata stay private. Read-only defaults, literal-origin/TLS/auth sealing, administrator
private/no-store routes, generation rechecks and destructive audit remain enforced. This exposes
no arbitrary native DSL/regex, subscription controls, Technitium manual/app effects or provisioning
replay. An uncertain effect remains `needs_review`, without retry or foreign-policy restoration.

## Separately assembled backend proof at b7038998

The following proof uses clean assembled source
`b7038998c668fc915404b51a0b2236e68fe148fa`, not the isolated `f3fe06aa` source above. Its backend
diff from that earlier proof is confined to `filters.go` and `filters_test.go`; the owned native
fixture is unchanged. The [assembled records](dns-service-domain-filters-2026-10-09/assembled-b703/)
retain the complete compiler/source receipts, both wrappers, the separate wrapper preflight failure,
both actual native logs/results and deterministic gzip copies of every original log.
The [verification manifest](dns-service-domain-filters-2026-10-09/assembled-b703/verification.json)
matches all 1,863 backend Go files and both modules against the named Git source, both native
manifests and the current backend files. It also records original/compressed/display-log hashes.
Terminal and trailing-whitespace normalization changed none of these four logs; all checked-in
display logs remain byte-identical to their workspace originals.

The [compiler receipt](dns-service-domain-filters-2026-10-09/assembled-b703/domain-native-compile.json)
records Go 1.26.8, race instrumentation, CGO enabled and Linux/amd64, with `GOMAXPROCS=2`,
`GOFLAGS=-p=2`, explicit `GOTOOLCHAIN=go1.26.8` and private 0700 workspace TMPDIR
`/home/ubuntu/jd-dff-t`. Compilation exits zero in 4.985s. Both engines use binary SHA256
`1874158aaf8b7a852573d214325f1c568919ae386c04e74fc745a73d9669a178`.

The [original wrapper](dns-service-domain-filters-2026-10-09/assembled-b703/domain-native-runner-final.py)
stops before a native spawn because the task account cannot dereference the production manager's
`/proc/883/exe`. The retained [transcript](dns-service-domain-filters-2026-10-09/assembled-b703/native-adguard-preflight-original.log)
and [result](dns-service-domain-filters-2026-10-09/assembled-b703/native-adguard-preflight-original.result.json)
explicitly identify copied tool-session output, wrapper exit one and no native test, task directory
or engine resource creation. This is a wrapper preflight failure, not an engine-test failure.
Original wrapper/compiler metadata remains unchanged. A separately reviewed
[corrected wrapper](dns-service-domain-filters-2026-10-09/assembled-b703/domain-native-runner-corrected.py)
uses readable, fixed PID 883/start time 452, `systemd-network` comm/name and all four UID/GID
fields equal to 998. Unreadable or mismatched identity refuses the run. Its
[linked receipt](dns-service-domain-filters-2026-10-09/assembled-b703/domain-native-runner-corrected.receipt.json)
binds the original compiler and the same binary without claiming another compilation.

| Actual engine | Wrapper / test child | Final native records |
| --- | --- | --- |
| AdGuard Home `v0.107.71` | 70.471s / 69.44s | [log](dns-service-domain-filters-2026-10-09/assembled-b703/native-adguard-final.log), [source](dns-service-domain-filters-2026-10-09/assembled-b703/native-adguard-final.source-binary.json), [result](dns-service-domain-filters-2026-10-09/assembled-b703/native-adguard-final.result.json) |
| Pi-hole FTL `v6.7.1` | 91.545s / 90.49s | [log](dns-service-domain-filters-2026-10-09/assembled-b703/native-pihole-final.log), [source](dns-service-domain-filters-2026-10-09/assembled-b703/native-pihole-final.source-binary.json), [result](dns-service-domain-filters-2026-10-09/assembled-b703/native-pihole-final.result.json) |

Each serial run exits zero without skips under the same `240s` wrapper, five-second kill grace
and `235s` Go-test limit shown above. The fixture retains its own 180-second native context.
AdGuard verifies allow/deny suffix configuration; Pi-hole verifies exact allow `[0]` and deny `[]`
memberships. Both verify add/removal, duplicate and consumed refusal, selected-current freshness,
unselected metadata preservation and restart persistence, then restore the original seeded
rule inventory fingerprint. There are 16 AdGuard and 20 Pi-hole selected-current reads across the
fixture, including eight custom-domain reads per engine. Existing override, client-membership,
UDP/TCP local-answer and interrupted-provision cleanup checks keep their original scope.

Before each actual run, the wrapper checks clean source, all Go/module hashes, compiler receipt,
binary and wrapper identities. Every terminal record reports zero owned test processes, Docker
containers/networks/volumes and task TMP entries, with the task directory removed and the backend
source unchanged. The Docker CLI endpoint was independently checked to match the fixture's local
Unix socket before release. Host resolver digest remains
`9b5b6ef96476a3c090e8e5d1b98ae55802c227581143800dcde7c075f0ff5a02`; networkd PID/start/credentials
remain fixed, and host NetworkManager paths remain absent. The native lane was released only after
both terminal cleanup checks. Cached images require no pulls or host resolver/manager changes.

This assembled backend proof does not replace the isolated records or establish subscription
downloads, loaded rule content, effective client filtering, precedence, Technitium domain controls,
timers or reboot recovery. Protection stays disabled in the fixture. The matching production
[build/browser/capture acceptance](../../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-domain-filter-ui-acceptance.md)
is a separate record; broader P17 acceptance remains open.
