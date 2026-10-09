# Native DNS service connections and owned provisions

`internal/dnsservice` connects to AdGuard Home 0.107, Pi-hole FTL 6 and Technitium 15 through
closed, version-checked native API adapters. It is not a DNS server, generic HTTP proxy or plugin
installer. These private administrator routes inspect native configured listeners, client policy,
local overrides, authoritative zone inventory, supported installed split-horizon/filtering apps and
bounded native query history. Missing, unreadable or unsupported inventory remains explicit.

Connections default to dashboard read-only even when their native credential has administrator
privileges. Credentials and optional custom CA material are sealed with the install key; API
responses, reviewed changes and audit metadata omit them. Management origins require a unicast
literal IP and an explicit port. HTTP is allowed only on loopback. Other origins require verified
HTTPS with system/custom trust and an optional declared DNS server identity. Dialing stays pinned
to the literal endpoint; redirects and environment proxies are refused. Technitium tokens use
`Authorization: Bearer` on closed POST requests, never a URL parameter. FTL sessions use
`X-FTL-SID` and are explicitly logged out.

All routes below `/api/v1/network/dns/services` require `system.admin` and return private, no-store
responses. This capability applies to authenticated sessions and appropriately scoped administrator
API tokens; these routes do not require a human session. Connection inventory and per-connection review history read only retained metadata;
history is bounded to the latest 64 rows with snapshots omitted. `GET /changes/{id}` reads one full
retained before/after review. These reads do not decrypt native credentials or contact an engine,
so a read-only or unavailable connection still has inspectable review history. Query entries are
removed before snapshots become reviewed or retained change state.

| Method and relative route | Request/result |
| --- | --- |
| `GET /`, `POST /` | Metadata-only `Connection[]`; connect with `ConnectionRequest` returns an authenticated `View`. |
| `GET /{id}`, `PUT /{id}`, `DELETE /{id}` | Fresh `View`; full credential/config replacement on the same engine and origin; disconnect only foreign-connected metadata. |
| `GET /{id}/filters` | Fresh read-only `FilterView` of bounded native subscription/custom-rule metadata. Connection generation and owned-resource identity are rechecked after native reads. |
| `GET /{id}/zones/{zone}/records` | Technitium-only fresh `RecordInventory` for an explicit lower-case zone, including native version, owner/type/DNSSEC, nullable internal classification, at most 256 records, metadata fingerprints and per-record editability. Read-only connections may inspect it. |
| `GET /{id}/changes`, `POST /{id}/changes` | Latest 64 metadata-only `Change[]`; `ChangeRequest` creates a five-minute retained reviewed `Change`. |
| `GET /changes/{id}`, `POST /changes/{id}/apply` | Full retained `Change`; single-use native apply/readback returns the terminal `Change`. |
| `GET /changes/{id}/current` | Fresh `View` of the exact retained request and connection generation, including selected raw record/client/override/custom-domain-filter fingerprints. Changed ownership/generation or malformed intent is refused. This read never claims, mutates or replays the review. |
| `GET /provisions`, `POST /provisions` | Latest 64 `Provision[]`; `ProvisionRequest` creates a five-minute sealed resource review without creation. |
| `GET /provisions/{id}`, `POST /provisions/{id}/apply`, `DELETE /provisions/{id}` | Retained `Provision`; single-use owned creation/bootstrap; separately reviewed exact owned removal including both volumes. |

The Go models define the exact JSON fields. `ConnectionRequest` carries name, engine, literal origin,
optional TLS identity/custom CA, explicit management and one engine-specific credential. Connect and
provision management default false. PUT keeps engine/origin fixed and requires the complete native
credential again; it increments generation and refuses a connection with an applying review. Native
Technitium bootstrap tokens remain sealed and are never returned; management may be explicitly
selected in the provision review, or a separately created native token can be supplied on update.

`ChangeRequest` is closed to `protection` (explicit boolean), `upstreams` (1–16 literal classic DNS
address/port endpoints), AdGuard `access` (nonempty canonical allowed prefixes, optional denied
prefixes), Technitium `zone_create` (a validated lower-case primary-zone DNS name), and the bounded
record/client/custom-domain-filter actions below. Arbitrary
native paths, payloads, app installation and command source are not accepted. Read-only connections
refuse staging. Consumed/expired/generation-conflicting claims return conflict; a claimed operation
can return HTTP 200 with `refused` or `needs_review`, so the UI must inspect terminal state/error.
Successful verification is explicit `verified`, never inferred from the HTTP status alone. Before
and after remain configured/runtime provenance, separate from measured client reachability.

