# Runtime composition and system boundaries

## Server, modules, degradation

`api.Server` holds config, logger, store, auth service, sealer, audit logger, authenticator, WS
upgrader, the three limiters, and in agent mode the `agent.Identity`. `api/modules.go` (`moduleSet`)
holds the feature backends: `sys`, `metrics`, `docker`, `dockerStats`, `dockerEvents`, `pm2`, `systemd`,
`table`, `cron`, `logs`, `term`, `files`, `git`, `github`, `forge`, `updates`, `selfUpdate`, `proxy`, `dbs`,
`linuxUsers`, `netsec`, `captures` (`netcapture`), `flowAccounting` (`netflows`), `networkVantages` (`netvantage`),
`dnsServices` (`dnsservice`, [native DNS connections and owned engines](../backend/network-dns-services.md)),
`remoteDesktop` (`remotedesk`, [Remote Desktop](../backend/remote-desktop.md)), `ipam` (`netipam`), `network` (`netx`, [the network module](../backend/network.md)), `jobs`, three backup pieces, and deployment components covering legacy
execution, planning, sources, preflight, artifacts, orchestration, automation, scheduling, Git branch
monitoring and managed database networks. The backup runner delegates native SQLite snapshots to
Databases and disposable application checks to a Docker adapter; Backups owns their evidence and cleanup.

**Every module is optional.** A host with no Docker socket, no systemd or no fail2ban serves everything
else; affected routes return a precise "unavailable on this host" code the frontend renders as
information (`ErrorState` in `components/state.tsx`), not an error.

