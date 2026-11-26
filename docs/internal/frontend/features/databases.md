# Features: Databases

The route areas of `/databases`: what each page is responsible for, then, under **Implementation**,
the files that own it and the specs that check it.

## `/databases`

Routes: `/databases`, `/databases/map`, `/databases/new`, `/databases/[id]` (+ `/assistant`,
`/data`, `/query`, `/search`, `/schema`, `/diagram`, `/generate`, `/performance`,
`/performance/statements/[key]`, `/performance/tables/[name]`, `/performance/sessions/[pid]`, `/logs`,
`/access`, `/access/[account]`, `/backups`, `/alerts`, `/import`, `/settings`, `/settings/network`,
`/settings/resources`, `/settings/pooling`, `/settings/parameters`, `/settings/extensions`,
`/settings/version`, `/settings/danger`)

Every page under `/databases/[id]` opens on the database's header, as a project's pages open on the
project's (`shell/database-header.tsx`, drawn by `shell/database-shell.tsx`): the engine's mark, the
name as the switcher, whether it answers, one line of facts, *Open data* as its one command, Connect
and the verb menu. The exceptions are decided in one place (`shell/header-rule.ts`): Data, Query,
Schema and the Diagram carry that line as their own frame's head, and the page of one item (a
statement, a table, a session, an account) keeps a one-line strip over the item's own identity line:
a smaller copy of the header in its order (state beside the name, the Open command, Connect and the
menu).

