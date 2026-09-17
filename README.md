<div align="center">

# Just Dashboard

**One authenticated UI for one Linux server.**
Metrics, Docker, processes, logs, a real shell, files, git, databases, the reverse proxy,
the firewall, backups and deploys, behind a login that lives on your private network.

**Version 0.6.7** · Go backend · Next.js frontend · one `docker compose` stack

[Install](#install) · [Security](#read-this-before-you-expose-it) · [The tour](#the-tour) · [Version](#version-and-updating) · [Configuration](#configuration-reference) · [Licence](#licence)

</div>

![The overview page](docs/overview.png)

---

## Why

Most panels of this kind show you a number and leave the reading to you: 68% CPU, exit code
137, restarted 12 times. Fine, and then what?

Just Dashboard tries to answer the next question instead. It records its own history so the
charts cover the night nobody had the tab open. It tells you the server is *waiting* rather
than merely *busy*, because iowait, CPU steal and kernel pressure have completely different
fixes. It says a container was killed for exceeding its memory limit and what the limit was.
It renders the `docker run` line before it runs anything. It keeps a shell alive so tomorrow
you pick up where you stopped.

It manages exactly one machine. There is no fleet view, no agents to enrol, no cluster.

## Install

```bash
git clone https://github.com/Wayy01/Just-Dashboard.git
cd Just-Dashboard && sudo ./install.sh
```

A handful of questions, and only one of them really matters: how you intend to reach it.
There are two answers, and neither puts anything on the public internet.

**Tailscale is the default.** Your laptop and phone get in from anywhere while the machine
stays invisible to the internet, and the installer will set it up for you. Where the tailnet
has HTTPS enabled it also asks Tailscale for a certificate, so the dashboard answers at
`https://your-box.tailnet-name.ts.net:8443` with an ordinary padlock — a real Let's Encrypt
certificate for a name that resolves only inside your tailnet, renewed by the dashboard
before it expires. **An SSH tunnel is the fallback**: nothing to install, no account, and it
works on a day Tailscale does not. That one is served over plain HTTP on loopback, which is
not a downgrade — the tunnel is already encrypted and authenticated, and browsers treat
`http://localhost` as a secure context, so there is no warning to click through either.

The two ports behind the one you connect to are picked at random from the high range, so a
new install cannot collide with whatever this machine already runs on 3000 or 8080.

The installer then generates the master key and a first password, writes `.env`, builds,
waits for the stack to answer and prints the exact command to get in. Re-running it later
keeps your `.env` and just rebuilds, so it is safe after a `git pull`.

Before rebuilding, the installer checks for an interrupted `dpkg` transaction and stops with
the recovery command if packages are half-configured. Run that recovery from SSH because completing
a pending Docker upgrade can restart the daemon and briefly interrupt the dashboard.

Everything it asked about — address, certificate, ports, allowlist, two-factor — is editable
afterwards from **Operations → Dashboard → Configuration**, which restarts the stack into the
change and puts the previous configuration back if it does not come up.

### Upgrading an install you already have

`git pull` and `docker compose up -d --build`, or the in-app update, or `sudo ./install.sh` again —
all three keep your `.env`, your database, your accounts and your sessions. There is no schema
change in this release and nothing to migrate.

Three things worth knowing before you do it:

- **Two-factor becomes optional unless you say otherwise.** An account that has already enrolled an
  authenticator is still asked for its code at every sign-in — that does not change. What changes is
  that an account *without* one can now sign in on its password. Re-running `install.sh` asks you
  which you want; upgrading any other way takes the new default, so set `JD_REQUIRE_2FA=true` in
  `.env` first if this install is shared.
- **Your address and certificate settings are preserved.** `JD_TLS` defaults to `internal`.
  Running dashboard ports stay put; ports occupied by another service are moved automatically,
  with the selected dashboard address printed in the startup log. Certificate settings remain
  editable under Operations → Dashboard → Configuration.
- **`docker-compose.yml` and `deploy/Caddyfile` both changed.** If you have edited either by hand,
  the in-app update fast-forwards and will stop with the conflict rather than discarding your edits;
  resolve it in the checkout and run the update again.

Prefer to do it by hand? [Setting it up without the installer](#setting-it-up-by-hand).

## Read this before you expose it

**This dashboard is root-equivalent.** It drives systemd, the firewall, host accounts, the
Docker socket and a PTY. Anyone who reaches it with a valid session has root on the machine.

It is built to sit behind a VPN or an SSH tunnel, and that is enforced rather than suggested:

- The backend **refuses to start** on a non-loopback address without an explicit
  `JD_ALLOWED_CIDRS` allowlist.
- The allowlist is checked **before authentication**. Off-network you cannot reach the login
  handler at all, let alone guess at it.
- Two-factor is **enforced for every account that has it**. A session that owes a code is
  rejected by every route except the 2FA ones. Whether an account *must* enrol is a setting
  (`JD_REQUIRE_2FA`, off by default): the perimeter above is what stands in front of an
  install with a single operator, and an authenticator that cannot be skipped was a chore
  rather than a defence there. Turn it on for anything shared — Configuration, or `.env`.
- Destructive actions pause for a confirmation, and the rare, unrecoverable ones — dropping a
  table, removing a volume, deleting an account, restoring over live data — additionally
  require a **typed confirmation phrase**, checked on the server, so it cannot be skipped by
  calling the API directly. The line is drawn by *frequency*: a phrase in front of something
  done a dozen times a day gets typed rather than read, which is exactly how it stops working
  on the routes that need it.
- Every state-changing request lands in an **audit log**: who, what, when, from where, and
  whether it worked.

The **Security** page reports how the dashboard is actually reachable and says so plainly
when that is wider than a private network. A machine that quietly became internet-facing
announces itself instead of waiting to be discovered.

---

## The tour

### Overview: utilisation, and whether the server is saturated

CPU split by user, system, iowait and **steal**. Memory judged on what is *available*, never
on "used", because Linux counts the page cache there and judging a server by it produces a
permanent, meaningless warning. Kernel pressure from PSI, the run queue and its blocked
count, per-filesystem capacity **and inodes**, disk throughput with IOPS and service time,
socket totals, per-interface rates, and a directory size scan when you ask for one.

Hovering any chart marks the same instant on every other chart on the page, and every legend
switches to the value its series held at that moment. Drag across a chart to zoom into a
span; zooming out returns to the window you were in, not to where you started.

Live data is pushed over a WebSocket. The 1h, 6h, 24h and 7d windows come from history the
backend records on its own timer, peaks kept next to means, so a 100% second inside a
ten-minute bucket does not average away into a quiet night that was not quiet.

Deploys, backups, reboots and destructive actions are marked on the charts, so a step in a
line sits next to whatever caused it. Reboots are inferred from a sample whose uptime is
lower than its predecessor's, which also catches the restart nobody started from here.

**Health** turns all of that into a verdict: disks and inodes filling, memory headroom, CPU
steal, kernel pressure, swap, socket exhaustion. Each finding carries what was measured, what
it means and what to do about it. It is evaluated on the server against the last hour of
history, so a spike is told apart from a trend, and it is visible from every page.

### Everything is one keystroke away

![The command palette](docs/command-palette.png)

**⌘K** from anywhere. Every page in the nav, because a server dashboard is
navigated by someone who already knows where they are going. The top bar keeps CPU, memory and
the health verdict in view while you are elsewhere.

**Every page comes back the way you left it.** The panel you hid, the folder you collapsed, the
tab you were on, the sort you chose and the rail you narrowed are remembered in the browser you
are sitting at — so leaving a page and returning to it is not a page you have to set up again.
What you were *looking at* is not: a search box, a selected row and a half-filled form all start
empty, because a filter restored from yesterday is a table that looks broken.

### Docker: a panel you can run things from

![The container list, with the verdict above it](docs/docker.png)

**Create container** from a template, from a pasted `docker run` command, or from a
blank form. The command and the compose service it would produce are rendered back to you,
by the server, before anything runs. Templates are a set of sane starting points with ports
bound to loopback, not an app store to maintain.

**Update one in place.** Pull a newer image and rebuild the container from the settings it
already has. The old container is renamed aside and restored if anything goes wrong, and
removed only once the replacement is up.

**Two verdicts, not one.** *Runtime health* is what Docker itself reports — running, exited,
unhealthy, and how many containers have no health check at all, which is the number that
stops "everything is up" quietly meaning "nothing is being watched". *Attention* is
everything else: security posture, disk, configuration, exposure. A container can be
perfectly healthy and still need attention, and the panel says so rather than picking one
word for both.

**Explanations, not numbers.** Why a container exited and what the limit was. That it has
restarted seventeen times in twelve minutes — a cadence read from the recorded event stream,
because Docker's own counter cannot tell that from forty restarts across a year. That a
health check is failing and what it last said. That PostgreSQL is published on every
interface, that the firewall has a rule denying the port, and that Docker's NAT rules are
consulted first so the rule does not apply to it. Where the dashboard can carry out the fix,
the finding comes with a button; where the conclusion was inferred rather than read, it says
"likely" and shows the evidence it was drawn from.

**Where the writable layer went.** Not "38.7 GB" but which directory holds it, whether that
directory is backed by a volume, and how fast it is growing — measured from recorded history,
so "up 6.4 GB today" is a measurement rather than a guess. Where data is sitting in a
container's own filesystem and will vanish on the next image update, the dashboard writes out
the migration to a named volume as steps and exact commands, and leaves you to run them.

**Stacks as the applications they are.** Deploy, Pull & redeploy, Rebuild, Restart and
Stop & remove stack — named for what they do to the server, with the compose command they run
and what they touch in the confirmation. Each is streamed line by line rather than hanging on
a request for four minutes. Before you press one, the deploy preview says which services are
expected to be recreated, shows the compose diff against the last deployment, and states
explicitly that no volume is removed. Afterwards the previous compose file and the image
digests that were actually running are kept, so "what changed" and "put it back" have
answers — and a rollback that could not find its images says so before you commit.

**Cleanup as a decision.** Each category of removable object states what it holds, what
removing it reclaims, and what that costs. Volumes are always listed, never selected by
default, and need a typed phrase — they are the one category that is the data.

**The rest of the surface.** Live stats and a named last-hour sparkline per container row,
with CPU that says what its denominator is and memory that says "no limit" rather than
showing host RAM as a budget nobody set. Recorded per-container CPU, memory, network, block
I/O and writable-layer size, keyed by name so it survives a redeploy. Images that distinguish
a moving tag, a pinned digest, a locally built image and a dangling one, with an update check
that asks the registry what the tag points at *now*. Volumes with what mounts them, whether
the size is known or simply not measured, and a link to browse inside. Networks with who is
attached and what they reach each other by. Streaming logs, a shell in the container, raw
inspect, and the daemon's own event stream kept in memory — correlated against the audit log,
so a container removed at 03:14 says whether somebody did it from here or from somewhere
else.

### Terminal: host shells in one workspace

![The terminal](docs/terminal.png)

A real PTY over a WebSocket, running `su -l` into a host account, not a shell inside the
container. Your dotfiles, your PATH, your installed tools.

The installer puts zsh on the host with `zsh-autosuggestions` and `zsh-syntax-highlighting`
and points the terminal at it: commands are coloured as you type, and the grey text that
finishes a command from history is accepted with the right arrow. Your `ssh` login shell is
not changed; blank `JD_TERMINAL_SHELL` in `.env` if you want the terminal to follow `chsh`.

Each session groups independent shell windows. Name a session, file it in a folder, drag it
somewhere else, filter the list, and pin the sessions you use most. Windows appear along the
top, with rename, reorder and close controls. Closing one window stops that shell; closing a
session stops all its windows, with confirmation first.

Visited windows keep their terminal screen and connection while you switch windows or
sessions, so full-screen tools retain their state and background output keeps arriving.
Leaving the page disconnects the browser; returning uses bounded, best-effort output replay.
Shells and session organization live in the dashboard process and end when it restarts.

Moving between sessions and windows has a key for each, and every binding is yours to
change: the shortcut sheet is the editor, not a read-only list. Each window has scrollback
search, copy, font size, clear, fullscreen and a row of the keys a browser normally eats
(`Ctrl+C`, `Ctrl+D`, `Ctrl+Z`, `Ctrl+L`, `Esc`, `Ctrl+\`). Middle-click pastes, the way it
does in X11.

The page has no title band, because a terminal is the one screen whose content is the
viewport. Opening and closing a session is recorded in the audit log.

### Files: browse, edit, and mean the host's paths

![The file manager](docs/files.png)

Monaco for editing, with the mode editable next to the save button. Upload, download, chmod,
chown, search by name or by content, archive and extract.

Every client-supplied path goes through one resolver that checks the cleaned path *and* the
symlink-resolved path against `JD_FILE_ROOTS`, including the paths that do not look like file
operations: a bind mount source, a build context, a restore destination. Archive extraction
refuses absolute symlink targets and never writes through a symlink already sitting in the
destination.

### Git: the repositories that are actually on the server

![A repository, with a working-tree diff open beside its history](docs/git.png)

Every repository under the configured roots, found by walking them rather than by being
registered. Branch, ahead/behind, working tree, history with diffs and per-commit line
counts, branches, and fetch, pull, push and stash.

Each command runs as the account that owns the repository, so a pull on a repo owned by
`deploy` does not leave root-owned files behind for you to find later.

Sign in to GitHub from the page itself: the same device-code flow `gh auth login` uses,
rendered as a screen rather than a series of prompts. The token is stored where `gh` keeps
its own — under the home of the account that owns the checkout, which is the account that
pushes — so commits are recorded as you, pushes are authenticated, and pull requests can be
opened from the branch you are on without leaving for a browser tab.

### And the rest

| | |
| --- | --- |
| **Processes** | PM2 apps with merged output tailing, systemd units with journal streaming, and an htop-style table sortable by CPU **or** memory, because a leaking service sits at 0% CPU holding gigabytes. Kill is guarded. Crontab editor included. |
| **Logs** | One viewer over files, container output, PM2 and the journal. Grep and level filters are applied on the server, before the lines are sent. |
| **Proxy & TLS** | A form that puts a domain in front of a port and writes the nginx for you — TLS, HTTP/2, HSTS, WebSockets, upload limits, IP allow lists, basic auth and extra paths sent somewhere else — /api to a backend while everything else goes to a static build — rendered on the server and shown live next to the form, so the file it produces is ordinary nginx you can commit and edit by hand. Password files managed here too, so the basic-auth option has something to point at. Streams forward the services that do not speak HTTP. Certificates issued, renewed and revoked through certbot in a live console rather than a request that hangs, including wildcards over a DNS challenge with eight provider plugins, and certificates you bought imported with the key checked against them first. A live TLS report grading what a visitor actually gets. |
| **Databases** | Eight engines: PostgreSQL, MySQL/MariaDB, SQLite, SQL Server, ClickHouse, Oracle, MongoDB and Redis — all on pure-Go drivers, so the image still needs no CGO. A data grid that edits rows through forms (always scoped to a primary key), server-side sort and filtering, schema editing with the statement shown before it runs, CSV/JSON import inside one transaction, a structure view, an entity diagram that remembers how you arranged it — drag, hide, colour and annotate tables, read a table's relations both ways in its inspector, jump from any table to its rows, structure or a query, go full screen, and export the picture as PNG, SVG, Mermaid or DBML — a query runner that classifies a statement as destructive before it runs with schema-aware completion, history and saved snippets, CSV/JSON export, one-click Prisma, Drizzle, TypeScript or Zod generation from the live database, and a value search that finds which table an id lives in without knowing where to look. A Monitor tab lists what the server is running right now — with the blocking session named — and stops a stuck query, next to a per-table size breakdown for when the disk alert fires. Any row copies out as JSON or as a runnable INSERT in that engine's own syntax, or duplicates into a pre-filled form. MongoDB gets document editing, an aggregation runner, and its own export and import; Redis gets a SCAN-based key browser with full collection editing. Plus a dump that downloads to the browser as it is written, restores, and a typed-confirmation delete of the database itself, for every one of the eight — the three with a client-side tool use it, the rest are dumped over the connection the dashboard already has, so no engine's backup depends on a binary that may not be installed. Passwords never appear in argv. Optional live tests exercise configured engines and skip unavailable servers. |
| **Security** | A verdict on the host, not just its settings: exposure, firewall, sshd, intrusion prevention, open ports, certificates and pending security patches, each finding carrying what was measured, what it means, what to do, and where the dashboard can do it, a button. Firewall rules on ufw **or firewalld**, with a named-service catalogue that warns before you open Redis to the world, default policies, logging, ordering, editing (the replacement goes in before the original comes out, so the port is never briefly unprotected) and outbound rules. sshd's own settings — root login, passwords, keys, port, account lists — applied through its parser and rolled back if it objects, refused outright when the change would leave nobody a way in, and streamed step by step so "it said it worked" and "the daemon came back" are not the same claim. fail2ban jails tuned in place and kept across a restart, folded into the one question a ban list cannot answer: who keeps coming back. Live connections by peer, interfaces and routes, the host's login record with the ability to end a session, and ping, DNS, traceroute and port checks on the page the question came from. |
| **Updates** | Two things that can be behind. The dashboard itself — with the release notes for every version between yours and the newest, and a one-click pull-rebuild-restart that runs in its own container so it survives replacing the dashboard. And the host's packages: what is behind, which of it is security, and whether a reboot is due, on apt, dnf, yum, zypper, pacman or apk. Alpine and Arch publish no advisory data, so they say so rather than reporting zero security updates. Upgrades run as a job with its output streamed, so closing the tab does not abandon a half-finished run. Upgrades only — it never installs or removes packages. |
| **Deployments** | Browse projects in a searchable grid or list. Import a connected GitHub repository or any HTTPS/SSH Git URL, configure environment variables, and create or connect a database before deploying. Follow numbered, searchable build logs and open the finished URL; each project opens on a website preview, release details, recent deployments and measured usage, with separate settings for configuration. A deployment is a plan, a run, and an immutable release. Point it at a Git repository, a registry image, a Compose file, or an existing container; it detects what the thing is, shows you the exact plan before anything happens, and runs it as a queued job with a permanent URL you can close the tab on. Every release records its source revision, image digest, configuration digest and variable digests, so rolling back reactivates a retained artifact through the same checks and cutover path rather than rebuilding and hoping. An HTTP service with somewhere to put a candidate gets a health-gated cutover; a database, a game server or anything holding an exclusive volume is told, before it runs, that it will stop first. Domains, ports, storage, backups and databases are *linked* to their own pages rather than reimplemented — the workspace reads each owner and says so when one cannot be reached, instead of showing an empty panel that looks healthy. Automatic production Git deployments, plus signed provider hooks, scheduled actions, PR previews, and notifications to Discord, Slack, Telegram, e-mail or a signed webhook for every started, succeeded, failed or cancelled run. Runs report their state to GitHub commits, a failed health check shows the application's own last output next to the failure, runtime settings cap memory, CPU and processes, and a Console tab opens a shell inside the live container. Notifications that fail are retried with backoff, and the Deployments tab shows delivery figures computed from the run history: success rate, deploys per week, median release time, recovery time and the current failure streak, each with its basis. |
| **Blueprints** | Seventeen reviewed, versioned definitions with a browsable catalogue and deterministic previews. PostgreSQL, MariaDB, MongoDB, Redis, MinIO, Adminer, Dozzle, Grafana, n8n, Uptime Kuma, Vaultwarden and the static nginx site deploy in one click as immutable image releases: the image is pinned to a digest at inspection, inputs become variables, declared passwords are generated on the server and only ever revealed on demand, data volumes are managed storage, and the definition's readiness checks and memory limit gate activation. Blueprints that install configuration files or download artifacts, and game servers, stay preview-only and say exactly why. |
| **Game servers** | Adapters for existing Minecraft Java and Bedrock deployments provide supported console/player controls and declared `server.properties` settings. Property reads exclude undeclared credentials, and edits preserve unrelated settings. New game-server deployments through blueprints are unavailable in this release; UDP runtime deployment and Bedrock save/backup automation remain unsupported. |
| **Backups** | Scheduled archives to local disk, S3 or Backblaze B2, with retention and restore. A job can also name saved database connections: every run captures a native dump of each (pg_dump, mysqldump, mongodump, a Redis snapshot, or the built-in dump when the tool is not installed) into the same archive with its own manifest evidence, a deployment linked to that database accepts the dump as its backup coverage, and any run can restore a dump back into the connection or into a drill database on the same server with a typed confirmation. |
| **System users** | Host accounts, SSH keys, lock and unlock. |
| **Audit log** | Every state-changing request, filterable by actor, action and outcome. |
| **Dashboard → Configuration** | The panel's own settings: the address and port it answers on, which certificate it presents, the network allowlist, whether two-factor is compulsory, session lifetimes, and the internal ports. Applying a change restarts the stack into it from a container that outlives the restart, narrates each phase, and **puts the previous configuration back automatically** if the new one does not come up. Restart and rebuild live here too. |
| **Appearance** | One dark palette, applied before the page paints. There is no light mode and no theme switch — this is a console you keep open beside the thing you are fixing, and a second ground was two of everything to maintain. How you left each page arranged belongs to the browser you are sitting at, not to the account. |

---

## Deployment certificates

Automatic deployment certificates use Let's Encrypt HTTP-01, which requires the deployment hostname
to resolve publicly and port 80 to reach the server. Generated `sslip.io` names use public interface
addresses; private and Tailscale/CGNAT addresses are excluded. Behind provider NAT, enter a domain
pointing to the provider's public IP yourself.

When nginx already serves port 80, the dashboard issues through a shared webroot while nginx keeps
serving traffic. It checks the challenge route before ordering and removes temporary configuration
afterward. Standalone Certbot is used only when port 80 is free. If another server owns that port,
configure its challenge routing or provision a DNS-01 certificate; the dashboard reports the owner
instead of stopping it.

If an older deployment has a hostname containing a Tailscale address such as
`100-110-34-31.sslip.io`, change its domain and create a new release. Retrying the failed run keeps its
original hostname. For private access, use a domain you control and provision its certificate through
DNS-01 on the Certificates page before deploying. An `sslip.io` name pointing at a Tailscale address
cannot pass the public HTTP challenge.

Git projects default to deploying new commits from their selected production branch after the first
deployment. The server checks the branch every five seconds over an outbound Git connection, so this
works while the dashboard remains private. **Deployment policy** on the overview or Automations page
can disable automatic deployments or set repository-relative include/exclude paths. Polling and push
webhooks share the same branch, complete Git change comparison and commit deduplication. Manual-only
mode also blocks new signed-hook and scheduled deployments; already queued runs continue. An unchanged
failed revision is not retried repeatedly. Repository failures and suppressed-change reasons appear
under **Automatic deployments**. See [automatic deployment policy](docs/internal/deployments/git-policy.md).

For eligible web and static applications, the old release keeps serving while the new candidate builds
and passes its checks. Traffic switches automatically only after success; a failed candidate leaves the
old release in place. Stateful and other stop-first workloads retain their documented restart and
recovery behavior. Default HTTP readiness follows redirects only within the candidate's origin and
requires a final 2xx response; redirecting to a failing page or an external site does not count as ready.
Additional signed webhooks, preview environments and schedules remain separate integrations. Webhook
watch-path filters do not disable the default production branch monitor.

Pull-request previews require administrator approval of each exact commit. Review it under
**Settings → Automations → Preview environments**, configure the preview's own variables, then deploy.
Production credentials and database links are not inherited; container previews receive separate storage
and a dedicated network. Compose and host-access preview plans are currently refused. PR close removes
only preview-owned resources and archives the environment after cleanup succeeds.
Preview storage is disposable: closing the PR deletes its owned volumes. Reopening waits for cleanup
and requires approval again. On upgrade, older unsafe previews are stopped and their routes withdrawn;
their existing containers and data are retained. The preview list reports isolation failures and guides
fresh approval/configuration before another deployment.

For automatic recipes, build-scoped variables reach the build command without extra mapping. Use
**Build settings** to limit private package credentials to dependency installation. Values compiled
into browser assets (including `NEXT_PUBLIC_` and `VITE_` settings) are public. Static sites use port 80;
SvelteKit requires its Node or static adapter. Go builds select a toolchain from the source or an explicit
Go version, and custom build commands must produce `/out/app`. CGO workloads use a Dockerfile.

Connecting a database saves a reference to its encrypted connection. Locally provisioned databases use
a stable hostname on the deployment's managed network; matching database container replacements are
reconnected automatically even when their IP changes. Applications must retry lost connections. Existing
literal IP settings need reconnecting once to adopt this behavior. **Configuration → Dependencies** shows
the network status. Removing that network leaves the database and its data intact. See
[database connections](docs/internal/deployments/database-networks.md) for supported cases and cleanup.

To remove a project from the active deployment list, open it and choose **Archive deployment** in the
header’s actions menu. Confirmation archives its history, disables automatic deployments, and frees its name for a
new project—even from the same repository. Existing deleted projects get this name-reuse fix on upgrade.
Run numbers start at **Run #1** for each new project and increase with each run, including retries.
Existing history receives project-specific numbers on upgrade; saved run links keep working. Running containers,
routes and data are retained; use **Settings → Lifecycle** to preview and
remove managed resources separately.

Open **Archived** on the deployments page to search retained projects. **Delete permanently** removes
an archived project’s saved configuration, variables, and history after confirmation. It leaves host
resources in place and forgets their deployment ownership; use managed-resource removal first if you
also want those resources removed. Unfinished runs must complete before permanent deletion.

Deployment overviews automatically preview a live website with mobile and desktop widths. Runtime
logs and live/recorded service metrics are available inside the deployment's own tabs. If a website
blocks embedded previews or requires a separate sign-in, use **Visit** to open it directly.

Container deployments supply `PORT` from the selected application port unless you explicitly configure
it as a runtime variable. Readiness checks follow the actual allocated host port and report the failed
check, address, and reason. Compose and host-network workloads retain their own port configuration.

## Version, and updating

This is **0.6.7**: the panel as a finished single-server product — every page in the tour
above is built and in use. It is not 1.0 because the API is still moving. 1.0 is when it
stops. Every release is in [CHANGELOG.md](CHANGELOG.md), and in the dashboard itself.

The number is on screen beside the logo in the sidebar and on the sign-in page, and the
server says so at boot as well:

```bash
docker compose logs backend | grep listening
```

### The dashboard updates itself

When a newer version is published, a notice appears above your account at the foot of the
sidebar: the version, what the release is called, **Update now**, and **View changes**. The
last of those opens the release notes for every version between yours and the newest — not
just the newest, because an install three releases behind is upgrading past three sets of
changes.

**Update now** pulls this repository, rebuilds every image in your stack and restarts it.
It runs in a container of its own, so it survives the dashboard being rebuilt underneath it;
the page keeps watching across the restart and shows you the build output as it happens.
Then it waits for the dashboard to answer again before calling itself done — `compose up -d`
returning is not the same as your panel being back. It asks you to type the version first,
which is the same guard the handful of other irreversible actions use.

Two things it deliberately will not do. It **fast-forwards rather than resetting**, so an
edited `docker-compose.yml` or Caddyfile survives; if your edit collides with the release,
the update stops and tells you what is in the way instead of discarding it. And it never
touches your data, accounts, sessions or settings — those live in `JD_DATA_DIR`, which the
upgrade does not go near.

If your install cannot be updated this way — you run it from a binary, or from a directory
the dashboard cannot identify — it says so, with the reason, on the Updates page. The
release notes still work; they are compiled into the build.

The update check makes an unauthenticated GET of one small file from GitHub, carrying a version
number in the user agent and nothing else. No telemetry, no install id, nothing reported
anywhere. `JD_UPDATE_CHECK=false` disables update discovery. Automatic deployment branch monitoring
uses separate outbound Git connections and continues independently.

### Cutting a release

If you are working on Just Dashboard rather than running it, a release is one command:

```bash
# 1. Write the notes in backend/internal/selfupdate/changelog.json
# 2. Then:
scripts/release.sh 0.6
```

That bumps the version in the three places it appears, regenerates `CHANGELOG.md` from the
changelog, and runs the tests that pin all four together. It refuses to cut a version the
changelog does not describe — every install decides whether to update by comparing itself
against that file, so a release with no entry is a release nobody hears about.

---

## Roles

Capabilities are checked on the route, never in the UI alone. The frontend hides what a role
cannot use; the server re-decides every request anyway.

Roles divide what you can *change*, not what you can *see*. "View everything" is meant
literally: a `readonly` account reads any file inside `JD_FILE_ROOTS`, any compose file, any
proxy config and any deploy log, and those routinely hold credentials. Container environments
are redacted below `system.admin`, which raises the cost of reading a secret rather than
preventing it. Give `readonly` to someone you would let read the disk, and narrow
`JD_FILE_ROOTS` if that is not what you meant.

| | `readonly` | `limited` | `admin` |
| --- | :---: | :---: | :---: |
| View everything | ✅ | ✅ | ✅ |
| Start / stop / restart services | | ✅ | ✅ |
| Git fetch / pull / push / checkout | | ✅ | ✅ |
| Edit files | | ✅ | ✅ |
| Terminal and container shells | | | ✅ |
| Delete, prune, kill, restore, git reset | | | ✅ |
| Apply system updates | | | ✅ |
| Host accounts, firewall, users, tokens | | | ✅ |

New accounts must change their password before anything else works. Enrolling an
authenticator is offered on the Account page and required at sign-in only where
`JD_REQUIRE_2FA` is on; an account that has enrolled is always asked for its code.

---

## Configuration reference

<details>
<summary>Every environment variable the backend reads</summary>

<br>

The installer writes the ones that matter. These are for tuning afterwards.

**Required**

| Variable | Description |
| --- | --- |
| `JD_MASTER_KEY` | 64 hex characters. Encrypts every stored secret. `openssl rand -hex 32`. |

**Network perimeter**

| Variable | Default | Description |
| --- | --- | --- |
| `JD_SITE` | `localhost` | The address the stack answers on and the name on its certificate. Your machine's MagicDNS name (`box.tailnet-name.ts.net`) is the recommended value. Loopback is bound alongside it either way, so an SSH tunnel always works. Never `0.0.0.0`. |
| `JD_BIND` | none | What the proxy listens on, when that is not the same string as `JD_SITE`. Blank uses `JD_SITE`. A Tailscale install answers for a MagicDNS name and binds the tailnet IP behind it, because the proxy container resolves names through Docker's resolver rather than the host's. |
| `JD_TLS` | `internal` | How the connection is trusted. `tailscale` serves the certificate `tailscale cert` issues for `JD_SITE` — publicly trusted, no browser warning, renewed automatically. `internal` is Caddy's own CA, which is what produces "not secure". `off` is plain HTTP and is refused on anything but loopback. |
| `JD_ALLOWED_CIDRS` | `127.0.0.1/32,::1/128` | Who may reach the API at all, checked before authentication. Use `100.64.0.0/10,127.0.0.1/32,::1/128` for Tailscale, and keep loopback or you lose the tunnel. |
| `JD_TRUSTED_PROXIES` | none | Addresses allowed to set `X-Forwarded-For`. Without it a client could spoof its way past the allowlist. One hop is supported: the bundled Caddy replaces the header with the client's real address, so anything placed *in front* of Caddy becomes the client as far as the allowlist is concerned. |
| `JD_PORT` | `8443` | The port you connect to — the only one the proxy publishes. |
| `JD_BACKEND_PORT` | `8080` | The API's loopback port, behind the proxy. `install.sh` picks a random high port on a new install so nothing collides with it. |
| `JD_FRONTEND_PORT` | `3000` | The UI's loopback port, behind the proxy. Likewise randomised at install time. |
| `JD_ADDR` | `127.0.0.1:$JD_BACKEND_PORT` | Where the API binds, if you need to override the host as well as the port. Leave it on loopback; the proxy is the entry point. |
| `JD_ALLOWED_ORIGINS` | none | Complete browser origins allowed to open WebSockets. Scheme, host, and port must match; only needed if the UI is served from a different origin. |

**Behaviour**

| Variable | Default | Description |
| --- | --- | --- |
| `JD_REQUIRE_2FA` | `false` | Whether an account with no authenticator may sign in. An account that *has* enrolled is always asked for its code, whatever this says. |
| `JD_TERMINAL_ENABLED` | `true` | The web terminal. |
| `JD_TERMINAL_SHELL` | account's shell | The shell the web terminal opens. Empty honours `chsh`. `install.sh` sets it to the host's zsh, which the bundled startup gives inline history suggestions and command colouring; ssh is unaffected. |
| `JD_TERMINAL_USER` | lowest regular account | Host account a terminal session logs in as. |
| `JD_SESSION_TTL` | `12h` | Absolute session lifetime. |
| `JD_SESSION_IDLE_TTL` | `60m` | Idle timeout. |
| `JD_DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker Engine endpoint. |
| `JD_DEPLOY_HEAVY_SLOTS` | `1` | Concurrent deployment build/activation workers. Valid range: 1–8. |
| `JD_DEPLOY_LIGHT_SLOTS` | `2` | Concurrent validation/metadata deployment workers. Valid range: 1–8. |
| `JD_DEPLOY_LEASE_TTL` | `30s` | Time before a silent deployment worker claim is reconciled after a crash. Valid range: 5s–5m. |
| `JD_METRICS_INTERVAL` | `15s` | How often the backend samples the host, and every running container, into its own history. Clamped to 5s and 5m. |
| `JD_METRICS_RETENTION` | `7d` | How long that history is kept. Accepts days. `0` records nothing and leaves only the live feed. |
| `JD_UPDATE_CHECK` | `true` | Whether the dashboard may ask GitHub whether a newer version of *itself* exists — one unauthenticated GET of one file, four times a day, carrying nothing but a version number. `false` turns it off; the release notes for the version you run stay readable, since they are compiled in. |
| `JD_UPDATE_REPO` | `Wayy01/Just-Dashboard` | The repository releases are read from. Change it to follow a fork. |
| `JD_UPDATE_BRANCH` | `main` | The branch whose changelog decides what "newest" means, and which an in-app update fast-forwards to. |
| `JD_UPDATE_DIR` | discovered | The directory you cloned into. Normally empty: the dashboard asks Docker where its own stack was deployed from. Set it only if the Updates page says that failed. |
| `JD_AGENT_MODE` | `false` | Run as an agent managed by a hub: no login, mutual TLS only. Not useful on its own yet. |
| `JD_DEV` | `false` | Development only. Drops `Secure` from the session cookie so the UI works over plain HTTP. Never set it on a real host. |
| `JD_LOG_LEVEL` | `info` | `debug` for verbose logs. |

**Where it looks**

| Variable | Default | Description |
| --- | --- | --- |
| `JD_FILE_ROOTS` | `/` | Directories the file manager may reach. |
| `JD_LOG_ROOTS` | `/var/log` | Directories the log viewer may read. |
| `JD_COMPOSE_ROOTS` | `/opt,/srv,/home` | Where compose stacks are discovered. |
| `JD_GIT_ROOTS` | `/opt,/srv,/home,/root` | Where the Git page looks for repositories. |
| `JD_DEPLOY_ROOTS` | `/opt,/srv,/home,/root` | Where a deploy project's repository may live. A project outside these is refused. |
| `JD_NGINX_DIR` | `/etc/nginx` | nginx configuration root. |
| `JD_CADDYFILE` | `/etc/caddy/Caddyfile` | Caddy configuration file. |
| `JD_BACKUP_DIR` | `/var/backups/just-dashboard` | Local backup destination and staging. |
| `JD_DATA_DIR` | `/var/lib/just-dashboard` | The dashboard's own database. **Back this up.** |

**First run only**

| Variable | Default | Description |
| --- | --- | --- |
| `JD_BOOTSTRAP_USER` | `admin` | Account created on an empty database. |
| `JD_BOOTSTRAP_PASSWORD` | none | Leave empty for a generated one, logged once. |

Durations take a unit (`12h`, `60m`, and the metrics settings also accept `7d`); booleans
take `true` or `false`. A value that cannot be parsed stops the dashboard at startup rather
than falling back to the default, so a typo is visible instead of silently in effect.

</details>

<details>
<summary>Upgrading from VPS Dashboard</summary>

<br>

This project used to be called VPS Dashboard. Its settings were prefixed `VPSD_`, it kept
state in `/var/lib/vps-dashboard`, and its compose project was named `vps-dashboard`.
**`sudo ./install.sh` migrates all three for you**: it rewrites `.env` (keeping a backup),
moves the two directories, stops the old stack and rebuilds. That is the recommended path
after a `git pull`.

By hand:

```bash
sudo sed -i -e 's/^VPSD_/JD_/' \
  -e 's|/var/lib/vps-dashboard|/var/lib/just-dashboard|g' \
  -e 's|/var/backups/vps-dashboard|/var/backups/just-dashboard|g' .env
sudo docker compose -p vps-dashboard down
sudo mv /var/lib/vps-dashboard /var/lib/just-dashboard
sudo mv /var/backups/vps-dashboard /var/backups/just-dashboard
sudo docker compose up -d --build
```

`/var/lib/vps-dashboard` is your database: accounts, TOTP enrolments, the audit log. Move it
rather than letting a fresh one be created beside it. Running the binary directly instead of
under compose? Skip all of this. The backend reads a `VPSD_` name when the `JD_` one is
unset, and adopts the old data directory when the new one has no database in it.

</details>

---

The installer quotes bootstrap passwords as literal Compose dotenv values, including dollar signs,
quotes and backslashes. It rejects line breaks in a single value.

## Scripting it

<details>
<summary>API tokens and deploy webhooks</summary>

<br>

**API tokens.** Create one under **Account → API tokens**:

```bash
curl -H "Authorization: Bearer vpsd_…" https://localhost:8443/api/v1/system/metrics
```

A token can narrow its creator's role but never widen it, and is demoted automatically if
that account is. Tokens cannot change a password, mint other tokens or manage accounts. Those
need a real session.

**Deploy webhooks.** Create a project under **Deployments** for a hook URL and a secret shown
once:

```bash
BODY='{"ref":"refs/heads/main"}'
SIG="sha256=$(printf '%s' "$BODY" | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $2}')"

curl -X POST "https://your-dashboard:8443/api/v1/hooks/deploy/$HOOK_ID" \
  -H "Content-Type: application/json" \
  -H "X-Hub-Signature-256: $SIG" \
  -d "$BODY"
```

That is the format GitHub sends by default, so a GitHub webhook needs no glue. It is the only
endpoint without a dashboard session, it authenticates by HMAC over the raw body, and it
still sits behind the network allowlist.

On delivery the project is fetched, hard-reset to its branch, its encrypted environment is
rendered into `.env`, and the stack is rebuilt.

</details>

## How it fits together

<details>
<summary>Architecture and the privilege model</summary>

<br>

```
browser ──(Tailscale / SSH tunnel)──▶ Caddy :8443
                                        ├─ /api/* ─▶ backend :8080  (loopback)
                                        └─ /*     ─▶ frontend :3000 (loopback)
```

Only Caddy binds a routable address; the backend and frontend are unreachable from outside
the machine. Serving both from **one origin** is not cosmetic: the session cookie is
`HttpOnly; SameSite=Strict` and the API rejects cross-origin WebSocket upgrades, so a split
origin would break both by design.

Every request passes the same chain:

```
network allowlist → rate limit → authenticate → capability → handler
```

Destructive routes get a tighter rate budget, and the rare irreversible ones also require a
typed confirmation phrase.

**On privileges.** The compose file grants the backend `privileged: true`, `pid: host` and
the Docker socket. That is what makes "restart this unit" and "kill this process" mean
anything, and it also means the security boundary is the network perimeter plus 2FA, not the
container. Read `docker-compose.yml` before deploying and narrow the mounts if your use case
allows.

**Reaching the host, not the container.** The dashboard manages a server, so everything it
reports has to be about the server rather than the container it runs in. Two mechanisms keep
that true.

*The directories it manages are mounted at their real paths.* `/home`, `/opt`, `/srv`,
`/root`, `/etc` and `/var/log` appear inside the container under the same names, because the
dashboard addresses files by the path you would type over SSH. Remove a mount and the file
manager, git discovery and compose scanning quietly browse the container's own empty
filesystem instead. Narrow them if you like, but narrow `JD_FILE_ROOTS`, `JD_GIT_ROOTS` and
`JD_COMPOSE_ROOTS` to match.

*Host tools run on the host.* nginx's config is readable here but its binary is not, and
shipping a second copy would validate your config against different modules than the server
actually uses. Anything in that category (`nginx`, `caddy`, `ufw`, `fail2ban-client`, `who`)
runs in the host's namespaces via `nsenter`, with an argv and never a shell string. It is
also why the dashboard can tell you fail2ban is *not installed* rather than reporting on a
copy that shipped in its own image.

Where a tool writes files it runs as the account owning that directory.

</details>

## Developing on it

<details>
<summary>Running the two halves locally</summary>

<br>

```bash
# Backend on :8080
cd backend && go run ./cmd/server

# Frontend on :3000
cd frontend && bun install && bun dev
```

The frontend proxies `/api` to the backend in development, so there is no CORS setup.
WebSockets are the exception: Next does not proxy upgrades, so set
`NEXT_PUBLIC_WS_BASE=http://localhost:8080` to point them at the backend directly — and
`JD_ALLOWED_ORIGINS=http://localhost:3000` on the backend, because the socket's origin check
is no longer looking at the same origin it does behind Caddy in production.

`bun dev` and `bun run build` copy the code editor into `public/` first (`predev` /
`prebuild`). Invoking `next` yourself skips that step, and every editor in the dashboard is
then a spinner that never resolves.

Before opening a pull request:

```bash
cd backend  && go build ./... && go vet ./... && go test ./...
cd frontend && bun run lint && bun run build
```

bun is the package manager. Do not add `package-lock.json` or `yarn.lock`.

</details>

## Setting it up by hand

<details>
<summary>Without the installer</summary>

<br>

```bash
cp .env.example .env
```

Edit it. Three settings matter more than the rest:

```bash
# Encrypts TOTP seeds, database strings, deploy env and backup credentials.
# Generate once. Losing it loses every stored secret.
JD_MASTER_KEY=$(openssl rand -hex 32)

# The address the dashboard answers on. Your machine's MagicDNS name is
# recommended; localhost means reachable only through an SSH tunnel. Either way
# loopback is bound too, so a tunnel is always available as a fallback.
JD_SITE=localhost

# How it is trusted: tailscale (a real certificate, no browser warning),
# internal (Caddy's own CA — this is what produces "not secure"), or off
# (plain HTTP, loopback only, for the ssh-tunnel setup).
JD_TLS=internal
```

For `JD_TLS=tailscale`, issue the certificate once before starting the stack — the dashboard
renews it from then on:

```bash
sudo mkdir -p /var/lib/just-dashboard/certs
sudo tailscale cert \
  --cert-file /var/lib/just-dashboard/certs/site.crt \
  --key-file  /var/lib/just-dashboard/certs/site.key \
  "$(tailscale status --json | grep -m1 DNSName | cut -d'"' -f4 | sed 's/\.$//')"
```

Then:

```bash
docker compose up -d --build
docker compose logs backend | grep "bootstrap admin"
```

That last line prints the generated admin password **once**.

</details>

---

## Backing it up

The dashboard's own state is a SQLite database in `JD_DATA_DIR`
(`/var/lib/just-dashboard`). It holds accounts, TOTP enrolments, API tokens, the audit log
and every encrypted secret.

Back up that directory **and** keep `JD_MASTER_KEY` somewhere separate. Either one alone will
not restore.

Backup jobs offer **Consistency and recovery checks**. Select SQLite files for native snapshots while
the application is running. A pinned application image can check a restored copy against its schema
and a known canary record; history shows verification and cleanup results. Deployment policies can
require that exact-artifact evidence before activation. Other database engines still need their own
consistent capture protocol. See [application restore verification](docs/internal/deployments/restore-verification.md).

## Licence

[AGPL-3.0](LICENSE). Run it, change it, distribute it, but if you run a modified version as a
network service, publish your changes. Contributions are welcome under the terms in
[CONTRIBUTING.md](CONTRIBUTING.md).

Dashboard installation, restart, rebuild and self-update automatically move ports occupied by other
services and save the selected ports in `.env`; the startup log prints the dashboard's current address.
Container creation and Compose startup also retry published-port conflicts while preserving bind scope
and internal ports. Actual connection ports appear in runtime details. Compose conflict recovery requires
Compose 2.24.4 or newer. This does not change ACME's public validation ports or ports configured inside
host-network applications.

Setup installs missing host `curl`, `openssl`, and `certbot` through the available native package manager,
checks Certbot's standalone/webroot authenticators, and enables the distribution's Certbot renewal timer
where supplied. Rerunning `sudo ./install.sh` also provisions missing tools on existing installations.
Package failures stop setup with their actual error; setup does not start a competing web server or stop
an existing port owner. DNS-provider plugins and API credentials remain provider-specific configuration.


For public deployments, the dashboard reuses a supported Docker Caddy already serving ports 80 and 443,
including its automatic HTTPS and renewal. It adds separate deployment routes while preserving existing
sites. On a fresh host with both ports free, the first deployment starts a persistent public Caddy
automatically. Applications keep private ports, and managed routes reconnect after proxy recreation.
Existing host nginx uses shared HTTP challenge routing. Other web-server layouts are reported with the
specific blocker; they are never stopped to free a port. See the
[deployment ingress reference](docs/internal/deployments/caddy-ingress.md) for supported layouts.
