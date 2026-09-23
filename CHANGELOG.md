# Changelog

Every release of Just Dashboard, newest first.

**This file is generated.** The source is [`backend/internal/selfupdate/changelog.json`](backend/internal/selfupdate/changelog.json), which is the same file the dashboard reads — both the copy compiled into your build and the one it fetches to find out whether a newer version exists. Edit that, then run `scripts/release.sh <version>`.

## 0.7.0 — 22 September 2026

**Git is a whole workbench, every template says how you get in, and a deployment's Logs page reads its traffic**

The Git workspace could commit, branch, merge and push, and everything past that was a terminal. 0.7.0 brings the rest into the same three columns: conflicts resolved side by side, single lines staged, local history rebased behind a recovery branch, worktrees, submodules, LFS and patches, reviews on GitHub pull requests and on GitLab and Gitea requests. Every reviewed template now declares how its first sign-in works, the five that could not say are no longer offered, and nine new ones join the catalogue. New project opens on whatever is still unanswered and checks the plan the moment Review is reached. A deployment's Logs page answers from the requests the proxy actually served, with insights, marks on the chart and traffic alerts. A container, a compose stack and a backup job are pages of their own, every page remembers what you were doing on it, and the rail's top level is twelve rows instead of seventeen.

### Added

- Resolve a conflict side by side, and stage single lines
  - Merge, cherry-pick and revert started from the workspace keep their conflicts instead of aborting, and each conflicted file opens on its base, current, incoming and working result: edit the result, or take one side whole, then continue or abort. Taking a whole side and aborting discard work, so both ask for a typed phrase. The changes panel stages or unstages chosen hunks or individual changed lines, rebuilt on the server from its own snapshot of the diff and refused if the file moved underneath it. Binary files, symlinks and very large diffs keep the whole-file action.
- Rewrite local history with a recovery branch, and get a lost commit back
  - An interactive rebase over up to a hundred local commits reorders, rewords, squashes and drops them from a plan, writes a jd-before-rebase branch before touching anything, survives a server restart mid-conflict, and refuses commits already on a fetched remote. The reflog pages back through HEAD and rescues any commit into a new branch without switching to it. Blame jumps from a line to its commit, and a commit says whether its signature is verified, bad, expired, revoked or simply unchecked.
- Worktrees, submodules, Git LFS and patches open in the same workspace
  - Worktrees are listed, created on an existing or a new branch, opened in Git or the Terminal, and removed only when clean, unlocked, and neither the main checkout nor the one you are in. Submodules can be added, initialised, synced, deinitialised and removed; LFS lists its tracked patterns and files, tracks and untracks, and fetches the working objects without ever rewriting history; a patch can be exported, checked and applied. The backend image now ships git-lfs.
- Compare branches file by file, search the graph, and set upstream and remote URLs
  - A comparison lists its changed files and opens each diff against the exact base and head it was computed for. The graph searches, filters by ref and pages into older history; History filters by author. Remote URLs can be edited and a branch's upstream set or unset, a commit's message can be amended with nothing staged, and a clone can pick a branch or tag, a shallow depth and sparse directories.
- Review GitHub pull requests and read Actions job logs without leaving the dashboard
  - A pull request's changed files and its conversation page in beside the workspace. A comment, approval or request for changes is sent against the head you were reading, so a push in between cannot be approved by accident. A workflow run opens on its jobs and steps, and a step's log, or only its failure output, is read in the preview with a link back to GitHub when the log has expired.
- GitLab and Gitea merge requests, with a token kept per checkout
  - A checkout on GitLab or Gitea takes the provider, its address, the project and an access token. The token is sealed, belongs to that checkout and its Linux owner, and never becomes a git argument. Requests can then be listed, read with their files and conversation, opened, reviewed and merged the way GitHub's are.
- The installer sets up the host tools the terminal and the dashboard's pages rely on
  - The web terminal is a shell on the server itself, not inside the dashboard, so its git uses the server's packages. install.sh now installs whichever of gh (from GitHub's own signed repository on Debian and Ubuntu), git-lfs, whois and traceroute are missing, one at a time, and names any it could not get instead of stopping. That is what lets a GitHub sign-in on the Git page push from the terminal and over ssh, LFS repositories check out there, and Security → Tools look up and trace addresses. Re-running sudo ./install.sh on an existing install adds them.
- Every template says how the first sign-in works, before and after it is deployed
  - Each card carries a word for it — you create the first account, a password generated here, a token, no sign-in page, or no sign-in at all — and the chosen template the full sentence. Once it runs, the project overview's First sign-in card says what to do and reveals the generated username and password through the audited reveal.
- Nine more templates: Open WebUI, ntfy, Beszel, Opengist, Qdrant, NocoDB, DocuSeal, Seerr and SearXNG
  - Each is one image with its own store, each was started live before it shipped, and each was admitted only with a first sign-in the catalogue can state. Open WebUI is the interface Ollama shipped without.
- Decide auto-deploy, extra hostnames and an image's project type when the project is created
  - The configure screen draws the plan as source, build, runtime and address, each step opening the fields that decide it. Whether pushes deploy themselves, a second and third hostname, and whether an image is an HTTP application with a health-gated cutover are settled before the first release rather than found as settings afterwards. A name already taken is flagged while it is typed, a template's domain arrives filled in with the suggested hostname, and an abandoned or failed setup is deleted rather than left as unfinished work.
- A deployment's Logs page answers from the requests the proxy served
  - Container output for a modern framework is a banner and then silence, so the page now reads the access record the proxy keeps for the route: requests per minute, page views, the failing share, the slow tenth, bytes served and container events, over a chart that marks a release going live, an exit or a restart. Insights breaks the window down by page, client, agent, referrer and status, names scanners and offers to block them, and a failing request jumps to the container's output around that moment. The window exports as CSV, and routes written before recording existed are switched on in place by the lifecycle pass.
- Traffic alerts, and the fleet's pulse on every project card
  - A rule watches one environment for its failing share, its p95, or silence from a route that used to be busy, and announces only when it starts firing and when it recovers, through the existing notification channels. Rules read as sentences on Automation settings and can send a test. Project cards carry the last hour as a sparkline, and a run's Metrics view compares requests, failures and p95 before and after activation.
- Browse a volume's files where the volume is named
  - A volume's panel and a container's Storage tab embed the file browser, clamped to the mount, with Open in Files carrying the directory reached. Storage that looks like a running database's own files carries a warning above it. The backend now mounts /var/lib/docker/volumes, without which every volume answered not found.
- Every page remembers what you were doing when you come back to it
  - Filters, chips, the page of results, the open row, the SQL in the editor, half-filled dialogs and a new project's source and settings survive navigating away for the life of the tab. Secrets are kept in memory only and never written to browser storage.

### Changed

- The Git page is a list of repository cards, worst first
  - The four tiles are gone; every figure they carried was already on a filter chip, which also narrows the list. Each card carries the branch, where it lives, the last commit with its author's colour, and how far it stands from its upstream, ordered under Needs attention by what is most wrong. Branches, commits and pull requests are drawn with real git glyphs, and a diff numbers its lines in two gutters that stay put while a long line scrolls.
- New project opens on the question still open, and checks the plan when Review is reached
  - An unset port opens Runtime, a required variable with no value opens Variables with its references unfolded, and a name another project holds opens Project. A port the source did not name stays unset instead of becoming 3000, and what detection already found — a Dockerfile's single EXPOSE, an image's exposed port, a Go module, a Deno server, a static site's root — is no longer asked again. Review saves and runs preflight on arrival rather than inside the Deploy press, re-checks when the plan changes, and reads back kept mounts and their backup coverage, generated variables, the readiness target, the cutover's cost and every passed check.
- New project says which GitHub identity reaches each repository
  - The Git tab opens on the GitHub App and the CLI, each with its status, what it grants and the repair for whichever is missing, and repositories are grouped under the identity that clones them, newest push first. All six sources start on the page's own left edge instead of collapsing into a narrow column, and the whole flow is drawn in a register for deciding rather than reading: a question, a step spine, and one focused surface.
- Container events say who caused them, and can be searched, followed and linked
  - The deployment's Events view matches the audit log, container first and then the project, so a release can be seen causing a recreate. An exit, an OOM kill or a health flip is never blamed on a release. The feed covers the deployment's networks, follows new events live, links a release to its run, and keeps its view in the address.
- A release is a timeline, and the project overview draws the path a request takes
  - The release path is one bar whose segments are as long as each stage took, and a release that goes live while you watch it build gets a burst of confetti. Details rows open in place to what the engine recorded about each step. The overview shows the site as one linked tile beside a drawing of source, live release, containers and domains, and each project carries its website's own icon, fetched through the dashboard and bound to the recorded endpoint. The GitHub App and Notifications pages are drawn as what they connect.
- A deployment's Databases page says what the deployment would otherwise refuse too late
  - It names a linked database the declared backup job does not cover, with one press to add the dump; names the variables that carry each link and offers them with its removal; says why a binding could not be repaired; and gives links, jobs and volumes their engine, schedule, last success and mount.
- A container, a compose stack and a backup job are pages of their own
  - Each held a log stream, a shell, an editor or a restore inside a sheet over its list. They are /docker/containers/[id], /docker/stacks/[name] and /backups/[job] now, with a breadcrumb back and their tab in the address; the old ?container=, ?stack= and ?job= links redirect.
- The account pages draw who is signed in, and with what
  - Profile opens on one identity line — your picture, your role, the browser and network this session came in through drawn as themselves, and the second factor's verdict — and each reading names what it counts: sessions by their browsers, keys by what holds them, users by their faces. Sessions draws this device as its own line and every other session as its browser with its system in the corner, the network it came from (Tailscale by its mark) and whether it passed a code. API keys opens on what is in use, used this week, never used and expiring soon, over keys drawn as the service their name says holds them and, for an administrator, whose each one is. Users is four readings over the accounts as cards that open their editor, and a role is handed out as a card saying what it allows. Security is a form with its state in a rail, where the password rule lights as it is met. An account without a picture has its initials in its own colour wherever it is drawn.
- Backups is its jobs and what they cover, and the terminal's second tab is the diff of your work
  - The four figures across the top of Backups are gone; each job is a card drawn as what it protects, with its last run in the colour of how it went, where it writes, its last fourteen runs as a strip and when it runs next, and the jobs that failed come first. Everything on the server is drawn as its product under a meter of how much is covered. Beside the terminal, the Git tab is now Diff: every changed file with its status and line counts and its diff under it, and a link to the Git page for the rest. Each terminal session and tab shows the program it is running by its logo, and a terminal where it has none.
- Logs are coloured by what is in them, and the Overview and Metrics draw the machine as itself
  - Every log line is drawn by its shapes: the level in its colour, the program in its own hue, addresses, request lines and paths told apart, a status read by its class and the words that say something failed in red, with error and warning rows washed so they are found by scrolling. A JSON line is its message and its fields rather than raw JSON, the line's own timestamp and this host's name are not repeated beside the time column, the sources are drawn as their products, and one toggle shows every line exactly as written. The Overview opens on one line naming the distribution, processor and hypervisor by their marks, each moving reading carries its last hour inside its tile, and the service tiles show the products they count; Metrics names its processor the same way and draws its top processes as what they are.
- Restarts and rebuilds are read line by line, and the dashboard's own pages are about what they set
  - A restart's or an upgrade's transcript is the whole run in a console — every line numbered, each BuildKit step coloured by the service it builds, the line that failed washed where it sits — rather than the last 64 KB in a small box, and new lines arrive a few at a time instead of landing in one jump. Restart and Rebuild are the two cards beside the stack's three services, the run shows its stages, and the tinted banners around it are gone. Configuration is a form in five sections, each saying what it currently is; Version is one line about the install over the history, drawn as a timeline.
