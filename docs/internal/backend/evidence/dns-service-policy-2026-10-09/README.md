# Reviewed DNS records and client groups, 2026-10-09

This backend/API slice adds reviewed A/AAAA local overrides on AdGuard and Pi-hole, supported
unsigned Primary-zone A/AAAA records on Technitium, existing Pi-hole client group assignment, and
private authoritative record inventory. The matching typed UI and fresh integrated reachable checks
remain required. Broader P17 is open; these results do not establish arbitrary zone/view/client
editing, remote publication, a measured filtering decision or host reboot acceptance.

The final race binary is `0b258ada8afc8c9b9f447e655f508a623b9d3829a14ce84ffa76d80a5dacc7eb`.
`native-source-final.sha256` captures its 22 Go/module files, and `native-binary-final.sha256` captures
the binary. All source hashes matched after the terminal runs. Each final run passed without a skip:

| Engine and actual version | Child / wrapper seconds | Raw result |
| --- | --- | --- |
| AdGuard Home v0.107.71 | 30.27 / 31.30 | `native-adguard-final.log` |
| Pi-hole FTL v6.7.1 | 57.21 / 58.23 | `native-pihole-final.log` |
| Technitium 15.6 | 12.12 / 13.15 | `native-technitium-final.log` |

Each actual engine verifies reviewed A and AAAA additions, exact UDP/TCP answers, persisted policy
and sealed credentials after container restart, reviewed exact removals, single-use apply, owned
removal and cold interrupted-predecessor cleanup without replay. Technitium answers must also carry
AA. Pi-hole additionally assigns an existing fixture client's groups to an explicit empty list and
back to its existing default group while preserving its native comment. The native seed of that
existing client is test setup, not a dashboard client-creation capability.

From `/home/ubuntu/Just-Dashboard/.network-worktrees/dns-service-policy/backend`, compile:

```bash
env TMPDIR=/home/ubuntu/Just-Dashboard/d17-t.TeRksV GOMAXPROCS=2 \
  go test -race -p 2 -c \
  -o /home/ubuntu/Just-Dashboard/.network-worktrees/dns-service-policy-artifacts/dns-service-policy.test \
  ./internal/dnsservice
```

Run serially, substituting `adguard`, `pihole` or `technitium` for the selected engine:

```bash
timeout 240s env TMPDIR=/home/ubuntu/Just-Dashboard/d17-t.TeRksV GOMAXPROCS=2 \
  JD_DNS_SERVICES_LIVE_ENGINE=adguard \
  JD_DNS_SERVICES_NATIVE_LOG_DIR=/home/ubuntu/Just-Dashboard/.network-worktrees/dns-service-policy-artifacts/native-logs \
  /usr/bin/time -f 'wrapper_elapsed=%e exit=%x' \
  /home/ubuntu/Just-Dashboard/.network-worktrees/dns-service-policy-artifacts/dns-service-policy.test \
  -test.run '^TestDNSServiceNativeOwnedEngine$' -test.count=1 -test.timeout=230s -test.v
```

The cached immutable images are recorded verbatim in each raw passing result. No image was pulled.
After each terminal run, independent container/network/volume inventories filtered by the exact
`io.justdashboard.dns.owner=<owner>` label exited zero with empty output. `final-cleanup.txt` records
the three identities and results; the anchored native-test process search returned no match, the
task temporary directory was empty, and the source manifest matched. No host resolver, existing
service or unrelated Docker resource was changed. Resource publication used dedicated high loopback
ports and ended with exact owned removal.

Focused policy races passed in 61.819 seconds (`policy-race.log`), covering selected-policy drift,
foreign and unavailable readback, no replay, client comment/group preservation, closed inputs,
duplicate/conflicting records and the last pre-effect read. Subsequent contract-specific races
verify the pinned missing Technitium flag, unknown native policy fields and AdGuard's typed enable
state. The last correction's all-engine preservation/unknown-field/enable-state race passed in
19.610 seconds (`adguard-enabled-contract-race.log`). Private API races passed in 4.500 seconds
(`private-api-race.log`): administrator capability, private no-store responses, credential sealing,
closed native requests, retained review/readback, single-use effects and audit. An appropriately
scoped administrator API token is accepted; these service routes do not require a human session.

`test-changed-final.log` records the final required selected build/vet/package gate: API 0.454 seconds
and DNS services 12.942 seconds, with no frontend or browser selection. It ran from the
worktree root with:

```bash
env -u JD_DNS_SERVICES_LIVE_ENGINE -u JD_DNS_SERVICES_PREDEATH \
  TMPDIR=/home/ubuntu/Just-Dashboard/d17-t.TeRksV GOMAXPROCS=2 GOFLAGS=-p=2 \
  scripts/test-changed.sh 3e0c20161b721de9f769a13439b9813f3f7dfed3
```

Original failures remain distinct. `private-api-first.log` was a test assertion selecting the later
replay-refusal audit row instead of the successful apply row; the mutation and replay behavior was
already correct. `native-technitium-1.log` refused before a record effect because the actual pinned
writer omits the legacy `internal` field shown in its documentation example. The corrected contract
retains explicit null and authenticated native version, allowing absent-flag editing only for the
inspected 15.6/15.6.0 unsigned Primary class. It invents no external-ownership claim.
`native-adguard-enabled-refusal.log` records a later strict-field guard incorrectly rejecting the
pinned optional `enabled` boolean after one accepted add. Its terminal state was `needs_review`;
no effect was retried. The correction retains that state, refuses disabled removal, and fences
type/enable-state drift without accepting arbitrary extra fields.

Earlier passing engine logs are separate from the final source set. The initial, ownership-corrected
and pre-enabled source/binary manifests describe their exact generations. Original manifests and
all binaries remain under the workspace artifact directory; copied binary manifests name their
archival filenames after preservation. Bounded redacted native daemon logs also remain there.
Sequential native APIs still provide no CAS between the final read and mutation; uncertain effects
remain single-use `needs_review`, with no automatic retry or foreign-policy restore.

The later exact-selection current-read endpoint is covered separately by
`current-selection-race.log` (DNS 16.551 seconds, private API 4.479 seconds) and
`test-changed-current.log` (selected build/vet, API 0.499 seconds, DNS 9.886 seconds). It adds
`GET /changes/{id}/current` with the same closed selection inspector already used by preview/apply,
without changing native mutation or owned lifecycle paths. Tests prove all selected metadata drift,
connection replacement before/during reads, malformed retained fields, bounded failure/cancellation,
private capability/no-store behavior and expired/consumed inspection without replay. The three
native engine proofs above predate this read-only seam; no new engine-acceptance claim is made.
