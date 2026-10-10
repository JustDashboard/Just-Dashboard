# Retained network diagnostics

`internal/netdiag` saves named observations from the existing `netsec` tools and typed connection-path investigations. It uses the existing
`jobs.Manager` to schedule and watch work; it does not add a monitoring engine or replay jobs when
the backend restarts. Quick `POST /network/probe` requests remain synchronous and use the same
`executeNetworkProbe` dispatcher and `netsec.ValidateProbeRequest` vocabulary as retained runs.
Recurring watches remain owned by their existing watch scheduler.

## Record and execution boundary

`network_diagnostic_runs` is an additive SQLite table created by `store.Open` for existing installs.
A normalized request, name, actor, scope, and queued lifecycle are written **before** starting a job.
If that write fails, no probe starts. The generated run ID is independent of its in-memory job ID.
Names may be changed during or after execution; requests and completed results are immutable.
Rerun creates a new record with `rerunOf`, using the previous normalized request. Additive investigation fields live in the existing bounded JSON payload; existing quick-tool records need no schema migration and retain their request/result format.

The quick-tool request is the closed shape `{tool,target,port?,record?,option?}`. It cannot contain
shell text, a command, a filesystem path, or an arbitrary capture filter. It validates targets,
ports, record types, STARTTLS choices, and existing capture/Wake-on-LAN interface restrictions before
queueing, and the existing tool validates again at execution. The tool vocabulary is ping,
traceroute, dns, port, scan, http, tls, whois, dnsauth, banner, ssh, starttls, tlssurvey, dnsbl, asn,
mx, httpsec, siteaudit, listeners, egress, neigh, route, mtu, capture, wol, and capabilities.

A run declares `dashboard_host` as its vantage. `family` is `inet`/`inet6` for an IP literal,
`resolved_at_execution` for a name, or `not_applicable` for host inventory/LAN operations. Scope
includes the selected port/protocol/interface where applicable and explains that the legacy tool
chooses its resolver, route, and source. An observation from this host does not establish inbound
reachability, a provider firewall's decision, or every intervening policy layer. Container/source
investigation uses its separate, explicitly scoped measurement adapter, retained through the same lifecycle described below.

The three timestamped stages are validation, probe, and recording. These are the available lifecycle
boundaries; they are not invented DNS/TCP/TLS/HTTP timing stages. `status` is queued, running,
cancelling, completed, failed, cancelled, or interrupted. `outcome` distinguishes completed,
completed_with_findings (DNSBL/HTTP-hardening findings), unsupported, permission_denied, dns_failure,
refused, timed_out, invalid_certificate, failed, cancelled, and interrupted. `outcomeSource` identifies
context cancellation/deadline, tool result, error-text classification, or restart recovery. In
particular, an error-text category is a hint with declared provenance, not independently measured
protocol evidence. Completed means the tool finished successfully; it is not a whole-path health
guarantee.

If recording a finished outcome fails, reads and new launches fail explicitly while a bounded
volatile queue retries **only the database write** on later access. The host operation is never
repeated. A restart before that write succeeds loses its unsaved result and settles the unfinished
durable row as interrupted. Cancellation still signals the running context if saving the cancellation
request fails, and its API error states that the signal was sent but recording failed.

## Structured evidence

Every quick tool returns `netsec.ProbeResult` with its verbatim `output` plus structured evidence
(`probe_evidence.go`): `verdict` (`ok`, `findings`, `unknown`, `failed`; empty keeps the older `ok`
reading), a one-sentence `summary`, `facts` with a `basis` (`observed`, `configured`,
`self_reported`, `inferred`, `registry`, `unknown`), ordered `stages` (`passed`, `warning`,
`failed`, `skipped`, `unknown`; a stage after a failure is skipped, never passed), `tables` with an
optional in-product `rowLinks` entry per row, `findings` with level, owner, action and an in-product
`href`, `links`, `metrics` (`{key,label,value,unit}`) and `limitations`. An unanswered probe is
`unknown`, never a declaration that the target is down. The run outcome follows the verdict before
any error-text classification: `unknown` → `completed_with_unknowns`, `findings` →
`completed_with_findings`, both with status `completed`.

