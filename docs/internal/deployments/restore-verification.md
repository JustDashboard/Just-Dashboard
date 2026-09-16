# Application restore verification

Backups can extract an exact recorded artifact into private temporary storage and run a pinned
application checker against the restored files. This gives deployment restore policies a usable
evidence path. It proves the checks that application performed for that artifact and schema; it
does not make all filesystem archives transactionally consistent or prove every application record.

## Configuration and operator flow

Open **Backups → New job/Edit → Consistency and recovery checks**. A recovery plan contains:

- An immutable local image ID (`sha256:…`) or repository digest (`registry/app@sha256:…`). The image
  must already be retained or pulled on this server. The checker runs its resolved image ID.
- A checker executable and explicit arguments, entered one per line. It runs inside that image.
- The expected application schema version and canary output. Only the output's SHA-256 fingerprint
  is persisted; leading/trailing whitespace is ignored. Output must be nonempty and under 64 KiB.
- An optional automatic check after each successful backup. The API also accepts `timeoutSeconds`
  (1–300, default 60) and `maxBytes` (up to 1 TiB, default 16 GiB) for larger checks.

The application must supply a checker that opens its restored data, checks the actual schema and
canary record through its usual data access code, prints the expected result, and exits zero. A
command that merely prints a constant does not establish recovery. `JD_RESTORE_SCHEMA_VERSION`
contains the configured schema expectation. `JD_RESTORE_ROOT=/restore` points to the restored data;
sources retain their manifest namespaces (`/restore/source-0001`, `/restore/source-0002`, etc.).
See the [SQLite fixture's shared application/checker code](../../../backend/internal/api/testdata/sqlite-recovery/app.py).

Run **Verify restore** in backup history to test an existing artifact. Status and cleanup results
appear with that run; Log includes the check ID, schema and actual application image ID. A failed
check can be retried. The session/API must have `system.admin`. This operation creates and removes
its own temporary copy; the existing destination restore still uses the destructive route and typed
destination confirmation because it overwrites operator-selected data.

Deployments with **Require restore evidence** first check coverage and artifact integrity, then
reuse a matching successful check or execute the job's configured checker. Missing configuration,
corruption, incompatible schema, wrong canary, command failure or incomplete cleanup blocks activation.
Backup success and recovery verification remain separate statuses: a usable archive is retained even
when its application check fails. Gate evidence includes the exact restore-test ID, image and schema.

## Isolation, persistence and cleanup

Backups pins retention while reading, copies and re-hashes the archive into private staging, and
extracts without accepting skipped unsafe entries. Verification extraction limits entries to 200,000,
enforces the configured byte limit and reserves 256 MiB of free space before each file. File ownership
is restored when running as root, so a non-root image can access its own data. Source paths and local
artifacts continue through the file resolver. Staging must be outside every backup source.

The Docker owner runs one disposable container with only the restored directory bound at `/restore`.
It supplies no deployment variables or database credentials, publishes no ports, and uses
[`network none`](https://docs.docker.com/engine/network/drivers/none/). The checker has a 512 MiB memory
limit, one CPU and 128 PIDs. The image's own environment and restored files still belong to that
application; operators choose and retain the checker image. No request-built host shell is added.

The additive `backup_restore_tests` table records artifact SHA-256, full manifest digest, recovery-plan
digest, actual image ID, schema, canary-output digest, timing, extraction counts and cleanup state.
Private workspace paths and ownership keys are not returned. The job's `recovery_json` and `sqlite_paths`
columns have additive defaults. Older jobs remain ordinary filesystem backups and old manifest-less
archives cannot acquire verified coverage.

Cleanup matches the recorded restore-test ID and random ownership label, removes only those containers
and their anonymous volumes, then deletes the owned workspace. A cleanup failure is never accepted as
a passed check. Startup, or a later retry, cleans interrupted checks and records them as failed rather
than resuming them as successes. Evidence survives request cancellation. Jobs and retention cannot
discard an active or cleanup-pending check's ownership records. A fresh server marks unfinished archive
runs failed before scheduling new work; it cannot resume a partial archive as successful.

## SQLite consistency and current limits

`sqlitePaths` lists regular database files inside the job's sources. Backups asks the Databases owner
for a native `VACUUM INTO` snapshot before archiving. The snapshot replaces that database's bytes in
the archive, and its live `-wal`, `-shm` and `-journal` sidecars are omitted. The immutable manifest records
the source, method and snapshot digest. Missing, excluded or failed snapshots make the backup fail.
The protocol produces a transactionally consistent SQLite copy, as described in
[SQLite's VACUUM INTO contract](https://www.sqlite.org/lang_vacuum.html#vacuum_with_an_into_clause).

Each SQLite file has its own transaction boundary; several databases and arbitrary files are not one
atomic application snapshot. SQLite warns that VACUUM can change implicit ROWIDs in tables without an
explicit INTEGER PRIMARY KEY. Applications depending on those identities need a suitable alternative
capture protocol. Other files retain ordinary filesystem capture and its changed-file refusal.

This implementation does not yet provide deployment-integrated native PostgreSQL/MySQL/MongoDB/Redis
capture, multi-container restore orchestration, external-service recovery or PITR. Copying their live
data directories is not promoted to an application-consistent backup by this feature. Application
checker coverage and actual production RPO/RTO must be established for each supported workload.

## Verification

`JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveSQLiteApplicationRecoveryVerification -count=1 -v`
builds a real application, reads its canary over loopback HTTP, captures a live WAL-mode SQLite snapshot,
drops only the fixture's original table, and verifies the archived record through the same application
data access code in an isolated container. It checks sandbox configuration, exact-artifact gate reuse,
rejection of an incompatible schema, and cleanup preserving the source application. This is a local
application recovery drill, not a clean-host install or a public traffic/RPO/RTO acceptance result.

Component tests cover artifact/manifest/image/schema/canary binding, corruption, extraction limits,
cancellation, restart cleanup, retention pinning, concurrent checks and role enforcement. Browser
coverage saves the profile, exposes a failed check, and retries to display successful evidence.
