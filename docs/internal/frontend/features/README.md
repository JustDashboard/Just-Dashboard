# Features

Every App Router route area: what its pages are responsible for, then, under **Implementation**,
the files that own it and the specs that check it. One file per section, so a page's pull request
edits its own section's file.

| File | Route areas |
| --- | --- |
| [`workspace.md`](workspace.md) | `/hub/enter`, `/login`, `/welcome`, `/excalidraw`, `/remote-desktop`, `/dashboard/*`, `/account`, `/audit`, `/assistant`, `/files`, `/git`, `/logs`, `/terminal` |
| [`github.md`](github.md) | `/github`, `/github/callback`: the list, setup and sign-in |
| [`github-repository.md`](github-repository.md) | `/github/[owner]/[repo]`: one repository and its views |
| [`host.md`](host.md) | `/`, `/map`, `/backups`, `/docker/*`, `/metrics`, `/packages`, `/processes/*`, `/system-users/*` |
| [`databases.md`](databases.md) | `/databases` |
| [`deploy.md`](deploy.md) | `/deploy` |
| [`network.md`](network.md) | `/network/*` |
| [`proxy-security.md`](proxy-security.md) | `/proxy/*`, `/security/*` |

The rest of this page is what spans the sections.

The App Router keeps route entry files thin where a feature has reusable panels and colocates page-only
orchestration where splitting it would hide the workflow. Backend capability checks remain authoritative;
hiding a control through `useAuth().can()` is affordance only.

## An address no page claims

`app/(dashboard)/[...missing]` hands every unclaimed address to `(dashboard)/not-found`, inside the
shell. It draws `components/missing-page.tsx`: the address asked for on a dashed ring, wired to this
server by a line nothing travels, and beyond the server the pages that are there. The nearest of
them is the page of the navigation whose own address the path starts with and the account's role
reaches (`missing-page-model.ts`, unit-tested), and is the page's command; *Back to the Overview*
is the second way, and the only one for an address under no page. `tests/browser/missing-page.spec.ts`.

## Navigation between features and shared interactions