The control center draws every saved database as a card, shelved by where it runs, its engine or its
environment (the table it could be laid across as, and its picture of what the databases feed, went
in 0.8.6). The map (`/databases/map`) is the server map's canvas with one diagram for each database
— what reads it, the database, the container it runs in, the volume that keeps it and the backups
that take it — its rail by engine, its arrangement kept per account under its own name,
`?database=<id>` opening on one ([server map](../../server-map.md#the-databases-map);
`lib/database-systems.ts`, unit-tested). On a phone the map is one pane at a time, the databases, the
canvas and the inspector, picked from a bar at the foot (`MapCanvas panes`; the server map keeps the
canvas alone). The control center opens on the databases' line (*Databases* and its verdict) with
*Add a database* as its one command; a card reads what it holds and who is connected as figures,
what uses it as their marks, and what needs a hand at its foot; and the `⋯` menu at the line's
end opens a Metrics export dialog (the
Prometheus URL, a metrics-only API key, a scrape config and how long activity is kept) and an MCP
server dialog (`fleet/mcp-server-dialog.tsx`: the MCP endpoint, the account's MCP keys with what
each reaches, a key made for chosen databases each to read or change — protected ones and non-SQL
engines listed with why — and for the deployments to read or deploy, every deployment or the ones
ticked (the deploy key dialog's `DeploymentChoice`) and whether it is offered every deployment route
as a tool (on for a new key, off for one made before the generated tools), shown once with Claude
Code's, Cursor's and VS Code's configuration, what a key reaches edited without a new secret, and
revoking; `tests/browser/database-mcp-server.spec.ts`). Engine-specific inventory and Home readings,
data/query/schema tools (Schema also writes, edits and drops functions, procedures and triggers on
the engines that have them, with the exact statement shown and the destructive capability the Query
tab asks for the same CREATE), generators, performance, logs, access, backups and settings.
Generate's *Seed data* card (`generate/seed.tsx`, gated on `seed`) says first what it does — made-up
test rows inserted beside the ones already there, amber on a connection labelled production — then
plans them for every table or the ones ticked — a tab per table in the order they are inserted with
its rows now and after, its first rows under two-line column heads naming each column with its type
and the generator filling it (changed there; a choice for a column the table has since lost is set
aside, said once and left on screen until the next change), and the statement a row is inserted with — and inserts
them behind `SqlReview` for a role that may change data on an unprotected connection; the preview is
every role's, and what was written, refused or left out is said table by table with a link to each
table's Data. Add a database (`/databases/new`) draws what is about to be made beside its catalogue
until an engine is picked (`connect/picture.tsx`): the engine as a mark not added yet, wired to the
container or the address and on to this dashboard. Home draws how the database is used (readers, what it holds, its dumps), one line each
for what it runs as, where it is reachable from and its dumps, every activity view as a line of
words with the chosen window's shape (the charts are Performance's; a stopped server keeps them),
and its server log followed live. Performance opens on who is connected
right now (each client as its program's logo, wired to what its sessions are doing) over the
recorded activity charts; Redis and MongoDB charts open on the recorder's last hour. Where
PostgreSQL never loaded pg_stat_statements, *Turn on statement statistics* (Performance →
Statements, and Home's busiest statements) is one dialog of three asked-for steps — the library
added to what the server loads at start, the restart, the extension — and a PostgreSQL server made
on `/databases/new` counts its statements from its first start, with slow-statement plans
(auto_explain) as an option. Logs' Queries view (`ops/queries-view.tsx`) says when the server
records no slow statements — Postgres's `log_min_duration_statement` off, MySQL's slow log off or
kept only in a file — with the statement that changes it to copy, and for an administrator a *Turn
on* that sets the same parameters through `PUT /databases/{id}/settings` (Postgres's reload
included) and reads the log again; a Postgres role that is not a superuser is told so rather than
offered a change it would be refused. Performance's Statement history view
(`ops/performance-history.tsx`) lists what each statement cost over the Overview's window, each with
its time drawn across it and ordered by the column pressed, and opens that statement's page on its
History: its time, calls, time per call (how long half, 19 in 20 and 99 in 100 took, where the
engine keeps run times) and rows. Access
opens on who signs in (each account wired to the server, pulsing while it holds sessions) over the
accounts with counted filters, one card to an account and one height in every state. **An account
is a page of its own** (`/databases/[id]/access/[account]`, `ops/account-page.tsx` and
`account-frame.tsx`; the sheet's `?account=` address redirects there), on the shared `DetailPage`
(`components/detail-page.tsx`): the way back, the previous and next arrows and the shortcuts' help
on one line (the arrows hidden on a phone), its line (mark, name, whether a session is open as it,
its tags, what its grants add up to), one command (a new password) with the drop in the `⋯` menu,
its views on `ViewTabList` with the open one in `?tab=`, and from `xl` the list beside it as a
`Rail` in the filter the list was left in, walked by Alt+↑ and Alt+↓ as Workspace commands with
the view carried. A SQL
account has Grants (memberships and the privilege matrix, staged and reviewed as the server's
statements), Sessions (Performance's sessions narrowed to it, each a way to its panel there) and
Settings (its attributes on one save bar, sending only what changed); a key–value user has Rule
(the rule as the server holds it, and its commands, keys and channels) and Settings (the form that
rewrites it); a document user has its roles, with no strip. The name is one segment of the path,
written once and read back from the path (`ops/account-address.ts`), and a MySQL host or a document
user's database is `?host=`. The page asks before it is left over unsaved work, and a reader who
may not change an account is shown its attributes as words rather than as switches. Backups opens
on where the dumps go (the database and its schedule, this
server's dump tool, the directory) over the dumps table, then, for a PostgreSQL server the dashboard
runs, its point-in-time recovery: the moments it can be recovered to, how archiving is going, its
base backups and restore points, setup with the command line the server restarts with, a recovery
into a new server planned before it starts, and the recovered copies kept beside the server or
switched in for it. Import is a flow page (§16) for the engines that can restore under another name:
it asks where the database is now (the source's address as fields or a pasted string, kept in the
tab and never drawn back), says what stands in the way of a replication before its command, and then
follows the import by the state the server keeps — each table's copy, how far behind the target is,
the cutover behind a statement that the writes have stopped, or a streamed copy's progress — with
the earlier imports as `ChoiceRow`s beside it. Backups' Copy dialog can send a copy to another saved
connection of the same engine. Alerts opens on who is told (the database, this server reading it
every thirty seconds, the notification channels its rules tell, red while one fires) over the rules
as cards of one height (`ops/alerts-rows.tsx`: the kind's tile, the rule's name over its condition, and
where it stands in a column of its own with the reading against its line as a small mark; the status
opens what the server read, the card opens the rule's form) and what was
told lately; an administrator adds a rule for this database or every database from a sheet of the
kinds its engine can be watched for (server down, disk, connections, replication, a backup failed or
missed), with its line, how long it must hold, a cooldown and whether its recovery is told, and
sends a test from the rule's menu. Settings is a group of pages in the rail, as a project's are
(`shell/settings-pages.ts`), each one centred column under the page's one save bar, headed by a
strip of what the server has stored and not yet read (a parameter or a library waiting for a
restart, each named as the way to where it is changed, with Restart beside them for the role that
may interrupt the server; `ops/settings-pending.tsx`; unsaved edits join the strip while it is drawn
and never make it arrive): General (the address as the way the dashboard reaches it, then its
fields in place — host, port, account, password, database, or the whole string — the sealed string
read only once a field changes, every option it carries kept, and Save dialling the new string
before it writes it, `ops/settings-address.tsx`; the labels and protection), Network (who can reach
the port, through its container, drawn with what the firewall and the gateway do about it and amber
for a port only a password guards, and the four answers a container the dashboard owns takes — this
server, one of this machine's tailnet, VPN or local-network addresses, a list of allowed addresses
the gateway table keeps it to, anywhere — as segments with the chosen one's value under them, saved
by the bar and asked for first, the two that take a value with what the server says it will run,
the gateway's rules among it; whether the server speaks TLS with a certificate from the dashboard's
own authority, drawn as the connection with its lock, plain text allowed or refused as a field of
the same bar, with the CA download, a client connection string and the renewal;
`tests/browser/database-reach.spec.ts`, `database-tls.spec.ts`), Resources (its container's memory
and processor limits beside their peaks, the machine and the engine's own buffer, changed in place
or — to lift one — by replacing the container after asking), Pooling (PostgreSQL's PgBouncer: its
settings, its figures on the picture of the way a connection goes — clients and the ones waiting on
the line in, server connections on the line out, the wait for one at the pooler — where it is
reached, and its pools one a line; Connect shows the pooled string beside the direct one),
Parameters (every one under the engine's headings, changed in its row — a yes/no and a closed set
as their words, the rest a field — several at once by the one Save, one request each, with what ran
shown afterwards; `ops/settings-parameters-edit.ts`), Extensions or Modules (each drawn as the
product it belongs to, on one line of one height with Update as its button and Disable behind its
menu; a SQL engine's extensions by category with what each needs, searched and filtered, and on
PostgreSQL enabled with a version, a schema and CASCADE, updated along a shipped path, disabled
after listing what depends on it, and — for one whose library has to be loaded at start — load,
restart and create in one dialog, `ops/extension-preload.tsx`), Version (an administrator's, for a
PostgreSQL server Databases started: the release it runs with its image, container and volume as
its captions, the four steps of an upgrade as marks on the way to the newer release with the line
moving through the one being taken, a check of the newer one listed line by line, the upgrade
followed as a job, and the old server kept until it is finished or reverted) and Danger zone; what
else the server holds is Home's *Databases on this server* block, not a Settings page. A page the
engine lacks is not listed, and an address from the one-page Settings (`#danger`, `?q=`) is sent on
to the page that holds it, `#databases` to Home. Every connection has an Assistant beside its Home
(`database/assistant.tsx`, the deployment's page through the shared
`assistant/agent/subject-page.tsx`), for a role with `service.control`: a chat about this connection
on `POST /databases/{id}/assistant`, filling the window under the header with the connection's chats
and what the open one did in the rail, suggestions in the engine's own words (keys and commands on
Redis), and saved chats opened by `?chat=`. Query keeps the same chat — the same session — in a
column beside the editor (`query/query-assistant.tsx`), for a role that may run statements on an
engine the capability table offers it to: set up from the column by an administrator, the tab's
statement, last result (a small text table), error and plan attached by toggles and sent as
`attached` with their labels drawn as tags, a finished answer's statements opened in a tab or at the
cursor, and a failed statement or a plan asked about from where it is shown (`query/attachments.ts`,
unit-tested). A protected connection says on both that nothing about it is sent while it is
protected. Engine capabilities decide which operations appear; protected connections keep their
write restrictions. A PostgreSQL table's Schema view has a *Row security* reading (`rowSecurity`):
the switches and policies with a template for each common shape and the server's statement before
every change, and a *Try it as a role* test that runs one SELECT as another role in a transaction
that is rolled back; an account's grants on Access list Functions and Columns beside Tables. The
Query page's Plan view carries two index tools the reader presses for (`query/index-advice.tsx`,
`index-advice-model.ts`, the Advisor's opposite: asked for, never shown unasked): **What if?** on a
PostgreSQL plan, which proposes indexes for the plan's filters, joins and sorts, tries one as a
hypothetical index (HypoPG) and shows the cost before and after, and offers the real `CREATE INDEX
CONCURRENTLY` through the schema change dialog, and **Missing indexes** on SQL Server, over its
`sys.dm_db_missing_index_*` views. Automatic Home findings and the Advisor route/API were removed in
0.7.5

A statement, a table and a session each open as a page of their own under Performance
(`/databases/[id]/performance/statements/[key]`, `/tables/[name]`, `/sessions/[pid]`), on the
shared `DetailPage` (`components/detail-page.tsx`, framed by `ops/performance-detail.tsx`): the way
back, the previous and next arrows and the shortcuts' help on one line (the arrows hidden on a
phone), an identity line with one command and a `⋯` menu, the views on `ViewTabList` with the open
one in `?tab=`, and from `xl` the list it came from as a `Rail` with that list's own filter, order
and narrowing (`ops/performance-lists.ts` holds the keys both read), Alt+↑ and Alt+↓ walking it as
Workspace commands with the view kept. A statement
(`ops/performance-statement.tsx`) has Figures as `ReadingLine`s with the recorded window at each
line's end, Text, Plan and History; it is the one place the Statements view, Statement history and
the top statements on Logs open. A table (`ops/performance-table.tsx`) has its size as one strip,
its rows and how it is read, and its housekeeping and maintenance. A session
(`ops/performance-session.tsx`) reads the session list at the list's cadence, has its statement,
its timing and who it waits on or holds up, and its stop verbs; one that ends while open is said to
have ended and stays as last seen. The address is one path segment with no percent sign
(`ops/performance-address.ts`, unit-tested): what would be escaped is written with `~`, and a dot
inside a name is escaped so the dot left is the one between schema and name. A statement's key is
the engine's id with the row's `variant` after a `~` where the engine gives one
(`statementKeys` in `ops/performance-figures.ts`), so a page keeps its row whatever order a read
lists them in; the id alone (History's links) is that id's statement, its rows summed where it has
several. The addresses that
opened the sheets these were (`?statement=`, `?object=`, `?session=`) are sent on to the pages. No
list of Performance draws a bar per row: a share is a figure in its own column, and what a view's
footnote said is a fact on its title line (`ops/performance-words.tsx`);
`tests/browser/database-performance.spec.ts`.

Data, Query, Schema and Diagram are four workbenches under one head
(`components/database/workbench/chrome.tsx`) — with one rail kit (`workbench/rail.tsx`: `RailHead`,
`RailFind`, `RailGroupHead`, `RailRow`, `RailSkeleton`, `RailNoMatch`), one side column
(`WorkbenchSide`, 256px opening width on every workbench), one open-object strip (`ObjectHead`),
and one container picker and mark (`kit/container-picker.tsx`, `kit/marks.tsx`; a menu, and past a
dozen containers a combobox whose find box has the keyboard as it opens): the database's identity and the open thing's verbs in
the frame's own bar, the glyph-only controls as words in its one menu, and on a phone one pane with
a bar at the foot — Tables, Rows and Details on Data, where a row is a card and its full row the
Details pane (`data/row-cards.tsx`); Editor, Results and Saved on Query, where Run's menu holds the
row limit and the head's menu Format and Save. A schema's and a table's figures are one line of
facts, a table's Statistics are sentences, and the Diagram is zoomed and fitted from the maps' bar
at the foot of its canvas (no minimap). A table the Diagram's inspector reads moves by the four Move
buttons in its foot as well as by a drag or the arrows, for a pointer that cannot drag (`MoveBar`;
not on a phone, nor while the layout is locked).

Backups' dumps are one line each in a framed table (a card of two lines on a phone): when, the
file's name with its tag and note, what it holds, its size, and Download, Restore and Delete. The
name opens who took it, the tool that wrote it and how long that took. *Back up now* is the one
command on the title line; *Upload a dump* and *Copy database* are in the menu beside it. Where a
server's log is not archived, point-in-time recovery draws the path it would take
(`ops/pitr-picture.tsx`: the server, its write-ahead log, a directory, S3 or Backblaze B2) over one
sentence and *Set up point-in-time recovery*, or over the server's reason where it cannot be set
up. Import's plan names the source beside its logo with what it holds as one line, and its copy
draws the replication (`import/picture.tsx`, words in the unit-tested `import/model.ts`): the
source, the subscription and the database here, a line that moves while rows or changes are on
their way with how far behind the target is written on it. Generate draws each generator as the
project it writes for (`generate/options.ts` `logoKeys`; its language's mark where the project has
none), with its language as a tag only where the language is not the name; before anything is
written its place is that mark, "Nothing written yet" and the one Generate.
Search says a limit only when one was hit, as the tail of its summary sentence, and each table's
*Every match* is a small verb on its title line.

**Implementation:** `components/database/`; backend in
[`../backend/databases.md`](../../backend/databases.md) Adding a database can limit its container from
the start.
