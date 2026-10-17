# Reverification findings

This page records implementation/documentation discrepancies found during the 2026-09-09 source audit.
They remain open engineering findings; documenting them does not relax
[`security/invariants.md`](../security/invariants.md).

## Host command authority is not uniform

Invariant 6 requires host commands to use `hostexec` with explicit argv. The normalized deployment and
proxy paths do, but the repository still contains direct `os/exec` calls in `gitx`, `ghx`, `linuxusers`,
`selfupdate`, `procs`, and `dockerx`, in addition to the documented stored deployment shell exception.
Many direct calls intentionally use binaries pinned in the backend image against bind-mounted host paths
or the Docker socket, and Git/GitHub apply `hostexec.AsOwner`, but they do not all pass through
`hostexec.Command*`. This boundary needs an architecture decision or implementation convergence; do not
cite the presence of this page as approval for another direct executor.

`dbx` was on that list, and that part of the finding is closed. It used to start `pg_dump`, `pg_restore`,
`mysqldump`, `mysql`, `mongodump` and `mongorestore` with `os/exec` itself. The transfer work put every
tool behind one type, `toolRun` (`dbx/dump_exec.go`): the process is built by `hostexec.Command` from an
explicit argv and run by `hostexec.RunGroup` as a process group, so stopping a job stops `pg_restore`'s
workers and not only the process that forked them, and a password still reaches a tool through its
environment or a private file, never argv. The `mysql` and `psql` clients are not started for a restore
at all any more: a dump that is a SQL script is replayed over the dashboard's own connection
(`dbx/dump_script.go`, invariant 6). No file of the package imports `os/exec`, and
`TestNothingHereStartsAProcessOfItsOwn` reads every non-test file of it and fails on one that does. The
Databases handlers in `api` that reach the host — the host account bootstrap (`handlers_db_host.go`) and
a unit's start, stop and restart (`handlers_db_connection.go`) — build their commands with
`hostexec.CommandOnHost` and `CommandOnHostAsUser` as well; they import `os/exec` only for the
`*exec.Cmd` type.

## Path containment has parallel implementations

The security contract names `files.Resolve`/`ResolveEntry` as the client-path choke point. Current Git
routes instead use `gitx.Resolve`, which independently cleans, resolves symlinks, and checks
`JD_GIT_ROOTS`. The 0.6.7 audit remediation closes the backup discrepancy: save, test and execution
validate sources/local targets through `files.Resolve`; artifact reads and retention validate their
stored paths under current roots as well. Git's parallel resolver remains a design-convergence item.

## PostgreSQL statement statistics keep utility literals

Found on 2026-10-17, after the audit above. The Databases reference holds the statement statistics
(`GET /databases/{id}/statements`, read capability) to a statement's shape, never one execution. On
PostgreSQL the text is `pg_stat_statements.query` as the extension keeps it, and the extension
replaces constants only in the statements it plans (`SELECT`, `INSERT`, `UPDATE`, `DELETE`, `MERGE`).
Utility statements are kept as they were sent while `pg_stat_statements.track_utility` is on, which is
its default. Checked against PostgreSQL 17.11: `CREATE ROLE … PASSWORD '…'` and
`ALTER ROLE … PASSWORD '…'` were listed with the password in clear, as were a `SET` value, a `COMMENT`,
a column `DEFAULT` and a `DO` body, while the `SELECT`, `INSERT` and `UPDATE` run beside them read
`$1`. The dashboard's own role form sends a new password that way (`dbx.passwordLiteral`), so on a
server that loads the extension an account that may only read can be shown a password an
administrator set from the dashboard. The Performance page's Statements panel tells its reader the
same rule ("the server replaces every value before it leaves").

The other engines held the rule in the same check: MySQL 8.4's digest text and ClickHouse 25.8's
`normalizeQuery` replaced every literal, and `dbx.statementShape` does so for SQL Server and Oracle
(`TestStatementShapeDropsLiteralsAndNothingElse`); MariaDB 11.4 ships with `performance_schema` off and
lists nothing. The code is unchanged. Closing the gap — shaping PostgreSQL's text here as well, by a
pass that keeps the `$n` markers the Statements panel asks values for (`dbx.statementShape` reads `$1`
as SQL Server's money literal), or leaving utility statements out of the list — is a decision still
to be made, and until then the rule stands as the contract.

## Verified inventory drift corrected in this documentation change

- The audit remediation raises `backend/go.mod` and the backend Docker build to Go 1.26.8 for standard-library security fixes.
- 33 `backend/internal` packages contained tests at that audit. 38 of the 39 do now, every one but
  `audit`.
- `frontend/src/app` contained 48 page entry files at that audit, not 18; three deployment wrappers
  are server components. The count is 90 since the Databases rebuild; the
  [repository map](repository-map.md#frontend) carries the current figure.
- `api.moduleSet` contains ten deployment components, not two.
- The historical 0.6.7 delivery ledger is absent from this checkout; C0–C7 completion claims cannot be verified from that ledger. See the deployment guide for current runtime limits.
- The SQLite inventory now includes saved database queries/history and the expanded normalized deployment
  tables instead of describing only the earlier schema.
