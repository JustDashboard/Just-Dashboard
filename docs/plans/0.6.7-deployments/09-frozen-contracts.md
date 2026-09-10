# Frozen 0.6.7 deployment contracts

- Status: accepted for implementation
- Date: 2026-09-02
- Authority: C0 contract, superseded only by an explicit ADR and matching migration/test update

This document turns the directional architecture into closed names that code, API tests, browser tests,
and migration fixtures can share. JSON uses camelCase. SQLite uses snake_case. Unknown enum values are
rejected on writes and preserved as `unknown` only where an external feature owner supplied the value.

## Identity and ownership

The shipped `deploy_projects.id` remains the stable deployment id; it is not copied into a replacement
top-level table. The UI calls the object a deployment, application, service, stack, or game server as its
profile requires. Existing `/deploy/{id}` URLs and API clients therefore retain identity through upgrade.

Closed workload profiles:

`web`, `static`, `worker`, `image`, `compose`, `service`, `game`, `imported`.

Closed environment kinds:

`production`, `staging`, `preview`.

Closed dependency ownership modes:

- `managed`: created from this plan and mutable only through a previewed deployment/removal plan;
- `linked`: selected existing resource, usable but never deleted by deployment lifecycle;
- `observed`: inferred relationship, read-only until the operator adopts or corrects it.

Archiving a deployment sets `archivedAt` and disables its triggers. It does not stop or remove runtime,
artifacts, data, proxy sites, backups, or connections. Removing managed resources is a separate plan whose
targets and confirmation requirements are returned before execution.

## Source, plans, and releases

Closed source kinds:

`git`, `local`, `image`, `compose`, `blueprint`, `import`.

Closed build methods:

`recipe`, `dockerfile`, `static`, `image`, `compose`, `none`, `legacy_compose`.

Closed release strategies:

`blue_green`, `stop_first`.

A plan revision is immutable after it is referenced by a run. Saving configuration creates a new integer
revision for the environment. `pending=true` means `desiredRevision != liveRelease.planRevision`; saving
never changes runtime. A successful activation points the environment at a release carrying the applied
revision. It clears only that revision's diff; changes saved while the run was active remain pending.

A release snapshot is immutable and contains:

- deployment/environment and monotonically increasing environment release number;
- source kind, immutable source identity, ref/revision, changed-path evidence, and credential reference;
- build/runtime plan revision and their digests;
- generated Dockerfile/rendered configuration digest and secret-free preview;
- image/config artifact digests and target OS/architecture;
- variable names, scopes, sensitivities, and value digests, never plaintext values;
- dependency/check snapshots, strategy, expected downtime, provenance, blueprint id/version;
- actor, creating run, predecessor, created time, activation time, and retirement time.

Closed artifact kinds:

`image`, `compose`, `runtime_config`, `static_bundle`, `source_archive`, `backup`, `diagnostic`, `export`.

## Run contract

Closed operations:

`deploy`, `redeploy`, `restart`, `force_build`, `rollback`, `preview_create`, `preview_update`,
`preview_remove`, `scheduled_action`, `import_adopt`, `remove_managed`.

Closed triggers:

`manual`, `legacy_hook`, `generic_hook`, `github`, `gitlab`, `bitbucket`, `gitea`, `api`, `schedule`,
`preview`, `rollback`, `migration`.

Closed run states:

`requested`, `validating`, `queued`, `preparing`, `running`, `verifying`, `activating`,
`failed_activation`, `restoring_previous`, `cancelling`, `succeeded`, `failed`, `cancelled`, `rolled_back`,
`superseded`.

Terminal states are `succeeded`, `failed`, `cancelled`, `rolled_back`, and `superseded`. A retry is a new
run with `retryOfRunId`; a terminal run never transitions again.

### Allowed run transitions

| From | Allowed next states |
| --- | --- |
| `requested` | `validating`, `cancelling`, `failed` |
| `validating` | `queued`, `cancelling`, `failed` |
| `queued` | `preparing`, `cancelling`, `superseded`, `failed` |
| `preparing` | `running`, `cancelling`, `failed` |
| `running` | `verifying`, `cancelling`, `failed` |
| `verifying` | `activating`, `cancelling`, `failed` |
| `activating` | `succeeded`, `failed_activation` |
| `failed_activation` | `restoring_previous`, `failed` |
| `restoring_previous` | `rolled_back`, `failed` |
| `cancelling` | `cancelled`, `failed` |