- The rail's top level is twelve rows instead of seventeen
  - Metrics, Processes and Logs sit behind Monitoring, and Proxy & TLS, Packages, System users and the Audit log behind Server configuration under a new Advanced heading. A group opens its panel over the page you are on, the breadcrumb names every level, and the command palette lists every nested page so they stay reachable with the rail collapsed.

### Fixed

- Signing in to GitHub on the Git page also works from the Terminal and over ssh
  - The sign-in wrote a credential helper naming the dashboard container's own copy of gh, which does not exist on the host, so any git push from the Terminal page or an ssh shell failed with "/usr/bin/gh: not found" while the Git page said the account was set up. The helper now names gh without a path, so each side runs its own copy against the same token. An install signed in before this shows a warning on the account button once; Use this account for git rewrites the line. The installer now puts gh on the host, so a new install has it; an existing one gets it by running sudo ./install.sh again.
- Grafana's address and the Minecraft servers' EULA reach the container, and every template's pins are current
  - An optional input with no default rendered as an empty string, so Grafana's root URL was https:///, and an accepted EULA was recorded without ever reaching either Minecraft server. Both shapes are now refused for every definition. Image pins, digests, readiness paths and review dates were refreshed across the catalogue, among them Adminer 6 and Caddy 2.11.
- Container output is read for what it says
  - A healthy Next.js project's Logs read "13 errors", every one a notice on stderr, above a line of cursor-movement bytes. Stderr is no longer promoted to an error, terminal control sequences are resolved to what a terminal would have shown, and JSON log lines are read for their level, message and time.
- Token clones, most registry logins and the GitHub App setup work
  - HTTPS token clones sent a bearer header GitHub's git endpoint answers with a username prompt, so none ever worked; registry credentials were encoded without padding, so the daemon dropped most of them and pulled anonymously; and the page's Content Security Policy stopped the GitHub App manifest reaching github.com, while the callback it returned to could not see the session cookie. The Credentials page itself was rebuilt: kinds are chosen as cards, a pasted URL becomes the bare host, and Test asks every kind for something concrete to try.
- The database browser tells the truth about the rows it shows
  - Export takes the filters and order it was launched from, and states its row cap. A table with no row estimate no longer claims to be empty, and ClickHouse connections, which answered every catalogue read with an error, work again. Each row offers the tables that reference it, paging has First, Last, a page you can type and Refresh, and selecting rows to copy no longer needs write access.
- The terminal stays usable at narrow widths and on a phone
  - The git panel's commit row and repository strip no longer run past the column's edge, and below the large breakpoint the session rail and tools cover the terminal one at a time instead of squeezing it to one line.
- Installing or updating no longer runs out of memory on a 2 GB server
  - The image build ran the type-check in a second process alongside the compiler, which intermittently exceeded a small server's memory. The image build skips it; the application it emits is identical, and the developer's build still type-checks.

### Removed

- File Browser, wallabag, MinIO, Syncthing and Healthchecks are no longer offered
  - Each has a first credential nobody can know before the container starts: a password printed only into its own log, a fixed default account with no way to change it, an archived upstream, a web interface that is full control until someone sets a password, or a sign-up that needs mail. Deployments already made from them keep redeploying, because the definitions still ship and are still validated.

## 0.6.7 — 12 September 2026

**A deployment is a plan, a run, and a release you can go back to**

Deployments were a git pull and a compose up: no record of what was deployed, no way back except another build, and nothing that survived closing the tab. 0.6.7 replaces that with a persistent engine. Every deployment commits its plan before it answers, runs as a queued job with a permanent URL, and produces an immutable release that records its source revision, image digest, configuration and variables — so rolling back reactivates a retained artifact through the same checks and cutover path rather than rebuilding and hoping. Domains, storage, backups and databases are linked to the pages that own them rather than reimplemented, and a reviewed blueprint catalogue — including Minecraft — deploys through exactly the same machinery as a hand-built container.

### Added

- A detected Node service applies its database schema before it serves, and preflight says so when it would not
  - A database created in the dashboard is empty, and Prisma, Drizzle, Knex, Sequelize and MikroORM create no table until their migration step runs — so a freshly linked application answered every request with HTTP 500 and failed its readiness check. Detection now recognises the tool from the manifest and its configuration files and chains its schema step in front of the start command: prisma migrate deploy or drizzle-kit migrate when migrations are committed, prisma db push or drizzle-kit push when only the model is, knex migrate:latest, sequelize-cli db:migrate or mikro-orm migration:up for the rest. TypeORM is recognised and asked about, since its data source cannot be guessed. The command is visible in the configure form and Build settings and follows the package manager when that changes. When a database is linked and neither the start command, a release task nor the package's own start script runs the tool, preflight raises a warning on the start command that names the command to add.
- A backup can freeze the containers that write to it, restore one file, or put everything back in place
  - A job names the containers to pause for the archive step — a pause is a freeze, not a stop; dumps are taken first while the engines can still answer, and the containers resume the moment the archive is written, whatever happened in between. A run's archive browser lists what it holds without unpacking it, and any entries can be restored on their own; a restore can go into a directory as before or back over the original paths, typed with the phrase "restore in place". Schedules can be paused and resumed from the job's menu without re-sending its definition, retention can keep by days as well as by count without ever ageing away the newest archive, an overdue job is reported on the Overview tile, and an administrator can download any verified archive.
- An address is the same thing on every security page, and the failed logins say who is attacking
  - A remote peer on Connections, a repeat offender in the ban log and an attacker in the failed-login record take the same verbs: block at the firewall inline, and behind the menu who owns the address, the reverse lookup and a trace of the route — each of which opens Tools narrowed to that probe with the address filled in. Logins folds the failed record into attackers: each address with its attempts, the account names it tried most and when it started. The jail sheet bans an address by hand, and the tuning dialog offers to never ban the address you are reading it from, so hardening cannot lock you out.
- Certificates say which site uses them, the renewal timer can be switched on, and a passed test hands over to the real issuance
  - The installed list names the sites whose ssl_certificate points at each file. When nothing is scheduled to renew and systemd knows a certbot timer, the page enables and starts it. A staging issuance that passes offers the real one with the same names; the site form links straight to issuance when it names a certificate the dashboard has not seen; a watched domain can carry a port; the DNS providers list says which plugins are installed and which have a token saved, and a saved token can be removed. The TLS report runs from a link on any site, certificate or watched domain, and a listening port's process is one click away.
- Clone a repository onto the server, or start one, from the Git page
  - Add repository clones a URL — or one of the signed-in GitHub account's repositories, picked from a list — into one of the configured Git roots, as the account that owns that folder, or initialises an empty repository in a folder that already exists. Remotes can be listed, added and forgotten; the committer identity can be set per repository; a merge that would conflict is abandoned cleanly with git's own message saying which files clashed.
- The file manager is a cloud drive you can see
  - Pictures and videos are drawn as themselves in the listing, in the details view and the tiles alike, so a folder of screenshots or recordings is recognisable without opening anything; a video tile plays, muted, under the pointer. Space opens any file full screen — a picture fit to the window or pixel for pixel, a video, a PDF, the head of a script, what is inside an archive — and the arrow keys walk the folder. Drag rows onto a folder, onto the tree, onto a breadcrumb or onto a starred folder to move them (hold Ctrl to copy); drop files or whole folders from your desktop to upload them, each with its own progress bar, three at a time, with folders recreated as they were. Right-click a row for its verbs and the space between rows for the folder's; Ctrl and Shift select, the arrows move, F2 renames, Delete deletes, Backspace goes up, and Ctrl+C, X and V do what the menu does. The editor goes full screen and shows a diff of your changes before you save them.
- The schema diagram remembers how you arranged it, and does a great deal more
  - Every arrangement — a table dragged into place, a lookup table hidden, a note, a colour, the level of detail, the viewport — is saved against the connection and schema and comes back on the next visit, in the next browser and for the next operator. A table on the canvas is a place to go from: its menu opens it in Browse, Structure or a ready-made query, focuses its neighbourhood, hides everything unrelated, colours or annotates it; the inspector beside the canvas reads its relations both ways; search pans to the match; the whole picture goes full screen and exports as PNG, SVG, Mermaid, DBML or JSON. Three levels of detail, two layout directions, compact or comfortable spacing, snap to grid, a lock for a finished diagram, and Tidy to lay it all out again.
- The connection page hands out the connection string, and can open a database to the internet
  - The string an application needs is on the Connection tab, masked until you show it and copied whole with one press — reading it is audited, as the deployment link already was. A database started from the dashboard is published to this server only, which is right until you want to reach it from your own laptop or give a colleague access: Open to the internet under Maintenance recreates its container with the port on every interface, adds the firewall rule where there is a firewall to add it to, and a second connection string carrying the server's public address appears beside the first. Close puts it back and removes only the rule the dashboard wrote. A compose-owned database says where its binding is changed instead.
- Creating a project is one page
  - Pick a Git repository, a Docker image, a reviewed template, a database, a Compose stack or an existing workload; the dashboard detects what it is and fills in the build and output settings. Add variables, a database and the public address, open Advanced only when you need to, and press Deploy or Save. An unfinished setup is offered for resumption the next time the page opens.
- Stop a deployment, start it again, and deploy a specific version
  - Stop keeps the release and its data and takes the routes down; Start brings the same release back. Deploy a specific version builds a branch, a tag or a commit once, without moving the branch automatic deployments follow. A Git run now records the commit's subject, author and date.
- Archived projects can be restored, a release can be pinned, and a webhook shows its deliveries
  - Restore brings an archived project back with its history and its name; a pinned release is never pruned; a webhook's row lists what it received and what the dashboard decided, and its secret can be rotated. A pull-request preview can be rejected, older deployments load on request, a proposed hostname is the same on every visit, and a refused setting is marked on the field it concerns.
- Saved credentials, a source you can change, and a project you can duplicate
  - Credentials — a token for a Git host, an SSH key, a registry login or a provider token — are saved once under Deployments, sealed with the server's key, tested against the remote before you rely on them, and picked from a list when a project is created; one cannot be removed while a project uses it. A project's repository, branch, root directory or image can be changed from its General settings: the new source is checked before it is saved and the next deployment builds from it. Duplicate a project to get a draft with the same source and settings, variable names without their values, and no domains, ready to review and deploy under a new name.
- Deploying is one screen now: pick a repository, check what was detected, press Deploy
  - /deploy/new opens on three answers — a GitHub repository, a Docker image, a database — instead of eight outcome cards followed by a source mode, a clone URL and a credential id. Repositories are listed from the GitHub account this server is already signed in to, so a private project is picked from a list rather than transcribed, and its branches come from the same credential. Detection fills in the framework's build and start commands, the port and the public hostname; what is left is a name, an environment and one button, which saves the plan, applies the variables and starts the release. The five-step wizard has not gone anywhere — it is one link away at ?mode=advanced, it still owns Compose stacks, blueprints, game servers and adoption, and quick deploy hands it the draft it was working on.
- HTTPS is part of deploying, not something to arrange first
  - A deployment that asks to serve a name now gets the certificate for it as a step of the run. `provision_certificate` sits between the backup gate and starting the container: it reuses an existing certificate where one covers the name, and orders one over HTTP-01 where none does, before anything is started — so a run that cannot get one stops with nothing left behind, rather than building, starting and then failing its cutover. The hostname itself is generated from an address that already resolves to this server, so there is no DNS record to create and nothing to wait for; where the host already holds a wildcard certificate, that domain is used instead. Your own domain goes in the same field and takes the same path. Repeat deployments ask the authority for nothing: an order inside the renewal window is a no-op, and certbot renews on its own timer afterwards. Activation is unchanged and still the last word — it resolves a real certificate or refuses to move traffic.
- A database hands back its connection string
  - Starting Postgres, MySQL, MariaDB, Redis or Mongo already chose the port, the user and a password nobody has to type. Now it also gives you the URL to paste into the next deployment's DATABASE_URL, with a copy button, instead of leaving you to assemble a DSN from four fields and a password you were never shown. Reading it back later is an admin action with its own audit entry.