Reviewed native changes use a five-minute retained baseline, connection generation and a single-use
claim. Apply re-reads the inspected native policy before the closed mutation, then checks native
readback. Sequential native APIs provide no cross-call CAS or atomic snapshot. Drift refuses the
consumed plan; missing/mismatched readback leaves `needs_review`. The dashboard does not repeat an
uncertain native mutation or restore foreign policy. Native client filter groups, local rewrites
and authoritative zones remain distinct; configured DNSSEC status is not cryptographic proof.

## Read-only native filter metadata

`GET /{id}/filters` also works for dashboard read-only connections. It returns a `FilterView` with
connection metadata, optional `inventory`, and `available`, `partial`, `unavailable` or `unsupported`
state. It makes no native policy change, refresh request, subscription download or DNS question.
This read does not select a retained review or authorize a mutation.

`FilterInventory` separates `sources` and `rules` sections, each with configured/unknown/unsupported
`evidence`, intentionally redacted `identity`, `entries` and an optional native metadata fingerprint.
Missing, null where unsupported, malformed or oversized collections stay unknown; their empty output
array must never be read as a known empty policy. A complete supported collection can explicitly be
empty. Every entry retains a full fingerprint; native numerical IDs remain distinct even when
subscription origins are equal. Fingerprints cover selected original JSON metadata, including
unknown per-entry fields, not the redacted display identity. They are read-only comparison evidence,
not an apply authorization or an atomic native revision.