`Server.Start(ctx)` is separate from `New` so failing to schedule background work is reported by `main`
rather than swallowed in construction. It starts the metrics recorder (here, not lazily — its whole
purpose is to have been running while nobody was looking), the database activity recorder, the repair
of connections sealed under another master key (`database_stranded.go`), the Docker event log, the host alerts' pass once a minute (`hostAlerts.Start`, after both of those it reads), the network module's interface sampler and its daily blocklist refresh, the self-update
check, the backup scheduler, `selfupdate.Installer.Reconcile`,
`selfcfg.Applier.Reconcile`, Remote Desktop's `Reconcile` (closing the history of sessions a restart cut
off) and the Tailscale certificate keeper. `Shutdown` releases what outlives a request:
samplers, scheduler, live PTYs, database pools, Docker clients (Remote Desktop's engine and stream bridge clients among them;
both containers keep running). A held terminal session is let go rather
than ended — its holder is a systemd unit of its own on the host — and module setup takes every
running holder back before the first request, including when preparing new holders fails. New direct
terminals require that protection; an unavailable holder returns a reason instead of silently opening
a process-owned PTY. Terminal sessions have no idle timeout
([`processes-terminal-github.md`](../backend/processes-terminal-github.md#sessions-outlive-the-dashboard)).

The deployment engine also starts automatic production Git branch monitoring after its recovery.
The monitor makes bounded outbound ref reads every five seconds and queues immutable source revisions;
it stops before engine shutdown so no new automatic work arrives during the drain. Its persisted
cursor survives restarts. See the [deployment guide](../deployments/implementation.md) for eligibility,
failure handling and the unchanged health-gated activation contract.

Startup marks interrupted archive runs failed and reconciles owned restore-check containers before
scheduling new backups. Interrupted restore checks are cleaned and recorded as failed, never promoted
to recovery proof. Managed database network reconciliation follows retained environment bindings every
five seconds and stops before deployment engine shutdown.
Saved network diagnostics settle predecessor runs as interrupted at startup and never replay their
probes. A recording failure leaves the service unavailable until a bounded pending write can be
retained; it does not relaunch the probe. Shutdown cancels and drains active diagnostic jobs through
their existing process-group cancellation. Saved artifacts and generic job views of those artifacts
remain administrator-only. See [diagnostic lifecycle](../backend/network-diagnostics.md).
Private PCAP startup marks unfinished captures interrupted without replay. Shutdown cancels and
drains their native groups; a separate host `timeout` bounds a capture even after backend death.
A final recording failure retains bounded pending data and blocks new launches until the same write
can be retried. Capture changes no host network configuration. See
[capture lifecycle](../backend/network-captures.md).
Socket history starts independently of page reads and collects only after persisted admin opt-in.
Shutdown cancels its bounded native capture and drains it before closing Docker. IPAM reconciles
interrupted native handoffs to held review state at initialization, without replay or native cleanup.
Controlled vantage checks persist signed single-use leases and expire lost results without repeating
traffic; no polling listener or scanner is installed on a source by the dashboard.
The separate Socket History collector stays off by default. Its explicitly opted-in kernel observer
holds only owned unpinned cgroup links, drains bounded event batches through a durable SQLite receipt,
and retains failed detach state for retry. Shutdown reports an unsuccessful final write or detach;
restart records interruption and never automatically reloads the observer. Ordinary history opt-out
requires an explicit observer stop first so it cannot bypass the destructive route and its rate budget. See
[the observer contract](../backend/network-flow-observer.md).
Native DNS services reconcile interrupted reviewed changes and owned setup/removal during startup
with a forty-second deadline. They never replay native mutations or bootstrap, and retain verified
owned engines. Shutdown closes the module's Docker SDK client without stopping those engines. See
[native DNS service lifecycle](../backend/network-dns-services.md).
Before deployment workers start, preview quarantine persists blocks on legacy unsafe environments and
fences their old work. Its controller stops owned containers, disables restart, withdraws their routes,
and retries incomplete isolation every 30 seconds without preventing access to the dashboard. It stops
before the deployment engine on shutdown; retained legacy data is never deleted by quarantine.

`helpers.detachedContext` is the deliberate opposite: work that must outlive its request (a backup
transfer, a `compose up --build`) descends from `context.Background()` and is not cancelled by shutdown
— a deploy killed halfway is worse than one finishing into a dashboard that is gone. Its timeout is the
only bound.

That outlives the process, not the container: a host command started through `nsenter` stays in the
backend container's cgroup and is killed when the container stops. Package commands can stop it
themselves (docker-ce's maintainer script restarts Docker), so they run as transient units of the host's
systemd through `jobs.RunDetached`, and a restarted backend adopts them; see
[streaming and jobs](../backend/databases-proxy-platform.md#streaming-jobs-secrets-agent-mode).

## Reaching the host, and containing paths

`internal/hostexec`:

- `Command` runs a binary locally when present, else via `nsenter --target 1`. `CommandOnHost` *always*
  crosses (for tools like `who` that exist in the image but would report on the container).
  `CommandInDir` and `CommandOnHostInDir` change directory in a small shell on the far side, because
  crossing namespaces silently discards `cmd.Dir` and nsenter's `--wd` resolves the path on the wrong side.
- nsenter runs with `--no-fork`, so the process `exec.Cmd` holds is the host command and a cancelled
  context reaches it. `--pid` still has nothing to fork for: `/proc/1` is resolved in this process's own
  PID namespace, so the namespace it joins is the one it is already in. Every command also carries a
  `WaitDelay` (5 seconds): Wait stops waiting for descendants that keep the output pipe open, and reports
  `exec.ErrWaitDelay` when the command itself succeeded. Killing one command does not kill what it
  started; a caller that must stop a whole tree uses `RunGroup`, which clears the delay and signals the
  process group.
- `AsOwner(cmd)` drops to the UID/GID owning `cmd.Dir`, so a `git pull` does not leave root-owned files.
- `CommandOnHostAsUser` enters the host namespace before using `setpriv` to select the trusted account's
  UID/GID and groups, clear capabilities and set no-new-privileges. PM2 uses this path with a minimal
  environment; a user-owned executable is never run as the dashboard's root identity.
- `InHostMounts(fn)` runs Go code, not a command, in the host's mount namespace: on a goroutine locked
  to a thread that unshares its filesystem attributes and `setns`es into `/proc/1/ns/mnt`, a thread Go
  then discards rather than reuses. Only `fn` moves; a goroutine it starts is back in the container.
  Where there is no other namespace (a plain install) or it cannot be entered, `fn` runs in place.
  The runtime cannot discard the process's main thread, so `cmd/server` keeps it on the main goroutine
  with `runtime.LockOSThread` in `init`. Otherwise `/proc/<pid>/ns/mnt`, which `docker exec` and so the
  container health check join, can end up as the host's for good.
- Argv is passed through unchanged and **never** through a shell. Keep it that way ([invariant 6](../security/invariants.md#invariants-that-must-not-regress)).

Local interactive rebase uses Git's editor protocol: `gitx` sets the editor to the current server
executable with the fixed `--git-editor` mode. Git invokes that fixed command itself; request values
remain in a validated JSON plan and never enter shell syntax. The helper starts before server flags or
configuration, accepts only Git's expected metadata targets, and supports no arbitrary command or
`exec` todo entries. Continue uses the same persisted plan after a conflict or server restart.

`files.Resolve` is the single choke point for client-supplied paths: it checks the cleaned path *and*
the symlink-resolved path (the nearest existing ancestor for new paths) against `JD_FILE_ROOTS`. Every new
filesystem entry point goes through it, including the ones that do not look like file operations —
backup restore destinations, database dump paths, bind-mount sources, build contexts. `ResolveEntry`
applies the same containment but returns the entry rather than its target: use it for delete, move, stat
and chmod, which act *on* a symlink.

The Files page's handlers (and `GET /system/disk-usage`) use `modules.hostFiles`, the same `Service`
made with `OnHost(InHostMounts)`: every exported operation, `Resolve` included, runs whole in the host's
mount namespace, so the page sees `/tmp`, `/usr` and `/var/lib` as the terminal does and a path is never
resolved in one tree and opened in another. Every other caller keeps `modules.files`, in the container's
view, because it resolves a path and then opens it itself. The roots and the checks are identical in
both; only the tree they are evaluated against differs. Resolving through `/host` was the alternative
and was rejected: every path the kernel hands back — symlink targets, walk results, error text — would
need translating, and absolute symlinks would have to be re-rooted by hand, at every one of the
package's syscalls.

Logs are read under their own roots: `logsx.Service.Allow` holds a path, after resolving symlinks, to
`JD_LOG_ROOTS`. Every log file the dashboard opens passes it, including the paths no request typed — a
PM2 process's files from its record, the files a database server holds open (read from `/proc`), the
access and error logs a proxy site's `access_log`/`error_log` directives name, and each rotated
generation beside a site's log, asked again before every open because a generation can be a link to
anywhere. A directive or a process is something an operator can point anywhere, so the path it yields is
contained like one typed into a request; a refused one is named with the reason, never read.

`internal/safepath` holds the archive-unpacking rules (absolute symlink targets refused, nothing written
through a symlink already in the destination, the final component unlinked rather than followed). Both
`files/archive.go` and `backups/restore.go` use it; they used to carry a copy each of the same lexical
prefix test, with the same hole. File-manager extraction additionally stops after 100,000 entries or
8 GiB of bytes actually written, reserves at least 1 GiB of free space, serialises extraction requests,
removes the current partial file on failure, and obeys a ten-minute request deadline — compressed
metadata is never trusted as the quota. `internal/sysinfo` reads the host through gopsutil rather than parsing
`/proc`, so the same path works across kernels and inside a container with `/proc` bind-mounted.

## Auth, secrets, state

`internal/auth` owns users, sessions, TOTP, recovery codes, passkeys, API tokens. The session cookie is
`__Host-jd_session` (HttpOnly, SameSite=Strict, Secure, `Path=/`, no `Domain`), or `jd_session` without
Secure where the dashboard is served over plain loopback HTTP (`JD_DEV`, `JD_TLS=off`), since a browser
refuses a `__Host-` cookie that cannot be Secure; `internal/sessioncookie` names both, and
`httpx.Authenticator` sets and reads them. The prefix is the browser's guarantee that the cookie is
sent to this one name and cannot be planted by a sibling. A browser still holding the name releases
before 0.8.2 set, `vpsd_session`, is answered on its first request, given the current name for what is
left of the session's lifetime and the old cookie cleared (`settle`); sign-out and a password change
clear both names. A browser still sends the cookie to every *port* of the dashboard's name, so every
proxy the dashboard writes in front of an application on that name — a tailnet address's front, a
deployment route on `JD_SITE` — takes it out of the request
([the dashboard's session cookie](../deployments/tailnet-apps.md#the-dashboards-session-cookie)). The
CSRF header and `SameSite=Strict` are unchanged. The WebSocket upgrader uses the same browser-facing
scheme for its same-origin check, so HTTP tunnel installs can open live streams. A
session that still owes a second factor is accepted only by
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
The account keys page replaces an owned key through the existing create route, followed by an
optional, separately confirmed revoke after the tool has switched credentials. The old key remains
valid until revoked or expired; creating a replacement does not impersonate another owner.
An MCP key's replacement is made on `POST /tokens/mcp` at the old key's role with the databases the
old key reaches that are neither protected nor forgotten since, each narrowed to `read` where that
role may not change a database, and the old key's deployment grant, narrowed to `read` where that
role may not deploy, with the deployments that grant was narrowed to and the generated tools only if
the old key had them. `PUT /tokens/{id}/connections`
(session only, the owner's own active MCP key, `404` for another account's, `409 not_mcp_key` for any
other key) replaces what a key reaches without a new secret — its databases, its deployment grant and
its chosen deployments and whether it has the generated tools, each kept when left out; it is audited as
`dashboard.token.connections` with the databases by name, the deployment grant, what it reaches and
`operations`. Forgetting a connection removes its grants
with it (`ON DELETE CASCADE`).
A deploy key (`auth.ScopeDeploy`) is made on `POST /tokens/deploy` (session only, `system.admin`) from
`{name, ttlDays, deploymentIds}` and always holds the administrator role, which `CreateDeployToken`
refuses to an owner without it; `ResolveAPIToken` narrows it to the owner's role on every call like any
key. Its deployments live in `api_token_deployments` and are read with the key on every request; none
is every deployment. The project is deliberately not a foreign key there: purging a deployment leaves
the row, so a key that reached only it keeps reaching nothing rather than everything. There is no route
to re-point one; a replacement is minted with the same list, and a narrowed key's own draft commit adds
the deployment it created, in the commit's transaction (`deploy.DraftCommitRequest.GrantToKey`). A draft
records the deploy key that made it (`deploy_drafts.key_id`, 0 for a session), which is what holds a key
to its own drafts. Where it may go is the API's decision
(`api/deploy_keys.go`, [request lifecycle](request-lifecycle.md)).
`DELETE /tokens/{id}` revokes and retains the record. The session-only
`DELETE /tokens/{id}/record` additionally requires `system.admin` and `s.destructive`; it deletes
only revoked or expired metadata through `auth.DeleteInactiveAPIToken`'s conditional SQL write.
An active record returns `409 token_active`, an absent record `404`, and a successful deletion is
audited as `dashboard.token.delete` with its token ID. Removing a record never deletes the audit
trail. No schema change or credential recovery is involved.
The first admin is created by `cmd/server/main.go` from `JD_BOOTSTRAP_USER`/`JD_BOOTSTRAP_PASSWORD`;
`must_change_pw` is set only when the password was *generated* there and printed to the log — a
password the operator chose in the installer is theirs and is not demanded again at first sign-in.
`install.sh` validates that password with `scripts/password.sh`, the same rule as
`auth.ValidatePasswordStrength`, and on a fresh install removes it from `.env` and recreates the backend
after the first health check; nothing reads it again once an account exists, and a kept `.env` is not touched.
The same holds for `scripts/create-user.sh` and `scripts/reset-password.sh`: a password root typed on
the host is already chosen. Only an account an admin creates from Account → Users owes a change.

Passwords are Argon2id hashes (`auth/crypto.go`, 64 MiB, three passes, four lanes; test binaries use a
token cost). Each hash allocates its whole memory parameter at once, so at most two run at a time and a
third sign-in waits for a slot. After a full-cost hash the backend collects and returns the buffer to the
operating system straight away: otherwise a collection that saw it live put the next one off until the
heap had doubled, which left the process at about 155 MB for up to half a minute after a sign-in.

Local host-root account recovery uses the same binary's isolated `--admin` mode through
`scripts/manage.sh`. It opens only an existing store and starts no server or modules. Password resets
atomically set the new password, clear any owed change and login lockout, and revoke sessions/API
tokens and passkeys while keeping TOTP enrollment, recovery codes and disabled state; account mutations are audited
as actor `cli`. See [terminal tools](../operations/terminal-tools.md) for commands and the local
privilege boundary.

`auth.Sealer` (from the 64-hex `JD_MASTER_KEY`) encrypts every stored secret — TOTP seeds, connection
strings, deploy env, backup credentials, the backup key encrypted archives are sealed to
([archive encryption](../backend/git-backups-users.md#archive-encryption)), and the SQL editor
assistant's provider and key (one sealed row of `settings`, `databases.assistant`).

An account has two names. `username` is the sign-in key and the actor every audit entry and
deployment record names: trimmed, lower-cased, one word, unique. `display_name` is what the account
shows as — the username as typed at creation, case kept, until it is changed. Both are renamed by the
holder (`PATCH /account/profile`, session-only) or by a `system.admin` (`PATCH /dashboard-users/{id}`);
a rename is audited with both spellings, and earlier entries keep the old one. Creation validates the
display name before inserting the account; profile patches validate both names before one update.
Administrative account edits commit all fields and any required session revocation in one transaction,
so a rejected request cannot leave behind a rename, a new account, or only part of an edit. The picture
(`avatar`, `avatar_type`, `avatar_at`, additive columns in 0.6.7) is uploaded as multipart to
`POST /account/avatar`, bounded by `auth.MaxAvatarBytes`, and stored only after `image.DecodeConfig`
has proven it a PNG or JPEG of at most 1024px a side under a type sniffed from the bytes — the
declared type is ignored, and the stored type is the one served. `GET /account/avatar` and the admin
group's `GET /dashboard-users/{id}/avatar` serve it with a long private cache life, since the version
stamp `avatarVersion` rides on the URL. `POST /account/sessions/revoke-others` drops every session of
the caller but the one asking.

An account also carries its answer to the first-run question (additive columns in 0.7.3):
`needs_onboarding`, set on every account `CreateUser` makes — the bootstrap admin, an administrator's
new account, `scripts/create-user.sh` — and defaulting to `0` for accounts that predate it, and
`hidden_sections`, a JSON list of the sections it keeps out of its sidebar, defaulting to `[]`. Both
ride on the session's `user` (`needsOnboarding`, `hiddenSections`). The holder alone writes them, with
`PUT /account/workspace` (session-only, no capability beyond that, audited as
`account.workspace.update` with the list stored): it takes `hiddenSections` — at most 32 names shaped
like a path segment, since the list of sections belongs to the frontend's nav registry — stores them
sorted without repeats, and clears `needs_onboarding`. The list narrows what the shell draws and
nothing the account may do; every route still answers by role. See
[the shell](../frontend/shell-design.md#sections-an-account-keeps-out-of-its-sidebar).

State is SQLite in `JD_DATA_DIR`, schema as one `CREATE TABLE IF NOT EXISTS` block in
`internal/store/store.go` with no migration tool ([invariant 8](../security/invariants.md#invariants-that-must-not-regress)). The file is still named `vpsd.db`
through the rename: moving it would strand every existing install's accounts, audit log and secrets.
For as long as the store is open it holds open file description locks on SQLite's shared-lock range and
on the `-shm` dead-man switch (`store/wal_guard.go`). SQLite's own locks belong to the process, so a
close of the file anywhere in it — a scan reading its header, a download, an archive — releases them,
and another SQLite closing its connection afterwards would believe it was the last one and delete the
`-wal` and `-shm` the store is still writing through, corrupting the database. Inspect a live store
read-only: `sqlite3 'file:/var/lib/just-dashboard/vpsd.db?mode=ro'`.
Tables are grouped by owner: authentication and audit (`users`, `recovery_codes`, `sessions`,
`api_tokens`, `api_token_connections` — the databases an MCP key reaches —, `api_token_deployments` — the deployments a deploy key, or an MCP key's deployment grant, was narrowed to —, `audit_log`, whose `key_id` and `key_name` name the API key behind a request); databases (`db_connections`, `db_saved_queries`, `db_query_history`, `db_diagram_layouts`, `db_inventory_ignored`, `db_metric_samples`, the statement history's `db_statement_intervals`, `db_statement_shapes` and `db_statement_samples`, point-in-time recovery's `db_wal_archives` — with the destination's keys and the archive's private key sealed — `db_wal_restore_points` and `db_wal_restores`, `db_upgrades`, and `db_tls_ca` and `db_server_tls` for the certificate authority, whose key is sealed, and the certificates issued from it); boards (`boards`, the images placed on them in `board_files`, the install's shape library in `board_library`, and `sticky_boards`, which nothing reads since Stickyboard was removed in 0.8 but which stays so the boards made with it in 0.7.2–0.7.5 survive an update); Remote Desktop (`remote_desktop_computers`, with sealed passwords and the stream bridge's id once paired, and
`remote_desktop_sessions`); backups
(`backup_jobs`, `backup_runs`, `backup_restore_tests`); legacy deployment compatibility (`deploy_projects`, `deploy_env`,
`deploy_runs`); normalized deployment environments, credentials, sources, plans, releases, artifacts,
runtimes, steps, logs, dependencies, checks, triggers, delivery records, variable and plan snapshots,
blueprint installs, port and queue leases, removals, drafts, schedules, Git watch cursors, notifications,
and previews, and the last answer the assistant gave about a failed run with the masked text it was sent
(`deploy_run_diagnoses`, which goes with its run); proxy
watching (`watched_endpoints`, filled from the older one-port-per-name `watched_domains`, holding TLS watches and TCP network probes by `kind`, with each check in `watched_checks`); compose deployment history (`docker_stack_deployments` — the file, the
running digests and the git commit captured before every state-changing action, with environment values
hashed rather than stored); the server map's arrangement, one JSON document per account
(`map_layouts`, which goes with its account); the general `settings` key/value table; mount, container, interface
(`metric_interface_samples`) and host metric samples; and the network module's sealed WireGuard client
configurations (`network_vpn_clients`), saved diagnostic runs (`network_diagnostic_runs`) and private
packet capture metadata/artifacts (`network_packet_captures`), optional source/check identities
(`network_probe_vantages`, `network_probe_checks`), shared planning pools/reservations
(`network_ipam_pools`, `network_ipam_reservations`), private native DNS investigation records
(`network_dns_evidence`), private native DNS service connections, reviewed changes, provisions and
resource owner identity (`network_dns_services`, `network_dns_service_changes`,
`network_dns_service_provisions`, `network_dns_service_settings`), socket-hour/coverage records
(`network_flow_buckets`, `network_flow_cycles`), and gateway counter totals across table generations
with seven days of per-minute protection samples (`network_gateway_counters`,
`network_protection_samples`). What the network module makes on the host is kept in
`/etc/just-dashboard/network/spec.json` instead, because it describes the host and has to outlive the
dashboard ([network module](../backend/network.md#three-rules)). The schema block in `store.go` is the authoritative column-level reference. `migrateLegacyDeployments` maps each populated
0.6.6 project transactionally and idempotently while preserving ids, ciphertext, hooks, logs, and the old
columns; `internal/store/testdata/0.6.6.sql` is the executable upgrade contract.

`internal/audit` writes `audit_log` **and** mirrors every entry to the process log, so a trail survives
the database being tampered with. The row is written with the request's values but not its
cancellation (bounded at five seconds), so a client that disconnects once its mutation applied still
leaves the row. An `Entry` records who (user, role, `Actor` = session or token), from
where, what (action, target, method, path), and how it went. Local root account commands use
`Actor = cli`, `Username = root`, and `Method = CLI`, with the dashboard account as the target.

## Passkeys

`auth/passkeys.go` (library: `github.com/go-webauthn/webauthn`, BSD-3-Clause) adds WebAuthn passkeys
that stand in for an account's password at sign-in. Tables: `webauthn_credentials` (the credential
id, the library's record of it as JSON — public key, flags, signature counter — the relying party ID
it was registered under and the name its owner gave it; `ON DELETE CASCADE` with the account) and
`webauthn_spent_challenges` (`store/schema_passkeys.go`, additive).

- **What a passkey is worth.** It replaces the password, never the code. A passkey sign-in starts
  its session through `startSession`, the function a password sign-in calls, so an enrolled account
  owes its code exactly as after a password and `JD_REQUIRE_2FA` keeps its meaning
  ([invariant 2](../security/invariants.md#invariant-2-what-two-factor-still-guarantees)). Sessions
  record `passkey`, then `passkey+totp` or `passkey+recovery` once the code is given.
- **Relying party and origin.** The RP ID is the host name the browser reached the dashboard at, read
  from the request's `Origin` (`auth.PasskeyParty`): it must be the request's own host or one of
  `JD_ALLOWED_ORIGINS`, over https or plain http on `localhost`, and never an IP address, which
  WebAuthn refuses. A credential is bound by the browser to the name it was made at, so one made at
  `dash.example.com` does not sign in at the tailnet name; each is stored with its RP ID and the
  security page says where a passkey made elsewhere works. At an IP address the sign-in and security
  pages say why no passkey is offered instead of offering one that fails.
- **Ceremonies.** Registration asks for a discoverable credential with user verification required,
  excludes the account's existing ones, and takes no attestation. Sign-in is discoverable (no
  username): the authenticator's user handle — an HMAC of the account id under the install's key, so
  it names nobody — says whose it is, and must match the credential's account. A challenge is not
  stored while in flight: its session data, kind, account, session, origin and a five-minute expiry
  are sealed with the install's key and handed to the browser as the challenge id, so beginning a
  sign-in writes nothing. The finish opens it, checks every field and spends it by inserting its id
  into `webauthn_spent_challenges`, so a response is tried against a challenge once.
- **Refusals.** User verification is required at both ends; a signature counter that does not move
  forward (`CloneWarning`) refuses the sign-in as a possibly copied key; a removed credential, one
  registered at another name, a disabled account, an expired, altered, replayed or misplaced
  challenge and a response signed for another origin or relying party are each refused, and every
  refusal is audited with its reason. A password lockout does not block a passkey: it brakes
  password guessing, and a passkey is not guessed.
- **Lifecycle.** Registering takes a complete session (`/account`, `RequireSession`) and a fresh
  password plus, where enrolled, a TOTP code that is consumed as at sign-in. Renaming and removing
  are the account holder's, from a session, with an ordinary confirmation; an account holds at most
  25. Disabling an account keeps its passkeys and refuses them until it is enabled. A root shell's
  `reset-password` deletes them with the sessions and API tokens, because that reset is how an
  account somebody else may hold is taken back; an administrator's password set and the holder's
  own password change keep them, as they keep API tokens. Role changes do not touch them.
- **Routes and audit.** `POST /auth/passkey/begin`, `POST /auth/passkey/finish` (sign-in);
  `GET /account/passkeys`, `POST /account/passkeys/register/begin` (password and code),
  `POST …/register/finish`, `PATCH /account/passkeys/{id}`, `DELETE /account/passkeys/{id}`.
  Audited as `auth.passkey.login` (success or the refusal), `auth.passkey.reauth`,
  `auth.passkey.register`, `auth.passkey.rename` and `auth.passkey.remove`, with the passkey's name
  and RP ID and never a credential.

## Session authentication provenance

`sessions.auth_method` is additive with default `unknown`; existing rows are never inferred to have
entered a code. Fresh sessions record `password`, and successful factor completion records `totp` or
`recovery`. This provenance is exposed as `authMethod` separately from `twoFactorPassed`, which is
an access-policy/elevation flag. Optional two-factor policy can complete a password-only session;
that does not make it a password-and-code session. Enrolment proves its code, while later policy-off
elevation retains its recorded provenance, including unknown legacy provenance. Account session summaries use this distinction and display
legacy/unknown authentication honestly. Migration and authentication tests cover existing rows and
password/code/recovery completion; route authorization and per-account enforcement are unchanged.

## Container limits in `docker-compose.yml`

Every service rotates its logs through the shared `x-logging` block (`json-file`, five files of 10 MB).
The backend stays root and privileged, because managing the host is what it is for. The frontend and the
proxy run with `no-new-privileges` and every capability dropped, and the proxy keeps `NET_BIND_SERVICE`
although it never binds below 1024: the `caddy` binary in its image carries that as a file capability, and
the kernel refuses to exec such a binary without it, so the container restart-loops with
`caddy: Operation not permitted`. The proxy's healthcheck is `nc -z` on loopback, which it binds in every
configuration. `up --wait` in the updater and the settings runner now waits for it as well, at the same
30-second interval as the frontend's, so neither takes longer, and a proxy that never listens now fails
that step instead of being found later.
