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
| `AGENTS.md`, `CONTRIBUTING.md` | Mandatory contributor workflow, security baseline, licensing, and full validation gate | [`../contributing/conventions.md`](../contributing/conventions.md) |
| `docs/plans/0.6.7-deployments/` (absent) | Historical references only; no frozen-contract or checkpoint evidence available in this checkout | [`../deployments/implementation.md`](../deployments/implementation.md) |

## Backend entry point and packages

`backend/cmd/server` loads configuration, opens the store, creates the API server, starts background
services, handles signals, and supports the isolated self-update worker mode. The 29 packages under
`backend/internal/` are:

| Package | Responsibility | Detailed reference |
| --- | --- | --- |
| `agent` | Agent identity, certificates, and hub-facing mode | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#streaming-jobs-secrets-agent-mode) |
| `api` | Route map, middleware composition, handlers, module wiring, audit, and feature joins | [`../architecture/request-lifecycle.md`](../architecture/request-lifecycle.md) |
| `audit` | Durable and process-log mutation audit records | [`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md#auth-secrets-state) |
| `auth` | Passwords, TOTP enrolment and policy, recovery codes, sessions, roles/capabilities, and API tokens | [`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md#auth-secrets-state) |
| `backups` | Backup definitions/runs, scheduler, object stores, retention, archive listing, and contained restore | [`../backend/git-backups-users.md`](../backend/git-backups-users.md#backups) |
| `config` | Environment parsing, defaults, bounds, legacy aliases, and network safety validation | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#configuration-version-release-self-update) |
| `dbx` | SQL and NoSQL connections, classification, browsing, DDL, query, import/export, and dumps | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#databases-eight-engines-one-shape) |
| `deploy` | Legacy compatibility plus normalized planning, artifacts, orchestration, activation, recovery, configuration, and automation | [`../deployments/implementation.md`](../deployments/implementation.md) |
| `dockerx` | Docker SDK, container/image/network/volume operations, compose state, builds, stats, events, scans, diagnosis, and the derived layer — attention, exposure, image references, writable-layer analysis, failure diagnosis, anomalies, deploy preview, deployment history, cleanup | [`../backend/docker-files-logs.md`](../backend/docker-files-logs.md#docker) |
| `files` | Root-contained browse/write/search/archive/preview/place operations | [`../backend/docker-files-logs.md`](../backend/docker-files-logs.md#files) |
| `ghx` | GitHub device login, pull requests (list, view, create, merge, check out) and workflow runs | [`../backend/processes-terminal-github.md`](../backend/processes-terminal-github.md#github-sign-in) |
| `gitx` | Repository discovery/status, graph, branches, tags, stashes, remotes, commit detail, comparison, diffs, ownership, and mutations including clone and init | [`../backend/git-backups-users.md`](../backend/git-backups-users.md#git-working-copies) |
| `hostexec` | Explicit-argv local/host namespace command execution and owner dropping | [`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md#reaching-the-host-and-containing-paths) |
| `httpx` | HTTP errors/JSON, identity context, CSRF, allowlist, auth, capabilities, limits, audit, and confirmation | [`../architecture/request-lifecycle.md`](../architecture/request-lifecycle.md) |
| `jobs` | Bounded long-running operations observed by id | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#streaming-jobs-secrets-agent-mode) |
| `linuxusers` | Host accounts, groups, lock state, login metadata, and authorized SSH keys | [`../backend/git-backups-users.md`](../backend/git-backups-users.md#host-users-and-ssh-keys) |
| `logsx` | Source discovery, filtering, history search, and live tails | [`../backend/docker-files-logs.md`](../backend/docker-files-logs.md#logs) |
| `metrics` | Persistent host/container samples, history, events, and health assessment | [`../backend/observability-security.md`](../backend/observability-security.md#metrics-saturation-health) |
| `netsec` | Exposure, posture, listeners, sessions/logins, firewall, fail2ban, sshd, and diagnostic probes | [`../backend/observability-security.md`](../backend/observability-security.md) |
| `procs` | Process inventory, signals, PM2, systemd, and cron | [`../backend/processes-terminal-github.md`](../backend/processes-terminal-github.md#processes) |
| `proxysvc` | nginx sites/streams, certificates, DNS/TLS checks, ports, htpasswd, and deployment routes | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#proxy) |
| `safepath` | Symlink-safe archive extraction boundary | [`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md#reaching-the-host-and-containing-paths) |
| `selfcfg` | The dashboard's own settings: `.env` reading/writing, validation, restart and rebuild in a sibling container with automatic rollback, and Tailscale certificate issuance/renewal | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#the-dashboards-own-settings) |
| `selfupdate` | Release checks, changelog, installer state, reconciliation, and updater | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#configuration-version-release-self-update) |
| `store` | SQLite schema, additive columns, deployment migration, and connection lifecycle | [`../architecture/runtime-boundaries.md`](../architecture/runtime-boundaries.md#auth-secrets-state) |
| `sysinfo` | Host metrics, disk/device statistics, pressure, sockets, and capacity | [`../backend/observability-security.md`](../backend/observability-security.md#metrics-saturation-health) |
| `term` | Direct PTY sessions, replay, organization, clipboard uploads, and bundled shell setup | [`../backend/processes-terminal-github.md`](../backend/processes-terminal-github.md#the-terminal) |
| `updates` | Six package-manager adapters, catalogue, upgrades, reboot state, and usage summaries | [`../backend/observability-security.md`](../backend/observability-security.md#packages-six-managers-one-interface) |
| `version` | Build/release version normalization | [`../backend/databases-proxy-platform.md`](../backend/databases-proxy-platform.md#configuration-version-release-self-update) |
| `wsx` | WebSocket origin validation, upgrade, and shared socket behavior | [`../architecture/request-lifecycle.md`](../architecture/request-lifecycle.md) |

## Frontend

| Area | Responsibility | Detailed reference |
| --- | --- | --- |
| `src/app/` | App Router layouts plus 48 page entry points across account, dashboard, audit, backups, databases, deployments, Docker, files, Git, logs, metrics, packages, processes, proxy, security, users, terminal, and login | [`../frontend/feature-map.md`](../frontend/feature-map.md) |
| `src/components/ui/` | Low-level accessible controls; project composition lives above this layer | [`../frontend/shell-design.md`](../frontend/shell-design.md#the-design-system) |
| `src/components/{database,deploy,docker,files,git,logs,metrics,packages,procs,proxy,security,terminal,update}/` | Feature panels, forms, tables, dialogs, visualizations, and workspaces | [`../frontend/features-terminal.md`](../frontend/features-terminal.md) |
| `src/components/` top level | Shell, sidebar, command palette, page/panel/state primitives, editors, icons, and shared confirmations | [`../frontend/shell-design.md`](../frontend/shell-design.md) |
| `src/hooks/` | Auth, polling, WebSockets, metrics history/windows, self-update, keyboard, theme, and responsive state | [`../frontend/data-theming.md`](../frontend/data-theming.md) |
| `src/lib/` | Typed API URLs/requests, view stores, formatting, themes, terminal behavior, metrics transforms, Docker templates, and pure helpers | [`../frontend/data-theming.md`](../frontend/data-theming.md) |
| `src/proxy.ts`, `next.config.ts` | CSP nonce/policy and development API rewrite | [`../frontend/shell-design.md`](../frontend/shell-design.md), [`../overview.md`](../overview.md) |
| `scripts/sync-monaco.mjs` | Copies the pinned Monaco worker/editor assets into the build output | [`../frontend/shell-design.md`](../frontend/shell-design.md#the-editor-is-served-from-here-not-from-a-cdn) |
| `tests/browser/`, `playwright.config.ts` | Chromium release journey gate and opt-in cross-browser evidence | [`../overview.md`](../overview.md) |
| `package.json`, `bun.lock`, build configs | Bun-only dependency, lint, type/build, and browser-test toolchain | [`../overview.md`](../overview.md) |

## Updating this map

Add a row when a new backend package, frontend source area, root executable, or operational subsystem is
introduced. Update the linked detailed guide in the same change; a map entry alone is not implementation
documentation.
