# Staging and promotion

A deployment can have one **staging** environment beside production: its own branch, domain,
variables, volumes and database links, built and deployed exactly as production is. A staging release
can then be **promoted**: production runs that release's exact image — the same local image id —
with production's own configuration and variables, and builds nothing. On one host the image is
already local, so no registry is involved.

Code: `deploy/staging.go` (the environment and its removal), `deploy/promotion.go` (admission, the
render step, the confirmation's read, retention), `api/handlers_deploy_staging.go` (routes).
`EnvironmentStaging` existed in `deploy/contracts.go` before this and nothing used it; the model is
the one previews already use — a second `deploy_environments` row of the same project with its own
versioned source, build and runtime plans, variables, dependencies, checks, releases and runs.

## The environment

`POST /api/v1/deploy/{id}/environments` `{kind: "staging", ref?, domain?, copySecrets?}` adds it
(`system.admin`, session only, audited `deploy.environment.create` with the names copied and skipped
and whether sensitive values were copied — the audit redactor hides any key containing "secret", so
the flag is `copiedSensitiveValues`). There is at most one, always slug `staging`; a second answers
`409 staging_exists`. `CreateStagingEnvironment` copies production's *desired* plan into revision 1 of
the new environment in one transaction:

| From production | Staging gets | Why |
| --- | --- | --- |
| Source | The same source; for a remote Git source the branch in `ref` (resolved to a commit by the route before the transaction, so a missing branch is `400 ref_not_found` and nothing is written) | Staging follows its own branch |
| Build plan, release tasks, build secrets | Copied | Staging must build and migrate as production does; release tasks get staging's variables, so they reach staging's database |
| Runtime plan | Copied, with every mount replaced by a new managed named volume `jd-staging-e<env>-<sha8(target)>`, fixed host ports and published ports dropped, and a sign-in for previews only made to gate staging too | Production holds the ports and the data; staging is work in progress |
| Health checks | Copied with their URL, host and port stripped (`copyPreviewChecksTx`) | A check must exercise staging, not production's address |
| Plain variables | Copied, marked `copied_from_environment` | Configuration, not credentials |
| Secret variables | **Not copied** unless `copySecrets` | Staging runs code production has not shipped: production's payment or mail key there acts on real customers. Previews take the same line |
| `${{database\|credential\|domain\|service.…}}` references | **Never copied** | They resolve to production's own resources |
| `${{project\|server.…}}` references | Copied | Sharing across a project or the server is what a shared variable is for |
| `${{variable.X}}` aliases | Copied once `X` was | An alias means nothing without its target |
| Database links, storage links, backup gates | **Not copied** | A staging release task on production's database would migrate production's schema from code that was never promoted |
| Domains | Only the `domain` asked for, HTTPS, refused `409 domain_taken` if any live environment of any project claims it | A hostname is one route |
| Git policy | Its own row: automatic, production's watch paths and status setting | Staging deploys its branch on every push from its first deploy on |

What was left behind and what was changed come back by name in `skipped` and `adjusted`, which the
dialog reads back as "set these before staging's first deploy". Compose stacks, compatibility
deployments, unadopted imports and host-network runtimes are refused (`409 staging_unsupported`):
their container names, networks or ports would collide with production's on one host.

After that staging is an ordinary environment: every `…/environments/{env}/…` route (configuration,
source, variables, domains, storage, database links, triggers, schedules, git policy, runs, releases,
rollback) works on it unchanged. The Git watcher polls `production` **and** `staging`
(`gitWatchTargets`), so a push to staging's branch deploys staging; provider hooks for a staging trigger
get the same watcher decision. Staging keeps production's conservative supersession rule rather than a
preview's. A staging environment may not share a database connection, endpoint or container with any
other environment (`checkEnvironmentIsolation` and the binding check now cover `staging` as well as
`preview`; the refusal is `staging_isolation_required`), because its release tasks run on every deploy.

Project-level reads take `?environment=<id>` to answer for staging instead of production:
`GET /deploy/{id}` (the summary and runtime), `/operations`, `/requests` (and its stream and export),
`/lifecycle` (and its stream), `/insights` and `/preview-frame`. Without it they answer for production,
and production's summary and insights leave staging's runs out — staging serves nobody, so its builds
are not production's delivery figures; previews stay where they were. `GET /deploy/{id}/runs?view=engine`
keeps its byte-compatible default (every environment's runs) and takes `scope=production` for the
project as production reads it, which the project pages ask for, or `environment=<id>` for staging's. `GET /deploy/{id}/environments` lists production and staging (with production's `promotionOnly`).

Removing it is `DELETE /api/v1/deploy/{id}/environments/{env}` (`s.destructive`, session, ordinary
confirmation, audited `deploy.environment.remove`; body `{volumes}`). `BeginStagingRemoval` archives the
environment first so nothing new can start, and refuses while a run is in flight there or a production
promotion from it is unfinished (`409 already_running`) and while production takes only promotions
(`409 promotion_only_requires_staging`). It names only staging's own release containers, its route, its
database networks and — when asked — volumes with the `jd-staging-e<env>-` prefix, so a mount the
operator pointed back at production's volume is never deleted. If anything cannot be removed the
environment is put back (`409 staging_removal_incomplete` with what did come off) so the removal can be
retried; otherwise `FinishStagingRemoval` frees the slug, retires staging's releases (an image production
was promoted from stays: production's own release names it) and turns its triggers and schedules off.
Archiving, restoring, removing the managed resources of and permanently deleting the deployment work on
every environment as before.

## Promotion

`POST /api/v1/deploy/{id}/environments/{production}/promote` `{releaseId}` (`service.control` — exactly
what deploying production needs, since it changes production the same way; token-usable like a deploy;
`Idempotency-Key` honoured; audited `deploy.release.promote` with the staging release, its number, the
image id and digest, the commit and the run) queues a production run with operation `promote` and trigger
`manual`. `PromotionSource` refuses (`409`) unless the release is a `live` or `retained` release of this
deployment's live staging environment (`promotion_source`), built as exactly one image with no Compose
artifact and a recorded local image id (`promotion_incompatible`), whose image artifact is still
`available` and which Docker still holds by that id (`promotion_image_missing`), from the same kind of
source production deploys and, for remote Git, the same repository (`promotion_incompatible`). Into
anything but production is `promotion_target`.

The run is production's: plan revision = production's desired revision, variables snapshotted from
production at enqueue like any run, metadata `promotion` (environment, release id and number, run, image
id, digest, commit) plus the staging run's `commit` reading, `sourceRevision` the promoted commit when
production's source is remote Git. Its steps are `PromotionStepKeys` — a deploy's from `render_runtime`
on (`render_runtime`, `backup_gate`, `rehearse_migration`, `release_task`, `provision_certificate`,
`start_candidate`, `verify_readiness`, `verify_smoke`, `activate`, `retire_previous`, `record_release`,
`notify`), with no source, plan analysis, context or build step; `TestPromotionStepsAreADeploysFromRenderRuntimeOn`
holds the list to exactly `DefaultStepKeys` from `render_runtime` on, so a step added to a deploy is
not quietly left out of a promotion. `render_runtime` (`renderPromotedRuntime`)
re-validates the source, inspects the image by its id, tags it as `just-dashboard/deployment-<prod>:run-<id>`
and reads the tag back to prove it names the same id, then records a candidate release whose runtime
snapshot is production's plan, production's variables, dependencies, checks and domains around the
staging image, with the staging release's source identity (and the image's own proxy-trust and
`WEB_CONCURRENCY` facts). `ExecutionPlan` gives a promotion run the staging release's source identity, so
the release records where its image came from and a release task in the dashboard's shell materializes
the promoted commit (from production's own source configuration — hence the same-repository rule); a
promotion with no shell task materializes nothing. The release's provenance carries `promotion`, which the
release read model returns as `Release.promotion`.

Everything after that is the release path every deploy takes: production's **release tasks run against
production** (production's variables, so production's database) after the backup gate and before
anything starts — and, when production's [migration rehearsal](implementation.md) terms are on,
**rehearsed first** in the promoted image against a copy of production's database restored from its
backup (`rehearse_migration`, the same step a build's run takes, between `backup_gate` and
`release_task`). The rehearsal is production's in every respect but the image: production's tasks,
production's variables, so the copy is of the managed database production's values reach, and the
transcript says so ("Rehearsing production's release tasks in the promoted image … with production's
variables"). A task that fails against the copy ends the promotion `rehearsal_failed` before any task
touches production, and a rehearsal that cannot be carried out ends it `rehearsal_unavailable` (or warns,
as the terms say), exactly as either ends a deploy: production's schema, rows and live release are as
they were. Then the certificate, the candidate, readiness and smoke checks, blue/green or stop-first
activation, automatic restore of the previous release on a failed activation, retirement, notification
("Promotion from staging", titled with the environment) and a commit status under production's context
(`just-dashboard/production`; staging's own runs report under `just-dashboard/staging`).

Concurrency: a promotion is queued behind whatever production already has queued or running, neither
supersedes nor is superseded by an automatic run, and replaces what an earlier run releases. The
confirmation says so when production has a run ahead of it. A promotion uses production's *current*
saved plan, so production settings saved but not yet live go live with it; the confirmation lists them.
Staging's frozen release configuration is never applied to production — only its image is taken.

Rollback is unchanged and works for promoted releases: rolling back to one clones it, and
`CloneCandidateRelease` now carries the target release's own source identity and `promotion` forward, so
the clone still says "from staging #N" and names staging's commit rather than production's plan identity.

Retention: a staging release referenced by an unfinished promotion is retained ("source of an unfinished
promotion to production") by `ArtifactRetentionPlan` and refused by `reserveArtifactPrune`, because a
queued run holds no lease yet. Once promoted, production's release holds its own tag and digest, so the
physical image survives as long as production keeps that release (live, the five prior successful
releases, pinned) even after staging's release ages out — the existing shared-digest rule.

`GET /api/v1/deploy/{id}/environments/{production}/promotion?releaseId=N` is the confirmation's read
(any signed-in reader): the source release, the image id, digest and size and whether Docker holds it,
production's live release, queued runs, promotion-only flag, pending changes, release task names and
whether production's rehearsal terms are on (`target.rehearsal`), the
blockers that would refuse it now, and `detail` — `compareRuntimeSnapshots` between the staging release's
snapshot and what production would freeze if queued now (`desiredRuntimeSnapshot`, the same rows
`Enqueue` snapshots): runtime fields, variables by name and value digest only, dependencies, checks and
domains.

## Production that takes only promotions

`deploy_git_policies.promotion_only` (added through `addedColumns`, default 0) is part of the
environment's saved [Git policy](git-policy.md). `PUT …/git-policy` accepts an optional `promotionOnly`;
omitted, the current value is kept, so an editor that predates it cannot switch it off by leaving it out.
Turning it on is refused on anything but production (`ErrInvalidPlan`) and without a staging environment
(`409 staging_required`). While it is on, `validateGitPolicyAdmissionTx` refuses every `deploy` and
`force_build` in that environment — manual, API, poll, hook or schedule alike — with
`422 promotion_only` (a webhook delivery records `promotion_only`), the watcher records the decision
`promotion_only` instead of observing the branch, and `git-watch` answers status `promotion_only`.
Redeploy, restart, stop, start, rollback and promotion stay available; staging still builds.

## UI

The project's pages read one environment at a time: `ProjectProvider` keeps the choice for the tab
(`deploy.<id>.environment`, `?env=staging|production` on arrival), reads `GET /deploy/{id}/environments`,
and asks the summary, runs, operations and the environment-scoped reads for staging's id while staging is
chosen; settings drafts are keyed per environment (`useDraftScope`). The header draws
`EnvironmentSwitch` (Production | Staging) once staging exists, and on staging a Promote button beside
the command. The verbs (`project-verbs.tsx`) add **Add staging environment…** to production's Project
group, **Promote to production…** to staging's, **Remove staging…** to staging's danger group, and in a
promotion-only production **Promote from staging…** replaces Deploy while Rebuild and Deploy a specific
version are not offered. Project-wide verbs (duplicate, test a pull request, archive) stay with
production. `AddStagingDialog` lists what crosses before anything is written and reads back what was
left behind; `PromoteDialog` leads with the whole image id, the staging release and what it replaces,
production's configuration difference, and notices for release tasks (saying they are rehearsed first
when production's terms are on), pending changes and runs ahead.
Release rows show a `From staging #N` tag; the run header shows "from staging release #N · image …";
the live release line says "promoted". Settings → General has a **Promotion** section on production
(any source kind) for promotion-only; Settings → Danger zone on staging holds Remove staging; Automation
and Logs leave out previews and traffic alerts on staging.

## Decisions

- `promote` joined the closed `Operation` vocabulary (with `ValidOperation`, notifications, insights and
  the UI labels) rather than riding on `deploy` with a flag, so release history, filters, insights and
  notifications say what happened. Its trigger is `manual`: who asked, not what kind of run.
- Secrets and database links stay behind by default; `copySecrets` is the one explicit opt-in, and the
  audit names what it copied. Database links have no opt-in, and staging may not share a production
  database at all, because the guarantee that staging's migrations cannot touch production is the point.
- Promotion needs `service.control`, not `destructive`: it is a deploy of production, which needs
  `service.control`; rollback keeps its own capability.
- One staging per deployment, named `staging`: the same project read twice, not an environment builder.
- A promotion rehearses as a deploy does, under production's terms. The first version of promotion
  left `rehearse_migration` out of `PromotionStepKeys`, so a release whose migration had only ever met
  staging's data ran it on production's unrehearsed, with rehearsal on for the environment. The step is
  the same one, placed the same way, reading the same terms: nothing about a rehearsal depends on where
  the image came from, and staging's terms are never consulted — staging has no link to production's
  database, and the proof is about production's data. The step list is held to a deploy's by test so
  the crash-recovery matrices (`engine_test.go` in-process, `engine_process_fault_live_test.go` with
  SIGKILL), which now interrupt a promotion's list as well as a deploy's, keep covering every step it
  takes.

## Verification

`deploy/staging_test.go` (copy rules, refusals, promotion render with the image tagged and proven by id,
admission, retention of a queued promotion's source, promotion-only admission and watch status, rollback
provenance, summary scoping, removal scope), `deploy/rehearsal_test.go`
(`TestPromotionStepsAreADeploysFromRenderRuntimeOn`; `TestPromotionRehearsesProductionsReleaseTasksInThePromotedImage`
renders a promotion and rehearses it: the task runs by the promoted image's id with production's
`DATABASE_URL` on the copy's network alone, a failing task ends it `rehearsal_failed` with no task run
outside the rehearsal, an unavailable one `rehearsal_unavailable`), `api/handlers_deploy_staging_test.go`
(capability, session, audit, the missing-image refusal, idempotency, promotion-only kept when omitted,
the confirmation's `rehearsal` flag following production's terms),
`frontend/tests/browser/deploy-staging.spec.ts`, and the live tests against Docker:
`JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveStagingReleaseIsPromotedAsTheSameImageAndRolledBack -count=1 -v`
and `TestLiveMigrationRehearsalGatesAPromotedRelease` of the migration rehearsal family — a PostgreSQL
through the routes, a staging release of the same image promoted three times: refused
`rehearsal_unavailable` before any backup, passing against the copy and reaching production, then failing
against the copy with production's schema, rows and live release unchanged
([CONTRIBUTING](../../../CONTRIBUTING.md)).
