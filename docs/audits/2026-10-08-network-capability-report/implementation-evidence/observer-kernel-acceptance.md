# Kernel observer acceptance

The fixed observer digest is
`055fae72bf5671b0f332b549bde5d55e8482d780a1699cc8a036160bfc553529`.
This implementation extends the retained socket baseline; P8 remains in progress until its remaining
frontend and integrated acceptance are complete. Native attribution and backend-death acceptance
below passed on the unchanged product implementation.

The final-source opted-in race binary passed all six observer fixtures on the native Linux host:
[raw native output](observer-kernel-native-race.txt). Fixtures used exact owned cgroups and network
namespaces, removed them, and did not attach production cgroups or replace a foreign program.
A private execution cgroup namespace initially made fixture migration fail with `ENOENT`; the
accepted command entered only the host cgroup namespace, as documented in
[the implementation contract](../../../internal/backend/network-flow-observer.md).

The storage/export fixture retained 132 distinct rows, including all 32 short TCP sender FINs,
32 UDP sender rows and 32 UDP receiver rows. Each direction's observed UDP payload subtotal was
8,224 bytes, matching 32 known 257-byte datagrams. The exported report was 180,180 bytes. Its quality
retained 365 emitted events, 99 identity gaps, 365 unknown owner observations and three reader budget
pauses; ring drops, budget omissions, byte gaps and pending omissions were zero in that fixture.
The non-Docker fixture does not verify container or process attribution.

Two additional opted-in fixtures passed in the final race binary:
[raw Docker and backend-death output](observer-docker-death-native-race.txt). The backend SIGKILL
fixture completed in 0.05 s: all five unpinned owned link IDs disappeared while its separately owned
foreign ingress link and program remained. Killing the backend does not prove that its uncommitted
shutdown tail was complete.

The Docker fixture completed in 10.34 s with two disposable containers sharing one network namespace.
All 32 UDP sender rows matched the actual owning container's cgroup and running instance; observations
were restricted to that owned container cgroup. After removal and recreation under the same name in
the still-shared namespace, all 32 old pending sender rows remained unknown, and none borrowed the
replacement container identity. SQLite history filtered by the replacement ID contained zero rows;
the original ID retained 64 verified rows. This establishes instance-safe cgroup attribution for that
workload, not general process identity or every Docker mode. The fixture used a cached image, read-only
binary and containers limited to 128 MiB, 0.5 CPU and 64 processes with all capabilities dropped.
Both fixtures completed owned cleanup; no named test containers or fixture cgroups remained.

The separate 4 KiB ring saturation fixture reported 31 delivered events, 4,969 ring drops,
15,256 deliberate budget omissions and 20,000 preexisting-socket identity gaps. Its 10,000-datagram
workload measured 100.61 ms without the observer, 118.54 ms on the missing-identity path and
169.25 ms with the saturated observer (ratio 1.682). An earlier run measured ratio 1.157. These
are workload-specific wall times on a shared host and a race-enabled fixture; they do not establish
a universal low-overhead threshold or packet-delivery total.

Native checks also verified a foreign ingress program survived a refused owned detach and a retry,
and nested BPF info/stat reads survived concurrent garbage collection. The real verifier accepted
all five fixed programs. Unit/race tests exercise the assembled budget CAS with a delayed old epoch
and a competing newer epoch, byte subtotals with per-direction unknown packet counts, clock changes,
and retryable retained link ownership.

Recorder-to-SQLite tests use an actual abort trigger to fail the transaction after row writes. The
immutable batch and quality baseline survive until a later successful transaction; new events stay
separate and bounded. A lost acknowledgement after commit is retried against the durable receipt,
proving packet/byte/coverage counts are not repeated. History address filters and exact JSON decimal
exports preserve the separated peers. Restart clears dead-process attachments and records an
interrupted session without loading programs.

`scripts/test-changed.sh 65516a69` passed on the final code:
[raw targeted output](observer-kernel-changed-checks.txt). It ran `go build ./...`, changed-package vet,
API tests (0.552 s) and the netflows package (5.908 s). The final focused race run passed in 13.051 s;
the earlier full netflows race run passed in 45.931 s, and focused observer API race checks passed in
4.741 s. No frontend source changed in this backend commit. The parent integration owns the new
observer controls, separate native/kernel totals and current-head mounted/browser checks.

The ordinary history-stop route now refuses active/retained observer ownership and pending batches;
operators must use the separately destructive observer-stop route first. This preserves the tighter
mutation budget. A focused API race regression passed in 3.851 s, and the follow-up changed checks
passed (API 0.527 s, netflows 4.689 s): [raw output](observer-stop-prerequisite-checks.txt). The parent
integration reuses `assertFlowObserverExplicitStop` on `Server.Routes` for mounted-surface acceptance.

Documentation review covered the complete backend diff against internal docs, `AGENTS.md`,
`README.md` and `CONTRIBUTING.md`. Affected operator, lifecycle, network, contributor and observer
documents were updated. `AGENTS.md` needs no change. No dependencies, release notes, licence headers
or CI were changed.

The Docker/death test follow-up updates this acceptance record, the observer contract and contributor
prerequisites. Operator behavior is unchanged, so `README.md`, `AGENTS.md` and the remaining internal
documents need no further update. Its final changed-file checks are recorded in
[the focused output](observer-docker-death-changed-checks.txt).