No other edge is legal. In particular, cancellation cannot interrupt the indivisible proxy write/restore
operation once `activating` has begun; it is observed immediately after the route converges.

Queued automatic runs for the same environment/ref may be superseded only when all are true: both are
non-production or trigger-class automatic; neither operation is rollback/manual/remove; neither has been
claimed; the newer run includes every watched change the older run represented. Superseding records the
new run id and emits a terminal event.

Idempotency keys are scoped to deployment/environment/operation. Provider delivery ids are scoped to the
trigger. Reusing a key with a different normalized request digest returns `idempotency_conflict`; the same
digest returns the original run.

## Step contract

Closed step keys, in default order:

1. `resolve_source`
2. `acquire_source`
3. `analyze_plan`
4. `prepare_context`
5. `build_artifact`
6. `render_runtime`
7. `release_task`
8. `backup_gate`
9. `start_candidate`
10. `verify_readiness`
11. `verify_smoke`
12. `activate`
13. `retire_previous`
14. `record_release`
15. `notify`

Adapters omit no nodes: inapplicable work is `skipped` with a reason and a missing owner is `unavailable`.
This keeps the release path structurally comparable without painting skipped work green.

Closed step states:

`pending`, `blocked`, `running`, `passed`, `warning`, `failed`, `skipped`, `cancelled`, `unavailable`.

Allowed step transitions:

- `pending -> blocked|running|skipped|cancelled|unavailable`
- `blocked -> pending|running|failed|cancelled|unavailable`
- `running -> passed|warning|failed|cancelled|unavailable`
- `failed -> pending` only by an explicit step retry that increments `attempt`

`passed`, `warning`, `skipped`, `cancelled`, and `unavailable` are terminal for that attempt. A run retry
copies the plan into new step rows rather than reopening old rows.

Closed check kinds:

`http`, `tcp`, `docker_health`, `command`, `public_route`, `dns`, `tls`, `game_handshake`, `backup_freshness`.

Check results use step states where applicable. `disabled`, `skipped`, `unavailable`, `warning`, and
`passed` remain distinct in API responses.

## Stream contract

Closed event types:

`run.state`, `step.state`, `step.log`, `step.evidence`, `queue.position`, `finding`, `artifact`,
`heartbeat`, `resync`.

```json
{
  "seq": 184,
  "type": "step.log",
  "runId": 84,
  "stepId": 7,
  "ts": "2026-09-02T12:03:46Z",
  "data": { "stream": "stdout", "text": "200 OK · 34 ms", "truncated": false }
}
```

Sequences start at 1 and are unique/monotonic per run. An append commits before publish. Reconnect uses
`after=N` and receives every retained event with `seq > N` exactly once in ascending order, followed by
live events. When `N` predates retained payload, the first event is `resync` containing run/step snapshot
and `oldestSeq`; clients replace their local snapshot and continue.

Limits:

- raw webhook/upload JSON body: 4 MiB unless a narrower endpoint limit is declared;
- one persisted log text: 64 KiB UTF-8, visibly truncated;
- one event payload: 256 KiB;
- live subscriber buffer: 128 events; a slow subscriber is disconnected, never allowed to stall append;
- hot transcript: latest 10,000 log events per run; older log text may compact after terminal summary;
- run summaries, terminal reasons, state events, steps, and artifact/dependency evidence are not compacted;
- default run summary retention: 10,000 per installation, preserving releases referenced by retention;
- default release retention: current + five prior successful artifacts per environment and failed artifacts
  for 7 days; operator pins override cleanup; volumes are never automatic cleanup targets.

## Draft and preflight contract

Draft steps are `intent`, `source`, `detection`, `configuration`, `preflight`. Drafts are server-side,
owned by the creating principal, expire after 30 days, and contain references to credentials/secrets rather
than plaintext reveal responses. Saving a step validates its closed schema and increments `revision`.

Preflight severities are `pass`, `decision`, `warning`, `blocked`, `unavailable`. Each finding has stable
`code`, title, measured, means, action, owning feature, and optional field id/deep link. Preflight is pure:
it may read source metadata, registries, Docker, ports, DNS, proxy config, backup state, and host metrics,
but cannot clone into an operator checkout, build, pull, start/stop, write proxy/firewall, or enqueue a
backup. Network checks have individual timeouts and state which evidence is unavailable.

