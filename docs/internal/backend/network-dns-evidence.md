# Native DNS policy investigations

The DNS page's **Policy investigation** obtains an absolute DNS record answer from an identified
systemd-resolved owner and retains its typed evidence. Effective lookup reuses the native safety
adapter; retained investigations additionally expose provenance and immutable per-question evidence.
Effective lookup and comparison inventory read the configured chain in the host namespace, because
Docker overlays a container-specific `/etc/resolv.conf` on the otherwise mounted host `/etc`.
An unreadable, malformed or oversized host chain stops effective lookup before a DNS question.
Explicitly acknowledged, named direct comparisons remain a separate classic wire operation. It installs no resolver,
trust anchor, certificate or dependency and changes no resolver configuration.

## Native scope and evidence

The adapter runs bounded explicit argv through `hostexec.CommandOnHost` with process-group
cancellation. It identifies the system-bus owner of `org.freedesktop.resolve1`, verifies that its
process executable is systemd-resolved, reads that executable's version and addresses subsequent
queries to its unique bus identity. Version 256 or later is required for the complete fresh-network
flag contract, including refusal of stale cache answers. A replaced/unreadable owner or oversized
inventory fails without issuing a query to another resolver. Static resolv.conf and other owners are
currently **unsupported/unknown** in this detailed adapter; their configured-chain classic wire
lookup remains available through the existing lookup API. Installed binaries alone do not establish
an active resolver owner. Before every question, the adapter requires native `ResolvConfMode` to be
`stub` or `static` and freshly reads the host chain: every configured nameserver must be the supported
`127.0.0.53` stub. Foreign/uplink chains, mixed owners, the proxy stub and unreadable/empty chains are
unsupported here; a separately running resolved process never substitutes for the actual host owner.

Manager and per-link D-Bus properties provide DNS servers, routing/search domains, effective
default-route/DNSSEC/DoT settings and negative trust anchors. The manager's flattened server list
does not reliably identify policy ownership by its address-scope ifindex: a loopback server can
carry the loopback index even when assigned to another link. The adapter enumerates native link
objects, reads their own server/domain properties, and subtracts their address-scope/endpoint multisets from the
manager list to recover global servers. Native `ScopesMask` distinguishes an active DNS link from
declared configuration on an inactive link. Longest matching suffix, including `~.`, produces modeled
policy candidates. Unmatched questions use eligible native default-route/global candidates; a
fallback-only or unreadable scope is refused rather than inferred. A matching declared suffix with
no servers or inactive DNS scope stops the query before native default-scope fallback can disclose
the name. Malformed domains, missing security settings, unmatched manager/link domain inventories,
empty policy and unreadable link ownership also stop disclosure. Effective native lookup uses the
same checks. An optional expected interface
must already be a best-match candidate and restricts the native query to that interface. It cannot
send a private suffix to an unrelated link.

`ResolveRecord` receives the absolute question and only DNS protocol, with automatic CNAME/DNAME
following disabled, cache, synthesized
records, local zones and stale cache answers disabled. Native DNSSEC/TLS validation stays enabled
according to native policy. A failed request never invokes direct comparison, host/public fallback,
LLMNR or mDNS. A/AAAA identifies **answer family**; upstream address family/source address is not
forced or measured. Returned records retain owner, type, TTL and answering interface index. These
wire records are checked against their metadata and exact question owner/type before accepting
answers. Index zero remains global/unspecified; exact per-query upstream endpoint is unknown. The
current-server property is a configuration observation and is never labeled the answering server.

