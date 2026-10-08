# Native DNS policy investigations

The DNS page's **Policy investigation** obtains an absolute DNS record answer from an identified
systemd-resolved owner and retains its typed evidence. This is separate from the existing effective
classic wire lookup and explicitly acknowledged, named direct comparisons. It installs no resolver,
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
an active resolver owner.

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
the name. The existing effective wire lookup also refuses unreadable policy and unavailable declared
best-match scopes. An optional expected interface
must already be a best-match candidate and restricts the native query to that interface. It cannot
send a private suffix to an unrelated link.

`ResolveRecord` receives the absolute question and only DNS protocol, with cache, synthesized
records, local zones and stale cache answers disabled. Native DNSSEC/TLS validation stays enabled
according to native policy. A failed request never invokes direct comparison, host/public fallback,
LLMNR or mDNS. A/AAAA identifies **answer family**; upstream address family/source address is not
forced or measured. Returned records retain owner, type, TTL and answering interface index. These
wire records are checked against their metadata and the requested canonical chain before accepting
answers. Index zero remains global/unspecified; exact per-query upstream endpoint is unknown. The
current-server property is a configuration observation and is never labeled the answering server.

Only a reply explicitly reporting **DNS from the network**, with no cache/synthetic/local/trust-anchor
origin, establishes a transport/authentication measurement. A native authenticated flag then means
the identified resolver reports validation under its own DNSSEC policy/trust anchors. An upstream AD
bit, configured DNSSEC switch or cached/local authenticated answer does not establish that result.
A native DNSSEC failure message is separately labeled `native_error_text`; it is not an independently
validated cryptographic failure. The adapter does not build its own DNSSEC validator or export trust
anchor/private configuration files.

Fresh confidential DNS data means **native-reported encryption**. Strict DoT for every answering
scope, together with a stable before/after routing/security snapshot, supports **native strict TLS
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
64/65535 aggregate wire bytes. Each bus request has a five-second timeout and one investigation a
twenty-second total budget. These are bounded diagnostics, not a scanning or arbitrary command API.

Startup wiring calls `store.InitializeNetworkDNSEvidence(ctx, db)` before
`network.ReconcileDNSEvidence(ctx)`. Reconciliation marks a predecessor's running rows interrupted
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
owners, cache/local-origin flags, configured-only DNSSEC/DoT, opportunistic trust, malformed wire
records, immutable retention, interrupted startup, narrowed-token/read-only denial and audited
deletion. Bun tests preserve provenance labels. Browser cases use typed retained fixtures and must
run against the combined built source.

`TestDNSEvidenceNativeDisposableResolver` creates new network/mount namespaces, private `/etc`,
`/run/systemd` and D-Bus. It runs actual systemd-resolved against controlled TLS servers and an
Ed25519-signed private zone anchored only inside that namespace. It tests A/AAAA through controlled IPv4 and IPv6 upstreams, native split-link
selection, native DNSSEC validation/rejection, strict TLS identity rejection, inactive private-scope
refusal, ignoring conflicting
hosts records for wire queries, and absence of default-scope queries. It requires root namespace
privileges plus already-installed resolved/busctl/dbus-daemon/ip; absence is an explicit skip. It
does not restart or configure the production host resolver.
