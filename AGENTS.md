# Just Dashboard contributor essentials

Just Dashboard is a root-equivalent, self-hosted control panel for one Linux server. It is a Go backend
and Next.js frontend deployed as one Docker Compose stack behind Caddy. Preserve the established
architecture and security model; detailed guidance is indexed in [`docs/internal/`](docs/internal/README.md).

## Mandatory workflow

- Inspect the worktree before editing. Preserve unrelated user changes and never use destructive Git
  commands to discard work.
- Read the relevant sections of [`docs/internal/README.md`](docs/internal/README.md) before changing
  architecture, security, backend features, frontend behavior, releases, or deployment code. For the
  deployment subsystem, follow [`docs/internal/deployments/`](docs/internal/deployments/README.md).
  The historical `docs/plans/0.6.7-deployments/` directory is absent from this checkout.
- A request to *redesign a page with the design system* means running that page's register's ordered
  passes in [`docs/internal/frontend/design-system.md`](docs/internal/frontend/design-system.md).
  **Decide the register first** — §16 has the table and the test, which is what the reader came to do
  rather than what the page contains. A page that *reports* takes §15's passes with the host Overview
  as the reference; a page where the reader is *deciding* a sequence takes §17's, with `/deploy/new`
  as the reference. Read §16 and the relevant passes before touching any UI for that request.
- Do not add or change CI: no GitHub Actions workflows, no `.github/` automation, no hosted checks of
  any kind, unless the operator asks for them by name. Verification happens locally with the commands
  below; a red check on GitHub that nobody asked for is confusion, not safety.
- Use **Bun only** in `frontend/`. Keep `bun.lock`; never create `package-lock.json` or `yarn.lock`.
- Match project style: Go uses standard formatting; TS/TSX uses Prettier with no semicolons, double
  quotes, a 100-column print width, and trailing commas. Comments explain why, not what.
- Commit messages are imperative sentences describing intent, without conventional-commit prefixes.
  Do not change repository or global Git configuration or user identity. Do not commit or push unless
  the user asks.
- **Before every push, documentation review is mandatory.** Compare the complete diff with
  `docs/internal/`, `AGENTS.md`, `README.md`, and `CONTRIBUTING.md`. Update every document affected by
  changes to behavior, architecture, security, configuration, commands, tests, or workflow in the same
  change. If no update is needed, explicitly confirm that during the pre-push review. Documentation must
  never knowingly be left stale.

## Required checks

Run checks appropriate to the changed surface; before a pull request, the full required gate is:

```bash
cd backend && go build ./... && go vet ./... && go test ./...
cd ../frontend && bun run lint && bun run build && bun run test:browser
```

Install the browser once with `bun run test:browser:install`. Deployment changes have additional live,
race, and browser requirements in [`CONTRIBUTING.md`](CONTRIBUTING.md). `go.mod` requires Go 1.26.8.

## Security requirements

This software controls the Docker socket, host services, firewall, accounts, files, and a real shell.
These invariants must not regress:

1. The network allowlist runs before authentication, and nothing but Caddy binds a routable address.
2. Two-factor is enforced per account, not per install: an enrolled account is always asked for a code,
   and a session owing a second factor reaches only the 2FA routes. `JD_REQUIRE_2FA` (default false)
   decides only whether an *unenrolled* account may sign in at all.
3. Capability checks are enforced by backend routes. Every mutation is audited, and every destructive
   action uses `s.destructive`; only the documented rare, unrecoverable subset requires a server-side
   typed phrase.
4. Every client path goes through `files.Resolve` (or `ResolveEntry` for entry operations). Host commands
   use `hostexec` with explicit argv, never a request-built shell string.
5. Database schema changes are additive and migrate existing installs through `store.addedColumns` with
   defaults; shipped migration entries are never removed.

Read [the complete invariant definitions](docs/internal/security/invariants.md)
before touching any of these boundaries. If a change intentionally weakens one, stop and call it out
explicitly rather than silently changing the contract.

## Release and licensing rules

- Never edit `CHANGELOG.md` directly. Add release notes to
  `backend/internal/selfupdate/changelog.json`, then run `scripts/release.sh <version>`.
- Read [`CONTRIBUTING.md`](CONTRIBUTING.md) before changing licence headers or adding dependencies. The
  project is AGPL-3.0 with an additional contributor grant to the owner.
