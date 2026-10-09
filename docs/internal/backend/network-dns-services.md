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
responses. Connection inventory and per-connection review history read only retained metadata;
history is bounded to the latest 64 rows with snapshots omitted. `GET /changes/{id}` reads one full
retained before/after review. These reads do not decrypt native credentials or contact an engine,
so a read-only or unavailable connection still has inspectable review history. Query entries are
removed before snapshots become reviewed or retained change state.

| Method and relative route | Request/result |
| --- | --- |
| `GET /`, `POST /` | Metadata-only `Connection[]`; connect with `ConnectionRequest` returns an authenticated `View`. |
| `GET /{id}`, `PUT /{id}`, `DELETE /{id}` | Fresh `View`; full credential/config replacement on the same engine and origin; disconnect only foreign-connected metadata. |
| `GET /{id}/changes`, `POST /{id}/changes` | Latest 64 metadata-only `Change[]`; `ChangeRequest` creates a five-minute retained reviewed `Change`. |
| `GET /changes/{id}`, `POST /changes/{id}/apply` | Full retained `Change`; single-use native apply/readback returns the terminal `Change`. |
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
prefixes) and Technitium `zone_create` (a validated lower-case primary-zone DNS name). Arbitrary
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
and [Technitium's version 15.6 API](https://github.com/TechnitiumSoftware/DnsServer/blob/v15.6.0/APIDOCS.md).
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
It removes its exact owned resources and leaves the host resolver unchanged. A missing selected
image is a failed acceptance, not a successful skip. `JD_DNS_SERVICES_NATIVE_LOG_DIR` optionally
retains bounded owner-specific fixture logs with bootstrap credentials redacted.
