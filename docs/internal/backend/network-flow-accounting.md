# Native socket history

`internal/netflows` is the opt-in P8 socket baseline. It retains evidence for identifying observed
TCP byte users, looking up an observed peer address by a freshly verified Docker identity, and
comparing sampled retransmission deltas and outstanding-loss gauges. P8 remains in progress: no
supported kernel observer is bundled, so UDP bytes, complete short-lived flows, close events and
actual dropped-event counts remain unavailable. A socket snapshot is not complete flow accounting.

## Collection and identity

Recording defaults **off**. Opening a page, filtering history and exporting never launch `ss` or
Docker inspection. Enabling recording starts an independent 30-second cadence; the policy allows
10–300 seconds. Stopping recording cancels the active capture and fences its database commit.
Changing policy, clearing history and restarting the backend reset the volatile byte baseline.
The response distinguishes recording intent, collector startup, latest recorded sample and query
read time. Previously saved rows remain readable when the recorder is off.

The native command is a fixed argv: `ss -H -n -t -u -a -i -e -p -O`. Each observed source carries the
namespace device/inode, capture time, actual field-availability counts, cap/parse/identity failures,
unknown descriptor-owner counts, and kernel release. The last `ss -V` reading includes its own time;
it is refreshed hourly and failure is distinct from a measured version. Socket values remain
private to the collector until reduced into bounded rows.

The collector pins the host namespace or a verified container namespace with an inherited file
descriptor. It samples the host and at most four running Docker sources, rotating a capped batch
only after two consecutive reads. More sources are explicitly omitted. Shared namespaces are
sampled once, including host-network containers. Namespace membership alone never assigns bytes to
its first container. Each descriptor must have one native owner, matching process start ticks,
cgroup and `socket:[inode]` FD link across the identity checks. Docker ownership additionally uses
fresh full-ID/running/PID/start-time/cgroup checks through `NetworkSourceCommand`, compares the
pinned namespace, and rechecks the container instance after the entire capture. Restarted,
unreadable, shared-descriptor and outside-budget owners stay unknown. Full container ID and startup
identity are retained alongside the observed name; the name is an alias, not ownership proof.
Descriptor ownership does not prove which side initiated the connection or where NAT sent it.

A byte baseline binds boot ID, namespace identity, protocol, socket cookie/inode, full tuple and
observed owner identity. The first observation contributes **no lifetime bytes**. The next read
contributes an independently nullable `bytes_sent` / `bytes_received` delta only when the native
field exists in both reads, the counter did not reset, the owner is unchanged, the source did not
fail, the gap is at most twice the configured interval and both readings are in the same UTC hour.
Absent fields stay unknown, including zero-valued fields that a native `ss` version suppresses.
Cross-hour intervals are discarded, so yesterday's view never assigns an interval spanning midnight
to one day. Unknown identity rows are counted in source quality and cannot acquire invented totals.
Each row separately counts measured sent, received and retransmission intervals. Counter overflow or an implausibly large single delta is also unknown.

UDP retains protocol, local endpoint, observed connected peer, state and owner evidence, with byte
counts unavailable. An unconnected socket has no observed destination. Sockets born and closed
between reads can disappear entirely or leave only an unusable TIME-WAIT identity. Disappearance
is not a recorded close event. Capture gaps, omitted sources, capped snapshots and unknown counters
remain separate from a dropped-event count, which is `null` for this baseline.

