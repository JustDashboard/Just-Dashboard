# Decision: permanent deletion of archived deployment records

The archive contract remains unchanged: `DELETE /deploy/{id}` and `POST /deploy/{id}/archive`
disable automation and retain deployment history and resource ownership. The workspace calls this
**Archive deployment**. An archive is not a permanent delete.

A separate `DELETE /deploy/{id}/permanent` operation removes the dashboard records of an already
archived project. It runs behind `s.destructive`, uses ordinary confirmation (the existing deploy
project confirmation policy), and records `deploy.project.purge` with the original display name,
deployment ID, and `resourcesRemoved: false`. Missing projects return 404. Unarchived projects and
projects with unfinished engine or legacy runs return 409. Unknown engine states fail closed.

The transaction checks archive and run state before deleting notification delivery records, committed
drafts, runs, and the project. Foreign keys remove its environments, variable revisions, releases,
configuration, triggers, schedules, database network bindings, and ownership joins. Runs are deleted before variables because
run-to-variable revision references intentionally do not cascade from the variable side. The audit
log is outside this cascade and survives deletion.

This operation does not invoke Docker, Proxy, Files, Backups, or a host command. Containers, images,
routes, database networks, persistent storage, checkouts, and on-disk artifacts remain. Database network
reconciliation ends when its binding records are deleted. Their deployment ownership and
rollback history are forgotten. Operators wanting managed resources removed must use the existing
previewed Configuration removal flow first; it retains its per-target capability and typed-phrase
rules. The permanent-delete dialog explicitly explains both the record loss and retained resources.

`GET /deploy/?view=archived` lists archived projects without inspecting their Git checkouts. The
archive page provides search, links to retained configuration/history, and permanent deletion. The
same deletion control is available on an archived project's detail page. Single-project summaries
include archived records and use the original display name; the fleet query still excludes them. Errors keep the
confirmation open and success refreshes the archive.

Verification covers destructive capability enforcement, archive and active-run refusals, cascading
variable/history cleanup, host resource preservation, audit persistence, repeat deletion, and the
browser cancel/error/retry flow. Existing archive route behavior remains covered separately.
