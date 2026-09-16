# Deployment experience redesign

## Direction

Use the supplied project-list, repository-import, configuration, build-log and project-preview
references to reorganize the complete deployment experience. Preserve the dashboard's dark palette,
orange command buttons, typography, flat surfaces, status vocabulary and capability checks.
Use existing components and APIs where possible; the backend remains the authority for execution,
secrets, resource ownership, confirmation and audit.

The referenced `docs/plans/0.6.7-deployments/` directory is absent from this checkout. The checked-in
implementation documentation, security invariants and source contracts govern this change.

## Delivery plan

1. **Projects:** independent cards with source, address, recent release and state; grid/list views,
   search and filters; obvious new-project and archive navigation; purposeful empty states.
2. **Create:** connected GitHub repositories and manual Git import visible on arrival; discoverable
   images, databases, Compose, templates, workers, static sites, games and adoption. Keep branch and
   credential choices accessible. Configuration includes environment editing, database creation or
   linking, detected build settings and generated HTTPS address before deployment.
3. **Progress:** one searchable, numbered, readable build transcript across all steps, compact
   progress and deployment summary; terminal outcome with working address; detailed execution
   evidence, runtime logs and metrics available on demand. Preserve stream replay and retry semantics.
4. **Project:** preview and Visit action, essential release/source information, honest compact usage,
   recent deployments and branch context. Configuration never fills the initial overview.
5. **Focused destinations:** runtime, diagnostics, build/runtime configuration, environment, domains,
   storage, dependencies, automation and lifecycle. Editors and secondary actions use disclosure,
   menus or sheets, including the extended creation flow and game workflows.
6. **Verification:** meaningful browser coverage of new and retained behavior; rendered desktop and
   mobile screenshots, keyboard and overflow checks, lint/build/unit/browser gates; backend gates
   appropriate to changes. Update deployment and frontend documentation to match final behavior.

## Strip-down — 2026-09-16

The delivered surfaces above were composed of framed panels, each with a tinted header, and the
projects page put four bands of chrome — a section heading, a count, a view switch and a toolbar —
between the title and the first project. This pass keeps every behaviour and test named below and
removes the containers:

- **Projects:** one toolbar row (search, a quiet Filters popover, clear, count, grid/list) above the
  cards. A card is the workload mark, the name, its address, the branch and release, then one line
  with the last run's state, its age and the environment. Health appears in that line only when it
  is not healthy; a pending-changes strip appears only when there are pending changes. Active work is
  a plain list above the fleet.
- **Create:** the page is titled "New project"; the three ways in are the product's underline tab
  strip rather than toggle buttons. The import form and the "Start with something ready" catalogue
  are plain (`Panel plain`, `RowList`). The configure step opens with a one-line source row instead
  of a step indicator and a titled box, and its sections — settings, environment variables, database,
  public address — are titled and ruled rather than framed. Preflight findings keep their frame.
- **Project overview:** the preview and its facts sit on the page; branch and deploy-on-push are two
  facts beside the source instead of a fourth panel; recent deployments and resource usage are plain
  lists. The settings destinations keep framed forms with a footer action, which is the shape a
  settings page reads best in.
- **Run:** the summary is the page's own opening (facts, progress line, outcome) rather than a box;
  the four views switch with the underline strip; execution details are a plain list. The build
  transcript keeps its frame because it is a scrolling console.
- **Archive:** a plain list with the search in its toolbar.

`deploy-ui.spec.ts` passes unchanged (56 checks) against these surfaces.

## Acceptance evidence

Completion requires inspection of the implemented routes and rendered screenshots plus passing
checks covering source import, variable and database setup, success/failure progress, replay,
navigation, secrets, capability restrictions and destructive confirmations. Missing runtime evidence
must be described as unavailable, never fabricated as a successful deployment or measured usage.

### Delivered and reviewed — 2026-09-15

| Area | Implemented behavior and evidence |
| --- | --- |
| Projects | Independent project cards, search/filter popover, grid/list switch, source/release/health evidence, per-project actions and searchable archive. Browser coverage preserves filters and checks 375–1440px layouts. |
| Import and configure | Connected GitHub repositories, account/owner controls, manual Git URLs with ref and credential selection, extended workload entries, editable build settings, masked environment rows and dotenv import. Tests cover GitHub, manual Git, Dockerfile workers, static recipes, Compose, images, blueprints, games and adoption. |
| Databases | Create a database or use an existing connection in setup; add the application URL to an environment variable and record the dependency. Retry and sheet reopen reuse the started container. All five quick-setup engines were provisioned and authenticated from separate containers with loopback-only host bindings. |
| Deployment progress | One numbered transcript with search, error/stage filters, wrap, follow and copy; optional execution details/runtime logs/metrics; finished URL only for the current live release. Browser tests cover chunk formatting, duplicate events, reconnect sequences, keyboard selection, cancellation and retry. |
| Project overview | Automatic constrained preview, Visit, release/source/health, recent deployments, branch and compact measured usage. Preview isolation and honest unavailable states remain covered. |
| Focused settings | Dedicated build, runtime, variables, domains, storage, dependencies, automation and lifecycle views. Editors use sheets; resource pickers use names; secret masking and destructive confirmations remain covered. Game console output has numbered lines and raw settings use disclosure. |

Desktop and mobile renders were inspected for project cards, source import, setup, the overview,
completed build transcript, runtime services and settings. Browser captures are produced by
`frontend/tests/browser/deploy-ui.spec.ts`, including `fleet-grid.png`, `create-desktop.png`,
`configure-mobile.png`, `overview-desktop.png`, `completed-build-mobile.png` and
`build-settings-desktop.png`.

### Checks

- Backend: `go build ./...`, `go vet ./...`, `go test ./...` passed. Focused URL tests also
  verify credential/transport preservation, exact loopback binding identity and safe refusal.
  The pre-push test run used `env -u JD_TERMINAL_SHELL go test ./...` to clear an inherited
  shell override that interfered with the legacy-prefix configuration test.
- Deployment race gate: `go test -race ./internal/deploy ./internal/api ./internal/proxysvc
  ./internal/backups ./internal/store -count=1` passed.
- Live database gate: `JD_DEPLOY_LIVE=1 go test ./internal/api
  -run TestLiveDeploymentDatabaseConnection -count=1 -v` passed for PostgreSQL, MySQL, MariaDB,
  Redis and MongoDB. Test containers and volumes were removed.
- Frontend: `bun run lint`, `bunx tsc --noEmit`, `bun run test` (15 tests) and `bun run build`
  passed. The production build used an isolated source copy to preserve the existing local service.
- `bun run test:browser` passed all 86 checks against the production build, including 45 deployment
  scenarios and both DOM and WebGL terminal renderers. Terminal coverage verifies retained screens,
  background output, input routing, resize behavior and connection cleanup. The fixture uses in-app
  navigation for cleanup and dismisses export notifications before exact screen comparisons.
- `git diff --check` passed. No dependencies, lockfiles, schema migrations, or release metadata changed.

Documentation was reviewed against the complete deployment and terminal diff: deployment
implementation, frontend feature map, database provisioning, terminal backend/frontend contracts,
README, contributor validation instructions and the internal index were updated. AGENTS.md requires
no change. Preview restrictions, capability checks, destructive confirmation, audit, secret storage
and the owning-feature boundaries remain in place.
