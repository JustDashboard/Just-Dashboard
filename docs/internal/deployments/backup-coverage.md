# Deployment backup coverage

The deployment gate collects writable top-level mounts, linked storage dependencies and the actual
merged Compose service mounts. Explicitly linked saved databases contribute their SQLite file or all
writable mounts of the matched local Docker database. Unresolved, external or ephemeral database storage
requires a supported native backup adapter and blocks a configured gate; an unrelated file archive
cannot silently satisfy a linked database dependency.
Compose normalization uses the release's source, ordered files and frozen runtime variables through
the Docker owner; normalized output is kept internal because it can contain secrets. Bind paths pass
through `files.Resolve`. Named volumes must inspect as local Docker volumes with a resolvable absolute
mountpoint. Unknown, anonymous or unresolved sources block the gate rather than disappearing from coverage.

Backups persist `manifest_json` on each successful run with a version, resolved source paths, disjoint
archive roots, exclusions, original destination and SHA-256 digest. The migration is additive; old runs
with no manifest remain listable/restorable but cannot satisfy the coverage gate. The gate checks both
the current job and the completed run: editing a job cannot make an old artifact cover new data.

Full persistent-data coverage rejects any exclusion filter. Every requested path must be under a recorded
source and present in the verified tar archive. Checking actual entries prevents a directory created after
the backup from being considered covered merely because its parent was backed up. Missing, incomplete,
corrupt or filtered artifacts fail closed. Archive readers pin retention and validate the artifact before
using it; remote retrieval and retention use the original recorded destination after job edits.

Each source restores under its `source-NNNN` root. This avoids collisions between named volumes whose
physical directories are both called `_data`. A failed walk/read or a file changed during copying makes
the run fail; partial archives cannot be reported as complete coverage.

Coverage is not application consistency. Filesystem archives of running databases do not prove a
recoverable transaction boundary. Jobs capture explicitly selected SQLite files through the Databases
owner's native snapshot, and they capture **native dumps of saved database connections**
(`backup_jobs.database_dumps`, additive): every run asks the Databases owner (`backupDatabaseDumper`,
which opens the sealed DSN like every Databases route and calls `dbx.Dump`) for `pg_dump`,
`mysqldump`, `mongodump`, a Redis snapshot or the built-in driver dump of each connection, stores the
file under `database-NNNN/<file>` beside the `source-NNNN` trees, and records
`manifest.databaseDumps` (connection, engine, database, method, file, archive path, SHA-256, bytes).
A failed dump fails the run; a runner without the dump owner fails rather than pretending. The
deployment gate accepts a native dump as coverage for a linked database (`Manifest.CoversDatabase`
plus `Runner.VerifyDatabaseCoverage`, which checks the archived entry and its recorded size) before
falling back to the engine's files; a run taken before the dump was configured cannot satisfy it.
`POST /backups/runs/{runID}/restore-database` (destructive capability, typed confirmation of the target
database name, audited) extracts one recorded dump bounded by its manifest size, verifies its digest
and hands it to the Databases owner; naming another database on the same server is the restore drill.
`JD_DEPLOY_LIVE=1 go test ./internal/api -run TestLiveBackupDumpsAndRestoresAPostgresDatabase` provisions
a real PostgreSQL server, dumps a canary row, deletes it live and restores it into a drill database.
Jobs can also run an isolated application checker against the exact artifact. The gate accepts only
persisted, matching application/schema/canary evidence with completed cleanup. Multi-container recovery
remains pending for F02. Never infer restore success from archive existence, checksum validity, or
source coverage alone. See [application restore verification](restore-verification.md) for
configuration, isolation and limits.

Tests cover omitted binds/named volumes, changed job definitions, colliding basenames, corrupt artifacts,
filters and original destinations. The opt-in `TestLiveDeploymentComposeStorageUsesMergedMountsAndFrozenVariables`
uses the real Compose parser to verify merged mounts and frozen interpolation.
