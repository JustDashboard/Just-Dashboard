# Components

The catalogue: one row per shared export, so a page can find what to use in five minutes. WP-17
fills the table in wave 1, and from WP-40 on `src/docs-catalogue.test.js` fails when a shared export
has no row or a row names something that is not an export. Each family's own page carries the
detail.

| Export | Family | File | One line | Status |
| --- | --- | --- | --- | --- |

## Families

| Family | Page | Written by |
| --- | --- | --- |
| Page header and identity header | [`page-header.md`](page-header.md) | WP-09 |
| Facts lines | [`values-facts.md`](values-facts.md) | WP-09 |
| Section head | [`section-head.md`](section-head.md) | WP-10 |
| Surfaces | [`surfaces.md`](surfaces.md) | WP-10 |
| Search and toolbar | [`search-toolbar.md`](search-toolbar.md) | WP-10 |
| List row | [`list-row.md`](list-row.md) | WP-11 |
| Card row | [`card-row.md`](card-row.md) | WP-11 |
| Choice card | [`choice-card.md`](choice-card.md) | WP-11 |
| Rail | [`rail.md`](rail.md) | WP-07 |
| Tabs, chips and segments | [`tabs-chips-segments.md`](tabs-chips-segments.md) | WP-08 |
| States | [`states.md`](states.md) | WP-12 |
| Status, tone and tags | [`status-tag.md`](status-tag.md) | WP-13 |
| Values | [`values.md`](values.md) | WP-14 |
| Overlays and confirmations | [`overlays-confirm.md`](overlays-confirm.md) | WP-15 |
| Menus | [`menus.md`](menus.md) | WP-15 |
| Forms and settings | [`forms.md`](forms.md) | WP-16 |
| Readings | [`readings.md`](readings.md) | WP-17 |
| Wiring pictures | [`wiring.md`](wiring.md) | WP-17 |
| Workbench | [`workbench.md`](workbench.md) | WP-17 |
| Feedback | [`feedback.md`](feedback.md) | WP-17 |
| Table | [`table.md`](table.md) | WP-34 |

Adding one: [`../contributing-a-component.md`](../contributing-a-component.md).

Until the table is filled, the map below is the one that closed `shell.md`. It covers the
first shared files, not all of them.

## The map carried over from the shell page

A small set of files defines the visual language, and pages compose them rather than hand-rolling
layout. The rules are in [`../standard.md`](../standard.md) and [`../foundations/`](../foundations/README.md); this is the map.

