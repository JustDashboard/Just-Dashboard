# Frontend shell and design system

The App Router currently has 91 `page.tsx` entry points, including nested database, Docker, proxy,
security, and deployment workflows plus `/login`. Most page modules are client components; the three
deployment detail/new wrappers remain server components and hand interaction to client components under
`components/deploy/`.

## The shell

`(dashboard)/layout.tsx` owns `CommandPaletteProvider`, `SelfUpdateProvider`, `NavScopeProvider`,
`SidebarProvider` + `AppSidebar`, `MetricsStream` — which renders nothing and exists to hold the
metrics socket open for the whole shell, so Overview's charts keep filling from other pages — and
`SavedFolderColours` around the page, which reads the folder labels from `/files/places` and again on
each navigation, so a folder coloured in Files is that colour in every tree and browser. Its
redirect to `/login` is convenience, not a control; every API call behind it is authenticated server-side.

**The scroll container is on the `SidebarInset`, not the document.** That is what lets a page ask for
the remaining height (`<Page fill>`) instead of growing past the viewport.

**A table comes fully on screen before its rows scroll.** A capped table (`max-h-[calc(100svh-22rem)]`
and the like) is sized to fit under the page's header, so with the page anywhere but its top the
wheel would scroll a grid half below the fold. Every `ui/table.tsx` container listens through
`hooks/use-reveal-on-wheel.ts`: when the wheel would scroll its rows while the shell's scroll (or a
dialog body's) clips part of it, the page first moves the smallest distance that shows the whole
container — `lib/scroll-reveal.ts`, 12px clear of the edge, its top when it cannot fit — and holds the
wheel while it glides, so the rows stay still under the reader. A wheel past the table's end already
scrolls the page and is left alone, as are a table with nothing to scroll, sideways and Ctrl-zoom
wheels; reduced motion jumps rather than glides. The database `DataGrid` fills its frame and has no
page around it to move.

The navigation sidebar is always expanded on desktop: the shell disables desktop collapse in
`SidebarProvider` and does not read the old `shell.sidebar` preference. Desktop has no toggle
and Ctrl/Cmd+B does not collapse it. Below `md`, the header opens the mobile sheet and the
sidebar header closes it; Ctrl/Cmd+B still toggles that sheet.

### The rail drills in

**The sidebar shows one list at a time: the list for where you are.** Opening Docker replaces the list
of everything with the list of Docker's seven pages, under Docker's own name and a control back out.
Until 0.6.7 the rail was one list whose section rows unfolded a second list beneath them — Docker and
Security open together was thirty rows deep — and because that rail could not be relied on to show a
section's pages, six section layouts also carried a strip of route tabs across the top of every page in
them. **Those strips are gone. There is no route-level tab anywhere in the product**; a switcher that
remains switches between views of one page, never between pages (see `components/tabs.tsx` below).

Which panel is showing is derived from the route, not remembered, so a pasted link opens with the rail
already inside the right section. The single piece of state is a look elsewhere without leaving the
page you are on — the step back out, or a group opened from the list — and it is dropped on the next
navigation. Three levels exist: the top-level list, a section, and one deployment inside Deployments
or one database inside Databases — or, inside a group, the group, a section in it, and that section's
pages (Monitoring → Processes → PM2; Server configuration → Proxy & TLS → Sites).

`components/nav.ts` is the nav registry — `NAV`, `PERSONAL_NAV`, `ACCOUNT_SECTION`, `PROJECT_NAV`,
`PROJECT_SETTINGS_NAV`, `navMatches`, `navOwns`, `sectionsFor`, `navLocation` — so `app-sidebar.tsx` and
`command-palette.tsx` walk the same lists without a second copy, and neither imports the other. Items
may carry a `capability`, and every reader hides what the role cannot use. ⌘K covers every page and is
the one surface that still sees them all at once.

A section's `children` are its pages **including its own landing page, first and named for what it
shows** — "Browse", "Live", "Version", otherwise "Overview" — so every destination in a panel is a leaf
and "where am I" points at exactly one row. The palette skips the child whose href is the section's own,
which is how one array serves both.

A **group** (`NavGroup`) is a row with a chevron and no page: Monitoring holds Metrics, Processes and
Logs, and Server configuration holds Proxy & TLS, Packages, System users and Audit log. There is nothing
that is "Monitoring" as such, so pressing the row opens its panel over the page you are on rather than
navigating, and a group owns a path only through its entries (`navOwns`) — Metrics, Processes and Logs
share no prefix. `sectionsFor` returns the chain of groups and sections a path sits inside, and the rail
draws one panel per link of it; the breadcrumb names every link (`navLocation().parents`).