| Tool | Structured readings |
| --- | --- |
| DNS lookup | NXDOMAIN and NODATA are told apart from the first configured nameserver's response code (Go's resolver reports both as not found); NODATA is an answer, not a failure. Process-resolver answers attributed to the hosts file (NSS `files`) or to each configured nameserver asked directly over UDP (TCP after truncation) with rcode/TTL/time; name-service order, nameservers and search domains as configured facts; a hosts override that disagrees with DNS is a finding; a loopback stub links to the DNS page for its upstreams. |
| DNS authority | Zone apex discovery, the parent's referral (delegation set and glue, glue checked against the nameserver's records for in-bailiwick names), and every authoritative address asked SOA/NS with recursion off (authoritative flag, serial, NS set). Lame, silent, mismatched and serial-disagreeing servers are separate findings; an unreadable parent leaves the verdict unknown. |
| Ping | Sent/received/loss, min/avg/max/mdev and jitter between consecutive replies as metrics, a reply table, and ICMP-error replies distinguished from silence. Total silence is `unknown` with a link to the TCP port check. |
| Traceroute / tracepath | A hop table (address, round trips, `!X`-style annotations, asymmetry), `hop N address` records for comparison, hops/answering hops/reached metrics; an unreached destination is `unknown`. |
| Route lookup | Kernel route type, next hop, interface, selected source, table, origin and metric. With a port (and `option` `tcp`/`udp`) the dispatcher joins the host-source path layers (policy rules, modeled firewall, NAT candidates, egress owner) without measuring. |
| Path MTU | Discovered MTU only when tracepath reached the destination with a Resume line; otherwise `unknown` with the largest size observed so far, or filtered when no router answered. A smaller MTU than the first hop is a finding. |
| TCP port check | Connected or attempted address and local source; the dispatcher joins the same tuple's route, firewall, NAT, listener-owner and proxy layers and names disagreements (connected without a local owner, a listener that was not reached, a firewall model that predicted a block). |
| Common-port scan | The address is resolved once and pinned; every catalogue port is listed as open, closed (refused), no reply or error with its time; exact coverage, method and what is not covered are facts. |
| Banner grab | Protocol identified by grammar (SSH identification string, RFB, MySQL handshake, POP3/IMAP greetings, 220 SMTP/FTP) with a confidence and basis; product/version are self-reported facts. |
| SSH host keys | Offered fingerprints compared with saved trust: matches, `DIFFERS` (critical finding), not saved, or saved but not offered. Without saved trust the verdict is `unknown`. |
| HTTP inspection | Per-request timing (DNS, connect, TLS, first byte, total) and per-stage status for the first request; transport trust is verified separately and an untrusted 200 is a finding; HTTPS-to-HTTP redirects are findings. |
| Header grade | Response kind detected from `Content-Type` (page, API, asset, unknown) or chosen with `option` `page`/`api`; each header is required, recommended, optional or not applicable for that kind and only applicable headers are graded. |
| TLS certificate | A chain table (position, subject, issuer, validity, key, signature, SHA-256), the verified root, trust and expiry as separate facts, `leaf sha256`/`chain N sha256`/`expires` records for saved comparison, and weak-key/expiry findings. |
| TLS versions | Each version on its own TCP connection: offered, rejected by the server (protocol_version alert), no common parameters, closed during handshake, not TLS, timed out; a TCP failure is a network failure and later versions are not tested. Links to the proxy TLS report. |
| Site audit | HTTP, certificate and header stages with every finding carrying an owner and action; the dispatcher names the enabled proxy site serving the host so certificate findings point at proxy certificates and header findings at that site. |
| Mail path | Stages for MX (failed lookup ≠ none; null MX; implicit MX), exchanger addresses, SPF, DMARC, SMTP connect to the preferred exchanger and its greeting. A port-25 timeout is `unknown` because providers block it; DKIM, PTR, reputation and delivery are stated as not checked. |
| STARTTLS | Connect, greeting, the protocol's capability listing (EHLO, CAPABILITY, CAPA, FEAT), upgrade command, TLS handshake and certificate trust, each with its own status. |
| DNS blocklists | Each list's result (listed, not listed, query refused — including Spamhaus `127.255.255.x` codes — query failed, unexpected answer), return code, reason, time asked and duration. Unanswered lists keep the verdict `unknown`. SORBS is no longer queried. |
| ASN ownership | Team Cymru as the named source, announcements table, registration country labelled as not a location, multiple-origin finding. |
| Whois | Normalised fields per registry spelling, each present, redacted or absent; a registry "no match" is distinct from a lookup failure, which leaves registration unknown. |
| Listeners | A socket table (protocol, address, port, process, bound scope) with each row linking to `/proxy/ports?q=:PORT&socket=…`. |
| Egress | Per-family route, next hop, interface, source and source scope (private, CGNAT, ULA, link-local, global) with default-route counts; private/CGNAT sources are NAT findings and the public address is pointed to external checks. |
| Neighbours | Cache rows with each state explained and a per-interface summary (role, local addresses, confirmed/stale/failed); passive read only. |
| Host support | Tool table plus capability probes (see the network document). |
| Packet snapshot | Packet count, fixed limits and a link that opens `/network/captures` setup prefilled with the interface and protocol. |
| Wake-on-LAN | Last cached address for the MAC, a pre-send check, the send stage and optional measured verification (`verify` plus `port`), reporting the seconds until an answer or `unknown`. A device that already answered before the packet is `unknown` (no wake time recorded); ICMP verification requires host `ping`, and a check that cannot run is reported as such rather than as silence. |