- Deployments are persistent runs that survive closing the tab, restarting the backend, and losing the connection
  - A run and its full step list commit to the database before the request answers, so a deployment that was accepted is a deployment that exists. Each run has a permanent URL, a sequenced event stream that resumes exactly where a disconnected browser left off, and a bounded transcript that cannot grow without limit. Environments serialise their own work, host capacity is bounded by configurable heavy and light slots, and expiring claim leases mean a worker that dies mid-build cannot leave a run believed to be running. Restart recovery reads the evidence each step recorded plus the owning feature's own state; it never guesses that a non-idempotent side effect is safe to repeat.
- Every deployment produces an immutable release, and rollback reactivates one rather than rebuilding
  - A release records the exact source revision, image digest, runtime configuration digest and per-variable value digests it was built from. Rolling back takes ordinary confirmation and runs the retained artifact through the same readiness checks and cutover path as a forward deployment, so the recovery path is the path that is exercised every day. Artifact retention keeps every release you could roll back to and reports why space is being held; a tag that moves under you creates a distinct release rather than silently changing an existing one.
- Start from a Git repository, a registry image, a Compose file, an existing container or a reviewed blueprint — and see the exact plan before anything runs
  - Detection is bounded and evidence-based: it says what it found, where it found it, and how confident it is, and re-running it against the same revision gives the same answer. Preflight then reads the host — tools, paths, ports, storage headroom, domains, DNS and certificates — without building, pulling, starting, stopping or writing a single line of proxy configuration. The plan it shows is the plan that runs, with no secret value in it.
- An HTTP service gets a health-gated cutover; anything holding a volume or a fixed port is told it will stop first
  - Where a candidate can run beside the live release, it does: the new container starts, its checks run, and the proxy moves only after they pass. Where it cannot — a database, a game server, a Compose stack, a fixed host port — the plan says so before you deploy rather than promising zero downtime it cannot deliver. A failure before cutover leaves the old release and its route untouched. A failure during cutover restores the previous proxy specification and verifies it before reporting recovery.
- Sixteen reviewed blueprints, including Minecraft Java and Bedrock
  - Nginx, Caddy, Uptime Kuma, Vaultwarden, PostgreSQL, MariaDB, Redis, MongoDB, Gitea, Adminer, Prometheus, Grafana, MinIO, Dozzle, n8n and Minecraft. A blueprint is data shipped and tested with the release, not a script downloaded when you click: unknown fields are rejected, rendering is a pure function, and the same version and answers always produce the same plan. None of them can ship a default password, publish a database port to the network, declare a stateful workload with nowhere to keep its state, or download anything without https and a checksum — those are refusals in the validator. The Docker page's starting points now come from the same catalogue, so there is one list rather than two that drift apart.
- Game servers get a console, a player list, safe settings editing and schedules that save before they back up
  - The console sends one game command at a time through the container's own client and refuses anything carrying a shell character — it is a console, not a shell. Players can be kicked, banned, opped and whitelisted where the server can actually report identities, and where it cannot the controls are hidden rather than shown doing nothing. server.properties is edited through the fields the blueprint declares while every other line keeps its own bytes, comments included. Minecraft versions come from Mojang's own manifest; when it cannot be reached the wizard says so instead of installing an unverified latest. The world lives in a volume named after the deployment rather than the release, so rolling back the server software leaves it untouched.
- Deploy on push, on a schedule, or for a pull request
  - GitHub, GitLab, Bitbucket, Gitea and generic webhooks with per-provider raw-body signature verification, repository, branch and event validation. A replayed delivery, a wrong repository, a wrong branch or an oversized payload never enqueues, and a duplicate delivery creates one run rather than two. Watch paths decide whether a change is worth deploying, with a simulator to check a rule before trusting it. Scheduled actions show their next three run times beside the cron expression they mean. Pull request previews open, update and clean themselves up without touching production.
- The workspace reads the modules that own your domains, storage, backups and databases — and says when it cannot
  - Domains show which proxy site actually serves them and which certificate actually covers them, not merely what you saved. Storage shows whether the volume the release names is still there. Backups show the last run and whether it is inside its maximum age. Findings state what was measured, what it means, and the action that follows, with a link into the page that owns the remedy. A module that could not be read is recorded as an unanswered question with its reason — never as a healthy result, and never as a problem that may not exist.
- Release comparison says what changed, without showing a variable value
  - Source revision, image, command, ports, strategy, storage, dependencies, checks and domains, compared between a release and the one it replaced. Variables are compared by name and value digest only. Beside it, whether the upstream image tag has moved since this release pinned its digest, and which artifacts are still retained so a rollback remains possible.
- The Docker pages explain themselves, and the overview names what is not running
  - Docker, runtime health, attention, exit codes and how CPU and memory are counted all have a definition a hover away, written for somebody who has never run a container. A server with nothing on it gets an introduction rather than four zeros. A stopped container used to be visible on the overview only as a digit changing in the Running tile, so it now has a panel that names it with a Start button beside it; disk is a breakdown of where the space went rather than a sentence about how little of it is worth reclaiming; and runtime health is one bar that shows how much of the estate nothing is checking. Filters on the containers page answer "which of these is down" without reading a column.
- The web terminal opens zsh with inline history suggestions and command colouring on a fresh install
  - The grey text that finishes a command from your history, accepted with the right arrow, used to depend on whatever the operator had set up for their own shell. The installer now puts zsh, zsh-autosuggestions and zsh-syntax-highlighting on the host and points the terminal at zsh through JD_TERMINAL_SHELL, so a clean server has it from the first window. The account's ssh login shell is not changed, an existing .env is asked before its terminal shell is changed, and a history file is kept for an account whose own shell never set one.
- Detection recognises the project and fills the form in, for twenty-one JavaScript frameworks, five Python frameworks, Go, Rust, Java, .NET and Deno
  - Import a repository and the configure form arrives with the framework named, the build and start commands, the port, static output where the framework builds a site, the interpreter or toolchain release, and a Procfile's web process honoured over every guess. The catalogue reads a meta-framework built on Vite (Remix, React Router, SolidStart, SvelteKit) before Vite itself, which is what used to turn a Remix server into a static site of client assets; Astro, Nuxt, Angular, NestJS, Gatsby, Docusaurus, VitePress, Eleventy, Create React App, Vue CLI, Ember and Parcel each get their own serving defaults, and a server framework's entry file is checked after the build so a wrong output path fails with the framework's name rather than at the readiness gate. Django starts by applying its migrations, FastAPI and Flask are started through the application object found in the source (factories included), Streamlit and Gradio through their scripts. Rust (axum, Actix, Rocket, warp, Poem), Java and Kotlin on Maven or Gradle (Spring Boot, Quarkus, Micronaut), ASP.NET Core and Deno build on digest-pinned images with the release the repository declares. Every detected value is an editable setting, and what the catalogue cannot serve automatically is a decision or a finding, never a silent guess.
- The variables your code reads and the database it connects to are listed before you deploy
  - Detection reads .env.example and its relatives (values kept as placeholders unless they look like credentials), a committed .env for names only, and the code's own reads of its environment in JavaScript, Python, Go, Ruby and PHP — never tests, fixtures or documentation — and the environment section opens with those rows, each saying where it was read. A row left empty is skipped rather than set to nothing, and the form says how many are unset. The engines the dependencies, a Prisma datasource or a documented URL name are each one button that opens the database sheet on that engine and the variable the connection belongs in.
- A single-page site answers deep links, and a deploy link opens the form with the repository filled in
  - Vite, Create React App, Vue CLI, Ember, Parcel and Angular sites are served with nginx's index.html fallback, so a client-side route opens directly instead of 404ing; the switch is on the configure form and Build settings whenever there is static output, and multi-page generators keep nginx's own configuration. /deploy/new?repo=<clone url>&ref=<branch> arrives on the Git tab with the URL filled in, which is what a “deploy to your server” link in a README points at.
- A Laravel, Symfony or plain PHP repository deploys with nothing typed in
  - Detection reads composer.json — the PHP version from Composer's own constraint syntax, the ext-* requirements, the framework from its packages and entry files — and builds on FrankenPHP with Composer, an asset stage when the package.json builds with Vite or Encore, migrations before the first request for Laravel and Symfony, and writable storage directories. The configure form mints APP_KEY for Laravel and offers a Generate button on any secret-shaped variable. A Laravel application and a plain index.php are built and served live by the test suite.
- A domain can ask visitors for a password
  - Ask visitors for a password on the public address step or the Domains settings, and the proxy — Caddy or nginx — asks for it before the application sees a request. The password is kept only as a bcrypt hash, so a plan, a release and a preview never carry it; previews inherit the protection, and a protected domain is tagged on the overview.
- Thirty-six more blueprints, every one started live before it shipped
  - Nextcloud, Jellyfin, Navidrome, Audiobookshelf, Kavita, Directus, Healthchecks, Gotify, Homepage, Shlink, linkding, FreshRSS, wallabag, Memos, Trilium, Actual, pgAdmin, phpMyAdmin, Mongo Express, code-server, Portainer, File Browser, Syncthing, Stirling PDF, Metabase, Jupyter, Ollama, Meilisearch, Typesense, RabbitMQ, InfluxDB, MySQL, IT-Tools, CyberChef, draw.io and whoami join the catalogue. A new sweep pulls every deployable definition's image, starts it with generated secrets and runs its own readiness checks, which is how five wrong health paths and settings were caught in review instead of on somebody's first deploy — and how three shipped pins (both Minecraft images and MinIO) were found to no longer exist and were repointed, and MinIO was found to have shipped without the server command it needs.
- A GitHub App: no webhook secrets to paste, private clones without a personal token, and a comment on every pull request
  - Create the App from the Credentials page through GitHub's own manifest flow and install it on the accounts whose repositories deploy here. Its repositories appear first in the import picker and clone with the installation's own short-lived tokens; a GitHub webhook created while it is connected needs nothing configured on GitHub, because the App's single webhook feeds every trigger that asked for it; deployment statuses are posted as the App wherever it is installed; and each preview keeps one comment on its pull request — building, ready with the address, failed with the reason, removed — edited in place as the run moves.
- Certificates can be ordered from a staging or private ACME authority, and object-storage backups are proven against a real S3 API
  - JD_ACME_DIRECTORY points the managed Caddy and certbot at another directory — Let's Encrypt's staging endpoint for a rehearsal that spends no rate limit, or a private authority with JD_ACME_CA_ROOT naming its roots — and the public-certificate journey is proven end to end against a Pebble that validates nothing. S3 and Backblaze B2 backup targets, which already existed, are now exercised live: target test, upload, retention and restore against a MinIO container.

### Changed

- A failed readiness check names the cause when the application's own output proves one
  - A freshly linked database is empty, and an application that queries it before anything applied its schema answers every request with HTTP 500 while its log says which table is missing. The failed check used to say only that the last output was in the build log; it now reads that output, names the missing table, and says what to do about it — apply the schema before the application starts, for Prisma with prisma migrate deploy, or prisma db push when the project has no migrations. Opening the run later shows the same sentence, and the step's evidence records the cause rather than the output.
- Backups reads like the host Overview and says what is not backed up
  - Four readings (jobs, last backup, next backup, stored), then what needs attention — a job whose last run failed, a job that has gone two intervals without a good run, the things on this server no job covers — then the jobs as a plain table and a Coverage list of every Docker volume, compose stack, deployment, Git repository, saved database, the proxy's configuration and the dashboard's own data, each with the job that protects it or a Back up button that writes one: the paths, the SQLite file to snapshot, the connection to dump and the containers to pause come with it. A job opens in a sheet with its facts, its verbs and its runs; the form picks sources from the same list, builds the schedule from presets with its next firings shown, and keeps archives by count and by age.
