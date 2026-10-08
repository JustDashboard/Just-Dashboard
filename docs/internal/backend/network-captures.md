# Bounded packet captures

`internal/netcapture` owns explicitly launched private PCAP jobs. `/network/captures` is a reading
page: retained captures, their dated progress and results, original artifact access and an explicit
setup dialog. The existing quick packet summary remains in Tools and does not retain a PCAP.

## Request and native scope

The closed request selects one native interface, `inet`/`inet6`, a protocol (`all`, `tcp`, `udp`,
`icmp` or `icmp6`), optional canonical literal source/destination and a TCP/UDP port. Names,
expressions, zones, mapped addresses, CIDR ranges, files, commands and arbitrary flags are refused.
The server constructs the complete BPF expression from these values. IPv6 extension headers and
VLAN encapsulation are subject to the native filter's supported behavior, not a complete path model.

Packet count is 1–10,000; duration is 1–120 seconds; artifact bytes are capped at 2 MiB; snapshot
length is explicitly 96, 128, 256 or 512 bytes. The byte budget must fit a header and one full
snapshot record. Capture uses fixed `tcpdump -nn -p -U -s … -c … -i … -w -` argv through
`hostexec.CommandOnHost`, wrapped in host `timeout --signal=TERM --kill-after=2s`. No file path
comes from the client or reaches the native tool. Missing `ip`, `tcpdump` or `timeout` is explicit
unavailability. No dependency is installed by the capture route.

The host timeout is independent of the backend's lifetime. Normal cancellation also stops the
entire command group through `hostexec.RunGroup`; final native cleanup is recorded only after it
returns. An abrupt backend exit does not prove final cleanup, but the surviving host timeout bounds
its capture to the selected duration plus two seconds. This is separate from a network change's
recovery watchdog: capture changes no network configuration and creates no listener.

Native interface index/name/address/parent/type/kind are compared before and after. A changed or
unreadable final identity produces a failed result and retained uncertainty. The endpoints of this
comparison cannot prove that a transient replacement never occurred between observations.

## PCAP and lifecycle

The bounded streaming writer accepts classic PCAP 2.4 with microsecond/nanosecond timestamps in
either byte order. It verifies global headers, snapshot lengths, packet lengths, timestamps and
reserved link flags; it does not decode application packets. Only complete packet records are
retained. A byte/packet limit stops the process; an incomplete trailing record is omitted and
disclosed. SHA-256 and structural validation run again before artifact download. Native stderr is
bounded at 16 KiB, and the kernel drop count is unknown unless tcpdump actually reports it.

The format/argv contract follows the primary [libpcap savefile documentation](https://github.com/the-tcpdump-group/libpcap/blob/master/pcap-savefile.manfile.in),
[tcpdump manual](https://github.com/the-tcpdump-group/tcpdump/blob/master/tcpdump.1.in) and
[native filter manual](https://github.com/the-tcpdump-group/libpcap/blob/master/pcap-filter.manmisc.in).
New formats and unrestricted filter features require an explicit adapter extension.

Two captures may run concurrently. Initial name/request/actor/lifecycle are durable before job
launch. Names and requests are immutable. Provisional observed packet/byte progress is recorded at
most once per second, with its timestamp; a telemetry failure keeps the last dated progress.
Cancellation stays `cancelling` until process cleanup returns. Terminal recording failures retain
their bounded result in memory and block new launches until a read/launch successfully retries the
same write; retries never recapture. A restart marks unfinished rows interrupted, without replay.
An unsaved result lost at restart is not presented as an available artifact.

The additive `network_packet_captures` table stores bounded JSON metadata and a BLOB, not paths into
the host filesystem. Existing installs gain this table on open without changing shipped migrations.
At most 32 rows are retained, including active captures; at most 64 MiB of original packet data is
logically retained. Terminal rows expire after 24 hours. Reads, writes and startup prune them;
active rows are preserved. SQLite/WAL file sizes can exceed the logical retained payload size.

## Access, incident reference and export

Every capture route requires `system.admin`, including interface choices, metadata, PCAP, support
export and cancellation. Generic `network.capture.*` job list/get/stream/cancel apply the same gate.
Deletion additionally uses `s.destructive`. Launch/cancel/delete are audited; successful private
downloads/support exports use `httpx.AuditRead`. Responses have `private, no-store`; original PCAP
downloads have a generated ID-only filename, explicit media type and `nosniff`.

The optional `incidentRunId` must identify a currently readable saved diagnostic at launch. The UI
links that run and compares its collection timestamp with the capture's. It never reruns the
diagnostic, assumes they contain the same flow, or infers causation from their proximity. An expired
reference remains visibly absent/unreadable.

Original PCAP contains real packet bytes, possibly application data or credentials, and is not
claimed to be redacted. The separate support JSON omits packet bytes, filter tuple, native text,
interface, names and actor. It retains bounded lifecycle/cap/result facts and genuine unknowns.
The create dialog explicitly describes original packet retention; refused launches preserve every
input. Failed later polls retain dated readings with retry. Read accounts request no capture data.

## Verification

`netcapture/capture_test.go` covers closed argv, hard caps, endian/timestamp formats, chunk boundaries,
corruption, durable reopen, retention, interrupted startup without traffic, pending cleanup and
failed final recording without recapture, provisional progress and old active rows within the total
retention cap. The API test covers actual routed role gates, private
download/support redaction, audit, immutable incident identity, and private generic-job access.

```sh
cd backend
go test -race ./internal/netcapture
go test -race ./internal/api -run TestCaptureAPI -count=1
JD_NETCAPTURE_LIVE=1 go test -race ./internal/netcapture -run Live -count=1 -v
```

The live fixture needs root or passwordless sudo and installed native tools. It reexecutes only its
own selected test as root when needed, creates a uniquely named disposable namespace, sends known
loopback UDP traffic in both families and verifies native PCAP bytes/caps, quiet timeout and
cancellation cleanup. Its backend-death fixture kills only its own applying process after
observing tcpdump, proves the capture survives briefly and then verifies the independent timeout
removes every PID from that namespace. It removes only that namespace. It does not capture production interfaces,
start public listeners or change host networking. Offload, provider paths, encrypted application
contents, long-running production overhead and real provider acceptance remain separate.