## History, saved interactive results and trusted references

`GET /{id}/history` returns every retained terminal run with exactly the same normalized request,
oldest first, with its outcome, verdict, summary and metrics. It reads saved runs only and is bounded
by the retention policy. Comparison adds `metrics` (before/after per key) and folds structured lines
(verdict, facts, stage outcomes, table rows, findings) into the record difference; columns whose
header names a time, round trip, first byte, total or check time — and the `Checked at` and
`Local source` facts — are left out so timing and ephemeral ports alone do not make every run
differ. A run's status follows its verdict: any structured verdict other than `failed` is
`completed`.

A quick `POST /network/probe` answer carries a `resultId`. The server keeps that exact result in a
volatile cache for fifteen minutes (at most 64 entries) bound to the requesting account.
`POST /results` `{resultId,name}` moves it into a completed run without running the tool again; the
hold is consumed only by a successful save (a refused name or unavailable store puts it back),
another account's or an expired ID answers 410, and the run's scope records that it was saved after
an interactive run. Saved runs never retain a `resultId`.

Trusted SSH fingerprints and Wake-on-LAN devices are bounded JSON in the existing settings table
(`network.diagnostics.ssh_trust`, at most 128 hosts × 8 keys; `network.diagnostics.wol_devices`, at
most 64), so no schema change is needed. A trust entry is keyed by canonical `host:port`, stores
`observed` (trusted from a scan) or `entered` (copied out of band) with who saved it and when, and
validates key types and `SHA256:` fingerprints. Observed keys are never taken from the client: the
server reads them, with the scanned host and port, from the requester's held SSH scan, so editing
the target after a scan cannot attach one host's keys to another. Saving over existing trust needs
the destructive replace route, and the UI confirms it with a stronger warning when the scan reported
a changed key. An unreadable trust store leaves a scan `unknown` ("could not be compared"), never
"none saved". A device stores name, canonical unicast MAC,
interface name syntax and an optional literal verification address and TCP port.

## Retained connection investigations

`POST /diagnostics/investigate` accepts `{name,investigation}` with the same closed `netpath.Request`
as quick `POST /network/investigate`. It validates source kind/full container ID, explicit family,
protocol, port, source, selected DNS address and mark before queueing. The run declares
`kind: "investigation"` and retains `investigationRequest` and a typed `investigation` artifact;
its unused quick-tool `request` is not treated as an alternate command vocabulary.

The existing four-run admission budget, exclusive jobs, durable recording, cancellation, retention,
restart interruption, private reads and exports apply to both kinds. Each explicit rerun reacquires
fresh native inventory and verifies current container identity; a retained record never holds or
replays a PID or network-namespace descriptor. Provider construction and investigation share a
30-second operation deadline inside the common 90-second job deadline. No arrival, read, refresh,
export, comparison or backend restart launches an investigation.

The timestamped lifecycle is validation, investigation, recording. The typed artifact preserves
individual layer timestamps, facts, observed/modeled/measured/unknown bases and limitations.
`completed_with_unknowns` and `completed_with_findings`, with `outcomeSource: "path_evidence"`, mean
the report completed with those observations; they do not claim whole-path connectivity. Cancellation
and runner/recording errors retain the existing lifecycle semantics. Actual selected source and
address are copied to the run's scope after execution without changing its immutable request.

Investigation artifacts are capped at 128 KiB of encoded JSON. Bounded text, layer/fact/address
counts and escaped content are copied and clipped before storage; further omissions set the same
explicit `resultTruncated` flag. Lists omit both artifact kinds. Export includes the typed request,
source, evidence and truncation. Compatible comparison requires exactly the same source kind,
container identity, requested source/destination, family, protocol, port, mark and measurement choice.
It compares retained layer bases/states, facts, owners and limitations, excluding collection timestamps
from the line differences. Actual observed source/destination changes remain visible; unknown layers
remain unknown and differences do not establish a cause.