- The Security section reads as readings, findings and lists, like the host Overview
  - All eight pages open the same way: how this dashboard is reached — the grade, the allowlist, the tunnel interfaces and the address your browser arrived from — then a run of figures on the page's own ground, then what needs attention, then the detail as plain tables. The overview's five tiles carry the verdict of each area and go to it; the firewall opens on its rules, defaults and logging with the controls that set them in one block; SSH opens on the port, passwords, root login and keyed accounts before its settings; a below-recommendation setting is marked by a word rather than a coloured band. No block on any of the pages is framed, every icon-only control is named, and row controls are reachable on a phone.
- The proxy pages are readings, rows and verbs, and the overview says what needs doing
  - Proxy & TLS opens on the engine as a fact — version, whether its unit is running, the directory it reads, certbot and who renews — with test, reload, restart, start and stop beside it, four destination tiles, and an attention list folded from the conditions somebody would act on: an expired, expiring or unreadable certificate, a renewal timer that is off, a site proxying an application in plain text, a site on disk but not serving, streams nginx is not reading, and a database port answering on every interface. Sites, certificates, streams and ports each open on four readings over one plain filterable table, and every site's verbs are words with a sentence — edit, raw config, open, TLS report, access log, duplicate, enable, disable, delete — declared once and drawn by the table, the narrow list and the editor alike. The site form is built from the same vocabulary as every other dialog.
- The Git page is a list of readings and one working surface per repository
  - The list opens on four figures — repositories, uncommitted, behind, unpushed — with chips to filter by them, a plain table that becomes rows on a narrow screen, and the chosen repository in the address bar so a link into a checkout can be shared. A repository is one framed workbench: the file tree, a Changes / History / Branches / GitHub column and whatever you last clicked, separated by hairlines and resizable, with the repository's reading and its verbs in one strip — fetch, pull and push inline, the rest behind a menu where each verb carries a sentence. Changes keeps the stashes at the top, offers amend, and says who the commit will be recorded as with the fix beside it when git has no name. History is searched, paged and narrowed to one file; a commit opens as its message and the files it touched, each one a click from its own diff, with branch, tag, cherry-pick, revert and undo one menu away. Branches show which are merged and which have lost their remote branch, and offer switch, check out a remote branch, merge, compare, rename and delete here or on the remote; tags are listed, made and pushed. The GitHub tab shows each pull request's review and check state, merges or checks one out, lists the Actions runs on the branch, and counts what a new request would carry before it is opened.
- The Files page reads as one workbench, with a sidebar that is your files
  - The left column was a list of a dozen system directories over a folder tree that started at "/", each in its own half of a 256-pixel panel. It is one column now: a tree rooted at the place the folder you are browsing belongs to — home, nearly always — with your starred folders above it, and everything else (the configured roots, /etc, /var/www, the other accounts, the folders you were just in) behind one menu. The listing, the sidebar and the details column share one frame with a hairline between them and can be dragged to the widths you want; the footer says how many folders and files are here, what is selected, and how much of the disk is left; the listing refreshes itself every twenty seconds so a file that arrived by scp shows up.
- Processes is four pages, and every process, application, unit and cron job has its verbs as words
  - Live, PM2, Services and Scheduled each open on four readings — blocked and zombie counts, PM2 applications that are not running, failed units, enabled-on-boot — over one plain table with filters. A process opens to what it listens on, how many connections it holds, how close it is to its open-file limit, and the chain of parents above it; terminate, kill, pause, hang up and priority are words with a sentence each. PM2 says whether each account's applications survive a reboot (boot hook, last save), and adds scale, reset restart counters, flush logs, daemon-wide reload, restart and stop, and a dialog that starts a new application from a script or ecosystem file and shows the exact pm2 start it will run. Units gain clear-failed, reload where the unit supports it, a link to the unit file and daemon-reload. Scheduled lists cron jobs with the schedule in words and its next run, enables, disables, edits, adds and removes one job at a time, and puts systemd timers — with run-now — beside them.
- The database pages read as one product: the connection is the title, Browse and Structure are workbenches
  - The connection's name is the section's title and a switcher, with the engine, address and database as a row of facts under it. Browse and Structure share one table rail and each fill the window as a single working region — the grid takes every row the screen has room for, and Structure can pick a table without a detour through Browse. The connection page is readings, facts and maintenance rows that say what each verb does; Find, Monitor and Generate lost their frames; selections are the neutral fill everywhere rather than the primary tint.
- Every database dialog was rebuilt on one form vocabulary
  - Create table, add column, create index, rename, connect, new database, import and the row editor share labels, fields, hints, errors and options that read the same way. Create table has a column editor that grows with Enter, an id-key preset, duplicate-name checks and its CREATE TABLE beside the button; add column, create index and rename show their statement too. Connecting a database asks for host, port, user, password and database and renders the engine's connection string itself — a pasted string still works. Import takes a dropped file, counts the rows before sending them, and explains each option in a sentence.
- A database created from the Databases page is reachable from anywhere by default
  - New database starts its container with the port published on every interface and the firewall opened for it, the same two things Open to the internet does, so the string under "From anywhere" works the first time you paste it on your own machine or send it to whoever needs the database. A Reachable from anywhere switch in the dialog is on by default; turned off, the database is published to this server only and can be opened later under Maintenance. Where the firewall could not be opened, the dialog says so and the audit entry carries the exposure and what the firewall did. A database started by deployment quick setup stays on loopback: the application reaches it over the deployment network.
- Deployments were rebuilt as one product
  - The projects page is a grid of cards with one status word each, reachable from the command palette; a project has its own pages — Overview, Deployments, Logs, Runtime, Console and nine settings sections of setting cards — instead of a tab in a query string, and every old link redirects. The header carries one command and a menu of verbs as words: Restart, Stop and Start, Redeploy the live release, Rebuild without cache, Deploy a specific version, Open in Docker, Archive. A run's row reads status, duration, title and the commit it built, and the deployment page shows the release path with each stage's duration over the build console.
- Images already on this server can be picked from a list
  - Deploying an image meant typing a reference from memory and finding out at inspection whether it was the one you meant. The images this host has pulled are now listed with their sizes and filterable by name; the free-text field is still there for anything in a registry that is not here yet.
- Existing deployments keep working, and their history, hooks and environment come with them
  - The schema change is additive and the existing project id remains the deployment identity, so existing URLs, webhook endpoints, encrypted environment values and run history survive the upgrade. Legacy projects continue to deploy through the compatibility path while the new engine runs underneath.
- Containers are a list on a phone and a table on a desktop, and a container's actions are words rather than glyphs
  - The nine-column table narrowed by dropping columns, which left a phone reader looking at the remains of a table: a wide name cell, a wedge of space, and two stubs. Below 1280px the same containers are drawn down the row instead of across it, and nothing is left out — image, ports, live CPU and memory, and the issue count are all there. The row's controls changed with it: start, restart and stop stay as icons because they are pressed constantly, and update, pause, shell, copy-id and remove moved into a menu where each one carries a line of plain English saying what it does. A lifecycle button now also says that it is working — a stop reads "Stopping…" until Docker honours it, instead of the row sitting still for ten seconds and then jumping.
- The overview answers what is wrong and what is waiting, not only how busy the machine is
  - A fifth tile shows the fullest filesystem, which is the number that stops a server and the one the health checks never carried. The health list now includes a systemd unit that has failed and a container that is unhealthy or stuck restarting, beside the recorder's own findings. The Services row grew from four cards to the eight modules in the sidebar: security exposure, pending package updates and whether a reboot is owed, deployments that are running or failed, and the last backup run and the next one due. Stat tiles lost the small glyph before their label, service figures took the same 24px as the tiles, and a scrollbar that appeared under Recent activity is gone.
- The metrics page reads as readings on the page, and answers what happened before it shows the charts
  - Ten headline tiles from the newest frame — CPU, memory, load, pressure, network, disk I/O, storage, sockets, processes, and the hottest sensor or the open file handles — each with how it compares to the previous window; a row of facts naming the processor, memory, swap and how history is recorded; the health findings as a plain list; and, before any chart, a list of the moments the window would be remembered for — the CPU peak, a disk at 100% busy, a stall, a failed deploy, a reboot — each of which zooms the charts to the minutes around it. Top processes by CPU, memory or I/O sit beside it. Every chart lost its frame. A dragged span is now in the address bar, so an incident window is a link; the live feed can be paused; and the window on screen exports as a CSV with its peaks. The host now reports sensor temperatures and open file handles, and the health checks judge both: a sensor past its own critical mark, and file handles past 80% of a real ceiling.
- The logs page is one workbench, and a log line is read down its columns
  - The source rail and the lines share one frame with a hairline between them, the way the terminal does; the rail hides and resizes and remembers both. One pane holds the source's facts and the Live/History tabs, the filter with the window and the journal unit inline, the histogram, the lines and a footer that says whether the stream is open, how many lines and errors are on screen, what the search read and whether logrotate is trimming the file. Each line is columns — a level edge, the line number, the clock, a small-caps level mark in the level's colour, the journal unit, then the message in ink — so a page can be scanned down for the red ones instead of read across; the level chips above the lines carry the counts and share their colours with the level column and the histogram.
- Unpinned Python requirements deploy with a warning instead of a refusal, and an undeclared gunicorn or uvicorn is installed
  - Refusing a requirements.txt without exact versions turned the most ordinary Python repository there is into a manual Dockerfile. It now builds, and preflight raises dependencies_unpinned as a warning that says what a rebuild of the same commit may resolve differently and how to pin. uv and Poetry locks, and a bare PEP 621 pyproject, install the way their tools intend; the interpreter follows .python-version, runtime.txt or requires-python (3.10 to 3.13); a process manager the start command runs that no manifest declares is installed at a pinned release into the same environment as the dependencies.
- A blueprint's second port is published beside the routed one, and its startup budget is honoured
  - Gitea's SSH port and Syncthing's sync protocol used to replace the web port silently, so the proxy would have sent browsers to SSH; a second direct port is now an additional publication on its own number, shown on the Domains settings as Also published, and any plan with one activates stop-first. A definition's readiness timeout used to become ten instant attempts before the process had bound its port; it is now paced retries over the whole budget.
- Gradle, Streamlit and Gradio projects are built and served live by the test suite
  - The framework suite gained a plain Gradle jar, a Streamlit script checked through its health endpoint and its own static-serving setting, and a Gradio app checked through the page's embedded configuration, so every detected layout in the catalogue is proven by a real build rather than a rendered Dockerfile.

### Fixed

- Choosing a verb from a row's menu closes the menu
  - Every menu drawn through the shared verb menu — processes, services, timers, cron jobs and now backups — stayed open after a choice, so the action ran behind a menu that was still asking. It closes now, and where a sheet has its own menu above rows that each have theirs, every ellipsis is named after its row.
- Blocking an address now blocks it, and an iptables host is graded on the policy it actually has
  - The one-click block wrote a deny rule at the end of the list, where ufw's first-match rule meant it sat behind every allow and refused nothing. A source-only deny goes in front now — at 1 for IPv4, after the IPv4 rules for IPv6, exactly where ufw will accept it. On a host with raw iptables the inbound policy was read as "accept" where every check expected "allow", so an ACCEPT default was never reported, an exposed database was graded as though a firewall stood in front of it, and moving the SSH port was refused as a lockout the policy could not cause. fail2ban's log is read from its tail rather than whole, and the jail tuning dialog no longer says a change is lost at the next restart when it is written to a drop-in.
