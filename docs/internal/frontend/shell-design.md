# Frontend shell and design system

The App Router currently has 48 `page.tsx` entry points, including nested database, Docker, proxy,
security, and deployment workflows plus `/login`. Most page modules are client components; the three
deployment detail/new wrappers remain server components and hand interaction to client components under
`components/deploy/`.

## The shell

`(dashboard)/layout.tsx` owns `CommandPaletteProvider`, `SelfUpdateProvider`, `NavScopeProvider`,
`SidebarProvider` + `AppSidebar`, and `MetricsStream` — which renders nothing and exists to hold the
metrics socket open for the whole shell, so Overview's charts keep filling from other pages. Its
redirect to `/login` is convenience, not a control; every API call behind it is authenticated server-side.

**The scroll container is on the `SidebarInset`, not the document.** That is what lets a page ask for
the remaining height (`<Page fill>`) instead of growing past the viewport.

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
navigation. Three levels exist: the top-level list, a section, and one deployment inside Deployments —
or, inside a group, the group, a section in it, and that section's pages (Monitoring → Processes →
PM2; Server configuration → Proxy & TLS → Sites).

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
`useNavScope(...)` and the rail draws what it publishes: the databases layout, because which pages exist
depends on whether the connection is SQL and because every one of them carries `?conn=` (`replaces: true`
— it stands in place of the static Databases panel); and `deploy/project-shell.tsx`, because a project's
name, its game-only pages and its pending-changes mark are not in the URL (a level *below* Deployments).
The rail draws a project's rows from the route alone until that registration lands, so the panel does not
reflow — only the name fills in. The alternative was the rail polling the driver catalogue and a project
on every page in the product to draw a list the page beneath it already holds.

The groups run in the order a day on the server runs, and a page's header eyebrow is its group's
label: **Server** (Overview, and Monitoring: Metrics, Processes, Logs), **Apps** (Deployments first,
then Databases, Docker), **Workspace** (Terminal, Files, Git), **Protection** (Security, Backups),
**Advanced** (Server configuration: Proxy & TLS, Packages, System users, Audit log) and **System**
(Settings). The top-level list is twelve rows rather than seventeen: the three monitoring pages answer
one question, and the four configuration pages are opened to change the server rather than to use it.
Deployments open the second group because shipping something is the reason most visits happen; until
0.6.7 it sat fourth in a group called Operations, between Packages and Backups.

`PERSONAL_NAV` is a flat list of leaves — Profile (`/account`), Security, Sessions, API keys and, for
`system.admin`, Users — drawn three times from the one array: `ACCOUNT_SECTION`, which is that list as a
panel the rail drills into once you are inside `/account`; the palette's Account group; and the menu on
the rail's foot. That menu opens with the account's picture,
display name, sign-in name and role, then the five pages (Security carries a `Status` for two-factor)
and Sign out. The picture is `components/account/user-avatar.tsx`: the stored image when there is
one, otherwise the display name's initials in a hue taken from the username (`lib/hue.ts`'s `LANES`,
so the same person keeps one colour in the rail, the users list and their profile), square with the
control radius rather than a circle, because a filled circle holding two letters is the pill §4
forbids.

## The design system

A small set of files defines the visual language, and pages compose them rather than hand-rolling
layout. [`design-system.md`](design-system.md) states the rules in full; this is the map.

- `components/page.tsx` — `Page` (one measure, gutter and rhythm; `fill` for terminal and logs, whose
  content *is* the viewport), `PageHeader`, `PageState`, `Section`, `Toolbar`, `SearchInput`,
  `Metric`/`MetricStrip`, `DetailList`/`Detail`, `RowLink`. **None of the heading primitives takes a
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
  input is its own header). `components/side-panel.tsx` — `SidePanel`, the right-hand detail surface.

  **Which detail views are sheets.** A sheet is for a *glance*: you open it with the list still
  showing, read it, and close it — the list is the context you are using. A detail that holds a
  **stream, a terminal, or an editor** is not that. You stay in it for minutes, the list behind it is
  dead weight, and a sheet's `sm:max-w-3xl` is about ninety columns of terminal. Those are their own
  destination with a breadcrumb back, built the way `deploy/run-page.tsx` is: `PageHeader` with the
  parent as an `eyebrow` link, the name and a `Status` as the title, the verbs in `actions`, and what
  the thing *is* as a `MetricStrip` under it. A container, a compose stack and a backup job went that
  way on 2026-09-21; every other detail in the product is a `SidePanel` and should stay one.

  Their tabs stay **in** the page — they are views of one thing, which is what `tabClasses` is for.
  Do not reintroduce a route-level strip for them (see the `SectionNav` note below), and do not give
  one a `useNavScope`: the rail drills into a *section* with many pages, not into a leaf.

  One thing a sheet gives free that a page does not: `onOpenChange` is a single funnel every exit
  passes through, which is where `files/file-editor.tsx` guards unsaved work. The App Router cannot
  block a soft navigation, so a detail that guards a dirty buffer on close stays a sheet.
  `Modal` and `SidePanel` share one anatomy: title, tinted strip, a body that is the only
  part that scrolls, a footer strip. Both take `actions` in the title strip; `Modal`'s
  `size="full"` is the whole viewport with that anatomy intact, for the one task that is looking
  at a thing rather than filling in a form (the file viewer). Their `description` is rendered `sr-only` — Radix wants an
  accessible description and nothing is drawn. **Raw `Dialog`/`Sheet` are assembled only in those three
  components** — a page or a feature panel never opens one itself.