The investigator's **Run and save** opens the shared naming dialog with an immutable draft snapshot;
the dialog displays source/family/protocol and whether measurement is requested. Opening it emits no
network traffic. A rejected launch retains its name and scoped request; acceptance navigates to the
Saved runs inspector. The inspector uses the same connection-path report renderer as the quick page,
then offers save name, cancellation, rerun, export, watch and exact-scope comparison.

## Cancellation and restart

At most four retained runs execute concurrently, each with a 90-second deadline. They use exclusive
job entries with a unique run prefix so that the job remains running while cancellation drains.
Cancellation first persists `cancelling`, then cancels the job context. The existing network command
runner uses `hostexec.RunGroup`, which signals the entire process group with TERM and, after its
bounded grace, KILL, and waits for cleanup. Native Go probes receive the same context. A run is
recorded as cancelled only after its runner returns; closing a browser or reading/streaming a job
does not cancel or restart it. Cleanup errors remain in the bounded run error.

Graceful API shutdown cancels and waits up to ten seconds for diagnostics only. It retains the
existing policy of allowing certificate/package jobs to finish. At startup, every unfinished saved
run becomes interrupted with an end timestamp and an explanation that it was not rerun and final
host-process cleanup was not independently observed. A restart record must not imply successful
cleanup of an abrupt predecessor. Startup failure leaves diagnostics explicitly unavailable (503)
while other optional features remain usable.

## Retention and artifact limits

The default policy retains 100 finished runs for seven days. The admin API persists its override in
the existing settings table under `network.diagnostics.retention`: 1–256 finished runs and 1–2160
hours (90 days). No environment variable or additional setup command is required. Retention is
applied at startup, creation, completed writes, reads, and policy changes. Running records are never
evicted. Expired records cannot be retrieved/exported after a quiet period because reads prune them;
there is no new background pruning worker.

Retained output is capped at 64 KiB, individual structured records at 1 KiB, and record count at 128.
Structured evidence is copied with its own bounds: 64 facts, 32 stages, 8 tables of 256 rows and 12
columns, 64 findings, 16 links, 32 metrics and 16 limitations, each string at 1 KiB. Encoded result
JSON is capped at 128 KiB, including escaping — output halves first, then the largest table, then
records; truncation is explicit. Stored run JSON
and a versioned export are bounded at 512 KiB. Exports contain the run's scope, timestamps, request,
stages, outcomes, and retained result; they are private/no-store JSON attachments using a generated
run ID in the filename. Capture keeps bounded packet summaries, including possibly sensitive
decoded fields; it never creates a PCAP or payload-dump artifact.

Lists omit result bodies. Get/export retrieve one bounded artifact, including its structured evidence. Compare requires two terminal
runs with identical normalized requests and retained results. It reports status/outcome changes,
the tool's duration difference where parseable, and added/removed/unchanged structured records and
output lines. Line order and duplicate counts are ignored. Each side of each difference is capped at
128 lines of 1 KiB, and truncation/partial inputs are reported. Changed tool timestamps/counters do
not establish the cause of a difference.

## API and authorization

All paths are under `/api/v1/network/diagnostics` and require `system.admin`, including reads,
exports, and comparisons. Packet summaries and private LAN targets are not exposed to a reader or
limited principal, including a token narrowed from an admin account. Diagnostic entries in the
generic `/jobs` list are filtered for those principals; get/stream/cancel check the diagnostic kind
prefix. Ordinary jobs retain their existing read permissions. The generic job ring remains an
in-memory status/watch channel with its existing retention; it contains diagnostic lifecycle lines,
not retained probe output.