AdGuard reads block/allow subscriptions and custom rules from `filtering/status`; its protection
switch, filtering enable state and DNS-running status stay distinct. Pi-hole reads persistent lists
and exact/regex allow/deny domain-rule metadata, preserving native enable flags and explicit group
arrays, including `[]`. Each group element must be an actual bounded integer; null membership
elements keep their section unknown and never become group ID zero. Explicit numeric zero and an
empty array remain distinct. Native counts, update times and numeric list statuses are retained
only when reported; absent values remain unreported. Technitium reads built-in block/allow
subscriptions and comment entries from settings. Only its pinned 15.6/15.6.0 response writer's explicit `blockListUrls:
null` establishes no configured subscription entries; a missing field or another version's null
stays unknown. Its manual Allowed/Blocked zone trees and installed app rule contents remain
explicitly unsupported here, separate from existing app group inventory.
AdGuard 0.107.71's pinned writer likewise emits explicit null for its zero-length subscription
slices and a nil custom-rule slice. Only that exact version's present `filters`,
`whitelist_filters` and `user_rules` null fields establish zero entries; missing fields and other
versions' null remain unknown. The original native failure is retained separately from this narrow
[response-writer compatibility](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.71/internal/filtering/http.go).
Every custom-rule array element must be an actual JSON string. An explicit empty string is a
reported native entry; an element-level null keeps the rule section unknown and is never converted
to empty text. This differs from the pinned writer's collection-level null representation.

AdGuard and Pi-hole source/custom-rule collections are bounded to 256 entries. Technitium supports
255 native entries; its adapter bounds each identity to 255 bytes and preserves its update interval
range of 0–168 hours. Native subscription destinations must be nonempty strings even when their
display identity is redacted. Other native source/rule identities are at most 4096 bytes,
groups at most 64 unique nonnegative 32-bit IDs,
responses at most 512 KiB and the returned inventory at most 192 KiB. Reads share a 20-second
deadline and the existing pinned literal origin, verified TLS, scoped authentication and no-redirect
transport. Cancelled reads return no available inventory; replacement generations/ownership refuse
the old result. Native version refusal is explicit `unsupported`. Other section failures retain
unknown evidence without echoing native response bodies.

Only an HTTP(S) subscription's scheme and host can appear as `origin`. URL credentials, path, query
and fragment, local file paths, native names/comments and rule contents stay fingerprint-only.
Optional `runtime` evidence describes an authenticated native status, or remains unknown; neither
configured membership nor reported counts prove loaded/compiled rule content or a measured client
filtering decision. All reads remain `system.admin`, private/no-store; administrator API tokens have
the same existing capability contract as sessions.

The engine detail sheet mounts this inventory before its reviewed change controls, including on
dashboard read-only connections. The separate `Read native filters` command and thirty-second
foreground poll retain their own observation time. Network errors and native unavailable responses
keep the last same-owner entries with a retry warning; changed connection identity or generation
clears them. A fresh partial response replaces unreadable sections with explicit unknown evidence
without drawing a zero count or implying empty policy. The bounded frontend decoder refuses unknown
DTO fields, malformed origins, mixed engines and contradictory configured/fingerprint evidence;
optional false flags, zero counts and empty group memberships remain visible. Fingerprints identify
redacted rows; native status codes and update times carry their native basis without acquiring a
healthy or effective-filtering verdict.

The pinned metadata contracts are [AdGuard 0.107.71](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.71/openapi/openapi.yaml),
[FTL 6.7.1 lists](https://github.com/pi-hole/FTL/blob/v6.7.1/src/api/docs/content/specs/lists.yaml)
and [domain rules](https://github.com/pi-hole/FTL/blob/v6.7.1/src/api/docs/content/specs/domains.yaml),
plus Technitium's [15.6.0 settings writer](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.0/DnsServerCore/WebServiceSettingsApi.cs)
and [block-list manager](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.0/DnsServerCore/Dns/ZoneManagers/BlockListZoneManager.cs).
Arbitrary rule editing, full rule contents, subscription/manual/app filtering and wider P17 acceptance
remain open. The reviewed custom-domain controls below are a separate bounded mutation contract.
The [source-matched filter acceptance](evidence/dns-service-filters-2026-10-09.md) retains scoped
checks, all three actual engine passes, exact source/binary/raw hashes and owned cleanup, with the
original AdGuard failure separately attributed. It verifies empty subscriptions and AdGuard/Pi-hole
local-rule metadata persistence; it does not establish loaded subscription content or effective
client filtering.

The [assembled filter UI acceptance](../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-filter-ui-acceptance.md)
separately records its source-matched production build, strict decoder checks, reachable browser
cases and settled phone/desktop captures. Browser fixture metadata does not establish native
subscription loading or a client filtering decision.

## Reviewed custom-domain filters

`filter_add` and `filter_remove` take only a nested `filter` object with `domain`, `disposition`
(`allow` or `deny`) and an explicit engine-specific `match`. The domain must be a complete lower-case
DNS name without a trailing dot, wildcard, regular expression or native rule syntax. AdGuard uses
`match: "suffix"`: the adapter generates `||domain^` or `@@||domain^` and preserves every unselected
native rule, comment and blank-string entry in order. Pi-hole uses `match: "exact"` and its native
exact-domain allow/deny endpoints. These semantics are different; neither configured rule establishes
a measured client decision or precedence over other native policy.

Pi-hole additions require explicit `groups`, including `[]`, with at most 64 unique existing native
nonnegative 32-bit IDs. Disabled native groups are still existing membership targets. Removal takes
no replacement group field and requires one enabled, exact native rule with supported metadata.
AdGuard takes no groups. Missing/null IDs or enable fields and null/noninteger group-array entries
are refused. Native comments remain unchanged for every unselected Pi-hole row; new rows explicitly
use a null comment. Unsupported engines, duplicate/opposite-domain owners, modified AdGuard targets,
disabled selected Pi-hole rules and unknown selected-row fields require the native console.

The retained `Snapshot.selectedFilter` reports the selected domain, disposition, match, presence,
optional native enabled/groups, nullable comment and `commentReported`, owner/exact counts, native
inventory count, configured evidence and selected/unselected fingerprints. AdGuard has no native
per-rule enable, group or comment field; these remain unreported. Pi-hole comments are bounded and
credential-redacted for review. Original rule text, source URLs, other comments and raw native
configuration stay private and enter only comparison fingerprints. The full selection baseline
includes AdGuard source/settings/rule metadata or Pi-hole domain rows, sources and native groups,
as well as the ordinary connection policy. It is bounded to 256 rules/sources, 128 native groups,
4096 bytes per rule/domain/source identity and the existing 512 KiB native response limit.

Preview and `/changes/{id}/current` use the same closed, selection-aware native inspection. Apply
validates the retained intent, connection generation and five-minute baseline, reads the complete
selection again immediately before the one effect, and reads it after the response. Raw drift or
unreadable pre-effect metadata refuses the consumed review without sending a mutation. Post-effect
readback must match the selected intent and all unselected policy fingerprints. A failed/lost native
response or changed foreign policy leaves `needs_review`; no mutation is replayed and no foreign
policy is restored. Sequential native API reads are not an atomic revision or a native CAS.

The AdGuard replacement endpoint updates only the freshly compared custom-rule array and schedules
native rule recompilation; it does not fetch subscription URLs. Pi-hole adds/deletes one exact-domain
row, retaining every unselected row/group/source field. No subscription, regex/DSL, Technitium
manual/app policy or provisioning replay is exposed. Existing `system.admin`, private/no-store,
sealed literal-origin authentication, audit and destructive-apply boundaries apply unchanged.
The contracts are the pinned [AdGuard set-rules API](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.71/openapi/openapi.yaml),
its [replacement handler](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.71/internal/filtering/http.go),
[AdGuard DNS filter syntax](https://adguard-dns.io/kb/general/dns-filtering-syntax/) and
[FTL 6.7.1 exact-domain API](https://github.com/pi-hole/FTL/blob/v6.7.1/src/api/docs/content/specs/domains.yaml).
The detail sheet builds those closed engine-specific intents and shows selected native metadata
before/readback. Unknown Pi-hole group inventory holds additions. A configured missing selected ID
stays in the draft until explicitly removed; unknown inventory does not establish disappearance.
Ambiguous or other-target readback is labeled with owner/exact counts instead of claimed absence.
The [assembled browser/native acceptance](../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-domain-filter-ui-acceptance.md)
records its fresh build, complete reachable gate and exact matching backend bytes; broader P17
criteria remain open.
The [source-matched custom-domain acceptance](evidence/dns-service-domain-filters-2026-10-09.md)
records final scoped/race checks, both actual owned engine passes, full source/binary/raw hashes,
selected-current reads and unselected preservation, with the preparation failure kept separate.
Its native configuration/restart proof does not establish effective client filtering.

## Reviewed records and client groups

The optional `record` request is `{name, type, value, ttl?}`. `name` is a complete lower-case DNS
owner without a wildcard or trailing dot; `type` is exactly `A` or `AAAA`, and `value` is a canonical
unicast IP of that family. Mapped IPv6, unspecified, multicast and scoped addresses are refused.
The optional `client` request is `{address, groups}`. All action-specific fields are mutually
exclusive with other policy fields; arbitrary record types, native request bodies and unknown JSON
fields are refused.
Client membership requires an explicit array of actual integer IDs. Empty `[]` and numeric `0`
remain valid; collection/element null, wrong numeric types, duplicate or out-of-bound IDs are
refused before selection. Native client/group reads apply the same presence-aware contract:
unreadable memberships or null/duplicate/invalid group identities and enable fields stay unknown,
with no partial configured inventory or replacement baseline.
The detail sheet withholds client/group counts while their native inventory evidence is unknown.

| Action | Engine and reviewed scope |
| --- | --- |
| `override_add`, `override_remove` | AdGuard or Pi-hole exact name/address override, without `zone` or TTL. Add refuses an existing owner/alias; removal requires one exact simple entry and refuses ambiguous, multi-alias or disabled AdGuard entries. Pi-hole updates only `dns.hosts`, preserving other hosts lines and CNAME configuration. These remain local answer overrides. |
| `record_add`, `record_remove` | Technitium enabled supported unsigned `Primary` zone, explicit `zone`, contained owner and exact TTL 1–86400 seconds. Add refuses duplicates, incompatible RR-set TTL/metadata, aliases, APP records, more-specific zones and native delegations. Expiring/disabled/unsupported records cannot be removed. There is no overwrite, automatic PTR/reverse-zone creation or SVCB hint update. |
| `client_groups` | Pi-hole existing canonical literal-IP/prefix client, explicit 0–64 unique nonnegative native group IDs, including an empty list. Every selected group must already exist. Native client comment/null state is preserved; no client or filter group is created. Group assignment does not establish a measured filtering decision. |

`Snapshot.records` retains the selected `RecordInventory`. Its records have
`{name,type,value?,ttl,disabled,editable,comments?,fingerprint}`; unsupported native RR types remain
visible with `editable=false`. Inventory `evidence` is configured native authority, separate from
DNS packet answers, delegated publication and local overrides. `Snapshot.selectedClient` retains
`{address,groups,comment,commentFingerprint,otherPolicyFingerprint}`, with a redacted comment or
explicit null. Full native selection metadata enters `selectionFingerprint` and the overall policy
fingerprint before retention, then is re-read before apply and immediately before sending the
mutation. Changed zone/RR metadata, client comments or group configuration refuse the consumed review.
Sequential APIs still provide no compare-and-swap across that last read and the native write.
`GET /changes/{id}/current` supplies the same selection-aware fresh baseline as preview/apply;
ordinary connection inventory has no selected fingerprint and cannot replace it for these reviews.
The current read validates the closed retained request and original baseline again, and rechecks
connection identity after native reads. Native unavailability is an unavailable `View`; malformed
scope is a bad request and changed generation/ownership is a conflict. Valid expired or consumed
reviews remain inspectable without becoming applicable again. The single-use apply claim and expiry
checks remain authoritative.

The administrator engine sheet mounts these bounded record/client forms. Technitium records require
an explicit fresh zone inventory; unsupported native RR types remain visible and read-only. The
form can copy one eligible native A/AAAA row with its exact TTL for review. Local overrides have
neither a zone selector nor a caller-selected TTL. Pi-hole group assignment selects an existing
literal client and native group IDs; selecting none explicitly removes its memberships. Field
refusals keep the draft, focus a linked error summary and associate each error with its control.
The retained sheet shows the exact owner/type/value/zone/TTL or client/group request and selected
native before/readback policy. Its freshness poll uses `/changes/{id}/current`, including for earlier
policy actions; a failed read, replaced owner, changed selection or changed retained intent holds
apply even while confirmation is open. Attempt IDs and single-use server claims continue to prevent
replay after a lost response or reload.

AdGuard `LocalOverride.enabled` retains the optional native boolean; missing stays unreported.
Its pinned add/delete body explicitly selects an enabled entry. Enable-state drift refuses a retained
review, and unreadable or unknown rewrite policy fields prevent an incomplete mutation.

The actual pinned Technitium 15.6 writer omits the legacy `internal` field shown in its documentation
example. `RecordInventory.internal` is therefore explicit null when unreported, and `nativeVersion`
retains the authenticated version. The inspected 15.6/15.6.0 `Primary` class has no separate internal
zone category; that exact version/type contract permits its enabled unsigned record operations.
An explicitly true internal flag, or an absent flag on any other version, refuses editing. No missing
flag is converted to a false external-ownership claim. The [pinned response writer](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.0/DnsServerCore/WebServiceZonesApi.cs)
and [native zone types](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.0/DnsServerCore/Dns/Zones/AuthZoneInfo.cs)
define this narrow compatibility rule.

Successful readback requires the intended addition/removal/assignment and unchanged unselected
records, overrides or client/group policy. A native SOA serial and its last-modified time may advance
as the engine maintains an unsigned zone; the remaining SOA fields must stay equal. Query-only
`lastUsedOn` is excluded from record configuration fingerprints. Pi-hole's selected client
`date_modified` may advance, while its comment, other native properties, other clients and group
configuration must stay equal. Unavailable, mismatched or uncertain readback ends `needs_review`
without repeating the operation or restoring native foreign policy.

The backend controls passed focused policy/private API races and actual source-matched AdGuard,
Pi-hole and Technitium acceptance; the [raw results, commands, hashes and original failures](evidence/dns-service-policy-2026-10-09/)
retain this bounded proof. The matching retained UI and fresh integrated reachable acceptance are
recorded separately in the [assembled record/client acceptance](../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-service-policy-ui-acceptance.md),
including actual exact-selection current reads before/after every owned engine operation. AdGuard client-specific
settings, client/group creation, filter-list content, other RR types, signed/secondary/forwarder zone
edits, native APP/view mutation, encrypted listener management and clustering remain outside this
bounded request contract. P17's broader requirements remain open.

FTL upstream changes can acknowledge persisted configuration before asynchronously exiting for a
native restart. Apply requires its authenticated process/uptime identity before mutation and waits
within the sixty-second change budget for a later native boot, then checks configured policy.
Container PID namespaces can reuse the same PID, so PID equality alone is not restart evidence.
The retained startup interval includes request latency and native uptime precision; a delayed
response from the old process cannot stand in for a later boot.
No mutation is repeated and no foreign engine is restarted. A native restart that remains unavailable
ends `needs_review`; ordinary inventory keeps absent runtime identity explicitly unknown.

## Owned Docker resources and privileges

Provision preview requires an already cached, reviewed immutable official image; it never pulls
an image or accepts a caller-selected image. It seals an explicit native bootstrap password and
records the intended resource names. Apply creates one dedicated bridge, two named persistent
volumes and one engine container. Host management and classic UDP/TCP DNS ports bind only
`127.0.0.1` on explicit ports above 1023; the host resolver is unchanged. No host bind, host namespace,
device, DHCP capability or privileged container is used. Memory/CPU/PIDs and container logs are
bounded. A verified provision uses an `unless-stopped` restart policy and native persistent credentials.

All engines drop every default capability and add only `CHOWN`, `DAC_OVERRIDE`, `FOWNER`, `SETGID`,
`SETUID`, `NET_BIND_SERVICE` and `KILL` for native volume ownership, native account transitions,
port 53 and native process shutdown inside the owned container. Pi-hole additionally receives
`SETFCAP` and `SETPCAP`: its pinned entrypoint sets the FTL binary's restricted file capabilities,
then uses `capsh` to enter the native `pihole` account. `NET_ADMIN`, `NET_RAW`, `SYS_ADMIN`, `SYS_TIME`
and `SYS_NICE` are not granted. `no-new-privileges` stays enabled. See the
[pinned Pi-hole entrypoint](https://github.com/pi-hole/docker-pi-hole/blob/2026.09.0/src/bash_functions.sh)
and its [account transition](https://github.com/pi-hole/docker-pi-hole/blob/2026.09.0/src/start.sh).

AdGuard is seeded before the first listener with its pinned schema and a native bcrypt hash.
Pi-hole is seeded with native TOML upstream/listener configuration, an empty adlist file and a
private `WEBPASSWORD_FILE`. Environment-forced DNS settings would make FTL controls read-only, so
the reviewed upstream policy is stored in native configuration. Technitium uses its supported
first-boot password file and creates a persistent API token through a closed POST body. Plaintext
password files are mode 0600 in the container, absent from Docker create arguments/environment,
and removed after authenticated bootstrap. No default credential, filter subscription or app is
installed. Native maintenance/version-check traffic is native engine behavior, separate from the
dashboard's inspected query history and from image acquisition.

Creation phases and returned resource identities are durably journaled. Every start, cleanup and
removal checks exact install/provision labels, immutable image identity, bridge, named mounts,
loopback publication and resource/privilege bounds. Changed or foreign resources are refused.
Failure cleans only matching newly owned resources; uncertain cleanup stays `needs_review`.
Startup reconciles an interrupted setup/removal without replaying native bootstrap or starting a
new engine. A verified provision is retained. Removal requires a separate destructive review and
removes both owned data volumes; ordinary connection deletion cannot bypass it.

## Acceptance status

The first retained UI mounts administrator-only native connection/inventory, reviewed changes and
owned setup/removal sheets on the existing DNS reading page, independent of host resolver read
availability. It preserves the backend's closed action scope and native provenance. Fresh matching
policy and immutable review checks hold stale confirmations; account-scoped attempt IDs prevent
the UI from replaying an uncertain apply after navigation or reload. The matching production build,
3,232 fast logic tests, 13 mounted service interaction cases and phone/desktop inspection pass;
the [UI acceptance record](../../audits/2026-10-08-network-capability-report/implementation-evidence/dns-services-ui-acceptance.md)
separates those checks from the [passing combined reachable gate](../../audits/2026-10-08-network-capability-report/implementation-evidence/native-dns-integrated-acceptance.md).
Broader native zone/view/client policy editing remains follow-up work under P17.

P17 remains in progress: all three pinned engine fixtures, the retained UI slice and combined
reachable checks pass, while complete query/zone/view/client-policy coverage remains open.
Pure/HTTP fixtures do not establish real native-engine compatibility, and native container restart
is not a host reboot proof.

The 2026-10-09 final-source race binary passed actual AdGuard Home 0.107.71 (25.17 seconds),
Pi-hole FTL 6.7.1 (46.21 seconds) and Technitium 15.6 (16.69 seconds), without skips. The
[raw engine logs and source/binary hashes](evidence/dns-services-2026-10-09/) retain those separate
results. After each terminal run, Docker container/network/volume label inventories were empty and
the owned test child had ended. No host resolver, existing service or unrelated Docker resource was
changed. The fixture's dedicated high loopback management/DNS ports were withdrawn with its exact
resources.
The metadata-only retained history read was added after that frozen binary; its separate private
API and bounded storage tests cover it. It does not change the native engine lifecycle or mutation
implementation measured by those three runs.

The final core/API change passed `scripts/test-changed.sh 148e4f38`: Go build, vet and the selected
API (174.290 seconds), store (4.533 seconds) and DNS service (10.337 seconds) tests. Separate final
HTTP, private API, retained-history and FTL restart races passed (DNS services 13.678 seconds,
API 4.640 seconds). Their raw logs are retained in the same evidence directory. No frontend source
changed in that core handoff; the subsequently assembled UI and reachable gate passed as recorded
above. Broader P17 controls remain open.
These checks used `GOMAXPROCS=2`, `GOFLAGS='-p=2'` and a short, nonhidden workspace `TMPDIR` because
the system temporary filesystem was constrained. Earlier full API attempts are preserved separately
as `test-changed-core-final.log` and `test-changed-core-short-tmpdir.log` in the workspace artifacts:
the long temporary path exceeded Linux's Unix-socket path limit, and the hidden `.network-worktrees`
path made the database inventory fixture count its application file as tool state. The final temporary
root `/home/ubuntu/Just-Dashboard/p17-t.P45lLQ` avoided both existing path rules; no product behavior
was changed to bypass them.

Failed native attempts are retained separately in the same evidence directory. Pi-hole first lacked
its pinned entrypoint's additional account/file-capability requirements; later attempts exposed
asynchronous FTL exit after accepted upstream configuration. The final implementation verifies the
later FTL boot before successful upstream readback, with no repeated mutation. Technitium's initial
readiness attempt encountered its 646,662-byte console page, beyond the 512 KiB API limit; final
readiness uses a body-free HEAD request and leaves API response limits intact. These failures were
cleaned before corrected runs; they are not passing evidence.

Pinned primary contracts include [AdGuard's native API](https://github.com/AdguardTeam/AdGuardHome/blob/v0.107.71/openapi/openapi.yaml),
[FTL's configuration API](https://github.com/pi-hole/FTL/blob/v6.7.1/src/api/docs/content/specs/config.yaml),
[FTL's process PID/millisecond uptime implementation](https://github.com/pi-hole/FTL/blob/v6.7.1/src/api/info.c),
and [Technitium's version 15.6.0 API](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.0/APIDOCS.md).
Record mutations follow the pinned native add/get/delete contracts; Pi-hole client replacements
follow its [FTL 6.7.1 client contract](https://github.com/pi-hole/FTL/blob/v6.7.1/src/api/docs/content/specs/clients.yaml).
Installed native app configuration contracts are distinct from packet-level view selection.

The opt-in owned fixture requires a reachable Docker Engine and all three reviewed images already
cached. From `backend/`, compile
`GOMAXPROCS=2 go test -race -p 2 -c -o <workspace-artifact>/dns-services.test ./internal/dnsservice`, then run that binary once per engine with
`JD_DNS_SERVICES_LIVE_ENGINE=adguard`, `pihole` or `technitium`, and
`-test.run '^TestDNSServiceNativeOwnedEngine$' -test.count=1 -test.v`. Use a workspace `TMPDIR` when
the system temporary filesystem is constrained. The fixture creates only labeled dedicated bridges,
two-volume containers and explicit high loopback DNS/management ports. It checks native auth,
read-only defaults, reviewed changes/readback, real UDP/TCP local or authoritative answers, retained
credentials/config after container restart, owned removal and cold interrupted predecessor cleanup.
Filter inventory changes also check known empty native subscriptions, AdGuard/Pi-hole local custom
rule metadata and stable section fingerprints after restart. Subscription URLs are never added or
fetched for this proof; unsupported Technitium manual/app rules remain explicit.
Custom-domain changes additionally review allow and deny rules with engine-specific match semantics,
exact-selection current reads, duplicate/replay refusal, explicit Pi-hole group membership including
`[]`, restart persistence and removals that restore the original unselected rule fingerprints.
AdGuard comments and blank entries and Pi-hole unselected comments/groups remain preserved. This is
native configuration/readback proof; effective client filtering and native policy precedence remain
unmeasured.
It removes its exact owned resources and leaves the host resolver unchanged. A missing selected
image is a failed acceptance, not a successful skip. `JD_DNS_SERVICES_NATIVE_LOG_DIR` optionally
retains bounded owner-specific fixture logs with bootstrap credentials redacted.
