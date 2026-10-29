<div align="center">

<a href="https://just-dashboard.com"><img src="frontend/public/brand-logo/just-tile-ink.svg" width="88" height="88" alt="Just Dashboard"></a>

# Just Dashboard

### Your whole Linux server, in one private control panel.

Deploy apps, run Docker, manage databases, shape the network, open a real shell and keep it all
backed up — behind one login that lives on your private network.

**Version 0.8.1** · Go backend · Next.js frontend · one `docker compose` stack

[![Website](https://img.shields.io/badge/website-just--dashboard.com-CAE9FF?style=flat-square&labelColor=16181d)](https://just-dashboard.com)
[![Version](https://img.shields.io/badge/dynamic/json?url=https%3A%2F%2Fraw.githubusercontent.com%2FJustDashboard%2FJust-Dashboard%2Fmain%2Fbackend%2Finternal%2Fselfupdate%2Fchangelog.json&query=%24.latest&label=version&style=flat-square&color=CAE9FF&labelColor=16181d)](CHANGELOG.md)
[![Licence](https://img.shields.io/badge/licence-AGPL--3.0-CAE9FF?style=flat-square&labelColor=16181d)](LICENSE)
[![Go](https://img.shields.io/badge/Go-backend-00ADD8?style=flat-square&logo=go&logoColor=white&labelColor=16181d)](backend)
[![Next.js](https://img.shields.io/badge/Next.js-frontend-ffffff?style=flat-square&logo=nextdotjs&logoColor=white&labelColor=16181d)](frontend)
[![Docker Compose](https://img.shields.io/badge/Docker-Compose-2496ED?style=flat-square&logo=docker&logoColor=white&labelColor=16181d)](docker-compose.yml)

**[just-dashboard.com](https://just-dashboard.com)** · [Install](#install) · [Features](#features) · [Tour](#a-quick-tour) · [Admin scripts](#terminal-admin-scripts) · [Security](#security-first) · [Configuration](#configuration) · [Support](#who-makes-this)

</div>

<br>

![The server overview: the machine drawn as itself, shortcuts to every section, live resources with their last hour, storage and the deployed projects](docs/readme/overview.png)

<p align="center"><sub>Screenshots show example data: the Overview and Excalidraw at 0.8.1, the rest at 0.7.5.</sub></p>

---

## Why Just Dashboard

<table>
<tr>
<td width="25%" valign="top">

**Readings and controls**

See what the server is doing through live readings, recorded events and logs.
Open the owning controls to review and apply a change.

</td>
<td width="25%" valign="top">

**Private by design**

Root-equivalent power belongs behind Tailscale or an SSH tunnel. The allowlist runs before the
login, two-factor is per account, and every change is audited.

</td>
<td width="25%" valign="top">

**One server, done properly**

No fleet, no cloud account, no AI model or API key required. One `docker compose` stack that knows the
machine it runs on, end to end.

</td>
<td width="25%" valign="top">

**Free, every feature**

Self-hosted and AGPL-3.0. Nothing is held back for a paid tier — every feature is free and stays
free.

</td>
</tr>
</table>

## Features

<table>
<tr>
<td width="50%" valign="top">

### 🖥️ Overview & monitoring

- A live overview of the machine: CPU, memory, load, network, storage and disk I/O
- Shortcuts to every section, with the open shells, the drawings edited last, the databases and
  every backup job one press away
- A map of the whole server: the server in the middle and everything it runs branching off it —
  projects, containers, databases, domains, backups, shells, accounts — wired together where the
  server knows they are, arranged however you like and remembered
- Metrics with seven days of history the backend records itself, and CSV export
- Alerts when the CPU or memory stays high, a disk fills, a container goes down or a backup fails
- Notable moments, top processes and the day's activity at a glance
- Processes, PM2 apps, systemd services and scheduled jobs, each with its verbs
- One log viewer for files, containers, stacks, PM2 and the journal, with insights
- Every service shows its own logs on its own page

</td>
<td width="50%" valign="top">

### 🚀 Deployments

- Deploy from a repository, an image, a Compose stack or **117 reviewed templates**, 35 of them
  multi-service stacks — an application with its own database, cache or worker
- Detects Node, Bun, Deno, Python, PHP, Go, Rust, Java, .NET, Ruby, Elixir and more
- Push to deploy, plus pull-request previews only your tailnet can reach
- Immutable releases, one-click rollback and health-gated cutover
- Up to eight replicas of a stateless release behind one route, cut over and rolled back together
- Canary releases: a new release takes a share of the requests beside the live one, read side by side,
  and is removed if it does worse or promoted when it does not
- Skew protection: a visitor stays on the release that first answered them for the warm window after a
  cutover, so a page loaded before it never asks the new release for scripts it does not have
- HTTPS issued during the deploy, wildcard domains too, through the DNS credentials under Certificates
- A staging environment on its own branch, promoted to production as the exact image it ran, with no rebuild
- Migrations rehearsed on a throwaway copy of the database, restored from its backup, before production
- Real recent requests replayed against a new release before traffic moves, so a page it broke stops it
- Variables per environment, plus shared ones defined once for the server or a project
- Variables read from HashiCorp Vault, OpenBao, Infisical, Doppler, AWS Secrets Manager or Azure Key Vault, fresh for each run
- Sign-in in front of an app or only its previews through Authelia, Authentik or oauth2-proxy
- A maintenance page switched on at once, without a deploy, and the app's own error pages
- Build logs, runtime, console, request logs and traffic alerts per project
- A failed run named from its own output, and on request explained by your own model, secrets masked
- Request insights that count real visitors apart from bots, with their devices and pages
- Notifications to Discord, Slack, Microsoft Teams, Mattermost, Telegram, ntfy, Gotify, Pushover,
  e-mail and webhooks
- Commit statuses and preview comments on GitHub, GitLab, Gitea and Forgejo
- Log drains: container output and requests to OpenTelemetry, Grafana Loki or any JSON-lines endpoint
- The `jd` command line and deploy keys for CI: create, configure, deploy, follow, roll back, promote,
  keep or switch back, maintenance, status, set variables

</td>
</tr>
<tr>
<td width="50%" valign="top">

### 🐳 Docker

- Overview of what every container uses and what just happened to it
- Containers, stacks, images, volumes, networks and events, each a page
- Create from a template, a pasted `docker run` or a form
- Docker state, health checks and recorded lifecycle events
- Reviewed container configuration and live resource limits
- Browse a volume's or a container's files in place

</td>
<td width="50%" valign="top">

### 🗄️ Databases

- A control center for every database on the server — even unconnected ones
- PostgreSQL, MySQL, MariaDB, SQLite, SQL Server, ClickHouse, Oracle, MongoDB, Redis
- Table editor with staged, reviewed edits and a SQL editor with plans and stored programs
  (DELIMITER, GO and PL/SQL scripts)
- An optional SQL assistant on your own Anthropic key or local model, told the schema and never the rows
- Schema browser, diagram, code generation, mock seed data
  written parents first, performance
- Key browser and console for Redis, documents and pipelines for MongoDB
- Protected connections, dump backups, PostgreSQL major-version upgrades with a way back, accounts,
  grants and a live map
- Import from another server, kept current by logical replication on PostgreSQL until you cut over
- Point-in-time recovery for PostgreSQL: the write-ahead log archived with WAL-G, recovered to any second
- Alerts when a database goes down, fills its disk, runs low on connections, lags or misses a backup
- An MCP server for AI clients such as Claude Code, Cursor and VS Code: keys that reach only the
  databases you choose, read-only unless you allow changes, never a protected one, and the
  deployments only if you allow it, every call audited

</td>
</tr>
<tr>
<td width="50%" valign="top">

### 🧰 Workspace

- A real terminal whose sessions survive the tab closing and dashboard restarts
- Split panes, drag-and-drop docking and one-click Codex and Claude launchers
- A file manager with search, previews, Monaco and image editors
- A full Git workspace: staging by line, history, branches, conflicts, PR reviews
- GitHub, GitLab and Gitea pull requests and Actions logs
- Excalidraw: drawings you edit together live, with live cards for the host, projects and databases, a shared shape library from libraries.excalidraw.com, diagrams written from a description, and each drawing listed as a thumbnail of itself
- Remote Desktop: your Windows PCs, Macs and Linux desktops in a browser tab over Tailscale, unattended, with clipboard and file transfer, and a Stream mode that plays the screen as 60 fps video from Sunshine

</td>
<td width="50%" valign="top">

### 🌐 Network

- A live topology of devices, tunnels and Docker networks
- Interfaces, bridges, VLANs, namespaces, routing and policy rules
- Firewall, port forwarding, NAT, rate limits and blocklists
- WireGuard and Tailscale, DNS and ad-blocking, egress groups with failover
- Traffic per device, program and container, plus speed limits and SQM
- 26 diagnostics, packet captures, connection paths, drift and saved runs

</td>
</tr>
<tr>
<td width="50%" valign="top">

### 🔒 Proxy & TLS

- Every domain drawn through nginx or Caddy to the application it reaches
- Sites written as ordinary config, with a builder for auth and access rules
- Certificates through certbot, including DNS wildcards, with runway at a glance
- A live TLS report, streams, ports and per-site traffic
- Alerts on renewals and certificate drift

</td>
<td width="50%" valign="top">

### 🛡️ Security & backups

- A verdict on the host: exposure, SSH, logins and who is attacking
- fail2ban, CrowdSec and Suricata where they run
- Scheduled backups to disk, S3, Backblaze B2, an SFTP server or Google Drive, OneDrive, Dropbox, WebDAV
  and the other services rclone speaks, encrypted on the way out, and native database dumps
- A coverage map of everything that is *not* backed up yet
- Single-file and in-place restore

</td>
</tr>
<tr>
<td width="50%" valign="top">

### ⚙️ Server & accounts

- Host package updates on apt, dnf, yum, zypper, pacman or apk
- System users, an audit log of every change and the panel's own settings
- Address, certificate, allowlist and ports applied with automatic rollback
- Roles, two-factor, passkeys, sessions and API keys for every account
- Search and replace API keys, with explicit revocation and inactive-record cleanup
- A first-run question that keeps the sections you don't use out of your sidebar
- One-click self-update, with the release notes compiled in
- Optionally linked to your just-dashboard.com account, at a scope you choose, to see this server from the site

</td>
<td width="50%" valign="top">

### ⌨️ Everywhere

- **Ctrl/⌘K** searches projects, sites, containers, databases, repos and pages
- Previews each result with what it is connected to
- Keyboard shortcuts on every page — press **?** to see them
- Every page remembers where you left it
- A consistent, dark design built around readings rather than tiles

</td>
</tr>
</table>

## A quick tour

<table>
<tr>
<td width="50%" valign="top">
<img src="docs/readme/deployments.png" alt="Deployment projects as cards with their state, source, traffic and recent runs">
<p align="center"><b>Deployments</b> — every project, its traffic and its runs</p>
</td>
<td width="50%" valign="top">
<img src="docs/readme/deployment.png" alt="A project's overview with a live website preview, its source, release, runtime and domains">
<p align="center"><b>A project</b> — live preview, release, runtime and domains</p>
</td>
</tr>
<tr>
<td width="50%" valign="top">
<img src="docs/readme/docker.png" alt="The Docker overview: processor and memory by container, recent events and every container's live readings">
<p align="center"><b>Docker</b> — who uses what, and what just happened</p>
</td>
<td width="50%" valign="top">
<img src="docs/readme/databases.png" alt="The database control center with every database as a card">
<p align="center"><b>Databases</b> — every engine on the server, each as itself</p>
</td>
</tr>
<tr>
<td width="50%" valign="top">
<img src="docs/readme/terminal.png" alt="Two terminal panes side by side running tests and htop">
<p align="center"><b>Terminal</b> — persistent sessions and split panes</p>
</td>
<td width="50%" valign="top">
<img src="docs/readme/git.png" alt="The Git workspace with staged changes and a diff">
<p align="center"><b>Git</b> — stage, review and ship from the browser</p>
</td>
</tr>
<tr>
<td width="50%" valign="top">
<img src="docs/readme/network.png" alt="The network topology from the uplink and tunnels through the server to its Docker networks">
<p align="center"><b>Network</b> — the server as a router, drawn live</p>
</td>
<td width="50%" valign="top">
<img src="docs/readme/proxy.png" alt="The proxy overview drawing domains through nginx to their applications">
<p align="center"><b>Proxy & TLS</b> — domains, applications and certificates</p>
</td>
</tr>
<tr>
<td width="50%" valign="top">
<img src="docs/readme/files.png" alt="The file manager with a Compose file previewed beside the listing">
<p align="center"><b>Files</b> — browse, preview and edit anywhere you allow</p>
</td>
<td width="50%" valign="top">
<img src="docs/readme/backups.png" alt="Backups drawn as what is covered flowing to each destination">
<p align="center"><b>Backups</b> — what is covered, and what is not</p>
</td>
</tr>
<tr>
<td width="50%" valign="top">
<img src="docs/readme/metrics.png" alt="Metrics with live readings, notable moments and top processes">
<p align="center"><b>Metrics</b> — a week of history, recorded by the server</p>
</td>
<td width="50%" valign="top">
<img src="docs/readme/command-palette.png" alt="The command palette searching for shop across projects, repositories, databases and containers">
<p align="center"><b>Ctrl/⌘K</b> — everything one keystroke away</p>
</td>
</tr>
</table>

### New in 0.8

<img src="docs/readme/excalidraw.png" alt="An Excalidraw drawing of a release path from the internet through Caddy to a Next.js app, its PostgreSQL database and a nightly backup">
<p align="center"><b>Excalidraw</b> — drawings kept on the server and edited together live</p>

- **Drawings edited together:** Excalidraw is the Workspace's one board. Everyone with a drawing open
  edits it live; its images and the shape library are kept on the server; and a drawing can hold live
  cards of your hosts, projects and databases, YouTube, Vimeo and Figma embeds, and diagrams written
  from a description.
- **An Overview that opens onto everything:** shortcuts to every section, the open shells beside the
  drawings edited last, and the databases beside every backup job.
- **Projects drawn by their own icon** wherever the dashboard names them, a web terminal that finds
  what ssh finds, and saved database connections that repair themselves after a reinstall lost the
  master key.
- **Gone:** Stickyboard (its boards stay in the database) and the schema comparison page.

0.8.1 made a streamed Mac connect once, unstretched, with the pointer where it is drawn. Every change
is in [CHANGELOG.md](CHANGELOG.md).

### Added in 0.7.5

<table>
<tr>
<td width="50%" valign="top">
<img src="docs/readme/remote-desktop.png" alt="The Remote Desktop overview drawing this browser, the server and four computers, one of them in a session">
<p align="center"><b>Remote Desktop</b> — your own PCs and Macs in a tab, over Tailscale</p>
</td>
<td width="50%" valign="top">
<img src="docs/readme/database-assistant.png" alt="The SQL editor with the assistant beside it, answering two questions with statements to insert or open">
<p align="center"><b>SQL assistant</b> — ask for a statement, an error or a plan explained</p>
</td>
</tr>
<tr>
<td width="50%" valign="top">
<img src="docs/readme/database-mcp.png" alt="A new MCP key with the command that connects Claude Code to the dashboard's databases">
<p align="center"><b>MCP server</b> — the databases you choose, for your AI clients</p>
</td>
<td width="50%" valign="top">
<img src="docs/readme/database-explain.png" alt="A measured PostgreSQL plan with a hash join and a sequential scan flagged as misjudged by four thousand times">
<p align="center"><b>Measured plans</b> — where the time went, and what the planner misjudged</p>
</td>
</tr>
<tr>
<td width="50%" valign="top">
<img src="docs/readme/database-rls.png" alt="A PostgreSQL table's row-level security policies, tried as a role that sees two rows">
<p align="center"><b>Row-level security</b> — write the policies, then try them as a role</p>
</td>
<td width="50%" valign="top">
<img src="docs/readme/database-seed.png" alt="The Generate page filling tables with believable mock rows, parents first">
<p align="center"><b>Seed data</b> — believable rows, written parents first</p>
</td>
</tr>
<tr>
<td width="50%" valign="top">
<img src="docs/readme/database-pitr.png" alt="Point-in-time recovery for PostgreSQL: the recoverable window, base backups, a restore point and a recovered copy">
<p align="center"><b>Point-in-time recovery</b> — any second, not only the last dump</p>
</td>
</tr>
</table>

## Install

```bash
git clone https://github.com/JustDashboard/Just-Dashboard.git
cd Just-Dashboard && sudo ./install.sh
```

The installer asks one question that matters — how you will reach the dashboard — then does the
rest in four labelled stages:

- **Tailscale (default):** the machine stays invisible to the internet and the dashboard answers at
  `https://your-box.tailnet-name.ts.net:8443` with a real certificate.
- **SSH tunnel (fallback):** the dashboard is served on loopback only.
- It generates the master key and a first password, builds the stack and prints how to get in.
- It installs the host tools the terminal and pages rely on — `gh`, `git-lfs`, `whois`,
  `traceroute` — where they are missing.

Use `./install.sh --help` to preview the steps, or `sudo NO_COLOR=1 ./install.sh` for plain output.
Everything it asked is editable afterwards under **Settings → Configuration**.

One of its questions is optional and defaults to **No**: whether to [link this server to your
just-dashboard.com account](#linking-to-just-dashboardcom). For unattended runs, `--link` and
`--no-link` answer it and `--link-scope observe|operate|maintain` names what the site may do
(default `observe`). The install finishes whether or not the link is approved, and
`JD_ACCOUNT_LINK=false` removes the question altogether.

**First sign-in:** every new account is asked once what it will use the server for — shipping apps,
running databases, containers, the network, security, or working on the box — and its sidebar,
Overview and search's empty list keep only those sections. Hiding a section is about the sidebar,
not access: links from other pages still open it, search still finds it, and the role decides what
the account may do. **Account → Workspace** changes the answer or asks the question again. Accounts
that existed before 0.7.3 are not asked and keep every section.

**Upgrading:** use **Update now** in the dashboard, run `sudo ./install.sh` again, or `git pull`
followed by `docker compose up -d --build`. All three keep your `.env`, database, accounts and
sessions.

**API keys:** under **Account → API keys**, search by name, prefix, role or owner and filter keys
that have never been used, have been unused for 30 days, expire soon or have no expiry. **Replace key**
creates a new credential for your account; copy it into your tool and check it works before choosing
**Revoke previous key**. Revocation immediately stops access and keeps the record in the folded
**Revoked or expired** history. An administrator can use **Delete record** there to remove inactive
metadata; the audit trail is kept. A key's secret is shown only once.

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

<details>
<summary>How passwords and resets behave</summary>

<br>

Passwords are entered twice with hidden input and must meet the dashboard's strength rules. The
password a script sets is the one the account signs in with — the dashboard does not ask for another
at sign-in. An account an admin creates from Account → Users, and a bootstrap password the backend
generated, are the ones replaced on first sign-in, after any required two-factor step. A reset
clears failed-login lockout and revokes that account's sessions, API tokens and passkeys, while keeping
two-factor enrolment, recovery codes, role and disabled state. Account changes are audited as local
root; `revoke-sessions` leaves API tokens unchanged.

For automation, append `--password-stdin` to either password command and pipe one password line from
a trusted source — never put passwords in arguments or shell history. The scripts find the checkout
from their own path and let Compose read `.env` without executing it. Run `restart` from SSH, since it
disconnects the web terminal; it recreates containers with current settings, which
`docker compose restart` alone does not.

</details>

## The jd command line

`jd` drives your deployments from a terminal or a CI job, over the dashboard's own API. The installer
puts it at `/usr/local/bin/jd` on the server, and the dashboard keeps that copy current after every
update (`sudo ./scripts/manage.sh install-cli` installs it again by hand). For another Linux machine
of the same architecture, **Account → API keys** links the same file (`GET /api/v1/cli/jd`, for any
signed-in account); anywhere else, build it from a checkout with Go 1.26: `cd backend && go build ./cmd/jd`
— it uses only Go's standard library.

It signs in with a **deploy key**: under **Account → API keys → New key**, an administrator chooses
*Deploy key*, a name, an expiry and the deployments it reaches (every one, or only those you pick).
The key is shown once.

```bash
jd login https://dash.example.com --key - < key   # the key read from stdin; stored 0600 in ~/.config/jd
jd deployments                                    # every deployment with its health and last run
jd deploy shop --wait                             # exit status is the run's result
jd logs shop --follow                             # or --run 12 for an earlier run
jd runs shop / jd releases shop
jd rollback shop                                  # to the release before the live one, or --to <id>
jd rollback shop --instant                        # straight back to the release kept warm after the last deploy
jd keep shop                                      # keep the new release now; the warm one is retired
jd canary status shop                             # a canary's window and both releases' requests, 5xx and p95
jd canary promote shop / jd canary abort shop     # decide a canary now; abort leaves the live release untouched
jd promote shop --wait                            # staging's live release into production, as the same image
jd maintenance shop on --bypass 10.0.0.0/8        # the switch acts at once; the bypass list with the next deploy
jd status shop                                    # environments, live release, rollback watch, maintenance, rehearsal
jd vars ls shop                                   # names, scopes and digests: never values
jd vars set shop STRIPE_KEY=- < stripe.key        # KEY=- reads the value from stdin, out of shell history
jd vars import shop .env --dry-run                # then without --dry-run
jd vars rm shop OLD_FLAG
jd config get shop --json > shop.json             # edit, then: jd config set shop shop.json
jd create --name blog --image nginx:1.27-alpine --port 80   # a new deployment, through a draft
```

Every read takes `--json`. In CI, set `JD_URL` and `JD_KEY` instead of logging in; nothing is written.
`--env` picks an environment other than production, `--ca-file` trusts the internal Caddy certificate
authority, and plain `http://` is refused except to loopback (an SSH tunnel). Exit codes: 0 done (the
run succeeded), 1 refused or the run failed, 2 usage, 3 not logged in or the key is no longer valid.

**What a deploy key can do.** Everything an ordinary key could on the deployments — list, read, deploy,
cancel, retry, roll back, promote staging, switch back to the warm release or keep the new one, promote
or abort a canary, switch maintenance, runs and logs — plus what used to need a browser session: create deployments from drafts
and duplicates (a key's drafts are its own, and a key narrowed to some deployments reaches the
deployments it commits), change configuration, source, Git policy and triggers, and set, import,
generate, rotate and delete variables and shared variables. Variables are write-only to it: it sees
names and digests, never a value. **What it cannot do, whatever it reaches:** reveal a value; list,
add, change or test saved registry and Git credentials; see or change secret-store connections; set the
rollback-watch or rehearsal terms or edit a route's maintenance and error pages; merge or comment on
pull requests as the dashboard's GitHub account; approve previews; archive or delete deployments; or
reach anything outside Deployments. It also cannot give a deployment access to the server itself — a
privileged container, the host's network, a host folder, a release task in the host's shell, a Compose
file — nor point a saved credential somewhere new; a deployment that already has such settings, or
reads a saved credential through a variable, can be deployed and its variables set, but its
configuration and source are changed in the dashboard. Every
write is in the audit log under the key's name. See [the security notes](docs/internal/security/invariants.md)
for the full boundary; a key that can deploy code can still read that deployment's own variables from
inside it, as in any CI system.

## Linking to just-dashboard.com

Optional, and off until you do it. A linked server shows up on your [just-dashboard.com](https://just-dashboard.com)
account with its health, version and the way to reach it, so you can find your dashboards in one
place. Link it from **Settings → Connect**, from the question in `install.sh`, or over SSH
with `sudo ./scripts/manage.sh link`; all three run the same code. The server asks the site for a short
code, you enter it on the site while signed in, and the server learns it was approved. Nothing secret is
typed or printed: the code only points at the request, and completing the link needs the key this
server keeps sealed in its own store.

You choose the **scope** when you link, and can change it any time in Settings:

| Scope | The site sees | The site can also |
| --- | --- | --- |
| `observe` (default) | Version and update state, OS, kernel, architecture, uptime, the address you reach the dashboard at (tailnet name or SSH tunnel port), CPU, memory, disk, load and network readings, container and backup counts, alerts firing | Ask for a fresh reading |
| `operate` | All of that, plus the names of containers, stacks, backup jobs and the last-deployed project | Start, stop or restart a container, restart a compose stack, run a backup job |
| `maintain` | Same as `operate` | Also install a dashboard update, set an automatic-update policy (evaluated here, in your server's time zone), restart the dashboard |

Never sent, at any scope: `.env`, the allowlist, accounts, API keys, file paths beyond mount points,
logs, connection strings, deployment variables or backup credentials. The Settings page shows the exact
body of the last report.

Everything is outbound over HTTPS: the dashboard is not made reachable and the site never connects to it.
A command from the site runs only if its signature verifies against the site key pinned when you linked,
it has not expired or been seen before, and it fits the scope you chose here; every one, run or refused,
is written to the audit log as the site account. Unlink from Settings, with
`sudo ./scripts/manage.sh unlink`, or by removing the server on the site. `JD_ACCOUNT_LINK=false` switches
the feature off entirely: no page controls, no installer question, no command, and a link that exists
goes quiet. `JD_ACCOUNT_URL` names the site, an origin only (`http://` just for `localhost`).

## Security first

**This dashboard is root-equivalent.** Anyone who reaches it with a valid session has root on the
machine, so it is built to sit behind a VPN or an SSH tunnel — and that is enforced:

- The backend refuses to start on a non-loopback address without a `JD_ALLOWED_CIDRS` allowlist,
  and the allowlist is checked **before** authentication.
- Two-factor is enforced for every account that has enrolled; `JD_REQUIRE_2FA` decides whether an
  account *must* enrol. A passkey replaces the password at sign-in, never the code.
- Capabilities are checked on every backend route, never in the UI alone.
- Destructive actions are confirmed and audited; deleting a project, database, stack or Git checkout,
  and uninstalling with a wipe, asks for a typed phrase checked on the server.
- Every state-changing request lands in the audit log.
- Linking to a just-dashboard.com account is optional and outbound only. A command from the site runs
  only if its signature verifies against the key pinned at link time and its kind is within the scope
  you chose; linking is a browser-session action for an administrator, or root at the terminal.

### Roles

| | `readonly` | `limited` | `admin` |
| --- | :---: | :---: | :---: |
| View everything | ✅ | ✅ | ✅ |
| Start / stop / restart, Git, edit files | | ✅ | ✅ |
| Terminal, delete, prune, restore, updates, accounts, firewall | | | ✅ |

`readonly` can read any file inside `JD_FILE_ROOTS`, so give it to someone you would let read the
disk.

## Version, and updating

This is **0.8.1**. It is not 1.0 because the API is still moving. Every release is in
[CHANGELOG.md](CHANGELOG.md) and in the dashboard itself, where **Update now** pulls, rebuilds and
restarts from a container that outlives the restart. The update check is one unauthenticated GET of
one file from GitHub; `JD_UPDATE_CHECK=false` turns it off.

## Configuration

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
| `JD_TLS` | `internal` | How the connection is trusted. `tailscale` serves the certificate `tailscale cert` issues for `JD_SITE` — publicly trusted, no browser warning, renewed automatically. `internal` is Caddy's own CA, which is what produces "not secure". `off` is plain HTTP and is refused on anything but loopback; the API also accepts same-origin HTTP WebSockets for live features in this mode. |
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
| `JD_TERMINAL_SHELL` | account's shell | The shell the web terminal opens. Empty honours `chsh`. `install.sh` sets it to the host's zsh, which the bundled startup gives inline history suggestions and command colouring; ssh is unaffected. The account's own login shell still reads its profile first, so a window finds the commands ssh finds. |
| `JD_TERMINAL_USER` | lowest regular account | Host account a terminal session logs in as. |
| `JD_REMOTE_DESKTOP_BRIDGE` | none | A Moonlight Web stream bridge on another tailnet machine, as `host:port`, used in place of the container this server would run. Remote Desktop's picture always passes through the bridge; one near you and your computers spares it the trip to this server and back. The bridge must trust the dashboard's account header from this server only. |
| `JD_SESSION_TTL` | `12h` | Absolute session lifetime. |
| `JD_SESSION_IDLE_TTL` | `60m` | Idle timeout. |
| `JD_DOCKER_HOST` | `unix:///var/run/docker.sock` | Docker Engine endpoint. |
| `JD_DEPLOY_HEAVY_SLOTS` | `1` | Concurrent deployment build/activation workers. Valid range: 1–8. |
| `JD_DEPLOY_LIGHT_SLOTS` | `2` | Concurrent validation/metadata deployment workers. Valid range: 1–8. |
| `JD_DEPLOY_LEASE_TTL` | `30s` | Time before a silent deployment worker claim is reconciled after a crash. Valid range: 5s–5m. |
| `JD_METRICS_INTERVAL` | `15s` | How often the backend samples the host, and every running container, into its own history. Clamped to 5s and 5m. |
| `JD_METRICS_RETENTION` | `7d` | How long that history is kept. Accepts days. `0` records nothing and leaves only the live feed. A deployment's container events (exits, OOM kills, restarts, health changes) are kept for 30 days or this, whichever is longer, whatever this is set to. |
| `JD_UPDATE_CHECK` | `true` | Whether the dashboard may ask GitHub whether a newer version of *itself* exists — one unauthenticated GET of one file, four times a day, carrying nothing but a version number. `false` turns it off; the release notes for the version you run stay readable, since they are compiled in. |
| `JD_ACME_DIRECTORY` | Let's Encrypt | Another ACME directory to order deployment certificates from: Let's Encrypt's staging endpoint for a rehearsal that spends no rate limit, or a private authority on a network that never sees the internet. Both the managed Caddy and certbot follow it — the Certificates page's orders and a deployment run's, over HTTP-01 and DNS-01 alike — and certbot registers an account with that directory rather than reusing one it has with another. |
| `JD_ACME_CA_ROOT` | system roots | A PEM bundle to trust when that authority signs with its own roots — its issuing roots, and the root behind its directory's TLS listener if that is private too. Caddy is given it as its trusted roots and certbot as `REQUESTS_CA_BUNDLE`, so keep it where both read it (under `/etc`, `/srv`, `/opt` or the data directory, which the stack mounts at the same paths). With it set, the public-DNS check before an order is skipped, because a private authority validates however it was set up to. |
| `JD_UPDATE_REPO` | `JustDashboard/Just-Dashboard` | The repository releases are read from. Change it to follow a fork. |
| `JD_UPDATE_BRANCH` | `main` | The branch whose changelog decides what "newest" means, and which an in-app update fast-forwards to. |
| `JD_ACCOUNT_LINK` | `true` | Whether this server may be linked to a just-dashboard.com account. Linking is always something an administrator does; `false` removes the Settings page's controls, the installer's question and the terminal command, and silences a link that already exists, so nothing is ever sent. |
| `JD_ACCOUNT_URL` | `https://just-dashboard.com` | The site this install may link to: an origin only, with no path, query or credentials. `https://` everywhere; `http://` only for `localhost`, `127.0.0.1` or `[::1]`, to develop the site beside the dashboard. Anything else refuses to start. |
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
| `JD_INGRESS_CONTAINER` | — | Name of the Caddy container deployment domains are served through, when it is not one publishing 80 and 443 on every address (discovered otherwise). |
| `JD_BACKUP_DIR` | `/var/backups/just-dashboard` | Local backup destination and staging. |
| `JD_DB_UPLOAD_MAX_MB` | `2048` | Largest file one database import or uploaded dump may be, in MiB. |
| `JD_DATA_DIR` | `/var/lib/just-dashboard` | The dashboard's own database. **Back this up.** |

**First run only**

| Variable | Default | Description |
| --- | --- | --- |
| `JD_BOOTSTRAP_USER` | `admin` | Account created on an empty database. |
| `JD_BOOTSTRAP_PASSWORD` | none | Leave empty for a generated one, logged once and replaced at first sign-in. A password set here is kept as is. |

Durations take a unit (`12h`, `60m`, and the metrics settings also accept `7d`); booleans
take `true` or `false`. A value that cannot be parsed stops the dashboard at startup rather
than falling back to the default, so a typo is visible instead of silently in effect.

</details>

<details>
<summary>Running it locally, setting it up by hand, and contributor scripts</summary>

<br>

```bash
cd backend && go run ./cmd/server      # API on :8080
cd frontend && bun install && bun dev  # UI on :3000
```

Set `NEXT_PUBLIC_WS_BASE=http://localhost:8080` for WebSockets and `JD_ALLOWED_ORIGINS=http://localhost:3000`
on the backend.

Without the installer: `cp .env.example .env`, set `JD_MASTER_KEY` (`openssl rand -hex 32`),
`JD_SITE` and `JD_TLS`, then `docker compose up -d --build`. The generated admin password is
printed once in `docker compose logs backend`.

| Script | What it is for |
| --- | --- |
| `scripts/test-changed.sh` | Runs exactly the checks, Go tests and browser specs your diff can reach — run it before a pull request |
| `scripts/go-test-race.sh` | Runs Go packages under the race detector, split across processes |
| `scripts/release.sh <version>` | Cuts a release: bumps the version everywhere and regenerates the changelog from `backend/internal/selfupdate/changelog.json` |

See [CONTRIBUTING.md](CONTRIBUTING.md) before opening a pull request.

</details>

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
```

## Who makes this

Just Dashboard is built by one person, [Wayy01](https://github.com/Wayy01), under the
[JustDashboard](https://github.com/JustDashboard) organisation. Find out more at
**[just-dashboard.com](https://just-dashboard.com)**.

The dashboard is free and every feature stays free. If it has saved you an evening, there is a
[Buy Me a Coffee page](https://buymeacoffee.com/ionmoisei72) — entirely optional.

**Sponsors.** Companies and individuals who would like to sponsor the project can write to
[ionmoisei755@gmail.com](mailto:ionmoisei755@gmail.com) and will be listed here with a logo and a
link. *No sponsors yet — the space is open.*

## Licence

[AGPL-3.0](LICENSE). Run it, change it, distribute it, but if you run a modified version as a network
service, publish your changes. Contributions are welcome under the terms in
[CONTRIBUTING.md](CONTRIBUTING.md). The product logos bundled in `frontend/public/logos/` are their
owners' trademarks and keep their own licences, listed in that directory's `NOTICE`.

<div align="center">
<br>
<a href="https://just-dashboard.com"><img src="frontend/public/brand-logo/just-tile-ink.svg" width="40" height="40" alt="Just Dashboard"></a>
<br>
<sub><a href="https://just-dashboard.com">just-dashboard.com</a></sub>
</div>