| Method | Path | Behavior |
| --- | --- | --- |
| POST | `/` | Save `{name,request}` and start an explicit quick-tool run; return 202 |
| POST | `/investigate` | Save `{name,investigation}` and start one explicitly scoped path report; return 202 |
| GET | `/` | List retained metadata without result bodies |
| GET | `/{id}` | Read one run and its retained result |
| PATCH | `/{id}` | Save `{name}` without changing request/result |
| POST | `/{id}/cancel` | Persist cancellation request; return 202 while stopping |
| POST | `/{id}/rerun` | Create a new record; return 202 |
| GET | `/{id}/export` | Download versioned bounded JSON |
| GET | `/compare?before=<id>&after=<id>` | Compare compatible terminal runs, including metric changes |
| GET | `/{id}/history` | Metrics and outcomes of every retained run of the same request |
| POST | `/results` | Save `{resultId,name}` from the server's hold as a completed run; 410 when expired |
| GET | `/ssh-trust` | List saved trusted SSH fingerprints |
| PUT | `/ssh-trust` | Save new trust: `{source:"observed",resultId}` from a held SSH scan, or `{source:"entered",target,port,keys}`; 409 `ssh_trust_exists` when the host already has trust |
| PUT | `/ssh-trust/replace` | The same body, replacing existing trust, through `s.destructive` |
| DELETE | `/ssh-trust?host=<host>&port=<port>` | Forget one host's fingerprints (canonicalized server-side) through `s.destructive` |
| GET | `/wol-devices` | List saved Wake-on-LAN devices |
| POST / PUT | `/wol-devices`, `/wol-devices/{device}` | Create or replace a device |
| DELETE | `/wol-devices/{device}` | Delete a device through `s.destructive` |
| DELETE | `/{id}` | Delete a finished record through `s.destructive` |
| GET | `/policy` | Read retention policy |
| PUT | `/policy` | Save `{maxRuns,maxAgeHours}` through `s.destructive` |

Every mutation, including rejected attempts, passes the existing audit middleware. Successful
handlers attach `network.diagnostic.*`, `network.ssh_trust.*` and `network.wol_device.*` actions
and generated run/job IDs; a saved trust entry's audit detail lists its fingerprints. Cancel and rerun are
ordinary admin mutations. Deletion and policy changes use the destructive capability and rate
budget because they erase saved records; neither needs the rare server-side typed phrase. Deleting
an active run is rejected so its process cannot be orphaned.

## Operator workflow

