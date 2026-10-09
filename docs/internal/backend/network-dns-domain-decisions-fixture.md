# Preparing measured native custom-domain decisions

`TestDNSServiceNativeDomainDecisions` is a separate test-only, explicitly opted-in fixture prepared
from pushed source `d427809d81492945396bd5386395dec4010b7916`. The existing
`TestDNSServiceNativeOwnedEngine` and its disabled-protection configuration proof are unchanged.
This preparation is not an actual engine pass. The final native source must first include the
separately reviewed native query-history shape/name correction and freeze its complete backend,
modules, static helper, race binary and wrapper. No engine, helper image, container, VM or timer is
started by ordinary package checks. Broader P17 and the capability ledger remain open.

## Owned source and transport

The fixture requires one of the exact cached AdGuard Home 0.107.71 or Pi-hole FTL 6.7.1 images;
there is no pull. A separately compiled Go 1.26.8, `CGO_ENABLED=0`, Linux/amd64 static helper is read
through one bounded `O_NOFOLLOW` file descriptor. Its task-account owner, one link, unchanged file
receipt, exact reviewed SHA256 and ELF without dynamic loader are required. The eventual reviewed
wrapper supplies `JD_DNS_DECISION_HELPER` and `JD_DNS_DECISION_HELPER_SHA256`.

Only during a separately released live run, a bounded local Docker API `ImageImport` supplies an
uncompressed rootfs tar through `fromSrc=-`. It contains exactly one regular `dns-fixture` entry,
mode 0555, UID/GID zero, with the frozen helper's exact size and digest; there is no Dockerfile,
link, directory, build step, subscription, remote URL, registry credential or parent pull. Fixed
`Changes` set Linux/amd64, user/group 65534, `/` working directory, literal executable/nonce argv
and exactly the nonce/helper-digest labels. The closed one-row response must report a full SHA256
image ID; it must equal the inspected unique tag's ID. That image must have no parent, exactly the
rootfs tar's SHA256 layer and the closed expected configuration. A malformed/duplicate/error reply
or rebound tag acquires no run or cleanup authority. The sidecar has a read-only root filesystem, user/group
65534, all capabilities dropped, no new privileges, 64 MiB memory/swap, 0.25 CPU, 16 PIDs and bounded
logs. It has no published port, host mount, volume or extra network. Its exact image/container,
argv, labels, limits, network ID and observed IPv4/MAC are checked before every query and cleanup.
Before questions or reviewed effects, the production container identity guard also rechecks the
engine and the bridge must retain exactly the captured engine and sidecar endpoint IP/MAC pairs.

The production owned bridge is an ordinary Docker bridge; it is **not** `Internal: true`. This
fixture makes no blanket egress-containment claim. The helper never forwards, accepts only eight
nonce-bound `.invalid` names and A/AAAA questions, and returns fixed documentation addresses with
TTL zero. Reviewed upstream configuration points only to its literal bridge IPv4 and port 5353.
Client questions run through bounded Docker exec from that same inspected sidecar IPv4/MAC to the
literal engine IPv4 on port 53. IPv4 client transport carrying AAAA questions does not establish
IPv6 client transport. Every response must match transaction ID, question, class, owner and type,
be untruncated NOERROR and contain exactly the known positive or explicit null-IP answer. Timeout,
SERVFAIL, NXDOMAIN, empty answers, aliases and foreign values are failures.

## Causality and preserved policy

The fixture seeds one literal unselected parent denial. AdGuard receives an ordered custom-rule
array with a fixed comment, an intentional empty string and `||seed-<nonce>.invalid^`. Pi-hole
receives the fixed regex `(^|[.])seed-<nonce>[.]invalid$`, enabled with group `[0]` and a comment,
plus a foreign exact client with its own preserved comment and `[0]` membership. This private seed
is fixture setup, not a public regex or arbitrary-rule control. No subscription is configured.

Complete persistent-client and enabled default-group inventories must prove the selected source
inherits global AdGuard policy or unmatched Pi-hole group zero. Native local rewrites/hosts/CNAMEs
must be explicitly empty; AdGuard filtering must be enabled. Actual native null-IP blocking mode
and the reviewed protection switch are checked, rather than inferred from configured rule counts.

