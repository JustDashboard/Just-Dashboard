# Recovered build and startup artifacts

Imports retain the live Docker image or native manager as independent baseline evidence. A verified
local Compose build context, or a supported native source directory, is captured into the private
`JD_DATA_DIR/deployment-recovery/sources/<sha256>/tree` store. The source mode is
`recovered_snapshot`; its server-issued resource ID selects the content-addressed tree. Client source
steps cannot create or replace that handle. Source inspection and build materialization verify the
whole tree and copy it into an isolated workspace before applying reviewed Compose documents.

Snapshots use the existing file-count, byte-count, no-follow, regular-file and contained-symlink
limits. Private files and linked application data stay outside the build tree. Identical captures
share the same snapshot; the dashboard never rewrites its tree to apply later configuration edits.
Source detection reports per-service framework/role confidence. Unavailable or unreproducible build
inputs keep the pinned image fallback, with an explicit reason. Historical private or unresolved
Compose build arguments are not guessed from current runtime variables.

Native recovery binds build selection to the captured entrypoint or supported Python module. Node
and Python recipes preserve the full source layout under `/app`, retain the captured startup argv,
and pin the supported interpreter major/minor. Version probes execute only a classified, hash-fenced
captured ELF interpreter as its original account with fixed version flags, a sanitized environment
and a bounded timeout. Build input scopes include source-detected build/install reads and framework
browser prefixes, while baseline variables remain the original runtime inputs. Other native
interpreters and wrappers require an explicit verified command/layout migration; a Dockerfile alone
does not establish support. Absolute external command paths remain `host_command_unsupported`
unless the importer has a verified container mapping for them.

The server prunes source snapshots once at startup and every 24 hours. Every persisted source
revision and unexpired draft protects its snapshot. Unreferenced snapshots receive a full 30-day
draft lifetime after their last capture, including the gap before a draft is committed. A shared
store lock serializes publication and retention; reuse refreshes retention without changing source
content. Cleanup stays inside the private snapshot store and does not follow external directory
symlinks or delete original checkouts, live containers, image baselines or application data.

Selected native startup authority is retained separately in encrypted `native-startup` artifacts
with a durable encrypted journal. Import writes these private artifacts only. A later deployment
retires verified selected PM2 saved entries or direct systemd target links; restoration precedes
original-manager restart on failed cutover and baseline rollback. Per-plan locks, adapter authority
locks and byte/link fingerprints fence retries and foreign changes. Retained startup journals are
kept for native rollback; source snapshot cleanup never removes restart authority.

Focused retention validation is `go test ./internal/deploy -run '^TestRecoveredSnapshot'`.
Owner integration validation is `go test ./internal/deploy -run '^TestNativeStartupOwner'`;
the isolated native lifecycle fixtures in `existing-workloads.md` exercise real manager cutover,
saved startup retirement and baseline restoration.
