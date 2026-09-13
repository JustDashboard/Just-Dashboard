# Frontend feature map

The App Router keeps route entry files thin where a feature has reusable panels and colocates page-only
orchestration where splitting it would hide the workflow. Backend capability checks remain authoritative;
hiding a control through `useAuth().can()` is affordance only.

| Route area | UI responsibility | Primary implementation |
| --- | --- | --- |
| `/login` | One centred column: password, TOTP challenge, enrolment where the install requires it, recovery codes, and partial-session states | `src/app/login/page.tsx`, auth hooks, shared logo/state/controls |
| `/` | Host overview, current health, capacity, recent events, and sparklines | dashboard root page plus `components/metrics/` |
| `/dashboard` | Dashboard self-update status and searchable release notes | `components/update/`, self-update provider |
| `/dashboard/configuration` | The panel's own settings — address, certificate mode, ports, allowlist, two-factor policy, session lifetimes — plus restart, rebuild, and a run record that is followed across the restart it describes. Picking a certificate mode carries the address, listening interface and allowlist with it, filled from the machine's own tailnet identity | `components/config/restart-progress.tsx`, `hooks/use-self-config.tsx` (`system.admin` only) |
| `/account` | Password, TOTP/recovery codes, dashboard users, roles, and API tokens | account page, auth hook, confirmation primitives |
| `/audit` | Filtered/paginated mutation audit trail | audit page and shared page/panel/state controls |
| `/backups` | Job creation/editing, target test, schedules, runs, archive contents, and restore | backups page; backend contract in [`../backend/git-backups-users.md`](../backend/git-backups-users.md#backups) |
| `/databases/*` | Connection context plus browse, query, structure, monitor, cross-table find, ER diagram, ORM generation, import/export, and mutation confirmations | `components/database/` and database layout/pages |
| `/deploy`, `/deploy/new`, `/deploy/[id]`, `/deploy/[id]/runs/[run]` | Deployment list; quick deploy for a GitHub repository, an image or a database, with the five-step wizard behind `?mode=advanced`; the release path renders a Certificate node between Release and Start; configuration workspace, automation, and resumable run evidence | `components/deploy/` (`quick-deploy.tsx`, `quick-database.tsx`, `deployment-wizard.tsx`, shared `deployment-defaults.ts` / `deployment-findings.tsx`); contract in [`../deployments/implementation.md`](../deployments/implementation.md) |
| `/docker/*` | Overview plus containers, images, networks, volumes, compose stacks, event history, creation, diagnostics, and resource detail. The overview names what is not running rather than only counting it, and draws disk as a breakdown; the containers page filters by state, is a card list below `lg`, and offers each container's verbs as words — declared once in `container-actions.tsx` and drawn by the table row, the card and the detail panel alike | `components/docker/`; see [`features-terminal.md`](features-terminal.md#feature-panels) |
| `/files` | Places/tree/grid navigation, search/quick-open, preview, Monaco editing, image editing, archives, permissions, transfers, and Git/shell handoffs | files page and `components/files/` |
| `/git` | Repository discovery, status/diff/history/graph, branch and worktree actions, GitHub auth, and pull requests | `components/git/`; backend contract in [`../backend/git-backups-users.md`](../backend/git-backups-users.md#git-working-copies) |
| `/logs` | Source rail, live/history modes, common filtering, histogram, console, retention context, and export | logs page and `components/logs/` |
| `/metrics` | Live and recorded host/container series, storage/inodes, saturation, health findings, annotations, range stack, and synchronized cursor | metrics page and `components/metrics/` |
| `/packages` | Updates/reboot state, installed/manual filters, catalogue search/install/remove, package usage, and resumable jobs | packages page, `components/packages/`, shared job console |
| `/processes` | Process ownership/inventory, signals, systemd, PM2 logs, cron, and journal detail | processes page and `components/procs/` |
| `/proxy/*` | Proxy overview, sites, streams, certificates, TLS scans, and listener/port inventory | proxy layout/pages and `components/proxy/` |
| `/security/*` | Shared posture/exposure context; an overview that lists every area with the verdict it actually measured; firewall rules with the policy and logging that decide what they mean; staged sshd settings; fail2ban jails as one table with bans behind a detail sheet; live connections, the host login record, network inventory, and twenty filterable probes | security layout/pages and `components/security/` |
| `/system-users` | Host account inventory, create/update/delete, groups, lock state, and SSH keys | system-users page; backend contract in [`../backend/git-backups-users.md`](../backend/git-backups-users.md#host-users-and-ssh-keys) |
| `/terminal` | Direct PTY sessions, folders, windows, side tools, replay, clipboard upload, renderer, and keyboard customization | terminal page, `components/terminal/`, `components/xterm-pane.tsx`; see [`features-terminal.md`](features-terminal.md#the-terminal-panel) |

Cross-feature navigation is intentional: Files and Docker can open Git or a terminal in context; Git can
open GitHub authentication; deployment runs link to their owning Docker, proxy, backup, metrics, logs,
files, and terminal surfaces. Query parameters that trigger a one-time terminal/file context are consumed
so refresh does not repeat an action.
