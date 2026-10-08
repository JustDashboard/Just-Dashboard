# Local server advisor

Health and Attention use local measurements and deterministic rules only: no model, API key, paid service or
outbound AI request. The existing capability, audit, containment and confirmation boundaries apply.

## Finding coverage

Health, Attention and their related finding surfaces combine measured evidence, investigation and
reviewed controls. Existing owner
workflows remain the action boundary for features that already have editors or recovery controls:

| Area | Evidence and actions to verify |
| --- | --- |
| Disk capacity and inodes | Largest directories by allocated space and entry count, large files, exact duplicates, old temporary files, partial scans, selected cleanup and post-action measurement |
| CPU pressure, load and I/O wait | Current process attribution and ownership, inspect, lower priority, stop and recovery verification |
| Memory, swap and reclaim pressure | Actual resident/swap consumers, history, limits or selected workload control; avoid claiming occupancy proves thrashing |
| Handles, sockets and network | Owning processes/connections, cumulative versus current counters, targeted diagnostics and applicable controls |
| Steal and temperatures | Measured limits, explicit external remedies where a local action cannot address the cause |
| Service and container runtime | Named failed checks/services, logs, start/resume/restart and applicable configuration remedies |
| Docker attention | Consistent handlers on overview/list/detail, verified cleanup scope, durable Compose changes, limits, checks, images, exposure, expected privileges and working dismissals |
| Security findings | Capability-dependent remedies and owner-directed investigation for every check |
| Proxy/TLS findings | Tested remedies, explicit external limits, fresh reads after changes and consistent triage |
| Deployment findings | Owner evidence, relevant deployment/recovery controls and unavailable evidence |
| Backup findings | Logs, rerun, destination/configuration remedies and coverage actions |
| Database findings/advisor | Connection, exposure, backups and reviewed engine remedies with existing SQL authorization |
| Shared experience | Clear actions and outcomes, stale-evidence handling, unavailable sources, keyboard/mobile access and established reading-register design |
| Linux support | Native filesystem and procfs investigation without GNU-only command assumptions; capability detection and tests across distribution families including Ubuntu/Mint |
| Delivery | Relevant unit/API/race/live/browser checks, documentation review, task-branch commits, push and PR into the active branch |

## Implemented host investigation

All advisor measurements use local Go/Linux reads. No model endpoint, model dependency, API key or
background AI call exists. `HealthPanel` maps each host finding to a bounded investigation with its
fix in the same sheet (`components/metrics/health-investigation.tsx`, `health-fixes.tsx`,
`storage-advisor.tsx`, `workload-advisor.tsx`) or to an explicit provider/hardware remedy. A failed
unit shows systemd's result, when it failed and its last journal lines, with Restart (destructive;
a unit that hit its start limit has the failure cleared first, since systemd refuses the start
otherwise), Clear failure (service.control) and Disable at boot (system.admin); a named container
shows its state, error and last log lines with Start, Resume or Restart for that state. Each control
reads the unit or container again and says whether it is running or failed again. The advisor does not infer that an old file or an
unknown process is unnecessary. The operator reviews named targets and confirms interrupting or
removal actions; successful actions refresh evidence and health, and partial failures remain visible.

### Storage

`GET /system/advisor/storage?path=<directory>&duplicates=true` validates the path through `files.Resolve`
and traverses an `os.Root` handle anchored under a configured root before opening the requested
subdirectory. In the shipped Compose deployment, `/host` is verified against host PID 1
(the backend image has a different root). A host scan uses that mount and reports `requestedPath`
and actual inspector/cleanup paths. A configured same-path host bind may be used directly; an alias
outside `JD_FILE_ROOTS` is refused instead of widening permission. Cleanup and retained-copy directories use the same anchor, so a parent swapped after
path validation cannot escape the configured roots. It reports allocated blocks (sparse lengths are separate), large
files, directory totals and entry counts, and current filesystem available bytes/free inodes. Hard-linked
inodes are counted once for allocation; symlinks/special files and other mount boundaries are skipped.
Kernel pseudo-filesystems cannot be scanned for cleanup. Limits: two simultaneous scans, 200,000 entries,
64 levels and a 20-second request deadline; unfinished scans and unread paths are explicit. Nested folder
totals include children and cannot be added. Entry counts include directory entries, not unique inodes.

