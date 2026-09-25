# Repository map

This is the coverage checklist for the codebase-level guides. It maps every maintained source area to its
owner and detailed internal document; source files remain authoritative for exported APIs and individual
fields.

## Root, build, and operations

| Area | Responsibility | Detailed reference |
| --- | --- | --- |
| `docker-compose.yml`, `deploy/Caddyfile`, `deploy/proxy-entrypoint.sh` | Single-host production topology, loopback-only services, the Caddy listener and its three TLS modes, mounts, and health checks | [`../deployments/implementation.md`](../deployments/implementation.md#deployment-topology) |
| `install.sh`, `.env.example` | Installation: two reachability routes (Tailscale, SSH tunnel), certificate issuance, randomised internal ports, secrets, and operator configuration | [`../overview.md`](../overview.md), public [`../../../README.md`](../../../README.md) |
| `scripts/release.sh`, `backend/scripts/` | Version update, generated changelog, build verification, and release commit preparation | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#cutting-a-release) |
| `AGENTS.md`, `CONTRIBUTING.md` | Mandatory contributor workflow, security baseline, licensing, and the validation gate | [`../contributing/conventions.md`](../contributing/conventions.md) |
| `scripts/test-changed.sh` | The local validation gate: only the checks, Go tests and browser specs the diff reaches | [`../../../AGENTS.md`](../../../AGENTS.md#required-checks) |
| `docs/plans/0.6.7-deployments/` (absent) | Historical references only; no frozen-contract or checkpoint evidence available in this checkout | [`../deployments/implementation.md`](../deployments/implementation.md) |

## Backend entry point and packages

`backend/cmd/server` loads configuration, opens the store, creates the API server, starts background
services, handles signals, and supports the isolated self-update worker mode. `backend/cmd/terminal-holder`
is the terminal holder (`ptyhold`), shipped beside it in the image and copied to the data directory
for the host to run. The 29 packages under
`backend/internal/` are:

| Package | Responsibility | Detailed reference |
| --- | --- | --- |
| `agent` | Agent identity, certificates, and hub-facing mode | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#streaming-jobs-secrets-agent-mode) |
| `api` | Route map, middleware composition, handlers, module wiring, audit, and feature joins | [`../architecture/request-lifecycle.md`](../architecture/request-lifecycle.md) |
| `audit` | Durable and process-log mutation audit records | [`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md#auth-secrets-state) |
| `auth` | Passwords, TOTP enrolment and policy, recovery codes, sessions, roles/capabilities, and API tokens | [`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md#auth-secrets-state) |
| `backups` | Backup definitions/runs, scheduler, object stores, retention by count and age, container pausing, archive listing, subset and in-place restore, and artifact download | [`../backend/git-backups-users.md`](../backend/git-backups-users.md#backups) |
| `config` | Environment parsing, defaults, bounds, legacy aliases, and network safety validation | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#configuration-version-release-self-update) |
| `dbx` | SQL and NoSQL connections, classification, browsing, DDL, query, import/export, and dumps | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#databases-eight-engines-one-shape) |
| `deploy` | Legacy compatibility plus normalized planning, artifacts, orchestration, activation, recovery, configuration, and automation | [`../deployments/implementation.md`](../deployments/implementation.md) |
| `dockerx` | Docker SDK, container/image/network/volume operations, compose state, builds, stats, events, scans, diagnosis, and the derived layer — attention, exposure, image references, writable-layer analysis, failure diagnosis, anomalies, deploy preview, deployment history, cleanup | [`../backend/docker-files-logs.md`](../backend/docker-files-logs.md#docker) |
| `files` | Root-contained browse/write/search/archive/preview/place operations | [`../backend/docker-files-logs.md`](../backend/docker-files-logs.md#files) |
| `ghx` | GitHub device login, pull requests (list, diffs, conversations, create, review, merge pinned to a head, check out, by checkout or by `--repo` name), check runs and statuses, issues and comments, and workflow job/step logs | [`../backend/processes-terminal-github.md`](../backend/processes-terminal-github.md#github-sign-in) |
| `forgex` | Encrypted per-checkout GitLab/Gitea accounts, request lists/diffs/conversations, creation, reviews and merging | [`../backend/git-workspace-expansion.md`](../backend/git-workspace-expansion.md#provider-accounts) |
| `gitx` | Repository discovery/status, graph, branches, tags, stashes, remotes, commit detail, comparison, diffs, ownership, and mutations including clone and init | [`../backend/git-backups-users.md`](../backend/git-backups-users.md#git-working-copies) |
| `hostexec` | Explicit-argv local/host namespace command execution and owner dropping | [`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md#reaching-the-host-and-containing-paths) |
| `httpx` | HTTP errors/JSON, identity context, CSRF, allowlist, auth, capabilities, limits, audit, and confirmation | [`../architecture/request-lifecycle.md`](../architecture/request-lifecycle.md) |
| `jobs` | Bounded long-running operations observed by id | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#streaming-jobs-secrets-agent-mode) |
| `linuxusers` | Host accounts, groups, lock state, login metadata, and authorized SSH keys | [`../backend/git-backups-users.md`](../backend/git-backups-users.md#host-users-and-ssh-keys) |
| `logsx` | Source discovery, filtering, history search, and live tails | [`../backend/docker-files-logs.md`](../backend/docker-files-logs.md#logs) |
| `metrics` | Persistent host/container samples, history, events, and health assessment | [`../backend/observability-security.md`](../backend/observability-security.md#metrics-saturation-health) |
| `netsec` | Exposure, posture, listeners, sessions/logins, firewall, fail2ban, sshd, and diagnostic probes | [`../backend/observability-security.md`](../backend/observability-security.md) |
| `procs` | Process inventory, signals, PM2, systemd, and cron | [`../backend/processes-terminal-github.md`](../backend/processes-terminal-github.md#processes) |
| `ptyhold` | The terminal holder: owns one session's PTY on the host, keeps its recent output, and hands the master to the dashboard over a unix socket so sessions outlive dashboard restarts; built as its own binary, `cmd/terminal-holder` | [`../backend/processes-terminal-github.md`](../backend/processes-terminal-github.md#sessions-outlive-the-dashboard) |
| `proxysvc` | nginx sites/streams, certificates, DNS/TLS checks, ports, htpasswd, and deployment routes | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#proxy) |
| `safepath` | Symlink-safe archive extraction boundary | [`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md#reaching-the-host-and-containing-paths) |
| `selfcfg` | The dashboard's own settings: `.env` reading/writing, validation, restart and rebuild in a sibling container with automatic rollback, Tailscale certificate issuance/renewal, and `tailscale serve` publication of preview environments on the node's ports 21000–21999 (`tailscale_serve.go`) | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#the-dashboards-own-settings) |
| `selfupdate` | Release checks, changelog, installer state, reconciliation, and updater | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#configuration-version-release-self-update) |
| `store` | SQLite schema, additive columns, deployment migration, and connection lifecycle | [`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md#auth-secrets-state) |
| `sysinfo` | Host metrics, disk/device statistics, pressure, sockets, and capacity | [`../backend/observability-security.md`](../backend/observability-security.md#metrics-saturation-health) |
| `term` | Direct PTY sessions, holding and adopting them through `ptyhold`, replay, organization, clipboard uploads, and bundled shell setup | [`../backend/processes-terminal-github.md`](../backend/processes-terminal-github.md#the-terminal) |
| `updates` | Six package-manager adapters, catalogue, upgrades, reboot state, and usage summaries | [`../backend/observability-security.md`](../backend/observability-security.md#packages-six-managers-one-interface) |
| `version` | Build/release version normalization | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#configuration-version-release-self-update) |
| `wsx` | WebSocket origin validation, upgrade, and shared socket behavior | [`../architecture/request-lifecycle.md`](../architecture/request-lifecycle.md) |

## Frontend

| Area | Responsibility | Detailed reference |
| --- | --- | --- |
| `src/app/` | App Router layouts plus 85 page entry points across account, dashboard, audit, backups, boards, databases, deployments, Docker, files, Git, logs, metrics, packages, processes, proxy, security, users, terminal, and login | [`../frontend/feature-map.md`](../frontend/feature-map.md) |
| `src/components/boards/`, `src/lib/boards.ts` | Embedded Excalidraw workspace, save state and server resource cards, plus board types | [`../boards.md`](../boards.md) |
| `src/components/ui/` | Low-level accessible controls; project composition lives above this layer | [`../frontend/shell-design.md`](../frontend/shell-design.md#the-design-system) |
| `src/components/{database,deploy,docker,files,git,logs,metrics,packages,procs,proxy,security,terminal,update}/` | Feature panels, forms, tables, dialogs, visualizations, and workspaces. `deploy/` also holds the section's shared decisions and drawings beside its pages: what a project is and what it is drawn as (`vocabulary.tsx`, `project-mark.tsx`, `service-product.ts`), its verbs and a run's (`project-verbs.tsx`, `run-verbs.tsx`), a pull request's from a project (`pull-request-verbs.tsx`, `pull-request-picker.tsx`), a run as a row and its starter as a mark (`run-row.tsx`, `run-marks.tsx`), the fleet's decisions and card (`fleet.ts`, `fleet-card.tsx`), live usage (`usage-tiles.tsx`), a request's parts and the traffic drawings (`request-marks.tsx`, `latency-ladder.tsx`, `traffic-strip.tsx`, `add-alert-sheet.tsx`), the HTTPS field toggle (`https-toggle.tsx`), the line only a game server can draw (`game/identity.tsx`), the Overview's placeholder (`overview-skeleton.tsx`), and the settings frame (`settings/setting-card.tsx`, `setting-picture.tsx`, `segments.tsx`, `dotenv.ts`, `use-column-width.ts`, `automation/use-automation.ts`, `automation/wiring.tsx`, `automation/marks.tsx`) | [`../frontend/features-terminal.md`](../frontend/features-terminal.md); `deploy/` in [`../frontend/feature-map.md`](../frontend/feature-map.md) and [`../frontend/design-system.md`](../frontend/design-system.md) |
| `src/components/` top level | Shell, sidebar, command palette, page/panel/state primitives, editors, icons, and shared confirmations, plus the drawings more than one section shares: the last attempts at something as a strip (`outcome-strip.tsx`), a schedule built from words (`schedule-builder.tsx`, Backups' and a deployment's), a transcript's lines and their drip (`transcript-line.tsx`, the dashboard's own runs and a deployment's build), and a client drawn as itself (`client-mark.tsx`, a session's and a request's) | [`../frontend/shell-design.md`](../frontend/shell-design.md), [`../frontend/design-system.md`](../frontend/design-system.md) |
| `src/hooks/` | Auth, polling, WebSockets, metrics history/windows, self-update, keyboard, theme, responsive state, and which rows of a polled list just arrived (`use-arrivals.ts`) | [`../frontend/data-theming.md`](../frontend/data-theming.md) |
| `src/lib/` | Typed API URLs/requests, view stores, formatting, themes, terminal behavior, metrics transforms, Docker templates, what a pasted token or key says about itself (`secrets.ts`), what a pull request and its preview say about themselves (`pull-requests.ts`), and pure helpers | [`../frontend/data-theming.md`](../frontend/data-theming.md) |
| `public/logos/` | Bundled product logos for the template catalogue, database engines, containers, images and stacks, and for the deployment section's families — frameworks and recipes, Git hosts and registries, notification services, crawlers and referrers, the services an environment variable names — mapped by `components/product-logo.tsx`; `NOTICE` carries each file's source, licence (Apache-2.0, MIT, CC0) and any recolouring | [`../frontend/design-system.md`](../frontend/design-system.md) §14 |
| `src/proxy.ts`, `next.config.ts` | CSP nonce/policy and development API rewrite | [`../frontend/shell-design.md`](../frontend/shell-design.md), [`../overview.md`](../overview.md) |
| `scripts/sync-monaco.mjs` | Copies the pinned Monaco worker/editor assets into the build output | [`../frontend/shell-design.md`](../frontend/shell-design.md#the-editor-is-served-from-here-not-from-a-cdn) |
| `scripts/sync-excalidraw.mjs` | Copies the pinned Excalidraw fonts into `public/excalidraw` for local serving | [`../boards.md`](../boards.md) |
| `tests/browser/`, `playwright.config.ts` | Chromium release journey gate and opt-in cross-browser evidence | [`../overview.md`](../overview.md) |
| `package.json`, `bun.lock`, build configs | Bun-only dependency, lint, type/build, and browser-test toolchain | [`../overview.md`](../overview.md) |

## Updating this map

Add a row when a new backend package, frontend source area, root executable, or operational subsystem is
introduced. Update the linked detailed guide in the same change; a map entry alone is not implementation
documentation.
