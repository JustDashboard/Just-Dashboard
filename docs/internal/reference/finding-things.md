# Finding things

This guide answers one question: *I need to change or understand X, where is it?* It is the short
way in. The [repository map](repository-map.md) says what every directory is for, and the
[documentation index](../README.md) lists the documents that hold the detail; this page tells you
which file to open first.

The codebase is large and flat. `backend/internal/api` holds every HTTP handler in one package,
`frontend/src/lib/types.ts` holds every API type in one file, and the browser specs sit in one
directory. What makes that workable is that the same area name runs through every layer, so most of
this page is the naming rules and the places where they break.

**Shorthand used in the tables.** To keep cells short, a path is written from the root its column
names, and a document is a link named for its file:

| Written as | Means |
| --- | --- |
| `app/…` | `frontend/src/app/(dashboard)/…` |
| `components/…`, `lib/…`, `hooks/…` | `frontend/src/components/…`, `frontend/src/lib/…`, `frontend/src/hooks/…` |
| `api/…` | `backend/internal/api/…` |
| a bare package name (`backups`) | `backend/internal/backups/` |
| a spec name (`backups-ui.spec.ts`) | `frontend/tests/browser/backups-ui.spec.ts` |

## One feature through every layer: Backups

Backups is a mid-sized feature that touches each layer once. Follow it top to bottom and you have
the route every other feature takes.

