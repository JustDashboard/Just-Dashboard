# Explicit download SQM

The Traffic page retains the existing upload queues and download policer. Download SQM is an
explicit choice that redirects a source interface's ingress to a dashboard-owned IFB with CAKE.
Existing installed profiles without `sqm` keep their policing behavior. The BBR switch is unchanged
and does not constitute measured SQM performance evidence.

## Intent and API

`POST /network/shaping/{device}` accepts the existing queue/rate fields and optional `sqm`:

```json
{
  "qdisc": "fq_codel",
  "egressKbit": 0,
  "ingressKbit": 9000,
  "sqm": {
    "diffserv": "besteffort",
    "flowMode": "dual-dsthost",
    "nat": false,
    "preserveDscp": false,
    "overhead": 22,
    "mpu": 64,
    "linkLayer": "noatm",
    "rttMillis": 100
  }
}
```

The download rate must be positive. Supported classes are `besteffort`, `diffserv3`, and `diffserv4`;
fairness is `dual-dsthost`, `triple-isolate`, or `flows`. Link accounting is `noatm`, `atm`, or `ptm`.
Overhead is −64…256 bytes, minimum packet size 0…256 bytes, and expected RTT 10…1000 milliseconds.
Empty choices default to best effort, destination-host fairness, no cell accounting, and 100 ms RTT.
NAT lookup defaults off; DSCP preservation defaults off, so CAKE washes DSCP after classification.
Split GSO is enabled, ACK filtering disabled, ingress accounting enabled, and fwmark overrides are
unsupported. The bounded options correspond to the
[iproute2 CAKE interface](https://raw.githubusercontent.com/iproute2/iproute2/main/man/man8/tc-cake.8).

IFB names, creation MACs, nonce aliases, redirect cookies and source identity are server-derived.
They are saved in the existing root-private network spec and recovery journal, never accepted as
request fields. No database migration, dependency, new service setting or production environment
variable is introduced.

Setting a profile needs `system.admin`; clearing it remains `destructive` with ordinary confirmation.
Both successful and failed set/clear mutations are audited. Every SQM mutation, including removal,
requires the existing interactive-session pending protocol: `X-JD-Network-Apply: pending`, an
independent host watchdog and a subsequent fresh response/explicit reconnect confirmation. The
frontend requests pending mode explicitly for SQM even if the optional global pending preference is
off. Unsupported recovery or an unverified helper refuses before IFB/redirect effects.

An absent `sqm` continues to mean policing. A legacy owned policer can become SQM after its ownership
is checked. SQM can become policing only when its ingress hook was created here; a native owner's
clsact/plain ingress hook is preserved and cannot be replaced with a policer. Clearing or removing
the download limit preserves such a native hook. Internal IFBs cannot be independently shaped from
the table; edit their source profile.

## Ownership, verification and recovery

Before provisioning, updates and removal, the source MAC/kind/MTU, native hook, redirect selectors,
action/cookie/target and IFB identity/queues/classes/filters are read. Failed or malformed inventories
remain unreadable; they never prove absence. Existing IFB name or alias patterns cannot authorize
adoption. A replacement source, conflicting ingress filter, replaced IFB, foreign IFB queue/filter/
class or unsupported root baseline causes an actionable refusal through the native owner.

An IFB name uses one portion of a random nonce; a different portion supplies a locally administered
MAC in the atomic link-add request. Alias installation during link creation was not observed in the
native fixture, so the full nonce alias is set and verified separately. Only bounded private recovery may accept an incomplete alias
when the saved creation MAC/name/kind/MTU and supported default queue prove partial creation. The
normal path requires full alias provenance. New redirects use `add`, so a competing filter at the
reserved selector is not replaced. Existing verified redirects remain while an owned IFB is held
down and its CAKE queue recreated, because CAKE/matchall cannot replace themselves on all kernels.

Native verification checks exact CAKE bandwidth, classes, flow mode, NAT/wash, overhead, MPU,
link accounting, expected RTT, ingress, split GSO, ACK filtering and fwmark; it also checks IFB up
state and redirect ownership. CAKE's own virtual classes are supported, in JSON and in the text
form iproute2 6.1 (Ubuntu 24.04) prints for `tc -j class show`, including its empty output when
the IFB has no flows yet. Before that was read, every Ubuntu 24.04 apply was refused as "foreign
classes" and its synchronous and independent recovery stayed degraded on the same check. iproute2 intentionally omits
zero MPU from its printed options; absent MPU is zero only for that documented case, with all other
required profile evidence present. See the
[iproute2 printer](https://raw.githubusercontent.com/iproute2/iproute2/main/tc/q_cake.c).

The typed `sqm` recovery command is one strictly decoded JSON argument, bounded to 4096 bytes, with
a version, source and optional prior/candidate owned profiles. Unknown fields, extra arguments,
trailing JSON, invalid/crossed identities or arbitrary argv/path vocabulary are refused before any
file restoration. Only ordinary change journals may dispatch it; selected drift repair must refuse
it. Ordinary legacy shaping retains its existing undo commands.

The journal and independent watchdog are prepared before effects. SQM undo removes the exact
redirect first, then the owned IFB queue/link, then only a dashboard-created empty ingress hook;
it restores the prior owned SQM profile and the transaction restores the original upload root/
supported baseline and boot files. Existing clsact and its egress filters survive. Foreign or
unreadable structures are preserved and failures leave explicit degraded evidence. Supported
fq_codel baseline timing has the existing tc tick↔microsecond round-trip quantization (at most one
microsecond in the native acceptance fixture); its other parameters are preserved exactly.

## Packaged helper and boot evidence

The installed `network-recovery` executable must answer `--network-sqm-check` with `sqm-v1` before
an explicit SQM mutation. It is the existing packaged server executable and runs in the host's
namespaces. `--network-sqm-restore <absolute-network-directory>` restores only explicit saved SQM
profiles, recreates absent owned IFBs, and refuses foreign collisions or changed native resources.
Failed fresh restoration attempts remove their own partial effects when ownership remains known.

The generated network unit invokes the helper after its ordinary links/rules/shaping/gateway/sysctl
restoration. The SQM command is not failure-ignored: a failure is visible as a failed unit, while
earlier unrelated restoration steps have already run. Explicit apply also reads the loaded unit's
fragment, drop-ins, helper command and enablement; a missing/replaced/overridden dependency triggers
transaction recovery. Restoring a failed candidate boot file reloads the prior unit. Legacy-only
specs acquire no SQM boot dependency.

`GET /network/shaping` remains available to read users. Each source's optional `sqm` response contains
only profile intent, IFB name/queue counters and separate host-helper/loaded-boot-command evidence.
Kernel matching means the configured local queue/redirect matches; helper matching means the
packaged helper answered; boot-command matching means loaded command/enablement readback. None
proves a reboot, provider acceptance, capacity or application latency. Later failed polls retain the
dated last successful reading and block shaping mutations until refresh succeeds. Failed applies
retain the complete editable draft.

## Focused validation

Run changed-file checks against the task base, with the browser server built from the integrated
source. Use Bun only; `network-sqm.spec.ts` covers read roles, bounded payload/pending mode, 409/500
draft retention, failed later polls/retry, validation and phone layout. Pure profile validation is in
`sqm-profile.test.js`.

For the disposable namespace lane, from `backend/` use a task-owned workspace temporary directory:

```sh
mkdir -p "$PWD/.test-tmp"
JD_NETNS_LIVE=1 TMPDIR="$PWD/.test-tmp" GOMAXPROCS=2 \
  go test -race ./internal/netx -run 'TestLiveSQM' -count=1 -v
```

The lane checks real ownership/parameter drift, prepared/runtime/cold recovery, incomplete link
creation, an independent process restoring after backend termination, fresh-response confirmation,
and rollback after a failed owned update's boot readback. It checks a supported fq_codel baseline
and preservation of native clsact egress. It never changes physical host interfaces or host units.
Service/watchdog plumbing is simulated inside the namespace fixture; the separate recovery process
runs actual ip/tc. Actual host systemd scheduling and a physical reboot remain separate acceptance.

The congestion fixture declares a 10 Mbit/s HTB/256000-byte FIFO access bottleneck, four saturated
TCP downloads with DSCP 0/8/46/34, plus a 46-marked UDP echo, two-second warmup and five-second sample
windows. It compares the 10 Mbit/s legacy policer with 9 Mbit/s IFB CAKE, records per-flow bytes,
throughput, latency median/p95 and sample counts, native IFB counters, and measured post-classification
DSCP wash. It asserts valid load/ownership/accounting, not a universal latency improvement. NIC
offloads, arbitrary foreign ingress filters, actual provider queues, reboot and production workload
performance remain outside that disposable fixture's evidence.