Committing a draft is idempotent and creates the deployment, production environment, source, first plan
revision, variables, dependencies, checks, and triggers in one transaction. It does not deploy.

## API and security matrix

All paths are below `/api/v1`. `read`, `service.control`, `system.admin`, and `destructive` name backend
capabilities. `ordinary` means the frontend pauses and sends the normal confirmed retry; `typed:<object>`
means `X-Confirm` is enforced server-side. Every mutation is audited by middleware plus the named action.

| Method/path | Capability/session | Confirmation | Audit action |
| --- | --- | --- | --- |
| `GET /deploy/` | `read` | none | — |
| `POST /deploy/drafts` | `system.admin`, session | none | `deploy.draft.create` |
| `GET /deploy/drafts/{draft}` | owner or `system.admin`, session | none | — |
| `PUT /deploy/drafts/{draft}` | owner or `system.admin`, session | none | `deploy.draft.save` |
| `POST /deploy/drafts/{draft}/detect` | owner or `system.admin`, session | none | `deploy.draft.detect` |
| `POST /deploy/drafts/{draft}/preflight` | owner or `system.admin`, session | none | `deploy.draft.preflight` |
| `POST /deploy/drafts/{draft}/commit` | `system.admin`, session | ordinary when warnings acknowledged | `deploy.create` |
| `GET /deploy/blueprints` | `read` | none | — |
| `GET /deploy/blueprints/{id}/{version}` | `read` | none | — |
| `GET /deploy/{id}` and environment/dependency/pending reads | `read` | none | — |
| `PATCH /deploy/{id}` | `system.admin`, session | none | `deploy.update` |
| `POST /deploy/{id}/archive` | `destructive` | ordinary | `deploy.archive` |
| `POST /deploy/{id}/removal-plan` | `system.admin`, session | none | `deploy.removal.preview` |
| `POST /deploy/{id}/remove-managed` | content-dependent `destructive` + admin for privileged targets | ordinary; `typed:<volume>` for named volumes/data restore rules | `deploy.resources.remove` |
| `GET/POST /deploy/{id}/environments` | read / `system.admin` session | none | `deploy.environment.create` |
| `PATCH /deploy/{id}/environments/{env}` | `system.admin`, session | none | `deploy.environment.update` |
| `POST /deploy/{id}/environments/{env}/runs` | `service.control`; content-dependent admin authorization | ordinary only for dirty managed checkout or warnings | `deploy.run.request` |
| `GET /deploy/{id}/runs[/{run}]` | `read` | none | — |
| `GET /deploy/{id}/runs/{run}/logs` | `read`; run must belong to project | none | — |
| `GET /deploy/{id}/runs/{run}/stream` | `read`, WebSocket origin | none | open event audited |
| `POST /deploy/{id}/runs/{run}/cancel` | `service.control` | none | `deploy.run.cancel` |
| `POST /deploy/{id}/runs/{run}/retry` | `service.control` | none | `deploy.run.retry` |
| `POST /deploy/{id}/releases/{release}/activate` | `destructive` | ordinary | `deploy.release.activate` |
| variable list | `read` returns names/scopes/masks | none | — |
| variable reveal | `system.admin`, session | none | `deploy.variable.reveal` |
| variable create/update/delete/generate/rotate | `system.admin`, session | none | `deploy.variable.*` (name only) |
| domain/port/storage/check create/update/delete/test | `system.admin`, session; content-dependent admin | ordinary only when public/unsafe warning is acknowledged | `deploy.configuration.*` |
| backup policy link/update | `system.admin`, session | none | `deploy.backup_policy.*` |
| import preview | `system.admin`, session | none and no side effects | `deploy.import.preview` |
| import adopt | `system.admin`, session | ordinary | `deploy.import.adopt` |
| trigger create/update/delete/test | `system.admin`, session | none | `deploy.trigger.*` |
| schedule create/update/delete/test | `system.admin`, session | none | `deploy.schedule.*` |
| notification channel create/update/delete/test | `system.admin`, session | none | `deploy.notification.*` |
| `POST /hooks/deploy/{hookID}` | bounded legacy HMAC | none | `deploy.hook.delivery` |
| `POST /hooks/providers/{provider}/{hookID}` | bounded raw-body provider verifier | none | `deploy.provider.delivery` |
| `POST /deploy/{id}/hooks/{triggerID}` | scoped HMAC/API credential | none | `deploy.generic_hook.delivery` |

