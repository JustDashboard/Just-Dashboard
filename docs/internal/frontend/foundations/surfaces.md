# Surfaces

## Reference

**The frame.** The window is `--ground`: the rail, the phone's strip and, from `md` up, an 8px
margin round the page. The page is a sheet on it, `--canvas` (`--background`), with its own edge
(`--line`) and `rounded-xl` corners, so the work is always the lighter, framed thing and the
navigation steps back (Just Hub's frame, 0.9.1). On a phone the sheet is the screen.

**Lines.** `--line-subtle`, `--line` and `--line-strong` are white at 6, 9 and 16%, so one edge reads
the same on every ground. `--border` and `--input` are `--line`; `--hairline` is `--line-subtle`.

**Grounds.** A surface steps up the ladder `--background` → `--card` → `--control`. A header strip
is `--surface-header`, a well `--surface-sunken`. A hairline (`--hairline`) is where a header meets
a body or two things meet.

**Which blocks are framed.** A table, a `Pane`, a `Well`, a `Group`, and the danger zone. Everything
else on a page is plain: a section is a title and a hairline (D6). A framed block that is none of
these carries a comment saying why.

**Elevation.** Shadows are for what floats, and nothing else.

| Role | Utility | For |
| --- | --- | --- |
| none | — | every control and every block on the page |
| flow focus | `shadow-sm` | register B's one focused surface |
| float | `shadow-md` | menu, select, popover, hover card, chart tooltip |
| dock | `shadow-lg` | the floating save bar, a dock, a floating toolbar, a sheet |
| modal | `shadow-xl` | a dialog |

**Radius.** `--radius` is 8px. Nesting runs outer to inner.

| Role | Utility | px | For |
| --- | --- | --- | --- |
| swatch | `rounded-xs` | 2 | a legend key, a bar cap, a tooltip arrow |
| mark | `rounded-sm` | 4 | a mono `Tag` ground, a menu row, a checkbox, inline code |
| control | `rounded-md` | 6 | `Button`, `Input`, `Select`, `Toggle`, a tab trigger, a skeleton line |
| fence, well, floating surface | `rounded-lg` | 8 | a well, a code block, dialog, popover, menu, hover card, tooltip |
| block | `rounded-xl` | 12 | `Panel`, `Pane`, a `ChoiceRow` card, a workbench frame, the save bar, a dock |
| tile mark | `rounded-tile-md`, `rounded-tile-lg` | 10, 14 | `WireMark` tiles |
| round | `rounded-full` | full | a status dot, an avatar, a switch, a progress track |

| Rule | Held by |
| --- | --- |
| Nothing lifts: no gradient, no glow, no drop shadow on a block or a control. A control's face carries one lit pixel along its top (`--edge`), and nothing else. | convention |
| A bare `rounded` is `rounded-sm` spelled twice; use the named step. | lint `radius-bare` |
| Only the steps above. | lint `radius-scale`; coming: `radius-arbitrary` (WP-39) |
| Press feedback is colour (`active:bg-control-active`), never movement or a shadow. | convention |

## 2. Nothing lifts

**A surface separates by what it is made of, not by pretending to float.**

Cards, buttons, tabs, tiles and the sign-in panel used to carry `raised`: a gradient gloss, a light
hairline on the top edge, a dark lip below, a drop shadow, and an inversion on `:active`. It is
removed. Forty-nine surfaces all claiming to stand in front of the page is a page with no foreground,
and the treatment spent five CSS properties saying what a one-pixel border already says.

**A control's face sits a hair above the page** (0.9.1). A button, a toggle and a chosen tab carry
one pixel of white along their top edge (`--edge`, white at 6%) and no border round them, as light
catches the top of a key: `border-t-edge` on a face with a border box (the side and foot borders are
transparent, so the control is the size it was), `shadow-edge` on one without, and the brighter
`shadow-edge-brand` and `shadow-edge-danger` on the coloured faces. It is an inset, not a shadow: it
casts nothing and moves nothing, and a press is still colour. Just Hub's search field is where it
came from. Keycaps go the other way, set a step into whatever holds them (`bg-ground/50`, a `--line`
edge).

What separates a surface now:

- a step of ground — `--background` → `--card` → `--control`;
- a border, and a `--hairline` where a header meets a body;
- `shadow-*` **only** for the three things that genuinely float above the page: popover, dropdown,
  dialog.

**And not every block is a surface.** The 0.6.7 pass found the other failure: a page on which every
block *was* framed read as a page of containers, and the frames stopped separating anything because
there was nothing unframed left to separate from. The ground went darker (`--background` 0.145,
`--card` 0.168) so the one step still reads, and the default flipped: **a block on a page is plain
unless it can say why it needs an edge.** The host Overview ended 0.6.7 with no framed block at all
above its Services row and none in it — see §15 for what that took. The edges it has gained since,
in 0.7.1, are the Deployments section's project cards, and they are the lit edge of things you take
(§16) rather than frames: each card opens its project. Three kinds of thing stopped
taking a frame:

- a run of figures — `StatGrid` draws hairlines *between* tiles and nothing around them (`framed`
  restores the box for the one case a run sits inside another surface). Every tile keeps the same
  inset, the first in a row included: the first column used to drop its left padding to line its
  name up with the title, and on a `StatLink` that put the hover wash flush against the name, one
  tile of the row drawn tighter than the rest. A tile left alone on the last row takes the whole row
  rather than sit beside a hole — five two-up is two, two and one. The Overview's Services row was one
  of these until 0.8.7:
  a module's headline figure is a reading, and eight framed cards under a page that had just stopped
  drawing boxes were eight boxes. They are rows you open now, a sentence each (§15, §16);
- a list that is the whole of a section — `Panel plain` keeps the panel's anatomy (header, toolbar,
  body, footer) and drops the border and ground, so a title and a hairline mark the block. Recent
  activity on the Overview, every chart, list and hardware reading on the
  metrics page, every block on the Docker pages (the
  overview's band, engine picture, compose projects, cleanup and disk; the containers, stacks and
  events lists with their toolbars; the Networks page's band; the Images page's band of what
  Docker holds on disk and what the registries say, over its framed table; the Volumes page's
  band of who holds the data and where each volume stands, over its framed table; the attention
  and storage blocks on a container's page), the
  Security section's exposure facts, findings, probe forms and the Auth log, Firewall
  log and Activity sections — each a title over the log's `Pane` (its tables, dashboard access
  picture and Tools workbench retain frames), every block on the proxy pages (the overview's engine
  facts, route picture, traffic, certificate and error band, attention list, sites and Engine log;
  the sites, certificates and
  streams inventories and the ports list with their toolbars; the Renewal view's sections on
  Certificates and a certificate's own page; a site's own page, its route picture on the page's
  ground and its logs one `Pane` under it; the TLS report's findings, protocol, certificate, chain, HTTP and
  preload rows, and its deep scan's findings, suite list with its chip filters, key exchange and
  connection rows; the password files and DNS provider lists),
  the runtime-health bar, and every block of the deployment section — the fleet
  and its archive, Credentials, Notifications, Secret stores and Log drains, a project's Overview, Deployments, Logs, Runtime
  and Console, the run page, the nine settings pages and the create flow — are plain, with every
  row in them that is *taken* rather than read (a project, a run, a credential, a channel, a drain, a
  secret store, a service, a webhook, a schedule, a variable, a linked database) a lit card — a `ChoiceRow` in a
  `ChoiceList`, or on the fleet's grid a project's own `SpotlightBorder` card with the same lit
  edge (what kept a frame there was, first, a *picture*, on the argument that a picture needs an
  edge to read as one thing — the settings pages' four were framed until 0.7.1 and now stand
  unframed on the page's own ground, as below, each inside its section and drawn through
  `settings/setting-picture.tsx` or, for Automation's, `settings/automation/wiring.tsx`: General's
  push to a deployment, Runtime's where it
  listens (the domain, this server, the container, with an amber *Anywhere* node when the port is
  open on every interface), Databases' way the application reaches its databases (each engine, the
  managed network, the application) and Automation's what deploys it (the watched branch, each
  webhook and each schedule, the project, then production and the pull-request previews). Each
  draws its lines with `ui/animated-beam` from the marks' own positions, and the line is the state:
  dashed before the thing exists or where a hop is missing, still while it is paused, manual or
  only a draft, amber where it works but should not be relied on (a branch that cannot be read, a
  port that bypasses the proxy), red where a link has broken, a brand-to-signal pulse travelling
  along it while it carries. Every picture draws this server as one mark (`WireHost`): the server
  rack (`Servers`) in the brand's blue on `bg-wash-brand` inside a ring of `--rule-brand`, at the
  size of the marks beside it. It was the J until 0.7.1, at the operator's request — the J is the
  dashboard, a product on the machine, where every other mark in a picture is the thing itself —
  and before that the filled brand face, which is a command's (§3); the tint and the ring say it
  is a location, where the reader is standing. The other marks are one tile, too: every
  `WireMark` stands on the same rounded square whatever its tone, a thing's own glyph on the
  `logo` tone, and the only circle left in a picture is the dashed ring of a thing not added yet
  (`WirePlaceholder`). The Databases map, Configuration's request path, a site's route map and the
  rollback dialog's picture took the page's own ground with the rest in 0.7.1, the map giving up
  the frame it kept as the last picture with one. Until 0.7.5 every picture stood over a dot grid
  that faded out towards its edges (`wire-grid`); the Databases pictures gave it up first and then,
  at the operator's request, every other picture did, and the class is gone: a picture stands on
  the page's ground alone. The rollback dialog holds a
  small one of its own — your domains wired
  to the live release by a still line and to the release you are going back to by a dotted one,
  which is the line that carries once Roll back is pressed. The same vocabulary
  (`components/wire/wire.tsx`) draws one more picture that sits *unframed* because it is inside a
  block that already has its edge: the way a request reaches a project on the overview — source,
  live release, runtime, domains, each drawn as its product — in the column beside the preview; each
  mark paints the ground under its tint so the line never shows through it. And two that are
  unframed because each is its page's opening rather than a block on it: where an outcome goes on
  Notifications — the projects on the left, this server in the middle, a mark per channel on the
  right, dashed rings for the first kinds not yet added, until the column holds five — and the
  GitHub App on Credentials — the
  accounts that installed it, the App and this server, with its setup path under it — stand on the
  page's own ground, as does the map a project's Runtime opens on (its domains,
  the containers answering them, and the volumes, folders and databases those keep, in three lanes
  with `shape="s"` wires, `deploy/runtime-map.tsx`). General's automatic deployment (the
  repository, the watch with when it last looked, the deploys, and the last check's decision on
  one line under them) took the same ground in 0.7.1 at the operator's request: its frame was the
  one box on a page of hairlines, and the decision's sentence had sat in the section head's status
  slot at the page's 16px, louder than the head itself. Runtime's, Databases' and Automation's
  followed with the rest of the settings pages, through `SettingPicture`; Security's pictures — the
  overview's ways onto the machine (`security/perimeter.tsx`), the firewall's inbound path
  (`network/firewall-picture.tsx`) and sshd's doors (`security/ssh-picture.tsx`) — stand on the
  same ground since 0.7.1, the overview's having lost the frame it kept as a block among framed
  readings. The
  preview beside it is the
  Overview's one framed block, a tile that *is* the website. Past the pictures, the build console
  and the two shells, Docker's and a game server's, are `Pane`s, the run page's Details is one frame
  around a rail of the run's steps and an inspector of the picked one (the Network Tools page's shape, the
  rail deciding what the inspector shows), and a game's raw settings file is
  a `Well`, for §7's reasons; and the Danger zone is one `border-rule-danger` panel, because
  everything inside it changes what the deployment is, so one red edge says "careful" once where
  four red cards said it four times. The release path on a deployment page is not a wiring
  picture but a timeline: one bar in seven segments, each as long as its stage took,
  `components/deploy/run-pipeline.tsx`), and so are the
  Databases section's reading pages — the control center's identity line, its lines of what
  protects the databases, fleet, picture and found servers, a database's home, its Performance, Advisor, Search
  and Generate — each a title, a toolbar
  and a hairline, with every database, found server, generator and search hit on them a lit card,
  and every block on the four Processes pages — the live table under its band of workloads, the
  PM2 applications under their band of what PM2 takes of the machine, the systemd units
  under their band of busy services and recent changes, and the cron jobs, timers and
  system cron files on Scheduled, each a title, a toolbar and a hairline, Scheduled's
  under its next day drawn as one plain band, an hour chart over the schedules in the order they
  run (what fires next counting down
  at the end of its identity line), whose Cron log is a plain panel holding the log's
  `Pane` and whose jobs and timers open sheets of their own — with a detail sheet built
  from plain panels that opens on the thing's own mark, the unit's journal, a timer's runs and a
  job's cron lines a `Pane` in their sheets, a PM2 application's logs a `Pane` on its page — and the
  two System pages follow the same shape: on System users the identity line, what to change and the
  ways to root as one picture over the accounts as one-line cards that open the account, each
  account's page three views of plain lists of rows and one settings column, and on the audit
  log the identity line and the day as one strip over its table and filters, where a request's outcome is a
  `Status` dot and the code in its family's colour rather than a wash across the row; and
  both of the dashboard's own pages — on Version the identity line, the update in flight and the
  history (a timeline: the version in a sticky column, a rail with a mark per release, the notes at
  a readable measure), and on Configuration the identity line with Restart and Rebuild behind its
  `⋯`, the request's path, the restart record, and the settings as one centred column of
  `SettingSection`s, each head over its fields — the framed things on those two pages are the
  transcript console, which is a `Pane`, and the cards you pick; the Backups page —
  a picture of where the server's data goes, on the page's own ground (what it
  has by kind, wired through this server to every directory and bucket a job writes to), an
  attention list of the jobs that failed or went quiet, the jobs as destination cards (a
  `ChoiceList`, each drawn as the products it covers, with its destination's mark and its last
  fourteen runs as a strip) and the coverage as lit cards under its filter chips and a meter of how
  much is covered — every thing on it is taken, opening the job that covers it or the form written
  for it — with a job's own page opening on an identity line and its own picture over its
  readings, and its runs one frame around a rail of the runs and an inspector of the picked one,
  the run page's Details shape; the six account pages — no tiles on any of them: the profile's identity line over everything
  signed in as the account wired into its face and the roles as a ladder, Sessions' identity line over
  the path each session takes and rows that carry its life on a shared axis, the keys wired through
  their roles into the API over rows under chips, the users' identity line and band over cards in a
  `ChoiceList` under chips, Security's picture of what a sign-in passes over `FormSection aside`s two
  to a row, and Workspace as the first-run purposes'
  lit cards, the experimental one carrying its amber tag, beside the rail itself, the one frame, in the
  sidebar's ground with a switch on each row it may hide; and the four views on
  Packages — the installed and updates tables, the software search and the package Log (its `Pane`
  on the page's ground, no panel around it), under one underlined strip
  (`tabClasses`) rather than a filled tab list, beneath the host's identity and the band of software
  size and updates waiting — each a toolbar and a hairline over a framed table or, for the
  catalogue, lit choices on the page's own edge. Security updates carry their command at the end of
  the Updates waiting block's strip;
  a reboot owed and a stale index remain notices, and the Git page's repository list under its four readings (its workspace is one framed
  workbench of three `Pane flush` columns with a strip across the top, the way the terminal page is
  drawn);
  the Databases workbenches — Data, Query, Schema, Diagram and Logs on a SQL engine, Keys and Console
  on Redis, Documents, Aggregations and Schema on MongoDB — are each one frame, a pane's
  (`rounded-xl border bg-card`) drawn once around the whole of it: a working region sized to the
  window, a rail or an inspector beside a grid, an editor or a canvas, with a hairline between the
  columns and no gutter. With nothing chosen a workbench is not blank: its pane opens on the figures
  of what it would show (a schema's tables, rows and bytes; a Redis database's keys by type; a
  MongoDB database's collections), which is §15's pass 2 taken inside the frame. A table
  inside a plain panel bleeds by its cells' own padding (`-mx-4` around the `Table`) so its first
  column starts where the title does; a row laid out by hand takes `ROW_BLEED` from `row-list.tsx`,
  which is the same three classes `Row` applies to itself;
- a panel's header — it is no longer a tinted strip. The title sits on the panel's own ground with a
  hairline under it. `--surface-header` survives at a fainter mix for the two places a strip is still
  chrome: a `Pane`'s header and footer. On a plain panel that hairline keeps a step under it even when
  the body is `flush` (12px above, 8px below): it is the only thing marking where the section begins,
  and with nothing below it the first surface the body draws — a row's hover wash, a table header —
  butted into it and the rule read as an edge of that surface rather than as the line under the title.

**A database's workbench has one head, and it is the frame's.** Data, Query, Schema and the Diagram
stood under the database's strip and each opened on a toolbar of its own: two bars before the work,
with unnamed glyphs among the commands. They now share one head inside the frame
(`components/database/workbench/chrome.tsx`, `WorkbenchFrame`), the Git workspace's bar for a
database: the way back to its Home, the engine's mark, the name as the switcher over one micro line
(the environment and *protected* first, then engine, address and database, which give way before
they do), whether it answers, then the page's verbs, Connect and one `⋯`. The shell draws no strip
over these four (`shell/database-shell.tsx`, where the engine has the page). A part of the page hands
its verbs up rather than the page being rebuilt around the head: `HeadVerbs` draws its children in
the head's verb slot and `useHeadMenu` puts verbs in the menu, under the page's name and above the
database's own. The rules it keeps: **one** brand-faced command on the page (Insert row, Add label
or Edit in the head; MongoDB's Insert is outlined there because Find, in its query bar, is that
page's; on Query it is Run, which stays between the statement and its result with Explain beside
it, the row limit inside Run's menu and Format and Save in the head's); every
icon-only control of a workbench (read again, show or hide a side column, lay out the other way,
clear the transcript) is a word in the menu; an object's own verbs (a table's rename and drop) stay
beside the object's name, not among the database's. What a schema or a table amounts to is one
`FactsLine` under its name ("~9,000 rows · 3.2 MB · owned by app", `workbench/table-facts.ts`),
never a strip of captioned figures, and a table's Statistics are sentences
(`schema/stat-sentences.ts`). The Diagram's canvas is the maps': a zoom bar at its foot (out, the
size as a figure, in, Fit, tidy), the inspector's toggle and full screen floating at its top right,
the keys as caps at the foot of the inspector, and no minimap. Below the width where a side column
lies over the work each is one pane with a `PaneBar` (`patterns/keyboard-and-place.md`).

