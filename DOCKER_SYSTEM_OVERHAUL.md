# Docker system overhaul

The plan behind turning the Docker section from "a dashboard that exposes Docker controls" into
an operations, diagnosis and safety layer. Written before the work started; kept as the record of
what was found, what was decided, and what shipped in which phase.

---

## 1. Current architecture

### Backend — `backend/internal/dockerx`

One package, one `Client` wrapping the Engine SDK, plus a compose driver that shells out to the
`docker compose` plugin (the daemon has no notion of projects, so there is no API alternative).

| File | Responsibility |
| --- | --- |
| `client.go` | Engine client, `Ping`, `Info`, and the cached `system df` walk (`diskUsage`) shared by every size figure |
| `containers.go` | List/inspect/lifecycle/logs/prune, `RedactEnv` |
| `images.go` | List/pull/remove/tag/prune, `DiskUsage` accounting, `ImageRef` |
| `imagedetail.go` | `InspectImage`, history, `UsedBy`, registry update checks |
| `resources.go` | Volumes and networks, including the "who uses this" joins |
| `compose.go` | Stack discovery (labels + on-disk walk), `RunCompose` |
| `composeops.go` | Streaming compose runner, validation, release flow, `DeclaredServices` |
| `create.go` | `ContainerSpec` ⇄ Engine config, create/recreate/rename/resources |
| `render.go` | Spec → `docker run` / compose YAML |
| `diagnose.go` | The findings engine — one `Diagnosis` with a flat `Status` |
| `events.go` | Daemon event ring buffer + subscriptions |
| `stats.go` | `docker stats` arithmetic and the sampler the metrics recorder uses |
| `buildkit.go`, `exec.go`, `checks.go`, `distribution.go`, `scan.go` | Builds, exec, health probes, registry digests |

Routes live in `backend/internal/api/handlers_docker.go` (mount + lifecycle) and
`handlers_docker_manage.go` (detail, spec, recreate, routes, stacks). Capability groups:
read is open to any authenticated principal, `service.control` for mutations, `s.destructive`
for anything that interrupts or destroys, `file.write` for compose file edits, `terminal` for exec.

### Frontend — `frontend/src/app/(dashboard)/docker` + `frontend/src/components/docker`

`layout.tsx` owns the reachability check and the seven-tab strip. Each sub-page is a thin shell
around a component in `components/docker/`. State comes from `usePoll` (REST) and `useSocket`
(container stream, stats, events, compose runs, pulls, builds).

---

## 2. Identified bugs

1. **URL parameters arrive percent-encoded.** chi v5 routes on `r.URL.RawPath` when it is non-empty,
   so `chi.URLParam` returns the *undecoded* segment. The frontend sends
   `/docker/images/${encodeURIComponent("sha256:abc…")}`, the handler passes `sha256%3Aabc…`
   straight to the Engine, and Docker answers
   `invalid reference format: repository name (library/sha256%3aabc…) must be lowercase`.
   Every `{id}`/`{name}` param in the Docker routes is affected — image ids, volume names with
   dots, stack names, container names.
2. **`ComposeStack.Total` counts containers, not declared services.** A stack discovered on disk
   but never deployed reports `0/0 up`, and a stack whose service failed to create reports
   `1/1 up` while the compose file declares two.
3. **The overview's stack count and the stacks page's stack count are different numbers.**
   The overview groups labelled containers (`groupStacks`); the stacks page calls
   `/docker/stacks/`, which also discovers compose files on disk. "3 stacks" and "7 stacks".
4. **`Container.SizeRw` is structurally zero in the listing.** The Engine omits it unless
   `size=1` is requested. Only `diagnose.go` works around it by reading the disk-usage snapshot.
5. **Delete is offered for objects Docker will refuse to delete.** Volumes with attached
   containers, networks with members, and the three system networks all render a live destructive
   button whose only outcome is a 409 rendered as a toast.