Rollback remains ordinary confirmation. Cancel is not destructive. Archiving leaves runtime/data.
Restore, volume deletion, and discarding a dirty checkout retain the existing typed-confirmation rules.

Advanced runtime content is authorized by the server after decoding. Privileged mode, host network,
added capabilities/devices, Docker socket, and host bind sources require `system.admin` regardless of the
path's generic capability. Blueprint declarations do not waive this check.

## Closed error codes

Validation and lookup:

`deploy_not_found`, `environment_not_found`, `run_not_found`, `release_not_found`, `draft_not_found`,
`invalid_transition`, `invalid_plan`, `invalid_source`, `invalid_ref`, `invalid_image`, `invalid_compose`,
`invalid_variable`, `variable_cycle`, `path_outside_roots`, `unsupported_source`, `unsupported_builder`,
`unsupported_runtime`, `advanced_authorization_required`.

Concurrency and lifecycle:

`idempotency_conflict`, `environment_busy`, `queue_full`, `lease_lost`, `run_terminal`,
`run_not_cancellable`, `run_not_retryable`, `artifact_missing`, `artifact_retained`, `candidate_ambiguous`,
`activation_failed`, `restoration_failed`, `cleanup_incomplete`.

Facilities and checks:

`docker_unavailable`, `git_unavailable`, `builder_unavailable`, `proxy_unavailable`,
`certificate_unavailable`, `backup_unavailable`, `metrics_unavailable`, `firewall_unavailable`,
`port_conflict`, `domain_conflict`, `dns_unverified`, `readiness_failed`, `smoke_failed`,
`backup_required`, `backup_stale`.

Hooks and automation:

`hook_disabled`, `bad_signature`, `wrong_event`, `wrong_repository`, `wrong_ref`, `delivery_replayed`,
`payload_too_large`, `watch_paths_ignored`, `preview_quota`, `notification_failed`.

Internal errors remain the shared `internal_error` wire response; raw command/store errors do not cross the
API boundary.

## Additive schema and 0.6.6 mapping

The executable DDL lives in `backend/internal/store/store.go`; this section fixes its meaning.

For every existing `deploy_projects` row, migration transactionally and idempotently creates:

1. one `production` environment whose desired revision is 1;
2. one `local` source with the existing `repo_path` and branch;
3. one `legacy_compose` build plan and one Compose runtime plan carrying `compose_file`, pre-command, and
   post-command without interpreting their shell text;
4. one active variable revision per `deploy_env` row, copying `value_enc` byte-for-byte;
5. one `legacy_hook` trigger, copying `hook_id`, sealed secret, and enabled state byte-for-byte;
6. environment links on every legacy run, plus a canonical `state` mapped as
   `running -> running`, `success -> succeeded`, `failed -> failed`;
7. one historical release for every successful legacy run, ordered by `(started_at,id)`, with source
   revision `to_commit`; the last successful release is the environment's live release;
8. one `legacy_pipeline` step and sequenced legacy log event per old run so history is resumable without
   deleting the original `deploy_runs.log` column.

The old tables/columns remain readable throughout 0.6.7. Legacy ids, hook behavior, ciphertext, log text,
run order, and metrics joins are not rewritten. The migration marker is the unique production environment
for the project, not a global version integer, so a crash can retry each project safely. Opening the same
database twice creates no new row and changes no mapped value.

Fresh deployment records use the normalized tables. Compatibility reads may project them into the 0.6.6
response until the old frontend is removed, but new secrets and plan data are never squeezed back into the
legacy columns as an authority.

## Recovery responsibility

Every shipped action documents a next step in the run evidence. Internal recovery can clean a candidate,
restore a prior proxy spec, reactivate a retained artifact, release a stale port/queue lease, or retry a
pure check. External recovery is explicitly named for provider credentials, DNS delegation, certificate
rate limits, missing backup targets, pruned artifacts, ambiguous manually edited resources, and failed
non-idempotent release tasks.

