# Project overview, commands, and tests

Detailed architecture, implementation, testing, and design guidance for contributors and coding agents.
The short canonical operating rules live in [`../../AGENTS.md`](../../AGENTS.md).

## What this is

Just Dashboard is a self-hosted control panel for **one** Linux server: metrics, Docker, processes,
logs, a real PTY, files, git, databases, reverse proxy, firewall, backups and deploys behind a single
authenticated UI. Go backend + Next.js frontend behind Caddy, one `docker compose` stack.

The software is **root-equivalent** — it drives the Docker socket, systemd, the firewall, host
accounts and a shell. Its security boundary is *the network perimeter plus authentication*, not the
container — a tailnet or an ssh tunnel in front, and two-factor enforced for every account that has
enrolled one (`JD_REQUIRE_2FA` decides whether enrolling is compulsory; it is not by default). Every architectural oddity here traces back to that.

## Commands

```bash
# backend/
go build ./... && go vet ./... && go test ./...
go test ./internal/gitx -run TestBranchParse -v
go run ./cmd/server                  # needs JD_MASTER_KEY and a writable JD_DATA_DIR

# frontend/
bun install && bun dev               # :3000, proxies /api to 127.0.0.1:8080
bun run lint && bun run build
bun run test:browser:install         # once per machine/cache: release Chromium
bun run test:browser                 # Playwright Chromium journey gate

# whole stack
sudo ./install.sh                    # interactive first install; re-runnable, keeps .env
                                     # asks one question that matters: Tailscale (default) or SSH tunnel
docker compose up -d --build
docker compose logs backend | grep "bootstrap admin"   # generated password, printed once
scripts/release.sh 0.6               # see backend/databases-proxy-platform.md#cutting-a-release
```

CONTRIBUTING requires backend build/vet/tests and frontend lint/build/browser journeys to pass before a
PR.

**Backend testing.** 33 internal packages carry unit and integration tests. Unit fixtures are isolated;
live database and Docker tests can contact reachable services, so configure disposable test targets.
Integration families skip rather than fail when their dependencies are absent:

- **Live database tests** (`dbx/live*_test.go`, `api/handlers_db_live_test.go`) read each engine's DSN
  from an env var defaulting to a local instance. Re-run with `-count=1` or the cache serves yesterday's
  skips. These are the tests that matter for dbx: a catalogue query naming a column the server does not
  have is string-matched identically by a unit test, and only a real engine rejects it.
- **Docker tests** use a reachable daemon for supported live checks. Deployment artifact/activation
  suites and daemon-wide prune tests require the separate opt-ins documented in `CONTRIBUTING.md`.
- **`term` and the terminal half of `api`** drive real PTYs. Direct-session tests isolate clipboard
  storage and never touch an operator shell; the remaining legacy tmux tests inside `term` take a private
  server in that package's `TestMain` (`TMUX_TMPDIR`).

Extend these when you touch the matching surface: security — `httpx/confirm_test.go`,
`api/routes_test.go`, `api/docker_spec_test.go`, `files/files_test.go`, `safepath/safepath_test.go`,
`dbx/classify_test.go`, `api/handlers_security_test.go` (signs a real admin in and drives whole routes,
because a rule tested in its own package says nothing about which group the route was mounted in);
product *claims* — `dockerx/diagnose_test.go`, `netsec/posture_test.go`, `proxysvc/tlsscan_test.go`;
nginx rendering **including the parse back** — `proxysvc/sites_test.go`, since anything the renderer
emits and the parser cannot read is a field silently dropped on the next save.

Deployment foundation checks can be isolated while iterating:

```bash
go test ./internal/store -run TestOpenMigratesPopulated066DeploymentsIdempotently -count=1
go test ./internal/deploy -run 'Test(RunTransitionMatrix|StepTransitionMatrix|C0AdapterTranscripts)' -count=1
go test ./internal/hostexec ./internal/config -count=1
```

The C4 builder matrix is hermetic by default. On a release host with Docker,
Buildx and network access, run its opt-in OCI/layer boundary as well:

```bash
JD_DEPLOY_LIVE=1 go test ./internal/deploy -run TestLiveC4ArtifactAdapters -count=1 -v
```

**Frontend notes.** bun only (`bun.lock`); never add `package-lock.json` or `yarn.lock`. Next's dev
rewrite proxies HTTP but **not** WebSocket upgrades, so socket-backed pages in dev need
`NEXT_PUBLIC_WS_BASE=http://localhost:8080` plus `JD_ALLOWED_ORIGINS=http://localhost:3000` on the
backend. The default WebSocket origin check matches scheme, hostname, and effective port (`https` in
production, `http` under `JD_DEV`); each cross-origin exception must be a complete origin in that
allowlist. `bun dev`/`bun run build` run `scripts/sync-monaco.mjs` first; invoking `next` directly skips
it and leaves every editor spinning. `go.mod` declares `go 1.26.8` — check `go version` before blaming
the code on a network-restricted machine.

**Browser testing.** Playwright tests live in `frontend/tests/browser`; `playwright.config.ts` starts a
local frontend unless `JD_BROWSER_BASE_URL` points at an already-running disposable stack. Chromium is
the required gate. Set `JD_BROWSER_PROJECTS=all` for the opt-in Firefox/WebKit evidence run. Deployment
journeys use the real API, SQLite queue, and a uniquely labelled Docker fixture namespace; provider and
DNS boundaries may be stubbed, but the orchestration path may not. Traces/screenshots are retained only
on failure under ignored `test-results`/`playwright-report` directories.
