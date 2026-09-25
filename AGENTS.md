# Just Dashboard contributor essentials

Just Dashboard is a root-equivalent, self-hosted control panel for one Linux server. It is a Go backend
and Next.js frontend deployed as one Docker Compose stack behind Caddy. Preserve the established
architecture and security model; detailed guidance is indexed in [`docs/internal/`](docs/internal/README.md).

## Mandatory workflow

- Inspect the worktree before editing. Preserve unrelated user changes and never use destructive Git
  commands to discard work.
- Always assume other agents are working in this repository. Create a separate Git worktree and task
  branch from the active branch before editing. Run commands in that worktree and leave other agents'
  worktrees untouched.
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
  Do not change repository or global Git configuration or user identity.
- **Every change, however small, goes through its own branch and pull request.** Start a new branch
  from the branch that is currently checked out (the active branch — a release branch such as
  `patch/0.7.0`, not `main`, unless `main` is what is checked out), commit the change there, push it,
  and open a pull request back into that active branch. This needs no separate go-ahead. After filing
  the pull request, watch its checks and review, fix failures or conflicts on the task branch, and keep
  it ready for operator review. **Merging the pull request and deleting the branch do** require the
  operator's permission every time, unless they said to merge in the same request.
- **Before every push, documentation review is mandatory.** Compare the complete diff with
  `docs/internal/`, `AGENTS.md`, `README.md`, and `CONTRIBUTING.md`. Update every document affected by
  changes to behavior, architecture, security, configuration, commands, tests, or workflow in the same
  change. If no update is needed, explicitly confirm that during the pre-push review. Documentation must
  never knowingly be left stale.

## Required checks

**Never run the whole browser suite or `go test ./...`.** They take minutes to report on pages and
packages the change never touched. Run what the change can reach:

```bash
scripts/test-changed.sh          # against the branch this one started from, or name a base
```

It reads the diff (committed, staged, unstaged and untracked) and runs Prettier and ESLint on the
changed frontend files, `tsc --noEmit`, `bun test src`, `go build ./...` and `go vet` on the changed
packages, the Go tests beside each changed file, and the browser specs that open a page the change
renders, with `design-system.spec.ts` for any UI change. The header of the script says how each is
picked. Use it while working and again before a pull request. There is no separate full gate.

`bun test src` is the fast layer over the pure logic in `src/lib` and `src/components`, usually in well
under a second. Anything expressible there belongs there rather than in a browser spec.

**Browser specs run against a server you already have up**, built from the tree under test:

```bash
cd frontend && bun run build && bun run start --hostname 127.0.0.1 --port 43117   # once per build
```

Rebuild and restart it after a source change, or the specs test the old build. `JD_BROWSER_BASE_URL`
points them at a server on another port when worktrees run side by side. To iterate on one spec, run
it directly: `bunx playwright test tests/browser/docker-ui.spec.ts` (~30s).

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