6. **Events default to `kinds=["container"]` with no way to say "everything"**, and the selected
   chip is a `secondary` button — the same treatment focus gets.
7. **Memory reads as a limit that does not exist.** `memLimit` is the host's RAM when no cgroup
   limit is set, so the table shows `97 MB / 62.7 GB` as if 62.7 GB were this container's budget.
8. **CPU percentage has no stated denominator.** `cpuDelta/sysDelta × onlineCPUs × 100` means
   100% is one core, and nothing on screen says so.
9. **The sparkline column is titled "Last hour"** without naming the metric.
10. **Published ports are rendered identically regardless of host binding.** `127.0.0.1:3000→3000`
    and `0.0.0.0:443→443` differ only by whether the badge is a link.

## 3. Misleading UX

- **"Health: All good" over a page full of warnings.** `Diagnosis.Status` is the worst level of a
  list that mixes runtime failures with security posture and disk hygiene, and the overview tile
  labels it "Health". A privileged container with a mounted Docker socket and no memory limit is
  runtime-healthy and needs attention; one label cannot mean both.
- **Compose verbs exposed raw**: `Up`, `Update`, `Re-apply`, `Down` with no statement of blast radius.
- **Compose-managed containers offer rename/duplicate/recreate** as if they were standalone, when
  the next `compose up` reverts the change.
- **Image "Version" column** mixes tag, dangling state and update availability.
- **Volume size `—`** is used for "not measured", "zero" and "failed" alike.
- **Container names** are the primary key of the row, but the id is not copyable anywhere.

## 4. Backend / data problems

- Disk accounting is correct but unlabelled: `LayersSize` (deduplicated on-disk total),
  `sum(image.Size)` (over-counts shared layers) and `sum(container.SizeRw)` are three different
  numbers presented with the same word, "size".
- There is no single place that says what a Docker object *is for*: ownership (stack → service →
  container → image/volumes/networks/ports/proxy) is recomputed ad hoc in four components.
- Exposure is computed in three places with three different rules (`diagnose.go`, `PortLink`,
  `handleContainerRoutes`).
- Findings have no machine-readable class, so the UI cannot separate runtime from posture.
- No deployment history exists for compose stacks, so "what changed" and "roll back" are
  unanswerable.

## 5. Proposed data model changes

New in `dockerx`:

- `imageref.go` — `IsImageID`, `NormalizeImageRef`, `ImageRefKind`. One definition of what a
  reference is, used by detail, remove, tag, pull and the update checker.
- `exposure.go` — `PortExposure` (`loopback` | `private` | `public` | `unknown`) plus
  `Reachability` describing binding, proxy route and firewall knowledge separately, so an
  inferred claim is never rendered as a known one.
- `attention.go` — `Attention` with `Severity` (`critical` | `warning` | `recommendation` | `info`)
  and `Class` (`runtime` | `security` | `storage` | `configuration` | `exposure` | `lifecycle`),
  and `RuntimeHealth` as its own counted summary. `Diagnosis` keeps its shape for compatibility
  and gains `runtime`, `attention` and `summary`.
- `ownership.go` — `Ownership` for any object: stack, service, compose file, git dir, labels,
  and the "what depends on this" edges.
- `writable.go` — the writable-layer investigator: `WritableLayerReport` with a per-directory
  breakdown, its measurement method, and honest failure states.
- `failure.go` — `FailureDiagnosis`: exit code, signal, OOM, health history, restart cadence,
  correlated events, and a `likely` cause that is always labelled as inferred.
- `anomaly.go` — rate-of-change detection over the metric history already recorded.

Changed:

- `ComposeStack` gains `declared`, `declaredCount`, `state`, `orphans`, `deployed`.
- `Container` gains `memoryLimited`, `cpuQuota`, `exposure`, `owner`.
- `DiskUsage` gains `explain` — the definition behind each line, so a tooltip is data rather
  than prose duplicated in the component.

