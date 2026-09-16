# Internal documentation

This directory is the engineering reference for Just Dashboard. Its source coverage and factual
inventories were reverified against the worktree on 2026-09-09. `AGENTS.md` is the short, canonical
operating guide; the documents here explain the implementation, rationale, security boundaries, test
strategy, and feature ownership behind those rules.

## Start here

- [`overview.md`](overview.md) — product scope, repository-wide commands, test strategy, browser testing,
  and local-development constraints.
- [`reference/repository-map.md`](reference/repository-map.md) — source-tree map covering every backend
  package and every frontend, deployment, build, test, and operations area.
- [`codebase.md`](codebase.md) — compatibility landing page for older links.

## Architecture and security

- [`architecture/request-lifecycle.md`](architecture/request-lifecycle.md) — middleware order, route
  capabilities, handler rules, destructive actions, and content-dependent authorization.
- [`architecture/runtime-boundaries.md`](architecture/runtime-boundaries.md) — server/module lifecycle,
  optional degradation, host execution, path containment, authentication, encryption, audit, and SQLite.
- [`security/invariants.md`](security/invariants.md) — the invariants that must not regress and the complete
  typed-confirmation policy.

## Backend features

- [`backend/observability-security.md`](backend/observability-security.md) — metrics, health, exposure,
  posture, login history, sshd, firewall/fail2ban, and six package managers.
- [`backend/docker-files-logs.md`](backend/docker-files-logs.md) — Docker, compose, file management,
  archives, previews, log discovery/search/tailing, and their frontend integration.
- [`backend/processes-terminal-github.md`](backend/processes-terminal-github.md) — processes, systemd, PM2,
  cron, PTYs, terminal organization, and GitHub device authentication.
- [`backend/git-backups-users.md`](backend/git-backups-users.md) — Git working copies and mutations,
  backup scheduling/storage/restore, and host accounts/SSH keys.
- [`backend/databases-proxy-platform.md`](backend/databases-proxy-platform.md) — eight database engines,
  nginx/proxy/TLS/DNS, streaming, jobs, secrets, agent mode, configuration, releases, self-update, and the
  dashboard's own settings (restart, rebuild, rollback, Tailscale certificates).

## Deployments and frontend

- [`deployments/caddy-ingress.md`](deployments/caddy-ingress.md) — automatic public Docker Caddy sharing,
  fresh-host provisioning, certificates, route recovery and ownership boundaries.
- [`deployments/implementation.md`](deployments/implementation.md) — implemented deployment model through
  C7, execution/activation/recovery, feature joins, automation, previews, and production topology.
- [`deployments/redesign-plan.md`](deployments/redesign-plan.md) — deployment experience redesign,
  delivered workflows, and validation evidence.
- The historical `docs/plans/0.6.7-deployments/` directory is absent from this checkout. Use the
  checked-in deployment guides above; missing checkpoint evidence must not be treated as verified.
- [`frontend/design-system.md`](frontend/design-system.md) — the design system's rules in full: one mode,
  no lift, the colour roles, the component vocabulary, and which surface to reach for.
- [`frontend/shell-design.md`](frontend/shell-design.md) — App Router shell, navigation, layout primitives,
  accessibility, Monaco, and charts.
- [`frontend/features-terminal.md`](frontend/features-terminal.md) — Docker/security/package feature UX and
  the terminal workspace, renderer, shortcuts, clipboard, reconnect, and layout behavior.
- [`frontend/feature-map.md`](frontend/feature-map.md) — every route area, its user-facing
  responsibility, component owner, and cross-feature handoffs.
- [`frontend/data-theming.md`](frontend/data-theming.md) — API and WebSocket clients, polling, metrics state,
  confirmations, and self-update state.

## Contributor reference

- [`../audits/2026-09-16-deployments/README.md`](../audits/2026-09-16-deployments/README.md) — deployment capability audit, fresh verification results, competitor comparison, and prioritized gaps.
- [`../audits/0.6.7/remediation-report.md`](../audits/0.6.7/remediation-report.md) — audit fixes,
  verification evidence and remaining operational/dependency limits on `patch/0.6.7`.
- [`reference/verification-findings.md`](reference/verification-findings.md) — open discrepancies found by
  source revalidation; these are findings, not exceptions to the security contract.
- [`contributing/conventions.md`](contributing/conventions.md) — comment, formatting, commit, and licensing
  conventions.
- [`../../CONTRIBUTING.md`](../../CONTRIBUTING.md) — contribution terms and the complete pre-PR gate.
- [`../../README.md`](../../README.md) — operator-facing behavior, installation, configuration, and use.

## Keeping these docs current

Documentation is part of the change. When code changes behavior, architecture, security boundaries,
configuration, commands, tests, or contributor workflow, update the owning document in the same change.
Before every push, compare the complete diff with this directory, `AGENTS.md`, `README.md`, and
`CONTRIBUTING.md`; update every affected document or explicitly record that no update is needed.

For repository-wide verification:

1. Compare `backend/internal/` packages and `frontend/src/app/` route areas with
   [`reference/repository-map.md`](reference/repository-map.md).
2. Compare route mounts in `backend/internal/api/routes.go` with the request-lifecycle and feature docs.
3. Compare `moduleSet`, `Config`, the SQLite schema, `frontend/package.json`, and `backend/go.mod` with
   their documented inventories and versions.
4. Check every Markdown link and every named source file, then run the checks in [`overview.md`](overview.md)
   appropriate to the changed surface.

New detailed guidance belongs in the narrowest matching document. Add a new document and index entry when
an existing file would need to cover a second independent concern.