| Controlled name | Before reviewed edits | After additions | After removals |
| --- | --- | --- | --- |
| Allow target under the seeded parent | Denied | Known positive upstream answer | Denied again |
| Child of allow target | Denied | AdGuard positive; Pi-hole denied | Denied |
| Independent deny target | Positive | Denied | Positive again |
| Child of deny target | Positive | AdGuard denied; Pi-hole positive | Positive |
| Pi-hole deny with explicit `[]` | Positive | Positive for default client group zero | Positive |
| Neutral and suffix-lookalike names | Positive | Positive | Positive |
| Unselected parent | Denied | Denied | Denied |

The same complete matrix also runs with protection disabled before the baseline, when all eight
names must resolve to the helper's positive values, and after actual engine restart without any
policy reapply. Every name is checked for A/AAAA over UDP/TCP. Additions/removals use retained
five-minute reviewed requests, fresh exact-selected `/current` evidence before/after, native
readback and consumed/duplicate refusal. Unselected raw rule strings, order, comments, client/group
rows, sources and filter settings are compared at each effect; final removals must restore the
entire seeded inventory. No cache clear, manual recompilation, failed-effect replay or foreign
policy restoration is used to force a pass.

Native history corroborates the actual source and matched rule/list identity after the added and
restarted matrices. AdGuard's pinned writer emits `question.name`, `client_proto` and matched rule
text/list ID; Pi-hole reports client IP, type, status and selected domainlist ID. Documented nullable
Pi-hole status/list IDs stay unreported and do not become a positive match. Each added/restarted
matrix captures actual local Go time immediately before its first questions. AdGuard's RFC3339Nano
row time and Pi-hole's finite nonnegative epoch time must be at or after that boundary and no later
than the actual completed native read. Missing, null, malformed or future times refuse complete
corroboration; old rows with the same selected native IDs/status cannot establish a new phase.
Both clocks use the same local daemon kernel in this fixture. Pi-hole history has no transport field: UDP/TCP evidence
comes from the wire helper, independently. Only controlled scalar evidence is logged; native
credentials, unrelated queries and raw response bodies are not printed.

## Fixed bounds and failure ownership

The static client-question ceiling is exactly **256**: five matrices × eight names × two types ×
two transports = 160, plus six settling phases × eight rounds × two sentinels = 96. Each settling
phase is also at most 20 seconds and requires two consecutive matching observed decisions. Rate is
at most eight questions per second; exchanges last at most 900 ms and DNS frames are at most 4 KiB.
Exhausting a count/deadline is failure, not permission to retry a mutation. The helper independently
bounds its served upstream requests to 256, simultaneous TCP connections to eight and lifetime to
180 seconds. Native query-history reads use at most 100 rows and eight read attempts per evidence
phase. The whole fixture has a 180-second native context and separate 40-second cleanup context.
The future reviewed runner must retain the existing 235-second Go / 240-second wrapper limits.

Cleanup is registered before any image/container creation. It first verifies/stops/removes the exact
sidecar, then removes only its captured image ID with labels/tag still matching. It does not prune
other image parents or build caches. Unknown import outcomes or changed receipts remain failures
requiring technical review; the fixture grants no cleanup authority over an unrecorded image.
Only after sidecar cleanup succeeds does ordinary owned provision removal touch the engine, two
volumes and bridge. The existing `Destroy` extra-endpoint refusal is preserved. All helper listeners,
accepted connections, exec capture FDs, owned resources and task TMP entries must be absent before
release. The eventual source-matched wrapper must independently retain host resolver, networkd and
NetworkManager witnesses, full source/binary/argv/raw hashes and exact terminal cleanup. No native
decision acceptance is claimed until both serial actual runs pass on the assembled frozen source.

## Preparation checks and preserved draft failures