New table: `docker_stack_deployments` (id, project, working_dir, config_hash, config, services,
image_digests, env_hash, git_commit, actor, source, action, result, created_at) — additive,
`CREATE TABLE IF NOT EXISTS`, no changes to existing tables.

## 6. Frontend component changes

- `components/docker/attention.tsx` — the severity-ranked list, shared by overview, containers,
  and each detail panel.
- `components/docker/exposure.tsx` — one renderer for a published port, replacing `PortLink`.
- `components/docker/tabs.tsx` — one chip/tab primitive with distinct selected/hover/focus states.
- `components/docker/writable-layer.tsx`, `failure.tsx`, `cleanup.tsx`, `deploy-preview.tsx`.
- Overview rebuilt around: running, runtime health, attention, active stacks, disk.
- Container table restructured: Container · Image · Status · CPU · Memory · Activity · Ports ·
  Issues · Actions, with Status carrying runtime only.

## 7. API changes

Additive only; no existing route changes shape except by gaining fields.

| Route | Change |
| --- | --- |
| `GET /docker/health` | gains `runtime`, `attention`, `summary` |
| `GET /docker/containers/` | gains `memoryLimited`, `exposure`, `owner` per row |
| `GET /docker/containers/{id}/writable-layer` | **new** — breakdown, lazy, cached |
| `GET /docker/containers/{id}/failure` | **new** — restart/failure diagnosis |
| `GET /docker/containers/{id}/routes` | gains firewall correlation |
| `GET /docker/images/{id}` | fixed for image ids and digests |
| `GET /docker/stacks/` | gains `state`, `declared`, `orphans` |
| `GET /docker/stacks/{name}/preview` | **new** — compose diff / impact preview |
| `GET /docker/stacks/{name}/deployments` | **new** — history |
| `GET /docker/cleanup/preview` | **new** — per-category reclaimable |
| `POST /docker/cleanup` | **new** — category-selected sweep |

## 8. Migration concerns

- `docker_stack_deployments` is a new table; existing installs get it on next boot via the
  existing `CREATE TABLE IF NOT EXISTS` schema block. No `addedColumns` entries needed.
- The URL-decoding fix changes what handlers receive. Anything that previously worked did so
  because the segment contained no escapes, and decoding an unescaped segment is a no-op, so the
  fix is applied only when chi actually routed on `RawPath`.
- `Diagnosis.Status` and `Diagnosis.Findings` keep their meaning so an older frontend bundle
  served from cache does not break.

## 9. Implementation phases

1. **Trust** — URL decoding + image references, runtime/attention split, disk-figure labelling,
   event filters, destructive-action gating.
2. **Standard UX** — ownership model, compose states and vocabulary, drift protection, one tab system.
3. **Containers** — naming, table shape, CPU/memory semantics, ports, detail, secrets.
4. **Attention** — severity model as a first-class feature.
5. **Writable layer investigator.**
6. **Persistence migration** — diagnosis and a generated plan; no silent mutation.
7. **Failure diagnosis.**
8. **Anomaly detection** over recorded history.
9. **Image intelligence.**
10. **Deployment preview / compose diff.**
11. **Deployment history + rollback.**
12. **Exposure intelligence** across Docker, firewall and proxy.
13–20. Networks, volumes, cleanup, run-a-container, secure defaults, templates, events/audit,
    structured errors.

## 10. Test strategy

Go tests exercise the pure functions against synthetic inspects, so no daemon is required:
`imageref_test.go` (tag/digest/id/dangling/local/multi-tag), `exposure_test.go`
(loopback/all-interfaces/IPv6/none/multiple), `attention_test.go` (severity ranking, runtime vs
posture separation), `compose_state_test.go` (running/partial/stopped/not-deployed/orphans/missing
file), `failure_test.go` (OOM, signal, clean exit, loop cadence), `writable_test.go` (parsing and
failure states), and an API-level test that a percent-encoded path parameter reaches the handler
decoded. Frontend browser tests cover the containers table, the attention list and the cleanup
preview.

