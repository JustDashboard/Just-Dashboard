# Bounded kernel flow observations

`internal/netflows` combines the retained native socket baseline with an independently opted-in
kernel observer. Socket history and export remain `system.admin` reads with `private, no-store`
responses. `POST /network/flows/observer` takes only an explicit `enabled` boolean, passes through
`s.destructive`, and records `network.flow.observer` in the audit log. Reading a report, enabling
ordinary history, and changing retention never attach programs. History must already be enabled.
An ordinary history stop is refused while the kernel observer or retained owned attachments exist;
the explicit destructive observer stop must succeed first. Clearing history also stops the observer
through its destructive route. The observer does not automatically reattach after restart: startup records an interrupted session
and requires a new explicit opt-in.

## Fixed programs and ownership

The Go assembler contains five packaged programs: cgroup socket create/release, TCP established
socket operations, and always-allowing cgroup ingress/egress header observations. There is no supplied
ELF, program source, map path, target path, helper ID or instruction input. Both packet programs always
return allow; no instruction writes packet data or its context. The assembler uses the Linux UAPI
layouts for `__sk_buff`, `bpf_sock`, `bpf_sock_ops` and cgroup links, and a fixed helper vocabulary:
map lookup/update/delete, monotonic time, socket and namespace cookies, current/socket cgroup IDs,
header-only `skb_load_bytes`, and ring output. The reported SHA-256 digest covers all five instruction
streams with map relocations replaced by zero. Exact native verification, not just a version number,
determines whether attachment is available.

The implementation supports Linux amd64/arm64, cgroup v2 and Linux 6.1 or newer, with
`CAP_NET_ADMIN` and `CAP_BPF` or `CAP_SYS_ADMIN`. Missing privileges, unsupported helpers, a refused
verifier, unreadable boot identity, or unsupported multi-link attachment produce dated unavailability.
The host cgroup root is opened beneath the trusted host root with `openat2` and no symlinks. Cgroup
creation stamps socket namespace cookies because that helper is unavailable in cgroup packet hooks;
accepted TCP children need the established callback. Preexisting sockets, handshake packets before a
stamp, evicted stamps and time-wait packets remain explicit identity gaps. TCP release retains only a
bounded LRU stamp to observe late FIN/RST; it does not infer a successful remote close.

Four bounded maps belong to one session: a 1 MiB ring, six quality counters, one shared attempt budget,
and 4,096 LRU identities. The shared CAS budget admits at most 5,000 event attempts per monotonic
second; three bounded CAS attempts cap contention work. A delayed packet cannot move its epoch
backward or reopen an exhausted newer allowance. Header inspection still occurs for omitted packets;
the event budget is not a promise of zero kernel CPU cost under a flood. The reader pauses 20 ms after
5 ms of work and uses a shared maximum of 2,048 pending and unacknowledged rows.

Only unpinned, owned link descriptors are detached. Before close, the link's type, kernel link ID,
program ID, cgroup handle and attach type must match. Failed verification retains the remaining links,
programs, maps and mappings for retry and refuses another observer session. Successful descriptor
closes are retired immediately; Linux close errors do not cause a later close of a reused integer.
Constructor cleanup, explicit stop and service state all preserve incomplete ownership. Programs
attached by another owner are never found by name, replaced, detached by target, or closed. Process
exit releases unpinned descriptors; this means stop evidence and shutdown tail completeness remain
unknown after backend death.

The syscall attributes contain nested integer addresses. Instruction, license, verifier-log, info,
map-key and map-value buffers are pinned with `runtime.Pinner` across each relevant BPF syscall;
`KeepAlive` alone does not prevent relocation of a referenced Go stack value.

## What a row proves

An event contains only fixed IP and TCP/UDP header bytes, socket/cgroup/namespace cookies,
monotonic time, length, GSO count and direction. It contains no payload, executable data or credentials.
IPv4 fragments, IPv6 extension chains and other unsupported headers are quality gaps. GSO or
inconsistent native header lengths leave payload bytes unknown. Kernel transport observations include
retransmissions and do not prove application bytes, delivery, NAT identity or end-to-end loss.

Kernel identity is separate from the inode-bearing snapshot identity. Its hash includes boot ID,
socket cookie, namespace cookie, cgroup handle, protocol and both complete endpoints. One unconnected
UDP socket contacting several peers produces several rows; unrelated cookies/namespaces/cgroups do
not merge simply because a packet event lacks a procfs inode. A changed proven owner creates a
separate row identity.

