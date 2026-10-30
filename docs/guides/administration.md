# Administration

[← README](../../README.md) · [Features](../../FEATURES.md) · [Install](install.md) · [Configuration](configuration.md) · [Command line](cli.md) · [Administration](administration.md) · [Linking & usage count](linking-and-usage.md) · [Security](../../SECURITY.md)

Running the dashboard from the server itself: account scripts, API keys, updates, backups and uninstalling.

## Terminal admin scripts

Run these on the server from your checkout. They manage **dashboard accounts** (not Linux users) and
use the built backend image, so neither Go nor a browser login is needed — account commands work even
while the backend is stopped.

| Script | What it does |
| --- | --- |
| `sudo ./scripts/reset-password.sh admin` | Recover an account by setting a new password |
| `sudo ./scripts/create-user.sh alice limited` | Create an account (`readonly`, `limited` or `admin`) |
| `sudo ./scripts/manage.sh users` | List accounts, roles and two-factor status |
| `sudo ./scripts/manage.sh revoke-sessions alice` | Sign out every browser session for an account |
| `sudo ./scripts/manage.sh link [observe\|operate\|maintain]` | Link this server to your just-dashboard.com account (prints a code to approve there) |
| `sudo ./scripts/manage.sh unlink` | Unlink it |
| `sudo ./scripts/manage.sh link-status` | Say whether it is linked |
| `sudo ./scripts/manage.sh status` | Show the containers and their health |
| `sudo ./scripts/manage.sh logs [backend\|frontend\|proxy]` | Follow logs (Ctrl+C exits) |
| `sudo ./scripts/manage.sh restart` | Recreate the stack to apply `.env` edits |
| `sudo ./scripts/manage.sh install-cli` | Install (or reinstall) the `jd` command line at `/usr/local/bin/jd` |
| `./scripts/manage.sh --help` | Usage, no sudo needed |

Passwords are entered twice with hidden input and must meet the dashboard's strength rules. The
password a script sets is the one the account signs in with — the dashboard does not ask for another
at sign-in. An account an admin creates from Account → Users, and a bootstrap password the backend
generated, are the ones replaced on first sign-in, after any required two-factor step. A reset
clears failed-login and failed-code lockouts and revokes that account's sessions, API tokens and passkeys, while keeping
two-factor enrolment, recovery codes, role and disabled state. Account changes are audited as local
root; `revoke-sessions` leaves API tokens unchanged.

For automation, append `--password-stdin` to either password command and pipe one password line from
a trusted source — never put passwords in arguments or shell history. The scripts find the checkout
from their own path and let Compose read `.env` without executing it. Run `restart` from SSH, since it
disconnects the web terminal; it recreates containers with current settings, which
`docker compose restart` alone does not.

## API keys

Under **Account → API keys**, search by name, prefix, role or owner and filter keys
that have never been used, have been unused for 30 days, expire soon or have no expiry. **Replace key**
creates a new credential for your account; copy it into your tool and check it works before choosing
**Revoke previous key**. Revocation immediately stops access and keeps the record in the folded
**Revoked or expired** history. An administrator can use **Delete record** there to remove inactive
metadata; the audit trail is kept. A key's secret is shown only once.

## Updating

The dashboard is not at 1.0 yet because the API is still moving. Every release is in
[CHANGELOG.md](../../CHANGELOG.md) and in the dashboard itself, where **Update now** pulls, rebuilds and
restarts from a container that outlives the restart. The update check is one unauthenticated GET of
one file from GitHub, every two hours and at most every five minutes while pages are being opened;
`JD_UPDATE_CHECK=false` turns it off.

## Uninstalling

**Settings → Configuration → Danger zone → Uninstall** takes the dashboard off the server and asks one
question first:

- **Keep for a reinstall** removes the dashboard's containers and images and closes its web-terminal
  sessions. Deployments keep running, and the data in `/var/lib/just-dashboard`, the backups and the
  `.env` (with a copy of it beside the data) stay where they are. `sudo ./install.sh` in the checkout —
  or in a fresh clone, which offers to restore the kept `.env` — brings everything back.
- **Wipe everything** also removes every deployment with its containers, managed volumes and folders,
  images, networks, routes and the public ingress, and deletes the data directory and every copy of the
  `.env`. Whether the backups go too is a separate switch, off by default. A wipe asks for the server's
  name to be typed.

Either way, databases the dashboard created, proxy sites, firewall rules, WireGuard and other network
settings are left as they are, and so is the checkout. The page follows the uninstall until the
dashboard stops; the whole transcript is in `/var/log/just-dashboard-uninstall.log`.

## Backing it up

The dashboard's own state is a SQLite database in `JD_DATA_DIR` (`/var/lib/just-dashboard`). Back up
that directory **and** keep `JD_MASTER_KEY` somewhere separate; either alone will not restore. The
Backups page lists the dashboard itself under Coverage and writes that job for you.

If you install again on a server that still has that directory but no `.env`, `install.sh` asks for
the earlier key. Without it, database connections the dashboard made from containers on the server
repair themselves under the new key; other stored secrets have to be entered again.

A backup job can encrypt its archives, and a new one that writes to S3 or B2 does unless you turn it
off. They are sealed with [age](https://age-encryption.org) to one backup key this server keeps, itself
sealed with `JD_MASTER_KEY`, and restores, recovery checks and downloads here decrypt them as they read.
**Export the key** — Backups → Backup key → Export key… — and keep the file somewhere other than the
server: if the server is lost, that file is how its encrypted archives open. On any machine with `age`:

```bash
age --decrypt --identity just-dashboard-backup-key.txt NAME.tar.gz.age > NAME.tar.gz
tar -xzf NAME.tar.gz    # source-0001/, database-0001/…: one folder per source and per dump