- `components/page.tsx` — `Page` (one measure, gutter and rhythm; `fill` for terminal and logs, whose
  content *is* the viewport), `PageContext` (an accessible page name, plus a linked parent and verbs
  on detail pages, with no visual page title; list commands live in their section or workbench),
  `PageState`, `Section`, `Toolbar`, `SearchInput`,
  `Metric`/`MetricStrip`, `DetailList`/`Detail`, `RowLink` (`SearchInput` is 40px and a line of its
  own below `sm`; `MetricStrip` is two-up on a phone — §8). **None of the heading primitives takes a
  `description`** — see rule 5 in [`design-system.md`](../design-system.md).
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

  **Which detail views are sheets: a small add or edit form, and nothing else** (D9). An item's
  detail is a page: a route of its own, its views named in `?tab=`, and from `xl` a rail of its
  siblings ([`../recipes/detail-with-rail.md`](../recipes/detail-with-rail.md)); the address that
  opened a sheet redirects to the page. What follows is how the product got there. A sheet was for a
  *glance*: you open it with the list still showing, read it, and close it. A detail that holds a
  **stream, a terminal, or an editor** is not that. You stay in it for minutes, the list behind it is
  dead weight, and a sheet's `sm:max-w-3xl` is about ninety columns of terminal. Those are their own
  destination with a breadcrumb back: `PageContext` with the parent as an `eyebrow` link and the
  verbs in `actions`, then the resource name and state among the page's facts — an identity line
  (`HostIdentity`) on a container's page and on a stack's, with the verdict at its end. The run page
  (`deploy/run-page.tsx`) goes one step further: since 2026-10-05 it opens on the header its
  project's pages open on (`run-header.tsx`, the project's tile, the commit, the run's state and how
  long it took, one line of provenance),
  because a run is one of the project's Deployments, and that header is the first thing on the
  page — its verbs sit at its far end beside the run's state, and the way back is the rail's panel
  and the menu's Open project rather than an eyebrow. (From 2026-09-24 it opened on the
  `HostIdentity` line the host Overview opens on instead.) A container,
  a compose stack and a backup job went that way on 2026-09-21, and a proxy site
  (`/proxy/sites/<name>`) on 2026-09-27, since it now holds its logs — the way back is a "Sites" link
  beside its verbs, the form and the raw file staying sheets it opens. A process followed in 0.8.6 and a network
  device and a volume in 0.8.7, and since 0.8.7 every item's detail is a page. A sheet that shows a log — a unit's journal, a PM2
  application's output — draws the service logs in their `layout="sheet"` form (no readings, no value
  columns, no facts beside the name), because the unit or the process is still the glance and the log
  one tab of it.

  Their tabs stay **in** the page — they are views of one thing, which is what `tabClasses` is for.
  Do not reintroduce a route-level strip for them (see the note on route-level strips below), and do not give
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
  The route-level strips were deleted in 0.6.7 when the rail started
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
  tile that is also a destination — the Services row on the host overview — with the revealed arrow that says so on touch; `StatButton` wraps one whose press
  narrows what is under it (a lens's readings wherever a page draws them, a site's request figures)
  with a revealed funnel and `aria-pressed`. `dense` sets the tiles two to a row on a phone.
- `components/outcome-strip.tsx` — `OutcomeStrip`, the last few attempts at something as a square per
  attempt in the colour of how it ended, oldest first: a backup job's runs, a project's runs, a
  channel's messages, a webhook's deliveries, a schedule's firings. One drawing, so a strip means the
  same thing on every page it appears on.
- `components/status-dot.tsx` — `Status`, the one live-state indicator: a coloured dot and a word.
  `components/tag.tsx` — `Tag`, small-caps text marking a *fixed property* of a row. No chip, no border.
  **There is no badge and no pill in this product**, and `design-system.spec.ts` checks that no page
  renders a filled pill. A count is `.numeric` text, a state is a `Status`, a property is a `Tag`, and
  anything longer than three words is prose.
- `components/icon-action.tsx` — `IconAction` (an icon-only row control with a real tooltip label),
  `RowActions` (the cluster of them that appears on hover, stays on keyboard focus and on an open menu,
  and is always visible on touch) and `DimActions`, the same cluster for a row whose controls have a
  column to themselves: always drawn, dimmed until the pointer arrives.
- Copying has two kinds of feedback. `copyText` (`lib/clipboard.ts`), called directly with the words
  to announce, raises a toast that says *what* was copied (340 rows as SQL); `useCopy`
  (`hooks/use-copy.ts`) sets a `copied` flag for 1.6 s, for an inline tick that says *that* it worked
  without moving the reader's eye (a chip about to be pasted) or where a toast would sit on what was
  copied (a set of recovery codes).
- `components/finding-list.tsx` renders results in requested diagnostic tools, including TLS/network
  reports, config lint and issuance diagnostics. Automatic attention panels, routine settings
  recommendations and navigation attention marks were removed in 0.7.5. Pending-edit marks still
  represent saved changes. Operational error messages and diagnostic evidence remain visible.