`observedTxBytes`/`observedRxBytes` are known transport payload **subtotals**, expressed as exact
unsigned decimal strings. Each direction also records total observed packets, packets with known
bytes, and byte-gap packets. A mixed known/unknown row never presents its subtotal as the total for
all packets. `observedSyn`/`observedFin`/`observedRst` retain observed flags; duplicates and
retransmissions remain observations. Snapshot counter deltas occupy the original fields and are never
added to kernel subtotals.

Docker attribution is verified only against a fresh running container instance, PID/start identity,
pinned cgroup handle and namespace identity/cookie, with an event no older than the captured binding.
At most 16 container bindings are held. A missing/restarted source, unavailable Docker refresh,
unverified identity or uncertain event time keeps ownership unknown. Reports carry the Docker read's
status, error, check time and omitted-source count. This observer does not turn a cgroup cookie into a
verified process name or PID.

UTC is projected from monotonic time and a wall-clock anchor, checked again at each batch. A failed
clock read, backwards monotonic reading or wall-clock drift over 250 ms marks pending and subsequent
rows `timestampUncertain`, with a reason and timestamp-gap count. Such rows cannot use their projected
wall time to prove container freshness. The implementation does not silently assign a clock step to a
precise UTC hour.

## Commit, retry and quality

A drain creates an immutable batch and retains its baseline until explicit acknowledgement. New
events use a separate map, sharing the same row bound. SQLite writes packet rows, hour coverage,
policy and the last observer receipt in one transaction. A failed write retains the original cycle,
rows, quality and batch; a later sample retries this work before collecting another native baseline.
The durable receipt makes a retry after an uncertain commit or lost acknowledgement idempotent.
Only a successful transaction or a matching committed receipt acknowledges the batch and advances
its quality baseline. An opted-out recording cannot discard a failed final write while appearing
successfully stopped. Shutdown can report a retained write failure; restart does not invent the lost
in-memory observations.

Hour coverage and current observer evidence distinguish delivered events, ring drops, budget
omissions, unsupported headers, missing identities, stamp-admission failures, parser/byte gaps,
pending-row omissions, unverified attribution, reader pauses, unsaved events and unknown shutdown
tails. A later successful transaction retains prior batch quality instead of clearing it with the
service's transient error. Full-ring drops and deliberate budget omissions are separate counters.

## Verification

Targeted `netflows` race tests exercise multiple kernel identities through the recorder, SQLite,
history filters and exact JSON export; trigger-aborted transactions; lost acknowledgements; shared
row capacity; clock changes; per-direction byte gaps; and ownership-preserving detach retries.
The assembled CAS section is executed with a deterministic old/new epoch interleave.

On a native Linux host, from `backend/`, build a race binary in an owned disk-backed artifact directory
and run the opted-in fixtures:

```sh
mkdir -p out/observer-checks
TMPDIR="$PWD/out/observer-checks" GOMAXPROCS=2 go test -race -p 2 -c ./internal/netflows \
  -o out/observer-checks/netflows-observer-race.test
sudo -n nsenter --target 1 --cgroup -- env JD_NETFLOWS_OBSERVER_LIVE=1 GOMAXPROCS=2 \
  TMPDIR="$PWD/out/observer-checks" "$PWD/out/observer-checks/netflows-observer-race.test" \
  -test.run '^TestLive(FixedObserver|Kernel|NestedBPF)' -test.v -test.timeout=3m
```

Entering only the host cgroup namespace avoids a test-session namespace rooted somewhere else while
`/sys/fs/cgroup` exposes the host mount. Fixtures attach only uniquely named disposable cgroups,
pass their pinned directory descriptor to subprocesses, and migrate only those subprocesses. Traffic
runs in disposable network namespaces. The suite verifies real fixed-program acceptance, 32 short TCP
sender FINs, 32 UDP sender/receiver byte differentials through storage/export, foreign-program
preservation through refused detach and retry, actual ring saturation/budget omissions, and nested
syscall buffer reads during concurrent GC. Its UDP timing ratio describes one measured workload and
host; it is not a universal overhead claim. Further live Docker attribution and backend-death coverage
are tracked with the implementation evidence; unknown attribution is not an acceptance substitute.

Primary contracts: [Linux BPF UAPI](https://github.com/torvalds/linux/blob/v6.14/include/uapi/linux/bpf.h),
[ring-buffer ownership and layout](https://docs.kernel.org/bpf/ringbuf.html),
[Go runtime pinning](https://pkg.go.dev/runtime#Pinner).
