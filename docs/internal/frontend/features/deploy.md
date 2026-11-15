# Features: Deploy

The route areas of `/deploy`: what each page is responsible for, then, under **Implementation**, the
files that own it and the specs that check it.

## `/deploy`

Routes: `/deploy`, `/deploy/notifications`, `/deploy/credentials`, `/deploy/variables`,
`/deploy/drains`, `/deploy/templates`, `/deploy/new`, `/deploy/[id]` and project subpages

Projects, in-progress runs and native state filters; project Overview, Assistant ([the deployment
assistant](../../deployments/operator.md)), Deployments, Logs, Runtime, Console and settings, read for
production or — through the header's environment switch — staging, with Add staging, Promote,
promotion-only and, on a pull request's row of the Overview, Promote to staging for its preview's
image ([staging and promotion](../../deployments/staging-promotion.md)). Operations show observed
runtime, domains, storage, backups and dependencies. Deploy/version/retry queue work directly;
source detection and actual execution preflight/readiness checks remain. New-project planning, Git
automation, credentials, shared variables and notifications retain their controls. Automatic
fleet/project attention panels and optional Before you deploy reports were removed in 0.7.5. Build
settings carries the migration rehearsal's terms under the release tasks (`settings/rehearsal.tsx`):
the switch, the database to restore when the tasks reach several, whether a release that cannot be
rehearsed stops or continues with a warning, and the restore and size limits, with a note naming a
task the rehearsal cannot run; the run page reads a `rehearse_migration` step's report as one
sentence on the step's mark with a line of facts (`run-verdict.tsx`, its words in the unit-tested
`run-verdicts.ts`) — whether the tasks passed, the restore and its source, the longest lock, the copy
removed — over each
task's exit and every lock with what production will wait for (`run-rehearsal.tsx`, its words in
`rehearsal-model.ts`, unit-tested). While a release just cut over to is on watch, the project
Overview opens on its window (`release-watch.tsx`), in the Overview's block shape: the proxy's mark
with the reading as one sentence (how much of the new release fails and how long 19 in 20 of its
requests take), what it was judged as and the window's countdown under it, then the previous release
and the new one as two strips of one length with their figures beside them, with *Keep release #N*
and *Roll back instantly*; a window that rolled back stays an hour with the reading that decided it,
and one that closed clean is not drawn. Deployments marks the rolled-back release and the warm one,
and the one Live row is the run whose outcome is live now; Runtime settings edits the terms under
*After a release goes live* (`settings/rollback-watch.tsx`, its words in `rollback-watch-model.ts`,
unit-tested) — skew protection too, *Keep each visitor on the release that answered them*, which is
the plan's `runtime.skewProtection` saved as a one-field change of the plan and said back with the
window it lasts; with it on, the Overview's window says how many of its visitors' requests the warm
release, running rather than paused, answered —, and the Releases section offers one to eight
replicas, only to a stateless plan and with the reason when not; the Overview's runtime line reads a
replicated release as *2 of 3 replicas healthy* and the Runtime page's cards say which replica each
is (`replicas-model.ts`, unit-tested). The Releases section also offers Canary beside Blue / green
and Stop first, with its share, window and *Promote on its own*, and the run page draws a canary's
trial while its step runs — the window's countdown, the candidate and the live release side by side
(requests, 5xx share, p95) and *Promote now* / *Abort* (`run-canary.tsx`, its words in
`canary-model.ts`, unit-tested; `tests/browser/deploy-canary.spec.ts`). Runtime settings also
carries the traffic replay's terms under *Traffic replay*, beside the release strategy and saved
with the runtime plan (`settings/replay.tsx`): the switch, the requests replayed, the status drift
allowed, the slowdown factor, the time limit and the path prefixes, each refused under its own field
before the save when the server would refuse it; the run page reads a `replay_traffic` step in the
Verify stage as one sentence on the step's mark with a line of facts (`run-verdict.tsx`) — how many
replayed requests matched, then those drifted against the share
allowed, how long 19 in 20 of the candidate's answers took against the recorded ones, the time taken
and how many were recorded — over each request the candidate answered differently by method, path, recorded status and its
answer (`run-replay.tsx`, its words in `replay-model.ts`, unit-tested). Deployments also carries
each release's data point as a tag, *Restore data as of this release…* under the release's verbs,
and the *Data copies* panel — a copy's address revealed on request, *Compare with live*, *Keep 1
more hour*, *Restore in place…* (a link to the database's Backups page or the backup run's restore,
prefilled by `?recover=` and `?run=&restore=`) and *Remove now*; the rollback dialog's side-copy
checkbox and the run page's link to the copy it asked for (`data-copies.tsx`, its words in the
unit-tested `data-rollback-model.ts`; [data-aware rollback](../../deployments/data-aware-rollback.md)).
Under Production the Overview's **Right-size** card reads the environment's suggested memory and CPU
limits from its retained samples — the verdict and *Apply suggested limits* in its head, each
suggestion beside its p95, peak and limit — and the fleet lists the environments whose limits are
off under the projects, each with *Apply*; both save through the configuration PUT
([right-sizing](../../deployments/right-sizing.md); `right-size.tsx`, words in `sizing-model.ts`,
unit-tested). The Overview's production panel carries *Diagnose* for a role with `service.control`:
a link to the project's Assistant page (`/deploy/[id]/assistant?ask=diagnose`,
`project-assistant.tsx` over `components/assistant/agent/`), which asks the assistant the diagnostic
question about the environment ([the deployment assistant](../../deployments/operator.md)). The
Overview's *Vulnerabilities* panel reads the live release's latest image scan
(`vulnerabilities.tsx`, its words in `vulnerabilities-model.ts`, unit-tested): a figure per severity
with how many have a fix, the moved base that makes a rebuild worth it, an end-of-life distribution,
the fixable findings at the floor, and *Rebuild* — the runs route's own `force_build` of the live
commit — and *Scan now*; Deployments marks a release whose scan found something at the floor, and
Build settings carries the scanning terms (`settings/vulnerability-scan.tsx`) ([image
vulnerabilities](../../deployments/image-vulnerabilities.md)). Templates added on this dashboard sit
beside the reviewed catalogue ([implementation](../../deployments/implementation.md)): the picker on
`/deploy/new` opens on *Your templates* above the shelves, with a *Yours* chip, each a card like a
built-in's drawn by its public image's product, and its empty state saying the two ways to make one
with a link to Templates; a chosen one says *Added … on this dashboard* where a built-in says
*Reviewed*. `/deploy/templates` (Deployments → Templates, `templates-page.tsx`) draws them as the
picker does, a card each (the product its image is, what it runs, Compose stack or image, *Used by
N*), and the chosen card (the first until another is pressed) as a picture over the grid: what it
was made from, by whom and when; the template under its id and version with how many deployments
use it, or why this dashboard cannot deploy it; and each service it runs with its image, the public
one marked. A card's own verbs are *Deploy* (into the picker with it chosen, not offered for a
template that cannot be deployed), *Export as JSON* and *Remove template* (whose confirmation says
those deployments keep running from their own copy); the head counts them (*3 templates · 1 in
use*) and carries *Import…* for an exported file and *Add from Compose file*, a sheet whose *Check*
shows what the template will ask before it is added (`template-preview.tsx`) and says the
catalogue's refusal under the form; empty, it offers the three ways as cards, each with a product's
mark. Settings → General carries *Save as template…* on a deployment that runs published
images (`settings/save-as-template.tsx`): a dialog that reads the live release's plan as it opens
and says what the template will run, ask when it is chosen and ask on the Variables screen, or why
it cannot be one. The words are in `templates.ts` (unit-tested). The new-project build step and
Build settings offer Railpack beside the recipes, a Dockerfile and a static site, with its one-line
description, its provider (Railpack's detection unless one is chosen) and its configuration file,
and the run page reads a Railpack prepare step as what Railpack decided — the release, the provider,
the packages it resolved, its plan's steps and start, and the images it builds from — with
Railpack's own errors first when it refused (`run-railpack.tsx`, its words in the unit-tested
`railpack-model.ts`; [Railpack](../../deployments/recipes.md#railpack)). Runtime settings carries the
release container's process flags under *Inside the container* — the user it runs as, a read-only
root with its writable paths (started from /tmp, and for a static site where nginx writes), and
behind a fold the ulimits and sysctls, each one per line as `docker run` writes them — and its GPUs
under *Container access* (none, every one, a number or named, and whether previews get them), read
and refused line by line in the unit-tested `runtime-flags-model.ts` before a save and under the
field the server's refusal names after one (`settings/runtime.tsx`,
`tests/browser/deploy-runtime-flags.spec.ts`). Domains settings give each saved domain *Open the DNS
record*: a sheet that reads the dry run of `GET /deploy/{id}/domains/{domain}/dns-record` once as it
opens — where the record stands, each set as the line to copy beside what the zone holds now, the
zone and provider whose saved DNS credentials write it, and who created what is managed here and
when — with one button in the dry run's words, *Create the record*, *Correct the record* or *Replace
the record* (which sends `replace`), and a refusal as a notice naming what stands in the way, under
the records to create by hand, leading to Certificates where a saved token is the answer; a run's
`dns_record_writable` finding offers *Create the record*, which opens the page on that sheet by
`?record=` (`settings/dns-record-panel.tsx`, its words in the unit-tested `dns-record-model.ts`;
[DNS records](../../deployments/dns-records.md)).

Five of these pages took the deploy standard's last steps in 0.8.7. A project's Logs reads the hour in
the Overview's `ReadingLine`s where four tiles stood and is one pane with a bottom bar on a phone
([request observability](../../deployments/request-observability.md)). Deployments opens Delivery on the
Overview's sentence on the source's mark with the rate, the frequency, the usual time and the recovery
as one line of facts (`deliveryFacts` in `delivery-model.ts`, each fact's basis its title), its waiting
shape the lead's and the charts' own, and a run's row says its time once, in amber when it ran long,
with no bar under it. A run at work that has written nothing shows the step it is at in the console,
its name lit (`build-console.tsx`). On `/deploy/new` the GitHub App that is not connected is the
Credentials picture, small (`new-project/app-picture.tsx`), with Connect on the section's title line,
the Git column ends on Import, and a shelf of one template stands beside its neighbours in the cards'
columns. A subject's assistant whose settings cannot be read is the off state with *Try again*
(`assistant/off.tsx`), a database's included. A line of facts that wraps is `deploy/fact-line.tsx`,
which never leaves a separator at a line's end.

**Implementation:** `components/deploy/`; see
[`../deployments/implementation.md`](../../deployments/implementation.md); checked by
`tests/browser/deploy-data-rollback.spec.ts`, `tests/browser/deploy-rollback-watch.spec.ts`,
`tests/browser/deploy-rehearsal.spec.ts`, `tests/browser/deploy-replay.spec.ts`,
`tests/browser/right-sizing.spec.ts`, `tests/browser/assistant.spec.ts`,
`tests/browser/deploy-vulnerabilities.spec.ts`, `tests/browser/deploy-dns-record.spec.ts` and
`tests/browser/deploy-templates.spec.ts`

## A deployment's project pages in more detail

The project identity line and Settings → General show read-only production Git monitoring status,
including repository access failures. Branch deployments are automatic after the first deployment;
additional webhooks remain separate integrations. Run labels use a per-project sequence starting at 1,
while permanent run URLs retain their global identifiers. A run that failed or rolled back offers *Ask
why it failed* under its failure (`components/deploy/run-diagnosis.tsx`): the assistant's provider is
asked only on the press, its answer — cause, quoted lines that open the build logs at themselves, steps,
and the exact masked text that was sent — is kept with the run, and an administrator sets the assistant
up from there (`deploy-run-diagnosis.spec.ts`). `/deploy/notifications`
(`components/deploy/notifications-page.tsx`) manages Discord, Slack, Microsoft Teams, Mattermost,
Telegram, ntfy, Gotify, Pushover, e-mail and signed webhook channels with event selection, pause/resume, test delivery and delivery history.
`/deploy/variables` (`components/deploy/shared-variables-page.tsx` over the list in
`shared-variables.tsx`, Deployments → Shared variables) lists the
server's shared variables and Settings → Variables a project's own under *Project variables*, the same
list in both; the server's page opens on a picture of what they reach (each variable, this server, the
projects whose variables read one, a dashed line to a project still on an earlier value;
`shared-variables-reading.ts`) and counts them on its head (*4 variables · 2 not live yet*): each row the name, the `${{server.NAME}}` or `${{project.NAME}}` it is read as, who changed
it when, how many variables read it and, under it, those readers by project and environment (each a link
to that project's Variables page), with *Saved · not live yet* on the row and *(not live yet)* on a reader
whose live release started before the value changed. Copy reference, Reveal value (the audited reveal),
Edit and Remove are its verbs; the editor sheet takes a name (shown as the reference it will be read as),
the Secret/Config type and the value, which an edit may leave empty to keep the sealed one. An
environment variable's editor, with *Reference* on, offers the project's and then the server's shared
variables by name beside the linked databases (`SharedReferencePicker`), and choosing a secret one makes
the variable secret too; a variable reading one draws `server.NAME` or `project.NAME` as a link to where it
is defined. Checked by `deploy-shared-variables.spec.ts`; the Automatic
deployment section on General carries the GitHub commit-status switch. `/deploy/drains`
(`components/deploy/log-drains-page.tsx`, its sentences in `log-drains.ts`, its state word, figure and
line in `log-drain-state.ts`) reads as Notifications does: a picture of the projects, this server and
each drain as the service it sends to (OpenTelemetry, Grafana, the webhook mark for JSON lines; the
formats not added as dashed rings an administrator presses), then one line per drain: the host and
what it carries, a dot and a word (*Shipping*, *Failing 3m*, *Paused*, *Nothing sent*) and one figure
(what waits, what was dropped, else what was shipped), the count on the head (*2 drains · 1 failing*).
The destination's own words and the totals open from the state word. It adds or edits one in a side panel: format, endpoint, sealed
headers, streams — container output, requests and, for an OpenTelemetry drain, traces with the link
its service opens one at — and projects. A request opened in a project's Logs names its trace and
links to it through the first drain that exports the project's traces with a link (`traceLinkFor`). `/deploy/secret-stores` (`components/deploy/secret-stores-page.tsx`, its
sentences and the reference grammar in `secret-stores.ts`) lists the Vault, OpenBao, Infisical, Doppler, AWS
Secrets Manager and Azure Key Vault connections under a picture of the stores, this server and the projects
whose variables read them (`secret-store-state.ts`; the kinds not connected as dashed rings): each store one
line with the product's mark, the name, the endpoint (derived
from the region or the vault name for the last two) with the mount, namespace and sign-in (or the Infisical
environment, the region, the tenant), a dot and a word for the newest test or run's read (*Answering*,
*Refused 2m ago*, *Not tested*) that opens on the store's own words, and *Read by N*, which opens on every
variable that reads it by project, each project a link to its Variables; the head counts them (*3 stores · 1 refused*). The side panel takes the store, the name (shown as the reference it is read as, fixed once
saved), the address, the kind's settings, an optional CA, the sealed credentials and whether previews may read
it; an edit adds Test with an optional path and key, which shows the version found and never the value. An
environment variable's editor, with *Reference* on, offers the connected stores with path, key and an
optional version (`StoreReferencePicker`, `components/deploy/secret-store-picker.tsx`), which makes the
variable secret; a variable reading one draws the store's mark and name, linked here, and where in it the
key is (`StoreReferenceLabel`). Checked by `deploy-secret-stores.spec.ts`. Settings → Runtime exposes memory,
CPU and process limits, the restart policy, a Shutdown section (stop signal, grace and drain
periods — `settings/shutdown.ts`) and the health-check editor; Settings → Storage names each
protected volume's backup standing (`settings/volume-backup.ts`). The Runtime page's service cards carry the
container's own Start, Stop, Restart and Pause/Resume verbs, every published port with its scope
(`runtime-ports.tsx`) and a Details panel read from `GET /docker/containers/{id}` on open
(`runtime-details.tsx`). The Console tab
(`components/deploy/project-console.tsx`) embeds the Docker exec pane on the live release container
for every non-game deployment, using automatic shell detection and the image's user, without
`cmd` or `user` overrides (`console-session.ts`). Its toolbar contains only Terminal actions and
fullscreen; shell, user, container, search, snippet and terminal settings controls are absent.
Runtime's Console action still selects a service through `?service=`. The
`docker.container.exec.open` audit entry records the exec command and user. A console with nothing
to open (no capability, no answer from Docker, no running container; a game server that is not
running, on its Console, Players and Server settings alike) is `ProjectEmpty`
(`components/deploy/project-empty.tsx`): the project's mark, one sentence, the backend's reason
under it and the next page as a button, in the height the pane takes. A game's Players
(`game/players.tsx`) draws each player as `PlayerFace` (`game/player-face.tsx`, the game's
eight-pixel head drawn from the name, since the image policy admits no skin server) and says since
when the page has seen them on (`game/players-model.ts`: the game reports who is online and not
when they joined, so "joined 3m ago" is only for a player the page saw arrive). The shared vocabulary
(`components/deploy/vocabulary.tsx`) is the one place a run state, a project state, a source line or a
release path is put into words, and the one
place a project is resolved to the product it is drawn as (`projectProduct`, `sourceProduct`); a
project's verbs are declared once in `project-verbs.tsx` and a run's in `run-verbs.tsx`, for every
surface that draws them. What a pull request and its preview say about themselves — the tone of the
folded check state, whether the head moved past the built revision, the one word for where the preview
stands, why a project cannot have one at all, what each service of a Compose stack gets in its preview
how a Compose preview's services stand, and how a preview's database branches read, where their data
came from and what a preview of a fork gets — is decided in `lib/pull-requests.ts` so the Git page's
card, the project Overview and the fleet card cannot disagree, and the verbs a deploy project offers a pull request are declared once in
`deploy/pull-request-verbs.tsx`.
