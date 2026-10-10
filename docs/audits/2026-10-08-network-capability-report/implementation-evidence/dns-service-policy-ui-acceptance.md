# Reviewed DNS record and client policy acceptance

This P17 checkpoint mounts exact A/AAAA local override add/remove, explicit supported unsigned
Technitium Primary-zone record add/remove with TTL, and existing Pi-hole client group assignment.
An empty group list explicitly removes memberships. Unsupported native RR types remain visible
without edit controls. The broader query/filter/view/client/DHCP product criteria remain open.

## Final application and source attribution

The application checkpoint is `777d04012b8a9f58efe3ff43e56a410266b045dd`; the final test-only
correction is `1fe294f9432e7ecd4feecb871f190f7ad6bae292`. The
[production build](dns-service-policy-ui/build.log) is from `9d863a9f`, serving only
`127.0.0.1:43147`. All frontend application source is unchanged between that build and final source;
subsequent commits add browser assertions only. The
[final source validation](dns-service-policy-ui/final-source-validation.json) captures all 1,533
tracked frontend source/package/lockfile hashes and verifies all 22 native DNS Go/module hashes.

The native fixture addition is `cfad4f55`. Its race binary SHA256 is
`0fee207c0d7689b9acde9048abb45961d1116862a273537c59e3aef7aa7ec86b`.
The [original freeze manifest](dns-service-policy-ui/source-binary.json) retains its earlier
`2ebb5e02` head and `588332e3` frontend provenance; those fields are not relabeled as the later build.
Every native source hash matches the final source, as independently reviewed after all terminal runs.
The earlier [backend-only policy proofs](../../../internal/backend/evidence/dns-service-policy-2026-10-09/)
remain separately attributed to their original source and failures.

Checked-in text copies remove only terminal carriage returns and trailing line/EOF whitespace;
original workspace logs remain unchanged. The [copy-normalization hashes](dns-service-policy-ui/log-copy-normalization.json)
identify both versions. All three native logs are unchanged byte-for-byte.

## Actual pinned engine acceptance

The same frozen race binary passed all three actual engines without a skip:

| Engine | Child / wrapper seconds | Exact-selection current reads | Raw result |
| --- | --- | --- | --- |
| AdGuard Home v0.107.71 | 38.48 / 39.50 | 8 | [AdGuard](dns-service-policy-ui/native-adguard-assembled.log) |
| Pi-hole FTL v6.7.1 | 63.64 / 64.67 | 12 | [Pi-hole](dns-service-policy-ui/native-pihole-assembled.log) |
| Technitium 15.6 | 11.69 / 12.71 | 8 | [Technitium](dns-service-policy-ui/native-technitium-assembled.log) |

Each engine proves A/AAAA additions and exact removals, real UDP/TCP answers, policy and sealed
credentials after native restart, single-use effects, exact owned removal and cold interrupted
predecessor cleanup. Technitium answers also require AA. Pi-hole assigns an existing fixture
client to no groups and back to native group 0 while preserving its nonempty native comment.
Native null/empty comment presentation is separate browser-mock evidence below.

Before and after every reviewed record/client operation, actual `CurrentChange` compares the exact
overall and selected policy fingerprints with retained before/readback, and verifies retained state
and before/readback fingerprints remain unchanged. The pre-read does not consume apply; the post-read does not permit
replay. These are actual native service reads, separate from private HTTP capability tests and
browser mocks. Sequential native APIs still provide no CAS between the last read and mutation.

The [serial run results](dns-service-policy-ui/results.json) retain each exact owner identity and
independent empty container/network/volume label inventories, all exit 0. The
[final independent cleanup](dns-service-policy-ui/independent-cleanup.json) finds no owned binary
process or native test temporary directory. Host networkd remains PID 883 and host NetworkManager
state directories remain absent. No image was pulled or host resolver changed.

The binary was compiled from `backend/` with:

```bash
TMPDIR=/home/ubuntu/jd-d17-t GOMAXPROCS=2 go test -race -p 2 -c \
  -o /home/ubuntu/jd-network-validation-tmp/dns-policy-assembled-native/dns-service-policy.test \
  ./internal/dnsservice
```

