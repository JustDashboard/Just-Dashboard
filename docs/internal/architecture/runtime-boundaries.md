# Runtime composition and system boundaries

## Server, modules, degradation

`api.Server` holds config, logger, store, auth service, sealer, audit logger, authenticator, WS
upgrader, the three limiters, and in agent mode the `agent.Identity`. `api/modules.go` (`moduleSet`)
holds the feature backends: `sys`, `metrics`, `docker`, `dockerStats`, `dockerEvents`, `pm2`, `systemd`,
`table`, `cron`, `logs`, `term`, `files`, `git`, `github`, `updates`, `selfUpdate`, `proxy`, `dbs`,
`linuxUsers`, `netsec`, `jobs`, three backup pieces, and eleven deployment components covering legacy
execution, planning, sources, preflight, artifacts, orchestration, automation, scheduling, and Git branch
monitoring.

**Every module is optional.** A host with no Docker socket, no systemd or no fail2ban serves everything
else; affected routes return a precise "unavailable on this host" code the frontend renders as
information (`ErrorState` in `components/state.tsx`), not an error.

`Server.Start(ctx)` is separate from `New` so failing to schedule background work is reported by `main`
rather than swallowed in construction. It starts the metrics recorder (here, not lazily — its whole
purpose is to have been running while nobody was looking), the Docker event log, the self-update check,
the backup scheduler, `selfupdate.Installer.Reconcile`, `selfcfg.Applier.Reconcile` and the Tailscale
certificate keeper. `Shutdown` releases what outlives a request:
sampler, scheduler, live PTYs, database pools, Docker client.

The deployment engine also starts automatic production Git branch monitoring after its recovery.
The monitor makes bounded outbound ref reads every five seconds and queues immutable source revisions;
it stops before engine shutdown so no new automatic work arrives during the drain. Its persisted
cursor survives restarts. See the [deployment guide](../deployments/implementation.md) for eligibility,
failure handling and the unchanged health-gated activation contract.

`helpers.detachedContext` is the deliberate opposite: work that must outlive its request (a backup
transfer, a `compose up --build`) descends from `context.Background()` and is not cancelled by shutdown
— a deploy killed halfway is worse than one finishing into a dashboard that is gone. Its timeout is the
only bound.

## Reaching the host, and containing paths

`internal/hostexec`:

- `Command` runs a binary locally when present, else via `nsenter --target 1`. `CommandOnHost` *always*
  crosses (for tools like `who` that exist in the image but would report on the container).
  `CommandInDir` and `CommandOnHostInDir` pass `--wd` to nsenter because crossing namespaces silently
  discards `cmd.Dir`.
- `AsOwner(cmd)` drops to the UID/GID owning `cmd.Dir`, so a `git pull` does not leave root-owned files.
- `CommandOnHostAsUser` enters the host namespace before using `setpriv` to select the trusted account's
  UID/GID and groups, clear capabilities and set no-new-privileges. PM2 uses this path with a minimal
  environment; a user-owned executable is never run as the dashboard's root identity.
