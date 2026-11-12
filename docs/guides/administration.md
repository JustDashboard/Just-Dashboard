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
| `sudo ./scripts/manage.sh hub connect hub.example.ts.net` | [Connect this server to a Just Hub](linking-and-usage.md#connecting-to-just-hub) on its tailnet (prints a code to approve in the hub; `--scope`, `--token-stdin`, `--wait`) |
| `sudo ./scripts/manage.sh hub disconnect` | Disconnect it |
| `sudo ./scripts/manage.sh hub status` | Say whether it is connected |
| `sudo ./scripts/manage.sh status` | Show the containers and their health |
| `sudo ./scripts/manage.sh logs [backend\|frontend\|proxy]` | Follow logs (Ctrl+C exits) |
| `sudo ./scripts/manage.sh restart` | Recreate the stack to apply `.env` edits |
| `sudo ./scripts/manage.sh install-cli` | Install (or reinstall) the `jd` command line at `/usr/local/bin/jd` |
| `sudo ./scripts/manage.sh version` | Say what is installed: the checkout's version and commit, what the built images call themselves, and the last update |
| `sudo ./scripts/manage.sh update [0.8.6]` | [Update over SSH](#if-an-update-fails) to the newest release tag, or to the one named, when the dashboard is not there to do it |
| `sudo ./scripts/manage.sh rollback [COMMIT]` | [Go back](#if-an-update-fails) to the images and the commit from before the last update |
| `./scripts/manage.sh --help` | Usage, no sudo needed |

A role is a set of buttons, not a level of trust: `limited` writes any file as root and so can become
root, and `readonly` reads every file as root, the dashboard's own key and database included. Read
[Roles](../../SECURITY.md#roles) before giving an account to someone you would not give the server.

Passwords are entered twice with hidden input and must meet the dashboard's strength rules. The
password a script sets is the one the account signs in with — the dashboard does not ask for another
at sign-in. An account an admin creates from Account → Users, and a bootstrap password the backend
generated, are the ones replaced on first sign-in, after any required two-factor step. A reset
clears failed-login and failed-code lockouts and revokes that account's sessions, API tokens and passkeys, while keeping
two-factor enrolment, recovery codes, role and disabled state. A reset and `revoke-sessions` also end the
account's choice to be [signed in from Just Hub](linking-and-usage.md#signing-in-from-just-hub), with every
session the hub opened. Account changes are audited as local root; `revoke-sessions` leaves API tokens
unchanged.

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
restarts from a container that outlives the restart. A version is offered only once it has a release
tag (`v0.8.6`) on the project's repository, which is made after that version's checks have passed, and
the update installs exactly the tagged commit. The Version page says so beside the offer, and names a
version that is on `main` but not released yet. The update check is one unauthenticated GET of one file
from GitHub and a second small request for the tag, every two hours and at most every five minutes
while pages are being opened; `JD_UPDATE_CHECK=false` turns it off.

### What an update trusts

An update runs as root on your server and builds whatever the repository's release tag names, so it is
worth knowing what stands behind that:

- **GitHub, and the project's repository on it.** The changelog and the tag are read over HTTPS from
  `JustDashboard/Just-Dashboard`, and the code is fetched from the `origin` of your checkout. Nothing
  else is contacted, and no host can be configured in its place. `JD_UPDATE_REPO` follows a fork
  instead, and then the trust is in that fork: it is offered the head of its branch when it has no tags.
- **The release tag.** It is made by the project's release job only after the checks have passed on
  `main`. A tag is not yet signed, so anyone who could push to the repository could make one. The
  dashboard can already require a signature from a release key built into the version you are running;
  that is switched off until the project publishes its key.
- **Your own checkout.** The update fast-forwards it and never resets it, so your edits stay; it
  refuses, changing nothing, when one of them collides with the new version.

`JD_UPDATE_CHECK=false` stops the dashboard asking GitHub at all. Updating is then yours to do by hand,
with `manage.sh update` below.

A dashboard built from anything other than a release, such as a patch branch, a fork or a clone with
commits of your own, shows the commit it was built from after the version: `0.8.5+98c9349` on the
Version page and in `jd version`, where a release shows `0.8.5`. Quote the whole of it when you report
a problem.

Before an update builds anything, the dashboard copies its own database to
`/var/lib/just-dashboard/pre-update/`, as `vpsd-<the version you were on>-<when>.db`, and keeps the
newest two. If the new version does not come up the update goes back to the previous one by itself; it
puts back the code and the images, and leaves the database as it is, because the previous version can
read it. The copy is for the case where the data itself is wrong afterwards. The update's transcript and
the message of a failed update both give its path. To put it back, from the directory you installed
into:

```bash
sudo docker compose stop backend
cd /var/lib/just-dashboard
sudo mkdir -p set-aside
sudo mv vpsd.db set-aside/
sudo mv vpsd.db-wal vpsd.db-shm set-aside/ 2>/dev/null       # there only after an unclean stop
sudo cp pre-update/vpsd-0.8.5-20261110T053000Z.db vpsd.db    # the copy you are going back to
cd - && sudo docker compose start backend
```

Move the `-wal` and `-shm` files away with the database, never leave them beside the restored one:
they belong to the file you set aside. Everything the dashboard recorded after the copy was taken is in
`set-aside/` and not in what is now running. The copy opens with the same `JD_MASTER_KEY`, which has not
changed. A copy uses as much disk as the database; delete the files in `pre-update/` if you need the
room.

## If an update fails

Most failures need nothing from you. The update runs in a container of its own,
`just-dashboard-updater`, which keeps the images that were running as `just-dashboard-backend:previous`
and `just-dashboard-frontend:previous` before it builds. If the build fails, the old version never
stopped. If the new version starts and does not answer, the updater puts the `:previous` images back,
moves the checkout back to the commit it started from and starts the old version again; the Version
page then shows the update as failed, with the reason and the whole transcript.

When the dashboard is not there to show that, look from SSH, in the directory you installed into:

```bash
sudo ./scripts/manage.sh version                       # what is checked out, what is built, the last update
sudo ./scripts/manage.sh status                        # which containers are up
sudo less /var/lib/just-dashboard/self-update.log      # the last update, as it ran
sudo docker logs just-dashboard-updater                # the same, from the container a failed update leaves behind
sudo ./scripts/manage.sh logs backend                  # why the backend is not answering
```

If the old version did not come back by itself, `rollback` finishes the job. It says what it is about
to do and asks first:

```bash
sudo ./scripts/manage.sh rollback
```

That puts each `:previous` image back as `:latest`, moves the checkout back to the commit the update
started from (your own edits stay) and recreates the containers without building anything. Name the
commit, `rollback 98c9349`, when it cannot tell which one: `manage.sh version` and the transcript both
show it. By hand, the same thing is:

```bash
sudo docker image tag just-dashboard-backend:previous just-dashboard-backend:latest
sudo docker image tag just-dashboard-frontend:previous just-dashboard-frontend:latest
git reset --keep 98c9349                               # the commit you were on before the update
sudo docker compose up -d --no-build
```

A rollback leaves the database alone, because the previous version can read it. If the data itself is
wrong afterwards, put back the copy from before the update as [Updating](#updating) describes.

To move forward instead, for example to a release that fixes what failed, while the dashboard is still
down:

```bash
sudo ./scripts/manage.sh update            # the newest release tag on origin
sudo ./scripts/manage.sh update 0.8.6      # or a version you name
```

It fetches the tags, fast-forwards the checkout to the release, keeps the running images as
`:previous`, rebuilds and restarts, and prints the `rollback` command that undoes it. It installs only
a release tag, and checks the tag's signature once a release key is in the checkout. Unlike **Update
now** it copies no database first and does not go back by itself, so use the Version page whenever the
dashboard is up. `sudo ./install.sh` remains the way to rebuild the checkout exactly as it stands.

## When the server's disk fills

The dashboard's own database lives on the server's disk, so a full disk used to mean nobody could sign
in to find what filled it. It now keeps 32 MB back (`/var/lib/just-dashboard/staging/disk-reserve`) and
gives that space up the moment its database is refused room, so signing in and deleting things keep
working for a while. The Overview then opens with **The server's disk is full**, the free space and
where; a save that still fails says the disk is full rather than "internal error". Free space (Files,
Docker's image and build-cache cleanup, old backups) and the reserve is written back by itself once
about 256 MB is free. The container stays "healthy" throughout on purpose: restarting the dashboard, or
rolling back an update, would not empty a disk.

## Uninstalling

**Settings → Configuration → Danger zone → Uninstall** takes the dashboard off the server and asks one
question first:

- **Keep for a reinstall** removes the dashboard's containers and images, with the build cache nothing
  has used for a week, and closes its web-terminal sessions. Deployments keep running, and the data in `/var/lib/just-dashboard`, the backups and the
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

To put the dashboard's own data back, onto the same server or a new one:

1. Install the dashboard if it is not there (`sudo ./install.sh` in a clone), with the **same**
   `JD_MASTER_KEY` in `.env` as the server the backup came from. The installer asks for the earlier key
   when it finds the data without a `.env`; on a new server put the key in `.env` before you start.
2. Stop the backend: `sudo docker compose stop backend`.
3. Set aside what is in `/var/lib/just-dashboard` now (`vpsd.db`, with `vpsd.db-wal` and `vpsd.db-shm`
   when they exist), then copy the backed-up directory's contents into it. `vpsd.db` is the database.
   If the backup has `vpsd.db-wal` and `vpsd.db-shm` beside it, restore all three together, and never
   leave the ones you set aside next to a restored `vpsd.db`: they belong to the file they were taken
   with.
4. Start it again: `sudo docker compose start backend`, then `sudo ./scripts/manage.sh status`. With a
   different key the dashboard still starts, but the secrets it stored cannot be read and have to be
   entered again.

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
