# Staging and promotion

A deployment can have one **staging** environment beside production: its own branch, domain,
variables, volumes and database links, built and deployed exactly as production is. A staging release
can then be **promoted**: production runs that release's exact image — the same local image id —
with production's own configuration and variables, and builds nothing. On one host the image is
already local, so no registry is involved. Staging itself takes one thing besides its own builds: a
pull request preview's image, promoted the same way with staging's configuration, so the chain is
preview → staging → production with nothing rebuilt on the way
([below](#promoting-a-pull-requests-preview-into-staging)).

Code: `deploy/staging.go` (the environment and its removal), `deploy/promotion.go` (admission, the
render step, the confirmation's read, retention), `deploy/promotion_preview.go` (a preview as the
source: eligibility, its refusals, the record), `api/handlers_deploy_staging.go` (routes).
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
what deploying production needs, since it changes production the same way; token-usable like a deploy,
by a deploy key too (`jd promote`, which takes staging's live release unless `--release` names one);
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
`start_candidate`, `verify_readiness`, `verify_smoke`, `replay_traffic`, `activate`, `retire_previous`,
`record_release`, `notify`), with no source, plan analysis, context or build step; `TestPromotionStepsAreADeploysFromRenderRuntimeOn`
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
they were. Then the certificate, the candidate, readiness and smoke checks, production's
[traffic replay](implementation.md) when production's plan turns it on (production's request record
replayed against the promoted image before the cutover), blue/green or stop-first activation, automatic restore of the previous release on a failed activation, retirement, notification
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
(any signed-in reader; `…/{staging}/promotion?source=preview&preview=N` reads a preview's into staging,
with `source.kind` `"preview"` and `source.preview {number, title, url, author, headRepository, fork,
approvedBy, approvalState}`, an unapproved commit, a missing image, an incompatible source or a shell
release task as `blockers` with the codes above, and a preview with nothing live to promote a `422`):
the source release, the image id, digest and size and whether Docker holds it,
production's live release, queued runs, promotion-only flag, pending changes, release task names and
whether production's rehearsal terms are on (`target.rehearsal`), the
blockers that would refuse it now, and `detail` — `compareRuntimeSnapshots` between the staging release's
snapshot and what production would freeze if queued now (`desiredRuntimeSnapshot`, the same rows
`Enqueue` snapshots): runtime fields, variables by name and value digest only, dependencies, checks and
domains.

## Promoting a pull request's preview into staging

The same route on staging's id: `POST /api/v1/deploy/{id}/environments/{staging}/promote`
`{source: "preview", preview: N, releaseId?}` (`source` omitted or `"staging"` is the promotion above,
which still needs `releaseId`; `preview` without `source: "preview"` is `400`). It queues a staging run
with operation `promote` whose `render_runtime` is `renderPromotedRuntime` exactly as production's is:
staging's desired plan revision, staging's variables snapshotted at enqueue, the preview's image tagged
`just-dashboard/deployment-<staging>:run-<id>` and read back by its id, and the candidate release frozen
from staging's plan, variables, dependencies, checks and domains around it. Nothing of the preview's
configuration crosses — not its plan, not its variables, so neither its own secrets nor the production
values a dashboard preview may have copied in (which isolation keeps from being anything of staging's).

Who: the route stays `service.control` (deploying staging needs no more), and a preview source also
needs `system.admin` — `403 forbidden` "promoting a pull request's preview takes an administrator, as
approving its commit does" — because it puts a pull request's code in front of staging's variables. A
session, an administrator's API key or a deploy key (which carries the administrator's role, and whose
`POST …/promote` line in `deployKeyRoutes` already admits it) may; approving the commit stays a
person's in the dashboard. The request is audited `deploy.release.promote` before anything can refuse
it (target `PR #N`, `sourceKind`, `previewNumber`), so a refusal is recorded under that action with
`success` false; an accepted one replaces the detail with the run, the preview release and its number,
the image id and digest, the commit, `pullRequestUrl` and `approvedBy`.

Eligibility, checked when asked, again inside the enqueue transaction, and every refusal a `422` whose
code and words name the rule (a `PreviewPromotionRefusal`, which is also `ErrPromotionSource`,
`ErrPromotionTarget`, `ErrPromotionImageMissing` or `ErrPromotionIncompatible`, so the engine's step
codes read it as a staging refusal):

| Refusal | When |
| --- | --- |
| `preview_promotion_target` | the target is production (or anything but staging): "promoted into staging, never straight into production". The confirmation's read refuses it the same way. |
| `preview_promotion_not_found` | the project has no preview of that pull request |
| `preview_promotion_closed`, `preview_promotion_quarantined` | the preview is closed or archived, or predates isolation |
| `preview_promotion_not_live` | no live release, or `releaseId` names anything but the preview's live release |
| `preview_promotion_unapproved` | the release's exact commit has no current administrator approval: never approved, pending, rejected, superseded by a newer head, or closed. A fork's says so first: "PR #N comes from a fork (owner/name), and a fork's code is promoted only at a commit an administrator approved: …" |
| `preview_promotion_image_missing` | the artifact is no longer `available`, or Docker no longer holds the image by its id |
| `preview_promotion_incompatible` | not exactly one image, a Compose release, or staging deploys another kind of source or another repository |
| `preview_promotion_shell_task` | one of staging's release tasks runs in the dashboard's shell |

The last one is deliberate. A shell task runs against a checkout of the release's commit — here the
pull request's — on the dashboard's own host; a preview never runs a release task and builds only in
BuildKit, and an approved commit was approved as code that runs in containers. A task with
`runner: "image"` runs in a container of the promoted image itself, which promoting it already decided
to run, so it is allowed and runs with staging's variables. The same rule follows the image on: a
staging release whose `promotion` names a preview is refused into a production whose release tasks
include a shell one (`preview_promotion_shell_task`, naming production's task), and production's record
of that promotion keeps the pull request (`sourceKind: "staging"` with `preview`). The routes refuse
early, in words; the `release_task` step holds the rule itself for every run whose record names a pull
request — the promotion's metadata or the candidate release's, which a redeploy or rollback clones —
and fails `promotion_refused` before any checkout is made.

The run's metadata and the staging release's provenance carry the usual `promotion` with
`sourceKind: "preview"` and `preview {number, url, repository, headRepository, approvedBy}` (staging
promotions now record `sourceKind: "staging"`; a record without one is staging's). The run executes with
the preview release's source identity — `refs/pull/N/head` at the approved commit — so the release
says where its code came from. A queued promotion is re-read when it renders: the image must be the one
chosen and still present, and the approval must not have been withdrawn (rejected); the pull request may
have been merged and the preview closed meanwhile, since retention holds the image ("source of an
unfinished promotion to staging") and the release frozen at the request is the one an administrator
decided on. Notifications call it "Promotion from a pull request preview".

Afterwards staging is staging: its next build of its own branch (a push, when automatic) replaces the
promoted release; rollback, the warm window and promotion of that release into production work as for
any staging release. `GET /deploy/{id}/pull-requests`, `/previews` and the Git page's listing carry each
preview's newest promotion as `promotion {runId, runNumber, state, environmentId, releaseNumber,
sourceRevision, requestedAt, endedAt}`. Closing the preview afterwards removes the preview's
containers, volumes and network; staging's release holds its own tag of the image.

`jd promote APP --from-preview N [--wait]` posts exactly this to the deployment's staging environment
(found by kind; refused before any request without one, and `--release` cannot be combined with it);
whether the preview may go is the server's answer, printed in its words.

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
the live release line says "promoted". A staging release promoted from a preview shows
`Promoted from PR #N (sha)` in its row (the title names the approver), "promoted from PR #N (sha)" in
its run's header — linked to the preview's run — in the header's live release line, which every
staging page draws, and on staging's Overview, whose Live release card reads "promoted from PR #N (sha)"
where production's reads "promoted from staging". On production's Overview the Pull requests panel's rows
offer **Promote to staging** (`pull-request-verbs.tsx`) to an administrator when the project has
staging and the preview is open, isolated, approved and serving a live release (`canPromotePreview`,
`lib/pull-requests.ts`); it opens `PromoteDialog` with `preview`, which skips the release choice and
reads the preview's confirmation: the image the preview built, PR #N · preview release #M with its
commit, who approved it, a fork's warning, "The release runs with staging's plan and variables as they
are saved now, not the preview's", staging's configuration difference ("only in staging" / "only in
preview") and staging's release-task notice, and posts `{source: "preview", preview: N}`. A preview
whose image went on shows a `Promoted to staging` tag on its row (a "Promoting to staging" status
while that run is in flight; `previewPromotionStatus`). Settings → General has a **Promotion** section on production
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
- A preview's image goes into staging, never straight into production. Production's promise is that
  it runs only what staging ran with staging's tasks and checks; a preview has no release tasks and no
  shared data to have been tested against. The refusals are `422` rather than staging's `409`: the
  request is well formed and what it asks is refused by rule, in words that say which.
- Promoting a preview takes `system.admin` on top of the route's `service.control`, since it is what
  makes a pull request's code meet staging's variables — the decision approving the commit was. A
  deploy key keeps the promote line it has (it carries the administrator role); the approval itself
  stays a person's.
- Eligibility is strict when asked and lenient where the world moved on its own: at the request the
  approval must be the pull request's current one, the preview open and its release live; when the
  queued run renders, only a withdrawn (rejected) approval or a changed or missing image stops it, so
  merging the pull request right after promoting does not undo the promotion.
- Staging's shell release tasks refuse a preview (and production's refuse a release that came from
  one): approving a commit was approving container code, and a shell task would run the pull request's
  checkout on the host. Tasks in the release image are the supported way to migrate with a promoted
  preview.
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
`deploy/promotion_preview_test.go` (an approved preview promoted into staging with staging's variables
and plan and the record, the transcript and the listing's `promotion`, and the release-task step refusing
a shell task over it for the promotion and for a rollback's clone; refusals: into production, an
unapproved fork, a superseded head, nothing live, no preview, a release the preview is not serving, a
shell task, a missing and a pruned image; a queued promotion surviving the pull request's close but not
a rejection; the chain into production held to the shell-task rule, its record still naming the pull
request; the pull request URL per forge; the
notification's words), `api/handlers_deploy_preview_promotion_test.go` (a preview built through a
signed delivery and the approval route: the administrator gate, an operator's `403`, bad requests,
production's `422`, the confirmation, a deploy key's promotion and its audit, a refusal audited under
the same action), `cmd/jd` (`TestPromoteFromPreviewLandsInStaging`),
`frontend/tests/browser/deploy-staging.spec.ts`, and the live tests against Docker:
`JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveStagingReleaseIsPromotedAsTheSameImageAndRolledBack -count=1 -v`
(which ends by building PR #41's preview through a signed delivery and the approval route — the
remote's release mirror pointed at a local origin, since a preview fetches `refs/pull/N/head` — refusing
it into production and promoting it into staging, where staging runs the very image id the preview
built, serves and runs its release task with staging's `GREETING`, carries none of the preview's
variables, records PR #41, its commit and its approver, and keeps serving after the preview closes)
and `TestLiveMigrationRehearsalGatesAPromotedRelease` of the migration rehearsal family — a PostgreSQL
through the routes, a staging release of the same image promoted three times: refused
`rehearsal_unavailable` before any backup, passing against the copy and reaching production, then failing
against the copy with production's schema, rows and live release unchanged
([CONTRIBUTING](../../../CONTRIBUTING.md)).
