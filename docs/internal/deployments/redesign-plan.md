# Deployment experience redesign

## Direction

Use the supplied project-list, repository-import, configuration, build-log and project-preview
references to reorganize the complete deployment experience. Preserve the dashboard's dark palette,
brand-coloured command buttons, typography, flat surfaces, status vocabulary and capability checks.
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

## Rebuild — 2026-09-17

The deployment section was rebuilt from the ground up around the shapes Vercel settled on and the
capabilities Coolify ships, on the design system's terms: readings on the page, plain panels,
hairlines, one brand colour. The query-string workspace (`/deploy/[id]?tab=`) became real routes under a
`(project)` route group; every old `?tab=` value redirects to its new address. The 24 `deployment-*`
components, the five-step wizard, `deployment-handoff.ts` and `deploy-ui.spec.ts` are gone; the
screens below live in `frontend/src/components/deploy/` and are covered by
`deploy-projects.spec.ts`, `deploy-new.spec.ts`, `deploy-project.spec.ts`, `deploy-run.spec.ts`,
`deploy-settings-a.spec.ts` and `deploy-settings-b.spec.ts` over the shared mocked fixture
`deploy-fixture.ts`.

| Route | Screen |
| --- | --- |
| `/deploy` | Projects: in-progress runs, search, state chips, a grid of project cards (or rows) with the workload mark, the address, the branch and commit subject, the Compose service count and one status word. `?view=archived` lists archived projects with Restore and Delete permanently. |
| `/deploy/notifications` | The fleet-level notification channels (Discord, Slack, Telegram, e-mail, signed webhook) with test delivery, pause, history and removal. |
| `/deploy/new` | One page: unfinished setups to resume, a source strip (Git repository, Docker image, Template, Database, Compose) and a configure form (name, type, build & output settings, environment variables, database, public address, an Advanced disclosure) that ends in Deploy or Save only. `?draft=` resumes a draft and `?mode=advanced` opens Advanced. |
| `/deploy/[id]` | The project shell (name, status word, Visit, the one command, a verbs menu, a facts row; its pages are the sidebar's third level, not a tab strip) and the Overview: the production block with the site preview and its facts, findings that need attention, recent deployments, live usage. |
| `/deploy/[id]/deployments` | Delivery figures, filter chips, and the run rows — status, duration, title, commit subject, branch · sha · trigger · time — with Roll back, Compare with live, Pin, Retry and Cancel behind each row, and older pages on request. |
| `/deploy/[id]/logs`, `/runtime`, `/console` | Five traffic readings and an alerts line over one pane of three views — Requests (what the ingress served, from a record the server keeps in memory, with deploy and container marks on the chart), Insights (the window faceted: failing pages, scanners, bots, sources, slowest) and Events (Docker's exits, OOM kills, restarts and health flips), see [`request-observability.md`](request-observability.md); services, live usage and recorded charts, routes/storage/backup evidence; a shell inside the live container. Game servers add `/players` and `/game-settings`. |
| `/deploy/[id]/settings/*` | General, Build, Runtime (with the health-check editor), Variables, Domains, Storage, Databases & backups, Automation (webhooks with their delivery log, schedules, previews), Danger zone — each a stack of setting cards with a footer Save, reached from the Settings group on the rail rather than a rail of their own. |
| `/deploy/[id]/runs/[run]` | The deployment page: status, facts, the release path with durations, the build console (search, stage, errors, wrap, follow), runtime logs, details and metrics. |

Backend additions in the same change: `stop` and `start` operations (and `stopped` on every
summary), a `ref`/`sourceRevision` override on manual deploys, commit subject/author/date recorded
on Git runs, project rename, unarchive, release pinning, run-history filters and pagination, a
trigger delivery log with secret rotation, preview-approval rejection, draft listing, the Compose
service count, deterministic hostname proposals, field pointers on validation refusals, and the
archive/candidate-release/remove-managed fixes listed in the audit ledger
(`docs/audits/2026-09-16-deployments/implementation-progress.md`, "Fourth pass").

### Verification — 2026-09-17

- Backend: `go build ./... && go vet ./... && go test ./... -count=1` passed for every package
  (one intermittent `internal/term` test, `TestSetMetaIsVisibleImmediatelyAndPersisted`, failed
  once under full-suite load and passed on its own and on two of three whole-package reruns; the
  package was not touched by this work). `gofmt` and `go vet` are clean for `internal/deploy`,
  `internal/api`, `internal/auth`.
- Deployment race gate: `go test -race ./internal/deploy ./internal/api ./internal/proxysvc
  ./internal/backups ./internal/store -count=1` passed with no data races.
- Live Docker gates: `JD_DEPLOY_LIVE=1 go test ./internal/deploy -run
  'TestLiveC5ActivationAdapters|TestLiveC4ArtifactAdapters' -count=1 -v` passed against the host's
  Docker daemon.
- `python3 scripts/e2e-deployments.py` passed 27 of 27 checks against a freshly built backend and
  a real Docker daemon (signed webhook channel, nginx image deployment with resource limits, a
  crashing image failing at the health gate with the transcript and evidence the operator sees,
  restart, pause, and a Redis blueprint deployment with a generated secret).
- Frontend: `bun run lint`, `bunx prettier --check` over the deployment tree, `bunx next typegen &&
  bunx tsc --noEmit`, and `bun test src` (64 tests) passed. `bun run build` passed in an isolated
  copy of the tree, and the complete browser suite ran against that production build:
  the ten deployment specs (`deploy-projects`, `deploy-new`, `deploy-new-flow`,
  `deploy-project`, `deploy-run`, `deploy-settings-a`, `deploy-settings-b`, `deploy-notifications`,
  `deploy-credentials`, `deploy-source`) passed 144 of 144, and the sixteen remaining specs passed
  136 with 16 conditional skips and no failures — 280 checks in all.
- Screenshots of every rebuilt screen at 1280, 1720 and 390 wide were reviewed (projects, archived,
  credentials and its add sheet, new project with unfinished setups, overview with its menu and the
  duplicate dialog, deployments, runtime, the deployment page, all nine settings sections); no
  page scrolls sideways at 390.
- Not verified live: a real GitHub push, provider webhooks and pull-request previews against a
  real provider, Discord/Slack/Telegram/e-mail delivery, public TLS issuance, and credentials
  against a real remote host (SSH and bearer plumbing were verified against a local bare
  repository and a refused loopback connection).

## Pictures — 2026-09-19

Four screens gained a drawing of the thing they are about, and the library components that draw
it were brought in through the shadcn registry (Magic UI, rewritten onto the design system's
tokens; see `docs/internal/frontend/design-system.md` §11):

- **Credentials:** the GitHub App as the accounts that installed it, the App and this server,
  with the traffic between them as lines (`github-app-card.tsx`, `wire.tsx`).
- **Deployment page:** the release path as a timeline — one bar in seven segments, each as long as
  the stage took, the working stage's name lit by `ui/text-shimmer` — and a burst of paper when a
  release goes live in front of the reader (`run-pipeline.tsx`). Details rows open to the step's
  evidence and timings and lead to the build console only where the step wrote to it
  (`run-steps.tsx`); a stage with no output says so in the console. Metrics draws the before and
  after windows on one scale with sparklines and reads the change (`run-metrics.tsx`); Runtime
  logs says what the stream is and where the two views look (`run-logs.tsx`).
- **Project overview:** the preview is one tile that is the website laid out at desktop width and
  shrunk, and one link that opens it (`site-preview.tsx`); the column beside it is the way a
  request reaches the project — source, live release, runtime, domains — drawn on an opaque ground
  so the line never shows through a mark (`project-wiring.tsx`, `wire.tsx`); the delivery insights
  sit under it, and the usage tiles carry the last hour's shape beside the live figure.
- **Projects and the project header:** a card, a row and the title carry the website's own icon,
  read through `GET /deploy/{id}/favicon` — the dashboard's image policy allows its own origin
  only — bound to the recorded website address, one host, one megabyte, remembered for an hour
  (`project-mark.tsx`, `handlers_deploy_favicon.go`).
- **Notifications:** every deployment on the left and a mark per channel on the right, with dashed
  rings an administrator presses to add a kind not yet set up; the rows under it are the list.
- **Projects:** cards land one after another, a card whose run is in progress carries a light
  around its frame and a seven-dot release path beside its state; the in-progress rows carry the
  same dots. **New project:** "Start with something ready" is a bento of the five other ways in.
  **Deployments:** the delivery figures count up.

The deployment, project, projects, credentials and design-system browser specs pass against these
surfaces; the details and preview scenarios were rewritten for the rows that open and the tile that
is a link.

## New project — 2026-09-20

`/deploy/new` was the one screen in the rebuild that still read as a form with a picture stuck
beside it, and four of the decisions it exists to make could only be made after the project was
already running. Both were fixed in the same pass.

- **The plan is the picture** (`new-project/plan-wiring.tsx`). The configure screen draws what the
  setup will create as the four things a request passes through — source → build → runtime →
  address — in the `wire.tsx` vocabulary the project overview already uses for a deployment that
  exists, so the screen where a project is planned and the screen where it is read agree about what
  a project is. Nothing pulses: a pulse means live traffic and a plan has none. A step not yet
  decided is a dashed line to a dashed ring, exactly as an undeployed project's release is. Every
  step is pressable and opens the fields that decide it, which makes the drawing the form's table of
  contents — the release strategy and the memory limit are readable without opening Advanced to
  find them, and an unbounded container reads as "No memory or CPU limit" rather than as silence,
  because on one server that is the thing that takes the dashboard down with it. The screen splits
  at `xl` (drawing left, a sticky summary right); below it the plan is read first and the form
  follows.
- **The Git tab's bento is gone.** The "Pictures" pass above put a grid of the five other sources on
  the Git tab; the source strip above it already did that job, so the same six ways in were drawn
  twice, one of them wired to a tab the other did not know about. The space now carries the clone
  options — submodules and LFS — as a disclosure that says it governs every import made on the tab.
- **Four decisions moved to creation**, each one previously reachable only from settings after a
  production service had already done the wrong thing once: whether pushes deploy themselves and
  which paths count (`new-project/automatic-deployment.tsx`, committed as the environment's
  `deploy_git_policies` row inside the commit transaction — no decision writes no row, so every
  other caller keeps `gitDeploymentPolicy`'s defaults); a second and third hostname; what an image
  is (project type is offered for an image source, not only a Git one, so an HTTP application
  shipped as a container can be told it is one and earn the readiness gate and candidate-first
  activation preflight requires for it); and whether an unfinished setup is still wanted —
  `DELETE /deploy/drafts/{draft}`, audited, owner-only, refused once committed, and called by the
  page itself when a second source is inspected in place of the first.
- **What the screen knew but did not say.** An image's exposed port is read from its own
  configuration (`imageExposedPort`, lowest TCP port, absence tolerated) instead of arriving as
  zero; a project name a live project already holds is reported by `GET /deploy/hostname?name=` as
  `nameTaken` — a pointer, so "not asked" stays distinguishable from "free" — and flagged while it
  is typed rather than refused by the schema's UNIQUE constraint at commit; what detection could not
  settle (`needsDecision`) is shown on the source row; all eight build recipes are offered rather
  than the four that fit on the old control; and an acknowledged warning now carries its remedy and
  its owning page's link, which is how `backup_policy_missing`'s deep link was found to be drawn
  nowhere.

Deliberately not done: no `runtime_unbounded` finding (a warning that fires on every plan and has to
be acknowledged on every deploy teaches operators to acknowledge without reading — the plan drawing
says it instead), no `readiness_missing` for image profiles (it is blocking severity and would refuse
existing image deployments), and no second server, second environment or promotion — those are an
architectural change (R12), not a change to this page.

Verified: `go build ./... && go vet ./... && go test ./...`, the deployment race packages, the
frontend lint, type-check, unit and complete browser suites against a clean production build, and
`scripts/e2e-deployments.py` against a real backend and Docker. Screenshots at 1280, 1720 and 390
were reviewed; nothing scrolls sideways. Not verified live: a real GitHub push through the new
`gitPolicy`, and public TLS for a second hostname.

## The chooser — 2026-09-20 (second pass)

The 2026-09-20 entry above rebuilt Configure and left the half of `/deploy/new` the reader arrives on
untouched. Opened side by side with the host Overview it failed §15's passes 3, 7 and 9 outright, and
the operator's complaint named all three without using their numbers: the page had no life, it never
said which of the two GitHub identities was connected, and the tabs other than Git were dry.

- **One measure.** Five of the six sources rendered into `mx-auto w-full max-w-2xl` while Git rendered
  full width, so pressing a tab collapsed the page from its own width to a 672px centred column under
  a title and a strip that stayed full width. Every source now starts on the page's own left edge
  (§15 pass 9). Git, Docker image and Template are two columns at `xl` — the list and the thing you do
  when the list has not got it (paste a URL, pull from a registry, fill in the chosen template);
  Compose, Database and Existing workload are a left-aligned form, because a form does not want 1440px.
  The Template tab's second column exists only once a template is chosen: a reserved empty column
  reads as a layout bug.
- **Both GitHub identities, said once.** The App was inferred from `(appRepos.data?.length ?? 0) > 0`,
  so an App installed on an account that had granted it nothing reported as no App at all and the page
  offered to connect one that was already connected. The Git tab now reads `GET /deploy/github-app/`
  through `hooks/use-github.ts` (lifted out of `github-app-card.tsx`, which had owned the hook since
  Credentials was the only caller) and opens with two rows — the App and the CLI — each carrying a
  `Status`, what it grants, how many repositories it is responsible for, and the repair for whichever
  is missing. `installUrl` was in the type and drawn nowhere; it is now the block's action *and* the
  remedy inside the empty result of a filter, which is where "my repository is not here" is actually
  felt. A GitHub-side failure degrades to one muted line: the page used to paint `ErrorState` across
  the top of the import path whenever an optional integration was unreachable, which every run of
  `deploy-fixture.ts` reproduced as a red "Not mocked" banner because both App routes were unmocked.
- **Provenance is a heading, not a tag on every row.** `<Tag>App</Tag>` at a row's right edge was three
  characters at 10px with nothing on the page defining them. The rows are grouped under the identity
  that clones them, and only when there is more than one.
- **The rows are the target.** Ten outline `Import` buttons down the right edge became §12's shape —
  the name is the control and carries the verb in its accessible name, the row around it answers the
  pointer — on the repository, image and template lists alike. A template that cannot be deployed has
  no control at all, so nothing announces one. A press that takes a second says `Importing…` (§13).
- **Recency.** The list arrived in whatever order the API answered in. `pushedAt` was on
  `GitHubRepoSummary` and discarded by the picker's own projection; it did not exist at all on
  `GitHubAppRepository`, which serves nearly every row on a dashboard with an App, so
  `internal/githubapp` now carries `pushedAt`, `fork` and `archived` through the installation listing.
  Newest push first, anything that never reported one last.
- **Both scroll containers grew a sideways scrollbar** — §15 pass 3 names this defect verbatim, and it
  was visible in the screenshots the complaint arrived with. A container over bleeding rows pays the
  bleed (`-mx-3 px-3`), and the caps went from `32rem`/`20rem` to `min(60vh,42rem)`.
- **Unfinished setups stopped opening the page.** Four rows, two lines each, above the thing the reader
  came to do. One line each now, with a count in the header and `expiresAt` — on the wire since the
  type was written, drawn nowhere — said only within three days of lapsing, because every draft expires
  and "29 days from now" on all four is a column of the same word.
- **The strip carries a mark per source.** Six words in a line are six words to read; the glyphs are
  wayfinding in §14's sense (the reader is choosing between kinds of thing, not being told what page
  they are on) and every one is `aria-hidden`, so each button's accessible name is still exactly its
  label.
- **Nothing was installed.** The 2026 registry landscape was surveyed — Magic UI, Aceternity, Motion
  Primitives, Kibo, cult-ui, Origin/coss, Base UI, Ark UI, React Aria, Tremor, shadcn's own `Item`
  and `InputGroup` — and every candidate either duplicates a primitive in `components/` or sells the
  gradient, glow, shadow and hover-transform §15 bans in one sentence. `ui/bento-grid.tsx` went the
  other way: dead since the 2026-09-20 pass removed the Git tab's bento, it is deleted, and the §11
  sentence still describing it as shipped is corrected.

Verified: `bun run lint`, `bun run build`, `bun test src` (80), the complete browser suite against the
production build (336 passed, 16 conditional skips), `go build ./... && go vet ./...`, and
`go test ./internal/githubapp ./internal/api`. Screenshots of all six sources at 390, 1280 and 1720
were reviewed, and no tab scrolls sideways at any of those widths or at 768. Not verified live: a real
GitHub App installation listing (the new `pushedAt`/`fork`/`archived` fields are exercised against the
package's fake GitHub only), and a real `gh` sign-in.
