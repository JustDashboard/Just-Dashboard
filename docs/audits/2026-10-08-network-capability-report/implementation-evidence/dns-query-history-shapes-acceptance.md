# Native query-history decoding acceptance

The adapter now reads AdGuard's reported question `name`. The former `host` mapping returned an
empty domain for the pinned native writer, and the shared test fixture repeated that incorrect
field. Every engine now validates a complete bounded query collection before exposing any row;
malformed or missing native values remain unknown instead of becoming fabricated empty strings or
an epoch timestamp. Valid explicit empty text, numeric zero, FTL nullable status and Technitium's
paired absent-question nulls retain their native meaning. The DTO, routes and frontend are unchanged.
The [owning contract](../../../internal/backend/network-dns-services.md#native-query-history) cites
the pinned response writer/schema sources and states the private history and retention boundaries.

The clean implementation source is `4ce967f1f4c8e604ab2f39ecfe02fc4e756a6be3`, descended from the
previous PR checkpoint `d427809d81492945396bd5386395dec4010b7916`. The compiler receipt includes
all 1,865 backend Go files and both module files. Its Go 1.26.8 race binary is
`b262c01738792ca6c964056a2f3b578c5ed394a44d1db6f1ed8f5a6375dd592c`.

The four new query tests cover native field mapping, complete/empty/oversized collections,
missing/null/wrong-type fields, mixed valid/invalid rows, zero versus absent timestamps, native
nullable fields, ignored unknown fields and every display field's redaction/bounds. Authenticated
HTTP fixtures establish the closed native paths and page limits for all three engines, preserve
independently configured policy/fingerprints when history is unknown, and check actual retained
review JSON excludes private query rows. The existing AdGuard fixture now supplies `name` and
asserts its returned domain and protocol.

The first focused invocation failed because its oversized-history assertion expected an error
from `Service.Inspect`; this API instead returns an unavailable `View`. Only that test assertion
changed before the final focused pass. That initial output exists in the tool record rather than
an archived raw file; no raw hash is claimed for it. The product decoder was unchanged.

Final focused, required changed-file, query/privacy/transport race and private API race results
are recorded separately below. The changed-file gate runs against `d427809d`, and includes backend
build, selected vet and the reachable DNS package tests. No new frontend build or browser outcome
is claimed: this incremental change has no frontend source, DTO, fixture, package or lock change.

The three actual pinned-engine runs use the same compiled race binary and the reviewed
[serial runner](dns-query-history-shapes/native-runner.py). It pins both CLI inventory and the
fixture to `unix:///var/run/docker.sock`, verifies the socket receipt, and requires terminal
source/module/binary/commit/runner equality. Each invocation selects only the existing
`TestDNSServiceNativeOwnedEngine`, uses cached images and a private temporary directory, and
retains exact configuration/restart/no-replay/UDP-TCP/owned cleanup evidence. Original child and
wrapper bounds remain 235 seconds and 240 seconds plus a five-second kill interval.

Native configuration and owned lifecycle proof remains distinct from the controlled malformed-row
fixtures and from actual client filtering decisions. The existing fixture keeps protection
disabled, and the Technitium engine has no installed query logger; an actual installed Technitium
logger's rows and broader query/filter/zone/view/client/DHCP coverage remain open. All 187 ledger
IDs and their existing statuses remain unchanged.

| Check | Wrapper / test time | Evidence |
| --- | --- | --- |
| Focused query tests | 3.204s / 0.769s | [raw](dns-query-history-shapes/focused.log.raw.gz), [result](dns-query-history-shapes/focused.result.json) |
| Required changed-file gate | 70.061s / DNS 29.461s | [raw](dns-query-history-shapes/required.log.raw.gz), [result](dns-query-history-shapes/required.result.json) |
| Seven selected query/privacy/transport races | 13.688s / no skips | [raw](dns-query-history-shapes/race.log.raw.gz), [result](dns-query-history-shapes/race.result.json) |
| Private DNS API capability/sealing/audit race | 81.444s / 4.350s | [raw](dns-query-history-shapes/api-race.log.raw.gz), [result](dns-query-history-shapes/api-race.result.json) |
| AdGuard Home v0.107.71 | 69.079s / 68.04s | [raw](dns-query-history-shapes/native-adguard.log.raw.gz), [result](dns-query-history-shapes/native-adguard.result.json) |
| Pi-hole FTL v6.7.1 | 91.024s / 90.00s | [raw](dns-query-history-shapes/native-pihole.log.raw.gz), [result](dns-query-history-shapes/native-pihole.result.json) |
| Technitium 15.6 | 30.340s / 29.30s | [raw](dns-query-history-shapes/native-technitium.log.raw.gz), [result](dns-query-history-shapes/native-technitium.result.json) |

All eight copied logs retain their original bytes in both display and deterministic gzip form;
[copy validation](dns-query-history-shapes/log-copy-validation.json) records every digest and roundtrip.
The [compiler receipt](dns-query-history-shapes/compile.result.json) includes full source and compiler
metadata. Each native [source/binary manifest](dns-query-history-shapes/native-adguard.source-binary.json)
records its exact argv, environment, owned Docker inventory and host baseline; the parallel
[Pi-hole](dns-query-history-shapes/native-pihole.source-binary.json) and
[Technitium](dns-query-history-shapes/native-technitium.source-binary.json) manifests use the same source
and binary. Every native terminal receipt confirms zero remaining owned process/container/network/
volume/temp entries, unchanged host resolver/networkd/NM state, exact socket continuity and unchanged
frozen inputs. Source and runner received independent read-only review before actual dispatch.