The [v257 flag contract](https://github.com/systemd/systemd/blob/v257/src/resolve/resolved-def.h)
defines `NO_CNAME` at bit 5; bit 10 is `NO_VALIDATE` and is never set. The request disables local
trust-anchor answers with bit 14 while preserving the validator's trust policy. `ResolveRecord`
adds `NO_SEARCH` internally; that bit is not accepted in caller flags. Output protocol/origin,
authenticated and confidential flags have separate meanings; unknown future output bits leave
transport/authentication unmeasured instead of inheriting an assumed meaning.

A native disabled-alias diagnostic triggers an explicit CNAME question for that same owner. Only a
single canonical exact-owner IN CNAME is accepted. Its target receives a fresh owner/delegation and
complete policy read before any target question. Inactive, serverless or unreadable private suffixes
stop the chain without default questions. Loops stop before a repeated question, at most eight
redirects are permitted, and the entire chain shares the twenty-second deadline and aggregate record
bounds. An explicit CNAME request returns its immediate target without following it. DNAME remains
unsupported and is refused. Each attempted question retains its name/type, unique owner, selected
before/after policies, complete-policy SHA-256 snapshots, native flags, answering links, record
metadata, alias edge and diagnostic. These sequential reads do not claim atomic policy isolation.
The report UI lists those questions in order, with each accepted answer, native policy scope,
transport/TLS/DNSSEC provenance and explicit unknowns for failed discovery questions. Historical
reports without the optional question chain continue to display their original artifact.

Only a reply explicitly reporting **DNS from the network**, with no cache/synthetic/local/trust-anchor
origin, establishes a transport/authentication measurement. A native authenticated flag then means
the identified resolver reports validation under its own DNSSEC policy/trust anchors. An upstream AD
bit, configured DNSSEC switch or cached/local authenticated answer does not establish that result.
A native DNSSEC failure message is separately labeled `native_error_text`; only the first exact
`Call failed: DNSSEC validation failed:` diagnostic qualifies. Server-controlled EDE text inside an
ordinary RCODE failure cannot acquire that label. It is not an independently
validated cryptographic failure. The adapter does not build its own DNSSEC validator or export trust
anchor/private configuration files.

Fresh confidential DNS data means **native-reported encryption** for every accepted CNAME and
terminal record. Authentication also requires every accepted record's native flag; a validated
terminal record cannot hide an unsigned alias. Failed discovery questions return no native flags,
so their transport remains unknown and is explicitly excluded from accepted-data claims. Strict DoT
for every answering scope of every accepted question, together with stable before/after
routing/security snapshots, supports **native strict TLS
policy** evidence: systemd-resolved enforces its declared server identity and system certificate
trust. Opportunistic TLS proves neither certificate trust nor resistance to downgrade. A changed or
unreadable after-snapshot leaves TLS trust unknown. Neither label claims certificate inspection,
packet capture, independent cryptographic chain verification, or encryption beyond that resolver's
upstream connection. The [native API](https://github.com/systemd/systemd/blob/v257/man/org.freedesktop.resolve1.xml)
and [strict DoT implementation contract](https://github.com/systemd/systemd/blob/v257/src/shared/resolve-util.h)
define these distinctions.

This DNS record operation excludes hosts/NSS/search semantics. Application NSS, browser/application
DoH, container clients, upstream forwarding, provider layers, authority/delegation and DNS64/NAT64
remain unknown unless a separate supported measurement establishes them. No result claims generic
connectivity or whole-path health.

## Retention, routes and startup

The additive `network_dns_evidence` SQLite table holds immutable normalized question/scope, actor,
start/end timestamps, lifecycle and bounded result JSON. At most four running investigations are
admitted transactionally; up to 128 finished reports survive for seven days. Admission, finalization
and reads prune finished records. A single artifact is limited to 192 KiB. Native command output is
capped at 256 KiB, native server inventory at 256, manager domains at 2048, native link objects at
128, per-link domains/trust anchors at 256, interface-name inventory at 4096 and answer records at
64/65535 aggregate wire bytes across the complete chain. A complete policy snapshot is capped at
64 KiB; retained chains reserve space before another question and refuse oversized answer data
without an alternate query. Each bus request has a five-second timeout and one investigation a
twenty-second total budget. These are bounded diagnostics, not a scanning or arbitrary command API.

`Store.Open` calls `store.InitializeNetworkDNSEvidence(ctx, db)`; schema failure prevents startup.
`Server.Start` then calls `network.ReconcileDNSEvidence(ctx)` under a five-second context. Reconciliation marks a predecessor's running rows interrupted
without rerunning their private questions or claiming that a lost process was observed to finish.
Request cancellation finishes only the result database write under a separate five-second context.
Failure to save is explicit; it does not retry the query. The existing DNS router mounts the evidence
routes; no module/netdiag field or machine/human authentication exception is added.

All paths below `/api/v1/network/dns/evidence` require backend `system.admin`, including history,
answers and exports. Normal allowlist, authentication, CSRF and rate controls apply. Mutations are
audited with the bounded normalized request and generated ID; native output/process details and
trust store files are not placed in audit detail. Delete uses the existing destructive capability and
rate budget. No typed phrase is required for deleting a finished retained artifact.

| Method | Path | Behavior |
| --- | --- | --- |
| POST | `/` | Explicitly investigate `{name,type,expectedInterface?}` and retain report; return 201 |
| GET | `/` | Retained metadata, without answer bodies |
| GET | `/{id}` | One typed artifact |
| GET | `/{id}/export` | Versioned private/no-store JSON attachment |
| DELETE | `/{id}` | Delete a finished record; a running row cannot be deleted |

No saved report automatically reruns, inherits disclosure consent or enables direct comparison.
The UI retains the last selected evidence when a launch/history read fails and shows its timestamp
and limitations. The separate named comparison control still requires an explicit destination choice
and disclosure acknowledgment.

## Controlled validation

Focused Go/API/schema tests cover private-suffix selection, wrong expected links, unreadable/changed
owners, safe/blocked/looped CNAME chains, whole-chain trust, foreign configured resolver chains,
spoofed upstream EDE diagnostics, cache/local-origin flags, configured-only DNSSEC/DoT, opportunistic trust, malformed wire
records, immutable retention, interrupted startup, narrowed-token/read-only denial and audited
deletion. Bun tests preserve provenance labels. Browser cases use typed retained fixtures and must
run against the combined built source.

`TestDNSEvidenceNativeDisposableResolver` creates new network/mount namespaces, private `/etc`,
`/run/systemd` and D-Bus. It runs actual systemd-resolved against controlled TLS servers and an
Ed25519-signed private zone anchored only inside that namespace. It tests A/AAAA through controlled IPv4 and IPv6 upstreams, native split-link
selection, signed safe CNAME chains, blocked inactive private alias targets, loops/DNAME refusal,
foreign host-owner refusal, ordinary native SERVFAIL/EDE classification under an explicit
nonvalidating fixture scope, native DNSSEC validation/rejection under the restored strict signed-zone policy,
strict TLS identity rejection, inactive private-scope refusal, ignoring conflicting
hosts records for wire queries, and absence of default-scope queries. It requires root namespace
privileges plus already-installed resolved/busctl/dbus-daemon/ip; absence is an explicit skip. It
does not restart or configure the production host resolver.

Run from `backend/`, using a task-owned artifact directory:

```bash
fixture_dir=$(mktemp -d)
GOMAXPROCS=2 go test -race -c -o "$fixture_dir/dns-evidence.test" ./internal/netx
sudo env GOMAXPROCS=2 "$fixture_dir/dns-evidence.test" -test.run '^TestDNSEvidenceNativeDisposableResolver$' -test.count=1 -test.v
```

Running the Go test as an ordinary contributor skips this namespace fixture; the root test binary
provides the native acceptance. It opens no production resolver configuration.