`components/nav-scope.tsx` is for the two panels the route cannot describe. A section calls
`useNavScope(...)` and the rail draws what it publishes: one database's layout
(`app/(dashboard)/databases/[id]/layout.tsx`, through `database/shell/nav-scope.tsx`), because the
connection's name and which pages it has depend on its engine — the engine registry
(`database/engine.ts`) says Redis has Keys and a Console where a SQL server has Data, Query, Schema and
a Diagram — and its rows carry the reader's place (`?schema=&table=`) from page to page; and
`deploy/project-shell.tsx`, because a project's name and its mark (its favicon or product), its
game-only pages and which of its settings pages hold a saved change that is not live yet
(`PENDING_KIND_PAGE`) are not in the URL. Both sit a level *below* their section (`/databases/<id>`
under Databases, `/deploy/<id>` under Deployments), so the back control leads to the section. The rail
draws a project's rows from the route alone until that registration lands, so the panel does not reflow —
only the name fills in. It draws a database's from the route and what the tab remembers of it
(`useSessionState("databases.known")`: by id, the name from the last connection list and — once its pages
have been open — the flavour and capabilities its summary answered with), so a database opened before in
this tab is its own panel at once, with exactly the pages that then register. One the tab has not opened,
or an id the list never held, gets none until its layout registers one: a saved row knows only its
driver, and a rail of MySQL's pages for a MariaDB server, or of any pages for a database that does not
exist, is worse than the section's panel standing a moment longer. The memory is the tab's and not the
browser's because connection names are the server's data and must not outlive a sign-out. The
alternative was the rail polling the driver catalogue and a project on every page in the
product to draw a list the page beneath it already holds. A project's run page keeps the project's
panel, with Deployments marked as where you are, because a run is opened from there.

**Which database is the path, and only the path.** The section's static children in `nav.ts` are the
three pages about every database at once — Control center (`/databases`), Map (`/databases/map`) and
Add a database (`/databases/new`, `system.admin`) — and one database's pages are
`/databases/<id>` and `/databases/<id>/<section>`, built by `sectionHref` in the engine registry and
never by hand. `database/shell/routes.ts` holds the one spelling of an id (`DATABASE_ID`, digits with
no leading zero), which the layout, the rail and the palette all read, so `/databases/007` is not
database 7 to one of them and an unknown address to another. An id no saved connection has is
"Database not found" with a way back, never some other database; any other path under a database is
"Page not found" inside its shell (`[id]/[...missing]/page.tsx`), rail and strip kept. A database's
panel has Home alone and then Work, Schema, Insights and Operate (`database/shell/nav-groups.ts`),
each holding only what the engine has, in the engine's words. The connection used to be a query
parameter (`/databases/browse?conn=3`), and those addresses are still out there — a board stores the
link of every database card drawn on it, and a bookmark outlives every release — so a segment that is
not a number is read by `legacyDatabasesHref` and replaced, not pushed: `overview` is the home,
`browse` Data, `structure` Schema, `find` Search, `monitor` Performance, `server` Access,
`connection` Settings, `diagram`, `query`, `advisor`, `backups`, `logs` and `generate` themselves,
with `schema`, `table`, `sql`, `source` and `view` carried over, and `/databases/topology` is the
map. An old page with no valid `conn` opens the control center rather than a guess.

A scope's head is not a section's. A section's name is one of this product's words and is drawn as an
eyebrow, the rail's label voice; a project or a connection is a name somebody typed, and small caps
turned `api-production` into API-PRODUCTION, so a scope's head (`named` in `app-sidebar.tsx`) is printed
as written at `text-title` semibold — a step above the rows, so the name reads as what the rows belong
to rather than as one of them — after the scope's `mark`, the thing drawn as itself at the rows' icon
size, a project's favicon or product (`ProjectMark size="xs"`), a connection's engine
(`ProductGlyph`). The mark rides along with the rest of the scope but is not what decides a republish,
so it is derived from the same data as the title. A scope carries no `icon`: a glyph in a
row's slot made the heading read as one more row to press.

The groups run in the order a day on the server runs, and the rail names each group:
**Server** (Overview, and Monitoring: Metrics, Processes, Logs), **Apps** (Deployments first,
then Databases, Docker), **Workspace** (Boards, Terminal, Files, Git), **Protection** (Security, Backups),
**Advanced** (Server configuration: Proxy & TLS, Packages, System users, Audit log) and **System**
(Settings). The top-level list is twelve rows rather than seventeen: the three monitoring pages answer
one question, and the four configuration pages are opened to change the server rather than to use it.
Deployments open the second group because shipping something is the reason most visits happen; until
0.6.7 it sat fourth in a group called Operations, between Packages and Backups.