**A picture of things wired through one hub is `WireFlow`.** Most of the pictures above have one
shape — a column of things on the left, this server (or an engine, a tunnel, a resolver) in the middle,
a column on the right, a line from each thing to the hub — and until 0.8.7 each drew it by hand on
`components/wire/wire`, 250 to 500 lines apiece. `components/wire/wire-flow.tsx` draws it from data: each node is a
mark, an eyebrow, a name and a hint, optionally a link or a button; its line is a `WireLine` whose state
is the vocabulary above (`up` and `moving` or still, `down` dashed, `warning` amber, `danger` red, `idle`
dashed for configured but not in use); a column has a title (and a count beside it where it is part of
something larger), a `max` past which the rest are one "N more" tile, and what it says when it is empty;
pointing at a node steps the rest back, keeping what `related` says travels with it. `loading` draws the
final shape — the hub and grey rows where the nodes will go — so nothing under it moves when the data
lands. On a phone it does one of the two things the pictures did: `stack`, the columns under their
headings with every word and no lines, for a picture that is the only place its nodes are named (the
Network overview, the gateway); or `marks`, a row of marks, the hub with its words under it, and a row of
marks with the lines running down from under the words (a column with `href` keeps its heading as a
link), for a picture a list under it names again (Notifications, Backups,
the proxy's alerts). The Network overview, the gateway and the firewall's inbound path draw through it
since 0.8.7; a new picture of
this shape uses it, and lays its nodes by hand only when it is not this shape (the routing decision's
two columns, a project's Runtime lanes).

**A picture whose nodes are not three lines says so, and still waits on `loading`.** By default
`loading` draws rows of a grey tile beside an eyebrow, a name and a hint, each bar on its line's own
height. That is the loaded height of a three-line node and not of any other: Socket history's nodes are a
name and one line, an approved service on External checks carries two lines of hint, a place on
Connections has a sparkline's box under its line, and down a phone (`marks`) the hub waited on its name
alone, so the rows of marks stood a line or two higher until the read landed. Five Network pages drew
grey nodes of their own for that. A column now gives its waiting rows their shape with `loadingLines`
(`WireFlowWaiting`, one for every row or a function of the row's place): the `mark` and the `eyebrow`
and `title` words where they are known before the read (this server's mark, "Tier 1", the three things
the recorder keeps), `eyebrow: false` for a node that has none, `hint` as a count of lines (or
`{ wide, narrow }` where a phone says a line more), and a `foot` for what stands under the lines at one
size. The hub takes the same `loadingLines` for its caption, and with it a phone's rows of marks draw
those lines too. With neither given a picture waits exactly as it did. A page never draws grey nodes
of its own; it says how many rows (`loadingRows`, remembered from the last visit where the count is the
server's) and what a row holds (a volume's and an image's pictures, the last two that drew their own,
wait this way too). Wide, the hub's words hang under its mark out of the flow, so the picture
measures what of them reaches past its own box and holds that room open under itself (`useHubRoom`):
a page does not pad its hub for them, and a picture that already had the room is what it was.
`LinePlaceholder` (`components/network/identity-facts.tsx`) is still the
grey bar for a line of words outside a picture, a grey bar after a real space so the line box is the
one the words will have, and the same file holds `IdentityFacts`, which keeps two lines' room for an
identity line's facts on a phone, where four facts wrap and a single bar in their place did not. Those
pictures are `marks` on a phone, since the lists under them name each node again, which is also what
stops a stacked column growing with every source.

**A section page beside Notifications reads as Notifications does.** Log drains, Secret stores, Shared
variables and Templates each open on a `WireFlow` picture (`marks` on a phone, since the list under it
names every node again) and then draw one-line `ChoiceRow`s, or the picker's cards for templates. A row's
state is a dot and a word in a column of fixed width, with one figure or count after it, and the count of
the whole list, with what needs a look, is on the section's head. What used to be a second and third line
under a row (a destination's refusal in its own words, the variables that read a store) opens from that
word or that count in a popover, so every row is one height in every state; a press inside the popover
stops there, because the row around it opens the editor. A project in these pictures is
`projectNode` from `deploy/section-picture.tsx`. Before the first item there is a picture only where
something real is already wired (a drain's projects), never two empty rings.

**And one kind of block came back to the frame: a table.** The 2026-09-21 pass found what sweeping
tables into the second bullet had cost. A table's container is `overflow-auto`, so at any width where
its columns stop fitting the grid is *already* clipped — and with nothing drawn at the boundary, a row
whose actions sit past the edge reads as a row that has no actions. The proxy Sites table shipped
exactly that: the last site's verbs were beyond an edge nothing marked, on a page that looked
finished. §7 frames a `Pane` for this very property, and the only thing separating the two is that a
`Pane` is a region of a workspace while this one sits in a page's flow.

So **a panel whose body is a table is framed, and every other block on the page stays plain.** That
is not a retreat from this section's argument — it is the argument: the frame separates *because* the
readings, findings and lists around it have none. Security's Logins is the shape to check a page
against — since 0.8.7 one framed table, the login record, under a picture, a run of cards and a
ranked list that have no frame, where it was three framed grids under an unframed `StatGrid`; the
page reads as one table rather than as a stack of containers. A page on which the table is the
only block is the easy case;
a page on which everything is a table has a different problem than this rule.

The flip is one word at the call site plus one on the bleed. A table inside a *plain* panel is bled
out so its cells' own padding lines the first column up with the title; measured from a framed
panel's origin that same bleed put the grid fifteen pixels through its own border, which is what
framing one of these looked like at first. The bleed is written three ways across the product —
`-mx-4` on a wrapper, `-mx-5` on the body, and nothing at all — so rather than compensate for it
centrally, each one now carries the `group-data-[plain]/panel:` variant and simply does not apply
once the panel has an edge; the outer-column padding rule that was written for the plain case is
what then lands the column on the panel's gutter. The same pass
fixed the neighbouring defect: `.scroll-affordance` painted its cover gradient in `--card`
unconditionally, so every table on a plain panel wore a faint lighter smear down both edges — the
same mistake §8 records for the sticky header, missed here because the gradient *named* the colour
instead of inheriting `--panel-ground`.

A table inside a section that also holds its toolbar and states (Performance's views) stays on the
section's ground and fits its column: `FIT_COLUMN` (`database/ops/performance-parts.tsx`) zeroes
the outer cells' padding instead of bleeding the table out, so no row hairline runs past the
title's.

A panel that is also a destination takes `interactive`: its border steps up to `--border-strong`
under the pointer, and nothing else moves.

Press feedback is colour. A control with a face takes its own `active:` step — `active:bg-control-active`
for the neutral faces, `active:bg-brand-active` for the brand command; a ghost or link button has no
face to move and borrows the accent wash. Nothing translates, and nothing casts a shadow to say it was
pressed.

## 9. Radius

`rounded-sm` (4) for a mark, `rounded-md` (6) for a control, `rounded-lg` (8) for a fence or a well,
`rounded-xl` (12) for a block. Nesting runs outer → inner in that order. Bare `rounded` and the steps
outside this ladder are lint errors.

Account avatars use `rounded-full` at every size (§4); their silhouette identifies a person rather
than a control or a surface. `InitialsMark` and forge faces retain their compact mark radii.
