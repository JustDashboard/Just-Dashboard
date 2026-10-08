# Kernel observer acceptance

The fixed observer digest is
`055fae72bf5671b0f332b549bde5d55e8482d780a1699cc8a036160bfc553529`.
This implementation extends the retained socket baseline; P8 remains in progress until its remaining
native attribution, process-death, frontend and integrated acceptance are complete.

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

Documentation review covered the complete backend diff against internal docs, `AGENTS.md`,
`README.md` and `CONTRIBUTING.md`. Affected operator, lifecycle, network, contributor and observer
documents were updated. `AGENTS.md` needs no change. No dependencies, release notes, licence headers
or CI were changed.