`PERSONAL_NAV` is a flat list of leaves — Profile (`/account`), Security, Sessions, API keys and, for
`system.admin`, Users — drawn three times from the one array: `ACCOUNT_SECTION`, which is that list as a
panel the rail drills into once you are inside `/account`; the palette's Account group; and the menu on
the rail's foot. Its button is a neutral control with a circular picture, display name and role,
with no chevron. Above the card the menu is exactly the card's width (Radix's trigger width),
so it reads as the card opening rather than a panel overhanging the rail. It
opens with a small picture, display name, sign-in name and a role `Tag`, then the five pages
(Security carries a `Status` for two-factor) and a separate neutral Sign out row. It grows out of the
card on the product's ease, and its rows `rise` in an 18ms stagger starting from the row nearest the
card; the stagger is `motion-safe` because the reduced-motion rule collapses durations, not delays.
Menu rows are 32px on desktop and 44px on a phone, with the current page marked by `aria-current`, an
accent wash and a brand glyph. The picture is `components/account/user-avatar.tsx`: the stored image when
there is one, otherwise the display name's initials in a hue taken from the username (`lib/hue.ts`'s
`LANES`, so the same person keeps one colour in the rail, the users list and their profile). Pictures and
initials are circular at every account-avatar size and carry `data-slot="user-avatar"`, the design
system's identity exception to the pill ban. `InitialsMark` for a person drawn from a bare name
retains the compact mark radius used in commit lines and forge faces.

## Workspace commands and place

### Global command search

`command-palette.tsx` opens with Ctrl/Cmd+K, the rail's search field (drawn as a framed field with
the shortcut in keycaps for the reader's platform — ⌘K on Apple keyboards, Ctrl K elsewhere, via
`lib/platform.ts`), or the search button in the top strip a phone gets in place of the rail. An
empty search puts the
previous distinct destination first, then the other recent destinations, current page commands
and permission-visible navigation. Enter on that first result switches back to the previous
place without a browser history traversal; native Back/Forward remain available. The bounded
12-destination history observes pathname and query changes from every navigation entry point,
keeps resource selections, and uses `useMemoryState` per account. Filter and tab edits update the
current place without displacing the previous page; switching a query-selected resource creates
a distinct destination. Reload and sign-out discard it.

`components/command-search/inventory.ts` projects explicitly selected metadata from ten existing
authenticated GET routes: the deployment fleet, proxy vhosts, saved database connections, Docker
containers and stacks, Git repositories, systemd services, PM2 applications, backup jobs and boards.
The dialog mounts its index only while open, with at most four concurrent inventory requests and
an eight-second timeout per source. Closing aborts reads; reopening reads fresh inventory rather
than retaining deleted resources. Reads settle independently; a failed source contributes no
stale rows and is named as incomplete search with a retry control. Retry refreshes all inventories.
Backend route permissions remain authoritative. No aggregate backend route or database migration
is introduced. Database rows, arbitrary files, logs, credentials, environment values and board
scenes are outside this index; URL userinfo, queries and fragments are excluded from addresses.

`model.ts` supplies scope aliases, AND matching of query terms, accent/punctuation normalization,
ranking (exact name/address, prefix, substring, one-edit typo), result limits and recent-history
logic. `domain:`/`site:`, `db:`/`database:`, `container:`, `stack:`, `project:`, `repo:`, `service:`,
`pm2:`/`app:`, `backup:`, `board:`, `page:`, `command:` and `recent:` narrow the same input; a
strip of scopes under the input — words on the Files search's underlined strip (`tabClasses`), no
glyphs, `role="group"`, one tab stop with arrow keys between them — makes the types discoverable,
presses the scope a typed prefix names, and counts each kind's matches for the words typed. Global searches omit duplicate recent
entries; the Recent scope searches history explicitly. At most 60 results are shown with a
matching count and a suggestion to narrow when there are more. A narrowed search with no matches
offers to search everything for the same words. A section's landing page is listed once under the
section's name and also answers to the name the rail gives it inside the section (Deployments'
"Projects", Databases' "Control center", Processes' "Live").

