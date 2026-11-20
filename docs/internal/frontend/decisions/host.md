# Decisions: Host: Overview, Docker, Metrics, Processes, Packages, System users, Backups

Why these pages look as they do. The stories were told in `design-system.md` §14 and §15 until 0.9.0
and are moved here verbatim, under a heading per page. They are history: a rule they state is in
[`../standard.md`](../standard.md) or the page it links to, and where a story and the standard
disagree, the standard wins.

## Backups (§15 pass 2)

Backups took it too: its four readings (jobs, last backup, next backup, stored) each said what
one job's card says, so the counts went to the Jobs header and the rest to the cards, ordered
worst first under an attention list of the jobs that failed or went quiet.

It took a second pass in 0.7.1, because the operator found it dead: three grey cards over a
long list of grey rows, nothing on it a reader could find without reading. It opens now on a
picture of where the data goes (`components/backups/protection-map.tsx`), the Notifications
picture's shape: what the server has, one node per kind drawn as the products it holds, wired
into this server and out to each directory and bucket a job writes to, drawn as its service.
The wires are the readings the tiles were: a kind's is green when an enabled job covers all of
it, amber when part of it, dashed when nothing does; a destination's is green, red, amber or
still by how its jobs last ran; both pulse while a backup is being taken, which is also when the
card carries its beam and the picture's middle names the job. The job count, the total stored
and the next backup are the middle's words. A server whose every archive is on its own disk gets
a dashed ring where an off-site copy would go. Pressing a kind narrows the coverage below to it,
and the coverage became lit cards, two to a row, because every one of them is taken (§16). A
job's page took the same line: the products it covers as its identity line's mark, its own
picture (each path as the thing it is, each dump as its engine), four readings with the archive
size over the runs as the Holding figure's trend, and its runs as a rail beside an inspector —
the newest until one is picked, its failing log line washed red, what the archive holds by its
manifest — where a table of runs had the chosen one's log two screens below it.

0.8.7 gave the Backups page the opening every page takes now: an identity line (`HostIdentity`
in a project's layout) with how the jobs stand as its state, the jobs, what they hold and the
next backup as its facts, New backup as its one command and Import behind its `⋯`; both sat on
the Jobs title line, under the picture. The picture's middle said the same three facts, so it
says only what is moving through it now, the job being taken, and nothing while nothing is. A
job's four readings became one `ReadingLine`, what it holds over how much is kept with the
trend at its end, because the other three tiles were a bare figure said elsewhere on the page:
how the last run went is the identity line's end and the rail's first row, and when it runs
next is its picture's middle, which says that for every job now (the containers a job pauses
stood there instead, and are named on the identity line). A run in the rail lost the bar of its
archive's size against the largest, the growing bar the standard retired, for the size as a
figure in a column of its own, so a run is one line under its date.

## Processes (§15 pass 2)

The live Processes page took the exit in 0.7.1, because the operator found it still: four grey
figures over a table, and nothing on it that answered *who* was using the machine. Each figure
went where it was already said or said better. The process count is a fact in the identity line
and beside the table's title; running, sleeping, blocked and zombie are counted chips in the
table's head — the two that mean trouble in their tone, drawn only while there is one — which
narrow the table as well as count; unmanaged was already an owner chip, and the supervised
breakdown the owner chips' counts. What took their place is the question they never answered:
`components/procs/workloads.tsx`, the five heaviest workloads by processor and by memory as
spans of one bar the size of the machine, each rank a step of its measurement's hue (§10), the
rest of what the host reports in use one muted span, every span easing to the next poll and
every figure gliding to it. A workload is a supervisor's processes or the copies of one program
started by hand, summed by the backend over the whole snapshot with shared pages counted once,
so forty Chrome renderers are one span rather than forty rows; a press narrows the table to it.
The five were rows under the bar (`ShareBar`) until 0.8.5, and are a bar reading now (§10): one on
the line, the marks of all five under it. The
line of two buttons that stood above the identity line went into it, as Metrics' shortcuts did.
The table took fixed columns — sized to their content, a long command line had pushed Memory
and every row's menu past the panel's edge at 1280 — and draws CPU and memory as a figure beside
a short bar; a process new since the last poll rises (`useArrivals`). The process sheet lost its
two tabs for one scroll that opens on four live readings over the process's recent windows,
which the sampler keeps for every process it measures, so a sheet opened from a table that has
been open for minutes opens on those minutes.

The sheet became a page in 0.8.6, at the operator's request: "when I open a process it opens in
a side panel … the information just gets cluttered", with everything the kernel keeps about a
process — threads, descriptors, limits, environment, the signals it handles — left nowhere to go.
A process is a destination now, the way a container is: `/processes/[pid]`, its identity line and
its views as the underlined strip (`procs/process-page.tsx`). What the sheet did well, the table
beside the thing being read, is the rail from `xl` — the table's filters and ranking folded into a
column, the process on screen filled, Alt+↑/↓ to walk it and the view carried along — so checking
the next worker is one press rather than a trip back. The Overview keeps the sheet's four readings
and replaces its lineage path and child rows with the container page's wiring picture: what
supervises it and its parent on the left, the process as its product in the middle, what it started
(copies of one program as one node), its sockets and its open files on the right. Control says what
each signal will do to *this* process, read from the masks of the signals it handles, so a hang-up
that reloads nginx and a hang-up that ends a script stop looking like the same button.

## PM2 (§15 pass 2)

PM2 took the same exit in the same pass, at the operator's request, and for the same reason: four
grey figures over a table, and nothing that moved. Online and Not running went to state chips in
the table's head, Errored in its tone and drawn only while there is one; Restarts summed every
application's counter since PM2 last reset it, so the one worker crash-looping hid inside a
number — it is the table's first row now, its unstable restarts in red under the count, which
rises when it moves; and Memory, one figure for all of them, is the band's
(`components/procs/pm2-band.tsx`): the Processes band drawn for PM2 alone, each application —
a cluster summed into one — a span of one bar the size of the host beside everything else in
use, a press narrowing the table to it. Each row's uptime ticks between polls, from a start held
across them so it never steps back; memory is drawn against the limit PM2 restarts the
application at where it has one, because that is its ceiling. The verdict reads the saved list's
names, so a list saved before the last start is amber with **Save list** beside it, and each
application the list lacks says *not saved*. The sheet kept its Logs tab — the log is a `Pane`
that wants the sheet's height — and its overview became the process sheet's one scroll: what is
wrong as a `Notice` with the way to the log, four readings from the process table's read of the
application's PID over the sampler's windows, a cluster's instances as rows, what it listens on,
the command coloured. Its scale dialog is a stepper over the workers themselves, and the start
dialog picks the interpreter and the mode as cards with their marks (§16), not two selects.

## Services (§15 pass 2)

