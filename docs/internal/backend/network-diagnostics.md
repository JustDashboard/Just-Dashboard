# Retained network diagnostics

`internal/netdiag` saves named observations from the existing `netsec` tools. It uses the existing
`jobs.Manager` to schedule and watch work; it does not add a monitoring engine or replay jobs when
the backend restarts. Quick `POST /network/probe` requests remain synchronous and use the same
`executeNetworkProbe` dispatcher and `netsec.ValidateProbeRequest` vocabulary as retained runs.
Recurring watches remain owned by their existing watch scheduler.

## Record and execution boundary

`network_diagnostic_runs` is an additive SQLite table created by `store.Open` for existing installs.
A normalized request, name, actor, scope, and queued lifecycle are written **before** starting a job.
If that write fails, no probe starts. The generated run ID is independent of its in-memory job ID.
Names may be changed during or after execution; requests and completed results are immutable.
Rerun creates a new record with `rerunOf`, using the previous normalized request.

The request is the closed quick-tool shape `{tool,target,port?,record?,option?}`. It cannot contain
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
investigation requires its separate, explicitly scoped measurement adapter.

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
Encoded result JSON is capped at 128 KiB, including escaping; truncation is explicit. Stored run JSON
and a versioned export are bounded at 512 KiB. Exports contain the run's scope, timestamps, request,
stages, outcomes, and retained result; they are private/no-store JSON attachments using a generated
run ID in the filename. Capture keeps bounded packet summaries, including possibly sensitive
decoded fields; it never creates a PCAP or payload-dump artifact.

Lists omit result bodies. Get/export retrieve one bounded artifact. Compare requires two terminal
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
| POST | `/` | Save `{name,request}` and start an explicit run; return 202 |
| GET | `/` | List retained metadata without result bodies |
| GET | `/{id}` | Read one run and its retained result |
| PATCH | `/{id}` | Save `{name}` without changing request/result |
| POST | `/{id}/cancel` | Persist cancellation request; return 202 while stopping |
| POST | `/{id}/rerun` | Create a new record; return 202 |
| GET | `/{id}/export` | Download versioned bounded JSON |
| GET | `/compare?before=<id>&after=<id>` | Compare compatible terminal runs |
| DELETE | `/{id}` | Delete a finished record through `s.destructive` |
| GET | `/policy` | Read retention policy |
| PUT | `/policy` | Save `{maxRuns,maxAgeHours}` through `s.destructive` |

Every mutation, including rejected attempts, passes the existing audit middleware. Successful
handlers attach `network.diagnostic.*` actions and generated run/job IDs. Cancel and rerun are
ordinary admin mutations. Deletion and policy changes use the destructive capability and rate
budget because they erase saved records; neither needs the rare server-side typed phrase. Deleting
an active run is rejected so its process cannot be orphaned.

## Verification

`netdiag/service_test.go` covers durable reopen, explicit reruns, interrupted startup without traffic,
name preservation, cancellation during cleanup, real TERM-ignoring forked descendants, concurrency,
failed durable writes, timeout provenance, retention and artifact/export/compare bounds.
`netsec/probe_request_test.go` covers the shared closed vocabulary, defaults, IPv6 normalization and
rejected options. `api/handlers_network_diagnostics_test.go` drives the real router for lifecycle,
audit, exports, comparison, invalid requests, and reader/limited/narrowed-token denial across
diagnostic and generic job paths. `store/network_diagnostics_test.go` verifies additive creation on
an existing install while preserving its settings.

Run `scripts/test-changed.sh c91b3903` for this tranche's scoped checks. For a focused iteration:

```bash
cd backend
go test ./internal/netdiag ./internal/netsec ./internal/store ./internal/api \
  -run 'Test(SavedDiagnostic|StartupInterrupts|CancellationWaits|Diagnostic|RetentionPersists|ConcurrencyValidation|TimeoutAndOutcome|ArtifactExport|ProbeRequest|OpenAddsDiagnostic)'
go test -race ./internal/netdiag
```