Every result is one line — its mark, its name, its second line in the hint size after it, and at
the far end its state, when it was visited, *Previous*, *Current* or its shortcut — so a screen holds
twice the rows the two-line list did. The mark is drawn as what the result is
(`command-search/marks.tsx`). An inventory row carries the product it is as a `ProductLogo` key — a
container its image's, a database its flavour or driver, a site nginx or Caddy, a repository its forge
or Git, a service the unit's program, a PM2 app its interpreter, a stack Compose's, a project
`projectProduct`'s, a B2 backup Backblaze's — drawn bare (`ProductGlyph`), and the pages that are one
product's own (Docker, Stacks, Git, PM2, Terminal) carry theirs. Everything else is its glyph as the
rail draws it, grey at rest and ink on the selected row. There is no hue per kind: the rows used to
stand on tiles tinted in seven `--tag-*` lanes, with the same hue on the group heading and the scope
chip, and a column of faded squares said nothing the heading did not — the icon plate §14 removed
everywhere else. The colour in the list is now the products' own. A recent destination is drawn as
the rail entry it is under, or as the resource it was opened as, with how long ago it was visited. A
container, service or PM2 app shows its running state as a `Status`; the words a title matched are
drawn in `--signal`, the search-hit colour (`highlight` in `model.ts`); page commands show their
shortcut as keycaps for the reader's platform.

From `lg` the dialog widens and the selected result is said in full beside the list
(`command-search/preview.tsx`), following the selection as the arrows move it: its mark on the
product tile, its kind, its name and state, then what the row had no room for. An inventory row
projects a few `facts` from the same explicitly chosen fields as the rest of its metadata — a
container's image, status, health and Compose service; a site's server and every domain; a
connection's engine, host, database and whether it is protected; a repository's branch and upstream,
folder, last commit and working tree; a backup's destination, schedule and folders — and never a
payload's secrets, environment, notes, database users or remote addresses. Each also names its
`links`: the domains, container names, stacks and folders other resources can know it by. A resource
sharing one exactly is listed as *Connected* — the site serving a project's domain, the container a
connection's host names, the stack and repository in one folder and the backup covering it — and a
relation that would need a guess is not drawn. A page shows the rail list it sits in with itself lit; a command
says what it does; a recent destination opened from search keeps the readings of the resource it
was. The pages and resources listed there open on a click and are out of the tab order, because the
combobox owns the keyboard and each is a result the same words reach. The preview's body takes
`animate-rise` keyed by the result, and rows that arrive as the words change rise the same way
(`useArrivals`); the list and the preview hold one height while typing so the footer stays put. A
phone keeps the whole width for the list.

Each resource result uses its existing detail address. Sites open `/proxy/sites/<name>` for
inspection rather than the legacy `?site=` editing link. Containers and stacks open their dedicated
pages; Git, systemd and PM2 retain query-selection links, including PM2's daemon and numeric ID.
Names with the same spelling retain separate identities. The current database keeps the existing
engine-dependent pages and broken-connection restriction; saved databases also retain the
`open <name>` search phrase. Page commands, proxy commands and
account navigation retain capability filtering. Reload nginx and Sign out require an explicit
search or the Commands scope, so neither becomes the blank menu's default action.

cmdk keeps combobox focus, arrow movement, Home/End, wraparound and active-option announcements;
the selected identity survives independently arriving results. Its Ctrl+K vim binding is disabled
so the global shortcut also closes the menu. Escape first clears a non-empty search through
`PaletteModal.onEscapeKeyDown`, then dismisses the dialog and restores focus. The scope strip and
the retry control own their keys. `PaletteModal` hangs from a fixed line near the top of the window
rather than centring, so the input does not move as the results grow and shrink. The shortcut ignores composition, repeats and additional modifiers.
Ctrl/Cmd+K is reserved in window capture so Monaco's chord and the terminal do not consume the
same keystroke before the launcher opens.

Pure ranking/history and metadata-projection contracts are tested with Bun in
`components/command-search/`; `tests/browser/command-search.spec.ts` verifies keyboard navigation,
real detail rendering with API fixtures, live refresh, partial failures, delayed reads, permissions,
database engine navigation and mobile bounds. The design and recording are in the
[command-search audit](../../audits/2026-10-04-command-search/README.md).

`components/workspace/` registers the active page's Find, read refresh, shortcuts and contextual
actions in the existing command palette. The shell marks its scroll region, and participating pages
restore row/field focus and scroll after navigation. Editors, terminals and portalled controls keep
their own keys. `SidePanel` returns focus through a workspace when the sheet's trigger is gone.
See [`workspace-interactions.md`](workspace-interactions.md) for the page contracts and storage rules.

## The design system

A small set of files defines the visual language, and pages compose them rather than hand-rolling
layout. [`design-system.md`](design-system.md) states the rules in full; this is the map.

- `components/page.tsx` — `Page` (one measure, gutter and rhythm; `fill` for terminal and logs, whose
  content *is* the viewport), `PageContext` (an accessible page name, plus a linked parent and verbs
  on detail pages, with no visual page title; list commands live in their section or workbench),
  `PageState`, `Section`, `Toolbar`, `SearchInput`,
  `Metric`/`MetricStrip`, `DetailList`/`Detail`, `RowLink` (`SearchInput` is 40px and a line of its
  own below `sm`; `MetricStrip` is two-up on a phone — §8). **None of the heading primitives takes a
  `description`** — see rule 5 in [`design-system.md`](design-system.md).
