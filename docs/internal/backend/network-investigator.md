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

## Retaining a scoped report

**Run and save** snapshots the same request into the [retained diagnostics lifecycle](network-diagnostics.md#retained-connection-investigations).
The named report preserves individual layer evidence and exact source/family/protocol/port/mark,
uses the same bounded native adapter and does not add a watcher or replay after restart. Rerun is
explicit and reacquires fresh source identity; comparison refuses different requested tuples.
Saving report completion never promotes an unknown layer into measured connectivity.

## Inbound path to a published port

`GET /api/v1/docker/containers/{id}/published/{port}?protocol=&family=` (system.admin, the
investigator's capability, because it reads the host's iptables and firewall) assembles the inbound
path to one binding a container publishes, as a `netpath.Result` with vantage `published_port`
(`netpath.InvestigatePublished`, `published.go`). It reads only and sends nothing to the port. The
layers, in order: Docker's publication (observed); the host socket — docker-proxy or a NAT-only
publication — and its proxy routes; Docker's DNAT rule in the nat `DOCKER` chain
(`netsec.ReadDockerChains`, `iptables`/`ip6tables -S` only), compared with the container's current
address (a mismatch says so; an unreadable chain, which is also what an Engine on its nftables backend
looks like, is unknown); `DOCKER-USER`, where any operator rule is listed and left unevaluated; the
forwarded leg — FORWARD's jumps read in order with Docker's filter `DOCKER` chain: where Docker's chains
come before the firewall adapter's and Docker's own rule accepts the container's address and port, the
adapter's route rules and routed default are never reached for it (`docker_admits`); when operator
rules in DOCKER-USER or a foreign chain FORWARD consults first (Tailscale's `ts-forward`, say) come
before that accept, the verdict says it holds unless they drop the connection, which is not evaluated
(`docker_admits_unless_earlier`); otherwise the
adapter's forwarded-traffic model decides, with the note that ufw/iptables inbound rules and default do
not apply to a translated port. The native run on this host first showed the adapter alone predicting
ufw's routed deny for the shared ingress's port 80, which Docker's earlier accept admits; that run is
kept as evidence of why the order is read. Then the dashboard's gateway — an
owned forward on the same port competes, and any other table's forward-hook chain that can drop is
unknown; the configured proxy; provider policy, always unknown, with whether the host has a public
interface address; and retained external measurements that can be about this binding — TCP, its
family, and for a binding on one address only that address (a loopback binding takes none) — labelled
by whether the measured address is on this host. The comparison line keeps those bases apart.

The container page's reachability rows and the ports sheet of a Docker-published socket open it.
`published_test.go`, `docker_chains_test.go` and `docker_published_path_test.go` cover the layers and
the capability; the browser cases are in `docker-ui.spec.ts` and `proxy-ports-reachability.spec.ts`.
The opt-in read-only host check compiles as the contributor and runs as root, so iptables can be
listed as the backend lists them; it sends nothing and changes nothing:

```sh
cd backend
go test -c ./internal/api -o /tmp/jd-api.test
sudo -n env JD_PUBLISHED_PATH_LIVE=1 /tmp/jd-api.test -test.run '^TestLivePublishedPathOnThisHost$' -test.v
```