- `components/tabs.tsx` — the switchers that remain, all of which switch between *views of one page*:
  `tabClasses` (the underlined tab, for the log console's live feed against its search, the packages
  page's installed against its updates, the deploy wizard's source kinds), `FilterChip` and `ChipCount`.
  `SectionNav` and `TabLink` — the route-level strips — were deleted in 0.6.7 when the rail started
  drilling into sections; do not reintroduce a strip that changes the URL.
- `components/form.tsx` — what goes inside a task surface: `Field` (a label, a control, one line under
  it — a hint, or the error while there is one), `FieldRow`, `FormSection` (an eyebrow and a hairline
  opening part of a longer form), `OptionList`/`OptionRow` (a switch with its sentence), `FormFacts`
  (what the form operates on, as data under the title), `Statement` (the SQL a schema-editing form is
  about to run, with a copy) and `FormNote`. The databases section's dialogs are built from these and
  nothing else.
- `components/stat-tile.tsx` — `StatTile` (a small name over a 24px figure, an optional meter and one
  hint) and `StatGrid`, which runs them across the page with a hairline between cells and no frame
  around them, the first column on the page's own edge. `framed` restores the box. `StatLink` wraps a
  tile that is also a destination — the Docker and proxy overviews, and the Services row on the host
  overview — with the revealed arrow that says so on touch.
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
`EmptyNote`, `ErrorState`, `Notice`). `min-w-0` on the frame and its children is load-bearing: a wide
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
tints with the palette and stays crisp in the 3rem rail and on a retina sign-in alike. This is the
only rendering of the product's name, so sidebar, sign-in and splash agree and a rename is one file.
`LogoMark` is the glyph alone, which is what the collapsed rail falls back to.

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

`components/ui/*` is generated shadcn/ui (new-york, zinc) with its icons rewired to
the Heroicons vocabulary in `components/icons.tsx` — compose rather than
edit. Every side-panel toggle — the navigation rail's trigger, the terminal's rail and Files/Diff, the
Files sidebar and details, the logs sources, the ER diagram's inspector — draws its panel's side and
state with `SidebarLeftOpen`/`Close` or `SidebarRightOpen`/`Close` (drawn inline in `icons.tsx`,
since Heroicons has no sidebar): the strip filled while the panel shows, and a chevron pointing the
way a press moves it. They mean a panel toggle and nothing else, which is why the Files page's
Places menu is a map pin. `ui/context-menu.tsx` is the right-click menu, drawn with the dropdown's classes so the two
read as one menu; a feature that needs both (the file listing) renders one verb list into whichever
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
  the one tooltip listing every series at the hovered instant.
- `chart-panel.tsx` — the header/chart/legend shape and the empty state. A series with no numbers anywhere
  in the window is **dropped rather than drawn flat at zero**, which is what makes a kernel without PSI
  say so instead of reporting three healthy zeroes.
- `series-legend.tsx` — min/mean/max/last, plus **"At cursor"**: while the pointer is over any chart, every
  legend shows the value its series held at that instant. Max reads the peak column where a series has
  one, since the maximum of the *means* is exactly what a downsampled window hides.
- `sparkline.tsx` — a bare SVG path for a table cell, not recharts: forty containers would otherwise mount
  forty responsive containers and resize observers.
- `range-picker.tsx`, `health-panel.tsx` — the window control (pan and zoom-out appear only once a window
  has been dragged) and the verdict.

`lib/metrics-crosshair.ts` holds the hovered instant **outside React**, for the reason the live buffer is:
a pointer crossing a chart fires continuously, and a context above the router would re-render the terminal
and the log tail on every mousemove. The value is a **timestamp**, not a row index — charts on a page do
not share a row array.