Services took the same exit the day after, at the operator's request, because beside Live it
was the still page of the two: four grey tiles over a table of names and state words, with no
figure on it that moved. Active, failed and inactive are counted state chips in the table's
head — failed in its tone, and a Starting chip in amber only while a unit is on its way
somewhere — which narrow as well as count; "enabled on boot" is a fact in the identity line and
the Enabled startup chip, where the disabled and static its hint named are chips of their own.
The line opens on the machine as Live's does, with systemd's version, how many services are
active and start on boot, and when it booted as facts, and at its right end the verdict — the
failed count, a press of which narrows the table to them — beside Reload unit files and the
shortcuts, where a button stood alone over the table. Under it `components/procs/
service-band.tsx` answers what the tiles never did: the five services using the most processor
and the most memory on Live's bar the size of the machine, and a third block of what happened —
the last units to start, stop, finish or fail since the boot settled, each a tick on the last day,
with its time, a failure saying why on the line and a restart in progress shimmering. Each is a
bar reading (§10), where they were rows under a `ShareBar` until 0.8.5. A press opens the unit.
The figures are systemd's own, read from each unit's cgroup by the list
route, so a service's share is every process it started. The table took Live's fixed columns
and its figure-beside-a-bar readings, and its state column says how long a unit has been in its
state — up for, failed since and why, the restarts it has taken. Only a newly listed unit rises
(`useArrivals` keyed on its name); starting or stopping an existing unit updates it in place.
The unit sheet became a page in 0.8.6, as the process sheet did and at the operator's request
("can we do the same for the /services page"): `/processes/services/[unit]`
(`procs/service-page.tsx`), the table as a rail beside it from `xl`, failed first, with Alt+↑/↓
and the view carried to the next unit. Its Overview keeps the sheet's readout — four live tiles
(CPU over the unit's recent windows, memory against its limit or over its windows, tasks against
theirs, automatic restarts with the policy), or the last run's counters when it is not running;
a failure or a restart loop said first as a Notice with the way to its journal — and draws where
the unit sits with the process page's wiring picture (`procs/wiring-picture.tsx`, one component
for both): what starts it and what it needs on the left, a down requirement in the danger hue
because that is usually why a unit will not start, and its main process, its other processes by
program, its sockets and where it writes on the right. What a drawer had no room for became views:
its dependencies by the question each answers with every named unit's state, the unit file and
its drop-ins coloured as one, its `Environment=`, `systemd-analyze security`'s exposure with each
setting most exposed first, and Control, where every verb is a row that says what it does and
each signal says what it will do to the main process, read from that process's signal masks.
The PM2 sheet became a page in 0.8.7 on the same shape: `/processes/pm2/[app]`
(`procs/pm2-app-page.tsx`), the list as a rail beside it from `xl`, crashing first. It is the
application's page rather than one worker's, which the sheet was: a cluster is one page whose
readings are its workers summed and whose verbs reach every worker in turn, with each worker a
one-line card under Instances that opens its process's page and keeps its own verbs. Its
identity line is laid out as a project's header (`HostIdentity` with a state and a command) —
one command, Start or Reload, and the rest in the menu — and its four readings are
`ReadingLine`s where the sheet opened on a dense `StatGrid`; its picture is the same
`wiring-picture.tsx`, the account's daemon and its boot hook on the left and the workers, the
port, the log and the working directory on the right.

## Docker containers (§15 pass 2)

Docker's Containers took the same exit in 0.7.1, at the operator's request, because it had no
life: a runtime health bar and a list of findings over a column of grey cards, nothing on it that
moved but a dot. Its figures went where they are read. Running, failing, starting, paused and
stopped are counted chips in the table's head — failing in its tone and starting in amber, each
drawn only while there is one — beside Needs attention; the health-check counts the bar drew are a
fact in the identity line (how many running containers a passing check vouches for) and each row's
second line, where "no health check" is still said out loud. The line opens on the engine as
Services' opens on the host: Docker as its mark and version, the host with its distribution, the
storage driver, the running count, the stacks and images, and at its right end the verdict — the
failing count, a press of which narrows the table. Under it `components/docker/containers-band.tsx`
answers what the bar never did, in bar readings (§10) that were rows under a `ShareBar` until 0.8.5:
the five containers using the most processor and memory on Live's bar the size of the machine,
its cap breathing and its figures gliding to each two-second frame of the container socket, and
**Recent**, Docker's event log followed live as ticks on the last day — a restart loop one tick
("×14 in 9 min · exit 1" on the line), an OOM kill said on the exit it caused, a failing health
check in red. Failing
is read from the state, the health check, the exit status and that log together
(`components/docker/containers.ts`): a 137 is `docker stop`'s as often as the OOM killer's, so only
the event log turns it red. The table took Live's fixed columns and figure-beside-a-bar readings —
memory against its limit where there is one, amber past 85% — with a ticking uptime under each
state, sortable headings that hold still under the pointer (`useInspectionOrder`), the processor's
hour and the network rate in the network section's colours from `2xl`, and each compose project in
its `LANES` hue as a chip and a press on any of its rows. A container new since the last frame
rises. The findings list went under the table: each row's issue count says which containers it is
about.

## Packages (§15 pass 2)

Packages took the same exit in 0.7.1 at the operator's request. Installed is a fact in the
identity and a count on its view; by-hand and dependencies are scope chips; Updates is its view's
count, the security chip and the band's Updates waiting; On disk heads the software band. The band
(`components/packages/software-band.tsx`) is two bar readings (§10). On disk is Processes' question
applied to installed software: the five largest products, or archive sections for packages no
product names, as spans of one bar of the installed size in the disk measurement's hue, with every
other package in one muted span. Its figures glide on arrival and to a new inventory; a press
narrows Installed to that group, including its dependencies. Updates waiting, a queue of rows until
0.8.5, is one bar with a span per pending package, security first in amber, the first five named
and each opening its package's page; the strip's end carries the security/all-upgrade decisions.
No advisory data is said rather than counted as no security updates. A package's detail, a sheet
until 0.8.7, is its own page (`/packages/[name]`, `components/packages/package-page.tsx`): the
identity line with its state and one command, the picture of what it needs and what it gives, its
views in the address and the installed list as a rail. Catalogue suggestions and results, and the
page's service/file/dependency destinations, take the lit choice edge (pass 3). Tables retain
their frame, one line a row with the size as a figure in its column (the bar under each size went:
it re-drew every row when a filter changed), and are a card a package below 1024px.

## The Docker overview and the section's pages (§15 pass 2)

