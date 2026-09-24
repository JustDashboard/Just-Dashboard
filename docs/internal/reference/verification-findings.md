# Reverification findings

This page records implementation/documentation discrepancies found during the 2026-09-09 source audit.
They remain open engineering findings; documenting them does not relax
[`security/invariants.md`](../security/invariants.md).

## Host command authority is not uniform

Invariant 6 requires host commands to use `hostexec` with explicit argv. The normalized deployment and
proxy paths do, but the repository still contains direct `os/exec` calls in `gitx`, `ghx`, `linuxusers`,
`selfupdate`, `procs`, `dbx`, and `dockerx`, in addition to the documented stored deployment shell
exception. Many direct calls intentionally use binaries pinned in the backend image against bind-mounted
host paths or the Docker socket, and Git/GitHub apply `hostexec.AsOwner`, but they do not all pass through
`hostexec.Command*`. This boundary needs an architecture decision or implementation convergence; do not
cite the presence of this page as approval for another direct executor.

## Path containment has parallel implementations

The security contract names `files.Resolve`/`ResolveEntry` as the client-path choke point. Current Git
routes instead use `gitx.Resolve`, which independently cleans, resolves symlinks, and checks
`JD_GIT_ROOTS`. The 0.6.7 audit remediation closes the backup discrepancy: save, test and execution
validate sources/local targets through `files.Resolve`; artifact reads and retention validate their
stored paths under current roots as well. Git's parallel resolver remains a design-convergence item.

## A deployment's Logs verb names a container the Logs page does not read

Found in the 2026-09-24 deployment pass. A Runtime service's Logs verb (`project-runtime.tsx`) and the
game console's Logs button (`game/console.tsx`) open `/deploy/{id}/logs?service=<container>`, but the
project Logs page reads only `view` and `moment`, so the parameter selects nothing: the reader lands on
the project's requests, not on that container's output. The container's own output is one press further,
through Open in Docker, and a run's runtime-log sources reach it directly through the host Logs page's
`source=docker:<id>`. Either the Logs page learns to open a named container or the verbs stop carrying
a parameter that promises it; the documentation describes the behaviour as it is until then.

## Verified inventory drift corrected in this documentation change

- The audit remediation raises `backend/go.mod` and the backend Docker build to Go 1.26.8 for standard-library security fixes.
- 33 `backend/internal` packages currently contain tests.
- `frontend/src/app` contains 48 page entry files, not 18; three deployment wrappers are server components.
- `api.moduleSet` contains ten deployment components, not two.
- The historical 0.6.7 delivery ledger is absent from this checkout; C0–C7 completion claims cannot be verified from that ledger. See the deployment guide for current runtime limits.
- The SQLite inventory now includes saved database queries/history and the expanded normalized deployment
  tables instead of describing only the earlier schema.