## 11. Performance strategy

- One `system df` walk, cached, shared by every size figure (already the case; extended rather
  than duplicated).
- One container listing per request path; joins happen server-side.
- Writable-layer breakdown and volume sizing are opt-in, lazy, and cached with an explicit
  "calculating" state rather than blocking a list.
- Registry checks stay on the existing TTL cache and never run during a list render.
- Event-driven invalidation of the disk cache on any mutation that changes disk.

## 12. What shipped

Everything below is in the tree, building, and covered by the checks in §10.

**Phase 1 — trust.** `httpx.URLParam` decodes path parameters chi leaves escaped (the image-id
bug); `imageref.go` classifies tag / digest / id / dangling and stops appending `:latest` to an id;
`attention.go` splits runtime health from attention and `Diagnosis` carries both summaries; the disk
model names `sharedLayers` and ships a `DiskDefinition` per figure; the events feed gained an All
filter with selection, hover and focus given three different mechanisms; delete is replaced by the
reason ("in use by 2") for volumes, networks and images Docker would refuse.

**Phase 2 — standard UX.** `ComposeStack` counts *declared services*, carries `state`, `summary`,
`orphans`, `deployed` and `declaredSource`; compose verbs are named for what they do with the raw
command in the confirmation; compose-managed containers move rename/duplicate behind a statement of
consequence; `components/docker/tabs.tsx` is the one chip/tab system.

**Phase 3 — containers.** Name and copyable id, never concatenated; the table is Container · Image ·
Status · CPU · Memory · CPU 1h · Ports · Issues · Actions, with Status runtime-only; CPU states its
denominator and memory says "no limit" instead of inventing one; ports carry their binding's meaning;
the secret-env list gained connection strings and a Copy that never reveals.

**Phases 4–9.** Four-level severity with classes; the writable-layer investigator (`du` inside the
container through `ExecCheck`, honest about its failure modes) and a generated migration plan that is
never executed; failure diagnosis with evidence, sources and a *likely* cause; anomaly detection over
recorded history including writable-layer growth, which required carrying `size_rw` on the sample;
image intelligence with a `pinned` state and a reference panel.

**Phases 10–12.** `docker_stack_deployments` records the compose file, the running digests and the
git commit before every state-changing action (environment values hashed, never stored); the deploy
preview diffs against it and states the volume impact; `GET /docker/containers/{id}/routes` correlates
binding, reverse proxy and firewall — including the ufw/NAT bypass — and labels every inferred verdict.

**Phases 13–20.** Network scope/attachable/labels and a shape view; volume size states and a database
warning before browsing; category-selected cleanup; the `docker run` parser now separates *unsupported*
from *unreadable* and keeps the original command; port bindings offered as intent, populated from the
host's real interfaces; templates across five categories; events attributed and correlated to the audit
log; structured errors carrying resource, operation, reason, raw text and retryability.

**Not done.** No automatic persistence migration — the prompt's own fallback was taken, and the
dashboard produces the plan and the commands rather than stopping a service and copying data
unattended. No dedicated Attention page: the prompt asked for one only if it outgrew Overview and
Containers, and it has not. The chi decoding fix was applied to the Docker routes only; the same
defect exists in ~20 other handler files and is called out here rather than fixed in a Docker change.

## 13. Security concerns

- Environment and image env stay redacted below `system.admin`; reveal is explicit and never
  logged, audited or notified with the value attached.
- Writable-layer analysis runs `du` inside the container through the Engine exec API with an
  explicit argv — never a shell string — and only for a running container.
- Every new mutation route keeps its capability group; cleanup with volumes selected keeps the
  typed-phrase requirement.
- Rollback never runs a compose file the operator has not been shown.