**Reach for `Panel`/`Pane`/`Page`, not raw `Card`**, and add a variant there rather than a one-off in a
feature page — before these existed, fourteen pages read as fourteen products. `components/state.tsx`
does the same for the non-happy paths (`Spinner`, `LoadingRows`, `LoadingPanel`, `EmptyState`,
`EmptyNote`, `ErrorState`, `Notice`); `LoadingPanel plain` is the same silhouette unframed, for a page
whose blocks arrive plain, and `EmptyState`'s `mark` draws what the list would hold (`ProductLogos`) in
place of the glyph. `min-w-0` on the frame and its children is load-bearing: a wide
table's intrinsic width would otherwise widen the flex column and take the whole shell sideways instead
of scrolling inside its panel.

**A page arrives from the top.** A block whose answer is taller than its skeleton, or that is not on
the page until its read answers, pushes everything under it down when it lands; eight pages moved that
way while they loaded, past a quarter of the window on Certificates. `Hold` (also in `state.tsx`) is the
answer where the loaded height cannot be reserved: `<Hold until={!read.loading} />` after the block
leaves a marker until the read's first answer, and a rule in `globals.css` keeps what follows the marker,
and every block of the `Page` under the one the marker is in, off the page until then. What is held is
mounted, so its own reads start with the page; it is only not drawn, so when it appears nothing that
was already there moves. Its children are what stands there meanwhile; `LoadingRows hold` and
`LoadingPanel hold` are the same for a skeleton that already stands in the block. A hold lets go once
and for good (a filter that makes the read load again takes nothing away) and after four seconds
whatever the read is doing, so a read that hangs does not keep the page from its reader. A band is not
a case for it: a band is drawn during load at its one height (design-system §10). `design-system.spec.ts`
makes the reads of those eight pages land apart, in both orders, at 1280 and at 390, and holds what the
browser's `layout-shift` observer reports under 0.02 (`tests/browser/layout-shift.ts`).

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

The brand ships as files in `public/brand-logo/`: the J mark (`just-mark*.svg`, with a `-small` cut
for 16px and under), the horizontal and stacked lockups of the mark with "Just Dashboard"
(`just-dashboard-*.svg`), each in ink, white and brand blue, and the app-icon tiles
(`just-tile-*.svg`). The `just-hub-*` files are the same system for Just Hub and are not drawn by the
dashboard. Anything outside the app — the README, a slide — uses those files as they are.

`components/logo.tsx` is how the app draws them, and nothing else does. `Logo` is the horizontal
lockup with the version as small muted text beside it: the lettering is the brand's own, outlined, not
"Dashboard" set in Source Sans beside a glyph, because the lockup fixes how large the mark is against
the words. It is one colour, `text-brand`, as `just-dashboard-on-dark.svg` draws it on this ground.
`LogoStacked` is the stacked lockup, for the start-up splash where the composition is centred.
`LogoGlyph` is the mark alone and `LogoMark` the mark as the collapsed rail's home link. All of them
are inline `<svg>` on `currentColor` — the same paths as the files, no raster — so they tint with the
palette and stay crisp in the sidebar and on a retina sign-in alike, and the glyph takes whatever
tone the surface it sits on gives it. `LogoGlyph small` draws the `-small` cut: at 16px and under,
the regular mark's gap between tittle and stem antialiases shut, so every call site at `h-4` or
below passes it. This is the only rendering of the product's name, so sidebar, sign-in and splash
agree and a rename is one file.

Where the UI draws the dashboard *itself* as one thing among others — the `just-dashboard` Compose
stack, its containers on a network, the sockets it listens on, the routes and rules it made, the
audit log's machine actors, a backup of its own store — it takes the mark (`DashboardMark` on a
product tile, `LogoGlyph` bare), not the Docker or Caddy logo its images would otherwise resolve to.

The browser icons are `just-tile-ink.svg`: `app/icon.svg` is the file itself, and `favicon.ico`
(16–128px) is rasterised from it, an ink tile rather than the bare mark because the pale mark would
vanish on a light tab strip. `apple-icon.png` is the tile's mark on a full-bleed ink square, because
iOS rounds the corners itself and fills transparency with black.

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