- `components/panel.tsx` — `Panel`, `PanelHeader`, `PanelToolbar`, `PanelBody`, `PanelFooter`; `Pane`,
  `PaneHeader`, `PaneFooter`; `Well`. A **panel** is *the* content block: a framed surface with a header
  on its own ground and a hairline under it, so a toolbar or full-bleed table sits flush beneath
  without a second edge. `Panel plain` is the same anatomy with no frame, for a list that is the
  whole of a section — but **not for a table**, whose panel keeps its frame because the grid owns a
  scroll region and a boundary you cannot see is one that lies about where the data ends (§2);
  `interactive` is the hover a panel-as-link takes. A **pane** is the same frame,
  for a working region of the page rather than a block of content on it — a session rail, a file
  tree, a log console — and its strips keep a faint tint. Neither lifts; the distinction is semantic
  and shows in how the two are composed and in their header heights.
- `components/row-list.tsx` — `RowList` and `Row`: the list-of-rows you *read*. A leading mark, a
  title, an optional second line and whatever sits at the right edge; a row with `href` is a link
  with a revealed arrow, with `onClick` a button, with neither inert.
  Its counterpart is `ChoiceList`/`ChoiceRow` in `components/flow.tsx`, for a row you *take* — a
  project to enter, a site to open, a stack to look inside. Same anatomy, plus an arrow that is
  always drawn and a border that lights under the pointer. Which one a list gets is decided by
  whether its rows are destinations, not by which register the page is in: see §15 pass 3 and the
  last subsection of §16 in `design-system.md`.
- `components/modal.tsx` — `Modal` (the centred task surface) and `PaletteModal` (a search overlay whose
  input is its own header, hung near the top of the window rather than centred). `components/side-panel.tsx` — `SidePanel`, the right-hand detail surface.

  **Which detail views are sheets.** A sheet is for a *glance*: you open it with the list still
  showing, read it, and close it — the list is the context you are using. A detail that holds a
  **stream, a terminal, or an editor** is not that. You stay in it for minutes, the list behind it is
  dead weight, and a sheet's `sm:max-w-3xl` is about ninety columns of terminal. Those are their own
  destination with a breadcrumb back: `PageContext` with the parent as an `eyebrow` link and the
  verbs in `actions`, then the resource name and state among the page's facts — a `MetricStrip` on
  a container's page, and on a stack's the identity line (`HostIdentity`) with the verdict at its end. The run page (`deploy/run-page.tsx`) goes one step further:
  since 2026-10-05 it opens on the header its project's pages open on (`run-header.tsx`, the
  project's tile, the commit, the run's state and how long it took, one line of provenance),
  because a run is one of the project's Deployments, and that header is the first thing on the
  page — its verbs sit at its far end beside the run's state, and the way back is the rail's panel
  and the menu's Open project rather than an eyebrow. (From 2026-09-24 it opened on the
  `HostIdentity` line the host Overview opens on instead.) A container,
  a compose stack and a backup job went that way on 2026-09-21, and a proxy site
  (`/proxy/sites/<name>`) on 2026-09-27, since it now holds its logs — the way back is a "Sites" link
  beside its verbs, the form and the raw file staying sheets it opens; every other detail in the
  product is a `SidePanel` and should stay one. A sheet that shows a log — a unit's journal, a PM2
  application's output — draws the service logs in their `layout="sheet"` form (no readings, no value
  columns, no facts beside the name), because the unit or the process is still the glance and the log
  one tab of it.

  Their tabs stay **in** the page — they are views of one thing, which is what `tabClasses` is for.
  Do not reintroduce a route-level strip for them (see the `SectionNav` note below), and do not give
  one a `useNavScope`: the rail drills into a *section* with many pages, not into a leaf.

  A sheet's `onOpenChange` is the funnel for guarding unsaved work. Files keeps that quick-edit
  sheet and also offers `/files/editor`, a destination with a collapsible file tree.
  `files/editor-surface.tsx` keeps the text editor's controls identical in both surfaces.
  Opening the destination transfers text/image drafts in memory; file contents never become a browser
  storage preference. The destination guards file switches and links with ordinary confirmation,
  browser unload with `beforeunload`, and cancelable history traversals with the Navigation API.
  Older browsers without cancelable traversal retain the link and unload guards.
  `Modal` and `SidePanel` share one anatomy: title, tinted strip, a body that is the only
  part that scrolls, a footer strip. Both take `actions` in the title strip; `Modal`'s
  `size="full"` is the whole viewport with that anatomy intact, for the one task that is looking
  at a thing rather than filling in a form (the file viewer). Their `description` is rendered `sr-only` — Radix wants an
  accessible description and nothing is drawn. **Raw `Dialog`/`Sheet` are assembled only in those three
  components** — a page or a feature panel never opens one itself.
