# Decisions: Databases

Why these pages look as they do. The stories were told in `design-system.md` §14 and §15 until 0.9.0
and are moved here verbatim, under a heading per page. They are history: a rule they state is in
[`../standard.md`](../standard.md) or the page it links to, and where a story and the standard
disagree, the standard wins.

## The control center (§15 pass 2)

The Databases control center took it in 0.7.1, at the operator's request, because it had no
life: a list of what needed attention over shelves of grey cards, every one the same weight, and
nothing on the page told one database from another but its words. It had no tiles to give up;
what it gave up was the attention list, which the operator asked to go, and nothing it said was
lost — each concern is said, in its tone, where the thing is: the verdict and the first card on
its shelf for a server that does not answer, the Backups block and the card's foot for a dump
that is old or missing, the card's foot for a port open to the internet, *Found on this server* for a
server that waits for a password, and every fix it carried (Start, Back up now, Restrict to this
server, Settings) a verb on the card. A MongoDB 8 server this kernel keeps from starting (the fleet's
`kernelProblem`) says *MongoDB cannot start on this kernel; apply the mitigation* where a failed
card prints what its server said, with the sentence behind it as the line's title, and its foot
offers *Apply mitigation* in Start's place (Start would only start it failing again), the same as
the menu's *Apply kernel mitigation*: the ordinary confirmation, saying what the recreate does,
then `POST /databases/{id}/kernel-mitigation` and *Starting…* until it answers. For a compose
service the line says to add the mitigation to its compose file, and nothing is offered. A found
server the inventory reports the same way (`kernelProblems`) says it as its row's reason, and its
one press is *Apply mitigation* (`foundAction`'s `kernel-mitigation`), asked first, after which
Connect is what the row offers. The database's home says the mitigation is on, and why, as a
*Kernel* row under *Runs as*. The page opens on `HostIdentity`: the server's engines as
a stack of their own logos, the commonest and then the largest first, the host's name, how many
databases, what they store, the sessions open, how many things read them (a link to the map),
how many were dumped in the last day and, in amber, how many never were; at its right end the
verdict — how many do not answer, else how many are stopped, else *All N answering* — beside
Scan and Add a database, which left the page's own row. Under it
`components/database/fleet/fleet-band.tsx`, over the unit-tested `fleet/band.ts`, reads the fleet
three ways the cards cannot, in three bar readings (§10): **Storage**, what each database holds as
spans of one bar with the five largest named; **Sessions**, the open sessions — a key–value store's
clients — as one bar, a server that did not answer counted at the strip's end rather than drawn
as zero, the line giving the products that read each; and **Backups**, every database the dashboard
can dump counted never, over a week ago — the age every other reading of the section calls stale —
this week and today on one bar, worst first, the line naming the worst database in that standing
with its newest dump's age. A press opens the database, or under Backups its Backups page. Until
0.8.5 each block's five were rows under its bar, Backups' on a `StandingBar`. Backups and
the Sites page's Certificates had also drawn a track under every row with a dot on it until 0.7.5, which
went at the operator's request: the track had no scale, so the dot said less than the age beside
it, a database never backed up was drawn at a place it did not have, and the track's hairline
under each row's own rule read as a double line. Each database keeps one hue (`fleetHues`, a
lane hue without slate dealt in the order the connections were saved, so five databases are
five hues and a new one moves nobody's) on its span and its mark's rule in the strip; the card's edge carries none (removed in 0.7.5 with every card's stripe, at
the operator's request), and its Stored figure carries a short bar in that hue
against the largest database. Until 0.8.6 the cards could be laid across as a table and the page
carried a picture of what the databases feed — two lanes of marks drawn the way Configuration
draws the path a request takes, with a line of the databases nothing was seen reading under it —
and the map was the same picture with a list of readers beside it. In 0.8.6, at the operator's
request, the table and the control center's picture went (the cards are the one way the fleet is
drawn, and what feeds what is reached from the identity line's "read by" figure), and the map
became the server map's canvas with one diagram for each database
([server map](../../server-map.md#the-databases-map)). *Found on this server* lists its servers in the order they can be
connected — one press, a password, a start, nothing this page can do — and a file row's second
line is the folder it is in, cut from its start, where it repeated the file's whole path.
In 0.8.2, at the operator's request, it lists only what there is to take: servers, and the files
an application keeps. A program's own state, a copy kept aside (a backup, the dashboard's store
under its own name elsewhere), a data directory no server runs on and what was ignored are behind
one *Set aside* fold, whose closed line counts each group and whose open body shows one group at
a time, picked by a chip; ignore marks whose instance is gone are one counted line with *Forget
them* rather than rows of bare keys. Every found row is the same two lines — where it is, then
after a dot why it waits — with its verb in one fixed width, so the readings line up; only a
connect that just failed adds a line, in full. The fleet's cards are one height whatever they
carry: the Stored track is drawn empty where no size is known, the failure block holds the
figures' height, and the facts are one line that cuts rather than wraps, so cards on shelves
standing side by side (separate grids) still line up.

## The section's pages (§15)

The Databases section omits the top metric-card sections on every page. The control center starts
with the server's databases as an identity line laid out as a project's header (0.8.7): the engines
as the mark, the server's name with the verdict after it, the figures as facts with no dot between
them, *Add a database* as the one command and a `⋯` menu holding the rest (Metrics export, MCP
server, Scan this server again, which were three grey controls beside the command). The line is
drawn while the fleet is being read, when the read failed and with no database connected, so it is
in place from the first paint; an empty server keeps it and leaves the command to its empty state.
Then a band of storage, sessions and backups (each Backups standing marked with the engine of the
database its line names, not a bare count), then the saved databases as cards with search, engine
chips and how they are shelved. The attention findings it opened on were removed in 0.7.1; each fix they carried is a
verb on the database's card. The former metric-card `?show=` filters no longer narrow the inventory.
Individual database cards retain the facts needed to compare connections, and read them as one line
rather than three figures under three labels (`fleet/fleet-card.tsx`, 0.8.7): who is connected in
words ("4 sessions open") with the last hour of it as a line in the database's hue, its newest dumps
as the strip a database's own Backups draws with "backed up 3h ago" under it, and what it holds as
words under the reading ("23.4 MB stored · 13 tables"). The hour is the recorder's history for that
database (`fleet/trend.ts`, unit-tested), asked once and then every five minutes; the strip is the
backup summary's `recent`. Every part keeps its place with nothing to draw, so a card with no
recorded hour, one never dumped and one that failed stand as tall. A collector that could not read
(*Found on this server*) is drawn as the product draws any missing thing: Docker's own unavailable
state with the step that fixes it (`DockerMissing`), the daemon's sentence folded away, and no
claim that everything found is connected. A database home starts with its
header, then a picture of how it is used with one line each for what it runs as, where it is reachable
from and its dumps, its recorded activity as lines, its server log, its rankings and the server's
other databases (below). Activity is recorded by the backend every 30 seconds
while the dashboard runs, including when no database page is open. Performance opens on its view
strip, Advisor on its findings with severity and category filters, Access on its accounts and list
filters, and Backups on its schedule state, next run, action and dump list. These controls stay beside
the content they affect, without recreating a headline metric section.

## Every page of one database opens on the database's header

**Every page of one database opens on the database's header** (0.8.7; `shell/header-rule.ts`,
unit-tested, drawn by `shell/database-shell.tsx`). A project repeats its full header on every one of
its pages and a database follows it: the engine's mark, the name as the switcher with whether it
answers and the environment and *protected* tags after it, one line of facts (engine and version,
its support window, where it runs, address, database, account), *Open data* as the one brand-faced
command, *Connect* as the one outline verb and the verb menu (`shell/database-header.tsx`,
`HostIdentity` in a project's layout). It is the same header at the same height on Home,
Performance, Access, Alerts, Backups, Import, Logs, Generate, Search, the assistant and every
Settings page, and over an address that is none of them; it scrolls with a reading page as a
project's does, and the two pages held to the window under it (the assistant, Logs) take the height
it leaves and the page's own gutters. Three kinds of page keep a line of their own instead, and the
rule is where each is named: **a workbench that heads itself** (Data, Query, Schema, the Diagram)
carries the database's line as its frame's head and the shell draws nothing over it; **the page of
one item two levels down** (a statement, a table or a session under Performance, an account under
Access) opens on that item's identity line and keeps the one-line strip above it (a smaller copy of
the header in its order: state beside the name, the Open command, Connect and the menu), so the page is
not about two things; and nothing else. Until 0.8.7 only Home had the header and every other page
the strip. Where the line itself is under 42rem wide (a tablet beside the rail, a phone) the
command, Connect and the menu stand in a row of their own under the facts (`HostIdentity foot`), so
the name is read whole and the facts keep the line's width. Every page keeps its own name in the
screen-reader-only `h1`. The workbenches carry no tiles
over their frame (§2); on Logs the lens's readings are the counts on its quick-view chips
(`ServiceLogs readings="chips"`), a reading no view asks being a chip of its own. What gives the
section its colour is the sanctioned five and nothing else: the engine drawn as itself, figures
that move, colour that names a kind (`--tag-*` for a Redis key type, a BSON type, a catalogue kind,
a statement's verb; `hueFor(name, LANES)` for an account, a schema or a namespace; `--git-*` for a
pending change; `--chart-1..5` for a series), the lit edge on what is taken, and work in flight said
as it happens (a `BorderBeam` round a server being started, the participle in its row). Flow pages
keep their visible question as the `h1`, since the question is the work on that screen (§16).

## A database's home and Performance (§15)

**A database's home took the deploy pages' shape in 0.7.5**, at the operator's request — "more alive,
more colouring, real logs", with the deploy pages as the reference — because it read as a grey page:
one chart in the brand's near-white behind a strip of four tabs, a column of tall cards for what used
the database, and three columns of 12px label-and-value pairs that ran into each other. It keeps its
identity line and no tiles (the Databases rule above); what changed, block by block
(`components/database/home/`):

- **How it is used** (`picture.tsx`) is the Runtime map's three lanes on the page's own ground:
  what reads the database on the left (the consumers route the cards listed, drawn with
  the fleet map's `ConsumerMark`), the database in the middle as its engine with its sessions and
  size, and on the right what it holds — a SQL engine's schemas summed from its table sizes
  (`holdings.ts`, unit-tested), a key–value store's numbered databases, a document store's
  collections, each in its name's hue and a way into Data — over where its copies go, the dumps. A
  reader's wire pulses while it holds sessions, quicker the more it holds, dashed where it could only
  reach the database, amber and red for a stale or broken link; a holding's wire is still, because
  the record says how large a schema is, not which one a session reads; the dumps' wire is green
  while the newest is under a day old, amber past a week, dashed when there is none, and pulses while
  one is taken. Below `lg` the lanes stack and each node says in words how it is wired.
- **Activity** draws every view the engine fills as a chart of its own, two to a row, each headed
  by its newest reading through `ChartReading` (`metrics/chart-reading.tsx`) with a meter where the server states a ceiling — open sessions against the most it
  accepts. Each line takes its measurement's hue (§10): sessions blue, transactions violet, rows read
  green against written magenta, the cache hit rate a line in the brand's pale blue rather than an
  area, which at 99% was one grey slab. The range is one row of presses (1h, 6h, 24h, 7d) over all
  of them, where a select stood beside the tabs. Nothing here says Live: the record is polled.
- **Server log** (`server-log.tsx`) follows the server's own log — the source the Logs page opens
  on, read from Docker, the file or the journal, never by dialling the engine, so a stopped server's
  home shows the lines that say why — over the same log socket, in a `Well`, each line in the console's
  colours (`LogText`, the level's word, an error's row on the danger wash and a warning's on the
  warning wash), held to the foot while the reader is there. Its head counts the errors and warnings it
  holds and carries the socket's `Status live`. A reconnect keeps what is drawn and takes only what is
  newer, through the console's own `resumeFrom`/`unseen` (`logs/resume.ts`). A server whose log is not
  on this machine says why in the block's place.
- **The rankings** keep their rows and gain colour that names a kind: a statement's bar in its verb's
  `--tag-*` hue, a table's or a collection's in its name's hue, the one the picture draws it in.
- **The reference blocks** — Runs as, Reachable from, Backups — and the file's facts are `FactRow`s
  (`blocks.tsx`): the name on the left at the body's size, the value beside it, a hairline under each,
  as a project's Runtime lists what a container may use.

**The home lost its charts, its columns and its footnotes in 0.8.7**, to read as a project's Overview
does. *Activity* is one `ReadingLine` a view (`home/activity-lines.tsx`, worded by `home/lines.ts`,
unit-tested): a mark in the measurement's hue, the newest reading as words ("12 open of 100", "10
transactions a second") with the view's other measures after it, and the chosen window as the shape
at the line's end. The block's link and every line open Performance, where the charts with their
axes, Min/Mean/Max/Now and the marks on the time axis are; the range stays on the title's line, with
how long the record is kept beside it and how it is collected on the pointer. A line is one height
with a reading and without. A server that is not answering keeps its recorded activity as charts
(`Activity`): a line says what a measure reads now, and a stopped server has no now. *Runs as*,
*Reachable from* and *Backups* are one line each directly under the picture (`ServerLines` in
`home/reference.tsx`): a mark, the thing in a name or a word, one run of facts with no labels, and
the way to the page that changes them at the line's end. What a reach word means in full is behind
an ⓘ, not a sentence in the line; how long the server has been up is a fact of *Runs as* rather than
of the identity line, where it arrived late and wrapped it. A sentence under a block became a fact
on its title's line with the whole of it on the pointer ("share of runtime since 42d ago", "by
rows", "of the last 20 kept · slower than 100 ms"). The header keeps one brand-faced command (*Open
data*, in the engine's own word) and *Connect* as the one outline verb; *Query* is a word in the `⋯`
menu, on every page of the database, a workbench's own head included (`useOpenVerbs`).

**A database's Performance took the home's shape in 0.7.5**, at the operator's request — "more
alive, more real logos and colouring" — because beside the new home it read as the grey page of the
two: the Overview drew the same activity in the brand's near-white with no reading in any head, the
cache hit as one slab, and four figures of the dashboard's own pool under it; Redis and MongoDB
opened on empty plots that said a rate would appear in six seconds, though the recorder had kept the
last week; sessions, clients and operations were names in a column. It keeps its view strip (the
Databases rule above); what changed, view by view:

- **Right now** (`ops/performance-picture.tsx`) heads the SQL Overview: the session list grouped
  by the name each client gave the server (`clientGroups`, unit-tested), each drawn as its program
  (`database/clients.ts`, `DbClientMark`) — a deployment or container the consumers route found as
  the product it runs, otherwise what the name says (`psql`, `pg_dump`, Grafana, Metabase, a JDBC or
  Node driver), the dashboard's own connection as its J carrying the pool's words that the four
  figures said — wired to the server and on to the four things a session can be doing, each counted
  and a way into Sessions narrowed to it. A client's wire pulses while one of its sessions runs, red
  while one waits on a lock, amber while one holds a transaction or runs too long; a state's wire is
  dashed while nothing is in it. It moves with the five-second session poll.
- **Activity** is the home's: each chart through `ViewChart` with its newest reading in the head
  (sessions against the server's ceiling), every line in its measurement's hue, the cache a line,
  and the range presses (`HistoryRange`) over all of them — the Overview had no range before.
- **Redis and MongoDB** open on the recorder's last hour (`joinRecorded`, `recordedSamples`,
  `recordedServers`, unit-tested) and carry on with the page's three-second samples; each chart is
  headed by its readings (`ChartReadings`, `SeriesReading`), memory, the cache and connections with
  a meter against their ceiling. MongoDB's latency is not recorded, so that one chart starts with the
  page and its caption says so.
- **Logos where a client is named**: SQL sessions, Redis clients (by name, else the library the
  client reported) and MongoDB operations each lead with `DbClientMark`; the lock queue sets the
  bare logo beside each session's application.
- **Statements** take their verb's hue, as the home's ranking does.
- **Replication** (`ops/replication-picture.tsx`) replaced its three figures with the picture of
  where the data goes: the server it follows, this one by its role, the replicas streaming from it,
  and the slots holding its log — a slot with no reader dashed amber, the one reading to act on.