- The proxy reads hand-written nginx and Caddy files correctly, and the config editor no longer shows password files
  - A location written on one line — location / { proxy_pass http://x; } — lost everything after its brace and came back with no upstream. listen 4430 and 127.0.0.1:8443 were read as TLS, listen 443 ssl http2 read back as HTTP/2 off, and a hand-written file with no access_log line had logging switched off by its first save. A Caddyfile's handle, header and tls blocks were listed as server names. Enabling a site whose sites-enabled link pointed elsewhere reported success and changed nothing. A Docker Caddy route, which has no file on the host, was offered an editor that could only fail. A validation of a disabled site said valid about a file nginx never read; it now says so. The raw config route refused nothing inside the nginx directory, so a read-only account could fetch the bcrypt hashes in an htpasswd file; those are refused.
- Git could not stage a file with an accented name, hid a second edit to a staged file, and missed linked worktrees
  - Status was read in the form that quotes and octal-escapes any path with a byte outside ASCII, so café.txt was shown as caf\303\251.txt and could not be staged, diffed or discarded. A file staged and then edited again was listed once, under ready to commit, so the second edit went unnoticed until the commit went out without it; it is now listed under both headings with the letter for each side. A both-added or both-deleted conflict read as an addition ready to commit. A linked worktree's checkout carries a .git file rather than a directory and was never discovered. A file whose name contains two dots could not be staged. Polling the page could take .git/index.lock from under a commit typed in a terminal; reads no longer take optional locks. A fresh git init showed no branch at all. Discarding an untracked file did nothing; it is now deleted, and the dialog says so.
- Uploads, moves and pastes no longer overwrite a file that was already there without asking
  - An upload replaced whatever held its name and a move replaced the destination through rename(2), silently. The server now refuses an occupied name unless told to replace it, and the page asks once per operation — replace, keep both or skip — before anything is written; keep both gives the newcomer a numbered name. An upload is also written whole or not at all, so a transfer that drops halfway no longer leaves a truncated file where a complete one had been, and a replaced file keeps its owner and mode. New file and New folder refuse a name that is taken instead of reporting success about nothing; dropping a folder from the desktop uploads its contents rather than an empty file named after it; deleting a selection that contains a folder asks you to type for it exactly as deleting that folder alone does; the parent row is only offered where the parent can be listed; and an archive download of a selected symlink packs the link rather than its target under a path that could begin with "../".
- A database found running on the host was offered under the literal title "{server.driver}"
  - The dialog for a server installed on the machine rather than in a container is titled with the engine's name again.
- A database running on this server could be removed from the dashboard and came straight back
  - Every database found running here connects itself on the next page load, so the Remove row on its connection page forgot it for as long as it took to refresh. The row is only drawn for a connection the sync would not re-add — one on another machine, or one that needed a password typed.
- A database the dashboard could see but not sign in to could not be deleted
  - Delete signs in and asks the engine to drop the database, which fails with the engine's own refusal when the stored password no longer matches — what a container started over a data volume set up under an older password produces. The page now says so, explains why, and the delete dialog offers to remove the container and its data volume instead, which needs no password and is what deleting a database started from this page meant all along. The Delete dialog offers it ticked when the connection cannot sign in.
- A Next.js repository that uses bun, pnpm or yarn now builds
  - Detection wrote "npm run build" whatever the repository locked to, while the builder picked its base image from that lockfile. A bun.lock project therefore built on oven/bun, installed perfectly, and then died on `npm: not found` — with nothing in the configuration screen pointing at the field that was wrong. The detected commands now name the package manager the lockfile names.
- Web deployments are no longer blocked at the last step by a readiness check nobody was asked about
  - Preflight requires a readiness check for anything serving HTTP, and the wizard's defaults did not include one — so an ordinary web deployment reached Review and was refused over a control buried under Advanced. Detected web and static plans now carry an HTTP readiness check on the port they were detected on, and both flows render preflight's findings, with the setting each one is about, where the decision is made rather than on a later screen.
- Opening one deployment no longer loads every deployment on the host
  - The workspace polls every few seconds and was reading the whole fleet, then filtering it down to one row in Go — with a handful of extra queries per deployment for health and run history. Both reads are now batched into a fixed set of statements whose count does not grow with the number of deployments, and a test fails if a per-deployment query ever returns.
- A path validator checked a trimmed copy of the string its callers actually used
  - Found by fuzzing: a relative path with a leading carriage return passed the control-character check, because the check ran against the trimmed string while the caller joined the original onto a root. It now refuses input that is not already trimmed. Three further fuzz findings were fixed alongside it — a typed variable reference accepting a parent segment, the properties editor silently dropping trailing whitespace, and an archive-root helper that was safe only when called through its validator.

## 0.6.6 — 1 September 2026

**The process table tells you what owns the work**

The Processes page could rank a short list by CPU or memory and send SIGTERM, but it could not tell whether a row belonged to systemd, PM2, a container, a login or the kernel; its search silently ignored every process below the first 200; and disk pressure had no path back to the process causing it. The live inventory now identifies ownership from the host itself, chooses the resource that needs attention, exposes the counters behind that choice, and puts configuration and safe control beside the process instead of at the end of an SSH session.

### Added

- Live processes identify what owns them — PM2, systemd, a container, a login session, the kernel or nobody managing them
  - The owner comes from the cgroup the kernel assigned, with PM2's own PID list overlaid where cgroups cannot tell its children apart. Search and filters cover owner, user, state, name, command and PID, and the complete inventory supplies the counts in each filter rather than only the rows currently on screen. An unmanaged label is deliberate: it says a process will not come back through a supervisor after it is stopped.
- Automatic focus moves between CPU, memory and disk I/O when the host says the bottleneck moved
  - Blocked tasks, iowait or I/O pressure rank processes by their current disk rate; low available memory or memory pressure ranks resident memory; otherwise CPU stays the useful default. The header says which signal made the choice, and CPU, memory, disk I/O or longest-running can still be pinned by hand. Refresh cadence and the number of returned rows are configurable and remembered on this screen.
- A process opens into its executable, working directory, parent, uptime, threads, file descriptors, disk totals and scheduling priority
  - Working directories and executables link into Files. Administrators can adjust the Linux nice value, and the full safe signal set is available with plain-language names — reload, pause and resume as well as terminate — behind the same confirmation and audit boundary as before. Process environments are deliberately not returned; they routinely hold credentials.
- PM2 can gracefully reload an application and save the current list for its existing startup hook
  - Saving runs pm2 save; it does not install or rewrite PM2's platform-specific boot integration. systemd details now put the effective account, working directory, restart policy, resource limits, current resource use and unit file beside the live journal, with a direct route to the unit file in Files.

### Fixed

- A process search now reaches the whole host instead of searching only the 200 rows already selected
  - Filtering happens before sorting and the response cap, and the page separately reports how many matched, how many exist and whether the answer was truncated. The old page told you to filter to reach the rest, then made that impossible.
- A stale process row cannot signal or reprioritise a different process that reused its PID
  - Every control carries the start time of the process that was on screen. The server re-reads that PID immediately before acting and refuses with a refresh instruction if it now belongs to something else. Disk-rate sampling uses the same identity, so PID reuse cannot appear as an impossible I/O spike either.

## 0.6.5 — 30 August 2026

**A file manager, rather than a directory listing with an editor attached**

The Files page opened at "/", drew the same grey glyph on every row, and answered every click by loading the file into a code editor — which is the right answer for a config file and the wrong one for a picture, a tarball, a video and a two-gigabyte log. It now opens where your work is, shows what each thing is before you open it, finds a file from three letters of its name, and edits pictures without sending them to a laptop and back.

### Added

- One click shows you what a file is — the first lines of a config, the picture itself, what is inside an archive, how big a folder really is
  - Selecting a row asks the server what the thing is rather than loading it: a text file comes back as a trimmed head with line numbers, an image as its dimensions and the picture, a video or an audio file with a player, a PDF rendered, a zip or a tarball as the list of what is inside it without unpacking anything. A folder gets a Measure button — the recursive size the listing has always shown as "—", with the heaviest few children ranked underneath, which is the answer to "what is eating this disk". Opening the file is the deliberate second click. Media is served under a closed allowlist of types, so a page uploaded into a web root is still a download rather than something the browser will run.
- Three views — details, tiles and tree — chosen from one control and remembered
  - Tiles are for directories of images and for folders you would rather hit than aim at: thumbnails are the real files drawn small, in three sizes. The tree is now a view in its own right as well as the rail beside the listing. Sorting, hidden files and tile size moved into one Arrange menu, so the tile view can be sorted too rather than silently losing the ability.
- Files are drawn by what they are: source, data, keys and certificates, archives, media and configuration each get their own icon and colour
  - Roughly two hundred extensions plus the files that have none — Dockerfile, Makefile, authorized_keys, .env, a lockfile — and folders whose name says more than "folder", like .git, node_modules and a web root. It is by category rather than by language on purpose: eight colours is something the eye learns in a directory or two, twenty icons is a legend to memorise. An nginx.conf.bak is still a configuration file, and a symlink keeps its target's icon with a link badge in the corner.
- Find a file by typing three letters of its name — ngxcnf finds nginx.conf
  - Ctrl+P opens a fuzzy finder that ranks a match in the name above one in the folder above it, a run of characters above scattered ones, and a shallow path above a deep one, with the matched letters underlined so the ranking is legible rather than magic. Terms are ANDed, so "app tsx" narrows. It searches the folder you are in and widens to home in one click, skips node_modules and .git, and stops itself on a budget rather than walking a disk for a minute — saying so when it did.
- Type a path instead of clicking to it, with Tab completion
  - Ctrl+L — the browser's own chord for an address bar — turns the breadcrumb into a text field that completes on Tab to the longest common prefix, exactly as a shell does. The separators between crumbs became menus of the folders beside them, so moving from one site's public directory to another's is one click rather than three levels of walking back up.
- The page opens where your work is, with a rail of places to get back to
  - Home rather than "/", which is the one directory on a Linux server where nothing you own lives. The rail lists what the machine says about itself — home, the permitted roots, the account directories, and the handful of paths a server keeps its work in, each checked before it is offered — plus folders you star, which are kept on the server because which directory matters is a fact about the box and should be there from another browser, and the folders you were in recently, which are kept in this browser because they are not.
- Crop, rotate, flip, resize and re-encode a picture in place
  - The alternative was copying it to a laptop, opening something and copying it back — for a favicon forty pixels too wide, on the server whose job is to serve that file. Brightness, contrast and saturation, a format and quality choice for JPEG and WebP, ten steps of undo, and Save over the original or Save as a new name. Nothing needs to be installed on the host, and the saved file keeps the owner and mode it already had.
- The editor grew the things an editor is expected to have
  - Ctrl+S saves, find and replace and go-to-line are the editor's own, and there is now word wrap, a minimap, font size, a formatter, a language override for the files whose extension says nothing, a line and column readout, Save as — which is the cheapest possible backup before editing something that keeps a server up — and a prompt before closing with unsaved changes instead of losing them silently.
- A checksum, on the row, for the file you are looking at
  - sha256, sha1 or md5, computed on the server and copied with a click — the other command people keep a terminal open for.

### Changed

- One click selects and previews; two clicks open
  - The click model every desktop file manager uses. A single click used to open the code editor for anything, which is how a JPEG and a tarball both ended up in front of a text editor that would only refuse them.

## 0.6.4 — 29 August 2026

**Controls that do what the page says they do**

Docker's own accounting said forty-three gigabytes could be reclaimed on this server, and nothing in the dashboard could reclaim any of it. The button under that figure navigated to the image list, whose only control removes dangling images — of which a host that redeploys through compose usually has none — and the build cache, which was forty-one of those gigabytes, had no route in the product at all.

Pulling that thread ran through the proxy and security pages, which had the same shape of problem in a dozen places: a control that reported success and changed nothing, or changed something other than what it said. Deleting a proxy site left a copy the list showed as a second site, so deleting that produced a third called .bak.bak. Choosing an application profile in the firewall form wrote a rule for the wrong end of the connection. Allowlisting your own address in fail2ban lasted until its next restart. Under most of them was an assumption about what a Linux server looks like — that nginx keeps its sites in sites-available, that a machine knows its own public address, that a password file root can read is one nginx can read — each false on a large share of the servers this runs on, and each perfectly true on the one it was written against.

### Added

- The build cache can be emptied — on most servers it is the largest thing Docker is holding
  - BuildKit's cache lives outside the image store, so neither an image prune nor the "prune everything" sweep ever touched it, and no route in the product could. It is its own line in the disk panel with its own button, and it is part of the reclaim sweep. Emptying it costs a slower next build and nothing else.
- A disk panel on the Docker images tab: where the space went, split into images, build cache, containers and volumes
  - The `docker system df` view, which is the first thing anybody runs on a server that has filled up. Every figure in it was already being computed and none of it was on screen. Each line says what is reclaimable and carries the button for it — except volumes, which are the one line here that is data rather than a copy of something fetchable, and are still removed one at a time from their own tab.
- Buttons for removing stopped containers and unused networks, which had routes and no way to press them
  - Both endpoints have existed since their tabs did and nothing ever called either, so clearing up meant doing it one row at a time. An unused network also holds a subnet out of the pool, which is what makes a later `compose up` fail to find one.
- The warning about an uncapped log file now has a button that caps it
  - It described a fix the dashboard could perform and did not offer. Docker cannot change a log driver on a running container, so it rebuilds it with a 10 MB limit over three files — the same bargain the restart-policy fix makes, and the dialog says so.

### Fixed

- Reclaiming Docker disk now reclaims it, from the health finding, the Docker page and the new disk panel alike
  - The "Reclaim it" button under the health finding used to navigate to the image list rather than reclaim anything, and the prune it sent you to removes only dangling images — so on a server whose images all carry tags, every route to reclaiming disk freed nothing while the page promised tens of gigabytes. All three entry points now run the same sweep, and it is the one the figure describes: every image no container is using, plus the whole build cache, and never a volume.
- Reclaimable figures now match what a prune actually frees, rather than over-reporting by every shared layer
  - An unused image whose layers also belong to a running one frees almost nothing when it goes — python:3.11-slim measures 189 MB and gives back 2. The old sum counted the full size of everything unused, so the promise was always larger than the result. Both figures are now Docker's own, and a test drives a real daemon to keep them that way.
- The disk figures refresh after a prune instead of redrawing the number they had before
  - The reading is cached for a minute and served stale while it refreshes behind the request, so a successful prune was followed by a page still showing the pre-prune total — which is indistinguishable from a button that did nothing. Every prune now drops the cached reading.
- The networks tab shows how many containers are on each network, instead of zero for every one
  - Docker's network listing never fills in its container map — only an inspect does — so the count was structurally zero on every host, and the delete dialog told you nothing was attached to a network carrying a running stack. The membership is joined from the container list, which already carries it, and the dialog names the containers it would cut off.
- The warning about data written inside a container instead of a volume now fires
  - It reads the container's writable layer size, and Docker omits that from a container listing unless it is explicitly asked for — so the value was always zero and the check had never once run, on any host, while the panel advertised it. It reads the figure from the disk-usage walk instead, which already computes it. On the server this was found on, it immediately reported 28.3 GB sitting in one container's own filesystem, due to be destroyed by its next update.
- Editing or recreating a container keeps its logging settings instead of resetting them to Docker's unlimited default
  - "Edit" here means read the container back and build it again, and the shape it was read into had no room for a log driver — so a container someone had capped came back out of an edit keeping every line it would ever print, in one file that is never rotated, with nothing on screen having changed. The `docker run` and compose previews show the setting too, so a command copied out of this dashboard carries it.
- Deleting a proxy site no longer leaves a second site called <name>.bak behind
  - The delete keeps the previous file beside the original — validation catches a broken config, not a correct one that says the wrong thing, and the only cure for the second is the version before it — but the site list showed every file in the directory, so the copy appeared as a site of its own and deleting that one produced .bak.bak. nginx reads none of these: sites-enabled is a directory of symlinks and conf.d is included as *.conf. The listing skips them now, along with the .dpkg-old and .rpmsave files a package upgrade leaves next to a config it touched, which used to show as a duplicate of every site apt had ever updated.
- Sites can be deleted and edited on hosts that keep their nginx config in conf.d
  - Which is every RPM distribution, Alpine and Arch — most of the servers this runs on. There the site list reports a name that already ends in .conf, and both delete and save appended a second one and acted on app.conf.conf, which exists nowhere: delete answered "no such site" about a site plainly on the page, and save wrote a duplicate while leaving the original serving. The enable switch is gone on those hosts too, replaced by "always on", because conf.d has no symlink to toggle and the control could only ever return an error.
- Basic auth on a site now works — the password file is readable by the server that has to read it
  - nginx opens auth_basic_user_file in a worker, which runs as www-data, nginx or http depending on the distribution, not as the root that wrote it. The file was 0640 root:root, so every visitor to a password-protected site got a 403 and the error log said "Permission denied" — which reads exactly like a wrong password. The group is handed to whoever this host's nginx.conf names, and where that account cannot be resolved the file is world-readable instead, which is what htpasswd itself produces and is a great deal better than a login nobody can pass.
- Extra configuration and the probe blocks survive an edit instead of being deleted by it
  - The site form's escape hatch was the one field saving destroyed: anything typed into "Extra configuration" was written into the file and silently dropped the next time the form read it back, so opening a site and pressing save removed directives nobody was shown. The "Block common probes" switch was the same — it read back as off, so an edit turned it off. Both round-trip now, and a test renders, parses and re-renders to keep them that way.
- A restriction on one path stays on that path when a site is saved again
  - An allow list inside a location block was read back as the site's own, so editing a site whose /admin was limited to a private range and saving applied that limit to the whole site — locking every visitor out of a site that had been public.
- "Does this domain point here?" says it cannot tell, rather than reporting a correct domain as wrong
  - The check compared the domain's addresses against this machine's own routable ones. A VPS behind provider NAT has none — AWS, Google Cloud, Azure and Oracle all give the instance a private address and map a public one in front of it — so on all four the answer was always "this is not an address on this machine", in front of a domain that was configured perfectly. A check that could not run is not a failure any more than it is a pass, and it now says which it is.
- A DNS certificate plugin is found whatever Python the host ships
  - The search named two interpreter versions, so RHEL 9 on python3.9 and Debian 13 and Fedora on 3.13 were all told a plugin they had installed was missing — a refusal in front of a wildcard certificate that would have been issued. The renewal check grew the same treatment: Fedora and RHEL schedule certbot-renew.timer rather than certbot.timer, and Alpine runs it out of /etc/periodic, so all three used to be warned that nothing was renewing while something was.
- A new stream no longer silently replaces an existing one of the same name
  - "New stream" and "Edit this stream" post to one route, and only the site form was passing the flag that tells them apart — so a forwarding rule could stop pointing where it used to with nothing said. The stream page also stops reporting nginx as reading these when the include is present but commented out, which is exactly how it arrives when somebody pastes the snippet and thinks about it.
- Saving a site reports it as enabled only when it actually is
  - If the symlink into sites-enabled could not be written — a read-only directory, or a real file sitting where the link belongs — the failure was swallowed and the page said the site was live. A link left pointing at a different file was the same story with a worse ending: the new config was never in nginx's include tree at all. Both are handled now, and a save that cannot enable the site undoes itself rather than leaving a file nobody can see.
- Password files and stream configs follow JD_NGINX_DIR instead of assuming /etc/nginx
  - Both were written to a hard-coded path, so a host whose nginx lives somewhere else — which is the only reason that setting exists — got files in a directory its nginx never reads. The listening-ports page had a smaller version of the same problem: every connected UDP socket was counted as something the server was accepting on, so an ordinary DNS lookup showed up as an open port.
- Firewall rules written from an application profile now open the port they name
  - Picking "Nginx Full" or "OpenSSH" and nothing else was refused outright by ufw — "Need 'to' or 'from' clause" — so the profile picker never worked on any host for the case it exists for. Adding a source made it worse rather than better: ufw accepted the rule and bound the profile to the *source* port, so what landed in the firewall admitted traffic coming from port 22 rather than going to it. A profile is a destination, and it is written inside a `to` clause now. Each argument list this form builds is checked against a real ufw with --dry-run in the test suite.
- A rule with a destination address is read correctly, warnings and all
  - ufw prints the address in front of the port — "10.0.0.5 5432/tcp" — and the whole column was taken as the port. Everything keyed off the port went quiet with it: no service name, no "this database is open to the world" warning, and an edit that read the rule back with a port nothing could parse. Port lists and ranges get the same treatment, so a rule opening Redis as `6379,6380` now raises the warning it always should have.
- Forwarding rules are recognised instead of being read as inbound ones
  - `ufw route` rules print ALLOW FWD, and ufw-docker writes a great many of them. The word was read as part of the source address, and a rule with no direction counts as inbound — so a host whose only rules were forwarding rules satisfied the "something is allowed in" test that guards switching the inbound default to deny. They are also no longer editable from the rule form, which has no way to express one and would have saved it back as an inbound rule.
- Editing a rule that names only a source no longer tries to write it as an application profile
  - ufw prints "Anywhere" as the destination of a `deny from 203.0.113.9`, and the form read any non-numeric destination as a profile name — so reopening such a rule offered to save `app Anywhere`, which is not a profile on any host.
- fail2ban will not ban the address you are connected from
  - A ban installs a firewall drop, so banning yourself severs the session exactly the way an inbound deny rule would. The firewall page has refused that since it shipped and the ban route reached the same outcome with nothing in the way. Same guard, same narrowness: only the caller's own address is refused.
- A fail2ban allowlist entry survives a restart instead of disappearing at one
  - `addignoreip` changes the running server and nothing else, so the address an operator allowlists after banning themselves was gone the next time fail2ban restarted — silently, with the page still showing it. It is written into the same jail.d drop-in the ban time and retry count already used, which fail2ban reads last.
- The security verdict counts failed logins over a week, not over the whole of btmp
  - The figure was the length of a capped 500-record listing of the entire failed-login file. So the "sustained attempts" threshold of 2000 could never be reached however hard a host was being hit, and the 200-attempt notice fired permanently on every server with a public SSH port regardless of what happened this week. It is a count inside a window now, and where the sample runs out it says "at least" rather than quoting a floor as a total.
- The SSH port control shows the port SSH is actually on
  - On a socket-activated host — Ubuntu 24.04 and later by default — systemd holds the listener and sshd_config's Port is read, resolved and ignored. The panel header said "Port 2222 · held by ssh.socket" while the field under it said 22, and saving that displayed value read as a request to move SSH back onto it.
- Ports are checked for being ports, not for being five digits or fewer
  - 0, 99999 and a range written backwards all matched the pattern and were refused three layers down by whichever tool ran, with an error naming none of the four fields on the form.
- The login pages say why they are empty, and name the package that fixes it
  - "wtmp is not being written here" was the wrong diagnosis: the records are there and it is `last` and `lastb` that are missing, from util-linux-extra, which minimal cloud images leave out. The posture verdict already said so; the panel does now too. A null MX in the DNS tool also reads as a null MX rather than as a preference followed by a blank.

## 0.6.3 — 29 August 2026

**Every page comes back the way you left it**

Hide the file panel on the terminal page, go and look at something else, come back — and it was open again. Every page in this dashboard is thrown away the moment you navigate off it, so every panel you closed, folder you collapsed, tab you chose and sort you set was gone by the time you returned. That is the whole arrangement of a page you come back to all day, and it is answered everywhere at once rather than on the page that annoyed somebody most.

### Added

- Pages remember how you left them arranged — hidden panels, collapsed folders, the tab you were on, the sort you chose
  - One store behind the whole app rather than a fix per page, so it covers the terminal's session rail, its Files/Git companion and which half of that you had open, the folders you collapsed in the rail, the file manager's tree, hidden files and sort order, the tab on Processes, Packages, Account, Deployments, a container, a stack and a repository, the processor's total-or-breakdown switch, "show system accounts", the connections filter, and the sidebar itself, which now stays collapsed across a reload instead of springing back. It is kept in the browser you are sitting at, like the theme and the terminal's font, not on the account. What you were looking at is deliberately not kept: a search box, a selected row, an open dialog and a half-filled form all start empty, because a filter restored from yesterday is a table that looks broken for no visible reason.

### Fixed

- Cards, panels and stat tiles no longer press down like buttons when you click them
  - The lift that makes a control look like something you press is one rule shared by every raised surface in the app, and its pressed state never asked whether the surface was a control. So a click anywhere on a panel — selecting a line of log output, dragging across a table, pressing on a heading — sank the whole card a pixel and took its shadow away, which reads as pressing a button that does nothing. The press now applies only to what a pointer can actually activate: buttons, links, tabs, checkboxes, switches and sliders. Nothing about the resting look changed.

## 0.6.2 — 29 August 2026

**The updater runs an image that is still on the machine**

Pressing Update on a recent Docker daemon could fail immediately with "could not start the updater: No such image: sha256:…", on a dashboard that was working perfectly at the time. Nothing was damaged and no upgrade was half-applied — the run died before it fetched anything — but the button did not work, and the reason it did not was invisible from the page.

### Fixed

- The in-app update no longer fails with "No such image" on Docker's containerd image store
  - The updater runs the backend's own image as a sibling container — that image already carries git, the docker CLI and the compose plugin, so there is nothing to pull — and it took the reference for it from Docker's container listing. That listing reports a tag only while the tag still points at the same image: after a rebuild that moved just-dashboard-backend:latest onto the new build without recreating the backend, it reports a bare sha256 instead. On the containerd image store, which is the default on recent daemons, an untagged image is collected even while a container is running from it — the container keeps its unpacked snapshot and carries on working, which is why nothing looked wrong until the day you pressed Update. The name the compose file pins is used now: it is a tag, so it moves with the rebuilds rather than being orphaned by one.

## 0.6.1 — 29 August 2026

**The last panels that ended at a shell prompt, and a dashboard rebuilt around one design system**

Packages, logs and git history were the three pages that still sent you to ssh — to find out what a package is called, to grep the log that rotated last night, to see which branch a commit is on. They answer in the panel now. The shell around them was rebuilt at the same time: the four features that were a screen of tabs each became their own routes, twelve themes became one palette in light and dark, and every control in the app now looks like something you press.

### Added

- Packages: search the archive, install, remove, and see what a package actually put on your path
  - The reason people open a terminal instead of a package page is that they do not know the name — it is postgresql-client, not psql — so the list updates as you type, and a search that comes back thin is widened to the descriptions, because somebody who does not know the name types what the software does instead. Once a package is installed the second tab answers the question every other panel in this class leaves hanging: which commands it added, which units it registered, what it left in /etc, and its primary manual page rendered next to them. Nothing runs the package's own binaries to find out.
- The package pages work on apt, dnf, yum, zypper, pacman and apk, and say how old the index is
  - Every read answers from the package database already on disk, which is right — a search that refreshed first would take a minute per keystroke — and it means the catalogue is only as current as the last refresh. On a server nobody logs into, that is the first timer to stop, so the age is on screen with the button that fixes it. Removing a package is an ordinary confirmation; purging one, which also deletes the configuration it left in /etc, asks you to type.
- One log filter, applied to every source, over the rotated archives as well as the live file
  - The grep box and the level chips used to be applied to file tails only, so they silently did nothing on the docker, PM2 and journal sources. There is one filter now, compiled once and applied to all of them, and a filtered tail opens on n *matches* rather than n lines — "the last 400 lines" of a log where one line in a thousand is an error is an empty page in front of a file full of them. The rotated generations beside a file are read as part of it, gzip and bzip2 included, so "when did this start" can be asked past last night's logrotate run.
- Log retention is reported as a verdict rather than as logrotate's rule list
  - The file with no rule governing it is the one that fills the disk, and it is exactly the entry a rule list cannot show. A rule that exists and has not run in a fortnight is reported too, since that is the failure this panel is really for.
- A commit graph on the git page, drawn from the same walk that lists the commits
  - Which branch a commit is on, and where the branches met, is the part of git history that a flat list cannot say.
- The dashboard's own version has a page of its own
  - It used to be the top half of a page whose bottom half was the host's packages, on the theory that "what can be updated on this machine" is one question. It is not — one of them is your server and the other is the tool you are looking at it through — and the release notes, which are the part somebody actually reads before upgrading a root-equivalent panel, had nowhere to live but a sheet over a table of library versions.

### Changed

- Docker, Databases, Proxy and Security are each a set of pages now, not one screen of tabs
  - A sticky sub-nav strip, an Overview that points at the detail pages rather than piling everything onto one screen, and a sidebar entry that expands to its children instead of being both a link and a toggle. Metrics took over the old Overview body, which leaves Overview the landing it should always have been: host header, health, an hour of sparklines, recent activity, and a card per service.
- One palette in light and dark, instead of twelve themes
  - Twelve palettes meant twelve places for a colour to be almost right, and no way to tell which of them a screenshot came from. Every verdict in the app now renders through one accordion of findings rather than a stack of tinted alert boxes, and a live state is a coloured dot and a word rather than a filled pill — the tinted badges stay, but only for tags, which are a fixed property of a row and never a state.
- Every control looks like something you press
  - A button now has a face a shade lighter than the surface under it, a hairline of light along its top edge, a dark lip at the bottom and a shadow on the page below — inverting when you press it, so the control sits into the page. Cards make the same claim in the same language rather than in one of their own, and hovering settles a control towards the surface instead of lighting it up. The quiet actions at the end of a table row stay flat, because giving 142 of them a face turns every row into a strip of controls competing with its own data, and the nav stays a list for the same reason: the thing that should stand out in a column of forty-nine items is the one you are on.
- The typed confirmation is reserved for the actions that have no way back
  - A phrase in front of something recoverable is typed rather than read, and that habit is precisely what it is protecting on the routes that keep it. Deleting a proxy site, an nginx stream, an htpasswd file or a git branch, pruning images, networks or containers, and ending an SSH session all still stop you with a confirmation — they no longer ask you to type. What keeps the phrase is what costs you the machine or the data: the firewall and sshd, every DROP, TRUNCATE and restore, removing a volume, compose down, git discard and git reset --hard, and installing a new version of the dashboard itself.

### Fixed

- Clicking a pane in the terminal focuses it, and a killed pane stops leaving a chip behind
  - Both were the pane list being polled only once the window already reported more than one pane — a count up to five seconds stale — so for the seconds after a split there was nothing to click, and for the seconds after a kill there was a chip pointing at a pane that no longer existed.
- The terminal's jump-to-the-end button appears when tmux has actually scrolled
  - It used to appear on the first wheel-up whether or not anything moved — which under a full-screen program that wants the mouse is never — offering to return a terminal that had not gone anywhere, and its button called a scroll the emulator cannot perform under tmux. The pane now asks tmux whether the pane is in copy mode and believes the answer, in both directions.
- Terminal shortcuts only fire where the shell has the keyboard
  - They move sessions, close windows and kill panes, and they were firing from anywhere on the page — so Ctrl+Alt+W closed a tmux window while the operator was clicking around the file tree.
- Box-drawing characters in the terminal line up, and the terminal follows the theme
  - Block and box glyphs are drawn from a vector atlas rather than from the font, which spaced them wrong and broke the border of every full-screen program. The colours are derived from the live theme at runtime, with a neutral ramp mixed per mode so the dim colours stay legible on white as well as on near-black.

## 0.6 — 27 August 2026

**The security and proxy pages say what the machine's posture is, on whatever distribution it runs**

The Security and Proxy pages showed you facts and left the reading to somebody who already knew how. They now grade the machine and say what to do about it — and they work on Fedora, Rocky, openSUSE, Arch and Alpine as well as on Debian and Ubuntu, where before the firewall reported inactive on every host that had one and every non-apt server was told it had nothing to update.

### Security

- An nginx address restriction is now a fence rather than a suggestion
  - nginx reads its access directives in order, stops at the first match, and falls through to permit — so a site restricted to a private range was reachable from anywhere on the internet. The list is now closed with `deny all`, with explicit denials above it so first-match still means what it reads as. One case it still cannot cover is stated rather than papered over: nginx answers a redirect in the rewrite phase, before the access phase runs at all.
- An account list written into sshd_config can no longer carry a directive of its own
  - A newline inside a list of allowed users would have written a setting of the caller's choosing onto the next line of the file.

### Added

- The Security page grades the machine instead of only showing its settings
  - Exposure, firewall, sshd, intrusion prevention, open ports, certificates and pending patches become findings that carry what was measured, what it means, what to do about it, and — where the dashboard can carry out the remedy itself — a button. The rules are deliberately conservative: an exposed database behind a default-deny firewall is a warning rather than a critical, and "turn off password authentication" stops being offered when no account holds a key, because there it is not advice, it is a lockout.
- The firewall page writes firewalld as well as ufw, and says plainly when it can only read
  - firewalld's model is genuinely different — zones, services and rich rules rather than numbered lines — so a rule with a source becomes a rich rule, everything is written --permanent and reloaded, and the page numbers rules positionally. Raw iptables stays read-only on purpose: it has no persistence of its own, so a rule added from here would work until the reboot and then vanish, leaving a page that says protected in front of a host that is not.
- Package updates work on dnf, yum, zypper, pacman and apk, not only apt
  - Every RPM, Arch and Alpine host used to report no package manager, which renders as "nothing to update" rather than "never checked" — so the posture audit's patch check was silently dead on half the servers this runs on. Alpine and Arch publish no advisory data, so they now say the security count cannot be told rather than reporting zero, and a security-only upgrade is refused there instead of quietly applying everything.
- A site builder that writes the nginx for you, shown live beside the form
  - Proxy targets, static roots, redirects, extra paths, WebSocket upgrades, basic auth and address restrictions, rendered on the server so there is one implementation of what a site means. The symlink goes in before nginx -t, because a file not yet in the include tree is one the test has nothing to say about.
- Certificates can be issued over DNS-01 with eight provider plugins, and one you bought can be imported
  - DNS-01 is the only route to a wildcard and the only thing that works behind a CDN. An imported key is checked against its certificate first, because a mismatched pair is accepted by every text editor and refused by nginx at reload — on a live server, during an outage.
- A live TLS report grading what a visitor actually gets
  - Each protocol version is probed on a connection of its own, and reported as unknown where this client would not ask — calling it absent would be a false reassurance about exactly the versions that matter most.
- Streams forward the services that do not speak HTTP, and password files are managed here
  - nginx's stream is a top-level context a server file cannot reach, so these live in their own directory and the page says plainly when nginx.conf does not include it; nginx.conf itself is never edited from here. Passwords are hashed with bcrypt in process, so the credential never becomes an argv that /proc/*/cmdline makes world-readable.
- Long operations run as jobs you can leave and come back to
  - Issuing a certificate, upgrading every package and reloading sshd used to hold a browser request open for the length of the run, so a dropped connection left you with no idea whether your SSH configuration had been applied. The work now descends from the process rather than the request: closing the tab, navigating away and losing the connection all leave it running and the transcript complete, and a finished run can be reopened from a recent list. What stays synchronous is the refusal — a bad email, a wildcard asked for over HTTP, an sshd change that would lock you out — so it answers the click that caused it rather than arriving a minute later as a failed run.
- fail2ban jails can be tuned in place and survive a restart, with a fold of the repeat offenders
  - Settings are written to jail.d/*.local and merged rather than regenerated, so no other jail or hand-written line is lost. The repeat-offenders view answers what a ban list cannot: who keeps coming back.

### Fixed

- The firewall no longer reports itself inactive on every host that has one
  - ufw's parser takes exactly one of `status numbered` and `status verbose`, so asking for both returned an error and no rules — which the page rendered as a firewall that was not running.
- Deleting or editing a firewall rule affects that rule and nothing else
  - A ufw rule written without an address family becomes two entries and deleting by number took only one, leaving its IPv6 twin behind on a page that said the rule was gone. Editing a rule to an unchanged value was worse: ufw answers a duplicate add with "Skipping" and the old number was then deleted anyway, so the rule that vanished was the next one down. Rules are now found again by what they are, in a listing read after the change.
- Moving the SSH port works on Ubuntu 22.10 and later, where systemd owns the listener
  - Socket activation is the default on 24.04: sshd_config's Port is read, resolved and reported back by sshd -T, and ignored, so the control passed validation, reloaded successfully, read back the new value — and the machine went on answering on 22. The socket unit is now read and written alongside the daemon, on both address families, and restarted rather than reloaded, because systemd rebinds a socket's addresses only on restart. The page says which unit holds the port and which port it is actually on.
- The root login setting shows the value sshd reported on a stock Ubuntu
  - OpenSSH 9.9 prints the deprecated spelling of exactly the value the distributions ship as their default, which the control had no entry for — so a security setting read as "not set" on a host where it was set correctly.
- The fail2ban allowlist is readable again
  - Fail2Ban 1.x draws its answer as a tree where 0.x printed a list, so a jail with one allowlisted address showed six entries beginning "These", and a jail with none showed five beginning "No". That is every fail2ban shipping today.
- A check that could not be answered says so, rather than reporting a reassuring zero
  - `last` and `lastb` come from util-linux-extra, which minimal cloud images leave out, so the failed-login count stayed at zero and the verdict called the host quiet. Zero attempts and "the tool that counts them is not installed" are the same number and opposite facts. The check is now listed as skipped, with the package named.
- A failed job reports the code the command exited with
  - The exit code was in every response and nothing ever assigned it, so every failure read "exit 0" — which next to "failed" is a contradiction the reader resolves by ignoring one of the two.

## 0.5.9 — 27 August 2026

**The parts of the last release that only worked on the machine they were written on**

Connecting a database installed on the server asked for nothing and then saved a connection that could not authenticate. The terminal's files and git panel needed tmux to know where the shell was, and said nothing on a host that has none — which is every stock Debian.

### Added

- The terminal says when tmux is missing, and what that costs
  - Windows, panes and a session that survives closing the tab are all tmux's. Without it the split button did nothing and the window strip stayed empty, with nothing on screen explaining why. Debian installs no tmux by default, so the page now says so and names the one command that fixes it.

### Fixed

- Connecting a database that is not in a container asks for its password, and dials before it saves
  - It used to hand you a connection string with the password missing, which could be saved as it stood — the result was a connection that existed, looked connected, and answered "password authentication failed for user postgres" to everything afterwards. Now nothing is stored unless the engine accepts it, and its refusal is shown next to the field that caused it. If the account has never had a password, the dialog names the one command that gives it one.
- The terminal's files and git panel works on a host without tmux
  - The panel is rooted at the shell's current directory, which was only ever read from tmux — so on a machine without it the panel had nowhere to look and stayed empty beside a shell sitting in a repository. The directory is read from the process itself when tmux cannot answer.

## 0.5.8 — 26 August 2026

**Sign in to GitHub from the page that does the pushing**

The git page can now hold a GitHub account: the same one-time code flow gh auth login uses, rendered as a screen. Commits are recorded as you, pushes are authenticated, and a pull request can be opened from the branch you are on. A database installed on the server itself is no longer invisible beside the ones in containers, and the commit box stopped disappearing under a long list of changes.

### Added

- Sign in to GitHub from the git page
  - The same device-code flow as `gh auth login`, as a screen rather than a series of prompts: a one-time code, github.com, done. The token is stored where gh keeps its own — under the account that owns the repository, which is the account that pushes — so it is the same credential a shell on the server would use. A pasted token is the way in for GitHub Enterprise or a machine account.
- Commits and pushes are made as the signed-in account
  - Signing in sets git's credential helper and, when the account has none, a committer name and address from your GitHub profile. The account chip in the header says whether git here will actually use it — including the case where the remote is SSH and the push uses the server's key instead.
- Open a pull request without leaving the page
  - A Pulls tab beside Changes and History: what is open, which one belongs to the branch you are on, and a form that pushes the branch and opens the request as your account.
- Databases installed on the server itself are found, not just the ones in containers
  - A Postgres, MySQL, MongoDB, Redis, ClickHouse or SQL Server that apt installed is recognised from the process listening for it, on whatever port it actually uses. The ones that ship with no credentials connect themselves; the rest say they are there and open the connection form with everything except the password already filled in — a container states its credentials in its environment, a native server keeps them in its own catalogue, and guessing at them would be an authentication failure in your own logs.

### Changed

- Pushing a new branch publishes it instead of refusing
  - A branch with no upstream used to be answered with git's advice to run a command in a terminal. It now sets the upstream, which is also what makes a pull request possible from a branch created here.

### Fixed

- The commit box stays visible however many files have changed
  - The changes list scrolls inside its own panel now. It used to push the commit box off the bottom of a page that does not scroll, which put committing out of reach exactly when there was most to commit.
- A host with no Docker socket can still find its databases
  - Detection answered “unavailable” for the whole question when Docker was missing, which on a plain VPS meant every database on it.
- The search button sits on the same line as every other icon in the collapsed sidebar
  - The rail is 3rem and its buttons are 2rem; the header kept a wider padding than the nav below it, so the one button in it overflowed half a rem to the right.

### Removed

- The “Amend last commit” checkbox on the git page

## 0.5.7 — 26 August 2026

**Selecting and copying in the terminal, as everywhere else**

Text dragged in a terminal pane unhighlighted itself the moment the mouse came up, and the Copy button and its shortcut both answered that nothing was selected. tmux owned the pointer; it now owns only the wheel.

### Added

- Ctrl+C copies the selection, and interrupts when there is nothing selected
  - Copying clears the selection as it goes, so the next Ctrl+C is an interrupt again. Ctrl+Shift+C still works and is still rebindable.
- Ctrl+V pastes
  - It used to send a literal ^V instead. The multi-line paste confirmation still sees it.

### Changed

- Hold Alt to hand the mouse to the program in the pane
  - For vim, htop, less and anything else that wants to be clicked. The wheel still scrolls the session's history with no modifier at all.

### Fixed

- A plain drag selects text, and it stays selected
  - tmux's mouse mode was taking the drag, drawing its own selection and clearing it on mouse-up — so the browser never had one, which is why every way of copying reported an empty selection.

## 0.5.6 — 26 August 2026

**Connect the database your application already uses**

A database on a Docker network with no published port — how nearly every application ships its own Postgres — was refused as unreachable. It was never unreachable: the dashboard shares the host's network namespace, and a Docker bridge is routable from there.

### Changed

- A published port is still preferred where there is one
  - It survives the container being recreated. A container address does not, so a connection made that way needs reconnecting after a redeploy — the Databases page marks which is which.

### Fixed

- A database with no published port now connects at its container address
  - The commonest database on any server this runs on was the one database the dashboard declined, while psql from the same machine worked fine.

## 0.5.5 — 26 August 2026

**The updater can build again**

Installing an update failed while building, with "the --mount option requires BuildKit". The updater's image was missing the buildx plugin, so it quietly built with the classic builder — which cannot read this project's own Dockerfile.

### Changed

- Builds are slower the first time and correct every time
  - The Go build cache mounts are gone, because they are a BuildKit extension the classic builder refuses outright rather than ignores.

### Fixed

- Installing an update no longer fails with "the --mount option requires BuildKit"
  - Compose delegates builds to buildx and falls back to the classic builder without saying so when it is absent. The image now carries buildx, and the Dockerfile no longer needs it — so an install already stuck on this can update its way out.

## 0.5.4 — 26 August 2026

**A taken port fails loudly**

A port already in use now stops the stack with a message naming it, instead of leaving the dashboard quietly serving whatever already held the port. Upgrading installs get their ports filled in automatically.

### Changed

- Re-running install.sh fills in ports missing from an older .env
  - No hand-editing to upgrade an install made before the ports were settings. A port already recorded is never moved — that was a deliberate choice, and guessing whether the process holding it is this dashboard is how a working install gets broken.

### Fixed

- A port already in use stops the stack instead of serving the wrong application
  - The frontend runs on its own network with its port published, so Docker refuses to start it and names the port. Previously it failed to bind, restarted in a loop, and the proxy forwarded to whatever already held the port.
- install.sh no longer exits early on an .env without the port settings
  - Reading an absent variable aborted the script under set -euo pipefail, so a re-run stopped before printing how to connect.

## 0.5.3 — 26 August 2026

**Say why a database was not connected**

A database running on this server that the dashboard recognises but cannot reach — almost always a container on a Docker network with no published port — used to be skipped in silence. The reconcile now names it and says what is in the way.

### Added

- The reason a database was not connected, on the Databases page
  - Most often that its port is published only inside its own Docker network. Publish it on 127.0.0.1 and it connects itself on the next visit.

### Fixed

- A database that cannot be reached is named instead of silently skipped
  - Its credentials were read and its container recognised, then it was dropped without a word — so the Databases page appeared to do nothing about a database sitting in plain sight on the Docker page.

## 0.5.2 — 26 August 2026

**Ports that do not collide**

The dashboard's three ports — 8443, 8080 and 3000 — are the three most contested numbers on a Linux server, and a machine already using one of them got a dashboard that silently served somebody else's application. All three are now settings, and the installer picks free ones.

### Added

- JD_PORT, JD_BACKEND_PORT and JD_FRONTEND_PORT
  - Change any of the three in .env. The compose file and the proxy read the same variables, so they cannot drift apart.

### Changed

- install.sh checks all three ports and picks free ones
  - It says which port was taken and what it used instead, and the connection details it prints at the end carry the ports it actually chose.
- `bun dev` follows JD_BACKEND_PORT
  - A developer who moved the backend off 8080 no longer has to find a second variable to keep the dev proxy working.

### Fixed

- A port already in use no longer serves you the wrong application
  - Only the frontend and backend failed to bind; the proxy came up clean and forwarded to whatever already held the port, so you reached another app over the dashboard's own certificate with nothing anywhere saying why.

## 0.5.1 — 26 August 2026

**Updates, in the dashboard**

Just Dashboard now tells you when a new version exists, shows you exactly what is in it, and installs it for you. Until now the only way to find out was to visit the repository, and the only way to upgrade was to ssh in and rebuild by hand.

### Added

- A notice above your account in the sidebar when a newer version is out
  - It carries the version, what the release is called, and two buttons: install it, or read what changed first. It is not there at all when you are up to date.
- Release notes for every version between the one you run and the newest
  - An install three versions behind is upgrading past three sets of changes, so all three are shown rather than only the last.
- One-click install: pull, rebuild and restart the whole stack
  - The upgrade runs in a separate container so it survives the dashboard being rebuilt underneath it, and it waits for the dashboard to answer again before calling itself finished.
- A live transcript of the upgrade, and the outcome once it is done
  - The page keeps watching while the backend restarts, so you see the build output rather than a spinner and a guess.
- The dashboard's own version and history on the Updates page, beside the host's packages
- JD_UPDATE_CHECK, JD_UPDATE_REPO, JD_UPDATE_BRANCH and JD_UPDATE_DIR
  - Turn the online check off entirely, follow a fork, follow a different branch, or name the directory you installed into.

### Changed

- Local changes in the install directory are kept, not discarded
  - The upgrade fast-forwards rather than resetting, so an edited compose file or Caddyfile survives — and when one genuinely collides, the upgrade stops and says what is in the way instead of deleting it.

## 0.5 — 22 August 2026

**One panel for one server**

Metrics with history, Docker, processes, logs, a real terminal, files, git, eight database engines, the reverse proxy, the firewall, backups and deploys — behind one login with mandatory two-factor and a network allowlist in front of it.

### Added

- Overview with recorded history, saturation signals and a health verdict
- Docker: containers, stacks, images, volumes, networks, events and a diagnosis for each container
- A persistent terminal with tmux windows, panes, folders and colours
- Databases for PostgreSQL, MySQL, MariaDB, SQLite, SQL Server, ClickHouse, Oracle, MongoDB and Redis
- Files, git, logs, processes, proxy and TLS, firewall, backups and deployments
- Capability-based roles, API tokens, an audit trail and twelve themes