Cross-feature navigation is intentional: Files and Docker can open Git or a terminal in context; Git can
open GitHub authentication; deployments embed scoped runtime logs, service metrics, and automatic live
website previews while retaining links to owning Docker, proxy, backup, files, and terminal surfaces.
A page that shows a service's log — a database's, a container's or stack's, a unit's, a PM2
application's, a site's, the proxy engine's, certbot's, the Security pages' and Packages' — reads it in
place through `components/logs/service-logs.tsx` (at a page's foot, through `EmbeddedLog`); on a
phone a page-foot log is a card per source that opens `/logs` on it, because a console inside a
scrolling page is the stacked box D12 rules out. `/logs` gathers the same readings and views in
one place.
Query parameters that trigger a one-time terminal/file context are consumed
so refresh does not repeat an action.

Files supports `entry=` alongside a parent `path=` to open a file's inspector without trying to
list a regular file as a directory. Container Configure is an administrator-only manual editor in
`docker/container-configuration.tsx`; it preserves reviewed credentials and mounts, previews the
specification and confirms replacement. Compose containers open their owning stack's Compose file.
Services links honor `?state=failed`. Failed or stale readings retain the existing operation controls.

Page-owned keyboard commands, history, restoration and polling interactions are specified in
[`../patterns/keyboard-and-place.md`](../patterns/keyboard-and-place.md). Their shared owner is `components/workspace/`;
page-specific navigation, confirmations and API capabilities stay with the feature components above.

Connection-path **Run and save** retains a typed source-sensitive report through the existing saved
run lifecycle. Both quick and saved reports use `network/path-report.tsx`; the exact-scoped comparison
includes source identity/address, protocol/family/port, selected address/mark and measurement choice.

## Requested diagnostics and operation errors

`components/finding-list.tsx` still renders requested TLS/network diagnostics, nginx lint and
certificate issuance reports. The requested operation keeps its measured result, limits and named
action; extended evidence opens in a popover off the finding. Actual deployment validation errors retain links to the
matching settings field. These reports use the operation's capabilities and confirmation flow.
The [2026-10-12 actionable-findings audit](../../../audits/2026-10-12-actionable-findings/README.md)
records the historical system before its automatic advice features were removed.

## Advisory removal (0.7.5)

The current routes omit automatic Attention/Needs attention/What to change panels, related filters,
issue badges, settings comparisons and navigation marks. The database Advisor route is removed.
Docker, Security, project/fleet, proxy, Backups and Network overview no longer request advisory
endpoints. PM2 details no longer add a heuristic attention panel. Editable settings, updates,
certificate operations and requested diagnostics remain. Network read failures remain visible as
errors; Gateway retains manual admission-rule repair, behind the admission word's Policy evidence and in the
page's menu. See the owning backend guides for retired
routes and retained operation checks; the 2026-10-12 actionable-findings audit is historical.

## Non-network audit repairs

The [2026-10-14 repair matrix](../../../audits/2026-10-14-non-network-fixes/README.md) records the 34 findings
from the 94-route audit, their owning features, regression coverage and remaining integration limits.
Route inventory and capability ownership remain unchanged; the fixes correct existing save, handoff,
status, loading, error-recovery and authentication behaviors. `/network` pages are excluded.

## Where the files are

Until 0.9.0 the repository map held these notes as three table cells; the map is one row per
folder now, and the notes are here, as they were, until each moves into its section's file.

**`src/components/` feature folders.** Feature panels, forms, tables, dialogs, visualizations, and workspaces. `alerts/` is the alerts kit the alerts pages share (`kit.tsx` and `kinds.ts`: the kinds' tiles and cards, the picture, the rule sheet and form; `alert-parts.tsx`: the channels a rule tells with their tests, the all-clear, a subject's card, a line of history, a channel in the picture); `metrics/alerts-page.tsx` is the host's Alerts page and `metrics/alerts-link.tsx` the way to it from Metrics; `metrics/capacity-forecast.tsx` is Metrics' Capacity forecast, its words in the unit-tested `lib/capacity.ts`. `overview/` holds the Overview's readings and storage band, its shortcuts (`shortcuts.tsx`), the shells and recent drawings and boards (`workspace.tsx`), the databases and backup jobs (`data.tsx`), the module rows (`services.tsx`, their sentences in the unit-tested `service-words.ts`) and the unit-tested order each capped list takes (`order.ts`), and the recent activity list (`activity.tsx`) with its unit-tested action/target and outcome presentation (`activity-event.ts`). `database/` is one directory per area of the Databases section — `fleet/`, `connect/`, `home/`, `data/`, `query/`, `schema/`, `diagram/`, `search/`, `generate/`, `import/`, `redis/`, `mongo/`, `ops/` — around what they share: the engine registry (`engine.ts`), a release's support status in words (`support.ts`), the section's shell (`shell/`: the two contexts, the header every page of a database opens on and the rule for the pages that do not (`database-header.tsx`, the unit-tested `header-rule.ts`), the identity strip, the rail scope, routes and the remembered place), the one head the four workbenches share (`workbench/chrome.tsx`), the kit (`kit/`) and the data grid with its change set (`grid/`). `deploy/` also holds the section's shared decisions and drawings beside its pages: what a project is and what it is drawn as (`vocabulary.tsx`, `service-product.ts`; the project's mark is the shared `components/project-mark.tsx`), its verbs and a run's (`project-verbs.tsx`, `run-verbs.tsx`), a pull request's from a project (`pull-request-verbs.tsx`, `pull-request-picker.tsx`), a run as a row and its starter as a mark (`run-row.tsx`, `run-marks.tsx`), the fleet's decisions and card (`fleet.ts`, `fleet-card.tsx`), the Overview's four live readings (`live-readings.tsx`), the picture the section pages open on and a project as its node (`section-picture.tsx`), a console or a game page with nothing to open (`project-empty.tsx`), a line of facts that wraps (`fact-line.tsx`), a request's parts and the traffic drawings (`request-marks.tsx`, `latency-readings.tsx`, `traffic-strip.tsx`, `add-alert-sheet.tsx`), the HTTPS field toggle (`https-toggle.tsx`), the shared variables both their places draw and the picker a variable reads one through (`shared-variables.tsx`, with the unit-tested `shared-variables-model.ts`), the secret store picker and a store reference's label (`secret-store-picker.tsx`, with the unit-tested reference grammar in `secret-stores.ts`), a migration rehearsal's report on the run page and its terms in Build settings (`run-rehearsal.tsx`, `settings/rehearsal.tsx`, with the unit-tested `rehearsal-model.ts`), a traffic replay's report on the run page and its terms in Runtime settings (`run-replay.tsx`, `settings/replay.tsx`, with the unit-tested `replay-model.ts`), an environment asleep by design on the Overview and the sleep terms in Runtime settings (`sleep-panel.tsx`, `settings/sleep.tsx`, with the unit-tested `sleep-model.ts`) and a variable's mark and type control, which both lists share (`settings/variable-type.tsx`), the warm-release window the Overview opens on and its terms on Runtime settings, skew protection among them (`release-watch.tsx`, `settings/rollback-watch.tsx`, with the unit-tested `rollback-watch-model.ts`), the Right-size card and the fleet's list of limits that are off (`right-size.tsx`, with the unit-tested `sizing-model.ts`), which plans may run replicas and how a replicated release reads (the unit-tested `replicas-model.ts`), a release container's process flags as Runtime settings types them, one entry per line (the unit-tested `runtime-flags-model.ts`), a domain's DNS record sheet on Domains settings (`settings/dns-record-panel.tsx`, with the unit-tested `dns-record-model.ts`), a canary's terms and its trial on the run page (`run-canary.tsx`, with the unit-tested `canary-model.ts`), the line only a game server can draw (`game/identity.tsx`), the Overview's placeholder (`overview-skeleton.tsx`), the deployment's feed of the pending strip (`settings/pending-changes.tsx`), `settings/dotenv.ts`, `automation/use-automation.ts`, `automation/wiring.tsx`, `automation/marks.tsx`), who reaches a route and what a sign-in on it would be refused for, said before the save (`settings/route-access.ts`), where a route's maintenance switch stands and what its pages answer, in words (`settings/route-pages.ts`), and a project's Logs page's decisions and views (`logs-model.ts`, its strip `logs-strip.tsx`, the window's sentence `window-headline.tsx` with the unit-tested `window-headline-model.ts`, a request's inspector `request-inspector.tsx`, `project-output.tsx`, `request-lines.tsx`, `output-lines.tsx`, `run-stream.ts`, which moves a run by its stream's events in the unit-tested `run-events.ts` and holds its transcript in the unit-tested `transcript-buffer.ts`, and a build's time in a sentence with how long a run usually takes, in the unit-tested `build-timing.ts`). Since 0.8.7 an item's detail is a page beside its list as a rail, and the page and the rail sit with the list's own files: `docker/` (`image-page.tsx`, `volume-page.tsx`, `network-page.tsx`, each with its `*-rail.tsx`, the rails of a container's and a stack's pages with `beside-rail.ts`, the line every Docker page opens on in `docker-identity.tsx` and the overview's `engine-picture.tsx`), `procs/` (`pm2-app-page.tsx`; `no-systemd.tsx` is the screen of a server without systemd), `packages/package-page.tsx`, `proxy/` (`site-page.tsx` with its Settings in `site-settings.tsx`, `certificate-page.tsx`, `stream-page.tsx`, `port-page.tsx`), `network/` (`interfaces/device-page.tsx`, `egress/egress-page.tsx`, `vpn/tunnel-page.tsx`, `connections/address-page.tsx`, `dns/engine-page.tsx`, `traffic/container-page.tsx` and `program-page.tsx`, and `capture-page.tsx` and `run-page.tsx` over `item-rail.tsx`), `security/jail-page.tsx`, `system-users/` (`account-page.tsx`, `group-page.tsx`), `audit/day-strip.tsx` for the audit log's day, and `database/ops/` (`account-page.tsx`, and `performance-detail.tsx` under a statement's, a table's and a session's). `logs/` is the service logs every page embeds (`service-logs.tsx`) and the `/logs` workbench around them: the workspace, filter, lens row, console, line detail, Insights, readings, a value drawn as what it is (`field-value.tsx`), the page views a source is offered (`service-views.tsx`, `service-views-model.ts`) and the request records (`request-records.tsx`). Each surface's own log readings sit with it: `database/ops/queries-view.tsx` and `queries.ts`, `docker/container-events.tsx` and `event-marks.tsx`, `procs/unit-runs.tsx`, `proxy/site-page.tsx`, `site-logs.tsx`, `site-log-plan.ts`, `engine-log.tsx` and `renewal-log.tsx`, `security/log-section.tsx` and `host-logs.ts`, `packages/log-view.tsx` and `package-logs.ts`