- Argv is passed through unchanged and **never** through a shell. Keep it that way ([invariant 6](../security/invariants.md#invariants-that-must-not-regress)).

`files.Resolve` is the single choke point for client-supplied paths: it checks the cleaned path *and*
the symlink-resolved path (the nearest existing ancestor for new paths) against `JD_FILE_ROOTS`. Every new
filesystem entry point goes through it, including the ones that do not look like file operations —
backup restore destinations, database dump paths, bind-mount sources, build contexts. `ResolveEntry`
applies the same containment but returns the entry rather than its target: use it for delete, move, stat
and chmod, which act *on* a symlink.

`internal/safepath` holds the archive-unpacking rules (absolute symlink targets refused, nothing written
through a symlink already in the destination, the final component unlinked rather than followed). Both
`files/archive.go` and `backups/restore.go` use it; they used to carry a copy each of the same lexical
prefix test, with the same hole. File-manager extraction additionally stops after 100,000 entries or
8 GiB of bytes actually written, reserves at least 1 GiB of free space, serialises extraction requests,
removes the current partial file on failure, and obeys a ten-minute request deadline — compressed
metadata is never trusted as the quota. `internal/sysinfo` reads the host through gopsutil rather than parsing
`/proc`, so the same path works across kernels and inside a container with `/proc` bind-mounted.

## Auth, secrets, state

`internal/auth` owns users, sessions, TOTP, recovery codes, API tokens. Cookie `vpsd_session` (HttpOnly,
SameSite=Strict, Secure unless `JD_DEV`). A session that still owes a second factor is accepted only by
the 2FA routes (`AuthenticatePartial`); everything else answers `totp_required` /
`totp_enrollment_required`. Who owes one is decided per account: an enrolled account is always challenged,
and `JD_REQUIRE_2FA` (default false, reported as the `require2fa` status field) decides only whether an
*unenrolled* account may sign in at all — see
[invariant 2](../security/invariants.md#invariant-2-what-two-factor-still-guarantees). With the policy off,
`Login` elevates the session at creation and `ResolveSession` completes one left half-authenticated by a
policy that changed under it, so turning the setting off cannot strand a session that can never be
elevated. `Service.DisableTOTP` is the account holder's own off switch, costs their password, and is
refused where the policy demands an authenticator.
Enrollment is available only while `totp_enabled` is false. Consuming its proof, enabling TOTP,
generating recovery codes and elevating the owning session are one transaction. `users.totp_last_step`
records the greatest consumed 30-second counter; verification conditionally advances it and elevates
the session in one transaction, so simultaneous requests cannot reuse a code. Recovery codes are also
consumed transactionally. An additive schema migration introduces the counter column; resetting TOTP
clears the counter with the account update.
An account marked `must_change_pw` must finish its required factors before the password-change
route becomes available, and cannot use feature routes or API tokens until the password is changed.
API tokens may narrow their creator's role, never widen it, and are demoted with the account.
`auth.Sealer` (from the 64-hex `JD_MASTER_KEY`) encrypts every stored secret — TOTP seeds, connection
strings, deploy env, backup credentials.

State is SQLite in `JD_DATA_DIR`, schema as one `CREATE TABLE IF NOT EXISTS` block in
`internal/store/store.go` with no migration tool ([invariant 8](../security/invariants.md#invariants-that-must-not-regress)). The file is still named `vpsd.db`
through the rename: moving it would strand every existing install's accounts, audit log and secrets.
Tables are grouped by owner: authentication and audit (`users`, `recovery_codes`, `sessions`,
`api_tokens`, `audit_log`); databases (`db_connections`, `db_saved_queries`, `db_query_history`); backups
(`backup_jobs`, `backup_runs`); legacy deployment compatibility (`deploy_projects`, `deploy_env`,
`deploy_runs`); normalized deployment environments, credentials, sources, plans, releases, artifacts,
runtimes, steps, logs, dependencies, checks, triggers, delivery records, variable and plan snapshots,
blueprint installs, port and queue leases, removals, drafts, schedules, Git watch cursors, notifications,
and previews; proxy
watching (`watched_domains`); compose deployment history (`docker_stack_deployments` — the file, the
running digests and the git commit captured before every state-changing action, with environment values
hashed rather than stored); the general `settings` key/value table; and mount, container, and host metric
samples. The schema block in `store.go` is the authoritative column-level reference. `migrateLegacyDeployments` maps each populated
0.6.6 project transactionally and idempotently while preserving ids, ciphertext, hooks, logs, and the old
columns; `internal/store/testdata/0.6.6.sql` is the executable upgrade contract.

`internal/audit` writes `audit_log` **and** mirrors every entry to the process log, so a trail survives
the database being tampered with. An `Entry` records who (user, role, `Actor` = session or token), from
where, what (action, target, method, path), and how it went.