- `components/tabs.tsx` — the switchers that remain, all of which switch between *views of one page*:
  `tabClasses` (the underlined tab, for a log pane's Live, History and Insights and the page's own
  views beside them, the packages page's installed, updates, search and log, the deploy wizard's source
  kinds), `FilterChip`, `ChipCount` and
  `ChipStrip`, the run every set of chips sits in (sideways-scrolling on a phone, wrapping from `sm`).
  `SectionNav` and `TabLink` — the route-level strips — were deleted in 0.6.7 when the rail started
  drilling into sections; do not reintroduce a strip that changes the URL.
- `components/form.tsx` — what goes inside a task surface: `Field` (a label, a control, one line under
  it — a hint, or the error while there is one), `FieldRow`, `FormSection` (an eyebrow and a hairline
  opening part of a longer form), `FormSections` (a run of `FormSection aside`s, each head over its
  fields, held with the hairlines between them to the fields' 48rem),
  `FieldCheck` (one rule a value has to meet, lit as it is met — a run of them in an
  `aria-live="polite"` wrapper), `OptionList`/`OptionRow` (a switch with its sentence), `FormFacts`
  (what the form operates on, as data under the title), `Statement` (the SQL a schema-editing form is
  about to run, with a copy) and `FormNote`. The databases section's dialogs are built from these and
  nothing else, and the statement one shows is the server's own plan of the change (`?preview=1`),
  never a page's approximation of it.
- `components/stat-tile.tsx` — `StatTile` (a small name over a 24px figure, an optional meter and one
  hint) and `StatGrid`, which runs them across the page with a hairline between cells and no frame
  around them, every tile the same inset and a lone last tile taking its row. `framed` restores the
  box. `StatLink` wraps a
  tile that is also a destination — the Docker and proxy overviews, and the Services row on the host
  overview — with the revealed arrow that says so on touch; `StatButton` wraps one whose press
  narrows what is under it (a lens's readings wherever a page draws them, a site's request figures)
  with a revealed funnel and `aria-pressed`. `dense` sets the tiles two to a row on a phone.
- `components/outcome-strip.tsx` — `OutcomeStrip`, the last few attempts at something as a square per
  attempt in the colour of how it ended, oldest first: a backup job's runs, a project's runs, a
  channel's messages, a webhook's deliveries, a schedule's firings. One drawing, so a strip means the
  same thing on every page it appears on.
- `components/status-dot.tsx` — `Status`, the one live-state indicator: a coloured dot and a word.
  `components/tag.tsx` — `Tag`, small-caps text marking a *fixed property* of a row. No chip, no border.
  **There is no badge and no pill in this product**; `ui/badge.tsx` was deleted so the decision cannot
  come back by accident. A count is `.numeric` text, a state is a `Status`, a property is a `Tag`, and
  anything longer than three words is prose.
- `components/icon-action.tsx` — `IconAction` (an icon-only row control with a real tooltip label),
  `RowActions` (the cluster of them that appears on hover, stays on keyboard focus and on an open menu,
  and is always visible on touch) and `DimActions`, the same cluster for a row whose controls have a
  column to themselves: always drawn, dimmed until the pointer arrives.
- `components/finding-list.tsx` — `FindingList`, the one list of verdict findings in the product. A row
  is a severity dot, a title and a short right-hand qualifier; the measurement, the reasoning and any
  one-click remedy are behind the row rather than printed under it. Metrics' health, Security's posture
  and Docker's attention all render through it, because they are the same shape of fact —
  what was measured, what it means, what to do.

**Reach for `Panel`/`Pane`/`Page`, not raw `Card`**, and add a variant there rather than a one-off in a
feature page — before these existed, fourteen pages read as fourteen products. `components/state.tsx`
does the same for the non-happy paths (`Spinner`, `LoadingRows`, `LoadingPanel`, `EmptyState`,
`EmptyNote`, `ErrorState`, `Notice`); `LoadingPanel plain` is the same silhouette unframed, for a page
whose blocks arrive plain, and `EmptyState`'s `mark` draws what the list would hold (`ProductLogos`) in
place of the glyph. `min-w-0` on the frame and its children is load-bearing: a wide
table's intrinsic width would otherwise widen the flex column and take the whole shell sideways instead
of scrolling inside its panel.

**Nothing lifts.** Cards, buttons, tabs, tiles and the sign-in panel used to carry a `raised` utility —
a gradient gloss, a light hairline on the top edge, a dark lip below, a drop shadow, and an inversion on
`:active`. It is gone. Forty-nine surfaces all claiming to stand in front of the page is a page with no
foreground, and five CSS properties were saying what a one-pixel border already says.

A surface separates by a step of ground (`--background` → `--card` → `--control`), a border, and a
`--hairline` where a header meets a body. `shadow-*` is reserved for the three things that genuinely
float above the page: popover, dropdown, dialog. Press feedback is colour — `active:bg-control-active`
on a control with a face, the accent wash on a ghost — so nothing translates and nothing casts a shadow
to say it was pressed.

The nav's own treatment did not change with it, because it never used the lift: the current destination
takes the sidebar accent fill, a brand-blue icon and a `font-medium` label against the `font-normal` of
the rest. The rail is the one surface dense enough to need three weights, and the group labels above the
entries are the third. `SidebarMenu`/`SidebarMenuSub` ship at `gap-0.5`.

`components/logo.tsx` is the logo and nothing else — `LogoGlyph`, the J mark in `text-brand`, then
"Dashboard" in the text colour and the version as small muted text beside it. No tile, no strapline.
The mark *is* the "Just", so the word is not also set in type next to it; it carries that word in
`aria-label` instead, and the sidebar's home link still announces "Just Dashboard". The glyph is an
inline `<svg>` on `currentColor` — two straight-edged subpaths, no raster, no second colour — so it
tints with the palette and stays crisp in the sidebar and on a retina sign-in alike. This is the
only rendering of the product's name, so sidebar, sign-in and splash agree and a rename is one file.
`LogoMark` is the glyph alone.

The same two paths are the browser icons: `app/icon.svg` (and `favicon.ico` / `apple-icon.png`
rasterised from it at 16–180px) put the mark in the tab, on the dashboard's dark ground rather than
transparent because the pale mark would vanish on a light tab strip. `public/LOGO.svg` is the file
the mark ships as — the same two paths, already filled `#CAE9FF` — for anything outside the app that
needs it, and the source `logo.tsx` inlines.

**Selection is `bg-accent`, everywhere.** The active session in the terminal rail, a pressed
`ToggleGroupItem`, an applied `FilterChip`, a highlighted command row, a selected table row, the current
file in the tree and the current sidebar entry all take the same neutral wash. Neither the primary tint
nor the brand hue is ever the mark for "this one is chosen": the brand is the *command* face and *where
you are*, and a filter borrowing either reads as the page's main action.

`components/ui/*` originated as shadcn/ui (new-york, zinc), with its icons rewired to
the Heroicons vocabulary in `components/icons.tsx`. Compose these shared primitives in features;
product-wide control changes belong in the primitives. Select triggers compose `Button`, while
select, dropdown and context-menu rows share `ui/menu-styles.ts`. `ui/menu-item-text.tsx` keeps
option names and metadata in one text column. The shared select replaces native selectors for
search scope and database activity history. Every side-panel toggle — the mobile navigation sheet's trigger,
the terminal's rail and Files/Diff, the Files sidebar and details, the logs sources, the ER diagram's inspector — draws its panel's side and
state with `SidebarLeftOpen`/`Close` or `SidebarRightOpen`/`Close` (drawn inline in `icons.tsx`,
since Heroicons has no sidebar): a rounded window with a bar inset on the panel's side, solid while
the panel shows and an empty outline of the same bar while it is hidden. They are drawn one pixel
wide on a 16px grid and render at 16px, where every line lands on a pixel. They mean a panel toggle and nothing else, which is why the Files page's
Places menu is a map pin. `ui/context-menu.tsx` is the right-click menu, drawn from `ui/menu-styles.ts`
alongside the dropdown and select so all three read as one menu; a feature that needs both (the file listing) renders one verb list into whichever
opened. Feature pieces live in `components/<feature>/`: `database/`, `docker/`, `files/`, `git/`, `logs/`,
`metrics/`, `packages/`, `procs/`, `proxy/`, `security/`, `terminal/`, `update/`.

## The editor is served from here, not from a CDN

`components/code-editor.tsx` wraps Monaco, and the line that matters is
`loader.config({ paths: { vs: "/monaco/vs" } })`. `@monaco-editor/react` otherwise fetches the editor from
`cdn.jsdelivr.net` at runtime — third-party JavaScript in the same origin as a session that drives the
Docker socket and a root shell, and a permanent spinner for an operator whose workstation has no egress,
which is the workstation this is meant to be reached from. `scripts/sync-monaco.mjs` copies
`monaco-editor/min/vs` into `public/monaco/vs` from the Bun `dev`/`build` scripts and explicitly in the
Dockerfile. The copy is gitignored and excluded from eslint.

Files exposes Monaco's Find, Replace, Undo, Redo, Format and Commands actions, indentation, word wrap,
minimap and font size. Models use the file path, search results reveal their matching line, and
`bun.lock` is recognised as JSON. The image editor bundles ISC-licensed `react-image-crop` 11.1.2
for resizable, keyboard-accessible crop handles and aspect presets; rotate, flip, resize, adjustments,
undo/redo, zoom and export remain local canvas operations. No editor loads a runtime from a CDN.

### Bundled sanitizer advisory review (2026-09-15)

Monaco 0.56.0, the current stable release at review time, includes DOMPurify 3.4.8 inside its prebuilt
AMD bundle. Updating only the transitive package does not change the JavaScript served to browsers.
Four advisories remain in `bun audit`; an exploitable path was not established in this integration:

- [Persistent configuration pollution](https://github.com/cure53/DOMPurify/security/advisories/GHSA-cmwh-pvxp-8882)
  requires `setConfig()` and a hook mutating its allowed-attribute map. Monaco uses per-call configuration
  and does not write that map.
- [Custom-element hook bypass](https://github.com/advisories/GHSA-c2j3-45gr-mqc4) requires
  `CUSTOM_ELEMENT_HANDLING` and an `afterSanitizeElements` enforcement hook. Neither is configured.
- [Trusted Types policy persistence](https://github.com/advisories/GHSA-vxr8-fq34-vvx9) requires a custom
  policy followed by `clearConfig()`. Monaco requests trusted output, but supplies no custom policy and
  does not call `clearConfig()`.
- [Detached subtree with in-place sanitization](https://github.com/cure53/DOMPurify/security/advisories/GHSA-55q2-fjhq-7xh7)
  requires `IN_PLACE`. Monaco does not enable it.

The application wrapper exposes no sanitizer configuration to API or editor content. Database result
cells render as React text, and SQL schema completions supply string labels and insertion text, without
trusted Markdown or HTML. Preserve these boundaries when extending editor features. Upgrade Monaco when
its shipped bundle contains the patched sanitizer, then recheck the copied runtime and `bun audit`.

## Charts

`components/metrics/` is a third design-system file in all but name: every chart goes through it rather
than assembling its own recharts tree, which is how the old Overview page ended up with three tooltip
formats and no way to compare a moment across four charts. Adding a measurement should mean naming a
series.

- `metric-chart.tsx` — `MetricChart` + the `Series` descriptor. The x-axis is **numeric over time**, not a
  category axis of pre-formatted labels: a category axis spaces every bucket equally, which lies whenever
  the record has a hole in it. It owns the shared crosshair, drag-to-zoom, event markers, thresholds and
  the one tooltip listing every series at the hovered instant. Click or Enter/Space pins a shared
  instant; Left/Right and Home/End inspect samples, Escape releases it, and pinned overlays show
  each chart's values without pointer movement changing the instant. The Metrics page adds sample
  controls and a journal History link for the surrounding two-minute window.
- `chart-panel.tsx` — the header/chart/legend shape and the empty state. A series with no numbers anywhere
  in the window is **dropped rather than drawn flat at zero**, which is what makes a kernel without PSI
  say so instead of reporting three healthy zeroes.
- `series-legend.tsx` — min/mean/max/last, plus **"At cursor"**: while the pointer is over any chart, every
  legend shows the value its series held at that instant. Max reads the peak column where a series has
  one, since the maximum of the *means* is exactly what a downsampled window hides.
- `sparkline.tsx` — a bare SVG path for a table cell, not recharts: forty containers would otherwise mount
  forty responsive containers and resize observers. It also exports `TileTrend`, the one shape a
  reading's last hour takes in a `StatTile`'s `trend` slot on the deployment pages (and, on Metrics,
  the window on screen): the tile's full
  width, 36px, rising once. It draws nothing below two points, or for a series that never moves on a
  scale of its own, and the tile then leaves no band. Its colour is a series colour, never a status
  one, so a failing share is `--chart-3`.
- `range-picker.tsx`, `health-panel.tsx` — the window control (pan and zoom-out appear only once a window
  has been dragged) and the verdict.

`lib/metrics-crosshair.ts` holds the hovered instant **outside React**, for the reason the live buffer is:
a pointer crossing a chart fires continuously, and a context above the router would re-render the terminal
and the log tail on every mousemove. The value is a **timestamp**, not a row index — charts on a page do
not share a row array.