Each engine ran serially with `JD_DNS_SERVICES_LIVE_ENGINE=adguard`, `pihole`, or `technitium`,
`JD_DNS_SERVICES_NATIVE_LOG_DIR` pointing at its owned workspace artifacts, and
`-test.run '^TestDNSServiceNativeOwnedEngine$' -test.count=1 -test.timeout=230s -test.v`, under an
outer 240-second timeout. Raw engine logs retain immutable image digests and wrapper timings.

## Reachable frontend checks

The [initial integrated required gate](dns-service-policy-ui/required-first.log), against
`fbc64b4f46b615e58e3b92a8a38cd342e0fd3b27`, selected 171 cases in the design-system, network-audit,
DNS evidence, DNS service and DNS specs. It exited 1 in 540.44 seconds: 156 passed, seven failed and
eight optional `JD_NETWORK_SHOTS` captures were skipped. Formatting, lint, TypeScript, all 3,245 Bun
tests / 14,253 assertions, Go build/vet and selected API (0.466 seconds) / DNS (17.059 seconds)
tests passed. The seven failures queried background sheet roles while the confirmation modal made
that sheet `aria-hidden`; their original disabled/refusal/metadata assertions remain unchanged.

The [final scoped required gate](dns-service-policy-ui/required-final.log), against the already
checked `777d0401` application checkpoint, covers the test-only role-query correction and subsequent
documentation. It exited 0 in 135.33 seconds: static checks, all 3,245 logic tests and all 45 DNS
service browser cases passed, with no skip. The exact sheet Apply button and section heading queries
now use `includeHidden: true`; visible-state assertions still require visibility. Application source
and the production build are unchanged across these two gates. Together they cover all 163
executable selected cases successfully; the eight optional captures remain unverified. This is
source-matched combined evidence, not a claim that the original integrated command exited 0.

The 45 DNS service cases preserve the earlier 13 interactions and exercise record/client requests,
before/readback details, exact TTL, unsupported/internal/signed zones, selected raw drift and failures,
generation replacement, immutable held confirmations, expiry, draft retention and reload without
replay. Field error summaries focus, link to the invalid control and supply its accessible error
description. A successful client/group refresh preserves edited choices and refuses disappeared
selections. Native null and empty comments keep distinct labels and their original fingerprints.

The record form/review/readback is captured at 390, 1280 and 1720 pixels; existing client groups and
their before/readback are captured at 390 and 1280 pixels. These captures and width assertions are
mocked browser evidence, separate from actual engine effects and client DNS answers.
All 15 final captures and their hashes are retained in the [capture manifest](dns-service-policy-ui/screenshots.json).
Inspected examples include the [phone record inventory](dns-service-policy-ui/dns-record-inventory-390.png),
[desktop record review](dns-service-policy-ui/dns-record-review-1720.png),
[phone client review](dns-service-policy-ui/dns-client-review-390.png) and
[desktop client readback](dns-service-policy-ui/dns-client-readback-1280.png). Every tested document
and sheet width assertion passed; the sheet uses vertical scrolling for longer native inventories.

## Original failures and remaining scope

The [initial browser run](dns-service-policy-ui/browser-first.log) exited 1: 30 passed and 10 failed.
Nine failures exposed a real first-read ordering bug: metadata-only history enabled the current
policy read before the full retained baseline arrived. The final application waits for that
baseline and preserves the strict current-read parser. One invalid-field assertion matched both
the error summary and inline error; it now names the summary links while retaining field checks.
The [original artifact manifest](dns-service-policy-ui/browser-first-artifact-manifest.json) hashes
all 40 preserved artifacts, including the last-run metadata; originals remain in workspace artifacts.
An earlier preliminary gate was explicitly interrupted before the final comment-label correction.
Its raw output records signal 2 and a timing-wrapper exit 0; the
[external controlled-session result](dns-service-policy-ui/required-interruption.json) records exit
130. Neither result represents a completed passing gate.
The later integrated role-query failures also keep their
[original trace/context/capture hashes](dns-service-policy-ui/required-first-artifact-manifest.json),
with originals under the workspace artifact directory. Neither failure generation is overwritten
or counted as a passing run.

This bounded checkpoint does not establish arbitrary RR/zone/view changes, client/group creation,
AdGuard per-client policy, filter-list editing, encrypted listener management, delegated publication,
measured client filtering, clustering, broader native-owner lifecycle or host reboot. P17 and the
complete 187-requirement ledger remain open.
