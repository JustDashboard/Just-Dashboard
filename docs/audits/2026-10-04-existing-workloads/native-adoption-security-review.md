# Managed workload adoption security review

Reviewed the integrated adoption API/store path and native capture/runtime adapters on 2026-10-04.
The review focused on retaining the existing workload, preventing foreign-resource claims,
keeping original private configuration separate from edited desired configuration, and refusing
unsafe restart authority. It is a scoped code and fixture review, not a claim that arbitrary
applications can be migrated without operator compatibility checks.

## API and storage boundaries

- Recovery and adoption routes in `backend/internal/api/handlers_deploy.go` require both a system
  administrator capability and an interactive session. They remain behind the established network
  allowlist, authentication, per-account second factor and browser CSRF middleware in `routes.go`.
- `handleDeploymentWorkloadRecover` accepts an inspected key/digest and name; the server selects and
  captures the authoritative resource. It does not accept client-supplied source/runtime ownership.
  `adoptRecoveredWorkload` obtains a new discovery/capture and compares topology, baseline digest and
  exact runtime metadata before commit. Committed retries are idempotent.
- `assertAdoptionUnownedTx` in `workload_adoption_store.go` checks exact manager resource ownership and
  overlapping Docker container IDs. Archived reservations remain owned. Baseline release, source,
  configuration, artifacts, original variables, runtime and live pointer commit atomically; no
  activation is queued during adoption.
- Generic draft commit rejects the adoption path. Draft source edits cannot substitute an adoption
  source; configuration saves cannot inject runtime dependencies. `retainRuntimeOwnershipTx` in
  `configuration_store.go` keeps server-owned runtime authority and rejects foreign replacements.
  Duplicate plans drop original runtime authority and Compose namespace identity.
- `RecordCandidateRuntime` in `activation_store.go` requires the active engine lease, candidate
  ownership and an artifact-digest-verified baseline snapshot. PM2/systemd identities must exactly
  match that immutable server-recorded snapshot; ordinary Docker candidates cannot claim a native
  unit or daemon.
- Public recovery/draft JSON omits original and desired environment values. `adoption_enc` and
  `environment_enc` are independently sealed, so desired path translation or settings edits do not
  rewrite original baseline variables. Manager errors remain behind safe API messages. Audit data
  records identities/counts, not original argv or variable values. `adoption_enc` is additive in both
  new schema creation and `store.addedColumns`, with an empty default for existing installations.

## Findings addressed during implementation

1. **High, running source drift:** a current filesystem snapshot can differ from modules already
   loaded by a running process. `RecoverHostWorkload` blocks known code or recognizable private
   startup-file edits later than the verified process birth time, allowing two seconds for clock
   precision. Native control rechecks original module/private-file contents and directory ownership
   and permissions before manager mutation. Regression tests cover changed modules with an unchanged
   entrypoint, private content/additions and source-root permission changes.
   Locations: `workload_adoption_host.go:263` (`knownHostSourceDrift`),
   `runtime_native.go:133` (`capture`) and `source_local_directory.go:96` (`nativeDirectoryDigest`).
2. **High, native path/configuration loss:** library/interpreter filesystem dependencies may not
   appear in source variable detection. Recovery now blocks unknown external filesystem values,
   external module/CA paths, loader or unknown `NODE_OPTIONS`, and ambiguous/escaping argv paths.
   Contained paths translate into `/app` while original values remain independently sealed.
   Standard manager/tool metadata has a specific review warning. Network URLs remain unchanged.
   Locations: `workload_adoption_host.go:381` (`translateHostRuntimeEnvironment`) and
   `workload_adoption_host.go:522` (`hostCommandForContainer`).
3. **Medium, credential files copied into images:** recognizable registry/account/cloud/SSH and
   key/keystore files are excluded from source copies and block automatic application-source recovery.
   Native restart evidence hashes original private contents without publishing or copying them into
   images. Regression tests verify credential files do not enter staged build copies.
   Location: `source_local_directory.go:75` (`privateSourceEntry`).
4. **Medium, host/container source identity:** a custom root may name an unrelated directory inside
   the dashboard image when the host directory has not been mounted. Recovery requires filesystem
   identity between the resolved dashboard-visible directory and its authoritative host path before
   analyzing or committing a baseline. A regression rejects another directory with matching source
   content and an unavailable host path. Location: `workload_adoption_host.go` (`hostSourceDirectoryMatches`).

5. **High, original startup resurrection:** a stopped enabled systemd unit or saved PM2 process can
   return after reboot alongside its Docker replacement. Matching entries in either saved PM2 list
   block; malformed/unreadable lists fail closed. Systemd requires a disabled original unit, rejects
   known loaded activation/control relationships, and inspects a bounded installed service/timer/path/socket/
   target and alias inventory for unloaded activation references. Matching private evidence is fenced
   into configuration identity and rechecked before lifecycle control. No enablement or shared saved
   list is rewritten. Regression tests cover unloaded installed timers, alias/default triggers,
   ordinary service dependencies, explicit template references, ambiguous enabled templates,
   unreadable private startup data and both PM2 dumps. The real PM2 adapter also proves a newly added
   saved entry prevents stop while its original HTTP server and PID remain unchanged.
6. **High, discarded native unit policy:** explicit unsupported unit directives, including resource
   limits, rlimits, scheduling and additional execution/security/dependency behavior, block recovery.
   Inherited host/kernel/manager policies cannot be universally reconstructed; every native review
   carries an actionable compatibility warning requiring application-relevant Docker/runtime policy.

## Real adapter validation

Separate owned PM2 daemons and uniquely named owned persistent systemd units were exercised through
the ordinary deployment engine with temporary stores, source directories and retained data. The
fixtures verified no restart on adoption, native logs, private/empty variables, original environment
snapshot isolation, UID preservation, `/app` working-directory and absolute data-path translation,
successful HTTP serving, failed built candidate compensation, Docker logs, successful Docker
migration and native baseline rollback. They clean up only their exact created resources.

Sanitized result files are `pm2-managed-adoption.json` and `systemd-managed-adoption.json` in this
directory. Focused native/source regression tests, race tests and the changed-files gate accompany
the implementation; the root task records the complete integrated verification.

## Remaining production boundaries

- Filesystem timestamps and hashes cannot reconstruct in-memory modules, dynamically changed
  environment/configuration or backdated source. The actual running source and startup authority
  still need review; no arbitrary in-memory state recovery is claimed.
- Filename exclusions do not identify every secret embedded in arbitrary configuration files.
  Existing configuration and file dependencies need operator review before cutover.
- Original native source must remain frozen while that baseline is retained. New managed code uses
  a separate checkout. Native rollback refuses changed original files; it cannot restore application
  data/schema writes, which require application-appropriate backups.
- Automatic Node builds install from manifests/locks rather than copying the host `node_modules`.
  Patched dependencies or undeclared global modules need a reviewed Dockerfile/source plan; recovery
  includes an explicit dependency-tree warning before cutover.
- Startup inventory cannot prove absence of cron, arbitrary scripts, future administrator commands
  or every external launcher. Review the reversible startup handoff before cutover; importing never
  disables shared startup authority. Unresolved enabled template service/target references block recovery.
- Explicit unit directive checks do not clone every inherited process limit, umask, scheduling,
  capability or security default. These application-relevant policies require compatibility review.
- Automatic migration fails closed for unknown build/command layouts, unsupported interpreter or
  manager semantics, missing restart authority, special files or unsafe symlinks, and source limits.
  A listening process alone does not prove sufficient configuration to restart it safely.
