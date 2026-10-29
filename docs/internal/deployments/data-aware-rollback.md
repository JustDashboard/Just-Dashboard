# Data-aware rollback

A rollback moves the application back to a retained release and leaves every database as it is now:
a release's image and configuration are immutable and can be put back, its data was never the
release's to keep. Data-aware rollback keeps where each linked database stood when a release went
live, and brings that data back into a **side copy** beside production — to compare with the live
database, or to restore production from through the Databases' own routes. Production never runs
against the copy. Code: `deploy/data_rollback.go` (points, eligibility, copies' records and
lifetime), `api/deployment_data_rollback.go` (the Databases owner's side: facts, restore, sign-in,
sweeps, comparison), `api/handlers_deploy_data_rollback.go` (routes), and the restore path shared with
the [migration rehearsal](implementation.md) (`prepareCopy` in `api/deployment_rehearsal.go`).

## The data point

When a release goes live (`activate`, right after `ActivateCandidate` commits the pointer), each
database the environment links (`deploy_database_bindings`) is recorded in
`deploy_release_data_points`, once per release and database — the first activation's stands:

| Kind | When | What is kept |
| --- | --- | --- |
| `pitr` | the server writes a point-in-time archive whose oldest base backup (the kept listing's window, else the last base backup) is from before the activation | the activation moment, which the archive recovers to, and the archive id |
| `dump` | otherwise, a successful backup run holds the database's native dump started at or before the activation | that run, its job and when it started |
| `none` | neither, or an engine no copy is restored for (only PostgreSQL, MySQL and MariaDB are) | the reason |

The facts are read from the store alone (`releaseDataFacts`), so recording costs no Docker call and
never fails the activation: a failed reading is one transcript line, and the release goes live
without a point. Each point is a transcript line (`Data point: orders-db can be brought back to …`).
A release activated before this existed has no point and says so. An instant rollback re-activates a
retained release and records nothing new; an ordinary rollback is a new release with its own point.

The release list carries `dataPoints` and `dataCopy {eligible, reason}`
(`DataCopyEligibilityFor`): a production or staging release, `live` or `retained`, with at least one
`pitr` or `dump` point. A preview's releases have no copies. The what-changed comparison carries both
releases' points (`dataPoints.from/to`) and `changes.dataPoint`.

## The copy

`POST /deploy/{id}/environments/{env}/releases/{release}/data-copies` (`{afterRunId?}`) records one
`waiting` copy per restorable point (`deploy_data_copies`); at most one waiting, restoring or ready
copy per release and database (`409 data_copy_exists`). The owner (`dataCopyOwner`, a pass every 15 s
and on every request) restores one copy at a time: a copy with `afterRunId` waits for that run and
starts only if it succeeded — the rollback dialog's checkbox — and is `skipped` with the reason
otherwise.

The copy is made by the rehearsal's own path: the production server's container configuration on a
fresh volume, archiving off on the command line, none of production's ports, labels or writable
mounts; from the recorded dump run (`pg_restore`, `psql` or the MySQL/MariaDB client), or recovered by
WAL-G from the recorded archive **to the recorded moment** (a moment past the archive's end recovers to
its end, as the Databases page's recovery does). Unlike a rehearsal's copy it joins the deployment's
own database network, under `db-<connection>-copy-<copy>.jd.internal` — never production's names —
and the account the application signs in as is given a password made for the copy (letters and
digits, `ALTER ROLE` / `ALTER USER` for every host but `localhost`), so the copy's address carries no
password production accepts. The restore is bounded by the environment's rehearsal limits
(`restoreTimeoutSeconds`, `maxBackupMb`). Eligibility reads only what was recorded: a point that
retention has since taken away — the backup run deleted, or no base backup left from before the moment
— is found when the copy is restored, and the copy ends `failed` with that reason. The address and sign-in are sealed in the row and handed out
only by `GET …/data-copies/{copy}/url`, audited `deploy.data_copy.reveal` without the password.

A ready copy is kept **2 hours** (`DataCopyLifetime`); `POST …/extend {hours}` (1–24) moves the end
from now or the current end, never more than 24 hours ahead. When it ends, or on
`DELETE …/data-copies/{copy}`, its containers and volumes are removed first and the row ends
`removed` with the reason; the row is listed for a day after. A variable that names a copy's host is
refused before a release starts (`databaseURLConnectionIDs`): pointing a release at the copy is not
offered anywhere, because the copy is removed when it expires.

`GET …/data-copies/{copy}/comparison` lists the copy's tables beside the live database's with each
one's rows (`dbx.ListTables`, `dbx.Count` with five seconds per table, the planner's estimate marked
`estimated` when a count does not answer; at most 200 tables). This process reaches the copy at its
address on the deployment's network, as it is on the host's network; the live side is the saved
connection's pool.

## Cleanup and labels

Everything a copy makes carries `io.just-dashboard.managed=true`, the environment id,
`io.just-dashboard.rehearsal=copy-<id>` and `io.just-dashboard.data-copy=<id>`. At start a copy left
`restoring` ends `failed` (`SettleInterrupted`), then the rehearsal's sweep (`RemoveOrphanedRehearsals`)
removes every rehearsal-labelled container, volume and network **except** ready copies still within
their lifetime. Every pass of the owner also removes what a copy that is no longer active left
(`sweepOrphans`, by the data-copy label). Removing a deployment's database network removes its copies
first, since a copy is an endpoint there that is no application's.

The environment's network reconciler (`attachApplications`) leaves every rehearsal-labelled container
where it is. Before this change it joined a rehearsal's copy and task container — both labelled with
the environment — to the deployment's database network on its next pass, which gave the rehearsal's
internal network a way out and put production's database beside the copy under the same name.

## Routes and capabilities

| Route | Who | Audit |
| --- | --- | --- |
| `GET …/environments/{env}/data-copies[?run=&release=]` | any reader; a deploy key | — (no sign-in in it) |
| `POST …/releases/{release}/data-copies` | `system.admin`, session | `deploy.release.data_copy` |
| `GET …/data-copies/{copy}/url` | `system.admin`, session | `deploy.data_copy.reveal` |
| `POST …/data-copies/{copy}/extend` | `system.admin`, session | `deploy.data_copy.extend` |
| `DELETE …/data-copies/{copy}` | `system.admin`, session | `deploy.data_copy.remove` |
| `GET …/data-copies/{copy}/comparison` | `system.admin`, session | — |

The owner's own acts are audited as `system`: `deploy.data_copy.finish` (ready, failed, skipped) and
`deploy.data_copy.remove` (expired). None of these routes is destructive — nothing production holds is
written. Restoring production to a point is the existing route's, with its own confirmation:
`/databases/<id>/backups?recover=<moment>` opens the point-in-time recovery on that moment (a recovery
into a new server, then the destructive switch), and `/backups/<job>?run=<run>&restore=<connection>`
opens that run's dump restore (destructive, ordinary confirmation).

## UI

On Deployments a release's row carries a **Data point** tag and, under the release's verbs, **Restore
data as of this release…** (eligible releases, `system.admin`); the **Data copies** panel lists copies
with their address (revealed on request), **Compare with live**, **Keep 1 more hour**, **Restore in
place…** (a link to the route above) and **Remove now**, and says the release is never pointed at a
copy. The rollback dialog's **Also restore the database as of this release into a side copy** is off by
default; the run page links the copy a rollback asked for. What-changed has a **Data point** section.
Wording is in `components/deploy/data-rollback-model.ts`, surfaces in `components/deploy/data-copies.tsx`.

## Verification

Unit: `go test ./internal/deploy -run 'DataPoint|DataCopy|ReleaseListCarries|ActivationRecords'`
(recording at activation with an archive, dumps only and no link; eligibility; lifetime and the
waiting decision; the store), `go test ./internal/api -run 'ReleaseDataFacts|DataCopy|VariableNamingADataCopy'`
(facts from the store, capabilities, audit, secrecy, the sweep's keep-set, a restart's interruption),
`go test ./internal/store -run 'DataRollback|DataPoints'`, `bun test src/components/deploy/data-rollback-model.test.js`
and `tests/browser/deploy-data-rollback.spec.ts`. Live, as root:
`JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveDataCopy -count=1 -v`
([CONTRIBUTING](../../../CONTRIBUTING.md)).

Not verified: MySQL and MariaDB copies live (the restore is the rehearsal's, verified on MariaDB; the
password change for those engines is not exercised live), a copy of a server that requires TLS with
`verify-full` (the comparison dials the copy by address), PITR into S3, and copies of databases near
the size limits.