The Docker overview took the same exit on 2026-10-08, at the operator's request, because it was
the still page of its section: four grey tiles — running, runtime health, attention, compose
stacks — over a list of what was not running and a list of projects, and no figure on it that
moved. Each went where it is said better. Running is a fact in the identity line and the
containers table's state chips — Failing in its tone and Starting in amber, drawn only while
there is one — which count and narrow, and the separate list of what was not running became the
table's order, failing first. Runtime health is the verdict at the line's end (*N containers
failing*, a press narrowing the table to them) and each container's State cell: up for how long
and whether a check passes, fails or does not exist, or how it stopped and when — *killed for
memory* where the runtime finding says so rather than a bare 137, and a container stopped
cleanly quiet. Attention is the second verdict, which goes to the Attention list, unchanged and
still never called health, and a container's issues are counted beside its name. Compose stacks
is the line's *3 of 4 projects up* and the projects' head, where each project's card now draws its
services as one strip of their states. The line opens on Docker drawn as itself, its version,
the host, the storage driver and cgroup version, and the images with what their layers hold
(`GET /docker/info` and the disk read). Under it `components/docker/container-band.tsx` answers
what the tiles never did, in Services' shape, as bar readings (§10): the five containers using the
most processor and memory on Live's bar the size of the machine, fed by the containers socket so
every span eases and every figure glides with each frame, and a Recent block of the last thing that
happened to each container, a tick on the last day, live off the daemon's events (`components/docker/overview.ts`: an
OOM kill and its exit read as one line, a stop's kill, die and stop as one, and a container that
came back after crashing says how many times it did in the hour). Then every container as a
framed table rather than `/docker/containers`' cards (§16 says why): fixed columns from `xl`, each
row the container as its product, its compose project in the project's lane hue (`hueFor(name,
LANES)`, the same hue on the project chips and the project cards), its state, its last hour of
processor as a sparkline beside the live figure, memory against its limit or the heaviest, its
published ports and its verbs. Below `xl` the same row is drawn down. Project chips narrow it;
a container that appears rises (`useArrivals`), and one that changes state changes in place.

The Docker section's first pages took the deploy pages' shape in 0.8.7, and three things in the
paragraphs here changed with it. **One line opens every Docker section page**
(`docker/docker-identity.tsx`): Docker's mark, "Docker" with its version after it in a fact's
weight, the page's verdict beside the name, the server as the first fact, the page's one command
at the far end. The overview said "Docker 29.2.0", Containers and Events "Docker Engine 29.2.0"
and Stacks the server's hostname, so one daemon had three names. **The overview's table of every
container went**: it was the table `/docker/containers` draws, and under the band it answered
the band's question a second time. In its place the engine is a picture (`engine-picture.tsx`,
on `WireFlow`): compose projects and the containers started by hand, Docker, the networks and
volumes it keeps, with the line as the state (red where a project has a failing container, amber
where one is partly up or a volume has nothing mounting it, dashed where a thing is stopped or
never deployed, moving only where the thing runs). The section's title still counts the
containers and links to their table, and the verdict opens that table narrowed to what is
failing. A picture cannot know its height before its data, so it remembers the rows each side
had (`docker.overview.picture.rows`) and waits at that size; the line, the band and the
projects' cards are drawn waiting too, where the page used to be a loading panel until its
first list. **The stacks are one-line cards** (`stack-row.tsx`): a stack's services no longer
unfold under its row, because a list whose rows grow by the number of services each has is not
read down its columns. The card keeps a strip of the services' states, the state and how much
of it is up, the two sums and the ports in fixed columns, Deploy in a box of one width so a
row with it and a row without end on one edge, then the arrow and the menu. On a phone the state
and the sums are the card's second line.

**A rail beside a page that was laid out without one** (`docker/beside-rail.ts`). A container's
and a stack's pages keep their list beside them from `xl`, as a process's does. Both were laid
out for the page's whole width, and a rail and its gap take 312px of it: at 1280px the stack's
services table, which fixes its columns, was left with no width for its first one. So inside
those two pages a block that went side by side at 1280px does so at 1600px, and one that waited
for 1536px waits for 1856px; between, the column is as wide as a tablet's and draws what a
tablet draws. A page built with its rail (a process, a service, a volume) sizes its blocks for
the column from the start and needs none of this.

## Scheduled (§15 pass 2)

Scheduled took the same exit in 0.7.1 and for the same reason: five grey figures over three
tables, and nothing on the page that showed *when* anything ran without reading row by row.
Each figure went where it is said better. Next run is the countdown at the end of the
machine's identity line, to the second, and the one dot on the band that breathes; the account's
jobs and how many are disabled are the counted chips in the Cron jobs head and a fact in the
identity line; the runs cron started in the last day, and those whose output went nowhere, are
the cron log's chips (`ServiceLogs readings="chips"`), the database workbench's answer, which
narrow the log to what they count; timers armed is the timers' head, armed, running now and
stopped as chips with how many start on boot beside them; the package files' lines are a fact
and their section's count. What took the tiles' place is the question they answered one figure
at a time: `components/procs/schedule-band.tsx`, the next 24 hours of all three, said two
ways that agree.

The first shape of it was a lane per schedule on one axis that started now, the next run a dot,
the runs after it ticks, a schedule too frequent to draw a band. In 0.8.6 the operator could not
read it — "is it a calendar? a timeline? what does it show" — and they were right on every
count: nothing said what a dot, a tick or a band was; most lanes were one dot by the left edge of
an empty track, because systemd reports only a timer's next run; the hours were an axis three
lanes up; the times were the browser's while the schedules said the server's, so "every day at
03:30" forecast 22:30; and `/etc/crontab`'s braced daily line named a lane "}". It is now a
chart of the next 24 hours an hour to a column — each schedule that runs in an hour one block in
its kind's `--tag-*` hue (blue for the account's crontab, violet for the timers, cyan for the
package files, none red or amber so no block reads as a failure), stacked with the hourly ones at
the foot so the day's floor is level, the hours named under the columns from *Now* — and one
fixed line under it that says the day in a sentence at rest and reads out the hour or the block
pointed at (§10's readout, one height in every state). Under the chart, **Up next** lists the
schedules in the order they run: each row the clock time, the mark, the name, the schedule in
words, the kind and a countdown, one height; pointing at a row lights its blocks across the day
and dims the rest, so the list and the picture are read together. The kinds are chips that
count and narrow; what the chart cannot draw — work later in the week, what runs only at boot,
what is switched off, and the package cron lines left to systemd — is a line under the list.
The band renders during load in the same shape, so the tables under it do not move when it
lands. Every time on the page is on the server's clock, and the identity line names the zone
when it is not the reader's. A cron line's runs are its expression's (`lib/schedule.ts`); a
timer's are read off its calendar only where it adds no random delay (`timerRuns`), since a
package timer that adds twelve hours fires at none of its calendar's times, and are otherwise
the one run systemd reports. A package's cron line guarded on `/run/systemd/system` does nothing
on a host running systemd — Debian ships it beside a timer for the same work — so it draws no
run, and its row and its sheet say which timer does the work (`deferredToSystemd`).

The tables under the band took the same pass. A job is named the way the band names it — the
note above the line, `run-parts`' Hourly or Daily scripts, or the program — with the command
that does the work under the name, the schedule in words over its expression in its own column,
and the next run as a countdown over its clock time ("in 2h 23m" over "Tue 00:59") where the
column printed the date to the second. A state that is the expected answer is not drawn: the
cron jobs lost their *enabled* column, the timers their *Startup* tag, and a timer that will not
come back after a reboot says so under its state. The timers say when each fires in words with
the delay its package adds — the backend reads every timer's calendar, intervals and delay with
one `systemctl show` — and what its unit says it is for. The system cron files are one table
grouped under a row per file, where they were a table per file each repeating its header, with
`/etc/crontab`'s worked example (a commented `* * * * * user-name command to be executed` under
a ruler of pipes, which parsed as a switched-off job) left out.

Every schedule opens a sheet built from one set of parts (`components/procs/schedule-sheet.tsx`),
because the three sheets were tiles, a strip, a command and a log stacked at 20px with nothing
louder than another, and the schedule — what a schedule is opened for — was a line of mono under
the title. Each opens on the schedule as a sentence over the expression it was written as, then
its figures as tiles, then **When it runs**: the next five runs as a list beside the week drawn
as seven days of a mark per run and a count per day, a timer's random delay a wash after each
mark (why certbot ran at 06:41 when its calendar says midnight is the wash, not a sentence);
then Command, Details and Runs, each a title and a hairline on one 24px gutter. A job's figures
are its next run and its runs this week, and its Runs the cron log narrowed to its command; a
switched-off job, a stopped timer and a line left to systemd say which they are first, the last
linking the timer. A timer's figures are its next and last run and how long that took, and its
Runs the service's Runs view. The job editor asks what runs, then when — its frequency a grid
of toggles — over a preview card of the schedule in words, its week and its next three runs,
drawn as the fields change.

## Docker networks (§15 pass 2)

Docker networks took the same exit on 2026-10-08, at the operator's request, because it was the
still page of the section: a column of grey cards, each a name, a subnet and a count, with the
members one press away and nothing on it that moved. The page had no tiles to give up; its three
chips (all, user-created, Docker system) became owner chips that count and narrow by who made a
network — a compose project, this dashboard, standalone or Docker — with Unused in amber beside
them. It opens on Docker's identity line, the overview's, whose verdict is the address pool nearly
or wholly spent, else the unused networks (a press narrows the table to them), else every network
in use. Under it `components/docker/network-band.tsx` answers what the cards never did, in three bar
readings (§10): which networks carry the traffic, read off their bridges every two seconds and
drawn as spans of one bar in each network's colour; how much of the address pool Docker carves
networks from is taken, one span per network sized by the blocks it holds and the first free block
at the strip's end — the pool every `compose up` takes a subnet from runs out at thirty-one with
the built-in pools; and who joined or left which network, from Docker's events, as ticks on the
last day. A network's colour is a lane hue (`networkHue`, slate for Docker's own) and is the
same on its row's edge, its spans, its ticks and its chips in the containers table, which lists
every container with each network it is on, its address there and the names it answers to, so
"can the API reach the database" is whether two rows share a colour. The sheet became a live
readout: four readings over the bridge's two minutes, the bridge wired to each member in the
wiring vocabulary with a pulse while that member moves bytes, the members as a table, the settings.
In 0.8.7 the sheet became a page (`components/docker/network-page.tsx`), on the process page's
shape: the identity line as a project's header, the views in the address, the networks as a rail
from `xl`. Its four tiles went the way every tile row has: members to the identity line, and
received, sent and addresses taken to reading lines (`components/reading-line.tsx`) under the
picture, with a fourth that says whether members answer by name. The picture grew a left side,
where the network leads: out through this server, onto a card, nowhere, and to each other
network a member is also on, which is what the members table's "also on" line was saying one
member at a time. The members are one-line cards that open the container.
Docker's Stacks page took the same exit in 0.7.1 at the operator's request, because it was the
still page of the section: a title over six cards, each a stack's name with its services as a line
of 6px dots under it, so "is the database up" was a dot, an Alertmanager killed for memory read as
grey as a mail server stopped by hand, and "which application is using the server" had no answer
on the page at all. It had no tiles to take apart; what it lacked was readings. It opens now on
the server's identity line — the Compose mark, Docker's version, stacks deployed, services running,
ports published — with the attention verdict at its right end, red where a service fails its check
or exited on an error and amber for the rest, a press of which narrows the table as the chip does.
Under it `components/docker/stack-band.tsx` is Services' band for applications, in bar readings (§10)
that were rows under a `ShareBar` until 0.8.5: the five stacks using the most processor and memory
on Live's bar the size of the machine, each a sum over the stack's containers from the containers
socket, a press narrowing the table to that stack; and Recent, the daemon's last starts, exits with
their codes, out-of-memory kills folded into the exit they caused and failed checks, each a tick on
the last day with its time and its stack's name in that stack's lane hue on the line, opening the
stack. The stacks themselves were one framed table of their containers
(`stack-table.tsx`) until 0.8.7 — a stack was a group row with its containers as rows of readings
under it, which grew by however many services it had; they are one-line cards now (`stack-row.tsx`,
the paragraph on the Docker section in 0.8.7 below), and the containers are the stack's page. What
follows describes that table as it was.
A stack's row is where they add up. A stack's row carries its products, state, how many declared
services are up (a service its file dropped is counted as an orphan, not as a fourth of three), the
summed CPU and memory as plain figures (a counting figure waits to be scrolled into view, and most
rows of a capped table are not), and Deploy when it is down, brand-faced only for a stack never
deployed; a lane in its name's hue (`hueFor(name, LANES)`, a compose project being a namespace) runs
down its containers. Each container is the containers page's readings in Services' columns: the
state with how long — up for, failing its check, killed with exit 137 — CPU as its last hour beside
this second's figure, memory beside a short bar against its limit where it has one, the published
ports, and its verbs. The chips count and narrow (Running, Needs attention in its tone, Stopped, Not
deployed, the last three only while there is one); a stack folds its containers away for the session;
a container's state moves the moment Docker's does and the list is read again when one does, so the
stack's own state follows within the second. Below 1280px the same rows are drawn down rather than
across, chosen once by a media query.
Docker's Images page took the same shape on 2026-10-08, at the operator's request, because it
had no life: four grey lines of disk figures, a notice and a column of grey cards, nothing on it
told apart by anything but its words. It opens on Docker's identity line (the engine's version,
how many images, the layers' size, how many are in use and untagged as facts; the registry's
verdict at its right end, a press of which narrows the table to what is behind, beside Pull and
Build). Under it `components/docker/image-band.tsx` asks Packages' two questions of Docker, as two
bar readings (§10). On disk is one bar of what Docker holds — images, build cache, containers,
volumes, each in the colour the Docker overview's `DiskSummary` gives that kind — with the part a
prune would give back hatched inside each span, so the reclaimable share is seen before a figure is
read; each kind keeps its own Reclaim, on the line while that kind is there and on its mark in the
strip, which now calls that kind's own route rather than the sweep (the sweep, Reclaim in the
block's head, also removes stopped containers and networks, which the Images and Build cache lines'
confirmations never said). Registry is one bar of every image by what its registry says —
update available in amber, current in green, pinned and built here in `--tag-cyan` and
`--tag-violet` because they are choices rather than states, unanswered and unused muted — with a
part per answer whose line draws the products in it, each narrowing the table; a check the reader
asked for sweeps the bar. The images are a framed table again (§12): the image as its product, its
registry and tag a step back from the repository; the containers running it by name with their
state's dot, each a link to its page; the registry's answer in the band's colours; its age; and
its size as a figure at the column's edge (a bar under each one went on 2026-11-14: a bar per row
is a chart of rows, and the order already says which is largest). Below `xl` the containers
and the answer join the name's second line as a dot, a count and the word. A pulled image rises
into the table (`useArrivals`), and its row says *Pulling…* while its pull runs. The pull dialog
draws a row per layer from Docker's stream, received bytes in `--chart-2` and written ones in
`--chart-4` (§10), with the raw transcript folded. Each answer's mark in the Registry strip is its
dot in the span's colour and its count; a count alone did not say which answer it was.
An image's detail was a sheet (`?image=`) until 2026-11-14 and is a page, `/docker/images/<id>`
(`docker/image-page.tsx`), on a container's and a process's shape. Its identity line is the image
as its product, its name with the registry and tag a step back, the registry's answer as its
state, and its size, age, users and id as facts; the one command is Pull (Tag where no registry
can serve it again) and the rest are in the menu. Under it one picture on `wire-flow`: where it
comes from (its registry and that registry's answer, this server for a local build, a dashed ring
for a layer with no name), the image, and the containers made from it, the line to a running one
moving; the block keeps one height whatever the image has. The sheet's four readings went to that
line and that picture. Its views are the underlined strip with `?tab=`: Layers is one `SplitBar`
of the image by layer in the hue of the instruction that wrote each, over a `RankList` in build
order with size and share (the bar per layer is gone); Containers is one-line cards; History is
every step as a framed table; Configuration is how it runs, its names, labels and environment as
title-and-hairline sections, each kind of reference a word with its meaning on hover rather than
a paragraph. An instruction is one line and opens whole in a popover, a RUN's command coloured by
`ShellWords`. From `xl` the table's list stands beside it as a rail.

## Docker volumes (§15 pass 2)

Docker's Volumes page took the Images page's shape on 2026-10-08, at the operator's request,
because it had no life: three chips over a column of grey cards, each a name over "1 container".
It opens on Docker's identity line (the engine's version; how many volumes, the bytes they hold,
how many are mounted and how many a backup job covers as facts), laid out since 0.8.7 as a
project's header: the verdict beside the name — how many volumes a prune would delete, a press of
which narrows the table to them — and Create volume as the one command at the far end.
Under it `components/docker/volume-band.tsx` asks two questions the cards left to be
added up, as two bar readings (§10). Held by is the Processes band asked of storage: the stacks and lone containers holding
the most, as spans of one bar the size of every volume, each in the lane hue (`hueFor(name,
LANES)`) the Stacks and Containers pages give that name, a stack taken down saying so in amber.
Mounts is every volume by where it stands — in use in green, held by stopped containers in
`--tag-cyan`, left by a stack in amber, not mounted in `--tag-violet`, anonymous muted — with the
part a prune would take hatched inside each span, as the Images band hatches what a prune gives
back, and the Prune command in the block's head naming the bytes; a prune in flight sweeps the bar.
Each count in the strip under the Mounts bar stands after its standing's dot (the strip was bare
figures until 0.8.7).
A press in either narrows the table. The table's widest column is the containers that mount each volume, by
name with their state's dot and the path they see it at, `ro` where they cannot write, each a
link to its page; then the volume's state in the band's colour over whether a backup covers it,
and its size as a figure in a column of figures (the bar under it against the largest went in
0.8.7: the Held by band already draws that). The column heads sort. Container state comes from
the containers socket, so a dot changes the moment Docker's does. Every row is one height
(`h-16`, two lines): a volume more than two containers mount names the first and counts the rest
on its second line, and a row with one line centres it. On a phone the row keeps the name over
its standing and its backup in two words, the size and the menu, and nothing on it is cut.

A volume is a page of its own since 0.8.7 (`/docker/volumes/[name]`, `components/docker/
volume-page.tsx`), on the service page's shape. It was a sheet over the table (`?volume=`, which
now redirects to the page): four readings, the mounting containers, a file browser and what
Docker was told, in a drawer. The page opens on the volume's identity line — the product whose
data it holds, its name, where it stands beside the name (a press opens Mounts), its size, where
it is kept, the stack that holds it, its age and whether a backup covers it, with one command
(Back up… where no running job covers it and the reader may write one, else Open in Files) and
the rest behind the menu. Under it `volume-picture.tsx` draws the volume between the containers
that mount it and the backup jobs that cover it (`WireFlow`): a stopped container's line is
dashed, a paused job's dashed, a failed one's red, and a line moves only while its job is
running; a volume nothing mounts or no job covers has a dashed ring there, the second the way
to the Backups form. The hub's cell holds room for three rows and a column draws at most three,
so the picture is one height for every volume and while it is read. Its views are the underlined
strip with `?tab=` in the address: Files (the embedded browser, the view it opens on), Mounts
(one-line cards, and what a prune would do when nothing mounts it), Backups (the covering jobs
as one-line cards with their destination, schedule and last run) and Configuration (what Docker
was told, driver options masked, and every label). From `xl` the table stands beside it as a
rail (`volume-rail.tsx`) with the table's own search, filters and order; Alt+↑ and Alt+↓ walk it
and the open view goes along. On a phone it is one pane, the picture as rows of marks.

The pass corrected what the page said about prunes. The cards called a volume whose only
container was stopped one "prune would delete", and the prune dialog said a stopped stack's data
would go. The daemon takes a reference for every container that mounts a volume, running or not,
and its prune removes only the local volumes without driver options that nothing references
(`volume/service.Prune`, `ByReferenced(false)`). What it does take is a volume whose containers
were *removed* — the stack taken down with `docker compose down` is the common case, and its
Compose labels still name it, so the page says which stack left it. A volume's standing is read
from the container listing rather than Docker's reference count, which comes from a disk-usage
walk the server caches for minutes and still says *held* after a `docker compose down` in a
shell. The list route says what a volume's driver options mount (`mountType`) without carrying
them, since a CIFS `o=` holds the share's password; the volume's page reads them from inspect
with such values masked. The prune confirmation reads the list again before it opens, and afterwards says
any volume Docker deleted that it did not name, or kept that it did.

## System users (§15 pass 2)

System users kept its four and changed one: the Locked count became a filter chip over the
cards beside who can sign in and who administers the host, where it also narrows the list, and
its tile went to the administrators — the members of `sudo`, `wheel` or `admin`, amber while one
of them needs no password. It took the exit in 0.7.1, at the operator's request, because four
counts with no name in them stood over a column of cards and nothing on the page moved. Accounts
is the identity line's *6 people* and the chips' All count; Can sign in the line's fact and a
chip; Administrators a chip and the Can become root block; Last sign-in the line's last fact
(who, how long ago, from where) and the first block of the band. The line is the Overview's
(`HostIdentity`: the people as their faces, the host's name, the people, who can sign in, the
keys authorised, who signed in last) with the verdict at its right end — how many accounts need
no password and how many of those are administrators, a press of which narrows the cards to
exactly the accounts counted, beside a *No password* chip drawn only while there is one. Under
it a band, since removed in the third pass below, answered what the tiles never did, three blocks over
the accounts the page held: **Last sign-ins**, one lane per account on an axis of 24h, 7d or 30d
that ended at that second, each a dot in the account's initials hue that moved toward now as the
clock ran, with a halo while the sign-in is under two minutes old and the places it came from
drawn as the network they are on; **Can become root**, every account with `sudo`, `wheel`,
`admin`, `docker` (root by another name) or uid 0, those needing no password first and locked
ones last; and **SSH keys**, one bar of every authorised key shared out by account, in the hue
each account had on the cards. A press on any row opened that account's keys sheet. The cards led
with the account that needed no password and a new account rose into the list (`useArrivals`).

It took a third pass in 0.8.2, at the operator's request, because the band said nothing on the
servers it is mostly used on: "Nobody signed in", "2 never signed in" and "No account has an
authorised key" filled the top half of the page, and every figure in them is a column of the
table that replaced the cards. So the band went, and what it was for became the findings under
the line (`accountFindings`): each a thing to change with where to change it, and none at all on
a host with nothing to say, so there is no green line to learn to skip. The line's verdict went
for a harder reason. It read "No account signs in without a password" over a host where root
signs in with one, because root is a system account by its number and the page left system
accounts out by default. Root is in the list now whatever the switch says.

## The host Overview (§15)

**What the Overview looks like after these passes**, as a checklist for the page you are on: the
machine's identity line first (`HostIdentity` in a project's layout — its distribution drawn as
itself, *Online* as its state, the processor and hypervisor as bare marks among its facts, *Open
terminal* as its command and the rest behind `⋯`); Shortcuts, a run of four `ShortcutCard`s; a
Resources `Section` whose head carries the way on to Metrics, holding the readings that move as
`ReadingLine`s — each a glyph on its measurement's hue, the reading as words, its last hour at the
line's end, its figure gliding to every frame — and then a capacity bar per filesystem; the
Deployments section; three pairs of plain blocks — the shells beside the drawings and boards, the
databases beside the backup jobs, the top processes (a `RankList`) beside the activity list; and a
`Section` holding eight `ChoiceRow`s, one per module, each one sentence, a state and the products it
counts. No frame anywhere on the page — the shortcuts, project cards, shells, drawings, databases,
jobs and modules carry the lit edge of a thing you take, which is not one. Everything that arrived,
rose.

**Its figures became sentences in 0.8.7.** Until then the readings were a four-tile `StatGrid` with
a fifth tile in a Storage band, and the modules a `StatGrid` of eight `StatLink` tiles: thirteen
metric cards on the page the operator, having had them taken off a project's Overview (§16), wanted
none on. A tile said "9 / running / 11 containers · 2 stopped" and left the reader to join it; the
row says *9 containers running, 2 stopped*. The page is still this section's reference for
everything but pass 2's tile: on a page being brought to the deploy pages' shape a figure is a
`ReadingLine` or a row's sentence, and the tile stays where a run of figures is read as a table
(Metrics' five, the Docker overview's). Its waiting state changed with it: the machine's line and
its readings are bones at their heights (`components/bone.tsx` holds a line of words open) and every
block under them is drawn and reading for itself before the socket's first frame, where the whole
page had been four grey boxes.

**It took a third pass in 0.8**, at the operator's request, because it did not include what the
product does: Terminal, Excalidraw, Files and Remote Desktop were nowhere on it, Databases
and Backups were one figure each, and the only thing it offered to *start* was a project. Nothing
above was taken back; what the page gained is the answer to "what was I doing, and how do I start the
next thing". **Shortcuts** opens the page under the identity line: a card per thing a visit
starts, each counting what it adds to (*6 projects*, *3 open · operator*) so it is data rather than a
caption (pass 8), and gated
on the section and on the capability its destination checks — a shortcut that opens a refusal is a
dead button. New drawing makes the drawing where it is pressed, as the list's own New does, and
opens it.

The run grew to eight and in 0.8.5 was cut, at the operator's request, to the four that are started
from here rather than from their own pages: New project, New terminal, New drawing and Browse files.
Add a site, Add a database, Add a computer and Back up each have a section one press away that opens
on the same verb; the list of sites, read only to count on its card, went with it. Four took a card
of their own, `ShortcutCard` in `overview/shortcuts.tsx`, rather than `ProductCard`: that is a button
you pick one of in a form, and these go somewhere, so each is a link across its whole face that opens
in a new tab like any other. The run is laid out against the content's width by a container query, two
by two until 42rem and four across after it, because the sidebar makes the window's width no guide to
what a card has. A card stays about 56px tall on a phone and 60px beside a wide run, and its end glyph
— `Plus` to make, `ArrowRight` to go — gives way wherever the label needs the room.

Under Deployments, three
**pairs**: the shells open on the server (`workspace.tsx`
— working first, each drawn as the program in it, a row opening `/terminal?session=` on that session)
beside the drawings edited last, each its own preview; the databases (the
control center's fleet read, its order, a row opening the database's home) beside every backup job
(`data.tsx`); then the processes beside the activity, as before. A pair holding one block lets it take
the row. Every row in the first two pairs is taken, so it is a `ChoiceRow` or a lit card (pass 3).

Databases and Backups left the tiles for their blocks, and the System services tile (systemd's
running services, or its failed units as the figure in red) and Remote Desktop took their places.
The Backups tile had said *Last run OK* off the newest run on the server, so a nightly job that failed
was hidden by an hourly one that succeeded after it; the block counts and orders every job by its own
last run (`order.ts`, unit-tested). Every tile's figure is now a count with its word as the tile's
`trailing`, as the readings write *6.5 GB free*, because the tiles went two to a row on a phone
(`StatGrid dense`, the readings too) and *3 certificates* at 24px did not fit half of one; the
products in a tile's hint are left out below `sm` for the same reason. On a phone the fleet keeps its
worst three, so at 375 px the page holds its four new blocks and the shortcuts in about the height
the old one spent on eight full-width tiles and six stacked projects.

The 0.7.1 pass asked of each block whether it answered the question a reader opens the page with,
and two did not. The Health panel that stood between the readings and the projects — the
recorder's findings with every module's beside them, and a sheet of fixes behind each — was removed
on 2026-10-08 at the operator's request, with `GET /system/health` and the advisor routes behind it;
the machine is read from its readings and each module from its own tile and page. The
projects, which are why most visits happen, were one figure on one tile; they are the fleet's own
cards now, worst first and two rows at most, and the tile went to Git. And nothing on the page said
*who* was spending the CPU the first tile reported, so the Metrics page's top processes sit beside
the activity list, which reads the last day rather than the last hour: an hour was "Nothing in the
last hour" on most visits. The two headers there share a height so their hairlines meet across the
gap (§15 pass 9). Each activity row shares the audit trail's product mark, with a
source name in its stable lane hue. Common actions read as verbs with the target stepped back in
monospace; the original action and complete target remain on the title. A status word and dot name
the outcome: an audited request is Accepted, while a deploy or backup is Succeeded only when its
recorded run detail says so. Failures and warnings keep their state hues, and pending or running
work is never labelled successful. The exact time sits below the outcome, the elapsed time and
recorded detail below the action. The header offers the full Audit log to administrators, matching
the trail's `system.admin` capability, without making these readings into destination cards.

## The Overview's top (§15)

The top was taken apart once more in 0.7.1, at the operator's request, because it read as a still
row of five numbers under a line with a button stuck to its end. The Metrics button had been the one
control on a line that otherwise only describes, beside the verdict, wearing the rising-trend glyph
where the sidebar draws Metrics as a chart: it is now the readings' own `Section` head, worded and
drawn as Deployments' "All projects" is, beside a `Status live` that is the truth about the socket
(*Live* while it is open, *Reconnecting…* while it is not, because the figures are then the last
frame). The fifth tile — the fullest filesystem's free space over a thin meter — was the one figure
in the row that fills rather than moves, and the one that could name only one disk, so storage is a
band under the four (`components/overview/storage.tsx`): every real filesystem, up to the four
fullest and two to a row from `lg`, as its name and free space over a wide bar over its size, device
and read and write, the free space in the bar's tone — beside a Disk I/O reading under Network, live
and with its hour like the four, so the band keeps the tiles' columns. The hours now draw through
`TileTrend`, which leaves out a line that never moves on a scale of its own; the page's own copy of
it had drawn a steady network as a band filled to the top.
What made the four "alive" is the sanctioned motion and nothing new: each figure is a `NumberTicker`
(`LiveFigure` in `components/live-figure.tsx`) that counts up once on arrival and then glides
to each two-second frame, its unit held outside the count so 980 KB/s becoming 1.0 MB/s swaps
rather than counting down; the CPU figure carries a bar per core from the same frame (the busiest of
neighbours when there are more than 32), which says whether 90% is every core or one pinned; and
each tile's name is keyed by its sparkline's series colour (a 2×10 bar until 0.9.1, a round dot
since, when the operator took every colour bar out).

The 0.7.0 pass took two things off it that had been saying the same figure twice: a `MetricStrip`
of uptime, processes and cores in the header's corner (facts about the machine, now in its identity
line; the cores were on the CPU tile as well) and a "Last hour" panel of four sparklines whose every
value repeated the tile above it (the sparklines are in the tiles now). The Metrics page opens on the
same identity line with the processor as its mark.

## Metrics (§15)

**Metrics took the Overview's readings in 0.7.1**, at the operator's request, because beside the
Overview it read as the dead page of the two: ten grey tiles over still meters, and every chart a
pale line in the same few hues whatever it measured. Its readings are now the Overview's — a
`Resources` section headed by the window the trends cover and the socket's `Status live`, holding
five tiles (CPU with a bar per core, memory, load, network, disk I/O), each figure a `LiveFigure`
gliding to every frame, keyed by its line's colour and carrying the window on screen as its
`TileTrend`, so picking 24h redraws five shapes before a chart is read. A tile with no line yet
(load on the live feed, anything while the record is off) keeps a meter against its ceiling. The
other five tiles each said what a block below says better and went to it, as the Runtime page's
did (pass 2): storage to the filesystems, pressure to the head of its chart, the processes to
Load's, sockets and open file handles to Sockets', the hottest sensor to Temperatures. Below them
the page is one `Section` per resource rather than two long runs of charts with the hardware as a
third: **Processor** (the utilisation chart beside the cores, each a column filled to its share in
the processor's colour, and the temperatures under them), **Memory** (where the memory is — what
programs hold, the cache, what is free, as one bar with the span the kernel calls available drawn
over it, and swap — beside its chart), **Network** (throughput beside sockets, and the interfaces,
each as its owner's mark and a bar of its in and out against the busiest), **Storage**
(the Overview's filesystem rows with their scan, beside capacity; throughput, operations, latency
and inodes), and **Saturation** (pressure beside load). Every series takes its measurement's hue
(§10). Neighbouring heads share a height so their hairlines meet (pass 9). The "Click a chart to
pin a moment" caption and the shortcuts button that stood as two lines above the identity line are
gone: the shortcuts are its last control, and the pinned moment's strip appears only while a
moment is pinned, held at the top of the scroll because the chart it was pinned from is usually a
screen down.

## Docker events (§15)

**Docker's Events page took the overhaul on 2026-10-08**, at the operator's request, because it
was the greyest page in the section: one plain column of 13px sentences with a grey dot each, no
figure anywhere, and a database in a restart loop as sixteen identical rows that pushed everything
else off the screen. It is `components/docker/events-page.tsx` over three files beside it. It opens
on the Docker section's identity line (Docker as its mark and version, the host, how long the
dashboard has been listening and how much it has kept) with the verdict beside its name — a loop
still going is named, then the containers that failed, then *Nothing failed* — beside the socket's
`Status live`, and at its end a 1h / 6h / 24h / All window that every block below reads over. Pass 2 was kept
the Logs page's way until 0.9.0: four `StatButton` tiles (failures, restarts, created or removed, external),
each counting up as it lands with its shape across the window as its `TileTrend`, keyed by its
line's colour, and each narrowing the table and the feed to what it counts. Since 0.9.0 they are
the band every Docker page opens on (`event-band.tsx`): what happened as one bar of four kinds,
each a press that narrows the page, and Recent, the newest of those on the window's axis, with the
window on the band's line. The containers the
record names are a framed table, one row each and worst first (`event-activity.tsx`): the
container as its product with its last outcome in the corner, its state now from Docker's own
listing (a loop shimmering, *out of memory · 25m ago*, *Removed*), its window as a lane of marks —
each outcome in its hue, a failure a dot, a loop a red band, dashed where the dashboard was not yet
listening, the newest mark breathing for two minutes — its failures, its comebacks and who last
acted on it. A row narrows the feed; its name opens the container. The feed (`event-feed.tsx`)
folds a restart loop into one row, whose events open in a popover off its button so the row keeps
one height, and a container's run of events moments apart (a restart's kill,
exit, stop, start and restart) into another (`foldBursts`), each row its product mark, its subject in
the project's lane hue and who did it with the audit log's account of how that is known; a row new
since the page opened rises. The feed is a plain section since 0.8.7 — a title, a hairline, a
hairline between rows, scrolling with the page — where it was a framed panel with a scroll of its
own: it is sentences, and the frame is the table's above it. An exit moments after a `kill` of the same container was asked for, and
is read as the stop it was (`settleAskedExits`): a restart pressed here used to count as a crash
because most programs answer SIGTERM with 143. The colours are the sanctioned ones: state hues on
outcomes, `--tag-*` for kinds (created or removed in cyan, external in violet), lane hues for
projects.

## A container's page and a stack's page (§15)

**A container's page took the Overview's line in 0.7.1** (2026-10-08), at the operator's request,
because it was the page with no life: a strip of four grey label-and-value pairs (container, image,
id, compose stack) over a filled tab list, and an Overview of three columns of grey fields, a run
of port tags and a second list of the same ports, with nothing on it that moved. Each of the four
went into the identity line (`docker/container-identity.tsx`, through `HostIdentity`): the
container is its product's mark with its state in the tile's corner and its name as the title; the
image is a fact with its tag in ink, the project a link in its lane hue (`hueFor(stack, LANES)`, the
containers table's), the uptime ticks, the restart policy is said as what it does ("restarted
unless stopped"), and the id copies. The verdict at the line's end (`docker/container.ts`) is the
one Docker's state cannot give: a container failing its health check is not "Running", and a crash
loop is not "Restarting", the word a deliberate restart gets — the failure diagnosis tells the two
apart. The views are the underlined strip (`tabClasses`) with Environment and Storage counting
what they hold. The Overview reads in the order a reader asks: why it is not working (the failure
notice, its evidence a column of facts, and nothing at all for a healthy container — the line
already says it); what it is using (`container-readings.tsx`: the Overview's four readings, each a
`LiveFigure` gliding to Docker's frame every second with its last hour as a `TileTrend` keyed by its
series colour, memory as a `Meter` against a limit where it has one); what is wrong with it; a
picture of how it is reached and what it keeps (`container-picture.tsx`, the runtime map's three
lanes: each published port as where it is reached from and each network as the
name it is reached by, the container as its product, each volume, folder and tmpfs it mounts — the
line amber where a port answers around the firewall, red where Docker's socket is mounted, dashed
where a thing does not outlive it, pulsing while it moves traffic); the containers it runs beside
as a live framed table (`container-company.tsx`, its project or failing that its own networks, off
the containers socket, this container the selected row); then what just happened to it
(`container-recent.tsx`, Docker's events with a loop folded to one row) beside how it runs (the
command coloured by `ShellWords`), and its ports and networks as framed tables. The Environment tab
is a framed table with a filter that does not search a hidden value. The page draws every verb the
container has — it used to draw only the inline three, so pausing or removing it meant the list.

Each of its other views then took the pass of the page it stands in for, so a reader who has learned
that page has learned the tab. Usage is a project Runtime's (`deploy/runtime-usage.tsx`): the Metrics
page's five readings over its chart panels, with the container's own crashes and folded restart loops as its markers,
then the Metrics page's breakdowns of the same frame — memory as one allocation bar, quota, share,
throttling and tasks as tiles, the limits beside the change that sets them, interfaces as a framed
table with an in/out bar per row. Storage is the file manager's workbench, a rail of mounts drawn as
their kinds beside the listing in one frame. Logs is the logs page's own pane. Inspect is the run
page's Details shape, a rail of the document's sections beside the picked one in the `CODE` hues.
Configuration is Health's, each finding a lit card on its level's wash that prepares its own change
and the editor's head naming the edited fields in `--git-modified`. Shell is the deploy Console's,
its strip saying the account and the directory and its first commands typing themselves in. The
Environment tab colours each name's namespace in its lane hue and each value in its kind's code hue,
its kinds chips that count and narrow, as the deployment Variables page's do.

**A stack's own page took the Services page's shape on 2026-10-08**, at the operator's request,
because it had no life: a `MetricStrip` of three facts over a filled tab list and a column of grey
cards that each said a state word, so a worker in a restart loop read "restarting" beside a
database a step from its memory limit reading "running", and nothing on the page moved. Where the
strip's figures went, and what took their place (`docker/stack-detail.tsx`):

- **The identity line.** `HostIdentity` with the stack's services' products overlapping as its
  mark, the compose file, the directory, the checkout (branch, uncommitted changes in
  `--git-modified`, how far behind), how many services run and how many ports it publishes; at its
  end the verdict (`stackVerdict` in `docker/stack-service-readings.ts`, unit-tested): the worst
  thing true of it, counted — *1 service failing* before *1 not created* before *All 5 running* —
  which narrows the table to those services when pressed, and the compose verb's participle
  shimmering while one runs. The deploy verbs stay beside the way back. The views are the
  underlined strip (`tabClasses`) over Radix's tabs, the identity line's own rule giving way to it.
- **The services as a table of live readings** (§12), from the containers socket the containers
  page reads rather than the ten-second stack poll, so a state changes when Docker's does. A state
  is read with the event log beside it: an exit the OOM killer caused is *Out of memory*, a
  non-signal exit *Crashed*, a deliberate 0/130/137/143 *Stopped*, and a restart loop says how
  many times in how long. State chips count and narrow, the toned ones drawn only while something
  is in them. Each service keeps its lane (`hueFor(name, LANES)`, the hue its lines take in the
  stack's log) as a stripe down its row (removed in 0.9.1 with every colour bar; the lane still
  colours its log lines and its sparklines), and its compose verbs are declared once per service —
  Restart and Stop inline, Recreate service and Pull & redeploy in its menu with Logs and Open a
  shell, each confirmation carrying the command as compose runs it.
- **The band** (`docker/stack-usage-band.tsx`), bar readings (§10) like the Services page's:
  Processor and Memory, the stack's five heaviest services as spans of one bar the size of the
  server beside everything else in use, figures gliding to each frame and the rest counted at the
  strip's end; and Recent, the stack's events as ticks on the last day, a restart loop one tick, an
  OOM kill on the exit it caused and a passing health check only after a failing one, each service
  named in its lane.
- **A picture of how it is reached** (`docker/stack-map.tsx`), the deployment Runtime map's shape
  under a plain head: the ports it publishes on this server, its services as their products with
  their state in the tile's corner, and the Docker networks they are on. A wire pulses while its
  service moves more than a kilobyte a second, at the Network section's `pulseDuration`; it is
  dashed and red where a port leads to a service that is not running, and amber where a database
  answers on every interface.
- **The order and the rail (0.8.7).** The Services view reads as a container's Overview does: the
  band, the picture, then the table, which came first until then and left the picture as the last
  block. The Stacks list stands beside the page from `xl` (`docker/stack-rail.tsx`), and the page's
  own side-by-side blocks wait for the width the rail leaves them (`docker/beside-rail.ts`).
- **The other views took the same vocabulary**, borrowed from the pages that did each job first:
  every service anywhere on the page is its lane, its product and its name (`ServiceLabel` in
  `docker/stack-diff.tsx`), and every diff of the compose file is one view grouped by the service
  each hunk changes, `+N −M` in the colours its lines are drawn in.
  - *Deploy preview* (`docker/stack-preview.tsx`) opens on Deploy's decision as one sentence over a
    bar of the services in the colour of what happens to each — amber recreate, blue create, green
    start, red remove, the quiet rest — then a framed table of them with why, the tag the image moves
    to and what the service is doing now, then the diff beside the volumes. Its Group-tinted verdict
    fence and its eyebrow sections are gone.
  - *Compose file* (`docker/stack-compose.tsx`) is the Files editor's shape: a pane with the path
    and the file's state (*Saved*, *Unsaved changes*, *Valid · 6 services*, *Compose rejects this*)
    across the top, an outline beside the editor where each service carries its live state, and a
    status line naming where the cursor is in the stack's terms.
  - *Files* (`docker/stack-files.tsx`) puts what compose reads from the directory over the browser
    as a table, with git's word for each change and a missing path's cost, and lets the browser take
    the tab's height (`fill`); the browser's rows carry the same words after their names.
  - *History* (`docker/stack-history.tsx`) is a backup job's shape: a strip of time in the colour of
    each compose file — the lanes in order of first appearance, so the versions a history holds never
    share one — over a rail of the records and the one picked read whole.
  - *Logs* (`docker/stack-logs.tsx`) keeps the log page's workspace and puts the services that write
    into it over it, the way that page's rail lists its sources: lines and errors in the last hour,
    their shape in the service's lane, a press narrowing the log to one.