Every tool card renders the shared `components/network/tools/probe-evidence.tsx`: summary, checks,
findings (owner, action and an in-product link), measurements, facts with a basis tag, tables with
row links, next-step links and limits; raw output sits behind **Tool output** when structured
evidence exists. An answer with a `resultId` offers **Save this result**, which names and saves the
held result and opens it in Saved runs. The SSH card offers **Trust the keys <host> offered** and
**Save fingerprint** for a pasted `SHA256:` value, replacement of existing trust after a
confirmation, and **Forget saved fingerprints for <scanned host>** with ordinary confirmation; the
scan actions follow the shown scan, not the current inputs, and none of these contacts the server. The Wake-on-LAN card adds optional **Verify
address**/**Verify TCP port** inputs and a saved-device list read only while the card is in view;
**Use** fills the inputs and sending remains a press. Route lookup takes an optional port and
protocol; Header grade takes a response kind.

Network tools keep their quick **Run** command and current drafts/results. **Run and save** snapshots
that same tool request, asks for a bounded name, and launches only after an explicit press. A rejected
launch retains its name and tool draft for retry. The command opens `/network/runs?run=<id>` after
acceptance. The Tools/Saved runs links join the two surfaces; no page arrival or reload emits a probe.

`components/network/saved-runs.tsx` renders `/network/runs` in the reporting register: counts on a
`StatGrid`, retained history as `ChoiceRow` destinations, and one framed, independently scrolling
history/inspector workbench. It shows scope, timestamps, lifecycle stages, typed outcome and its
provenance. Structured records remain readable; raw tool/error text is behind **Bounded tool
evidence**. Completed is kept distinct from a whole-path guarantee, and unsupported, permission,
DNS, refusal, timeout, certificate, findings, interruption, and absent answers have separate labels.

The inspector renders a retained result's structured evidence with the same component as the quick
card, shows **History of this request** (one row per retained run with its outcome and every metric
column) and lists measurement changes in a comparison. The inspector retains its selected ID in the
URL and retrieves its saved result after navigation or reload. **Save name**, **Cancel run**, **Rerun**, **Export JSON**, and compatible-run **Compare** use the
API lifecycle above. **Watch job** attaches the existing job console to its saved job ID without
restarting work. Cancellation remains visibly stopping until the durable record settles. Name and
retention drafts survive rejected writes. Deletion and retention changes use ordinary destructive
confirmation, with no typed phrase. Readers see a capability notice and issue no diagnostic reads;
their local subnet calculator remains accessible from Quick tools.

List, result, and retention reads retain successful data on a failed later poll and show a dated
retry warning. Initial failures show an error with Retry, so an unavailable backend is never shown as
an empty successful inventory. Retention/count bounds and comparison compatibility are shared pure
readings in `lib/network-diagnostics.ts`; backend authorization and validation remain authoritative.

## Verification

`netdiag/investigation_test.go` covers typed source persistence/reopen/export, explicit fresh rerun, exact-scope comparison, cancellation cleanup, validation before queueing and escaped artifact bounds. `api/handlers_network_diagnostic_investigation_test.go` covers the real router and private metadata/artifact split; the shared role tests include the investigation launch route.

`netdiag/service_test.go` covers durable reopen, explicit reruns, interrupted startup without traffic,
name preservation, cancellation during cleanup, real TERM-ignoring forked descendants, concurrency,
failed durable writes, timeout provenance, retention and artifact/export/compare bounds.
`netsec/probe_request_test.go` covers the shared closed vocabulary, defaults, IPv6 normalization and
rejected options. `api/handlers_network_diagnostics_test.go` drives the real router for lifecycle,
audit, exports, comparison, invalid requests, and reader/limited/narrowed-token denial across
diagnostic and generic job paths. `store/network_diagnostics_test.go` verifies additive creation on
an existing install while preserving its settings.

`netsec/diag_*_test.go` cover every tool's parsing and verdicts against recorded tool output and
loopback fixtures: a UDP/TCP fake DNS server for wire provenance and truncation, in-memory and
loopback authorities for delegation/glue/serial/lame cases, httptest servers for timing stages,
trust, response kinds, chains and per-version TLS refusal, fake SMTP/IMAP/POP3/FTP servers for each
STARTTLS stage, fixture resolvers for mail stages and blocklist codes, and stubbed runners for
ping, traceroute/tracepath, route, whois, ASN, listeners, egress, neighbours, capture and
Wake-on-LAN verification. `netpath/probe_join_test.go` covers layer joins and port correlation;
`netx/capabilities_test.go` covers probes without mutating commands; `netdiag/history_test.go` and
`saved_test.go` cover verdict outcomes, structured comparison, history, adoption, bounds and the
saved stores; `api/handlers_network_diagnostic_saved_test.go` covers the new routes' gating, audit,
410 holds and SSH comparison, and `handlers_network_capabilities_test.go` the route-layer join on
loopback. `tests/browser/network-tools.spec.ts` drives the structured card, save-result, route
join, listener links, hop table, SSH trust, Wake-on-LAN devices/verification, capture hand-off,
subnet comparison and IPAM check (admin and reader), saved-run history/measurement changes and
375/1280-pixel containment.

`lib/network-diagnostics.test.js` covers compatible requests, terminal/unfinished evidence, byte and
retention limits, and the outcome labels. `tests/browser/network-runs.spec.ts` prepares 19 cases for
409/500 launch retry with an IPv6 draft, save rejection, lifecycle persistence on reload, pending
cancellation, explicit rerun/compare/export/delete, restart interruption without replay, each typed
failure/finding, initial/stale result retry, retention confirmation/refusal, read-role gating, and widths of
375, 1280, and 1720 pixels. The desktop cases also save screenshots for visual review.

Run `scripts/test-changed.sh c91b3903` for this tranche's scoped checks. For a focused iteration:

```bash
cd backend
go test ./internal/netdiag ./internal/netsec ./internal/store ./internal/api \
  -run 'Test(SavedDiagnostic|StartupInterrupts|CancellationWaits|CancellationSignals|FailedFinalRecording|Diagnostic|RetentionPersists|ConcurrencyValidation|TimeoutAndOutcome|ArtifactExport|ProbeRequest|OpenAddsDiagnostic)'
go test -race ./internal/netdiag
```

Capture keeps bounded packet summaries in quick snapshots; its structured result links to a retained
PCAP job instead of creating one.

The connection-path inspector labels elapsed report collection separately from any retained TCP
measurement. It does not present collection time as connection latency, and missing or invalid
report timestamps remain “Not recorded”.

## Watched probes

Saved runs are evidence kept on request. A question to be asked again on a schedule is a **watched
probe**: a TCP connection checked by the proxy's watch monitor on the watch list's interval, with
its history and the `watch_unreachable` alert, rather than a second monitor. The TCP port check's
**Watch on a schedule** creates one, and the runs page lists them; see
[the watch list](databases-proxy-platform.md#proxy) (`watched_endpoints.kind = 'tcp'`).