| Layer | File | What is there |
| --- | --- | --- |
| Sidebar entry | `frontend/src/components/nav.ts` | `NAV`, group "Protection": `{ title: "Backups", href: "/backups" }`. `SECTION_KEYS` in `frontend/src/lib/sidebar-sections.ts` is what lets an account hide it |
| Page route | `frontend/src/app/(dashboard)/backups/page.tsx` | The list page; `[job]/page.tsx` beside it is one job. A page file holds state, polling and composition |
| Components | `frontend/src/components/backups/` | `job-form.tsx`, `job-card.tsx`, `import-dialog.tsx`; pure logic as `import-model.ts` with `import-model.test.js` beside it |
| Types | `frontend/src/lib/types.ts` | `BackupJob`, `BackupResourceReport`: the API's shapes, written by hand |
| API call | `frontend/src/lib/api.ts` | `get<BackupJob[]>("/backups/")` inside `usePoll`; `post`, `put` and `del` for changes. Paths are relative to `/api/v1` |
| Route mount | `backend/internal/api/routes.go` | `Routes` calls `mountBackupRoutes`, which is at the top of `handlers_backups.go` |
| Capability | `backend/internal/api/handlers_backups.go` | Reads need a sign-in; changes are under `RequireCapability(auth.CapSystemAdmin)`; delete and restore are inside `s.destructive` |
| Handler | `backend/internal/api/handlers_backups.go` | `handleBackupJobList` and the rest; helpers that are not handlers are `backup_*.go` |
| Module wiring | `backend/internal/api/modules.go` | `initModules` builds `backupStore`, `backupRunner` and `backupSched`; `server.go` starts and stops the scheduler |
| Feature package | `backend/internal/backups/` | `store.go` (rows), `runner.go` (one run), `scheduler.go`, `restore.go`, `objectstore.go` and the other destinations |
| Store table | `backend/internal/store/store.go` | `CREATE TABLE IF NOT EXISTS backup_jobs`, and the columns added later in `addedColumns` |
| Go tests | `backend/internal/backups/` | `scheduler_test.go` beside `scheduler.go`; the handlers' tests are `api/backup_*_test.go` |
| Browser specs | `frontend/tests/browser/backups-ui.spec.ts` | Also `backup-import.spec.ts` and three more `backup-*.spec.ts`, against `workbench-fixture.ts` and `backup-import-fixture.ts` |
| Internal document | [`../backend/git-backups-users.md`](../backend/git-backups-users.md#backups) | Scheduling, destinations, encryption, restore, import |
| Public document | [`../../features/security-backups.md`](../../features/security-backups.md) | What an operator is told it does |

Two things Backups does not show. A feature with logic worth sharing has a module in `lib/`, such as
`frontend/src/lib/remote-desktop.ts`. A feature with live data reads a WebSocket through
`frontend/src/hooks/use-socket.ts` and `wsUrl` in `lib/api.ts`.

## Naming rules, and where they break

| Layer | Rule | Exceptions |
| --- | --- | --- |
| Page | `app/<section>/…/page.tsx`; the directory path is the URL | `(project)` under `app/deploy/[id]/` is a route group and is not in the URL. `login/`, `welcome/` and `hub/enter/` are outside `(dashboard)/` |
| Components | `components/<section>/`, named as the route's first segment | `/` → `overview/`, `processes` → `procs/`, `databases` → `database/`, `excalidraw` → `boards/`, `dashboard` → `update/`, `config/`, `account-link/`, `hub-link/` |
| Shared UI | `components/ui/` for low-level controls; single files at the top of `components/` for primitives (`panel.tsx`, `page.tsx`, `state.tsx`) | `components/workspace/` is shared page behaviour, not a section |
| Logic | `lib/<area>.ts` or `lib/<area>-<topic>.ts`, its test beside it as `.test.js` | Many areas have none and keep pure logic beside their components. Database modules are `lib/db-*.ts` and `lib/database-*.ts` |
| Types | The API's types are in `lib/types.ts`, one file | Some areas keep theirs beside the code: `lib/network-investigator-types.ts`, and `types.ts` files under `components/database/` and `components/logs/` |
| API path | The first segment under `/api/v1` is the section | See the path column of the next table: Processes, Security, Proxy and System users each use several |
| Handlers | `api/handlers_<area>*.go`; `mount<Area>Routes` is at the top of `handlers_<area>.go` | Databases are `handlers_db*.go`. Code that is not a handler drops the prefix: `database_*.go`, `deployment_*.go`, `boards_*.go`, `backup_*.go`, `map_*.go` |
| Package | One package per feature under `backend/internal/` | Named for the mechanism, not the page: Security is `netsec`, Packages is `updates`, System users is `linuxusers`. Excalidraw and the Map have no package; they live in `api/` |
| Go tests | `<file>_test.go` beside `<file>.go` | `*_live_test.go` needs a real engine or host and skips unless its environment variable is set; [`CONTRIBUTING.md`](../../../CONTRIBUTING.md) has the variables and the exceptions |
| Browser specs | `<area>-<topic>.spec.ts`; most sections have an `<area>-ui.spec.ts` for the page as a whole | `system-ui.spec.ts` is the Audit log, `dashboard-ui.spec.ts` is Settings, `services-ui.spec.ts`, `stacks-ui.spec.ts` and `container-page.spec.ts` are pages of Processes and Docker |
| Fixtures | `<area>-fixture.ts` beside the specs: one area's mocked API | `host-fixture.ts` and `workbench-fixture.ts` are shared; the proxy's tables are in `fixtures/proxy/` |
| Documents | A backend feature: a guide in [`../backend/`](../backend/README.md). A deployment topic: one in [`../deployments/`](../deployments/README.md). A page's UI: [`feature-map.md`](../frontend/feature-map.md) | Several features share one backend guide; the table below names it |

### File groups inside `api`

276 source files and their tests, in one package. The prefix says the area.

| Files | Count | Area |
| --- | --- | --- |
| `handlers_db*.go` | 40 | Databases |
| `handlers_deploy*.go` | 36 | Deployments |
| `handlers_network*.go` | 27 | Network |
| `handlers_proxy*.go` | 18 | Proxy; with `handlers_certificates*.go`, `handlers_tls*.go`, `handlers_ports*.go` |
| `handlers_assistant*.go` | 9 | The assistant |
| `handlers_git*.go` | 9 | Git (4) and GitHub (5, `handlers_github*.go`) |
| `handlers_mcp*.go` | 5 | The MCP endpoint and its keys |
| other `handlers_*.go` | 59 | One to four files per remaining area |
| `database_*.go` | 21 | Databases: inventory, metrics, alerts, point-in-time recovery, TLS, the pooler, upgrades |
| `deployment_*.go`, `deploy_*.go` | 17 | Deployments: joins with other features, deploy keys, the OpenAPI description |
| `boards_*.go`, `map_*.go` | 9 | Excalidraw and the server map, which have no package of their own |
| `routes.go`, `route_methods.go`, `server.go`, `modules.go`, `modules_proxy.go` | 5 | The router, the `Server`, and the module set |

### File groups inside the four largest feature packages

Counts are source files; the tests sit beside them. A group named `detect` is `detect.go` and every
`detect_*.go`; a name with a `*` is a plain wildcard. Files outside a group are named for the one
thing they hold.

**`deploy`** (247 source files), described in
[`implementation.md`](../deployments/implementation.md):

| Group | Count | What it holds |
| --- | --- | --- |
| `detect`, `detector` | 51 | Reading a checkout: languages, frameworks, start command, databases, variables |
| `build` | 28 | Build recipes per language, and reading a failed build's output |
| `preflight` | 20 | What is checked about a plan before a run starts |
| `frameworks` | 16 | Per-ecosystem framework knowledge the detector and recipes read |
| `run` | 8 | A run's history, logs, metrics, transcript and diagnosis |
| `preview` | 8 | Pull request preview environments |
| `runtime` | 7 | A release's container: flags, replicas, listeners, concurrency |
| `orchestration` | 6 | The persistent engine: model, queue, steps, events, store |
| `source`, `release`, `planning` | 15 | Fetching a source, immutable releases, drafts and plans |

**`dbx`** (174 source files), described in
[`databases.md`](../backend/databases.md):

| Group | Count | What it holds |
| --- | --- | --- |
| `orm` | 22 | Schema generation for each ORM |
| `mongo`, `redis` | 27 | The MongoDB and Redis surfaces |
| `dump` | 11 | Dump and restore, per engine |
| `ops` | 9 | Watching a server: activity and health, per engine |
| `dialect`, `catalog` | 16 | One file per SQL engine: its dialect and its catalogue queries |
| `seed`, `import` | 11 | Mock data and importing files |
| `admin` | 6 | The server behind a connection: roles, grants, settings |
| `discover` | 5 | Finding the databases on the machine |
| `statement`, `statements` | 7 | Splitting and classifying SQL (`statement`), statement statistics (`statements`) |
| `*_capabilities.go` | 18 | What each engine supports, one file per feature; they cut across the groups above |

**`proxysvc`** (142 source files), described in
[`proxy.md`](../backend/proxy.md):

| Group | Count | What it holds |
| --- | --- | --- |
| `sites`, `site` | 26 | nginx sites: parse, render and apply (`sites`); one site's logs, drift, trial (`site`) |
| `deployment` | 10 | The routes, certificates and edge rules a deployment asks for |
| `docker_caddy*` | 8 | The Docker Caddy ingress |
| `stream` | 8 | TCP and UDP streams |
| `tls*` | 16 | The TLS report: scans (`tlsscan`), deep checks, monitoring |
| `certbot`, `cert` | 9 | Issuing and renewing; reading certificate files |
| `ports` | 5 | Listeners and who owns them |

**`netx`** (131 source files), described in [`network.md`](../backend/network.md):

| Group | Count | What it holds |
| --- | --- | --- |
| `native` | 21 | Editing the host's own network manager profiles |
| `dns` | 13 | The resolver chain and DNS evidence |
| `wg*`, `wireguard` | 11 | WireGuard: record, keys, peers, sites, kill switch |
| `shaping` | 8 | Queue disciplines and SQM |
| `gateway` | 8 | The gateway table: NAT and forwarding |
| `egress` | 8 | Egress groups and failover |
| `drift` | 6 | Rendered state against runtime state |
| `route`, `routes` | 6 | Routes, rules and the routing model |
| `protection` | 5 | Kernel protections and limits |

## From a sidebar page to its code

The rows follow `NAV` in `frontend/src/components/nav.ts`, group by group. "Path" is the first
segment under `/api/v1`.

| Page | `app/` | `components/` | Path → `api/` files | Packages | Specs | Document |
| --- | --- | --- | --- | --- | --- | --- |
| **Server** | | | | | | |
| Overview | `page.tsx` | `overview/` | `/system` → `handlers_system.go`; `/dashboard/health` → `handlers_self.go`; and one read from most sections | `sysinfo`, `metrics`, `storehealth` | `overview-ui.spec.ts` | [`feature-map.md`](../frontend/feature-map.md) |
| Map | `map/` | `map/` | `/map` → `handlers_map.go`, `map_*.go` | none: lives in `api/` | `server-map.spec.ts` | [`server-map.md`](../server-map.md) |
| Assistant | `assistant/` | `assistant/` | `/assistant` → `handlers_assistant*.go` | `assistant` | `assistant.spec.ts` | [`operator.md`](../deployments/operator.md) |
| Metrics | `metrics/` | `metrics/` | `/system` → `handlers_system.go`, `handlers_capacity.go` | `metrics`, `sysinfo` | `metrics-ui.spec.ts` | [`observability-security.md`](../backend/observability-security.md) |
| Alerts | `metrics/alerts/` | `metrics/`, `alerts/` | `/system/alerts` → `handlers_host_alerts.go`, `host_alert*.go` | `metrics` | `host-alerts.spec.ts` | [`observability-security.md`](../backend/observability-security.md) |
| Processes | `processes/` | `procs/` | `/processes`, `/pm2`, `/systemd`, `/cron` → `handlers_procs*.go` | `procs` | `processes-ui.spec.ts`, `services-ui.spec.ts` | [`processes-terminal-github.md`](../backend/processes-terminal-github.md) |
| Logs | `logs/` | `logs/` | `/logs` → `handlers_logs*.go` | `logsx` | `logs-*.spec.ts` | [`docker-files-logs.md`](../backend/docker-files-logs.md) |
| **Apps** | | | | | | |
| Deployments | `deploy/` | `deploy/` | `/deploy` → `handlers_deploy*.go`, `deployment_*.go` | `deploy`, `blueprint`, and the ones below | `deploy-*.spec.ts` | [the deployment guides](../deployments/README.md), [`implementation.md`](../deployments/implementation.md) |
| Databases | `databases/` | `database/` | `/databases` → `handlers_db*.go`, `database_*.go` | `dbx`, `dbtls` | `database-*.spec.ts` | [`databases.md`](../backend/databases.md) |
| Docker | `docker/` | `docker/` | `/docker` → `handlers_docker*.go`, `docker_*.go` | `dockerx`, `imagescan` | `docker-*.spec.ts`, `stacks-ui.spec.ts`, `stack-detail.spec.ts`, `container-page.spec.ts` | [`docker-files-logs.md`](../backend/docker-files-logs.md) |
| **Workspace** | | | | | | |
| Excalidraw | `excalidraw/` | `boards/` | `/boards` → `handlers_boards.go`, `boards_*.go` | none: lives in `api/` | `boards-ui.spec.ts` | [`boards.md`](../boards.md) |
| Terminal | `terminal/` | `terminal/` | `/terminal` → `handlers_terminal*.go` | `term`, `ptyhold` | `terminal-*.spec.ts` | [`processes-terminal-github.md`](../backend/processes-terminal-github.md), [`features-terminal.md`](../frontend/features-terminal.md) |
| Files | `files/` | `files/` | `/files` → `handlers_files*.go` | `files`, `safepath`, `contentguard` | `files-ui.spec.ts` | [`docker-files-logs.md`](../backend/docker-files-logs.md) |
| Git | `git/` | `git/` | `/git` → `handlers_git*.go`, `handlers_github*.go` | `gitx`, `ghx`, `forgex` | `git-*.spec.ts` | [`git-backups-users.md`](../backend/git-backups-users.md), [`git-workspace-expansion.md`](../backend/git-workspace-expansion.md) |
| Remote Desktop | `remote-desktop/` | `remote-desktop/` | `/remote-desktop` → `handlers_remote_desktop*.go` | `remotedesk` | `remote-desktop.spec.ts` | [`remote-desktop.md`](../backend/remote-desktop.md) |
| **Network** | | | | | | |
| Network (19 pages) | `network/` | `network/` | `/network` → `handlers_network*.go` | `netx`, and the ones below | `network-*.spec.ts` | [`network.md`](../backend/network.md) |
| … Firewall, Connections | `network/firewall/`, `network/connections/` | `network/`, `security/` | `/firewall` → `handlers_netsec.go`, `handlers_firewall.go`; `/connections` → `handlers_security.go`, `handlers_connections.go` | `netsec` | `network-routing-firewall.spec.ts` | [`observability-security.md`](../backend/observability-security.md) |
| Proxy & TLS | `proxy/` | `proxy/` | `/proxy`, `/certificates`, `/ports` → `handlers_proxy*.go`, `handlers_certificates*.go`, `handlers_tls*.go`, `handlers_ports*.go` | `proxysvc`, `cftunnel`, `accesslog` | `proxy-*.spec.ts` | [`proxy.md`](../backend/proxy.md) |
| **Protection** | | | | | | |
| Security | `security/` | `security/` | `/security`, `/ssh`, `/ssh-sessions`, `/fail2ban`, `/logins` → `handlers_security*.go`, `handlers_netsec.go` | `netsec` | `security-*.spec.ts` | [`observability-security.md`](../backend/observability-security.md) |
| Backups | `backups/` | `backups/` | `/backups` → `handlers_backups.go`, `backup_*.go` | `backups` | `backups-ui.spec.ts`, `backup-*.spec.ts` | [`git-backups-users.md`](../backend/git-backups-users.md) |
| **Advanced** | | | | | | |
| Packages | `packages/` | `packages/` | `/packages` → `handlers_packages.go` | `updates` | `packages-ui.spec.ts`, `package-page.spec.ts` | [`observability-security.md`](../backend/observability-security.md) |
| System users | `system-users/` | `system-users/` | `/system-users`, `/system-accounts` → `handlers_linuxusers.go` | `linuxusers` | `system-users.spec.ts` | [`git-backups-users.md`](../backend/git-backups-users.md) |
| Audit log | `audit/` | `audit/` | `/audit` → mounted in `routes.go`, handled in `handlers_auth.go` | `audit` | `system-ui.spec.ts` | [`runtime-boundaries.md`](../architecture/runtime-boundaries.md) |
| **System** | | | | | | |
| Settings → Version | `dashboard/` | `update/` | `/dashboard` → `handlers_selfupdate.go`, which mounts everything under `/dashboard` | `selfupdate`, `version` | `dashboard-ui.spec.ts` | [`platform.md`](../backend/platform.md) |
| Settings → Configuration | `dashboard/configuration/` | `config/` | `/dashboard` → `handlers_selfconfig.go`, `handlers_uninstall.go` | `selfcfg`, `uninstall`, `stackports` | `dashboard-ui.spec.ts` | [`platform.md`](../backend/platform.md) |
| Settings → Connect | `dashboard/link/` | `account-link/` | `/dashboard/link` → `handlers_accountlink.go` | `accountlink` | `account-link.spec.ts` | [`account-link.md`](../backend/account-link.md) |
| Settings → Hub | `dashboard/hub/` | `hub-link/` | `/dashboard/hub` → `handlers_hublink.go`, `handlers_hubsignin.go` | `hublink` | `hub-link.spec.ts`, `hub-signin.spec.ts` | [`hub-link.md`](../backend/hub-link.md) |
| **Account** (footer menu) | | | | | | |
| Profile, Security, Sessions, Workspace, Users | `account/` | `account/` | `/account`, `/auth`, `/dashboard-users` → mounted in `routes.go`; `handlers_auth.go`, `handlers_account.go`, `handlers_passkeys.go` | `auth` | `account-ui.spec.ts`, `account-passkeys.spec.ts` | [`runtime-boundaries.md`](../architecture/runtime-boundaries.md) |
| API keys | `account/keys/` | `account/` | `/tokens` → mounted in `routes.go`; `handlers_auth.go`, `handlers_tokens.go`, `handlers_mcp_keys.go`, `deploy_keys.go` | `auth` | `api-keys.spec.ts` | [`invariants.md`](../security/invariants.md) |
| **Outside the shell** | | | | | | |
| Sign-in | `frontend/src/app/login/` | the page file itself, with `onboarding/` for the picture beside the form | `/auth` → `handlers_auth.go`, `handlers_passkeys.go` | `auth` | `sign-in.spec.ts`, `auth-recovery.spec.ts` | [`shell-design.md`](../frontend/shell-design.md) |
| First-run question | `frontend/src/app/welcome/` | `onboarding/` | `/account/workspace` → `handlers_account.go` | `auth` | `onboarding.spec.ts` | [`shell-design.md`](../frontend/shell-design.md) |

One database's own pages (`app/databases/[id]/…`) and one project's (`app/deploy/[id]/(project)/…`)
are not in `NAV`: `components/database/shell/nav-scope.tsx` builds the first from the engine, and
`PROJECT_NAV` and `PROJECT_SETTINGS_NAV` in `nav.ts` list the second.

**Packages behind Deployments and Network.** Both sections reach more packages than fit a cell.

| Section | Package | For |
| --- | --- | --- |
| Deployments | `blueprint`, `gameserver` | Templates and game servers |
| | `githubapp`, `dnsrecords`, `cftunnel`, `tailnetgate` | The GitHub App, DNS records, Cloudflare Tunnel, tailnet-only access |
| | `secretstore`, `logdrain`, `accesslog`, `imagescan` | Secret stores, log drains, request records, image scans |
| | `proxysvc` (`deployment_*.go`), `dockerx`, `backups` | Routes, containers and backup coverage, each done by its owner |
| Network | `netsec` | Firewall, connections, diagnostic tools |
| | `netdiag`, `netcapture`, `netpath`, `netvantage` | Saved runs, packet captures, connection path, external checks |
| | `netflows`, `netipam`, `dnsservice` | Socket history, address planning, DNS services |

## Recipes

Each is a checklist. The linked document owns the rule; this only says which files move.

**Add a route.** [`../architecture/request-lifecycle.md`](../architecture/request-lifecycle.md)

1. Mount it in the area's `mount<Area>Routes` (`api/handlers_<area>.go`) with `r.Method(…,
   s.handle(…))`, inside the group whose capability it needs.
2. Write the handler beside it: decode with `httpx.DecodeJSON`, name the audit action with
   `httpx.SetAudit`, return `httpx` errors.
3. Add the response type to `lib/types.ts` and call it through `lib/api.ts`.
4. A route under `/deploy` also needs a line in `deployOperations` (`api/deploy_openapi.go`) and
   the regenerated description and clients: [`../backend/deploy-api.md`](../backend/deploy-api.md).
5. Document it in the feature's backend document.

**Add a page.** [`../frontend/shell-design.md`](../frontend/shell-design.md),
[`../frontend/design-system.md`](../frontend/design-system.md)

1. Create `app/<section>/<page>/page.tsx` and its panels under `components/<section>/`.
2. Add it to `NAV` in `components/nav.ts`, with `capability` if the backend restricts it. A new
   section also goes in `SECTION_KEYS` (`lib/sidebar-sections.ts`).
3. Decide the page's register first ("16. Two registers" in the design system), then build from
   the primitives.
4. Add a spec and, if the area has none, a fixture; add its section to
   [`../frontend/feature-map.md`](../frontend/feature-map.md) and its row to the table above.

**Add a setting.** [`../../guides/configuration.md`](../../guides/configuration.md)

1. Read it in `Load` (`backend/internal/config/config.go`) with a default and bounds; the
   restrictive value is the default.
2. Pass it to the backend service in `docker-compose.yml` and describe it in `.env.example`.
3. If Settings → Configuration should edit it, add it to `backend/internal/selfcfg/settings.go`.
4. Document it in `docs/guides/configuration.md`.

**Add a store column.** [`../security/invariants.md`](../security/invariants.md)

1. Add the column to the `CREATE TABLE` in `backend/internal/store/store.go` (or the feature's
   schema file beside it), so a new install has it.
2. Add an entry to `addedColumns` with a `DEFAULT`, so an existing install gains it. Never remove
   or edit a shipped entry: the list is the path from every released schema.
3. Add a migration test beside the others in `backend/internal/store/`.

**Add a module** (a feature package the API calls).
[`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md)