`retrans:current/total` supplies the sampled sender's cumulative total when present. Its difference
is a retransmission count, not a retransmitted-byte measurement. `lost` is a sampled outstanding-loss
gauge; a bucket keeps its maximum rather than summing it. Neither is an end-to-end loss percentage.
Native field semantics and flags are grounded in [ss(8)](https://man7.org/linux/man-pages/man8/ss.8.html)
and Linux's [TCP_INFO definition](https://kernel.googlesource.com/pub/scm/linux/kernel/git/next/linux-next/+/master/include/uapi/linux/tcp.h).

## Storage, queries and access

The additive central schema owns `network_flow_buckets` and `network_flow_cycles`. Rows aggregate
one socket/owner identity per UTC hour; coverage records aggregate actual samples per UTC hour and
retain the last native source reading. Normal sampling never adds columns to existing tables.
Policy and cumulative prune count use the existing settings table. A capture, its quality record,
retention pruning and settings update share one SQLite transaction. Failed recording is exposed as
unavailable and discards the byte baseline; it does not publish unsaved success or backfill lifetime
counters after a later successful write.

Retention defaults to seven days, permits 1–31 days, and is bounded by 50,000 socket-hour rows and
32 MiB of combined encoded row/coverage payload. Expiration uses UTC hour boundaries, with less
than one additional hour of boundary precision. Reads, policy changes, captures and startup prune
expired records. An off recorder without reads does no collection; expired rows are removed at its
next access/startup. This is a logical payload cap: SQLite indexes, reusable database pages, its WAL
and existing backups follow the store's ordinary lifecycle. Clearing history removes application
rows, not forensic copies in those storage layers.

Each source admits at most 1,024 non-listening socket rows. A native command retains at most 2 MiB
stdout and 4 KiB stderr, with a two-second command deadline inside an eight-second total capture
budget. The bounded writer deliberately does not embed `bytes.Buffer`, whose promoted `ReadFrom`
could bypass `Write` through `io.Copy`. The regression exercises the actual subprocess-output path.

Queries accept UTC hour boundaries spanning at most 31 days, canonical literal peer IPs, exact full
Docker IDs and a limit of 1–1,000 rows (default 200). The default period is yesterday in UTC. There
is no DNS resolution or request-derived command. Queries return a cap flag, earliest retained row,
pruned-row count, period coverage, independent latest-sample evidence and explicit observer status.
Empty rows do not establish no contact or zero bytes. Report figures cover displayed sampled rows;
they are neither all-host traffic nor provider/billing totals. Public uint64 counters are decimal
JSON strings or `null`, preserving values beyond JavaScript's integer precision.

All paths below `/network/flows` require `system.admin`, including exports. Responses use
`Cache-Control: private, no-store`. The API surface is:

| Method / path | Effect |
| --- | --- |
| `GET /network/flows/` | Read bounded retained observations and quality. |
| `GET /network/flows/export` | Export at most 1,000 rows and 2 MiB JSON. Both row and coverage truncation are explicit; one export is admitted at a time. |
| `POST /network/flows/recording` | Audit an explicit `{enabled}` opt-in/out; no host network mutation. |
| `PUT /network/flows/policy` | Audit interval/retention changes; `s.destructive`, ordinary reviewed confirmation because pruning can erase records. |
| `DELETE /network/flows/history` | Audit erasure of observations and coverage; `s.destructive`, ordinary confirmation. |

These paths do not enroll in network pending confirmation and never alter links, policy, shaping,
DNS or boot inputs. The server initializes this module after Docker exists, starts its recorder
during startup and drains it before closing Docker. It mounts the admin-only routes within Network.
No auto-reconcile daemon or kernel program is installed. The reporting page is `/network/flows`.

## Measured acceptance and its limits

On 2026-10-08, Go 1.26.8 / linux-amd64, kernel `6.14.0-37-generic` and native
`ss utility, iproute2-6.14.0`, the disposable namespace fixture measured a held TCP socket's
**131,072-byte** counter delta for an exactly sized payload. A held connected UDP peer was observed
with bytes unknown. **32** TCP connections created and closed between snapshots produced **zero**
usable retained identities. Actual dropped-event count remained unknown; the known fixture workload
is not a measurement of observer event drops.

Eight immediate snapshots in this small single-namespace fixture took **658.330 ms** total,
**82.291 ms** mean, **92 ms** maximum capture, and **654,717 µs** combined process/child CPU. That CPU
cost would amortize to **0.2728%** of one CPU at a 30-second interval for this fixture. Process peak
RSS was **12,828 KiB**. A second fixture used two disposable, unpublished Docker containers sharing
one namespace: it sampled the namespace once, attributed **2/2** held TCP descriptors to their
actual container and **1/1** connected UDP descriptor to the other container, taking **462.169 ms**.
These measured fixtures do not establish all-host overhead or complete attribution under source
caps; the benchmark states its workload and keeps omissions visible.

Three-iteration package microbenchmarks on the same host measured a 1,024-socket parse/two-read
identity-delta operation at **12.649 ms**, **8.394 MB allocated**, and a 1,024-row durable SQLite cycle
at **113.671 ms**, **9.013 MB allocated**. They measure package work, independently from native command
and Docker process-inspection costs. Larger hosts must assess their own reported capture time,
source caps and retention before shortening the interval.

A later final-source rerun at 20:43 UTC timed out: the isolated native `ss` read exceeded its
two-second budget, and Docker image inspection exceeded the fixture's 15-second preparation
deadline before creating any fixture containers. The reader returned unavailable instead of byte
counters. The earlier measured results remain evidence for their declared fixture; acceptance
under the later shared-host load is pending. No successful final live replay is inferred from
static or race checks.

Run targeted tests and declared benchmarks:

```bash
cd backend
GOMAXPROCS=2 go test ./internal/netflows -count=1
GOMAXPROCS=2 go test ./internal/netflows -run '^$' -bench 'Benchmark(NativeSnapshot|DurableSocket)' -benchtime=3x -benchmem
GOMAXPROCS=2 go test ./internal/api -run '^TestFlowHistory' -count=1
GOMAXPROCS=2 CGO_ENABLED=0 go test -c -o /tmp/jd-netflows-native-fixture.test ./internal/netflows
sudo env JD_NETFLOWS_LIVE=1 GOMAXPROCS=2 /tmp/jd-netflows-native-fixture.test -test.run '^TestLive(NativeTCPUDP|DockerAttribution)' -test.v
```

The live fixtures require root/iproute2 and a locally cached `python:3.11-slim` image for the Docker
case. They create and remove exact uniquely named namespaces/containers, publish no ports and do not
change existing workloads or the host's routing/firewall. Narrow unit tests cover default-off zero
native calls, opt-out while a sample is in flight, missing/reset/gapped/hour-crossing counters,
owner changes, exact JSON counters, PID/FD/container revalidation, output caps/cancellation, durable
reopen/filtering, storage failure, retention/payload pruning, capability/token restrictions and audit.
Frontend pure logic tests cover nullable counters, UTC boundaries and exact arithmetic. Browser
specs cover opt-in, unknown history/UDP, stale readings, filters, retention review, role privacy and
mobile containment; a source-matched production build/browser run is required at integration.

No real reboot or power-loss acceptance is claimed for this observation baseline. No optional
kernel observer, UDP byte measurement, complete short-lived coverage, event-drop proof or billing
accounting is accepted by these fixtures.
