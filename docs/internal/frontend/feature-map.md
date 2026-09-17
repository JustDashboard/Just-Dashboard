# Frontend feature map

The App Router keeps route entry files thin where a feature has reusable panels and colocates page-only
orchestration where splitting it would hide the workflow. Backend capability checks remain authoritative;
hiding a control through `useAuth().can()` is affordance only.

| Route area | UI responsibility | Primary implementation |
| --- | --- | --- |
| `/login` | One centred column: password, TOTP challenge, enrolment where the install requires it, recovery codes, and partial-session states | `src/app/login/page.tsx`, auth hooks, shared logo/state/controls |
| `/` | Host overview: five capacity tiles (CPU, memory, load, network, and the fullest filesystem), a health list that folds failed systemd units and unhealthy or restarting containers into the recorder's findings, an hour of sparklines, recent events, and one destination tile per module — Docker, databases, proxy, security exposure, package updates and reboot state, deployments, backups, and the dashboard's own version | dashboard root page plus `components/metrics/` |
| `/dashboard` | Dashboard self-update status and searchable release notes | `components/update/`, self-update provider |
| `/dashboard/configuration` | The panel's own settings — address, certificate mode, ports, allowlist, two-factor policy, session lifetimes — plus restart, rebuild, and a run record that is followed across the restart it describes. Picking a certificate mode carries the address, listening interface and allowlist with it, filled from the machine's own tailnet identity | `components/config/restart-progress.tsx`, `hooks/use-self-config.tsx` (`system.admin` only) |
| `/account` | Password, TOTP/recovery codes, dashboard users, roles, and API tokens | account page, auth hook, confirmation primitives |
| `/audit` | Filtered/paginated mutation audit trail | audit page and shared page/panel/state controls |
| `/backups` | Job creation/editing (sources, SQLite snapshots, native database dumps of saved connections), target test, schedules, runs, archive contents, restore, and typed-confirmation database restore into the connection or a drill database | backups page; backend contract in [`../backend/git-backups-users.md`](../backend/git-backups-users.md#backups) |
| `/databases/*` | The connection as the section's title (a switcher), a facts row and the tab strip; Browse and Structure as one full-height workbench each — a shared table rail (`table-rail.tsx`) beside the grid or the column/index/key/definition view — with every table verb declared once in `table-actions.tsx`; the ER diagram as a workbench with an inspector, a per-table menu (browse, structure, query, focus, isolate, hide, colour, note), search-and-pan, three levels of detail, layout direction and spacing, tidy, snap, lock, full screen, and export as PNG, SVG, Mermaid, DBML or JSON, with its arrangement saved per connection and schema (`diagram/memory.ts`); query with a URL-supplied statement, cross-table find, monitor, ORM generation, and a connection page of readings, facts and maintenance rows. Every dialog is built from `components/form.tsx` and shows the statement it will run | `components/database/` and database layout/pages |
| `/deploy`, `/deploy/new`, `/deploy/[id]`, `/deploy/[id]/runs/[run]` | Project grid/list; connected GitHub and manual Git import; environment editing and inline database setup; extended workload wizard with one-click blueprint deployment reviewed from the server's rendered plan; preview-focused overview; delivery insights (success rate, deploys per week, release duration, recovery time, failure streak) on the Deployments tab; dedicated runtime, diagnostics and settings; searchable numbered build transcript with execution details on demand | `components/deploy/` (`quick-deploy.tsx`, `project-database.tsx`, `quick-database.tsx`, `deployment-overview.tsx`, `build-transcript.tsx`, `deployment-wizard.tsx`); contract in [`../deployments/implementation.md`](../deployments/implementation.md) |
| `/docker/*` | Overview plus containers, images, networks, volumes, compose stacks, event history, creation, diagnostics, and resource detail. The overview names what is not running rather than only counting it, and draws disk as a breakdown; the containers and stacks pages both filter by state; the containers, images, volumes and networks tables are each replaced — not narrowed — by a list of the same rows below the width where the table stops fitting (`xl` for containers and images, `lg` for volumes and networks); and each container's and stack's verbs are offered as words — declared once in `container-actions.tsx` and drawn by the table row, the card, the stack's overflow menu and the detail panel alike | `components/docker/`; see [`features-terminal.md`](features-terminal.md#feature-panels) |
| `/files` | Places/tree/grid navigation, search/quick-open, preview, Monaco editing, image editing, archives, permissions, transfers, and Git/shell handoffs | files page and `components/files/` |
| `/git` | Repository discovery, status/diff/history/graph, branch and worktree actions, GitHub auth, and pull requests | `components/git/`; backend contract in [`../backend/git-backups-users.md`](../backend/git-backups-users.md#git-working-copies) |
| `/logs` | Source rail, live/history modes, common filtering, histogram, console, retention context, and export | logs page and `components/logs/` |
| `/metrics` | Live and recorded host/container series, storage/inodes, saturation, health findings, annotations, range stack, and synchronized cursor | metrics page and `components/metrics/` |
| `/packages` | Updates/reboot state, installed/manual filters, catalogue search/install/remove, package usage, and resumable jobs | packages page, `components/packages/`, shared job console |
| `/processes` | Process ownership/inventory, signals, systemd, PM2 logs, cron, and journal detail | processes page and `components/procs/` |
| `/proxy/*` | Proxy overview, sites, streams, certificates, TLS scans, and listener/port inventory | proxy layout/pages and `components/proxy/` |
| `/security/*` | Shared posture/exposure context; an overview that lists every area with the verdict it actually measured; firewall rules with the policy and logging that decide what they mean; staged sshd settings; fail2ban jails as one table with bans behind a detail sheet; live connections, the host login record, network inventory, and twenty filterable probes | security layout/pages and `components/security/` |
| `/system-users` | Host account inventory, create/update/delete, groups, lock state, and SSH keys | system-users page; backend contract in [`../backend/git-backups-users.md`](../backend/git-backups-users.md#host-users-and-ssh-keys) |
| `/terminal` | Direct PTY sessions, folders, windows with retained screens and connections while switching, side tools, replay, clipboard upload, renderer, and keyboard customization | terminal page, `components/terminal/`, `components/xterm-pane.tsx`; see [`features-terminal.md`](features-terminal.md#the-terminal-panel) |

Deployment overviews and Automations show read-only production Git monitoring status, including
repository access failures. Branch deployments are automatic after the first deployment; additional
webhooks remain separate integrations. Run labels use a per-project sequence starting at 1, while
permanent run URLs retain their global identifiers. Automations → Notifications
(`components/deploy/deployment-notifications.tsx`) manages Discord, Slack, Telegram, e-mail and signed
webhook channels with event selection, pause/resume, test delivery and delivery history; the Deployment
policy editor carries the GitHub commit-status toggle. Runtime settings expose memory, CPU and process
limits and the restart policy. The Console tab (`components/deploy/deployment-console.tsx`) embeds the
Docker exec pane on the live release container for every non-game deployment.

Cross-feature navigation is intentional: Files and Docker can open Git or a terminal in context; Git can
open GitHub authentication; deployments embed scoped runtime logs, service metrics, and automatic live
website previews while retaining links to owning Docker, proxy, backup, files, and terminal surfaces.
Query parameters that trigger a one-time terminal/file context are consumed
so refresh does not repeat an action.