1. Create `backend/internal/<feature>/` with a package comment saying what it owns.
2. Add its field to `moduleSet` and build it in `initModules` (`api/modules.go`). A module that
   cannot start on this host must leave the dashboard running and fail only its own routes.
3. Start and stop background work in `Server.Start` and the shutdown path in `api/server.go`,
   through `safego`.
4. Add the package's row to [`repository-map.md`](repository-map.md).

**Add a destructive action.**
[Invariant 3](../security/invariants.md#invariant-3-which-routes-take-a-typed-phrase)

1. Mount the route inside `s.destructive(r, …)` (`api/routes.go` defines it): destructive
   capability, the tighter rate budget, audit.
2. Only the deletions that invariant lists take a typed phrase; those call
   `httpx.RequireTypedConfirmation` inside the handler.
3. On the page, ask through `useConfirm` (`components/confirm-dialog.tsx`).

### Where each security boundary is checked

Read [`../security/invariants.md`](../security/invariants.md) before changing any of these.

| Boundary | Enforced in | Mounted or called from |
| --- | --- | --- |
| Network allowlist | `AllowlistCIDRs` in `backend/internal/httpx/middleware.go` | First middleware of `/api/v1` in `api/routes.go`; `JD_ALLOWED_CIDRS` is read in `config/config.go` |
| Authentication | `Authenticator` in `backend/internal/httpx/authmw.go`; sessions and keys in `backend/internal/auth/` | Each group in `api/routes.go` picks its `Authenticate…` method |
| Two-factor | `AuthenticatePartial` in `httpx/authmw.go`; codes in `backend/internal/auth/totp.go` | The half-authenticated group in `api/routes.go` |
| Capability | `RequireCapability` in `httpx/authmw.go`; roles in `backend/internal/auth/roles.go` | Each `mount…Routes`. Checks that depend on the request's content are listed in [`request-lifecycle.md`](../architecture/request-lifecycle.md) |
| Destructive actions | `s.destructive` in `api/routes.go`; `backend/internal/httpx/confirm.go` for the typed phrase | Around the route at its mount |
| CSRF | `RequireCSRF` in `backend/internal/httpx/csrf.go` | The groups in `api/routes.go` a session cookie reaches; the stream bridge asks for a same-site Origin instead |
| Audit | `AuditMutations` in `backend/internal/httpx/auditmw.go`; rows in `backend/internal/audit/audit.go` | Each group in `api/routes.go` that takes changes; the handler names the action with `httpx.SetAudit` |
| Client paths | `Resolve` and `ResolveEntry` in `backend/internal/files/files.go`; archives in `backend/internal/safepath/` | Every handler that takes a path |
| Host commands | `Command` and `CommandOnHost` in `backend/internal/hostexec/hostexec.go` | Feature packages, with explicit argv |
| Deploy keys | `deployKeyGate` and `deployKeyRoutes` in `api/deploy_keys.go` | The feature group in `api/routes.go` |
| Hub sessions | `RefuseHubSession` in `httpx/authmw.go` | On each credential and link route |
| Schema | `addedColumns` in `backend/internal/store/store.go` | `Open` applies it at boot |

## Things that are not features

| Thing | Where | Document |
| --- | --- | --- |
| The stack | `docker-compose.yml`, `deploy/Caddyfile`, `deploy/proxy-entrypoint.sh`, `backend/Dockerfile`, `frontend/Dockerfile` | [`implementation.md`](../deployments/implementation.md), [`overview.md`](../overview.md) |
| Installer | `install.sh`, and the helpers it sources: `scripts/preflight.sh`, `scripts/install-dependencies.sh`, `scripts/dotenv.sh`, `scripts/password.sh` | [`terminal-tools.md`](../operations/terminal-tools.md), [`install.md`](../../guides/install.md) |
| Operator's terminal tools | `scripts/manage.sh`; its account and link commands run `backend/internal/admin/` | [`terminal-tools.md`](../operations/terminal-tools.md) |
| Release | `scripts/release.sh`, `backend/internal/selfupdate/changelog.json`, `backend/scripts/gen-changelog.go`, `backend/internal/version/`, `.github/workflows/release.yml` | [`platform.md`](../backend/platform.md) |
| Self-update | `backend/internal/selfupdate/`; the `-self-update` mode in `backend/cmd/server/main.go` | [`platform.md`](../backend/platform.md) |
| Local checks and CI | `scripts/test-changed.sh`, `scripts/ci-plan.py`, `.github/workflows/verify.yml` | [`AGENTS.md`](../../../AGENTS.md#required-checks), [`CONTRIBUTING.md`](../../../CONTRIBUTING.md) |
| The `jd` command line | `backend/cmd/jd/`; `backend/internal/clihost/` keeps the server's copy current; `api/handlers_cli.go` serves it | [`cli.md`](../../guides/cli.md) |
| OpenAPI description | Generated by `api/deploy_openapi.go`, committed as `docs/api/deploy-openapi.json` | [`deploy-api.md`](../backend/deploy-api.md) |
| SDKs | `sdk/go/`, `sdk/typescript/`, generated by `backend/internal/openapigen/` | [`deploy-api.md`](../backend/deploy-api.md), [`sdk/README.md`](../../../sdk/README.md) |
| MCP server | `backend/internal/mcp/` (the protocol), `api/handlers_mcp*.go` (the endpoint, tools and keys) | [`databases.md`](../backend/databases.md) |
| Assistant | `backend/internal/assistant/`, `api/handlers_assistant*.go`, `components/assistant/` | [`operator.md`](../deployments/operator.md) |
| Go test support | `testdata/` in a package; `backend/internal/store/storetest/`, `backend/internal/auth/passkeytest/`, `backend/internal/proxysvc/acmetest/` | [`overview.md`](../overview.md) |
| Browser fixtures | `frontend/tests/browser/*-fixture.ts`, `frontend/tests/browser/fixtures/` | [`overview.md`](../overview.md) |
| Design-system lint | `frontend/eslint-rules/design-system.mjs`, turned on in `frontend/eslint.config.mjs`; `design-system.spec.ts` checks the rendered pages | [`design-system.md`](../frontend/design-system.md) |
| Shell and navigation | `components/app-sidebar.tsx`, `components/nav.ts`, `components/command-palette.tsx`, `app/layout.tsx` | [`shell-design.md`](../frontend/shell-design.md) |
| Polling and sockets | `hooks/use-poll.ts`, `hooks/use-socket.ts`, `lib/api.ts` | [`data-theming.md`](../frontend/data-theming.md) |

## Keeping this page true

Add a row when a sidebar page, a handler prefix or a package appears. The counts in the file-group
tables are a snapshot of 0.8.6; correct them when a group changes by more than a few files, and
derive them from a directory listing, not from memory.
