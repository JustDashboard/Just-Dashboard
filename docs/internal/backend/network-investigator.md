# Connection path investigator

The administrator-only `/network/investigate` page explains one source, destination, address family,
protocol and port. `GET /api/v1/network/investigate/sources` supplies running-container names and full
inventory IDs. `POST /api/v1/network/investigate` validates a closed typed tuple, audits
`network.path.investigate`, and collects bounded evidence for DNS → policy rules → kernel route →
firewall → NAT → tunnel/interface → proxy → destination owner → optional TCP connection measurement.
Both routes require `system.admin`, including inventory reads. Existing allowlist, authentication,
second-factor and session middleware remain ahead of these routes.

`internal/netpath` composes existing owners rather than interpreting another packet engine. Each
evidence item carries its owner, scope, collection time, facts, limitations and one of four bases:

- **Observed:** a native snapshot or kernel route decision; no packet traversal is implied.
- **Modeled:** supported selectors in the native UFW adapter or configured proxy inventory. Other
  hooks, state, foreign rules and provider policy remain unknown.
- **Measured:** a native DNS response or an explicitly requested TCP handshake from the selected
  source at that time. TCP success does not establish TLS, login, HTTP, UDP or inbound reachability.
- **Unknown:** unavailable, foreign or unsupported evidence, including the container's second host
  bridge/FORWARD/NAT leg and remote ownership. A failure does not identify a blocking layer.

The default is an explanation without a TCP probe. Native DNS may send a query to resolve a name.
Host DNS follows the effective policy through `netx.LookupWithOptions`; unavailable private resolver
scope never falls back to a public preset. Container DNS reads the freshly verified process root's
`etc/resolv.conf` and queries only its configured literal nameservers inside the selected namespace,
including Docker's embedded resolver. It uses absolute A/AAAA wire queries and reports that full
NSS/hosts/search behavior and encrypted transport are not represented. A native negative answer ends
the query; it does not trigger another resolver. An unavailable/unrepresentable chain remains unknown.

At most eight selected-family unicast DNS candidates are retained. The chosen literal address is used
for both the `ip -j route get` query and the probe. A previously chosen address must still exist in the
fresh native answer, or later queries are skipped. The kernel request includes the optional source
address and mark, protocol and destination port. Rules remain ordered candidates rather than a
synthetic per-rule trace; UID, source port and unsupported rule selectors are disclosed. UDP and
marked tuples can be explained, but the TCP adapter cannot measure them and sends no substitute.

Container execution accepts a full running inventory ID; HTTP requests cannot supply a PID,
executable, filesystem path or argv. Fresh Docker PID/start-time, process start ticks and exact cgroup
attribution are checked before each namespace operation. A verified network namespace file descriptor
is pinned and passed to `nsenter`, so PID reuse after validation cannot select another namespace.
Unreadable or unattributable process metadata fails closed without a host-source substitute. Only
network metadata is returned; environment variables and mounts are excluded. Commands use `hostexec`
with closed `ip`, `dig` and `nc` argv, bounded output and process-group cancellation. The whole report
has a 30-second deadline; individual container tools have an eight-second bound.

The UI uses the report register: a small tuple form, original scope and timestamp, separate basis
counts, named evidence and owner links. A failed later request preserves the draft and previous report
with its original scope. Reports are current responses, not durable diagnostic runs; the diagnostics
runner owns persistence, cancellation, reruns and comparisons separately.

Focused checks are beside `netpath`, `dockerx`, `netsec`, `netx` and the API handler; pure request/basis
tests live in `frontend/src/lib/network-investigator.test.js`. The browser contract is
`tests/browser/network-investigator.spec.ts`, run against a fresh combined build. The native container
fixture is opt-in and uses an existing local `python:3.11-slim` image; it creates one labeled container
with `network=none`, native loopback DNS/TCP listeners, and no published ports. Compile it as the normal
contributor, then run the binary as root to inspect its process root and pinned namespace:

```sh
cd backend
go test -c ./internal/netpath -o /tmp/jd-netpath-investigator.test
sudo -n env JD_NETPATH_LIVE=1 /tmp/jd-netpath-investigator.test \
  -test.run '^TestLiveContainerPath' -test.v -test.timeout=60s
```

The fixture verifies native resolver selection, literal destination/source agreement, real TCP
connection evidence, retained host/provider unknowns, and stale-identity rejection after a restart.
It removes only its own container ID. It does not pull an image or alter the production host network.

## Native streams on the path

A local listener the proxy inventory attributes to a native nginx stream (`Listener.Stream`) joins
that stream as its own evidence item (`stream`, `netpath/stream.go`), read through
`proxysvc.StreamPath`: the configured forward (protocol, listens, backends with backup/down roles,
balancing, where nginx ends TLS) and its access list as a sentence, the stream's state as the Streams
page reads it, the client sessions established to its ports now, the TCP connections nginx holds open
to each backend now (`streamConnections`: the host's ESTABLISHED sockets owned by nginx, or by an
owner this account cannot read, whose far end is a backend's address or an address its name resolves
to), and, for a stream that logs its sessions, the last hour by the backend each session ended on —
completed and 5xx — with the log's completeness. It is **observed**: none of it is this tuple's
traversal, and the limitations say so. A UDP stream has no per-session socket and says it cannot be
counted.

When the request measures a TCP connection and it connects, the investigator reads the session that
connection left in the stream's log (`proxysvc.AwaitStreamSession`, up to 1.5 s: nginx writes a
session's line when it closes) by client address and time, since nginx logs no client port, and adds a
**measured** `stream_traversal` item: `forwarded` to the backend nginx names last in
`$upstream_addr`, `denied` (403, the access list), `failed` (5xx, no backend took it) or `logged` with
another status; earlier backends a retried session tried are listed. Without a measurement, after one
that did not connect, or with nothing logged in time, no backend leg is claimed. Both items link to the
stream (`/proxy/streams?stream=<name>`). The Streams page's **Trace in Network** verb (TCP streams,
administrators) opens `/network/investigate?target=&port=&protocol=&family=&measure=` on the stream's
own address (loopback for a wildcard), and the investigator takes only values its form could have
produced (`pathDraftFromQuery`). `GET /proxy/streams/{name}/path` serves the same reading to any
signed-in account, as the stream's sessions and traffic are. Tests: `TestStreamPathJoinsConfigurationSocketsAndLog`,
`TestStreamPathSaysWhatItCannotCount`, `TestAwaitStreamSessionFindsTheMeasuredConnection`,
`TestStreamListenerJoinsTheStreamAndItsBackendLeg`, `TestStreamBackendLegIsOnlyClaimedFromTheLog`, and
`TestLiveStreamPathReadsNginxsBackendLeg`, where the host's nginx binary loads the host's stream
module on a private prefix, forwards a loopback port to a backend the test holds, and the path counts
the open client session and nginx's backend connection and then finds the closed session, 200 to that
backend, in the stream's log.

## Retaining a scoped report

**Run and save** snapshots the same request into the [retained diagnostics lifecycle](network-diagnostics.md#retained-connection-investigations).
The named report preserves individual layer evidence and exact source/family/protocol/port/mark,
uses the same bounded native adapter and does not add a watcher or replay after restart. Rerun is
explicit and reacquires fresh source identity; comparison refuses different requested tuples.
Saving report completion never promotes an unknown layer into measured connectivity.
