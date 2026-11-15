# Features: Host

The route areas of the Overview, the server map, Backups, Docker, Metrics, Packages, Processes and
System users: what each page is responsible for, then, under **Implementation**, the files that own
it and the specs that check it.

## `/`

Host overview. While the dashboard itself has a problem (`GET /dashboard/health`: a full disk under
its data, a database that refuses writes or reads) a danger `Notice` says so first, to every role,
with the free space or the store's own error; a background task that panicked in the last half hour
is a warning `Notice` naming the worst one and counting the rest. For an administrator, until one
answers, the [usage count](../../backend/usage-ping.md)'s one question comes first, with the report
printed in it. Then the machine's identity line, laid out as a project's header (its distribution,
processor and hypervisor drawn as their marks, kernel, uptime, processes; *Online* while the socket
is open, and *Restart needed* beside it while the packages owe one; **Open terminal** as its command
for an account that has the terminal, and behind `⋯` *Update to …* (the sidebar notice's dialog,
while there is a version an administrator can take), *Update N packages* and *Restart the dashboard*,
which goes to Configuration where the restart is confirmed and followed: there is no route that
restarts the machine); Shortcuts — the four things a visit starts, as lit cards
two by two and four across once the content is 42rem wide, each counting what it adds to and ending
in a glyph for whether it makes something or goes somewhere: New project, New terminal (in the
account's home), New drawing (made in place and opened) and Browse files, every one gated on its
section being in the sidebar and on the capability its destination needs, and those with a
destination real links; a Resources section headed by an All metrics link, holding five live
readings as `ReadingLine`s, each a link to Metrics (CPU with a bar per core, memory, load, network
and disk I/O: a glyph on the measurement's hue, which takes amber or red past its line, the reading
as words, what it is made of under it, its last hour as a sparkline from `sm`, each figure gliding
to every two-second frame; two to a row from `xl`) and then each real filesystem, up to four, as a
capacity bar with its free space and total (one disk beside the disk I/O line, more two to a row
under the lines). The page carries no `StatTile`. Until the socket's first frame the identity line
and the readings are bones at their heights and every block under them is already reading, so
nothing moves when it lands. Then the Deployments section
(up to six of the fleet's own project cards, three below `lg`, worst first, with each project's
product, address, traffic, run strip and a light round the edge while one deploys, a count of those
left out, New project and All projects, or the first-project empty state); the shells open on the
server (worst-first by work: working, holding a program, idle, ended; each drawn as the program in
it, its directory and windows, opening `/terminal?session=`) beside the drawings edited last, up to
four, each its own preview; the databases (the control center's fleet read, worst first, engine and
version, size and objects, environment and whether it answers, opening its home) beside every backup
job (a failed last run first, then running, then by next run, with its destination's mark, next run,
last run and Back up now for `service.control`); the top processes (the Metrics page's `RankList`,
by CPU, memory or I/O, each drawn as its product and opening `/processes/[pid]`) beside the last 24
hours of recorded events (shared audit
product marks, coloured source names, common action labels with literal targets, explicit outcomes,
exact/elapsed times and an administrator-only link to the full audit log); and one `ChoiceRow` per
module, two to a line from `lg`, each the product it is, one sentence and a state (unit-tested in
`service-words.ts`: *9 containers running, 2 stopped*) — Docker (with the products its running
containers are), proxy certificates (amber while one is late to renew, red once one expired),
security exposure, package updates and reboot state, System services (a failed unit leads the
sentence, in red), Git (its checkouts, what is uncommitted or behind, and the forges they push to),
Remote Desktop (its computers drawn as their systems and how many are open, for administrators) and
the dashboard's own version. A row is one height in every state: a bone while its read is out, *Not
on this server* for a module that is absent, *Its reading did not arrive* for a read that failed.
Each block, row and shortcut — with the read behind it — is left out for a
section the account keeps out of its sidebar, and two blocks that share a row take the whole of it
alone

**Implementation:** dashboard root page plus `components/overview/` (`shortcuts.tsx`,
`workspace.tsx`, `data.tsx`, `resources.tsx`, `services.tsx` with `service-words.ts`,
the unit-tested orders in `order.ts`, `activity.tsx` and `activity-event.ts`
(unit-tested), `storage.tsx`, and the readings' figures in the shared `components/live-figure.tsx`,
`health-notice.tsx` with its unit-tested wording in `lib/dashboard-health.ts`),
`components/section-link.tsx`, `components/identity-header.tsx`,
`components/metrics/top-processes.tsx`,
`components/deploy/fleet-card.tsx`, `components/database/fleet/read.ts`,
`components/product-logo.tsx`

## `/map`

The server map (Server → Map, beside the Overview and, like it, never hidden): every system the
server runs drawn as its own diagram — this dashboard (its tailnet, address, stack, store and
backup), each deployment, each Compose stack nobody deployed, each PM2 app, a service something is
wired to, a container or a database on its own, and *Everything else* (what nothing claims, in rows
by kind) — found from the links the server knows and laid out left to right: where traffic arrives,
the thing itself, what it runs, what that keeps, what backs it up. Every kind is its own shape (the
deployment a large card, a domain rounded like a trigger, a database a cylinder, a volume a disk, a
container by its role — a web container's browser bar, a proxy's point), a release stack a frame
round its containers, a checkout, a certificate and the logs round sub-nodes under what they belong
to; what two systems share is drawn in each and marked. A rail lists the systems by what anchors
them and flies the canvas to one (`?system=` opens on it); choosing a box lights its wiring and the
inspector names each line; boxes drag and stay where they were put, and a whole system is carried by
its head, per account; the canvas moves like a whiteboard's (two fingers slide it, a pinch zooms, a
mouse drags open ground) and nothing on it changes size but by the zoom. No bar runs over the
canvas. Below `lg` it is the canvas alone, and a tap floats one *Open* button to the box's page.
Read-only: every box opens its own page. With no place left behind and every system at once too
small to read, it opens on the dashboard's own system; what a source did not answer for is one
line per cause in the inspector ("Docker is not answering: containers, stacks and volumes not
drawn", **Open Docker**); and the inspector's *On this server* is a row per kind that lights that
kind's boxes

**Implementation:** `app/(dashboard)/map/`, `components/map/`, `lib/server-systems.ts` and
`lib/server-map.ts` (unit-tested); [server map](../../server-map.md); checked by
`tests/browser/server-map.spec.ts`

## `/backups`

Routes: `/backups`, `/backups/[job]`

Backup coverage, jobs, destinations, schedules, next runs and recorded outcomes. The page opens on
an identity line: how the jobs stand as its state (a job that failed, one being taken, every job
backed up, with paused ones after), the jobs, what they hold and the next backup as its facts
(`components/backups/readings.ts`, unit-tested), New backup as its command for an administrator and
a `⋯` menu holding Import from a destination… and Refresh. Manual runs,
restore, retention and job configuration keep their confirmed controls. A job may encrypt its
archives (on by default for S3 and B2): its card and identity line say so, an encrypted run carries
a lock on the rail and an Encryption block in its inspector, and the Backup key panel under the jobs
shows the server's key, what depends on it and whether a copy has been exported, with a confirmed
Export key… for an administrator (`components/backups/backup-key.tsx`, `encryption.ts`), and Import
key… for one exported elsewhere, listed beside it. The menu's Import opens Import from a destination
(`components/backups/import-dialog.tsx`, words in the unit-tested `import-model.ts`): this server's
destinations as choices or another typed in with its keys, then the archives it did not write
grouped by server and job, each with what it covers, its size, time and whether a key here opens it,
imported one at a time or a job's whole history, a missing key brought in place, and an optional
copy kept here; an imported job's card and page say where it came from rather than when it next
runs, offer only Test destination and Remove imported job, and its runs restore through the same
dialogs, the in-place one saying per source what it overwrites and what it makes first (a volume
through Docker, a directory) (`tests/browser/backup-import.spec.ts`). A job opens on its own page
(`/backups/[job]`, `components/backups/job-detail.tsx`): its identity line, its picture narrowed to
the one job (its middle the schedule over the next run), one reading as a sentence (what it holds
over how much is kept, with the archive sizes as its trend), and its runs as a rail beside the run
they show, each run one height with its archive's size as a figure, with each run's log, archive
and restores. Automatic overdue/attention recommendations were removed in 0.7.5

**Implementation:** `components/backups/`; backend in
[`../backend/git-backups-users.md`](../../backend/git-backups-users.md)

## `/docker/*`

Native inventory, container state/health/events, live usage, logs, inspect, shell and storage;
images, volumes, networks and Compose projects with their existing ownership guards and confirmed
changes. Configure retains the administrator-reviewed replacement specification for standalone
containers and opens the owning Compose file for Compose containers. Automatic overview findings,
issue badges, failure/anomaly suggestions and recommendation cards were removed in 0.7.5

The overview, Containers, Events and Stacks open on one line (`docker-identity.tsx`, `HostIdentity`
laid out as a project's header): Docker's mark, "Docker" and its version, the page's verdict beside
the name, the server as the first fact, and the page's one command at the far end (Open Deployments,
Create stack, Events' window). The overview (`app/(dashboard)/docker/page.tsx`) reads: that line; the
band of the busiest containers and what last happened to each; the engine as a picture
(`engine-picture.tsx` on `WireFlow`, its nodes worked out in `engine-picture-model.ts`, unit-tested)
with the compose projects and the containers started by hand on one side of Docker and the networks
and volumes it keeps on the other, a line red where a project has a failing container, amber where it
is partly up or a volume has nothing mounting it, dashed where a thing is stopped or never deployed,
six on the left and three of each kind on the right before the rest fold into a count that opens
their page, pointing at a project lighting what is its own; then the compose projects as one-line
cards beside the disk. The table of every container is `/docker/containers` only: the section's
title links to it and the verdict opens it narrowed to what is failing. The page is drawn before
anything is read, and the picture waits at the height it had on the last visit
(`docker.overview.picture.rows`). Stacks (`stacks-tab.tsx`) lists each stack as a one-line card
(`stack-row.tsx`): logos, name and directory, then a strip of its services' states, its state with
how much of it is up, summed CPU and memory and published ports in columns that line up, Deploy for
a stack that is down, an arrow and a menu (View, Files, Open shell); nothing expands in place, and a
stack found by a service's name or image says which matched. A stack's page reads in a container
page's order (the band, the ports-and-networks picture, the services table), and both detail pages
keep their list beside them from `xl` (`stack-rail.tsx`, `container-rail.tsx`, copies of
`procs/process-rail.tsx`): the list's own search, chips and order from its session keys, the item
on screen filled (and drawn first when the filter does not hold it), Alt+↑ and Alt+↓ and the two
arrows walking it with `?tab=` carried. The column itself is one component for a container, a
stack, an image, a volume and a network (`docker/item-rail.tsx`: `DockerRail` and `DockerRailRow`);
each `*-rail.tsx` is its row and where its list comes from.
Beside the rail the blocks that went side by side at 1280px
and 1536px do so at 1600px and 1856px (`beside-rail.ts`)

An image is a page of its own, `/docker/images/[id]` (`image-page.tsx`; the old `?image=` address
redirects there): its identity line with Pull or Tag and the rest in a menu, a picture of its
registry, the image and the containers made from it, the views Layers, Containers, History and
Configuration in `?tab=`, and from `xl` the images as a rail with the table's own filters.
`tests/browser/docker-image-page.spec.ts`

A volume is a page of its own (`/docker/volumes/[name]`, `components/docker/volume-page.tsx`): its
identity line with one command, a picture of the containers that mount it and the backup jobs that
cover it, and its views in the address as `?tab=` — Files, Mounts, Backups, Configuration — with
the Volumes table as a rail beside it from `xl`. `/docker/volumes?volume=<name>`, which opened the
sheet this replaced, redirects to it. Specs: `tests/browser/docker-volumes.spec.ts`,
`tests/browser/docker-volume-page.spec.ts`.

**Implementation:** `components/docker/`; checked by `tests/browser/docker-overview.spec.ts`,
`stacks-ui.spec.ts`, `stack-detail.spec.ts`, `container-page.spec.ts`, `docker-events.spec.ts` and
`docker-ui.spec.ts`; see
[`host.md`](host.md#docker-panels) and
[`../backend/docker-files-logs.md`](../../backend/docker-files-logs.md)

## `/metrics`

Redesigned per design-system §15, last in 0.7.1 to read as the Overview does: the identity line (the
processor drawn as its vendor's mark, cores, memory, swap, hypervisor, distribution, recording
cadence, then the pause, export, range and shortcuts controls); a Resources section headed by the
window the trends cover and the socket's Live state, holding five live readings — CPU with a bar per
core, memory, load, network and disk I/O — each figure gliding to every frame, keyed by its line's
colour, carrying the window on screen as its trend and, on a recorded range, a delta against the
previous window; a notable-moments list that groups the peaks, stalls, failed deploys and reboots
that arrived together into incidents — each led by its heaviest signal (an event outweighs any
spike) with the rest as keyed signals beside it, the window drawn above them as a timeline of marks
— where pressing an incident pins its instant on every chart (pressing again releases it), hovering
one previews it as the charts' crosshair, and a zoom glyph narrows the charts to it and keeps the
pin, beside top processes by CPU, memory or I/O as a `RankList`, each drawn as its product and
opening its own page; then one section per
resource, each reading what it is now beside what it did: Processor (utilisation, total or broken
down, beside every core as a column and the temperatures, a sensor carrying its vendor's mark),
Memory (Allocation, a `BarReading` of what programs hold and what the cache holds on a track that is
all of it, its head saying what is available, and Swap as a second one under it, beside the memory
chart), Network (throughput beside sockets, whose head carries TCP, TIME_WAIT and open file
handles; then how connections fared as three recorded charts — resent segments, whose head carries
the current resent share and median RTT, connection RTT and failed connections — and the interfaces
— the host's real devices by default with Docker's veth pairs and bridges behind an Everything
toggle, as on the Network section's Interfaces page, each its owner's mark and a bar of its in and
out, two to a row on the page's own ground as the filesystems are), Storage (the Overview's
filesystem rows with their scan beside capacity; disk throughput, operations, latency and inodes)
and Saturation (pressure, its head the three stalls now, beside load, its head the processes it
counts). Every series takes its measurement's hue; live and recorded windows, annotations, a
synchronized cursor and pinned moments run across every chart, the pinned moment's strip holding at
the top of the scroll. A dragged span is mirrored into the URL as `from`/`to` so a window is a link,
the live feed can be paused, and the window on screen exports as CSV. Under Storage, the **Capacity
forecast** says when each filesystem fills at the rate the record shows, with the fit's confidence,
beside the five deployments whose volumes grow fastest ([capacity
forecast](../../deployments/right-sizing.md#capacity-forecast))

**Implementation:** metrics page, `components/metrics/` (`notable-moments.tsx` with
`lib/metrics/moments.ts`, `top-processes.tsx`, `hardware-panels.tsx`, `moment-inspector.tsx`,
`capacity-forecast.tsx` with `lib/capacity.ts`), `components/live-figure.tsx` and
`storage.tsx` (shared with the Overview), `lib/metrics/summary.ts`, `lib/metrics/export.ts`

## `/metrics/alerts`

The host's Alerts (administrators; under Monitoring beside Metrics, whose identity line links here
with the firing count): the proxy Alerts page's layout over the machine's own readings — CPU,
memory, filesystems, Docker's containers and backups wired through this server to the channels told
— under an identity line laid out as a project's header: what the rules are doing now as its state
(*N firing*, *N holding*, *Watching*, *No rules*), how many are on, how many channels they tell and
when they were last checked as its facts, **Add rule** as its command and *Check now* behind `⋯`.
Then what fires or holds with its mute, the rules as rows of one height that open their form in a
sheet (each kind's line and minutes checked against the server's ranges before it is sent), a rule
that cannot be judged saying why on its own line, and beside them what was told lately and a test
per channel; a rule's menu also sends its own test. With no rule the kinds are offered as cards, a
name and one fact each; with no channel the section shows the services a channel can be and the way
to add one. The page has its shape while the rules are read. An account that is not an administrator
is told the page is one's, and the server is not asked

**Implementation:** `app/(dashboard)/metrics/alerts/`, `components/metrics/alerts-page.tsx` and
`alerts-link.tsx`, `components/alerts/alert-parts.tsx` (shared with `/proxy/alerts`),
`lib/host-alerts.ts`; backend in
[`../backend/observability-security.md`](../../backend/observability-security.md#host-alerts-told-when-the-machine-itself-goes-wrong)

## `/docker/events`

What the Docker daemon did and who did it, over a 1h / 6h / 24h / All window: Docker's identity line
(the one every Docker section page opens on) with the verdict beside its name (a restart loop named,
the containers that failed, or nothing), the socket's state and, at its end, the window; four readings (failures, restarts, created or removed, external), each counting up
with its trend across the window and narrowing the page to what it counts; the containers the record
names as a table, worst first, each with its state now from Docker's listing, its window as a lane
of outcome-coloured marks (a loop a red band), its failures, comebacks and who last acted on it, a
row narrowing the feed and its name opening the container; and the feed, a plain section (a title,
a hairline, rows on hairlines, scrolling with the page; framed with a scroll of its own until 0.8.7),
restart loops and a container's events moments apart folded into one row each, every row saying who
did it. The buffer
is read whole once and its newest hundred every fifteen seconds, so the polled copy's audit-log
match replaces the socket's; an exit right after a kill is read as the stop it was

**Implementation:** `components/docker/events-page.tsx`, `event-activity.tsx`, `event-feed.tsx`,
`event-marks.tsx`; `lib/docker-events.ts` (unit-tested)

## `/docker/networks`

Docker's networks, who is on each and what they carry. The engine's identity line, laid out as a
project's header (Docker as its mark and version, how many networks and how many were made here, the
containers on them; from `sm` the host and how many join two or more) with the verdict beside the
name: the address pool nearly or wholly spent, else — where the container listing failed — that who
is on them is unread, since then no network is called unused or in use; else the unused networks — a
press narrows the table to them — else every network in use. Create network is the line's one
command. A band of bar readings (`network-band.tsx`, design system §10): **Traffic**, the
five busiest networks by their bridge's two-second counters (`useLiveTraffic`) as spans of one bar
in each network's colour, with in and out gliding on its line; **Addresses**, Docker's address pools
(`default-address-pools` from `/docker/info`, else the built-in thirty-one) as a span per network in
its colour sized by the blocks it holds, the first free block at the strip's end and any subnet
outside the pools in the figure's tooltip; **Recent**, Docker's network events followed live as
ticks on the last day, each naming the container that joined or left. A press in Traffic or Recent
opens that network's page. Then the networks as a framed
table — the network's lane colour down its edge, who made it as counted chips that narrow (compose
project, this dashboard, standalone, Docker's own; the backend's reading of the labels where it has
one, so a deployment's network and a database link are the dashboard's and name the deployment,
otherwise the labels themselves), its subnet and gateway, its members as their products with how
many run (**members unread**, not "nothing attached", where the container listing failed, with a
notice over the page; a failed refresh of the list keeps it with a dated warning), its traffic with
two minutes of shape, and Attach and Remove drawn disabled with the reason where they would fail
(Docker's own, in use, members unread, a swarm network without `--attachable`, the dashboard's own
private network) — and every container as a second table: each network it is on in that network's
colour with its address there, the names it answers to (none on the default bridge, in amber), its
traffic, chips for the containers that join networks, the default bridge and the stopped ones, and a
network's chip narrowing the table to it. A network's name is the link to its own page
(`/docker/networks/[name]`, below) and the row is the pointer's larger target for it;
`/docker/networks?network=<id>`, the address of the sheet that page replaced, redirects there on the
server. A row is one height in both tables and in the lists they become below `xl`: a container on
more than two networks names the first and counts the rest, its names stay on one line with the rest
behind a count, and a network's traffic below `xl` has its place whether or not it carries any. On a
phone the two strips of chips wrap rather than run off the panel's edge. Remove
unused sits in the networks table's toolbar. Attach a container, Remove and Remove unused
each read what they would disturb first (`network-dialogs.tsx`, `network-conflicts.tsx`; the
previews' types and ordering in `lib/docker-networks.ts`, unit-tested): refusals are the server's
too and leave the confirmation off, warnings are confirmed, the attach dialog keeps its container
and extra name when the candidates or the preview cannot be read, and the reviewed prune removes
only the networks nothing names and lists the rest as kept with why; see [guarded
changes](../../backend/docker-files-logs.md#network-ownership-dependencies-and-guarded-changes). Create
network (`network-create.tsx`, with `network-create-reading.ts`, unit-tested) keeps its draft after
Engine refusals or a stale inventory and offers advanced gateway and allocation ranges, dual-stack
pools, attachable, labels and, to an administrator, drivers and options read from the Engine's
catalogue (a missing or refused driver and option keys Docker would ignore are named);
`?ipamReservation=` opens it on a shared address plan's reservation; see [network
creation](../../backend/docker-files-logs.md#network-creation)

**Implementation:** `components/docker/networks-page.tsx`, `network-band.tsx`, `network-table.tsx`,
`network-members.tsx`, `network-live.ts`, `network-dialogs.tsx`, `network-conflicts.tsx`,
`network-create.tsx`, `networks.ts` (unit-tested), `lib/docker-networks.ts` (unit-tested); checked
by `tests/browser/docker-networks.spec.ts`, `docker-network-maturity.spec.ts` and
`docker-network-create.spec.ts`

## `/docker/networks/[name]`

One network, as a place of its own; it was a sheet over the Networks table until 0.8.7. The address
names the network, and an id opens it too (the Engine resolves either, and a deployment's removal
plan names one by id): the address is then put right to the name. A way back and Previous / Next;
the identity line as a project's header — Docker's mark for a bridge or an overlay, the kind of
device otherwise, the name behind the network's own colour, where it stands beside it (**in use**,
**N of M running**, **unused** in amber for a network somebody made that nothing is on, **nothing
attached** for Docker's own, **members unread**) with **no internet** and **IPv6**, then who made it
(a compose project's is a link to its stack), its driver and device on this server, its subnet and
how many are on it. **Attach a container** is its one command, offered where Docker would take one;
the menu holds Open the stack, Copy name, Copy ID and Remove, which is drawn disabled with the
reason where Docker would refuse, as is the reason the dashboard's own network or a swarm network
without `--attachable` takes no container. Removing goes back to the list.

Views in the address as `?tab=` (Overview carries none), remembered between networks:

- **Overview**: the wiring picture (`network-picture.tsx`, on `WireFlow`) — where it leads on the
  left, the network in the middle (its bridge device and subnet), its members on the right as the
  products they run with their address and what each is moving. Where it leads is how its members
  leave it (out through this server at its gateway address; straight onto a network card for a
  macvlan or ipvlan, a link to that card on Interfaces; no way out for an internal one; nothing for
  `host` and `none`, said in a sentence) and then the other networks its members are also on, each
  a link to that network and saying through whom, since a container on two networks is the only
  thing that joins them. A member's line pulses while it moves more than a kilobyte a second, faster
  the busier, and is dashed while it is stopped; pointing at a member lights the networks it joins
  this one to. Five members a side and three leads are drawn and the rest counted, the count opening
  Members. Under it four reading lines: received and sent on its bridge with their last two minutes,
  addresses taken of the subnet with its gateway, and whether its members answer by name (not on the
  default bridge, in amber).
- **Members**: every member as a one-line card (`ChoiceRow`) that opens the container — its product,
  name, **This dashboard** / **Shared ingress**, its address (both families from a wide column) and
  the other networks it is on, the names the others reach it by (two and a count, all in the tooltip;
  **aliases unread** where its inspect failed; where the list is too narrow for the column they are
  the row's third line, on every row), its state and rate — one height whatever it has. Detach reads what it disturbs first; the dashboard's own
  containers and the shared ingress get a locked control with the reason. Nothing attached is an
  empty state with Attach a container. The notices for members unread, members' details unread and
  the default bridge's lack of names are here.
- **Configuration**: subnets, gateway and IPv6; who made it and what acts on it next (Compose
  recreates it, its deployment reconciles it, nothing recreates one made by hand), driver and
  device, whether it reaches the internet and accepts new members, scope, created, id; then the
  driver's options and the labels as written.

From `xl` the networks stand beside it as a rail (`network-rail.tsx`): the table's own search, owner
chip and order (the same session keys), a caption ("11 networks", "3 of 11 · Compose"), Clear
filters, the network on screen filled and kept in view and drawn first when the filter does not hold
it. Alt+↑ / Alt+↓ and the two arrows walk it and the open view goes along. Below `xl` the page is
one pane that scrolls as a whole, the picture's columns stacked with every word kept.

The list is read every ten seconds, for the rail and for who is on the network now; the inspect once,
and again when the list says its members changed (it inspects every member for its aliases). So the
page draws from the list before the inspect lands, and when the inspect fails. It has its shape
while both are on their way (the identity line with the name from the address, the three views, the
picture waiting); a name that is no network is an empty state with the way back and the rail still
listing the rest; a network removed while its page is open goes back to the list.

**Implementation:** `components/docker/network-page.tsx`, `network-picture.tsx`, `network-views.tsx`,
`network-rail.tsx`, `network-live.ts` (the containers socket's listing and rates, shared with the
table), `networks.ts` (the page's address, standing, members, joined networks and way out,
unit-tested); checked by `tests/browser/docker-network-page.spec.ts` and
`docker-network-maturity.spec.ts`

## `/packages`

Reading register without summary tiles: the host identity laid out as a project's header, with the
manager beside the distribution, what is owed as its state, the inventory count and index age as
facts, **Refresh index** as its one command and Re-read and the upgrades behind its menu; a band of
two bar readings (`software-band.tsx`, design system §10): the whole inventory's size (products or
archive sections, largest five and other packages) that filters Installed, beside Updates waiting, a
span per pending package, security first, the first five named and each opening its package's page,
with confirmed upgrade actions at the strip's end; count-bearing views, drawn while the inventory is
still out, and by-hand/everything/dependencies/security/behind chips; from 1024px framed tables of
one line a row (the package as the software it is and its name as the link to its page, the
description as the row's title, version, the changed part of a pending one, the size as a figure in
its column, security/essential at the edge), below it a card a package of one height; catalogue
product choices and lit search-result rows that open the package's page, with separate install
verbs, pending searches hiding obsolete results, and `?q=` opening Add software on a search handed
over from another page; **Log** reads the manager's logs through its lens and stays available after
an inventory failure; unreadable updates report errors, missing sizes/advisory coverage say so. The
list's search, scope, software group and order are one filter (`use-package-filter.ts`, the pure
part in `inventory.ts`) shared with the rail beside a package

**Implementation:** packages page, `components/packages/` (`marks.tsx` and `inventory.ts`
unit-tested), shared job console; checked by `tests/browser/packages-ui.spec.ts`, mocks in
`packages-fixture.ts`

## `/packages/[name]`

A package's own page (`package-page.tsx`; the old `?package=` address that opened its sheet
redirects there, and the name is one encoded path segment, so `g++` and `libc6:amd64` open): a way
back and two arrows, then the identity line, two rows in every state: the package as the software it
is, its name and its state (installed or not, an update or a security update waiting, or what the
manager is doing to it), then the version it is at, the one an update lands on with its changed part
in ink and amber for a security fix, its size and its description, **Install** or **Update** as the
one command and Remove and Remove and purge behind the menu and their confirmations (for an account
with both `system.admin` and `destructive`, and never for a protected package). The job console
opens under the line, a run started elsewhere for the same package is attached rather than offered
again, and the package is read again when the run ends. Views in the address (`?tab=`), carried to
the next package (`package-views.tsx`): **Overview**, the picture of what it needs, the package and
what it gives the server (`WireFlow`: dependencies as links to their pages, dashed where one is not
installed; its commands, its service or services, its configuration and its manual as ways into the
views or the unit's page; every line pulsing while a run is in flight; held to one height for every
package and while loading), why it cannot be removed where it cannot, and About (description,
installed by hand or as a dependency, essential, repository, section, architecture, licence,
maintainer, homepage); **Files**, what its file list put
on the server (commands that copy, services opening the unit with `systemctl start` to copy,
configuration and documentation opening in Files); **Manual**, its man page and its other pages;
**Depends**, everything it needs one line each with the version on the host or "not installed" and
what was asked of it; **Log**, the package manager's logs searched for its name, dpkg's per-package
record first, kept apart from the list's own Log view (`log-view.tsx`'s `about`). No changelog view:
no manager is asked for one. From `xl` the installed list is a rail beside it (`package-rail.tsx`:
the list's own search, scope and order, the first 300 drawn, the package on screen filled and drawn
first when the filter does not hold it, a dot in a logo's corner for one that is behind), walked
with Alt+↑/↓; on a phone the page is the one pane. Not installed, a library with nothing to run, a
name nothing knows (with a search for it) and a failed read are each said in place

**Implementation:** `app/(dashboard)/packages/[name]/page.tsx`, `components/packages/package-page.tsx`,
`package-rail.tsx`, `package-views.tsx`, `use-package-filter.ts`; checked by
`tests/browser/package-page.spec.ts`

## `/processes/*`

Four routed pages listed on the rail's Processes panel, every row drawn as the product it is where
it is one (`product-logo.tsx`: `processProduct`, `unitProduct`, `pm2Product`, `programProduct`) and
a glyph where it is not. Live: the machine's identity line (its distribution as its mark, CPU, load,
available memory and uptime as facts gliding to each socket frame, the process count, and the
table's refresh cadence and row cap, Pause and the shortcuts at its right end), then who is using
the machine (`procs/workloads.tsx`: the five heaviest workloads by processor and by memory — a
supervisor's processes, or the copies of one program started by hand, summed over the whole host
with shared pages counted once — as spans of one bar the size of the machine that ease to each poll,
one of them on the line beneath it (a bar reading, design system §10) and a press narrowing the
table to its workload), over the inventory table whose head counts running, sleeping, blocked and
zombie as state chips beside the ranking as five chips (Automatic, which says why it chose, CPU,
Memory, Disk I/O, Uptime), with owner chips (PM2 and Docker drawn with their marks), the accounts as
chips beside them (the three with the most processes, the rest behind one more chip, the one in
force always drawn) and the chosen workload as a chip under it; fixed columns,
so a long command line never pushes the readings or the row menu past the panel's edge, CPU and
memory as a figure beside a short bar, a container owner by its name, a kernel thread, a zombie and
an unreadable command line each saying which it is, and a process that started since the last poll
rising into place; a row's verbs (open, terminate, kill, pause/resume, hang up, interrupt,
SIGUSR1/SIGUSR2, open its supervisor, copy) in one menu, none of the signals offered to a kernel
thread or a zombie, whose menu opens its parent instead; a row opens the process's own page. **A
process's page** (`/processes/[pid]`, `process-page.tsx`; the old `?pid=` address redirects there):
from `xl` the table folded into a rail beside it (`process-rail.tsx`: the table's own filters,
search and ranking, held while the pointer is in it, the process on screen filled), Alt+↑/↓ and two
arrows to walk it, the view on screen carried to the next process; the identity line (the process as
its product with its state in the corner, the PID to copy, its user, its supervisor with that
supervisor's mark as a link, uptime ticking, its parent, its threads, and its state and wait channel
at the end) under Terminate, Kill and a menu of every other signal and "with its children"; then the
views as the underlined strip, `?tab=` in the address — **Overview** (`process-overview.tsx`:
notices for an exit or a reused PID, a zombie, a kernel thread, a paused process, a binary or
library an upgrade replaced, descriptors near their limit, deleted files held open and a waiting
signal; four live readings over the recent windows; *Where it runs*, a wired picture of what
supervises it and its parent on the left and what it started — copies of one program as one node —
its listening sockets and its open files on the right, each node opening what it is; the command;
who it runs as and how it is scheduled; and its memory as one strip and a list that does not grow),
**Threads**, **Files** (descriptors by kind and the libraries it loaded), **Network**,
**Environment** (the container's table, credentials hidden), **Limits and privileges** (its cgroup's
limits, every rlimit, capabilities, seccomp, and the signals it handles, ignores and holds),
**Control** (`process-views.tsx`: its supervisor's own verbs — the unit's, the PM2 application's or
the container's — every signal with what it will do to *this* process from its signal masks and a
switch to send it to its children too, the priority stepper and the out-of-memory bias as five
presets) and **Logs** (its supervisor's log through the service logs) — and it stops reading and
says so when the process exits or its PID is reused; below `xl` the page is the one pane and scrolls
as a whole. PM2: PM2's own identity line laid out as a project's header (whether it resurrects on
boot as the verdict beside its name, read against the saved list's own names so an application
started since the save turns it amber and offers **Save list**; the account, the Node every
application runs, the boot hook, the last save and the application count as facts; **Start
application** as the line's one command and the daemon-wide save/reload/restart/stop in its menu;
several accounts as rows under it), a band of what PM2
takes of the machine (`pm2-band.tsx`, bar readings: the five heaviest applications by processor and
by memory, a cluster summed into one, as spans of one bar the size of the host beside everything
else in use, easing to each poll, a press narrowing the table to the application with a chip to let
it go), state chips (online, stopped, errored) in the table's head that count and narrow, and a
table of every instance — crashing applications first, each drawn as its interpreter, its uptime
ticking, CPU and memory as figures in aligned columns (memory over the limit PM2 restarts it at,
where it has one; the short meter each row had is gone, since a meter a row is a bar a row and the
band over the list is where they are compared), its restart count rising when it moves with the unstable ones in red, *not saved*
where the saved list lacks it, fixed columns from `xl` with the mode folded into the name line below
`2xl`, every row one height — with start/reload/restart/stop inline and logs, scale, reset counters,
flush, delete behind the menu; a row opens the application's own page. The line and the band are
drawn while the list is read; a server without PM2, one where PM2 is installed and no daemon runs,
and a PM2 that does not answer each say so under PM2's mark with the next step
(`pm2-missing.tsx`). **An application's page** (`/processes/pm2/[app]`, `pm2-app-page.tsx`; the
name is the address, `?account=` only where two accounts run the same name, and the old `?app=`
address — an exact `account:id` or a bare name — redirects there): from `xl` the list folded into
a rail beside it (`pm2-app-rail.tsx`: the list's own search, state chip and order, a cluster one
row, held while the pointer is in it), Alt+↑/↓ and two arrows to walk it with the view carried
along; the identity line (the application as its interpreter, its name and state, its script, fork
or cluster of n, restarts and unstable restarts, uptime ticking) with one command — Start where a
worker is down, otherwise Reload — and the rest in a menu; then the views as the underlined strip,
`?tab=` in the address — **Overview** (`pm2-app-overview.tsx`: notices where PM2 gave up, where
some workers are down and where a reboot would not bring it back; *Where it runs*, the process
page's wired picture with the account's daemon and its boot hook on the left and each worker as a
process, the port they answer on, the log and the working directory on the right; *What it
takes*, four reading lines — CPU and memory summed over its workers from the process table's read of
each PID, memory against the limit PM2 restarts a worker at, disk rates and restarts — that hold
their place while it is not running; the command; how PM2 runs it; and whether it comes back
after a reboot), **Instances** (a cluster only: each worker a one-line card that opens its
process's page, with its own verbs in its menu and Scale on the title line), **Logs** (the service
logs on each worker's `pm2:` source under the record's own facts — restarts, unstable restarts,
uptime — with an Around the last start window only where PM2 stamps its lines, and the server's
reason instead of a socket where the logs are outside the log roots), **Environment** (read from
the running process, credentials masked as on a process's page) and **Control** (every verb as a
row that says what it does, and the startup list). A verb pressed on the page reaches every worker
in turn through the per-process routes (`pm2-app-actions.tsx`); a worker's own verbs reach it
alone. Below `xl` the page is the one pane and scrolls as a whole. Dialogs: a scale dialog
(`pm2-scale-dialog.tsx`) that is a stepper over the workers, those to start outlined and those to
stop struck, with one per core a press away, opened from a row, the Instances view or the menu; and
a start-application dialog whose interpreter and mode are
cards with their marks and which shows the `pm2 start` command it will run, coloured. Services: the machine's identity line (systemd's version, the
active and boot-enabled counts and when it booted as facts, the failed count as the verdict that
narrows the table to them, Reload unit files and the shortcuts at its right end), then
`procs/service-band.tsx`, bar readings — the five services using the most processor and memory as
spans of Live's bar the size of the machine, read from each unit's cgroup, and the last units to
start, stop, finish or fail since boot as ticks on the last day, the line saying when and why — over
the table, whose head counts active, failed, starting and inactive as state chips and whose toolbar
has the startup chips; failed units first, each drawn as what it runs with how long it has been in
its state (up for, failed since and why, its restarts) and its CPU and memory as a figure beside a
bar, a unit no file defines counted in the footer rather than listed, and new units rising while
existing units update state in place; clear-failed and enable/disable on a row, and a row opens the
unit's own page. **A unit's page** (`/processes/services/[unit]`, `service-page.tsx`; the old
`?unit=` address redirects there): from `xl` the table folded into a rail beside it
(`service-rail.tsx`: the table's own filters and search, failed first, held while the pointer is in
it, the unit on screen filled, each row its state in words — up for, why it failed, restarting — and
while it runs its CPU and memory), Alt+↑/↓ and two arrows to walk it, the view on screen carried to
the next unit; the identity line (the unit as what it runs with its state in the corner, its name to
copy, its description, who it runs as, whether it starts on boot, uptime ticking, its main PID as a
link to that process's page and its tasks, and at the end its state and its exposure score in its
tone, opening Limits and security) under Start/Restart/Stop and a menu of reload, clear failed,
freeze or thaw, enable/disable, mask or unmask, reload unit files when this one changed on disk, its
main process, its unit file and copy; then the views as the underlined strip, `?tab=` in the address
— **Overview** (`service-overview.tsx`: a failure, a restart loop, a mask, a unit file changed on
disk with Reload unit files, a frozen cgroup, a skipped condition and memory at its ceiling as
Notices; four live readings over the unit's recent windows, or its last run's counters; *Where it
runs*, the wired picture the process page draws (`wiring-picture.tsx`, shared): what starts it — a
timer or socket, the units it needs with a down one in red, the targets that want it — on the left,
and its main process, its other processes by program, what it listens on and where it writes on the
right, every node opening what it is; every command systemd runs for it, from ExecCondition to
ExecStopPost; how it runs — runs as, startup, type, restart policy and start limit, timeouts, how it
is stopped, watchdog, working and root directory, cgroup, unit file and overrides; and what it has
used since it started from its cgroup's counters), **Processes** (every process in its cgroup,
busiest first, the main one marked, each opening its page), **Journal** (the service logs on the
unit's journal with the **Runs** view — `unit-runs.tsx` over `lib/unit-runs.ts`: a week of the
manager's own lines about the unit, grouped by invocation into runs, how each ended, when, how long,
its exit, systemd's result, the restart count, CPU and peak memory, identical runs folded, a crash
loop said as a Notice, a run opening History on its own invocation — and sshd's unit refused to
anyone but an administrator in a Notice rather than a socket), **Dependencies**
(`service-views.tsx`: every unit it names under the question each answers — what starts it, what it
needs, what it starts, start order, what it cannot run with, reloads — each with its state from one
`systemctl show` of them all, a down requirement in red, a service opening its page and a timer its
schedule), **Unit file** (the file and each drop-in as systemd reads them, coloured as a unit file,
each opening in Files, with Reload unit files when they changed on disk), **Environment** (its
`Environment=` in the container's table, credentials hidden, then the files it reads, what it passes
through or removes, and a link to its main process's live environment), **Limits and security**
(`systemd-analyze security`'s exposure score and verdict in its tone, each setting most exposed
first, then what its cgroup lets it use against what it uses and each process's limits) and
**Control** (every verb as a row that says what it does — now, and on boot — every signal sent
through `systemctl kill` with what it will do to its main process from that process's signal masks
and a switch for every process in it, and its memory, processor and task limits set through
`systemctl set-property` until the next boot or kept); below `xl` the page is the one pane and
scrolls as a whole. Reload unit files (`daemon-reload`) is also on the table's identity line.
Scheduled: the machine's identity line (its distribution as its mark, the account's jobs and how
many are disabled, timers armed, the system cron lines, and the server's zone when it is not the
browser's — every time on the page is on the server's clock, the one its schedules are written in)
with what fires next across cron and the timers, its clock time and a countdown to the second at its
end, then **Next 24 hours** (`procs/schedule-band.tsx` over `lib/schedule.ts`): a chart of the next
24 hours an hour to a column, each schedule that runs in an hour one block in its kind's hue,
stacked, the hours named under the columns from Now, and one fixed line under it that says the day
in a sentence at rest and reads out the hour or block pointed at; under it **Up next**, the
schedules in the order they run, each its clock time, mark, name, schedule in words, kind and
countdown, six until Show all, pointing at a row lighting its blocks across the day; the kinds as
chips that count and narrow, and under the list what the chart cannot draw — work later in the week,
at boot only, switched off, and package cron lines left to systemd. A timer's later runs are read
off its calendar only where it has no random delay. Then one account's cron jobs as a table of the
job (the program's mark, the note or program as its name, the command under it), the schedule in
words over its expression, and the next run as a countdown over its clock time, per-job
enable/disable/edit/remove and the enabled and disabled counted as chips, an empty crontab saying
what a job is for with Add a job; the timers as a table of the timer (its unit's product, its name
and what its unit says it is for), its schedule in words with any random delay, next and last run,
and its state, saying "off after a reboot" only where it is; the read-only **System cron files** as
one table grouped under a row per file, `/etc/crontab`'s worked example left out, `run-parts` lines
named Hourly, Daily, Weekly and Monthly scripts, and a line guarded on `/run/systemd/system` said to
be "left to systemd" with the timer that does its work; and a **Cron log** (the service logs on the
cron daemon's unit journal, else a cron file, else the journal's `CRON` lines, through the cron lens
with its sessions hidden as a chip and its runs and lost output counted on its chips). Every
schedule opens a sheet built from one set of parts (`procs/schedule-sheet.tsx`): the schedule in a
sentence over the expression it was written as, its figures as tiles, **When it runs** — the next
five runs listed beside the week drawn as seven days with a mark per run and a count per day, a
timer's random delay a wash after each mark — then Command, Details and Runs. A job's (`?job=`) has
its next run and runs this week, and the cron log narrowed to its command; a package line's opens
the same sheet without verbs, and one left to systemd says so first and links the timer; a timer's
(`?timer=`) has its next and last run and how long that took, the service it starts, and the
service's Runs with Live and History. The job editor sheet asks what runs, then when — its frequency
a grid of toggles — over a preview of the schedule in words, its week and its next three runs

**Where there is no systemd** (a container, a host started by another init, or the dashboard's own
container without `/run/systemd`), the Services list, a unit's page and Scheduled's Timers draw
one screen (`procs/no-systemd.tsx` over `MissingThing`): the systemd logo, why, **Open Processes**
and the mount to copy. A unit's page is that screen whole, with no rail, whether the list answered
`available: false` or the unit's read answered `systemd_not_running`, and systemctl's own refusal
is behind *What the server said*; on Scheduled it is one block under Timers and the cron jobs
above it are drawn as usual

**Implementation:** processes layout and pages, `components/procs/` (`process-page.tsx`,
`process-rail.tsx`, `process-overview.tsx`, `process-views.tsx`, `process-detail.tsx`,
`process-facts.ts`, `wiring-picture.tsx`, `services.tsx`, `service-page.tsx`, `service-rail.tsx`,
`service-overview.tsx`, `service-views.tsx`, `service-facts.ts`, `service-band.tsx`, `pm2-band.tsx`,
`pm2-shared.ts`, `pm2-facts.ts`, `pm2-app-page.tsx`, `pm2-app-rail.tsx`, `pm2-app-overview.tsx`,
`pm2-app-views.tsx`, `pm2-app-actions.tsx`, `pm2-scale-dialog.tsx`, `pm2-start-dialog.tsx`,
`pm2-missing.tsx`, `unit-runs.tsx`, `units.ts`, `schedule-band.tsx`, `cron-job-sheet.tsx`,
`timer-sheet.tsx`), `components/verbs.tsx`, `lib/cron.ts`, `lib/crontab.ts`, `lib/schedule.ts`,
`lib/unit-runs.ts` (`units.ts`, `pm2-shared.ts`, `pm2-facts.ts`, `process-facts.ts`,
`service-facts.ts` and these unit-tested); checked by `tests/browser/processes-ui.spec.ts`,
`tests/browser/pm2-application.spec.ts` and
`tests/browser/services-ui.spec.ts`

## `/system-users`

Host accounts, under the host's identity line (how many can sign in as its state, New account as its
command, Groups and Account defaults in its menu) and, only when there is something to change, a list
of findings each with where to change it. Then the ways to root as one picture (`RootPicture`, a
`WireFlow`): the accounts that become root on the left, by sudo or by a group that is root by another
name (`docker`, `lxd`, `libvirt`, `disk`), each line in the tone of how strong it is; root in the
middle; and on the right whether root itself signs in by password and by key. Under it every account
is a one-line card (`ChoiceRow`) that opens the account: who can sign in and how (password, keys, or
the one thing closing them), how far each reaches root (read from the sudoers files), and when each
last signed in, in columns from `xl` and on two lines of their own under the name below it, so no
account is taller than another. Root is in the list however the system-accounts chip is set, and
daemons are a shelf of their own drawn as their products. A row's checkbox selects it for lock,
disable, enable, end sessions and delete (one request at a time); its menu goes to that account's
terminal, files, settings and history. The page has its shape while it waits: the line, the picture
and the rows are drawn at the height the last read gave them.

The whole section is `system.admin`, reads included. An account without the role is shown a closed
door on every page of it (`read-state.tsx`) rather than controls that fail.

**Implementation:** `app/(dashboard)/system-users/page.tsx`, `components/system-users/access.ts`,
`marks.tsx`, `root-picture.tsx`, `wire-marks.tsx` and `read-state.tsx`

## `/system-users/user/[name]`

One account, on the shape a service's page has: a way back, its identity line (sign-in state beside
the name; a terminal as that account as the command, `/terminal?as=<name>`, which `/terminal` passes
on as `asUser`, or its files where it has no shell; Files, Disable or Enable, End sessions and a
delete that reads what would go first in the menu), and three views as an underlined strip with the
one on screen in the address (`?tab=`). **Overview**: its password and every SSH key as rows of one
list (set or generate a password, require a change, lock, unlock only when there is a password
behind the lock, remove; add a key by paste, GitHub import or generation, with label, restrictions,
last use and a weak-key warning, and give it to other accounts); its routes to root with the file
each rule is in, its own sudo rule, and its groups; what it is running right now, drawn only when it
is running something. **Settings**: profile, password rules and limits as one centred column of
sections under one floating save bar; they are still three writes, and the bar sends each form that
holds an edit. A disabled account's expiry is not offered and not sent, since an empty one is how an
account is enabled. **History**: sign-ins, refusals, sudo commands and dashboard changes on one line
of time, narrowed by chips; an account with nothing on record is drawn as itself with one sentence.
From `xl` the accounts stand in a column beside it (`AccountRail`) with the list's own search, filter
and order; Alt+↑ and Alt+↓ walk them and the view on screen goes along. A system account is read-only
until `Allow changes` is pressed.

**Implementation:** `components/system-users/account-page.tsx`, `account-rail.tsx`, `detail-shell.tsx`
and the `account-*.tsx` views

## `/system-users/new`

A flow page: what the account is for, as templates (the operator's own beside the built-in ones),
then its name, how it signs in, its root access, groups and shell in the one focused surface, ending
in Create with Save as template beside it. The column beside the form (`NewAccountPicture`; under it
below `xl`) draws what the answers add up to: the way in, the account, how far it will reach. An
account nobody can sign in to has to be asked for by name.

**Implementation:** `components/system-users/new-account.tsx`, `new-account-picture.tsx`

## `/system-users/groups`

The host's groups under an identity line that says how many reach root (a press narrows the rows to
those) with New group as its command. Each group is a one-line card: its mark, its name over its
number, what it hands over in its tone, and who is in it; on a phone what it hands over takes the
line under the name. What reaches root comes first; a private group is hidden and system groups are
behind a chip. A row opens the group's page; `?group=<name>` lands there too.

**Implementation:** `app/(dashboard)/system-users/groups/page.tsx`, `components/system-users/groups.ts`,
`group-parts.tsx`

## `/system-users/groups/[name]`

One group, on the same shape as an account: its identity line (what it hands over as its state, its
number, how many are in it, whether a package made it), three views in the address and from `xl` the
groups in a column beside it (`GroupRail`) with the list's search and switch, walked by Alt+↑ and
Alt+↓. **Overview**: who is in it, the group, and what being in it reaches as a picture, then the
sudo rules that say so, each with its file and line. **Members**: a switch per account (those already
in it that the people's list does not hold as well); an account it is the main group of is said, not
offered. **Settings**: name and number, held for a package's group until `Allow changes` and never
offered for one the system depends on, and deletion. Membership, name and number are one write, saved
by the one floating bar whichever view the edit was made in. It was a sheet over the list until
0.8.7.

**Implementation:** `components/system-users/group-page.tsx`, `group-rail.tsx`, `detail-shell.tsx`

## `/system-users/defaults`

What an account is born under, as one centred column: New accounts (default shell, grace after a
password expires, what `/etc/skel` holds with a link to it) and Passwords (how long one lasts, how
soon it can change, the warning, the shortest allowed and how many kinds of character), each head
naming the file it writes; one floating save bar; and the operator's own templates, one line each.

**Implementation:** `app/(dashboard)/system-users/defaults/page.tsx`, `components/system-users/fields.ts`

## Docker prunes and the confirmation's working bar

Docker's image, build-cache, container, volume and network prunes and the overview cleanup keep a
named working bar and elapsed time in the footer of the active confirmation. These routes still
return a final report rather than intermediate category progress. Compose confirmations can receive
the latest streamed output through the runner's optional output callback, shown on one line in that
footer; the page's full transcript remains available after the dialog settles. Nothing reconnects or
replays a Compose command to obtain progress. Reviewed DNS provisioning, apply and removal show the
reading in their confirmation. One-off network diagnostics, blocklist refreshes and the proxy request
tester run on the page and draw no working bar.

## Docker panels

**Docker** (`components/docker/`) is where the product's opinion about newcomers lives.

- `explain.tsx` is the teaching layer — `Hint`, `Term` (a dotted underline, definition one hover away),
  `Field`, `GLOSSARY` — written for somebody who has never run a container and phrased around the decision
  rather than the mechanism. The rule it enforces is that **explanation is quiet**: a form that shouts
  every caveat is as unusable as one that explains nothing.
- Docker has no standalone create flow. Containers are deployed from `/deploy/new`, which owns images,
  Compose, templates and the rest of a workload's setup (see
  [`../deployments/implementation.md`](../../deployments/implementation.md)). The template list
  was removed on 2026-09-12 and the create form on 2026-09-15. `lib/docker-run.ts`, the lenient `docker run` parser that form used, is
  kept for its unit tests only and has no caller in a page.
- `run-console.tsx` deliberately **does not** let `useSocket` reconnect: reconnecting re-issues the GET,
  and re-issuing the GET runs the command again — a redeploy fired twice because a VPN blinked is not a
  re-render. A dropped socket ends the run and says so.
- Docker pages display native state, usage, events and manually editable configuration. The attention
  list, per-container issue badges, advisory Configuration cards and anomaly/failure suggestion panels
  were removed in 0.7.5. `container-configuration.tsx` retains reviewed manual replacement; Compose
  configuration remains owned by its stack. No page polls the deleted advisory endpoints.
- The containers page (`app/(dashboard)/docker/containers/page.tsx`) is three parts under the engine's
  identity line. `containers-band.tsx` draws the five containers using the most processor and memory
  as spans of one bar the size of the host (a bar reading, `design-system.md` §10), figures gliding to
  each two-second frame, and **Recent**: Docker's event log over `/docker/events/stream?kinds=container`
  as ticks on the last day, a restart loop folded to one tick and an OOM kill said on the exit it
  caused (`foldRestarts`).
  `containers-table.tsx` is the table — sortable headings, a ticking uptime under each state, CPU and
  memory as figures beside short bars (memory against its limit, amber past 85%), the processor's
  hour and the network rate from `2xl`, the compose project in its `hueFor(…, LANES)` hue as a filter
  — drawn down the row below `xl`. `containers.ts` (unit-tested) holds what the rows say: the five
  buckets the state chips count (failing, starting, running, paused, stopped — failing being a restart
  loop, a dead container, a failed health check, a crash exit or an exit the event log says the OOM
  killer caused, since `docker stop` also leaves 137), the exit words, the sort, and the network rate
  from two frames of Docker's cumulative counters.
- `tabs.tsx` gives selection, hover and focus three different mechanisms. They had two: the accent
  outline meant both "this filter is on" and "the keyboard is here", so a keyboard user could not tell
  which filters were applied and tabbing looked like the selection moving.
- `exposure.tsx` draws a published port so the two cases that matter look different.
- `container-cells.tsx` holds the three cells that were saying something other than what they meant:
  memory against an explicit container limit, CPU with its denominator stated (Docker counts one core
  as 100%), and Docker's runtime state. The security finding underneath that state was removed.
- `docker-identity.tsx` is the line the overview, Containers, Events and Stacks open on: Docker's
  mark, "Docker" and its version, the page's verdict beside the name, the server as the first fact
  and the page's one command, in `HostIdentity`'s project layout.
- `container-band.tsx`, `engine-picture.tsx`, `engine-picture-model.ts` and `overview.ts` are the
  overview's. The band reads the containers socket, so its figures move with every frame.
  `engine-picture.tsx` draws what the engine holds on `components/wire/wire-flow.tsx`: compose projects and
  the containers started by hand, Docker, and the networks and volumes it keeps, each node a link to
  its page. `engine-picture-model.ts` (unit-tested) works the nodes out: the state each line is drawn
  in, the order (what is failing first), which fold into a count, and which nodes light together. A
  container is *failing* as `containers.ts` reads it for the Containers page, with the event log
  telling a kill for memory from a stop, so the overview's verdict and the table it opens count the
  same containers; until 0.8.7 `overview.ts` kept a second reading (any exit but 0 or 143) and the two
  could disagree. `overview.ts` still holds Recent's words: Docker's events one line per container, a
  stop's kill, die and stop as one stop and an OOM kill folded into the exit it caused. The overview
  drew a table of every container until 0.8.7; that table is
  `/docker/containers` only.
- `cleanup.tsx` and `stack-preview.tsx` are the two "before you press it" panels: what each category of
  removable object costs, and what a compose deploy is expected to change — including the sentence about
  volumes, stated whether or not any are affected. The preview opens on that decision as one sentence
  (*Deploy will recreate 2 services and create 1*) over a bar of the stack's services, each in the
  colour of what happens to it, and lists them as a framed table, most consequential first: the change,
  why (the keys that changed as tags over the server's reason), the image and the tag it moves to, and
  what the service is doing now — a running service about to be recreated says it goes down while it
  is replaced. The unchanged rest stays in the table, quiet, rather than five copies of "its
  configuration is identical" each with a heading. The file's diff is grouped by the service each hunk
  changes (`stack-diff.tsx`, the same view the compose editor's review and the history use), beside
  the volumes, each kept or destroyed, and the "compose makes the final call" caveat is stated once.
  Deploy and a fresh read sit at the top, where the reader who has just read what will happen can act
  on it. A cleanup category is one line — name, size, count, in
  fixed columns — with the cost sentence and the example names behind its `?`. It used to be a block
  whose height depended on how far the cost sentence wrapped and whether that category had examples, so
  the six rows came out at three different heights and the size and count, top-aligned against the
  tallest, never lined up with each other; four of the six were "Nothing in this category" at half
  opacity and were the tallest thing on screen.

Panel headers carry no icon plot. A tinted square in front of every title is chrome repeated once per
panel, and on a page that is eight stacked panels it reads as a column of brand marks rather than as
eight headings. It started here and now holds product-wide — the prop is gone from `PanelHeader`,
`Modal`, `SidePanel` and `Section`, and the title sits at `text-title` instead
(`docs/internal/frontend/design-system.md` §14). The `?` stays: it is the one mark on those headers
that does something.
- The Images page is `app/(dashboard)/docker/images/page.tsx` over `image-band.tsx`, `image-table.tsx`
  and `image-pull.tsx`, with the pure parts — a reference split as a registry reads
  it, one registry answer per image, the table's filters and order, a pull's stream folded by layer,
  a history line read as its instruction, and an image's address — in `images.ts` (unit-tested).
  An image's own page is `app/(dashboard)/docker/images/[id]/page.tsx` over `image-page.tsx`
  (the identity line, the tabs, the address), `image-rail.tsx` (the table's list beside it, and the
  hook that reads it with the table's own filters), `image-picture.tsx` (registry, image, containers
  on `wire-flow`) and `image-views.tsx` (Layers, Containers, History, Configuration). The verbs of an
  image and the Pull and Tag dialogs they open are declared once in `image-actions.tsx` for the row
  and the page, and `image-marks.tsx` holds the marks both draw an image with. The address names an
  image by the hex of its id: unique, stable while the image exists, and in need of no escaping; a
  reference escaped into one segment is resolved by the API and replaced. The list, the registry
  answers and the containers are keyed polls (`/docker/images/`, `/docker/images/updates`,
  `/docker/containers/`), so the rail is drawn from what the table last read. Each kind on its disk
  reading reclaims itself, from its line or its mark: unused images through `POST /docker/images/prune?all=true`, the build cache through
  `POST /docker/build-cache/prune` and stopped containers through `POST /docker/containers/prune`;
  only Reclaim in the block's head runs the sweep, and says it reaches stopped containers and networks.
  The pull socket is enabled only while a pull is in flight: the server closes it when the pull
  ends, and `useSocket` reconnects whatever is still enabled, so a finished pull used to open a new
  one about once a second until the page was left. Tag names the same image again, so the copy a
  container runs can keep a name across the next pull of its tag. Removing an image with more than
  one name sends `force=true`, because Docker refuses an id two names point at; it is forced only
  after the image is read again and nothing uses it, since a forced removal also takes an image a
  stopped container was made from. A pull from an image's page follows the tag to the image it
  points at afterwards.
- `stacks-tab.tsx` is the Stacks page, read the way Services is (`design-system.md` §15 pass 2):
  Docker's identity line (stacks running beside its name, stacks deployed, services running, ports
  published, Create stack as its command), then `stack-band.tsx`, bar readings
  (the five stacks using the most processor and memory as spans of one bar the size of the machine,
  summed over each stack's containers from the containers socket, a press narrowing the list to that
  stack; and Recent, the daemon's last starts, exits with their codes, out-of-memory kills folded into
  the exit they caused and failed checks, each a tick on the last day opening its stack), then the
  stacks as one-line cards (`stack-row.tsx`, a `ChoiceRow` each): logos, name and directory, a strip
  of its services' states, its state with how many declared services are up, its summed CPU and
  memory, its published ports, the one action that belongs there (deploy when the application is
  down), an arrow and a menu, in columns that line up and at one height in every state. A stack's
  containers are its page: until 0.8.7 they were rows under the stack's own in one table,
  folding away per session. A stack found by a service's name or image says
  which matched. Stacks are running first, then stopped, then never deployed, each by name.
  The joins, buckets and change words are pure (`stack-readings.ts`, unit-tested). A stack's own page follows the same rule as a container's: the compose verbs that are pressed
  daily (deploy, restart) sit inline, and the rest are behind one overflow menu, one word to a line,
  drawn the same way as the container menu. Its services and the deploy preview's services are framed
  tables (below), its deployment history a rail beside the record it reads; the network panel's shape
  diagram is the one `Group tinted` fence the section keeps, and a diff sits in a `Well`.
- `stack-detail.tsx` is a stack as the application it is. It opens on the stack's identity line — its
  services' products, the compose file, the directory and its checkout (uncommitted changes, how far
  behind, a link to the Git page), how many services run and how many ports it publishes — with the
  verdict at its end (*1 service failing*, *1 not created*, *All 5 running*), which narrows the table
  to those services. Its Services view is a framed table of every service (`stack-services-table.tsx`)
  fed by the containers socket: the service as its image's product with its log lane down the row,
  its state read with the event log (out of memory, crashed or stopped by the exit status, a restart
  loop counted) and how long it has been in it, its processor's last hour beside this second's share,
  memory against its limit, network rates from 1536 and its published ports, under state chips that
  count and narrow; each row opens its container and carries the compose verbs for that one service
  (`docker compose restart|stop|start|up --force-recreate|pull <service>`, each confirmed with its
  command). Over it, in a container page's order since 0.8.7 (the table came first before), are the
  Services page's band for the stack (`stack-usage-band.tsx`: its share of
  the processor and memory, and Recent from `/docker/events?stack=`) and a picture of its published
  ports, services and networks whose wires pulse with each service's traffic (`stack-map.tsx`); the
  joins and words are `stack-service-readings.ts`, unit-tested. Every other view names a service the
  same way — its lane, its product, its name (`ServiceLabel`):
  - the deploy preview (`stack-preview.tsx`, above);
  - the compose file (`stack-compose.tsx`), editable in place with an outline beside it — each section
    and the names under it a press from their line, each service with its live state — a state that
    says whether the screen is the disk and whether compose took it, a status line that says where the
    cursor is in the stack's terms (*services · api*), Ctrl+S, and a review of an unsaved edit as a diff
    against the file on disk. An edit survives a look at another view (kept in memory, never written
    down: a compose file holds passwords). Validation jumps to the line compose names, and saving says
    it is *not* deploying and offers the preview and Deploy;
  - the stack's directory (`stack-files.tsx`), read first as what compose takes from it: the compose
    file, the root `.env`, each build context, bind mount and env file the services name, with the
    services that read each in their lanes, git's word for any change, and a path that is missing said
    with what it costs (a build or env file compose cannot start without in red, a mount Docker will
    create empty in amber). The same words follow the names into the browser under it, which takes the
    tab's height;
  - the deployment history (`stack-history.tsx`): a strip from the first record to now, each stretch
    in the colour of the compose file that was live through it, over the records as a rail (the backup
    job's runs' shape) and the one picked read whole — who did what from which commit, whether its
    images are still here, how its file differs from the one on disk now, the digests running just
    before, and *Edit from this version*, which puts that file in the editor unsaved;
  - its logs as one source (`stack-logs.tsx`, `stack:<name>`: every container merged by time, each line
    under its service in that service's hue, the stack's readings over it, and an Events view of what
    Docker did to the project), under a strip of the services writing into it — each with its state,
    its lines and errors in the last hour and their shape — where a press narrows the log to one.

  The outline, the line diff, what each service reads from the directory and the history's spans are
  `stack-views.ts`, unit-tested. The stack's menu opens a shell in its directory. `container-detail.tsx` adds
  the reachability join (published port + the proxy site pointing at it turns "running on 3000" into a
  URL), the writable-layer investigator, and its logs through the lens its
  image names with an Events view beside them (`container-events.tsx`: the health check's probes,
  Docker's events for the container with crash loops folded and an exit's last lines under it),
  editable limits, raw inspect, every verb `container-actions.tsx` declares (the lifecycle as words
  beside the way back; Pause, Update, Copy id and Remove behind its menu) and Rename — behind a
  statement of consequence when compose owns the container, because the next deploy silently undoes
  it days later. The page opens on the container's identity line (`container-identity.tsx`: its
  product with its state in the tile's corner, image, project, ticking uptime, restart policy in
  words, the id, and the verdict from `container.ts`, which reads a crash loop and a failing health
  check as failing where Docker says "restarting" and "running"), and its Overview on four live
  readings off its stats socket with their last hour (`container-readings.tsx`), a picture of how it is reached and what it keeps (`container-picture.tsx`), the
  containers of its compose project — or of its own networks — as a live table
  (`container-company.tsx`), its recent events with loops folded (`container-recent.tsx`), how it
  runs, and its ports and networks as tables. From `xl` the Containers table stands beside the page
  as a rail (`container-rail.tsx`: the table's search, chips and order from its session keys, Alt+↑
  and Alt+↓ to walk it with the view carried), and a stack's page keeps the Stacks list the same
  way (`stack-rail.tsx`); the column the five Docker item pages share is `item-rail.tsx`
  (`DockerRail`, `DockerRailRow`), and each `*-rail.tsx` is its row and its list;
  `beside-rail.ts` says where the two pages' blocks go side by side now that
  the rail takes its width. The tables are `container-tables.tsx`, which also draws the
  Environment tab: each name's namespace in its lane hue, each value in its kind's code hue, and
  the kinds — credentials, addresses, paths, switches, numbers — as chips that count and narrow, beside
  a filter that never searches a hidden value). Each other view is the overhauled page it stands in
  for. **Usage** (`container-usage-tab.tsx`) is a project Runtime's `deploy/runtime-usage.tsx` — the
  Metrics page's five readings over the same chart panels, a Live range off the stats socket beside
  the recorded ones, the processes chart and the data it has moved since it started — with the container's own exits, OOM kills
  and folded restart loops as the charts' markers (`usage-markers.ts`), then the Metrics page's
  breakdowns read off the same frame: the memory allocation as one bar of programs, active files,
  reclaimable cache and headroom, the quota, host share, throttling and task count as tiles, the
  memory and CPU limits drawn beside the change that sets them, and the interfaces as a framed table
  with an in/out bar per row. `RuntimeUsage` takes `running={false}` for a stopped container: no
  socket, the recorded hour, and "Not running" rather than a socket waiting forever. Pause and Export
  CSV are the Metrics page's, and a pinned moment holds its inspector over the charts. **Storage**
  (`container-storage-tab.tsx`) is the file manager's workbench: one frame around a rail of the mounts
  — each the path *inside* the container, drawn as its kind in that kind's hue (a database's volume as
  its engine, Docker's socket in red) with where it really lives under it, a tmpfs marked *not kept* —
  and beside it the picked mount open in `files/inline-browser.tsx`, flush, at the tab's height; the
  rail's foot counts how many mounts outlive the container, and the writable-layer investigator stays
  under the frame. With more than one to look in, the rows are the switch; a tmpfs row never is.
  Storage that looks like a database's own files is named as such above it while the container runs
  (`docker/shared.tsx`). **Logs** is the logs page's own `ServiceLogs`, read through the lens the image
  names, the lens's readings as tiles over the pane where it has them. **Inspect**
  (`container-inspect-tab.tsx`, logic in `inspect.ts`) is the run page's Details shape: one frame
  around a rail of the inspect document's sections and the picked one as coloured JSON in the `CODE`
  hues, folding, filterable, credentials still masked and never matched by the filter.
  **Configuration** (`container-configuration.tsx`) opens the owning Compose file or presents the
  administrator's manual replacement specification. The framed editor names edited fields and previews
  the submitted specification before replacement is confirmed; it preserves the reviewed credentials
  and mounts. A `BorderBeam` and "Replacing…" show the in-flight operation. **Shell** (`container-shell-tab.tsx`) is the deploy Console's: one pane, its strip saying
  `account@container` (root in amber) and the working directory, the run-as switch where the image
  declares a user, and read-only first commands (`id`, `ls -la`, `ps aux`, `df -h`, the OS release)
  that type themselves into the session — no `env`, which would print every credential the
  Environment tab hides. `build-dialog.tsx` is where the
  git panel and Docker stop being two products: a repository we already pull is a build context.

