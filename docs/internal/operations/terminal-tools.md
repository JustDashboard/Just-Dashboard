# Installer presentation and local terminal tools

`install.sh` locates its checkout from its own path and presents four stages: prepare the host,
configure the dashboard, build/start, and verify startup. Its existing private-access, migration,
secret-preservation and port-selection decisions remain authoritative. Substeps and explicit `[ok]`
or `[!]` labels keep status readable without color. Color requires a capable terminal and is disabled
by `NO_COLOR` or `TERM=dumb`. `--help` prints usage without provisioning the host. It takes `--link`, `--no-link` and `--link-scope`, which
answer the optional question about linking the server to a just-dashboard.com account
([account link](../backend/account-link.md#terminal-installer-managesh)): asked after the two-factor question on a
new install (default No) and skipped when `JD_ACCOUNT_LINK=false` in the environment or in the kept `.env`; on
a re-run that keeps `.env`, only `--link` links. The link itself is the last step of stage 4, after the health
check, runs with input from `/dev/null`, and never fails the install: a decline, a timeout or an unreachable
site becomes a warning and a `Not linked. Later: ...` line in the summary.

When the checkout has no `.env` and `/var/lib/just-dashboard/install.env` exists — the copy an uninstall
that kept its data left beside that data — the installer offers to restore it (default yes) and then
continues as a re-run that keeps its settings, so the reinstall opens the secrets it kept. Declining
starts fresh and says the kept data's secrets stay unreadable.

When there is no `.env`, no kept copy, and `/var/lib/just-dashboard/vpsd.db` is still there — an install
removed by hand, or a fresh clone after the old checkout was deleted — the installer says so and asks
for the earlier `JD_MASTER_KEY` (default no). A pasted key (64 hex characters, a leading
`JD_MASTER_KEY=` accepted, not echoed) is written to the new `.env` in place of a generated one. Without
it the installer generates a key and says what comes back by itself — connections made from found
servers that state their credentials, repaired by the backend — and what has to be entered again.

At the end of stage 3 the installer copies the `jd` command line out of the freshly built backend
image (`docker run --rm --entrypoint cat just-dashboard-backend:latest /usr/local/bin/jd`) to
`/usr/local/bin/jd` on the host, mode 0755. An image without it, or a failed copy, is a `[!]` line naming
`sudo ./scripts/manage.sh install-cli` and never fails the install. After that the dashboard keeps the
host's copy current: at every boot `internal/clihost` replaces `/host/usr/local/bin/jd` with the jd built
beside the running binary when the file there is an older jd — recognised by a line of its usage text —
and leaves a missing file, or any other program called `jd`, alone.

The completion guide appears after backend health verification and shows the dashboard address,
sign-in instructions and terminal command examples, including a shell-quoted checkout path, and a
`jd login` line for deploying from a shell or CI with a deploy key from Account → API keys.
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

- Creation defaults to `readonly` and leaves `must_change_pw` clear: root typed the password twice
  on the host, so it is already chosen and the dashboard does not demand another at first sign-in.
- `auth.ResetPassword` atomically replaces the hash, clears `must_change_pw`, `failed_count` and
  `locked_until`, deletes browser sessions, revokes API tokens and deletes the account's passkeys. A
  failed revocation rolls the whole reset back. It preserves role, disabled state, TOTP
  seed/enrollment/counter and recovery codes, so recovery still works on an account whose passkeys
  are lost, and a passkey registered by whoever held the account does not survive the reset.
- `revoke-sessions` calls `auth.RevokeAllSessions`; it leaves API tokens unchanged.
- `users` reports role, disabled status, TOTP enrollment and required password change, without secrets.
- Mutations and failed account operations use `audit.Logger`: actor `cli`, username `root`, method
  `CLI`, actions `dashboard.user.create-user`, `dashboard.user.reset-password`, and
  `dashboard.user.revoke-sessions`. The target identifies the dashboard account. Success/failure is
  recorded in the database and mirrored to stderr. Audit detail contains only the chosen role.

Neither command issues a temporary password. Until 0.7.3 both set `must_change_pw`, so an operator
who had just reset their own password from a root shell was signed in and immediately asked for a
third one. Resetting a password does not provide a way around enrolled two-factor or enable a
disabled account; a reset also clears a change an account still owed.

## Linking commands

`link [observe|operate|maintain]`, `unlink` and `link-status` run `--admin link --scope S`, `--admin unlink`
and `--admin link-status` the same way. Unlike the account commands they read `JD_MASTER_KEY`,
`JD_ACCOUNT_LINK` and `JD_ACCOUNT_URL` from the Compose environment, because the identity they sign with is
sealed and they must refuse when linking is switched off; `link-status` needs no key. `link` prints an approval
address and a code, waits (`--wait`, 15 minutes by default) and exits 0 when linked, 2 when declined or not
approved in time, 1 for any other failure; `manage.sh` keeps that status. They run in the same image against
the same store as the running dashboard, which picks up a link or unlink within seconds. Audit: `account.link`
and `account.unlink`, actor `cli`, username `root`, method `CLI`. Agent mode refuses all `--admin` commands.

## Stack commands

`status` runs Compose `ps --all`. `logs [backend|frontend|proxy]` follows the selected service with
the last 100 lines; the default is backend. `restart` runs `up -d --force-recreate`, so edits to `.env`
reach recreated containers rather than preserving the old environment as Compose `restart` would.
Run it from SSH since a web terminal connection drops during recreation. It does not pull source or
request an image rebuild. The dashboard configuration page remains the path for changes needing
automatic rollback. `install-cli` does what the installer's jd step does: it reads `/usr/local/bin/jd` out
of the built backend image into a temporary file beside the target, makes it 0755 and moves it over
`/usr/local/bin/jd` (`JD_CLI_PATH` chooses another path), so a jd already running keeps its file; an
image with no jd fails with a message and leaves nothing behind.

## Verification

Run `scripts/test-changed.sh main`, plus `python3 scripts/test_manage.py` and
`bash -n install.sh scripts/manage.sh scripts/create-user.sh scripts/reset-password.sh`.
The Python fixtures check command arguments, stdin preservation, root and input checks, checkout paths
with spaces, Compose fallback, restoring a kept uninstall's `.env`, asking for the key of a store left
without one, installing jd from the image (and finishing without it), `install-cli`, and the installer's successful/failing
completion flows without changing
host packages or the installed stack. The linking cases prove what the installer asked by what its answers did
(bash prints no prompt for piped input): the question is skipped under `--link`, `--no-link`, a kept `.env` and
`JD_ACCOUNT_LINK=false`, the link command runs after the health check with the scope given, and a decline, an
error or silence from it leaves the install finished. Go tests use temporary stores to verify password strength,
account creation, TOTP preservation, token/session revocation, transactional rollback, audit records,
and secret-free output.