Normal preparation checks keep `JD_DNS_DOMAIN_DECISIONS_LIVE=0`. From `backend/`, the focused
selection is `go test ./internal/dnsservice ./internal/dnsservice/testdata/native-dns-decisions
-run 'Test(Finite|MalformedQuestions|FramesAnd|DNSDomainDecision|DNSServiceNativeDomainDecisions)'
-count=1 -timeout=60s`, with Go 1.26.8, `GOMAXPROCS=2`, `GOFLAGS=-p=2` and a short owned workspace
`TMPDIR`. The corresponding focused race selection and `scripts/test-changed.sh d427809d` must pass
before freezing the final fixture. Compile the helper with `CGO_ENABLED=0`, Linux/amd64 and ordinary
executable mode; compile the separate DNS-service race binary from that same clean final checkout.
Compilation is preparation and does not opt into either native fixture.

The original preparation logs remain in
`/home/ubuntu/Just-Dashboard-network-dns-domain-decisions-artifacts`. These include compiler-only
failures for Docker's `Os` field and its image-versus-container configuration types, and the mock
receipt failure that exposed named `Entrypoint`/`Cmd` slices compared without conversion. Those
draft failures started no native resource. Exact argv comparison remains enforced after converting
the native named slices. The corrected ownership selection passed with DNS-service child 0.047 s
and packet-helper child 0.004 s (`decision-ownership-final-preassembly.log`). NativeMan's independent
read-only review also found the mutable-tag adoption gap. The legacy builder preparation then bound
its full returned ID and passed its checks, but root's review identified the unavoidable intermediate
parent from its appended metadata step. No legacy image build or native decision run occurred.
That complete `b307456d`/`60c02142` source, helper/race binaries, wrappers and receipts remain separately
attributed preparation. The replacement local import creates one parentless image and tests the
sole rootfs entry, exact local request, full result ID, rebound tags and foreign-image cleanup
refusal. These records establish
preparation only; final assembled-source checks and native results need their own attribution.
The single-import `9474c056` preparation also passed its required/focused-race checks and froze
separate helper/race/runner receipts without any invocation. Root's subsequent review identified
that post-restart history could reuse old matched rows with the same rule IDs. The separate phase
correction requires real before-matrix/read boundaries and tests old same-ID rows, current rows,
nullable/missing/malformed/negative/overflow/future times and explicit old epoch zero. The corrected
selected-client log describes absence of a selected override; the preserved foreign Pi-hole client
remains present. Historical preparation records are retained, without relabeling any native pass.

The pinned primary contracts are [AdGuard query-log JSON](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.71/internal/querylog/json.go),
[filter application and precedence](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.71/internal/filtering/filtering.go),
[null-IP response construction](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.71/internal/dnsforward/msg.go),
[FTL default client groups](https://github.com/pi-hole/FTL/blob/v6.7.1/src/database/gravity-db.c),
[allow-before-deny processing](https://github.com/pi-hole/FTL/blob/v6.7.1/src/dnsmasq_interface.c),
and [bounded native query metadata](https://github.com/pi-hole/FTL/blob/v6.7.1/src/api/docs/content/specs/queries.yaml).
The timestamp fields are written by the same pinned
[AdGuard entry writer](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.71/internal/querylog/json.go#L58)
and [FTL query writer](https://github.com/pi-hole/FTL/blob/v6.7.1/src/api/queries.c#L961).
The exact already-used Docker SDK is Moby v28.5.2: its [import client](https://github.com/moby/moby/blob/v28.5.2/client/image_import.go)
sends the local reader and fixed changes, the [HTTP route](https://github.com/moby/moby/blob/v28.5.2/api/server/router/image/image_routes.go)
uses the request body for `fromSrc=-` and emits the returned full ID, and the
[classic image store](https://github.com/moby/moby/blob/v28.5.2/daemon/images/image_import.go) and
[containerd image store](https://github.com/moby/moby/blob/v28.5.2/daemon/containerd/image_import.go)
each create one parentless image with one imported layer. This is a source-level packaging argument;
the wrapper must still prove the complete image-ID inventory is unchanged after actual cleanup.
These intended fixed decisions do not establish arbitrary precedence, subscribed rule content,
Technitium effects, timer/reboot recovery or full product acceptance.