Copy checking is opt-in. Up to 2048 retained large-file candidates, 1–128 MiB each, are grouped only by
a complete SHA-256 checksum with a 256 MiB read budget. Hard-linked files are excluded from removable
copies. The report states its scope and incomplete coverage; equal names or sizes never prove a copy.
Temporary candidates are singly-linked regular files beneath `/tmp` or `/var/tmp` modified at least
seven days ago; age is evidence for review, not evidence that an application no longer needs a path.

`POST /system/advisor/storage/cleanup` requires **file.write and destructive**, is rate-limited and
records each selected outcome in the audit log. A batch selects 1–50 measured files. Only the two closed
candidate kinds are accepted: old temporary files or exact copies with a measured retained copy. Every
path passes `ResolveEntry`; the server refuses root entries, hard links, changed fingerprints, unreadable
keepers and detected open descriptors. An unavailable descriptor check fails closed. Descriptor checking
is a point-in-time guard: a mapped file can lack a descriptor and another process can open a file later.
The UI states these limits and permanent removal explicitly.

Cleanup requests are serialized so two requests cannot remove each other's retained copies. A private
sibling staging directory pins the selected entry, verifies its inode/size/mtime/mode after rename and
rechecks the keeper before unlinking. On a stale selection, restoration never overwrites a new occupant;
an entry that cannot be restored is retained with its staging path reported. Reported unlinked allocation
is not a promise of immediately available disk space; the subsequent scan reads actual filesystem space.

### Workloads and other findings

`GET /system/advisor/workloads` returns the top 20 CPU/RSS/swap/I/O/descriptor consumers with native
ownership, start identity and unavailable readings. A cold request takes two snapshots a second apart.
CPU reports its measured interval; 100% is one core. Shared resident pages must not be added, idle swapped
pages do not establish thrashing, and blocked tasks can show low throughput on a slow disk.