**`src/components/` top level.** The shell in `shell/` (the sidebar, the nav registry `nav.ts`, the nav scope, the command palette and the tab title), the settings kit in `settings/` (the setting card and picture, the pending strip, the floating save bar and its words, a section's facts line with its ⓘ, and `settings-column.tsx`, the centred column of a settings page), the wiring marks and pictures in `wire/`, page/panel/state primitives, editors, icons, and shared confirmations, plus the drawings more than one section shares: the last attempts at something as a strip (`outcome-strip.tsx`), a schedule built from words (`schedule-builder.tsx`, Backups' and a deployment's), a transcript's lines and their drip (`transcript-line.tsx`, the dashboard's own runs and a deployment's build), a client drawn as itself (`client-mark.tsx`, a session's and a request's), the picture of things wired through one hub to other things (`wire/wire-flow.tsx`, which most pages' opening pictures are drawn with, its layout unit-tested in `wire/wire-flow-model.ts`), a reading as one line of words with its last hour (`reading-line.tsx`, what a tile row became), the screen of a thing that is not there (`missing-thing.tsx`, its words in the unit-tested `missing-thing-model.ts`) and of an address no page claims (`missing-page.tsx`, `missing-page-model.ts`), a line of words held open while it is read (`bone.tsx`), the identity header every item page opens on (`identity-header.tsx`), `segments.tsx`, `sparkline.tsx`, `chart-panel.tsx`, `live-figure.tsx`, `section-link.tsx`, `user-avatar.tsx` and `project-mark.tsx`, and one pane at a time with a bar at the foot on a phone (`phone-panes.tsx`)

**`src/lib/`.** Typed API URLs/requests, view stores, formatting, themes, terminal behavior, metrics transforms, Docker templates, what a pasted token or key says about itself (`secrets.ts`), what a pull request and its preview say about themselves (`pull-requests.ts`), what a typed scan or watch target names and which targets the TLS report offers (`scan-target.ts`, sharing its case table with the backend), what the TLS report reads off a scan — where a plain-HTTP visitor ends up, how far a failed scan got and where to look (`lib/tls/report.ts`) — the logs' vocabulary — source ids (`lib/log/sources.ts`), the lens registry (`lib/log/lenses.ts`, held to the Go lenses' golden), attr kinds (`lib/log/fields.ts`), predicates and filters (`lib/log/filter.ts`), readings' searches (`lib/log/insights.ts`), view settings (`lib/log/view.ts`) and the tokenizer (`lib/log/tokens.ts`) — Docker events folded into crash loops and bursts, exits that answered a stop, and each container's part of the record for the Events page (`docker-events.ts`), a unit's runs (`unit-runs.ts`), the Certificates page's readings (`certificates.ts`, including the renewal tile's, and `private-certificates.ts` for the certificates made on the server), the recent runs' chip labels (`recent-jobs.ts`), the host alerts' kinds, words and form ranges (`host-alerts.ts`), the single sign-on presets a Proxy site and a deployment route share (`sso-providers.ts`), and off its deep scan: which suites a filter keeps, which findings the quick report has not listed, and how each connection feature reads (`lib/tls/deep.ts`) — and pure helpers