Six deep links are worth preserving: `/files?path=`, `/git?repo=`, `/terminal?cwd=`, `/terminal?as=` (a shell as that account, from its System users page),
`/terminal?session=`, `/audit?action=`. Files and Git selections follow the address bar, while the
terminal opens a session exactly once per mount because a shell is a process; `?session=` opens on an
existing session instead (the Overview's shells link with it), remembered as if picked in the rail,
and leaves the address once read. Audit's settled filters are history entries; Back restores the
previous question. The audit link is how the Docker event feed hands off a correlated event.

A package is its own page (`/packages/[name]`; `?package=`, which opened its sheet, redirects), and backup job pages use `?run=` for their selected run.
These, the Docker container inventory, Git and the Audit/Security lists participate in the shared
keyboard/place controller; [`../patterns/keyboard-and-place.md`](../patterns/keyboard-and-place.md) describes their
shortcuts, held arrivals and focus restoration.

A container and a stack are their own destinations — `/docker/containers/<id>` and
`/docker/stacks/<name>` — since 2026-09-21: each holds its logs, and the container a shell and
the stack a compose editor, which is the test for a page rather than a sheet (`shell.md`). The
container takes `?tab=` so a link can ask its question — "show me the logs" lands on the logs. The
`?container=` and `?stack=` addresses those pages replaced still resolve, by redirect. Deployment
runtime rows use these handoffs, including when a container disappears between the observation and
the click: the page reports it, and a container removed from its own page returns to the list.

`useQuerySelection` still keeps the remaining sheets' selections in browser history so reload and
back/forward restore them; closing clears only that selection parameter.

## Packages panels

**Packages.** `install-panel.tsx` updates as you type, which is not decoration: the reason people open a
terminal instead of a package page is that they do not know the name (`postgresql-client`, not `psql`;
`build-essential`, not `gcc`), and a form where you type a guess and press a button to find out you were
wrong is a form you use once. Install is one press on the row — there was a tray, and it cost a click and
a concept on every single-package install while protecting against an interruption a job survives anyway.
The command each install button runs is its `title`. The empty search offers eight software choices
with product marks; results are `ChoiceRow`s with their install action, so opening a result and starting
an install remain separate gestures. A pending query hides the previous query's results and says it is
searching; failed reads say so. An active install has a job-backed `Status`; failed or canceled
attempts offer retry, and completed attempts refresh native package readings.
The page is drawn per design-system §15 in the reading register, without summary tiles. Installed and
by-hand/dependency counts live in the identity, view strip and scope chips. `software-band.tsx` draws the
five largest software groups as shares of the installed size, in the disk measurement's colour, with
other packages as one muted span. Products are grouped by `packageProduct`; unnamed packages use the
archive's section. These sums cover the whole inventory, not the 400-row rendered table. Pressing a
group opens Installed, clears its search and selects Everything so dependencies appear too; Escape or
the selected chip clears the group. Beside it **Updates waiting** is one bar with a span per pending
package, security first, the first five named with their version changes and each opening its package,
advisory coverage in its head and confirmed security/all-upgrade actions at the strip's end. Both are
bar readings (design-system §10), drawn before the inventory lands. Missing sizes and advisory data are stated;
a failed update read is an error, never an empty all-clear.
A package's detail is its own page, `/packages/[name]` (`package-page.tsx`), on the shape a service's
page has: the identity line with its state, one command (Install or Update) and Remove and Remove and
purge behind the menu and their ordinary confirmations, then views in the address. Overview is the
picture of what it needs and what it gives (`WireFlow`) over About; Files holds the copyable commands
coloured as shell words, registered services with links to their systemd page, and configuration and
documentation links into Files; Manual the man page in a well; Depends every dependency one line each,
opening that package on the same view; Log the manager's logs searched for the package's name. A usage
read that fails reports its reason with Try again, instead of leaving a loader. The page keeps package
protection and the resumable job: its console opens under the identity line, and the package is read
again when the run ends. The installed list is a rail beside it from 1280 pixels, with the list's own
filter. The installed table is one line a row from 1024 pixels, with the size as a figure in its
column, and a card a package below; a 400-row cap has the count said plainly underneath. The fourth view, **Log**
(`components/packages/log-view.tsx`), reads what the package manager did from its own logs — apt's
history with each transaction's command and who asked, dpkg's record, the unattended runs, dnf's — in
History and Insights only, opening on everything on disk, since a package log is written a few times
a week and a live tail of it is an empty pane; it is named Log because the pane's own first tab is
History.