Beside them it returns `groups`: the eight heaviest **workloads** by the same measure, over every
process rather than the twenty listed, grouped by `procs.GroupKey` — a supervisor's processes are its
unit, PM2 application or container; anything started by hand is grouped by program, so forty Chromium
renderers are one row. Each group carries its count, summed CPU, memory (private pages plus the
group's shared pages once), swap, I/O rate and — for the handles measure — descriptors, its users, its
heaviest member's PID and command line, and its members as `{pid, createTime}`, heaviest first and
capped at 256 (`truncated`). A hand-run group also carries its `launcher` where one process outside it
started all of it: the nearest common ancestor of the group's topmost members, within three levels of
each. Shells, terminal multiplexers, sshd, su/sudo, init/systemd, container shims and PM2 are never
offered, nor PID 1–2 or the dashboard itself — stopping one of them takes far more than the workload
with it. The launcher is named from its command line, since Node renames its main thread.

Process nice values are read on the −20…19 scale `SetNice` takes. gopsutil returns getpriority(2)'s raw
kernel value, 20 minus the nice value, which made every ordinary process read as "nice 20" and left the
advisor's Lower priority disabled for all of them; `procs.niceOf` converts it.

A workload's remedy is the group's, so two batch routes act on many processes in one audited request,
each behind the per-process route's own gate:

- `POST /system/advisor/workloads/signal` (**destructive**, as `/processes/{pid}/signal`):
  `{targets: [{pid, startedAt}], signal: "SIGTERM" | "SIGKILL"}`, SIGTERM by default;
- `POST /system/advisor/workloads/priority` (**system.admin**, as `/processes/{pid}/priority`):
  `{targets, nice: 0–19}`, which only ever lowers — a target already at or below that priority is
  reported `skipped`.

Both take 1–512 targets and require every target's `startedAt`, unlike the single-process routes,
because the list was measured seconds earlier and a reused PID inside it would receive a signal meant
for another process. Each target passes the same checks as its single route — it still exists, its
start time matches, a signal could reach it (not a zombie or kernel thread), and PID 1 and the
dashboard are refused — and the answer is 200 with each target's outcome (`{pid, ok, skipped?,
error?}`) and `signalled` or `changed`, so one refused PID does not hide the rest. Each request is
audited once, as `advisor.workloads.signal` or `advisor.workloads.priority`, naming the first process
and how many others with the PIDs acted on. Lower priority changes scheduling, not a CPU cap;
signalling a supervised child may cause it to return, so owner controls are linked.

Network findings are judged on the window between two Health checks, never on since-boot counters
(see [observability](observability-security.md#metrics-saturation-health)); investigation shows the
cumulative counters and observed deltas separately and opens real network, connection and diagnostic
controls. TIME_WAIT may have no live process owner. Sensor limits come from
the driver; cooling and hypervisor steal remedies name the external owner. Runtime host findings carry
each failed unit (with its last result and when it failed) or affected container as a subject, so
service and container controls act on it by name. Runtime findings are aggregated by the backend so all Health surfaces
receive the same verdict. Missing reads and optional kernel evidence appear in `silences`.

## Configuration and feature owners

Docker uses one action handler on overview, list and detail. Restart policy updates happen in place;
RAM/CPU updates retain the running process and link Compose owners for persistence. Standalone
Configuration offers local preparation for checks, immutable image versions/digests, bindings and
privilege/socket changes. The full supported specification is reviewed before confirmed replacement;
credentials, volumes and unrelated settings are retained. Replacement loses the old writable layer
and logs, reports warnings and opens the returned container ID. Auto-remove workloads cannot use this
replacement path. Compare Inspect for Engine options outside the supported specification. Dashboard
host/socket access is explicitly described as required for its controls, never removed automatically.
Compose examples open the owning file for validation, save and deployment. Attention reclamation
removes only unused images and cache, leaving containers, networks and volumes untouched.

Security findings retain the existing confirmed firewall/SSH remedies when capabilities permit;
all other cases open real owning controls (allowlist, firewall, SSH, intrusion, listeners, certificates
or packages). Read-only principals get investigation without an unavailable mutation label.
Proxy/TLS already use owner-aware finding routes, explicit missing-source findings, reviewed TLS fix
sheets, site enable/reload and renewal-timer controls; external DNS/provider remedies remain external.
Deployment preflight and runtime diagnosis carry measured evidence, field fixes/owner links and
silences, with deployment/recovery/settings controls owned by the deployment module. Backup findings
open the failed run and its logs, rerun, target test/edit, resume and recovery controls; unprotected
resources prefill a backup job. Failed coverage reads remain visible and disable new protection from
stale evidence. Database fleet findings carry their fix on the control center — start the server,
restrict its port to this server, back it up now, connect a found server — or open the database's
Settings. Database catalogue findings are applied from the Advisor page only where the server itself
marks the fix safe (a maintenance action that locks nothing, a statement the classifier calls
non-destructive) and otherwise open in the Query page for review; SQL execution retains
its original authorization. Capped schema scans report omitted tables and failed engine reads retain
completed structural evidence with silences instead of claiming a complete report.

## Linux compatibility and verification

Storage and workload investigation uses native Linux filesystem/procfs reads rather than distribution
specific shell tools. Optional kernel counters, sensors, Docker, systemd and PM2 are detected or
reported unavailable. Existing owner modules retain their supported-tool limits; an absent manager
cannot acquire controls simply because the advisor has detected a finding.

Targeted unit/API tests cover storage containment, sparse/hard-link accounting, exact-copy checks,
changed/open files, retained keepers, concurrent cleanup, cancellation, process identity/intervals,
resource validation, action capabilities, per-file audits, partial database scans and runtime silences,
workload grouping and launcher refusal, the nice scale, the batch routes' validation, capabilities,
per-target identity outcomes and single audit, sustained-pressure and windowed-loss judgement, and areas.
Race tests cover storage, workload attribution and runtime aggregation. Browser acceptance checks
reviewed cleanup, actual post-action space, read-only roles, measured process actions, configuration
preview/confirmation, Compose ownership, dismissals and partial Health, plus the reachable UI specs.
Native tests pass in Ubuntu 22.04, Ubuntu 24.04, Debian 12, Alpine 3.20 and Fedora 42 containers via
`scripts/test-server-advisor-linux.sh`. These share the host kernel. Mint uses the same Ubuntu-family
interfaces, but an actual Mint host/kernel has not been tested and must not be claimed as verified.
A read-only host-root bind plus the host PID namespace tests the actual production filesystem
mapping against a harness-owned fixture, rather than assuming `/` inside a container is the host.
Live Docker acceptance owns temporary fixtures only and verifies in-place policy/RAM/CPU changes plus
reviewed replacement with a pinned image, health check and rotated logs while retaining environment
and network settings. No global prune is run against the operator's daemon.
