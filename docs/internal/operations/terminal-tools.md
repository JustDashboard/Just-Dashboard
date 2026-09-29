# Installer presentation and local terminal tools

`install.sh` locates its checkout from its own path and presents four stages: prepare the host,
configure the dashboard, build/start, and verify startup. Its existing private-access, migration,
secret-preservation and port-selection decisions remain authoritative. Substeps and explicit `[ok]`
or `[!]` labels keep status readable without color. Color requires a capable terminal and is disabled
by `NO_COLOR` or `TERM=dumb`. `--help` prints usage without provisioning the host.

The completion guide appears after backend health verification and shows the dashboard address,
sign-in instructions and terminal command examples, including a shell-quoted checkout path.
`scripts/manage.sh` owns the commands; `scripts/create-user.sh` and `scripts/reset-password.sh` are
executable convenience wrappers. All locate their own checkout, validate arguments before calling
Docker and allow help without root, configuration or Docker. Actual commands require local root and
an existing `.env`; Compose reads it without shell evaluation. The Compose plugin is preferred, with
the installer's legacy `docker-compose` fallback.

## Account commands

`users`, `create-user USER [readonly|limited|admin]`, `reset-password USER`, and
`revoke-sessions USER` run `docker compose run --rm --no-deps -T backend --admin ...`. They reuse
the already-built image and configured state mount, start no dependent services, publish no listener
and work with a stopped backend. They require an existing `vpsd.db` and refuse to silently create a
replacement store. This is local host-root recovery, independent of HTTP login and capabilities;
HTTP authorization, allowlisting and second-factor enforcement are unchanged. Agent mode refuses
human account commands. Rebuild the backend image when installing this feature on an older checkout.

The `--admin` dispatch in `cmd/server/main.go` runs before server flags, configuration and module
startup. `internal/admin` validates its command, requires effective UID 0 and opens the existing store
through `store.Open`, retaining additive migration and SQLite locking behavior. It uses the existing
auth service and password validation/hashing; these operations never decrypt stored secrets and
do not need an auth sealer. Account names use the service's case normalization.

Password prompts read hidden input twice. Noninteractive input requires explicit `--password-stdin`;
the scripts send the password only on stdin, never as Docker/Compose arguments or in `.env`. The
backend accepts a single line of at most 4096 bytes and never prints it or puts it in audit detail.

- Creation defaults to `readonly` and sets `must_change_pw` for every role.
- `auth.ResetPassword` atomically replaces the hash, sets `must_change_pw`, clears `failed_count` and
  `locked_until`, deletes browser sessions and revokes API tokens. A failed revocation rolls the whole
  reset back. It preserves role, disabled state, TOTP seed/enrollment/counter and recovery codes.
- `revoke-sessions` calls `auth.RevokeAllSessions`; it leaves API tokens unchanged.
- `users` reports role, disabled status, TOTP enrollment and required password change, without secrets.
- Mutations and failed account operations use `audit.Logger`: actor `cli`, username `root`, method
  `CLI`, actions `dashboard.user.create-user`, `dashboard.user.reset-password`, and
  `dashboard.user.revoke-sessions`. The target identifies the dashboard account. Success/failure is
  recorded in the database and mirrored to stderr. Audit detail contains only chosen role and
  required-password-change metadata.

The account holder completes required factors before changing a temporary password. Resetting a
password does not provide a way around enrolled two-factor or enable a disabled account.

## Stack commands

`status` runs Compose `ps --all`. `logs [backend|frontend|proxy]` follows the selected service with
the last 100 lines; the default is backend. `restart` runs `up -d --force-recreate`, so edits to `.env`
reach recreated containers rather than preserving the old environment as Compose `restart` would.
Run it from SSH since a web terminal connection drops during recreation. It does not pull source or
request an image rebuild. The dashboard configuration page remains the path for changes needing
automatic rollback.

## Verification

Run `scripts/test-changed.sh main`, plus `python3 scripts/test_manage.py` and
`bash -n install.sh scripts/manage.sh scripts/create-user.sh scripts/reset-password.sh`.
The Python fixtures check command arguments, stdin preservation, root and input checks, checkout paths
with spaces, Compose fallback and the installer's successful/failing completion flows without changing
host packages or the installed stack. Go tests use temporary stores to verify password strength,
account creation, TOTP preservation, token/session revocation, transactional rollback, audit records,
and secret-free output.
