# The design system

The rules the linter cannot enforce. `eslint-rules/design-system.mjs` catches the two failures that
are mechanical — typing a value where a token exists, and re-deriving a pattern a primitive owns —
and `tests/browser/design-system.spec.ts` checks three structural properties in a browser. Everything
below is judgement, and this is where it is written down.

Two earlier documents and the lint plugin's own header pointed at
`docs/plans/design-system-unification/reference.md`, which was never committed on any branch. This
file replaces that reference.

## 1. One mode

**The product is dark. There is no light palette, and there is no theme switch.**

Shipping two grounds meant every tinted surface, status hue, chart colour and terminal ANSI slot had
to be chosen twice and verified twice, and the second set was seen by almost nobody. `lib/themes.ts`
is now one bootstrap script: it puts `.dark` on the root element before first paint so the generated
shadcn primitives' `dark:` variants resolve, sets `color-scheme`, and clears the stored preference
from anyone upgrading. `hooks/use-theme.tsx` and `/appearance` are gone.

Colours live on `:root` in `app/globals.css`. That is the only place a colour is named.

## 2. Nothing lifts

**A surface separates by what it is made of, not by pretending to float.**

Cards, buttons, tabs, tiles and the sign-in panel used to carry `raised`: a gradient gloss, a light
hairline on the top edge, a dark lip below, a drop shadow, and an inversion on `:active`. It is
removed. Forty-nine surfaces all claiming to stand in front of the page is a page with no foreground,
and the treatment spent five CSS properties saying what a one-pixel border already says.

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
  rather than sit beside a hole — five two-up is two, two and one. The Overview's Services row is one
  of these too:
  a module's headline figure is a reading, and eight framed cards under a page that had just stopped
  drawing boxes were eight boxes. Each is a `StatLink`, so the arrow says it goes somewhere;
- a list that is the whole of a section — `Panel plain` keeps the panel's anatomy (header, toolbar,
  body, footer) and drops the border and ground, so a title and a hairline mark the block. Recent
  activity on the Overview, every chart, list and hardware reading on the
  metrics page, every block on the Docker pages (the
  overview's idle containers, attention, compose projects, cleanup and disk; the containers,
  volumes, networks, stacks and events lists with their toolbars; the Images page's band of what
  Docker holds on disk and what the registries say, over its framed table; the attention and
  storage blocks on a container's page), the
  Security section's exposure facts, area readings, findings, probe forms and the Auth log, Firewall
  log and Activity sections — each a title over the log's `Pane` (its tables, dashboard access
  picture and Tools workbench retain frames), every block on the proxy pages (the overview's engine
  facts, attention list, sites, certificate expiry and Engine log; the sites, certificates and
  streams inventories and the ports table with their toolbars; the Renewals section on
  Certificates; a site's own page, its readings on the page's ground and its logs one `Pane` under
  its identity line; the TLS report's readings, findings, protocol, certificate, chain, HTTP and
  preload rows, and its deep scan's findings, suite list with its chip filters, key exchange and
  connection rows; the password files and DNS provider lists),
  health findings, the runtime-health bar, and every block of the deployment section — the fleet
  and its archive, Credentials and Notifications, a project's Overview, Deployments, Logs, Runtime
  and Console, the run page, the nine settings pages and the create flow — are plain, with every
  row in them that is *taken* rather than read (a project, a run, a credential, a channel, a
  service, a webhook, a schedule, a variable, a linked database) a lit card — a `ChoiceRow` in a
  `ChoiceList`, or on the fleet's grid a project's own `SpotlightBorder` card with the same lit
  edge (what kept a frame there was, first, a *picture*, on the argument that a picture needs an
  edge to read as one thing — the settings pages' four were framed until 0.7.1 and now stand
  unframed over `wire-grid`, as below, each inside its section and drawn through
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
  along it while it carries. Those two draw this server as one mark (`WireHost`): the J in its own
  blue on `bg-wash-brand` inside a ring of `--rule-brand`, because it is a location — where the
  reader is standing — and the filled brand face it used to stand on is a command's (§3). The rollback dialog holds a small one of its own — your domains wired
  to the live release by a still line and to the release you are going back to by a dotted one,
  which is the line that carries once Roll back is pressed. The same vocabulary
  (`components/deploy/wire.tsx`) draws one more picture that sits *unframed* because it is inside a
  block that already has its edge: the way a request reaches a project on the overview — source,
  live release, runtime, domains, each drawn as its product — in the column beside the preview; each
  mark paints the ground under its tint so the line never shows through it. And two that are
  unframed because each is its page's opening rather than a block on it: where an outcome goes on
  Notifications — the projects on the left, this server in the middle, a mark per channel on the
  right, dashed rings for the kinds not yet added — and the GitHub App on Credentials — the
  accounts that installed it, the App and this server, with its setup path under it — stand on the
  page's own ground over `wire-grid`, as does the map a project's Runtime opens on (its domains,
  the containers answering them, and the volumes, folders and databases those keep, in three lanes
  with `shape="s"` wires, `deploy/runtime-map.tsx`) — the dot grid that fades out towards its edges, which gives a
  picture a middle where a border gave it an outline. General's automatic deployment (the
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
  Databases section's reading pages — the control center's readings, attention list, fleet and found
  servers, a database's home, its Performance, Advisor, Search and Generate — each a title, a toolbar
  and a hairline, with every database, found server, generator and search hit on them a lit card,
  and every block on the four Processes pages — the live table under its band of workloads, the
  PM2 applications under their band of what PM2 takes of the machine, the systemd units
  under their band of busy services and recent changes, and the cron jobs, timers and
  system cron files on Scheduled, each a title, a toolbar and a hairline, Scheduled's
  under its next day drawn as one plain band of lanes (what fires next counting down
  at the end of its identity line), whose Cron log is a plain panel holding the log's
  `Pane` and whose jobs and timers open sheets of their own — with a detail sheet built
  from plain panels that opens on the thing's own mark, the unit's journal, a PM2
  application's logs, a timer's runs and a job's cron lines a `Pane` in their sheets — and the
  two System pages follow the same shape: on System users four readings (accounts, administrators,
  who can sign in, the last sign-in) over the accounts as lit cards in a plain list, because each
  opens its keys, with its SSH-keys sheet a plain list of rows and a plain form, and on the audit
  log four readings of the last day over its table and filters, where a request's outcome is a
  `Status` dot and the code in its family's colour rather than a wash across the row; and
  both of the dashboard's own pages — on Version the identity line, the update in flight and the
  history (a timeline: the version in a sticky column, a rail with a mark per release, the notes at
  a readable measure), and on Configuration the stack (its checkout and three services as a
  `RowList`, beside Restart and Rebuild as two `ChoiceCard`s), the restart record, and the settings
  as `FormSection aside`s, each head over its fields — the framed things on those two pages are the
  transcript console, which is a `Pane`, and the cards you pick; the Backups page —
  a picture of where the server's data goes, on the page's own ground over `wire-grid` (what it
  has by kind, wired through this server to every directory and bucket a job writes to), an
  attention list of the jobs that failed or went quiet, the jobs as destination cards (a
  `ChoiceList`, each drawn as the products it covers, with its destination's mark and its last
  fourteen runs as a strip) and the coverage as lit cards under its filter chips and a meter of how
  much is covered — every thing on it is taken, opening the job that covers it or the form written
  for it — with a job's own page opening on an identity line and its own picture over its
  readings, and its runs one frame around a rail of the runs and an inspector of the picked one,
  the run page's Details shape; the five account pages — the profile's identity line, readings and capability rows,
  sessions and keys as rows under plain panels where a framed table used to be, the users as cards
  in a `ChoiceList`, and Security as `FormSection aside`s; and the four views on
  Packages — the installed and updates tables, the software search and the package Log (its `Pane`
  on the page's ground, no panel around it), under one underlined strip
  (`tabClasses`) rather than a filled tab list, beneath the host's identity and the software-size
  band beside its update queue — each a toolbar and a hairline over a framed table or, for the
  catalogue, lit choices on the page's own edge. Security updates carry their command in the queue;
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
against — three framed grids read against one unframed `StatGrid`, and the page reads as three tables
rather than as a stack of containers. A page on which the table is the only block is the easy case;
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

A panel that is also a destination takes `interactive`: its border steps up to `--border-strong`
under the pointer, and nothing else moves.

Press feedback is colour. A control with a face takes its own `active:` step — `active:bg-control-active`
for the neutral faces, `active:bg-brand-active` for the brand command; a ghost or link button has no
face to move and borrows the accent wash. Nothing translates, and nothing casts a shadow to say it was
pressed.

## 3. One blue, three jobs

The product has one colour, and it is asked to do three things. The brand blue is the face of a
command *and* the mark of where you are; the lit step is attention. What keeps the first two from
reading as each other is form rather than hue — a command is a filled face you press, a location is a
tint, a glyph or a fill behind a word — and what keeps all three apart is that only the lit step is
never at rest.

| Role | Token | Spent on |
| --- | --- | --- |
| Command | `--brand` (`#CAE9FF`), black `--brand-foreground` | The face of the one action on a surface: `bg-brand` at rest, `bg-brand-hover` under the pointer, `bg-brand-active` on press. Never a state, never a selection. |
| Location | `--brand` (`#CAE9FF`, oklch 0.919/0.044/239°) | The mark, the current nav entry, a module tile's mark on the overview, the active section tab, `--chart-1`. |
| Attention | `--signal` (the same blue saturated, L 0.78 C 0.13) | The focus ring, a search hit in the log console, the terminal bell. |

White is still a fill, but it is no longer the command face. `--primary` is the neutral ink a
*reading* draws as a solid mark — the checked state of a checkbox or a switch, a meter's bar — so a
filled state can never be mistaken for the one thing you press.

The two blue tokens are one hue, because the product has a mark and the mark is one colour:
`#CAE9FF`, the pale blue the J in `public/LOGO.svg` (inlined in `components/logo.tsx`) is drawn in.
**What separates the two is chroma, not lightness**: `--brand` is the logo's own value, the colour at
rest, and at L 0.92 it is already among the lightest things on this ground, so there is no brighter
step left for attention to take. `--signal` is the same blue saturated instead — a step deeper and
far more vivid — so what is happening *now* is still found before what is always there. Because the
brand face is pale, its label is black: `--brand-foreground` is the darkest value on the ground, and
the text and icon inside a command button are black, never white.

Blue sits at 239°, a long way from the 78° amber and the 25° red the status hues occupy, so the
identity hue can never be read as a warning or a failure. The brand hue is still never spent on a
*state* — failure always arrives attached to a status word, a dot or a toast.

**Nothing reads a hue by its old name.** The terminal's ANSI blue is `--chart-2`, which is a literal
blue rather than a reference to any role token, or `ls` paints directories in the pale brand tint.
`--chart-1` is the brand and `--chart-2` is a blue too; they are told apart on a chart by lightness
and chroma rather than by hue. `--chart-3` is a magenta.

Status keeps its own three hues (`--success`, `--warning`, `--destructive`) and they are never
borrowed for anything that is not a reading of state. What changed between two releases is not a
state, so the release comparison marks an addition, a change and a removal with git's letters in
`--git-added`, `--git-modified` and `--git-deleted`, and the pending-changes strip names them in the
same hues (`CHANGE_TONE` and `ChangeTag` in `deploy/vocabulary.tsx`) — a green "added" beside a green
"healthy" said the same thing about two different facts. The same argument runs the other way for a command: rolling back is the brand
face, not destructive red, because it moves traffic between immutable releases and destroys
nothing.

**Selection is `bg-accent`, everywhere** — a neutral fill, deliberately not a hue. The active session
in the terminal rail, a pressed `ToggleGroupItem`, an applied `FilterChip`, a highlighted command row,
a selected table row and the current file in the tree all take it. A filter that borrowed the brand or
the primary tint would read as the page's main action.

Tinted surfaces come from the three tone tokens, never hand-mixed:

- `bg-plot-*` — the rounded square behind an icon in an empty state, a notice, a module tile. Not in a
  surface's header: see §14.
- `bg-wash-*` — the ground of a banner that is tinted rather than framed.
- `border-rule-*` — the border of that banner, and of anything else framed in a tone.
- `bg-meter-track` — the unfilled part of a `Meter`. It was `--muted`, two hundredths of a step
  lighter than the card it sits on, so a meter had a fill and no track and eight per-core bars could
  not be compared with each other.

Hover has exactly three answers: `bg-row-hover` for a table or list row, `bg-menu-hover` for a menu,
select option or command row, and `bg-accent`/`bg-control-hover` for a control with a face.

## 4. One vocabulary per idea

**There is no badge and no pill in this product.** `ui/badge.tsx` was deleted so the decision cannot
come back by accident, and a Playwright test asserts no filled, fully-rounded label renders on any
page.

**An account's avatar is circular.** `UserAvatar` draws a picture or initials and carries
`data-slot="user-avatar"`. Account avatars are the one identity exception to the filled-circle ban;
labels, counts and numbered steps remain subject to it.

- A **count** is `.numeric` text next to its label.
- A **state** is `Status` — a coloured dot (or an icon) and a word. No border, no fill.
- A fixed **property** of a row is `Tag` — **small caps text, and nothing else**. No border, no
  ground, no padding. The chip is gone: 10px caps inside a hairline box is a container three times
  the height of the word it holds, and beside the 13px title where most of the 117 of these sit, the
  box read as the louder of the two. A `mono` tag keeps a quiet recessed ground because its contents
  are literal strings from the host — a cipher, a port map, a config hash — which run together
  otherwise and which small caps would corrupt.
- Anything longer than three words is **prose**, and belongs in the row's secondary line or a `Notice`.
- A **choice** is a `ChoiceCard` or a `ChoiceRow`, from `components/choice-card.tsx` and
  `components/flow.tsx`. Two shapes, one look: a choice that is *a name* is the whole card, and a
  choice carrying *a sentence* puts the control on its title and takes a `verb` — an ARIA button
  names itself from everything inside it, so a card holding a title, a paragraph and three tags
  announces all of it as the name of one control (§12). A run of the same *kind* of thing is rows;
  a choice between *kinds* is a grid.

  This one has already gone wrong once, which is why it is written here. `components/choice-card.tsx`
  existed to stop six places building the same tile six ways; the flow register then shipped a
  **second** `ChoiceCard` in `components/flow.tsx`, with a lit border the first one did not have — so
  the deploy pages had the new treatment and the database dialogs, the credential picker and the
  engine picker kept the old one. There is one again. A component whose name already exists in this
  product is not a new component — and the rule holds for a shape as well as a name. `ProductCard`
  is `ChoiceCard`'s compact form, a product's logo, its name and one line of what exactly would be
  used; it began as the database engine picker's card, and when the build settings needed the
  same card for ten builders and five package managers it became the shared one rather than a
  second, with `EngineCard` now rendering through it. A run of them sits in
  `ChoiceGrid columns="compact"`, two to a row on a phone and five across at `xl`.

A tag that annotates a **row** belongs at the row's edge, not against its title: ten of them
interrupt ten sentences at ten different points, and in a column they are a column. And a tag is a
property, not a state: on a run card *Live* is a `Status`, because a release serving is a reading
that will change, and *Pinned* is a `Tag` with a pin, because somebody set it and it stays.

`components/tone.ts` exports the one severity union: `"default" | "success" | "warning" | "danger"`.
`Verdict` keeps `critical` because that is what the backend sends and `Button` keeps `destructive`
because that is what the control does — but anything choosing a *colour* chooses from the shared
union. A component that declares its own tone names is the drift this file exists to stop.

## 5. No descriptions under titles

**`PageContext`, `Section`, `PanelHeader` and `ChartPanel` have no `description`.** They have no `icon`
either — that is §14.

Every page carried a sentence under its heading explaining what the page was — a caption for a title
the reader had already read and understood. It pushed the first real row of every page a line and a
half down the screen, and it was never read twice.

The rule and its three exceptions:

- What the reader genuinely needs before acting goes in a **`Notice`**, where it is a fact rather
  than a subtitle.
- Where the slot was carrying **data** rather than prose — a row count, how many volumes are unused,
  what platform and kernel a host runs — the data stays, moved to where it belongs: the header's
  actions, a `Tag`, or its own row.
- `Modal` and `SidePanel` keep a `description` prop, rendered `sr-only`. Radix wants an accessible
  description and a screen reader is the one audience that cannot see the rest of the box.

`EmptyState`, `ErrorState` and `Notice` keep their body text: there, the sentence *is* the content.

## 6. Three states, three mechanisms

Selection, hover and focus were once drawn the same way, so a keyboard user could not tell which
filters were applied and a mouse user tabbing away found the selection had apparently moved.

- **Selected** — a filled surface. A property of the data.
- **Hover** — a faint background. A property of the pointer.
- **Focus** — a ring drawn outside the control. A property of the keyboard.

A control can be all three at once and stay readable, which is the test.

The ring is `focus-ring`, or `focus-ring-inset` where there is no room outside the control — one
appearance, two placements. It is an `outline`, not Tailwind's `ring`: an outline follows
`border-radius`, cannot be overwritten by a `shadow-*` utility landing on the same element, and works
on a `<tr>`.

A row's actions appear through `RowActions`, `IconAction reveal`, or `rowReveal()`. Never by hand: the
four mechanisms that sentence hides — pointer, keyboard, open menu, touch — are how five action
clusters ended up permanently unreachable on a phone.

**An action on a row is laid out, never overlaid.** The draft list positioned its discard button
`absolute right-2` against the `<li>` while the row reserved space for it with `pr-12` against a box
that `ROW_BLEED` had made twelve pixels wider on each side — so the button sat on top of the last
reading in the row. The arithmetic is not the lesson: two boxes measured against different origins
will drift again the moment either one's padding changes. A second action belongs in a slot in the
row's own flow (`ChoiceRow`'s `actions`), where there is nothing to mis-measure, and it must stop the
press from reaching the row around it — discarding a draft used to navigate to the draft it had just
deleted.

Reveal only where the controls share their space with something else. Where they have a column of
their own — a container card's actions slot — use `DimActions` instead: always drawn, one step of opacity until
the pointer is on the row. A reserved column left empty reads as a layout bug, not as an affordance.

## 7. Which surface

| Surface | Is | Is not |
| --- | --- | --- |
| `Panel` | A block of content *on* the page: framed, header, hairline, body — or `plain`, the same anatomy with no frame | Not a working region |
| `RowList` / `Row` | A list of rows with hairlines between them: a leading mark, a title, a second line, a trailing state | Not a table — nothing lines up in columns |
| `Pane` | A sized region of a workspace that owns its own scrolling — session rail, file tree, log console. `flush` drops its frame for a pane that is one column of a workbench sharing a single frame, with a hairline between columns (the terminal page, the logs page's source rail beside its log workspace, the files page's sidebar, listing and inspector under the strip that holds the page's commands, and a deployment's Output inside its Logs page's pane) | Not a block in a page's flow |
| `Well` | Output you read: command output, a log tail, a diff, a stored secret | Not a fence around controls |
| `Group` | A fence around part of a body: a set of ports, one release task, a repeated form row | Not a `Panel` — no header, no lift |
| `StatTile` | One headline figure, in a `StatGrid` | Not free-form — a row of them is read as a table |
| `ChoiceCard` | One thing you pick out of several, in a `ChoiceGrid` — a source, a template, a database engine | Not a `Panel`: a panel is content that happens to be framed, this is a button that happens to be large |
| `ChoiceRow` | One instance among many of one kind, in a `ChoiceList` — a repository, an image tag, an unfinished setup | Not a `Row`: a `Row` is read, a `ChoiceRow` is taken |
| `BarList` | A ranked list with the meter's track behind each name and the figure at the right — the top ten of something, with a signal segment for the share that is wrong | Not a chart, and not a `RowList`: nothing here has a second line worth a row |

**Reach for `Panel`/`Pane`/`Page`, and add a variant there rather than a one-off in a feature page.**
Before these existed, fourteen pages read as fourteen products.

`Modal`, `PaletteModal` and `SidePanel` are the only assemblers of raw `Dialog` and `Sheet`, and
`no-restricted-imports` enforces it. A page never opens one itself.

**What goes inside a task surface is `components/form.tsx`.** A `Field` is a label at `text-body`,
a control, and one line under it — a hint, or the error while there is one; `FieldRow` puts two or
three side by side; `FormSection` opens a part of a longer form with a title and a hairline;
`OptionRow` is a switch with its sentence, because "Stop on error" as a checkbox label asks the reader
to guess what an error stops; `FormFacts` states what the form operates on as data under the title;
`Disclosure` is the part of a form that is folded away; and `Statement` shows the SQL a
schema-editing form is about to run, nearest the button that runs it.
The databases section's dialogs had assembled their own forms out of `Label`, `Input` and a
`space-y-1.5` div and arrived at three label sizes, two input heights and no way to write an error
beside the field that caused it. They are built from these now, and the statement in one is the
server's own: a structure change (`components/database/schema/change-dialog.tsx`) asks its route with
`?preview=1` as the reader types and draws the answer, the command stays off until the server has
planned exactly what the form says, and the run sends the same body. A change that rewrites or may
refuse rows adds a warning `Notice` under the statement and asks its second question in the dialog's
own footer rather than in a second dialog, because the subject and the statement it is about are
already above it; a change that destroys goes through `useConfirm` with the subject named and the
statement from one preview. A row edit is reviewed the same way (`kit/sql-review.tsx`): the
statements a change set will run are the server's dry run of it.

**A page that is a form stacks each section's head over its fields, in one column.** `FormSection
aside`, run in `FormSections`, sets the title over the fields with the section's current state under
it as data — the address it answers at, the certificate on disk — and holds the whole section to
`max-w-3xl`, the 48rem the fields already had. `FormSections` keeps to the same width, so the
hairline between two sections, and between two of a settings page's forms, stops where the fields
stop. The heads used to sit in a rail, a 15rem column beside the fields from `lg` (from `xl` inside
a project's settings), made for `/dashboard/configuration`, whose five sections with their heads
over their fields had read as one long run in which a head was as far from the fields above it as
from its own. The rail spent a third of the width on a few words a section and set each head on the
line of the first field label beside it, so a section read as two labels side by side. `aside`
keeps its name from that column, and now means a section of the page rather than of a dialog.

**Where the column sits is the page's call.** A page that is only a form centres it: the nine
project settings pages (`SettingsPage` centres its strip and readings with it, so they line up with
the fields), a game server's settings, the account's Security and a database's Settings. A 48rem
column against the left edge of a wide page left the rest of it an empty band nothing explained. A
form that is one block among full-width ones keeps their left edge instead — the proxy pages'
password files, access lists, backups and watched domains, Security's findings and SSH — because
centred there its title stood 180px in from every title above it. Configuration's settings are the
exception: five short sections under a full-width stack, so from `xl` they run as two columns of
`FormSection aside`s (`max-w-none`), the certificate across both, rather than one column that left
two thirds of a wide screen empty. SSH's four directive groups took the same two columns in 0.7.1.

**Spacing does what the rail was for, and type keeps the ranks apart.** The space is asymmetric on
purpose: 32px above and below every section, so 64px and a hairline between one section and the
next, against 16px from a head to its own fields, which stand 20px apart — a head is always nearer
its own fields than the ones above it. The title is `text-base` semibold, a `Section`'s 16 and a
rung above a dialog section's 15, because it names a part of the page (§8). The state under it is
`text-xs` muted, 12, between the title and the 11px hints in the fields, because it is data rather
than a caption (§5). `actions` sit at the far end of the head's row, and so does a settings
section's `settingStatus`, so "Unsaved changes", arriving with the first keystroke, takes no line of
its own and pushes no field down.

**The deployment settings are that shape, and they were the last pages in the product that were
all containers.** Each of the nine was a stack of framed cards — a title strip, the form, a footer
holding Save — on the argument that things filled in one at a time read best as bordered boxes,
which is the wrong shape for a page that *is* a form. `settings/setting-card.tsx` draws them now.
`SettingsPage` reads the configuration, then draws what is saved but not live yet (a strip with no
button: the project context row already carries the one "Deploy changes", and a second brand face a
hundred and fifty pixels under it was two commands on one surface), the page's readings, and its
forms in one run of sections. `SettingForm` is one `<form>` and one write, and may span several
sections, because what one PUT writes is one form — Runtime is five. `SettingSection`
is a section's head carrying what the section currently is as data (the host and branch it builds
from, the port it answers on) and, at its far end, at most one `settingStatus`: *Not saved*,
*Unsaved changes* or *Saved · not live yet*. A form draws no Save of its own. Each one ended on
its own button at the fields' right edge, so a page of three forms carried three Saves in three
places and none of them was where the eye was after an edit two screens up. The page has one Save
instead (`settings/save-bar.tsx`): `SettingsPage` mounts a `SaveBarProvider`, every `SettingForm`
puts itself on it with its dirty state, its count and its `onSave` (which resolves true when the
write went through), and while any form holds an edit a bar floats at the foot of the content area,
centred on the column it saves. It is the one thing on a settings page that floats, so it takes a
popover's surface and shadow (§2), and it rises in (§11). It counts the edits across the page,
names the parts they are in and when they apply, and carries Discard and Save; ⌘S (Ctrl+S) and
Enter in a field are the same Save. Save writes each dirty form in page order, one after another,
because two forms on one page write the same configuration: `useConfiguration.save` sends the
revision the last write handed back and fills what a form does not own from that copy, and a form
that owns only a field of a part (Build's release tasks are a field of `build`) passes a function
of the latest copy. A refused form keeps its edits and says why under the field, and the bar stays
with what is left; when everything went through it says *Saved* and when it applies, then leaves —
so the forms no longer toast their own success. A clean page has nothing to save and no button:
re-checking an untouched Source means saving an edit to it. The game server's settings take the
same bar, and hold its Save while a value is outside its declared range. Each form keeps its draft keyed on
a digest of its own saved value (`useSettingDraft`): every save writes a new revision of the whole
configuration, and a draft keyed on the revision was thrown away by a save of the section beside it.

**A sheet is the same anatomy wherever it opens**, which is `SidePanel`'s doc comment and `Modal`'s
too. The deployment section's sheets each put a different body in the one frame — a `Select` for a
kind, an eyebrow `<legend>` over a group, Cancel on one and not the next — so a sheet was
recognisable by which page opened it. In order: a sheet that edits a thing that exists opens on
that thing, its mark, its name and its `FormFacts`, so the subject is recognised before a field is
read; a choice between kinds — a credential kind, a channel's service, a webhook's provider, an
alert's measure, a schedule's action — is a `ChoiceGrid` of cards carrying their marks (a
service's own logo where it has one), never a `Select`, because a kind is picked by its mark;
groups of fields are `FormSection`s, a title and a hairline, never a legend quieter than the labels
it heads (§8); options are an `OptionList`; and the footer is Cancel, then the one command,
right-aligned, with a line about what the command will do first and pushed to the left. A body that brings its own command — the traffic-alert form,
which opens both on Automation and on Logs — hands it to the sheet's footer through
`SidePanelFooter` rather than drawing a second foot inside the body.

**A field and the controls that belong to it are one box.** `ui/input-group.tsx` — shadcn's
`InputGroup`, re-sized onto this product's control ladder — holds an `InputGroupInput` with
`InputGroupAddon`s at either end, separated from the field by a hairline rather than by a gap. It
exists because of the shape the deployment forms had in four places: an `Input`, a 14px `Switch` and
a 36px `Button` standing in one wrapping flex, so a single decision — *this host, over HTTPS, check
it* — was three boxes at three heights, and the smallest of them was the one that decides whether
the site answers on 443. Inside a group there is one border, one height and one focus ring: the
group takes `focus-ring-within` and the field drops its own, because the border it would hang a ring
on now belongs to the group. A binary inside a group is a `Toggle` the height of the field (pressed
is `bg-accent` and a `text-brand` mark, which is §3's selection), never a `Switch` — a switch is the
control for an option in a list of options, where a sentence names it and it answers. That toggle is
`InputGroupToggle`, in the same file: it began as `/deploy/new`'s HTTPS segment, and the settings
pages wanted the same control for two more fields — *Read only* on a container path, *Reference* on
a variable's value — and the credential and channel sheets and first sign-in a third, *Show* on a
secret. Its word goes on a phone, where 390px of
field had become 140px of field and 250px of labels, and its `aria-label` says the whole of what it
does ("Serve this hostname over HTTPS") at every width. The requests page's path filter is the
reference for a field that holds its own binary: *Pages only* inside the path's group, and the
narrowings already applied beside it as chips that each clear themselves. A group sits
*inside* a `Field` and takes its label, its hint and its error; it does not replace one.
**Every rule inside a group is the addon's**, drawn for each child after the first — a call site
adding its own `border-l` to the button it puts there gets two pixels where §2 asked for one, which
is what the environment rows shipped with while the hostname field a section above them drew the
same divider correctly.

**A closed set of short words is one segmented control.** `settings/segments.tsx` is a single
`ToggleGroup` for two to five words — a Python release, the stage a variable reaches, a health
check's kind and phase, an HTTP method — which the settings had drawn as a `Select` (a closed set
asked for as if it were open, behind a press that hid the other answers) or as free text that
accepted "3.9". The segments not chosen step back to the muted ink, because `bg-accent` on its own
is one step of ground the reader has to compare against its neighbours rather than a mark; and an
empty value is ignored, because Radix lets a second press clear the choice and here there is always
an answer. Two binaries that belong to one row — a domain's HTTPS and its password, a variable's
Secret or Config — are one outline `ToggleGroup`, not two switches.

**A rule a value has to meet is lit as it is met.** `FieldCheck` (`form.tsx`) sits under the field
it belongs to — a credential's name rules, a Telegram channel's token and chat id, a password's
length and mix — so an error is
never the first the reader hears of a rule; a run of them is `aria-live="polite"`, never
`role="alert"`, because a rule being met is news rather than an interruption.

**A run of filter chips is a `ChipStrip`** (`tabs.tsx`). On a phone it scrolls sideways and bleeds
to the page's gutter, so the chip cut off at the edge is the cue that there are more; wrapped, a
strip of five broke into ragged lines with the last chip alone and pushed the list it filters a
screen down. From `sm` it wraps as chips always have. A chip that is a legend — the request log's
status families — may colour its count for the chosen state, so "5xx 13" says which thirteen.

**A confirmation names its subject.** `ConfirmRequest.subject` draws the thing an act touches above
its sentence — its mark, its name and the facts that tell it from its neighbours — because "Remove
credential" over GitHub's logo and *GitHub PAT · github.com* is recognised before it is read, which
is the moment the wrong one gets caught. Where the act takes a typed phrase, the phrase is a
`Field` like every other in the product rather than a bare label at a fourth size, an `InputGroup`
whose mark at the end answers the typing: the phrase is right, and the button under it is live.

**A fold is a section whose head is a button.** `Disclosure` is a native `<details>`, so a page can
open one from outside by setting `.open` (which is how a preflight finding reaches a control folded
away inside one) and find-in-page can reveal it; the platform triangle is replaced by the chevron
the rest of the product draws. It exists because one screen had four of these in three spellings:
two bare `<summary>`s at different sizes, and — on Configure's Advanced — a full-width framed strip
in `bg-surface-header`, a ground this file reserves for a `Pane`'s chrome, carrying a glyph §14 does
not allow a header and the word "Show" at the far end. It was the only block on that screen still
drawing a box around itself.

Unifying those three left the opposite defect: a left chevron in front of a 13px word, in a column
of sections that each began with a title and a rule, so the two largest parts of Configure — the
build settings and Advanced — read as stray lines of text. Giving it a section's anatomy instead —
the title at a section's rank, a chevron at the far end — fixed the rank and not the affordance, and
made the affordance worse: the fold was then *indistinguishable* from the `FormSection` heads above
and below it, and the only thing saying otherwise was a 16px chevron a thousand pixels from the word
it belonged to. Nothing on it said "press me" until the pointer was already on it, which is no use,
because the reader has to decide to go there first.

**So a section fold is drawn as what it is: a wide button.** `--control` over `--input` at
`rounded-lg`, which is the resting anatomy of every other control on the screen — the outline
button, the select, the field — with the chevron anchored to that edge and the body below it needing
no rule, because the head has its own. §2 allows a control a face; what it refuses is a block
pretending to be one, and the framed strip this descends from was refused for its ground
(`--surface-header`, a `Pane`'s chrome), a glyph §14 bans and the word "Show", never for having an
edge. `quiet` is the other rank, for a fold *inside* a section — a pasted block, a clone option, an
effective plan: no face, a hover wash, and its chevron **leading**, because at the far end of a line
of body text a marker reads as unrelated punctuation.

`facts` says what is inside while it is shut, because "Advanced" tells the reader nothing about
whether their answer is in there and the cost of finding out is a press and a wall of fields. It is
drawn in the `<summary>`, which is the one place content survives the fold being closed, so it joins
the control's accessible name — and that is the right name. It takes a third of the row and no more,
because the title's `flex-1` gives it a flex base of zero and a facts line sized by its own content
would otherwise take the width it wanted and truncate the title instead. It yields the row entirely
below `sm`, which is the one thing to know when choosing what to put in it: **`facts` is what the
fold holds, never a caveat the reader needs whether or not they open it.** "Clone options · apply to
every import here" keeps its qualifier in the title for exactly that reason — the switches inside
govern the rows above the fold, so a phone that never sees the second half of the sentence is a
phone that cloned without submodules and was not told.

The summary carries `role="button"` and `aria-expanded`: a native `<summary>` is a disclosure widget
the platform knows about but exposes no readable expanded state, and the framed strip this replaced
was a real `<button aria-expanded>` that did. `aria-expanded` is seeded from `open` and thereafter
fed by the element's own `toggle` event rather than derived from the prop — a caller may pass `open`
without `onOpenChange` (the build fold computes its initial state from detection and has nowhere to
store the operator's later answer), and React writes the `open` property only when the prop changes,
so a prop-derived value went on announcing "collapsed" over fields that were plainly on screen. The
one thing `role="button"` costs is the heading outline: a button's descendants are presentational,
so a fold cannot also be an `<h3>` the way a `FormSection` is, and the standard
`<h3><button aria-expanded>` shape is not available inside `<details>`, whose first child must be
the `<summary>`. That is the same as it was before the unification — the framed strip was not a
heading either — and the widget semantics are worth more here than the outline entry. A caller that
has to open one from outside passes `open`/`onOpenChange`; the rest pass neither.

**An option is one control, not a sentence with a switch at the end of it.** Three things had come
apart in `OptionRow`. Its switch was `size="sm"` — 14px, shorter than the 13px line of text naming
it, and the only place in the product drawing that size while every other switch was the default 18.
Its text ran the full measure of a 1200px panel, so the switch was not at the end of a sentence but
across a void from one. And what the switch *revealed* was a sibling of it at the same rank: on
Configure, "Hostname" sat directly under "Publish on a public hostname" in the same 13px medium,
with nothing saying the field existed *because* the switch was on. It is now the product's own
switch, a label row that washes under the pointer so both ends light as one target, a measure that
stops where a line stops being readable, and children indented behind a rule — the containment §7
gives a `Group`, drawn as one line rather than four sides because this fence already has a head.
The switch is centred on the row rather than hung off the title's line: a two-line option put it
against the top edge of a box it was nowhere near the middle of, and it read as having slipped.

**And on `/deploy/new`, an option's title says the whole thing.** §5 took the sentence out from under
every page, panel and chart title in the product and left it under a switch, one rung smaller — so a
flow screen of six options was six titles and six captions, and the caption is what made the row two
lines tall in the first place. "Report each release on the commit" plus "Posts a pending, success or
failure status to GitHub through the dashboard's own credential" is one fact written twice; "Report
each release as a GitHub commit status" is the fact. Where the hint carried a *consequence* the title
has to carry it too — "Required — a failing readiness check blocks activation", not "Readiness check
required" — and where it carried something that belongs to the whole section rather than to one
switch (which branch is watched, that nothing fires before the first deployment) it moves to the
section's `FormNote`, where it is said once. Configure's container access is the shape to copy:
"Run privileged — every host device and kernel capability", "Share the host's network — every port
it opens is open on the host", "Build without the cache — every layer from scratch". A risky
option's title turns amber only while it is on — a reading of state, not a warning at rest — and a
template's yes-or-no input is an `OptionRow` whose label is the title, never a switch under a
label with an *On*/*Off* word beside it.

`hint` stays on the component, and §5's second exception is why: the ambiguous-candidate rows on
Configure pass `high confidence · Next.js via recipe in apps/web`, which is **data** about the option
rather than prose about the switch, and data stays. Roughly forty-five option rows elsewhere in the
product still pass prose; they are not wrong so much as not yet done.

## 8. Type

The face is **Source Sans 3**, self-hosted from Adobe's
[3.052R release](https://github.com/adobe-fonts/source-sans/releases/tag/3.052R). The upright and
italic variable WOFF2 files sit in `src/app/fonts/` with their SIL Open Font License. `next/font/local`
loads both in `app/layout.tsx` at weights 200–900 and emits `--font-source-sans-3`; `--font-sans` in
`globals.css` puts it ahead of the system fallback. Nothing at build or run time reaches out to a
font CDN. `--font-mono` stays fixed-width for terminals, source code, and technical identifiers.

The ladder is `text-micro` (10) → `text-hint` (11) → `text-xs` (12) → `text-body` (13) →
`text-sm` (14) → `text-title` (15). An arbitrary `text-[Npx]` is a departure from it, and the linter
says so.

**`text-title` is what a surface calls itself**: a `PanelHeader`, a `Section`, a `Modal`, a
`SidePanel`. It was `text-body` on the first two, which put a panel's name at the size of the rows
underneath it — legible only because a tinted icon square was sitting in front of it doing the
separating. With the square gone (§14) the title has to be the thing that reads as a heading, and two
pixels is the whole of what that takes: `text-title` is a step the ladder already had, so the visible
ranks run a flow question (24, `text-2xl`) or a reading (24) → section (16, `text-base`) → surface
(15) → body (13) with nothing invented in between. The reading page title went from 20 to 24 in
0.6.7, then left the visual layout when the rail became the visible page location. A `StatTile`'s
figure stays at 24 because a headline number is what a reader finds without reading.

Weight carries hierarchy where size cannot. The sidebar is the one surface dense enough to need
three: group labels in the eyebrow's small caps, resting entries `font-normal` so the column reads as
a list rather than as forty-nine headings, and the current entry `font-medium` alongside its accent
fill and brand-blue icon. A panel's head speaks in the eyebrow when it names one of this product's
sections, and not when it names something the reader called — a project, a database connection:
small caps turned `api-production` into API-PRODUCTION, which is the corruption §4 keeps literal
strings out of caps for, so a scope's head is printed as written at `text-hint` semibold, after its
own mark (the project's favicon or product, the connection's engine) at the line's height.

A menu reads the same way at a smaller size: a `DropdownMenuLabel` or `SelectLabel` is an
eyebrow over the group it names, and every option and action is `text-body`. Selects, action menus,
right-click menus and form popovers share the surface and opening motion in `ui/menu-styles.ts`;
menu rows are at least 32px on desktop and 44px on a phone, with the same hover wash, disabled state and
right-hand check for a selected option. Reduced motion removes the opening animation.

`SelectTrigger` composes the shared outline `Button`, so its border, focus ring and press feedback
are the button's. A select opens below its trigger with a 4px gap, aligned to its starting edge;
Radix can flip it above or constrain it to the viewport when space runs out. Its width accommodates
the trigger and its options, without an ownership-specific minimum width. Arrow keys, typeahead,
Home/End, Escape, focus restoration and scrolling remain the primitive's responsibility.

**An option has one text column.** `MenuItemText` draws an option's name and any useful metadata
beneath it, never at the far end of a second column. `SelectItem` uses this for its `hint`, outside
`ItemText`, so metadata is excluded from the option's accessible name and the value copied into the
closed trigger. `CredentialSelect` keeps the host's glyph beside the name and the host and kind
below it. File locations, terminal snippets, database switchers, saved queries, column pickers and
export menus use the same text component. Keyboard shortcut hints retain their end alignment.

`OwnershipSelect`, shared by deployment domains, mounts and volume dependencies, offers only
Managed, Linked and (where supported) Observed in the menu. Its button shows the chosen word;
the selected removal consequence stays beside it and is connected with `aria-describedby`.
Search scope and database activity history also use the shared select, rather than native controls.

**A field is 16px on a phone.** `Input` has always gone to 16px below `sm`, because iOS zooms the
page into any smaller field that takes focus, and `SelectTrigger` and `SearchInput` now follow it —
the search box had pinned `text-body` over it. From `sm` the select's text is `text-body`, where it
was 14px throughout, so an `Input` and a `Select` in one `FieldRow` read at one size. The search box
is 40px on a phone, beside the 40px compact `Select` it usually shares a toolbar with, and takes a
wrapping toolbar's first line to itself: a call site's `flex-1` had squeezed the projects search to
95px beside its chips. The same pass sized the rest of a phone for a finger. A dialog's or a sheet's
close button is 40px below `sm` and 32px from it, and answers the pointer with a wash rather than an
opacity step; it was a bare 16px glyph, the smallest thing to hit on a surface whose fields are
44px. A `MetricStrip` is two columns on a phone rather than a wrapping row, because wrapped, the
first metric of each new line kept the rule that belonged beside its neighbour on the line above.
And `HostIdentity` sets its tile and title on one row with the facts the full width beneath them,
where they had run down a narrow column beside the tile. These are primitives, so every page that
draws them — Docker, Security, Audit, the account pages and the rest — changed with the deployment
section, not only `/deploy`.

A **view strip** — the underlined tabs that switch between two readings of the *same* page — is
`text-body`. It was `text-xs` while the page title was 20px and the strip sat within two pixels of both
the title and the panel titles; the earlier 24px title gave the strip a rank of its own, and 12px
chrome under it read as an afterthought. There is no route-level strip to size: since 0.6.7
the sidebar drills into a section and lists its pages, and a tab that changes the URL is not a thing
this product has.

A **form runs four ranks**: section (15, semibold) → option (14, medium) → field label (13, medium)
→ hint (11, muted). `FormSection` opened with the eyebrow's 10px muted small caps, so a Configure
screen of nine sections set every head *quieter than the 13px field labels it opened* and the
loudest line in each block was a label — the same argument the paragraph above makes about a panel's
name, one rung down and one degree worse. The head went to 14 first and 14 was not enough: an
`OptionRow`'s title is 14 so that it outranks the fields it governs, which left "Public address" and
"Publish on a public hostname" two lines apart at one size with a weight step between them and
nothing else. At 15 the four steps are visible and every one of them is a rung the ladder already
had. A `Disclosure` takes the section's rank, because a fold is a section (§7). A page section's
head is the exception upward: it names a part of the page rather than of a dialog, so it is a
`Section`'s 16, and its state is 12 so that it reads between the head and the 11px hints in the
fields under it (§7).

A **table header** is `text-hint`, medium weight, muted — not the eyebrow's small caps. At 10px
tracked-out caps a nine-column header was the loudest line in the table, above rows it exists only to
name. It is opaque, so rows scroll *under* a sticky header rather than through it, and the ground it
is opaque with is `--panel-ground` — declared by the panel (`--card` framed, `--background` plain),
never assumed by the table. Reading `--card` there put a faint unexplained band across every table on
a plain panel, overhanging the header hairline by the table's own `-mx-4` bleed.

**A row's text shares one centre line.** A bare `span`, `div` or `Link` placed in a flex row is a
block, and a block keeps the line box it inherits — the page's 16px, 24px-tall one where nothing
set another. A 12px `Status` or `Tag` inside it rests on that box's baseline, three pixels below the
button beside it: the database strip's "connected", a deployment's route and certificate columns,
a container card's state, a release note's kind and the identity line's aside all shipped that way.
A wrapper around an inline status is a flex box (`flex`, or `flex flex-col items-start` for a state
over its detail), or it carries its content's own type size. A glyph beside a title is nudged by
the title's line box, not by habit: `mt-0.5` centres a 16px glyph on a 20px line and drops it two
pixels below a `leading-tight` one. A field and its button in one row are `items-center`, since the
field is 44px on a phone and the button is not. A view strip's tabs carry `pt-0.5` against their
2px underline, so their labels sit on the strip's centre line with whatever shares the strip.
`design-system.spec.ts` checks the first of these on every surface it opens.

`.eyebrow` is the small-caps label that opens a section, a panel header or a stat tile. `.numeric` is
any figure meant to be compared with the one above it — tabular digits stop a polling table from
shimmering.

**`cn` has to be told about these names, and `lib/utils.ts` is where that happens.** tailwind-merge
resolves conflicts by looking each class up in a table of groups built from stock Tailwind. The six
custom steps are not in it, so `text-micro` fell through to the generic `text-{colour}` matcher and
was treated as a colour — which meant any element carrying both a size and a colour silently lost the
size and rendered at the browser's default 16px. Every `Tag` in the product did exactly that, which is
why they looked oversized while the markup was correct. Adding a step to the scale means adding its
name to the `font-size` group in `extendTailwindMerge`, or it will not survive a `cn()`.

## 9. Radius

`rounded-sm` (4) for a mark, `rounded-md` (6) for a control, `rounded-lg` (8) for a fence or a well,
`rounded-xl` (12) for a block. Nesting runs outer → inner in that order. Bare `rounded` and the steps
outside this ladder are lint errors.

Account avatars use `rounded-full` at every size (§4); their silhouette identifies a person rather
than a control or a surface. `InitialsMark` and forge faces retain their compact mark radii.

## 10. Charts

`components/metrics/` is a third design-system file in all but name. Every chart goes through it
rather than assembling its own recharts tree — adding a measurement should mean naming a series.

- The x-axis is **numeric over time**, never a category axis of pre-formatted labels: a category axis
  spaces every bucket equally, which lies whenever the record has a hole in it.
- A series with no numbers anywhere in the window is **dropped rather than drawn flat at zero**.
- **A measure some days have and others do not is a mark on each day that has one**, on the
  window's own days, never a line through the days that do. Deployment history's release time per
  day was a sparkline over the days something shipped: four releases a week apart drew as one
  slope, with no date and no scale to read it against. It is a bar on its day now, a tick on a
  day nothing succeeded, the window's median ruled across and the top of the scale named.
- **Live and recorded data are never spliced into one line** — the cadences differ by two orders of
  magnitude. Where a page offers both, Live is a range of its own beside the recorded ones: a
  project's Runtime draws the container's stats socket over its last five minutes
  (`useContainerLive`, which keeps the window per container so the Overview's minutes are already
  on it), and 1h–7d from the record, in the same panels. Live is drawn as five-second buckets,
  each a mean inside the envelope of its frames' peak (`bucketLive`): a frame a second drew a
  saw-tooth beside recorded ranges that drew a mean and its peaks, and the operator read the two
  as two kinds of chart. Bucketing within one source is not splicing, and the one-second spike
  survives as its bucket's peak.
- **An axis ends on a round figure and ticks at its quarters.** Fitted to the data, recharts split a
  3.1 MB/s peak into 781.3 KB/s steps, and a container idling at 0.2% drew five ticks that all read
  "0%". A container's charts take `byteScale` and `cpuScale` (`lib/container-usage.ts`): byte
  tops whose quarters print as whole figures, a processor axis of at least 1% that grows by the
  hundred past one core, headroom so a peak never sits on the top rule. A percentage tick keeps the
  decimals it needs and drops the zeros it does not ("0.25%", "50%").
- The hovered instant lives outside React, as a timestamp rather than a row index.
- **One formatter decides how a series prints**, and it is `seriesFormat` in `metric-chart.tsx`. The
  axis gutter, the tooltip and the legend each used to carry their own
  `unit === "%" ? \`${v}${unit}\` : format(v)` fallback, which concatenates the raw double: every
  percentage chart without an explicit `format` reported "91.25299841560013%" beside a mean that
  *was* rounded. A percentage gets one decimal — not zero, because pressure and steal live between 0
  and 5, and not variable-by-magnitude, because a column whose precision changes per row cannot be
  read down.
- An **area fill is a vertical ramp**, not a flat wash. At a constant opacity the ink is heaviest
  along the axis — furthest from the line the reader is following — and two overlapping areas mix
  into a third colour belonging to neither series. Stacked series keep a solid fill: there the block
  *is* the quantity.
- A chart's `height` is a **floor**, not a fixed size. Panels in a row stretch to the tallest of
  them, and a fixed plot puts the surplus between the chart and its legend as a band of nothing.
- **A scale fitted a little above a limit splits into unround steps** (143 / 286 / 429 MB), so
  `yTicks` on `MetricChart` and `ChartPanel` names the ticks. A container's memory chart is scaled to
  its limit and ticks at the limit's quarters, so the top tick names the limit — on Docker's
  container page and on a project's Runtime alike, since both take `useContainerScales`.
- **A series' hue is the measurement's, not the chart's position.** A project's Runtime draws
  processor in `--chart-1`, memory in `--chart-4`, received and read in `--chart-2`, sent in
  `--chart-5`, written in `--chart-4` and processes in `--chart-5`, so five charts that were all one
  blue read apart at a glance; a chart's reading now in its head carries the same 2×10 key the
  legend does. The host pages take the five readings' hues from one map (`HUE` in
  `components/overview/readings.tsx`) — processor `--chart-1`, memory `--chart-2`, load
  `--chart-3`, network `--chart-5`, disks `--chart-4` — and Metrics draws every line of a
  measurement in it, so Pressure's CPU, memory and I/O lines are the three tiles' colours; a
  chart's second line takes the hue furthest from its first (out `--chart-2` against in, write
  `--chart-3` against read).
- **A reading's last hour in a deployment page's `StatTile` is `TileTrend`** (`sparkline.tsx`), the
  one shape for it: the tile's full width, 36px tall, rising once (§11 *arrived*). Nothing is drawn
  below two points, which is not yet a shape, nor for a series that never moves on a scale of its
  own — scaled to its own maximum a flat line fills the band and reads as a full meter — and the
  tile's trend band then collapses rather than leave a gap. With a fixed `max` a flat line sits at
  its level and says so. Its colour is a series colour, never a status one: a failing share is
  `--chart-3`, because `--destructive` on a line says the line itself is an error (§3).

## 11. Motion says one of four things

Before 0.6.7 the only shared motion in the product was whatever Radix and `tw-animate-css` happened
to ship: a `transition-colors` here, an `animate-pulse` there, and no answer to "what should this
look like while it is happening". Four names in `@theme`, and no more — three of them are about a
reading, and the fourth exists because 0.6.7 gave the product one navigation that moves between
levels rather than between pages:

| Token | Says | Spent on |
| --- | --- | --- |
| `animate-breathe` | this reading is live | A halo breathing out of a `StatusDot`, at an amplitude low enough to read as "still arriving" and never as an alarm. |
| `animate-rise` | this was not here a moment ago | Opacity and four pixels, once, on arrival: a panel that appears, a disclosure that opens, a sparkline whose data landed. |
| `animate-sweep` | this is working, and cannot say how far along | An indeterminate bar for a pull or a prune, where a spinner in the corner of a wide panel is too small to be the answer. |
| `animate-drill` | you crossed a level | `rise` turned sideways, for the one place where a list is replaced by a list: the sidebar going into a section and back out. The direction *is* the message — in arrives from the right, out from the left — so the distance is `--drill-from` at the call site, not in the keyframe, and a panel that merely re-rendered does not move at all. |

**`rise` is expected wherever a fetch settles**, not reserved for special occasions. The Overview is
the reference: the page rises once when its first snapshot lands; each sparkline when its hour of
history arrives; the activity list when its events arrive; each service figure when its own poll
settles (swap the figure's `key` between skeleton and value so it remounts). Hover is colour only —
`transition-colors` on a wash or a border — and a meter eases its width. Nothing translates, scales,
glows or casts a shadow to say it was hovered or pressed; the one `group-hover:translate` the product
had was removed from the Overview's service arrow in 0.6.7.

`animate-pulse` is not one of them. It dims the element itself, which on a table of twenty running
containers reads as twenty things blinking for attention — `breathe` leaves the dot constant and
moves only the air around it, so the column still scans as "all green" at a glance.

**Live is a claim about the data, not a decoration.** `StatusDot`'s `live` prop belongs to a row fed
by an open socket. A polled figure is not live, and marking it so is the interface lying about how
fresh its numbers are.

Nothing here needs a `motion-reduce:` guard. The rule lives once at the root of `globals.css` and
collapses every animation's *duration* rather than cancelling it, so a keyframe that would otherwise
never reach its final frame still ends up there. A *delay* is the one exception, because the rule does
not touch it: the account menu's rows `rise` in a stagger from the row nearest its card, and that
stagger is `motion-safe:[animation-delay:…]` so a reduced-motion reader gets every row at once rather
than rows popping in one after another.

### Motion that arrived with a library

The deployment section brings in registry components — Magic UI's `ui/animated-beam`,
`ui/border-beam`, `ui/blur-fade`, `ui/number-ticker` and `ui/confetti`, and Motion
Primitives' `ui/text-shimmer` — each rewritten onto the tokens and each saying one of the four things
above:

- *arrived* — `BlurFade` staggers the fleet's cards by a beat each, the Overview's blocks by
  0.04s, the Insights facets, and the first screen of the Git branch graph's rows while its lanes
  draw down beside them; `ChoiceRow` given its `index` staggers every lit list the same way, capped
  at twelve so a long list does not spend a second arriving, and `ChoiceCard` staggers a grid by its
  `index`, a beat each and uncapped. `NumberTicker` counts a figure up to its value once it lands:
  the fleet's live and build-slot figures, the
  Overview's requests, the delivery insights, Automation's
  revisions awaiting review and alerts firing, the live usage tiles, the readings on
  Packages, System users and the audit log, and every lens's readings (`ReadingTile`) — the logs
  page's, a service page's and Security's alike. A figure that follows a
  draft as it is typed — Build's and Runtime's settings readings — does not count, because it would
  count again on every keystroke; it rises once when it lands instead;
- *live* — `AnimatedBeam`'s pulse on a line, `BorderBeam` running around anything whose work is in
  flight, and `TextShimmer` lighting the word for what it is doing, are `breathe` for a link, a
  frame and a word: a reading that is happening now. The beam reaches a row through
  `ChoiceRow busy` and a card through `ProductCard working`, so it is one mark for one meaning
  wherever it lands — a project card, a run row and the build console while a run is going (the
  fleet's in-progress rows carry the sweeping release path and the lit stage instead); the
  in-flight card on the Overview; a channel while its test is out and a
  credential while its probe runs; a candidate service during a release, a backup job taking a
  backup (on /backups as well as on a deployment's pages, with the protection picture's wires
  pulsing and its middle naming the job), a game command waiting on its reply; a webhook whose delivery's run is building, a
  schedule firing, a preview environment building; a variable being rotated, a linked database
  being tested, an engine being started from quick setup; and, on `/deploy/new`, a repository or
  an image being inspected. The shimmer lights the stage a release is at and the present
  participles that stand for a wait — *Sending test…*, *Testing…*, *Pausing…*, *Candidate for
  release #14*, *deploying #14*, *running now*;
- *once* — `Confetti` fires only when a release goes live in front of the reader — on the run page,
  and on the Overview when a run watched there from start to finish ends in success — never on
  arrival, and never for a Stop, which takes the release down.

`animate-sweep` is spent the same way, on the three waits in the section that cannot say how far
along they are: a credential's test while the server tries it, the website preview while the site
paints behind an invisible frame, and the build console before its first line. The preview's rise
sits on a wrapper round the frame, because the keyframe ends on `transform: none` and cancelled the
scale that shrinks a desktop-width page into the tile.

**`hooks/use-arrivals.ts` is *arrived* for a polled list, in the tokens rather than a library.** A
new request, a container event, a delivery or a player who has just joined used to appear in its
list with nothing to say it had not been there a moment ago, so a list that gained a row read
exactly like one that had merely re-rendered; only the run transcript let its new lines rise.
`useArrivals(keys)` returns the keys that were not in the list the last time it changed, and those
rows take `animate-rise` once — the live request rows, the lifecycle feed's events, a channel's and
a webhook's deliveries, a game's players, the audit trail's entries. It is empty on the first render, because the page's own
rise covers what arrived with it and forty rows rising at once are not forty arrivals, and its
answer is held until the keys change again, so a re-render halfway through a rise does not cut it
short. A list whose rows already stagger in on mount — the runs, keyed by id — does not take it as
well, which would draw one arrival twice.

The dashboard's own restarts and upgrades use the same three and nothing new: `BorderBeam` runs
around the transcript console and around the Restart or Rebuild card while that run is in flight,
the request path's wires pulse while this tab's own requests go down them and stand still and
amber while a restart replaces the containers,
`TextShimmer` lights the stage it is at (`components/run-phases.tsx`, drawn with the release path's
own `Segment`), and the transcript's new lines *arrive* — a poll's forty lines are let out a few a
frame, each taking `animate-rise` once, so a live log reads line by line instead of jumping; scrolled
up, searching or with reduced motion they are simply drawn. A deployment's build console draws its
lines through the same painter and the same drip (`components/transcript-line.tsx`), so the two
consoles cannot drift into two answers to what a live log looks like. The version history's
timeline took its layout from Aceternity's and Magic UI's timelines — the sticky label, the rail —
and not their scroll-driven gradient beam, which is a motion this section does not have.

Magic UI's `animated-circular-progress-bar` was tried beside the release path and removed: a ring
saying "100%" next to a header saying "Ready" was the same fact twice, and the timeline now shows how
far a run is by how much of the bar has coloured. Its `safari` device mock was evaluated for the
website preview and not adopted — its chrome is drawn for a hero, and at tile size the address bar's
text is too small to read — so the preview draws its own strip and shrinks a desktop-width frame.

The redesign of the whole section in September 2026 measured it against Magic UI's registry again,
fetched live a component at a time, and took nothing new. `animated-list` reveals items it already
holds on a timer, which is an arrival made up — its real meaning is `useArrivals`. `code-comparison`
needs shiki and next-themes, and there is no theme to switch (§1); a release comparison keeps its
field list in the git hues, and a text diff is the file manager's `diff-view`. `file-tree` brings
Radix's accordion and lucide into a product that already draws a file as what it is
(`files/file-icon.tsx`). `terminal` types scripted lines that claim a liveness they do not have,
beside a real console. `animated-circular-progress-bar` stays out for the reason above, and every
proportion in the section is already a figure and a `Meter`. `progressive-blur` is stacked
`backdrop-filter`, which is glass (§15), where `.scroll-affordance` already says "more past here"
with ground; `scroll-progress` is a gradient bar tied to the window's scroll in a shell that
scrolls an inner container, and reading progress is none of the four meanings. `avatar-circles` is
round faces in white rings with a filled "+N" circle; an account's circular avatar is an identity
exception, but the filled count still violates §4. `pulsating-button` glows a command at rest, and §3
never lets a command be a state. `dock` magnifies on hover and blurs behind itself;
`orbiting-circles` and `ripple` are perpetual decoration, beside wires that draw the real mechanism and a `StatusDot` that
already breathes. `shine-border` and `magic-card` are `BorderBeam` and `SpotlightBorder` already,
and `animated-shiny-text` is `TextShimmer`. The marquee, the globe and the dotted map (which would
need a GeoIP database and would fabricate the rest), the device mocks, the lens, the highlighter,
the glyph matrix, the flickering grid, particles, patterns and the aurora, sparkle, hyper and
morphing texts each sell a gradient, a glow or a movement this system does not have. What the pass
took instead was more of what was already here, on more of the section: the ticker, the border
beam, the fade, and wire marks that draw the products they connect.

Every one of them honours `prefers-reduced-motion` in JavaScript, because the root CSS rule cannot
reach a JavaScript-driven animation.

Database creation on `/databases/new` and `/deploy/new?source=database` uses one shared catalogue,
settings `FlowPanel`, and startup sequence (`database/connect/start.tsx`). While a container is being
provisioned, adopted or verified, the panel carries `BorderBeam` and `DatabaseProgress` draws the
release path's own sweeping `Segment`s, stage marks and `TextShimmer` through `RunPhases`. The stages
advance on API results, including a fresh ping after adoption, and a failed stage stays red for retry;
there is no estimated percentage or simulated build output. The Databases page opens the verified
connection's home; Deploy shows the masked connection string through `deploy/database-ready.tsx`.

`ui/bento-grid` was a seventh, and is gone. It drew "Start with something ready" on New project's Git
tab as cells of unequal size that were buttons; the 2026-09-20 pass removed that grid because the
source strip above it already listed the same six ways in, and the file then sat unimported for a
release while this paragraph went on describing it as shipped. A registry component with no caller is
not a component the product has — the next reader takes a sentence like that as permission to rebuild
what was deliberately deleted.

## 12. A table is a layout, not a contract

A wide table narrows by dropping columns, and that is the right answer until it is not. Past roughly
half of them the reader is no longer looking at a table — they are looking at the remains of one: a
wide first cell, a wedge of empty space, and two stubs where seven columns used to be.

**When more than half the columns would go, replace the layout instead.** The containers table was
nine columns wide and kept three on a phone, so below `xl` the same rows were drawn down the row
instead of across it. The test of that replacement is that **nothing was dropped to achieve it**:
the image, the ports, both live readings and the issue count are all present, because a phone is
where somebody checks whether the thing they just deployed is alive and every one of those is part
of that answer.

**The breakpoint is where the table stops fitting, not where the viewport stops being wide.** That
one swapped at `xl` (1280) rather than `lg` (1024), because the sidebar takes 256px of it: at 1024 the
table appeared already scrolling inside its own panel, with Issues and the row's actions past the
right edge — a table that arrives broken.

**And a table whose every row is a place to go is not a table at all.** Since 2026-09-23 the
containers, volumes, networks and stacks lists are cards at every width — the argument
`git/repo-card.tsx` made for checkouts, and §16's for anything you take: each row opens a page or a
panel, so it carries the lit edge. `components/docker/container-card.tsx` keeps both halves of the
paragraphs above: from `xl` its readings sit beside the name in fixed measures, each naming itself
because there is no header over it; below, they go beneath the name at the card's full width. Which
shape is drawn is chosen once by the page (`useMediaQuery`) rather than by `hidden`/`xl:block` twins,
because a reading that exists in a hidden copy is two answers to every query a test or a screen
reader makes. The list around the cards is a plain panel — a frame around framed cards is two nested
frames, which is the stacking this section refuses. System users took the same argument in 0.7.0:
every account opened its keys, so the eight-column table became cards (`AccountCard` on the page),
their groups, last sign-in, keys and state beside the name from `lg` and beneath it below. The audit
log is the counter-example on the next page of the same section: an entry opens nothing, so its
trail stays a table. The images were cards from 2026-09-23 and became a table again on 2026-10-08
at the operator's request, on the Processes, Packages and Services tables' precedent: a row that
opens a sheet but is read down its columns — which image is largest, oldest, unused or behind — is
a table of readings with a destination on its name, and it keeps its frame (§2).

**The deployment section's rows took the same rule, and it moved the breakpoint twice more.** A run
(`deploy/run-row.tsx`), a runtime service and a channel set their readings beside the name in fixed
measures where there is room and under it where there is not, chosen once with a media query — and
the width they need is measured inside the project's shell, not the window: at 1280 the content
column beside the project's navigation is about 968px, which left a service's name 150px beside
five readings, so the wide shape of the Overview's runs-beside-previews starts at `2xl`, as does
the build console's rail of stages beside the transcript. Inside a settings page the column is
narrower still, because the fields are held to 48rem however wide the window is, so there the
window is the wrong thing to ask at all: `settings/use-column-width.ts` measures the column a list
is drawn in, before its first paint, and the variables and linked databases (from 600px), the
storage page's mounts (from 480px) and Automation's webhooks, schedules, approvals and previews
(from 560px) choose beside-or-under from that — still once, still drawn once.

**Wide, a lit card is one line** — the Notifications channel card is the reference. The mark, the
name over one truncating line of its secondary facts joined with " · ", and at its far end the state
word and the outcome strip, the strip in a fixed `w-27.5` column so that down a list the strips are
one column and the states end on one edge, then the arrow and the verbs on the card's middle. A
second band under the name — a strip, a preview address, a run's trigger — left the card two or
three lines tall with the verbs level with the name over an empty corner, and 0.7.1 took it off
every deployment card that had one: webhooks, schedules, approvals and previews; linked databases,
the gate evidence, the live mounts and a variable's linked database; a backup job (`JobCard`, which
measures its own width because it is drawn on /backups as well); a runtime service (one line from
1280, its ports from 1536), a domain (from 1024), a mount and a dependency; the fleet's list rows
(from `lg`, in columns two lines tall so the row keeps the height of its name and source line with
both lines spent, traffic joining at `xl`), the runs in flight (from `sm`) and the archive (from `sm`).
Narrow, each keeps one short second line, as the channel card does on a phone; a fact that fitted
neither line and is not needed to recognise or judge the card went to the page or sheet it opens.
A revealed variable value and an isolation note on a preview keep their line, because one is a
control and the other is the thing to do. A row that
only reflows rather than rearranging, a domain or a mount editor, uses a container query
(`@container`, `@min-[40rem]` for a domain and `@min-[36rem]` for a mount) and draws nothing twice
by construction.

A row drawn this way is a **click target, not a control**. `role="button"` on the wrapper is the
obvious way to make a whole card pressable and is wrong: an ARIA button takes its accessible name
from its contents, so a screen reader announces the entire row — readings, ports, and the label of
the menu button nested inside it — as one control's name. The row's title is a real `<button>`; the
surrounding click handler is a convenience for the pointer that skips any press landing on a control
of its own, exactly as `TableRow`'s `onActivate` does.

**A title column caps its width, and the title binds itself to that cap.** `truncate` on the title
is not enough on its own: a `<button>` is inline-block, so it sizes to its text and a process whose
name is the whole of a headless Chrome's argv paints across Owner, State and CPU rather than
ellipsing. `RowLink` carries `max-w-full min-w-0` for that reason — the first binds it to the
`max-w-[Nrem] min-w-0` wrapper every title cell is built from, the second lets it shrink where the
title is laid out with flex beside a tag. Anything else that sizes to its content in a capped cell
needs the same pair.

## 13. A verb is a word

An icon-only control is legible when its shape is universal and it is pressed constantly — play,
pause, stop, restart. It is not legible the first time, and `ArrowCircleUp` has never meant "pull a
newer image and rebuild this container with the same settings" to anybody who did not already know.

The rule that fell out of the Docker pass, and which generalises:

- **Two or three verbs inline, as icons.** The ones pressed daily, whose glyphs are conventional.
- **Everything else behind one overflow menu**, one word to a line, with no sentence under it. The
  menu is not where things are hidden — it is where a verb gets its name, which is the only form
  most of them are usable in. A verb that needs a sentence to be understood needs a better word, or
  a confirmation that carries the sentence; a menu of two-line items read as a wall of captions
  (§5), and every item cost twice the height it earned. Rows in a menu that carry a *fact* beside
  the word — a place's path, a saved command, a connection's host — keep it on the same line, muted
  and truncated, never underneath.
- **The verbs themselves are declared once**, as data, and a surface decides only how many it has
  room to draw. `components/docker/container-actions.tsx` is the pattern: three surfaces used to hold
  three different answers to "what can I do to this container", and they disagreed about which
  capability each needed. `components/verbs.tsx` is that pattern made shareable — a `Verb`, drawn
  by `VerbActions` in a row (inline icons and one menu), `VerbBar` in a sheet or a detail page's
  header (named buttons and
  the same menu) and `VerbMenu` alone in a header — and the process, PM2, unit, timer and cron rows
  all draw theirs through it, so four kinds of row did not arrive at four menus. A process row keeps
  nothing inline: the daily verb on a process table is reading it, and a stop glyph beside four
  hundred rows is four hundred invitations to end something by mis-click, so Terminate and Kill are
  named buttons in the sheet and words in the row's menu.

The deployment section declares its two sets the same way. A project's verbs are declared once in
`deploy/project-verbs.tsx` and drawn by the project context row — whose one brand command,
`projectCommand`, is the first of View, Start, Deploy and Redeploy the list holds — by a fleet
card's menu and by a fleet row, so a card and the context row cannot disagree about what can be done to
a project. A run's and its release's are declared once in `deploy/run-verbs.tsx` and drawn by a
Deployments row's menu and by the run page's header, which is what keeps a finished run from
being a dead end. A long menu is grouped: a `Verb` may name its `group` — Running, Building, Project,
*Release #4* — and `VerbMenu` draws an eyebrow and a separator where the group changes, because
eleven words in a row are a wall and the same eleven under three names are three short lists.
Cancelling a run is not a danger verb, any more than Stop is: it is undone by deploying again, and
the danger rule in front of it would have cut its group in two.

A choice **closes the menu**. `VerbMenu` lets Radix's default close run from 0.6.7: the item's
`preventDefault`, copied from the Docker actions menu, had kept every menu drawn through it open after
a choice, so the verb ran behind a menu that was still asking. Where a surface draws more than one
menu — a sheet with its own verbs above a table of rows that each have theirs — `menuLabel` names
each ellipsis after its row, so a screen reader and a test can tell them apart.

A control that changes state should also **say that it is changing**. A Docker stop takes ten seconds
to honour while the socket keeps reporting the old state, so the row answers the press by sitting
still and then jumping — indistinguishable from a button that did not work, and the reason anybody
presses restart twice. The row carries the present participle (`Stopping…`) until the change lands.

## 14. A header is its title

**A surface's header carries its name and its actions. It does not carry a picture of itself.**

Every `PanelHeader`, `Modal`, `SidePanel` and `Section` used to open with a 28px brand-tinted plot
and a glyph inside it. On a page of six panels that is six brand marks down the left edge, each one
the same weight as the one control the reader is actually meant to press — and none of them said
anything the word beside them did not. `Servers` in front of "Filesystems", `Cpu` in front of
"Processor", `ShieldOff` in front of "Health": a glyph is a guess at a word the header has already
spelled out. Where the picture and the title disagreed, the title was the one that was right.

The prop is gone from all four components, so this one is enforced by the type checker rather than by
judgement: there is no `icon` to pass. `ChartPanel` forwarded one too, and does not any more. Nor does
`StatTile`: the 12px glyph it drew before its eyebrow was the same guess at a smaller size — `Cpu` in
front of "CPU" is the label twice — and twenty-eight tiles across the product carried one.

`Notice` kept the last one, and it is gone too. A notice's glyph is allowed — a severity is a thing a
shape can say, which is the exception below — but the 28px tinted square around it never was. On a
toned banner it was the fourth tinted object in a box that needed one: a wash, a rule, a plate, and
the glyph on the plate, all the same hue, to say that a certificate was fine. The glyph now stands on
the banner's own ground at the size of the title's line, and the banner is `rounded-lg` — §9's fence
step, beside `Group`, which is what it is — rather than the block step beside `Panel`, which it is
not. **And a `Notice` is for what the reader has to act on.** `/deploy/new`'s public address drew
three: one saying HTTPS was ready, which repeated the field's own hint word for word; one saying a
certificate would be issued, which nobody has to do anything about; and one saying automatic HTTPS
needed attention, which is the only one of the three that asks for a decision. The restart record on
`/dashboard/configuration` had four more — amber "do not reload" while it ran, green "the dashboard
has moved", amber "worth knowing", red "what went wrong" — and none asked for a decision either: the
state is now the mark beside the headline, the guidance one line of hint, and the error the line of
the transcript that failed, washed where it sits. A state is a `Status`
beside the thing it is a state of; what will happen by itself is a line of hint; a tinted box is for
the third case.

What the header has instead: the title at `text-title` (§8), the header's own ground and hairline
doing the separating, and — where there is something to say about state — a `Status` in the actions.
That was always the honest mark, because its colour is a reading rather than a decoration.

**Icons did not go away; decorative icons did.** A glyph stays wherever it *is* the message:

- a `Status` verdict, a `Notice` severity, an `EmptyState`'s mark — the shape carries a tone the words
  would need a sentence for;
- a verb on a button or in a row's actions, under the rules in §13;
- **wayfinding** — the sidebar entry, the overview's module tiles, the security page's area rows.
  These are the same glyph the reader is about to click through to, held steady across the product so
  the eye can find "Docker" without reading. A header is not wayfinding: you are already there.

**A wayfinding mark may carry its own colour, and only a wayfinding mark.** A repository list is
scanned rather than read, and after the name the language is what decides which of forty rows is the
one — so `LanguageMark` draws the language's logo in front of its word. In `text-muted-foreground`
that glyph was the same grey as the forty words around it: a mark doing the half of its job that
costs pixels and none of the half that saves a read, because a column of twenty identical grey
glyphs is a texture and the reader goes back to reading the words. The hue is not this product's to
choose — it is GitHub Linguist's, the colour the same repository carries on the site the list was
fetched from, which is the argument §3 makes for `--brand` applied to somebody else's mark. The
lightness *is* ours, and it is one rule rather than twenty judgements: every `--language-*` in
`globals.css` is the logo's hue and chroma at L 0.72, the rung `--tag-*` already sits on, because
Linguist's values were picked for a white page and four of them (Lua's navy, Ruby's oxblood,
Markdown's ink, C's grey) are invisible on a 0.16 ground. The rule is written out as thirty literal
`oklch()` values carrying their source hex in a comment rather than stated once as
`oklch(from <hex> …)`: relative colour syntax is the one modern colour function Lightning CSS cannot
downlevel, so those thirty would have been the only tokens in the file shipping without a fallback,
below the floor Next's default browserslist target declares. Deleting the lightness dimension has a
price, and it is paid by the pairs Linguist separated by lightness alone — Lua and Markdown come out
as the same blue. The glyph shapes and the word beside them still tell those two rows apart, and a
floor high enough to be legible on this ground collapses them either way, so the rule stands as
written. This
does not extend to the marks a reader is choosing *between*: the four source kinds on `/deploy/new`
stay muted with the current one in `--brand`, because there the colour is saying which one you are
on (§3), and twenty hues in a row of four would be saying nothing.

**A product is not a kind, and it is drawn as itself.** The template catalogue is sixty-two products
the reader already knows by their marks — n8n, Grafana, Redis — and set as sixty-two names in one grey
face it was a wall of words, with the language marks on the Git tab the only colour anywhere in the
flow. `components/product-logo.tsx` draws each product's *own* logo, in its own colours, on the recessed
tile `ProjectMark` uses for a deployment's favicon, so a template and the project it becomes are drawn
the same way: every template card, each database as the product that answered — its driver's mark, a
flavour's own where the server is one (TimescaleDB, CockroachDB, TiDB, FerretDB), and the mark of a
server the inventory sees and nothing here opens (Kafka, Cassandra, etcd) —, the images on the Images
tab (by the last segment of the reference, Docker's whale for the rest), and the settings panel's
header. The
colour lives in the artwork rather than in a token, which is the same argument as the language marks
— the hue is not this product's to choose — taken one step further: the files are bundled in
`public/logos/` (the page's `img-src` is its own origin, and §8's locked-down networks cannot reach a
CDN), picked in the variant drawn for a dark ground, with their licences in `public/logos/NOTICE`. The
one file whose own colour failed that ground, MySQL's navy dolphin, was lifted to the L 0.72 rung the
`--language-*` tokens sit on. The tile is not an icon plate — it carries no tint of this product's and
sits beside a card's words rather than in front of a header's title — and the source strip still
stays muted, because four *kinds* are not four products. A repository row on the Git tab carries its
owner's picture on the same reasoning: the face is the account, which a glyph could only guess at.

The same marks carry into Docker and Databases, because they are the same products. A container,
an image and a stack's service are drawn as the product their image is (`imageProduct` reads the
last segment of the reference; anything it cannot name is Docker's whale). A container whose reference
names nothing — a deployment's is a bare image id — is what its image's OCI title or source label says
(`containerProduct`), since the publisher wrote those and Docker copies them onto the container; the
reference wins when it names a product, because labels are inherited from a base. A stack is drawn as its services'
products overlapping (`ProductLogos`, the way a group of avatars overlaps; Compose's own mark when
none has a logo), a volume as the product of the container that keeps its data there, and a
database connection as the product that answered, not as its driver: `EngineMark` and `EngineGlyph`
(`components/database/kit/engine-mark.tsx`) ask the engine registry, so MariaDB is not the MySQL
dolphin and Valkey is not Redis, on a fleet card, a home's identity tile, the strip's switcher, the
rail's head and every engine picker, which are one `EngineCard` (`choice-card.tsx`) rather than three
shapes that had already drifted. A flavour with no artwork of its own draws the database glyph on
the same tile and never borrows its driver's logo with its own name beside it.
Networks have no product and keep a glyph on the same tile, so their titles line up with the rest.

**What a backup covers is a product, and so is what a terminal runs.** A coverage row is drawn as
the thing it protects — a saved database as its engine, the proxy's configuration as nginx or Caddy,
a repository as git, a volume as the product of the container that keeps its data there (joined
through the container list, since the report names containers rather than images; a database
container run from a bare image id is its connection's engine), a stack as its services overlapping,
the dashboard as its own mark — and a job as the products of what it covers, with Backblaze drawn as
itself where it writes. S3 is a protocol a dozen providers speak, so the protocol alone is no one
company's, but a bucket is the provider its endpoint names — Amazon's when it names none, because
that is where the SDK sends it, Cloudflare's on `r2.cloudflarestorage.com`, MinIO's on a host
carrying the word (`destinationProduct` in `backups/marks.tsx`) — and keeps a cloud glyph when the
host names nothing; a directory on this server is a server glyph. A run's database dump is drawn as
its driver's engine, and an entry in a run's archive as its file (`FileIcon`). The terminal
draws the program in each window's foreground the same way. A thing none of these can name keeps its
kind's glyph on the same tile.

**The host is a product too.** The Overview, Metrics and the logs rail draw the machine as what it
reports itself to be — its distribution (`platformProduct`, from `/etc/os-release`'s id), its processor
(`cpuProduct`, from the model string: AMD, Intel, Arm), its hypervisor (`virtualizationProduct`: QEMU for
a KVM guest) — and a running process as the product it is (`processProduct`: `postgres` is Postgres,
`dockerd` is Docker, `node` is Node.js) in the top processes the Overview and the Metrics page share
and in every row of the live process table. Metrics draws two more of the machine's parts by
whose they are: an interface by the name its owner gives it (`interfaceProduct`: `tailscale0` is
Tailscale's, `docker0`, a `br-` bridge and a `veth` pair Docker's, an `eth0` its kind's glyph), and
a temperature by the hwmon driver that reads it (`sensorProduct`: `coretemp` is Intel's, `k10temp`,
`zenpower` and `amdgpu` AMD's, an NVMe drive or an ACPI zone nobody's). The other three Processes pages read their rows the same way: a systemd unit as
the product it runs (`unitProduct` — `postgresql@16-main.service` is Postgres, `pm2-deploy.service`
PM2, `certbot.timer` Let's Encrypt's renewal, by the unit's name with its suffix and instance
dropped, then by its first word), a PM2 application as its interpreter (`pm2Product`: Node unless
the ecosystem file says Bun or Python, a glyph for a binary), and a cron line as the program its
command starts (`programProduct`, the terminal's reader: `docker system prune` is Docker's, a
script of the operator's own keeps the clock). Each returns nothing for a name it does not know,
and the tile keeps a glyph: a Tux on an unrecognised distribution, or a guessed logo on `bash` or
`apt-daily.timer`, would be the drawing lying about the row. A reading that counts products carries
them after its words (`ProductGlyphs`): the Overview's Docker tile draws the images its running
containers are, Databases the engines its connections speak, Git the forges its checkouts push to, and the live table's Processes tile what
the machine is running. The Services page draws each busy service and each recent change as its
product beside its name, which is where its Active and Failed tiles carried their marks.
The Live page opens on the machine's identity line — the same one, with the table's cadence and cap
at its right end where Metrics keeps its range — and PM2 on PM2's own: its mark, the account, the
Node it runs, the boot hook and the last save as facts, and whether it resurrects as the verdict.

And the dashboard's own two pages draw what it is made of and reached through: the stack's three
services as Caddy, Next.js and Go, its checkout as Compose, the certificate modes as Tailscale and
Caddy (and a lock glyph for plain HTTP, which is no product), the trusted certificate's issuer as
Let's Encrypt, and the repository it updates from as GitHub. Inside a line of text a tile is a box
in the middle of a sentence, so `ProductGlyph` draws the artwork bare at the line's height — the
GitHub mark before the repository's name, Let's Encrypt's before "through Tailscale".

**What signs in is a product too, and a person is drawn as their face.** The account pages draw a
session as its browser's own mark with the system it runs on as a badge in the tile's corner — the
browser is what the reader recognises, so it is not a `ProductLogos` pair in which the second tile
covers the first — and a program that signed in (curl, Go, Python) as itself; its address as the
network it is on, with Tailscale's ranges drawn by Tailscale's mark (`lib/clients.ts`: `parseAgent`,
`networkOf`). A key is drawn as the service its name says holds it: minting one asks where it will
live, so `github-actions` is GitHub's, and `backup-cron`, whose name says nothing, keeps the key glyph
(`keyProduct`). An account is its picture, and without one its initials take a hue by the username
from `LANES` — `AuthorMark`'s argument: a users list of eight brand-blue squares was a texture, and
the same person now keeps one colour in the rail, the list and their own profile. The profile opens on
`HostIdentity` with that picture where the tile would be, which makes it the fourth page that
describes a thing the same way; a project's header is the fifth, and a deployment's run page
opens on that same header saying what the run is (`run-header.tsx`) — it had its own identity line,
a 48px tile and the duration as a 24px figure, until the operator asked for the project's compact
one. A game server's three pages add one line under that header (`GameIdentity`) with what only
the game can say — the address a player types, the edition, how full it is — and draw neither the
game nor its name again.

**The Security section draws what it watches.** All eight pages use the reading register, and
since 0.7.1 the overview, Firewall and SSH each draw a wiring picture on the page's own ground over
`wire-grid`, in the `WireNode`/`AnimatedBeam` vocabulary the deployment and Configuration pictures
speak, with the line as the state (dashed where a layer is missing, red where one is off, amber
where it works but should not be relied on, moving where it carries). The overview's
(`perimeter.tsx`) is the two ways onto the machine — the internet's through the firewall, fail2ban
and sshd, and this browser's through the allowlist to the dashboard — and it describes the layers
and their states, not the reach of every port. Above it the posture is a strip of its seven checks
(`posture-strip.tsx`), a segment each in the colour of what that check found and dashed where it
could not run, and a segment narrows the findings to its area. Findings carry their severity counts
in their head. Firewall's (`firewall-picture.tsx`) folds the rules by where they lead — in from the
internet, through the firewall, out to each admitted port drawn as the product that answers there,
or into the default (`firewall-reading.ts`, unit-tested) — and its rule table names each action in
a `--tag-*` hue down the row's edge, ending on the default as its last row. SSH's
(`ssh-picture.tsx`) draws the port, password authentication, root login and the keyed accounts as
the doors a login can take, following the draft as it is edited; it replaced four tiles that said
the same facts one at a time. Firewall's policy controls sit beside its bounded rule table; SSH
lays its directive groups out two to a row from `xl`, as Configuration does, each head over its
fields with *Edited* in `--git-modified` while it holds a staged change, and its apply bar follows
the reader and names what changed. Intrusion's jail choices carry their watched service's mark,
state and comparable readings. SSH, Firewall and
Intrusion each end on their own log — the auth log, the firewall's log, fail2ban's Activity — read
through its lens in one `Pane` under a title across the page, since a log needs the width a rail or a
half row does not have; the last day's counts join the page's one `StatGrid` rather than drawing a
second, and an attacker's address is blocked from the line it is on. Repeat offenders therefore take
Intrusion's row alone. Connections, Logins and Network combine related facts into fewer,
richer columns: a peer and its network, a service and its ports, an account and its terminal, an
interface and its kind. Their action columns are always drawn. Tools is a two-pane working surface,
with a searchable choice rail and one visible form/result, retaining every other probe's work.
The diagram and the workbench have edges for §7's reasons; tables keep theirs, and forms and
findings remain plain. `components/security/marks.tsx` supplies the inline address, `PeerIdentity`,
process and interface marks. A product is named only when it can be identified; no logo is guessed
for ufw, sshd or an unknown interface. Source choices in the firewall dialog use the same lit
`ChoiceCard` as deployment choices, with Tailscale's own mark for the tailnet.

**The proxy section draws routes, engines and authorities.** All seven pages stay in the reading
register and begin with four `StatTile` readings, two per row on phones; on Sites, what the reader
has to act on first — a failed read, nginx not running, changes on disk nginx has not loaded, with
Test config and Reload nginx — stands above them as a `Notice`. On the overview the engine
identity and service commands sit below them, with the routes in the main column and attention and
expiry in a narrower column. On an nginx host Live traffic sits between the engine line and the routes: two readings
(requests a second, open connections) each carrying its hour as a `TileTrend`, and a hint line naming
where the counters come from. Its switch is a `Switch` beside a `Status` in the panel header rather
than a button, because whether nginx is counting is a state of the engine; a switch in flight reads
"Switching on…", readings that stop keep their hour and lose their figures, and nothing on it breathes
as live, since it is a poll. A source the overview could not read is never drawn as an empty or
healthy one: its tile's hint reads "couldn't read", its panel shows the `ErrorState`, and attention
carries it as a finding whose button is Try again, so the all-clear line cannot appear over it. The
overview's context row is its age and one ghost Refresh: "Updated 14s ago" is the oldest reading on
the page, and while a refresh is out the line reads "Refreshing…" until every source has answered,
or, after twenty seconds, names the source that has not ("No answer from sites") and Refresh can be
pressed again. A status that fails after answering keeps its age in the line, since the engine
identity still draws it. The engine line's one brand command follows the unit: Reload while it
runs, Start once it is stopped or failed, with Reload beside it disabled and its reason on a tooltip
(a disabled button takes no hover, so the reason hangs on a focusable wrapper). Whether it starts at
boot is a fact on the line, a warning with an inline Start at boot where `systemctl enable` would fix
it. A failed unit is the one `Notice` the overview draws under the line — the reader has to act on it
— holding systemd's reason in words, a fold that reads the journal when opened and opens scrolled to
its newest line, and its two verbs. A start or restart the config test refuses keeps its dialog
open on the test's diagnostics as rows (level as a `Status` verdict, file:line in mono, Open at line
N), not a toast of nginx's output; the editor a row opens closes back into that dialog, with the
keyboard on the row's button. Test config is a `SidePanel` of the same rows under a verdict `Notice`
(success, warning or danger: "Valid", "Valid with 1 warning", "Fails"), with the output in a quiet
fold, Copy output and Test again in the footer beside how long ago it ran, and a conflicting server
name's claimants as rows indented under it behind a rule, "served by" and "ignored in" each with its
own button (a name taken from a shared snippet adds "server_name in" the snippet's line under the
site's); while it runs it says "Testing…" rather than keep the last verdict under a new run, and a
test that gives no verdict is the panel's `ErrorState` with Try again, never a "Fails" `Notice`. The
last test's warnings and failure stay in attention as one finding whose button, Open test, shows it.
Its routes are ordered worst first like the Sites cards, eight with "Showing 8 of N". An
administrator's route opens the site on Sites and a Docker ingress route its live TLS report; a
reader's route opens its file read-only in place rather than the site form — skeleton rows while it
is read and an `ErrorState` with Try again when it cannot be, never an empty editor — and a route with
nothing a role may open is a disabled row. A site's and a stream's card separates identity, route and named
actions into three bands: `components/proxy/route-path.tsx` gives the source and destination their
own labelled columns (stacked on phones), so a hostname and its upstream do not compete for the same
truncated line. Sites and streams use a two-column grid on wide screens and a single column on
smaller ones. The nginx or Caddy mark names a site's engine, the stream's port names its product
where known, and the TLS reading carries Let's Encrypt's mark only where the certificate path
supports it. Unknown products keep a glyph. Until streams can forward, the Streams page puts a plain
block of two numbered steps above its list — the stream module, then connecting the directory — each
with its `Status`, a line of meaning and the button that does it where the page can; the step that
needs doing carries the page's one brand command, so "Prepare a stream" stays outline, and the
connect opens a sheet showing the file before anything is written. The install step keeps the recent
installs beside its button, the list its job console says a run is reopened from. A stream card's Status is nginx's own state for it — live, not listening, shadowed, not read — and a card that is not live says why in a hint line under its route, nginx's logged error in mono beneath; the first tile counts the live streams and chips filter by state.
Every site card, the overview's route rows and a certificate's links to the sites using it open the
site's own page (`/proxy/sites/<name>`): the same marks as an identity line, its readings on the
page's ground, and its requests and errors read there in one log `Pane`, so a site's Logs verb goes to
that page rather than to a file on the Logs page. Editing stays with the card's verbs. The overview
ends on the engine's own log, a `Pane` across the page under the two columns.

Certificates has a searchable inventory beside renewal and DNS management. Each inventory card opens
its details — all names, dates, the full path and links to the sites using it — so it takes the lit
edge; its issuer, expiry and lifetime meter remain on the card. An unreadable certificate carries a
short verdict on the card and its complete error in the detail sheet; unavailable dates and signing
status stay unknown, and it draws no invented lifetime. A test certificate — a staging authority's —
draws no issuer mark, reads "test certificate" and a red meter whatever its days, and carries its
sentence and its one verb, the real issuance, on a line of its own under the card.
The panel's command is split rather than doubled: "Issue certificate" is Let's Encrypt on the brand
face, and a chevron joined to it opens every other way to get one — the local CA, self-signed, a
signing request — as words in a menu; without certbot the menu is the whole command. What the server
makes itself is said where it is read: the local CA's certificates carry a `local CA` Tag, and the
details of one nothing trusts by default say where it is trusted in a hint line, not a Notice. The
rail adds Signing requests at its head only while one waits — what the operator owes an answer to —
and a Local CA panel after DNS providers, which is an administrator's offer to create one and nothing
to a reader until it exists.
A watched domain opens a live report and preserves its nonstandard port. Password files, access
lists and watched-domain setup use the deployment settings' sections, each head over its fields; an
access list is a row you read (its addresses as mono tags, its sites as links, its include line with
Copy) with Edit inline and Delete in its menu. Certificate renewal lineages and
DNS providers remain readings with their own actions, laid out to fit the management column; every
run certbot made, the timer's included, follows the two columns as Renewals, a log `Pane` across the
page's width, which a log needs and the management column does not have. The
renewal column opens on its recent certbot runs (in the body, where they wrap, not in the narrow
column's header), then what the timer's last run did — a failed run is a danger Notice naming each
certificate and certbot's reason, a passing run whose hook failed a warning Notice with the hook's
own words, each with Run now and Show log; otherwise a Status and the next run — then the "Reload
nginx after every renewal" OptionRow; a lineage's last failure is a line in its row, its webroot
folders wrapping mono paths rather than tags, and a certain next failure a warning Notice. The TLS
report keeps findings, protocol checks and HTTP readings beside the live certificate and its
vertical chain; long header values wrap instead of hiding the verdict. Its scan field is one input
group, the address and its port, with a hint naming what will be scanned and a native datalist of
known names; a scan in flight is a line with its elapsed time and an outline Cancel. Listening sockets stay a
table of readings, with fixed endpoint, application, reach and action columns and a stacked phone
layout chosen once by `useMediaQuery`; the three named columns sort from their headings, which carry
`aria-sort`, and a phone gets the same orders as one Sort menu. The application cell is the owner
drawn as its product (a container as its image's), its name with a "This dashboard" `Tag` on the
dashboard's own, how it runs as one hint line, then the command and the account; the owner's page is
the row's first inline verb. Grouped by application, one owner's sockets are a line whose name is the
disclosure button, unfolding its sockets indented beneath it. How old the list is sits in the panel header beside Refresh, Pause and Export; a failed
poll is a warning `Notice` over the rows it kept. A socket first seen in the last day carries a `New`
tag beside its protocol, a property of the row rather than a state. Below the table, Changes is a
plain panel of lines grouped under day headings — the minute, the change as a `Status` word coloured
as the table colours that socket, the port, its addresses, the program and where it answered — with
its window as `Segments` in the header and the reach chips under it. Tables retain their scrolling
boundary; forms and sections remain plain.
`tests/browser/proxy-ui.spec.ts` covers all six populated pages at 390, 1280 and 1720, detail
navigation, site-kind choices and read-only access; `proxy-engine-overview.spec.ts` covers the
overview's failure states, its freshness and Refresh, its routes by role and the engine controls,
including a stopped, failed, masked and boot-disabled unit, a refused start, restart or reload, and
the config test panel; `proxy-insights.spec.ts` covers Live traffic, its switch and a phone.

**What a host has installed, who is on it and what they changed are products too.** Packages
draws a package as the software its name says it is (`packageProduct`, `components/packages/marks.tsx`:
`postgresql-16` and `libpq5` are PostgreSQL, `python3-requests` Python, `linux-image-*` the kernel's
Tux, `python3-certbot-nginx` Let's Encrypt) and a name that says nothing as its archive section's
glyph on the same tile (`sectionGlyph`: a library as layers, a tool as a wrench), so `libc6` is never
drawn as a guess. The page opens on the host's identity line with its distribution as the mark and the
manager beside the name, an upgrade's origin is the archive that published it (`originProduct`:
Ubuntu's security pocket, `apt.postgresql.org`, a `pgdg16` repository), and the version an upgrade
lands on keeps the part it shares with the installed one stepped back and the part it changes in ink
(`VersionTo`, cut at the last field both versions end), amber only for a security fix. System users
draws a person as their initials in the hue their name has everywhere else — the host's `ion` and the
dashboard's `ion` are one colour — and an account a package made for its daemon as the product that
runs under it (`accountProduct`, `components/system-users/marks.tsx`: `postgres`, `redis`,
`gitlab-runner`), with `www-data`, which is nginx's on one host and Apache's on the next, left as a
glyph; an administrator's group is drawn first behind a shield and `docker` behind Docker's mark,
because a member of either can become root. The audit log draws each entry with the mark of the part
of the product it touched (`auditSection`, `components/audit/marks.tsx`: Docker's whale, Git's and
GitHub's marks, PM2's, fail2ban's, the terminal's, Let's Encrypt's for an issued or renewed
certificate, the dashboard's own for its settings, and the sidebar's glyph for every section that is
no product), the action's first word in its `LANES` hue the way the log console draws a program, the
route's method and path in the request log's hues, and who acted as their face — an API key's use
with a key in the tile's corner, a webhook as the webhook's mark and the dashboard's reconcilers as
the dashboard.

**A project is drawn as its website, else as what it is.** `ProjectMark` tries three things in
order, all on `ProductLogo`'s tile so a card does not change shape when an icon arrives: the icon
the site declares (read through the dashboard's origin), then the product the project is
(`projectProduct` — the template, the image's product, Compose, the framework detection recognised,
else the recipe's language, Docker for a Dockerfile, nginx for a static site), then its workload's
glyph (`WORKLOAD_GLYPH`: a globe for a site, a box for an image or a service, layers for a stack,
servers for a game). A repository's own site says what it is and not what it runs on, so there the
favicon carries the framework or language as a badge in the corner, the session list's
browser-over-system shape; a template's or an image's favicon already is its product, and the badge
would be one logo twice. It is one tile rather than `ProductLogos`, because a column of titles has
to line up — a stack's services are drawn after its words instead. The fleet card, the rail's
scope head, the project identity line and the notification picture's source all draw it; the archive
draws the product alone, at a step of opacity — a project at rest, the way Backups dims a paused
job.

**Where a project comes from, and what builds it, are products too.** A Git remote, a clone URL, a
registry host or an image reference is drawn as the forge or registry it names (`hostProduct`:
GitHub, `ghcr.io` included, GitLab, Bitbucket, Codeberg, Gitea, Forgejo, Docker Hub, Quay, Harbor,
Azure, AWS and Google Cloud's registries, and a self-hosted host whose name carries one of those
words), falling back to git's or Docker's own mark — on a fleet card's source line, the run page's
header, and as the field is typed on `/deploy/new`'s Clone URL and Image reference and the
credential sheet's Host. A saved credential is the host it signs in to, an SSH key
with a key in the tile's corner and a GitHub App credential as the installed account's face with
GitHub's there. What is pasted into a credential's secret is read for what it says about itself
(`lib/secrets.ts`): providers prefix their tokens so that secret scanners can find them, so
`glpat-` is named a GitLab token the moment it is pasted and a mismatch with the host is said
before it is saved, and a key's first line names its format — which is how the public half of a key
pair, pasted where the private half belongs, is caught. A build is its framework
(`frameworkProduct` over detection's ids — Next.js, Django, Laravel, Spring Boot), else its
recipe's language (`recipeProduct`; Bun rather than Node.js when that is what runs it), else what
its method is (`buildMethodProduct`), and a package manager is itself. A GitHub App that is not
connected is GitHub's own tile, and a GitHub CLI that is not signed in the terminal's tile with
GitHub's mark in its corner, `ClientMark`'s shape.

**Where an outcome goes, and who came asking, are products.** A notification channel is its service
(`channelProduct`): Discord, Slack and Telegram as themselves, a signed webhook as the receiver its
URL's host names (`webhookProduct`: n8n, ntfy, Gotify, Home Assistant, Healthchecks, Uptime Kuma)
and the webhook's own mark otherwise, e-mail as an envelope or as the mail service its server's host
names with an envelope in the corner; a paused channel is drawn greyed. A request's client is
`ClientMark` again, now shared with the account pages (`components/client-mark.tsx`), and a crawler
is the search engine it belongs to — Googlebot is Google, bingbot is Bing — or a bug glyph when
nothing names it (`agentProduct`); a referrer is the site it came from (`refererProduct`: Google in
every country domain, GitHub, Hacker News, Reddit, X, LinkedIn); an address is the network it is on
(`NETWORK_GLYPH`: Tailscale's mark, this server, the local network, the internet).

**A variable is the service that holds it, and a certificate its issuer.** `variableProduct` reads a
service word anywhere in an environment name — `STRIPE_SECRET_KEY` is Stripe's, `SENTRY_DSN`
Sentry's — and otherwise the first word, which is a framework's prefix (`NEXT_PUBLIC_`, `VITE_`);
`DATABASE_URL` names nothing and keeps the key glyph, and a typed reference is drawn as the database
it points at, in its engine's colours. A prefix two names share takes a `LANES` hue, so a family of
`STRIPE_*` variables is found as one. A domain's certificate is drawn as its issuer — Let's Encrypt
from the intermediate's name (`issuerProduct`), read off the certificate the route actually matched
and never guessed from how the domain is owned. A running service is its image's product, else the
project's (`deploy/service-product.ts`); a mount is the file manager's folder in the colour the
operator gave it, or its volume's product; and a Docker event on a project's Logs page is drawn on
its project's or image's tile with what happened as a toned badge in the corner.

**And a person is their face, or their initials in their hue.** `RunActorMark` draws who started a
run: you, as your own picture; anyone else as `InitialsMark` in the `LANES` hue their name has in
the rail — another administrator's picture would cost a users request per list, and the hue is the
same colour they are everywhere else; a push as the host its remote names, a pull request's preview
as its forge, and a schedule, an API call or the system as a glyph on the same tile. `ForgeFace`
draws a pull request's or a commit's author, GitHub's picture where the forge has one and
`AuthorMark` everywhere else, and the connected identities and repository owners on `/deploy/new`
are the same squares. A game's players are initials in the hue their names take in the console's
replies, and whoever set a variable is drawn beside it the same way.

The families above brought 57 files into `public/logos/`, keyed in `product-logo.tsx` and each
recorded in `NOTICE`: the dashboard-icons set (Apache-2.0) in the variant drawn for a dark ground,
Simple Icons' framework marks (CC0) filled with their brand colour — the black ones, Express,
Fastify, Remix, Symfony and Koa, filled white — with Azure's, Django's, .NET's and Solid's colours
lifted to the L 0.72 rung the way MySQL's navy was. Rust's and pnpm's originals, dark marks drawn
for the file manager's paper page, stay, while the tile draws light copies (`rust-light.svg`,
`pnpm-light.svg`), and two marks already keyed, Valkey's and FreshRSS's, were lifted after the
contact sheet showed them failing the ground.

The Databases section brought sixteen more, from the same three collections under the same rules:
dashboard-icons' etcd, CouchDB, Cassandra, OpenSearch, FerretDB and Kafka (its white variant);
devicon's Memcached, NATS, YugabyteDB and Elasticsearch — dashboard-icons ships Elastic's company
mark under that name, and a product is drawn as itself —; and Simple Icons' TimescaleDB, TiDB,
ScyllaDB, DuckDB, CockroachDB and Neo4j, whose dashboard-icons file is the disc that product retired.
Seven colours were lifted to the L 0.72 rung after the contact sheet: CockroachDB's violet,
FerretDB's navy, both of OpenSearch's blues, Cassandra's lashes, Elasticsearch's charcoal band and
Neo4j's blue. SQL Server's file became devicon's silver-and-red mark, its dark red lifted, in place of
a red outline that covered half a per cent of a 14px glyph; Azure SQL Edge, which no collection
draws, is keyed to it. Percona Server, KeyDB and Dragonfly are in none of the three and keep the
database glyph rather than MySQL's or Redis's mark: a product with no licensed artwork has none, and
is never drawn from memory. ScyllaDB's only licensed rendition is a hairline outline, faint at 14px
on a 1x screen and left as drawn.

**The same argument buys the git surface its own glyph set.** Heroicons draws no branch, no commit
and no pull request, so `icons.tsx` maps those words onto the share, hash and chat-bubble marks —
near enough on any other page, and wrong on the one screen where the reader identifies the thing *by*
the drawing. `components/git/glyphs.tsx` takes seven from Material Design Icons, which is already a
dependency for the language marks (`language-icon.tsx`) — the six git marks and the ringed dot a
forge prints in front of an open issue. Nothing else is imported from MDI there: a glyph that exists
in both sets stays Heroicons, or the git pages grow a second icon weight. `AuthorMark` in `git/marks.tsx` is the coloured-wayfinding rule again — a column
of commits where mine and the bot's are two hues is scanned, one where they are the same grey is read
— drawn as the account face without a picture (`InitialsMark`): the initial on a wash of the `LANES`
hue the name is given in the rail and as a run's actor, hashed without its case, so one person is one
colour in a commit line, a run row and a forge's face alike. A square rather than a circle, because a
filled 16px round mark with a character in it is the pill §4 deleted. The one forge face that is
round is the account the dashboard is signed in as (`ForgeFace account`, in the workspace strip's
account control and beside the comment box), because that face is an account and §4 draws every
account round, with `data-slot="user-avatar"` so the pill check knows it.

**A pull request's state is its forge's glyph in its forge's colour.** A list of requests whose
state was a word in the same grey as everything else is read; GitHub prints the state as the mark in
front of the title — green while open, violet once merged, red when closed without it, grey as a
draft — and that is the index the reader already has. `git/pull-state.tsx` draws it
(`PullStateMark`, `PullStateWord`) in `--pull-open`, `--pull-merged`, `--pull-closed` and
`--pull-draft`: GitHub's hues at one lightness, kept apart from the status hues so an open request
never reads as a healthy service, and so merged has a hue none of them owns. How the checks and the
reviews stand are readings of state, so they keep the status hues (`ChecksMark`, `ReviewMark`, an
Actions run's tick, cross or clock). A merge commit in History and a merged branch in Branches take
the merged glyph and hue for the same reason.

**A checkout is drawn as what it is written in.** `/git` cards and the workspace strip open on
`RepoMark` — the logo of the checkout's largest language on the product tile, git's own mark where the
server could not read one — with the language strip under the name: the share of tracked bytes per
language (`languages` on the repository summary) as one bar in the `--language-*` hues, and the
largest three by their own logos (`git/languages.tsx`, over `lib/git-languages.ts`). The owner's
face stays on the shelf rule, said once for every card under it.

**Text a person wrote on a forge is drawn as the Markdown it is.** A pull request's description and
each comment on it are rendered by `git/markdown.tsx` from the tree `lib/markdown.ts` reads, on this
product's type ladder rather than GitHub's, with a `#123` and an `@mention` linking to the forge. It is
drawn with elements, never `innerHTML`; raw HTML is dropped where it is layout and shown as text
otherwise, and an image becomes a link to itself, because the page's `img-src` is its own origin.

**A log line is read by its shapes, and coloured by the same rules as the rest of the product.**
The logs console used to draw every line as one grey-white string with a 10px level tag in front of it,
so a failed password, the address it came from and a 502 were found only by reading every line.
`lib/log-tokens.ts` cuts a line into its shapes — syslog's `time host proc[pid]:`, the common log
format, logfmt pairs, a JSON object, bracketed and shouted levels, addresses, request lines, statuses,
paths, ids — as spans over the original text, so the search's match ranges still land; and
`components/logs/log-text.tsx` colours them from one map. The **status hues** go only to what is a
reading of state: a level, an HTTP status by its class (2xx success, 4xx warning, 5xx destructive), a
word that says something failed or succeeded. A word is read whole, digits and all — `fail2ban` is a
name, not "fail" — and a firewall's `BLOCK`, `DROP` or `REJECT` is the firewall working, on every line
of its log, so it is not a failure word. Every other kind takes a `--tag-*` hue, which sit at one
lightness so no kind outshouts another, and what the eye should skip goes muted — the line's own
timestamp (not drawn at all while the time column shows it), this host's name on a syslog line (not
drawn either), the pid, the punctuation, the keys. The message stays in the foreground. A program's
name takes a hue by name from `LANES` (`lib/hue.ts`: the tag hues without red and amber, which on this
console would read as a process that failed), so one process can be followed down a busy page — the
same argument as `AuthorMark`, whose hash now lives there too. An error or a critical row is washed the
way the build console washes a failing step, a warning row in amber; the level column is the level's
word at the line's size. Where a lens named what the line records, **its event word takes that
column** — "auth failed", "deadlock", "upstream refused" say more than "err" — at the line's size in
the event's tone (`EVENT_WORD`: `font-medium text-destructive` for a failure, `text-warning`,
`text-success`, muted otherwise), and the level stays as the edge and the wash; the column is as wide as
the longest word on screen, within bounds, rather than a fixed width that cut "restart scheduled" short.
An event a busy log says constantly and neutrally — a request, a connection, a cron session — keeps the
level's word (`mark: false`), or the column would be a wall of words saying nothing. A value the lens
read out of the text is drawn as what it is (`components/logs/field-value.tsx`: an address as its
network, a status in its family's colour, a method as its word, a duration in its latency tone, a unit,
a jail, a package or an image beside its product), the same drawing a request's client gets, so a
Postgres client and a visitor are one mark; at most three such columns stand before the message, only in
colour and only while a line on screen fills one, a column drawn as its text as wide as its longest
value on screen (an upstream cut to "http://1…" identified nothing). A record's continuation lines stay
under their head, the first three shown and the rest one fold away; a run's start (a unit started, an
application came up) is a hairline rule across the pane with its word and the unit's lane hue, so a
service's runs read as runs — in one service's stream only, and never for a stop or a failure, whose
word and wash already mark them: a whole host's journal ruled off every timer that fired; and in the
live tail a run of identical lines is one row with a muted `×N`. `stderr` is a muted word, never a danger tag — Postgres writes every line there, and a red tag on
each said a failure that was not there. None of these is a pill or a badge: a `Tag` carries the word
only with Colour off. A structured line is drawn as its message and fields in the logfmt shape the
tokenizer reads, most telling field first. The "Colour" toggle beside Wrap and Time turns all of it
off and shows each line exactly as it was written; in a pane too narrow for a switch each (a phone, a
sheet), Wrap, Time, Colour and Repeats are one **View** menu, so the level chips are not squeezed to one.

A deployment's request log is a log, and is drawn by the same rules: the request console, a request
opened in place, the Insights lists and the scanners notice all take their parts from
`deploy/request-marks.tsx`, which is `log-text.tsx`'s token classes over `lib/requests.ts`'s one
status map. A status is coloured by its family — 2xx success, 3xx the path hue, 4xx amber, 5xx red —
a write takes the method hue while a read stays muted (a `DELETE` is a change, not a danger), the
path and query are tokens, and an address takes the address hue beside the client drawn as itself.
Its Colour switch is the console's own; turned off it keeps a failure, a refusal and an answer
slower than a second, because those are readings of state (§3) rather than decoration.

**A step's record is read by its shapes, and code is coloured by the same rules.** The run page's
Details drew a step's evidence as a well of grey JSON under an accordion row: the commit a build
checked out, the Dockerfile it wrote and the image it made were all found by reading braces.
`deploy/run-evidence.tsx` reads the record by shape instead — a value by its key, a list by what its
items carry — and draws each the way the product draws it elsewhere: a toolchain, a Node version, a
platform or an image beside its product's mark, a digest cut to twelve characters with a copy, a
commit as the Git page draws one, a list of images as rows of their products with size and platform,
a list of health checks as rows of their outcomes. What is text — a Dockerfile, a command line, a
record nested past two levels — is a `CodeBlock`: the `Well`'s ground with a strip naming it, its
length and a copy, and its tokens in the `--tag-*` hues the log console's tokens sit on (keys blue,
strings green, numbers pink, literals and a Dockerfile's instructions violet, paths and flags cyan,
digests slate, punctuation and comments stepped back). None of the status hues: a string is not a
success. A step is drawn on the tile of the product it works with (`StepTile`, its state in the
tile's corner the way `ProjectMark` carries a framework) — the forge for the source, the toolchain
for the build context, Docker for the build and the runtime, Let's Encrypt for a certificate, the
authority certbot asks — and a step that is the dashboard's own bookkeeping keeps a glyph.

`/deploy/new` reads its plan the same way since 2026-10-05, because a plan is the run before it
happens. The drawing beside Configure's fields — four nodes on beams — is a rail
(`new-project/plan-rail.tsx`, over the pure `plan-reading.ts`): each of the four steps under its
segment of the spine, each part of the plan the step decides on the tile of the product it is (the
forge, the framework, the runtime, Let's Encrypt for a name served over HTTPS, a glyph for a health
check, the limits, the storage, the environment and the server's check), what it currently says
under its name, a build or start command, a check's request, the paths kept between releases or the
variables still owed as code in the same hues (`ShellWords`, the inline form of the run page's
`CommandBlock`), and a part that wants a look before Deploy marked in its tile's corner. Every row
opens the fields that decide it. Review opens on what the server said about the plan the way the
run page opens on how a run ended, and draws what the release does to this server — a mount, a
generated secret, a value the plan sets, a readiness request, the cutover — as rows of what each
is. Detection's evidence is its files drawn as files (`FileIcon`), and an image tag is its
registry, its repository and its tag.

**A file is drawn as what it is, and a folder in the colour it was given.** The file manager drew
Material Design Icons' file family, a stencil per category in one flat tone: it told a config from a
certificate, but a directory of forty files was forty stencils and the format's own mark — the thing it
is recognised by — was nowhere. `files/file-icon.tsx` now draws its own two shapes, the ones every
desktop file manager has trained the eye on. A **folder** is two-toned, its tab behind its face, with
what it holds pressed into the face a step darker — a product's Simple Icons mark for `.git`, `.docker`
or `node_modules` (single-shape drawings, so a mask of one reads as the mark), a glyph from this
product's Heroicons vocabulary for `.ssh`, `etc` or `logs`. A **file** is a page with its corner folded,
the format's own logo on it in its own colours (devicon's language marks beside the product logos), and a
band along its foot in the format's colour carrying the extension where the icon is large enough to set
a word. The page is light — `--doc-page` stops short of white — because those logos were drawn for paper
and Rust's black gear or Markdown's ink is invisible on this ground; it is the one light surface in the
product and it is artwork, not a surface anything sits on. The band's hue is the format's (`--language-*`
where GitHub colours it, `--tag-*` otherwise), by the argument `LanguageMark` makes: a colour the reader
already knows the format by is a legend they do not have to learn.

A folder's colour is a **label**, and §3's tag argument is why it is the operator's: "the red one is
production" is a fact about this server, so it is stored there (`files.colours`) and drawn wherever the
folder is — the listing, the tiles, the sidebar, the inspector, the strip, the finder, the terminal's and
a checkout's tree, a volume's or a stack's browser, a deployment's storage. The shell provides the saved
labels to every page (`SavedFolderColours`, read again on each navigation); Files nests its own provider
over it so a colour it picks is drawn before the round trip lands.
The strip's compact folder button sets one colour for every folder, stores it as `files.defaultColour`,
and clears old individual labels. A folder can then be labelled on its own in its inspector or menu;
that label takes precedence until another global choice. Nine names (`--folder-*`, one value each;
the tab and the pressed mark are mixed from the face in `[data-folder]`), blue until somebody says
otherwise, graphite for build output and installed dependencies until a global colour is picked
because nothing in them is yours to edit. The picker is the folder drawn in each colour,
the chosen one `bg-accent` like every selection. The Files page is a workbench and, like the terminal
and a Git working copy, has no page header: its commands sit in the strip across the workbench, beside
the folder they act on.

Files and `/files/editor` take the **reading** register: their frames contain independently scrolling
workbench panes, while directory and selection readings live in the status strip. Tiles use fixed,
compact columns (80/104/128px) and tight gaps rather than stretching to fill empty space. A tile
contains its icon and name; metadata stays in details view and the inspector. Listing entries have
no overflow dots: their menus open through right-click, Shift+F10 or touch long-press. Selection and
clipboard commands float above the listing's foot, with a four-pixel arrival/exit and opacity over
160ms (instant with reduced motion), so selecting never inserts a row or shifts the workbench.
Selection washes and drag-source opacity ease; the marquee follows the pointer immediately.
The sidebar's active wash and
`aria-current` identify its folder without another dot. Its rows are one line — the folder drawn as
what it is and the name of what it holds (an account's name for a home, *File system* for `/`,
*Configuration* or *Logs* for the server's own folders, named by `files/places.go`) — under four
headings that fold and stay folded: Home, Starred, This server, Recent. A second line carrying each
place's path or a caption such as "Locally installed software" doubled the column and read as small
print; the path is the row's tooltip. Every control in the strip has a face and a 32px height, and the
faces come in groups rather than as a row of bare glyphs with three boxed buttons among them: the
sidebar toggle; Back, Forward, Parent and Refresh as one box (`StripGroup`, the Git workspace's fetch,
pull and push shape: the group draws the edge, each segment only its hover); where you are as a
field — the colour button, the crumbs and the star inside one `bg-input/30` edge, which is what it
becomes on Ctrl+L; Find and content search as a second field-shaped box; the view toggle and Arrange;
then the account, New, Upload and the details toggle. Upload keeps the brand face as the page's one
command. Name and content search share a compact keyboard palette with
file identities, paths and highlighted matching lines. Quick editors keep these same controls when
opened in the full workspace beside a collapsible tree.

The Files search palette reserves its viewport-bounded height before results arrive. The input is
borderless even while focused, with the caret and active result carrying its keyboard state; other
controls retain their focus rings. Only the results scroll. Its footer is one line on a wide screen and
two on a phone, the same lines in every state: where to search (*This folder*, *Home*, *Everywhere* —
a segmented choice, with two that are the same folder offered once), the folder that means, hidden
files, the count and the unreadable-entry warning as text inside the status cell. It used to reserve a
whole empty row under the rest for that warning, which read as a strip of dead space at the foot of
every search. A hit's second line is the folder it is in, said from the scope (`ubuntu/Downloads`),
never its own path again under its name. Header controls and footer cells are fixed so typing, loading
and partial results do not move the frame. Result arrivals and departures
fade, with departing rows immediately inert; reduced motion renders the next state immediately. The
search body stays mounted through the dialog's closing animation so dismissal does not collapse it.

A `Pane`'s chrome strip is the one place a small inline glyph still sits beside a name (the git tools
column, the session rail). A pane is a region of a workspace rather than a block of content, its strip
is deliberately tighter than a panel's, and the mark there is a bare 14px outline rather than a tinted
plate — it reads as part of the chrome, which is what it is.

## 15. Redesigning a page

What "redesign this page with the design system" means, in the order to do it. It was settled on the
host Overview (`app/(dashboard)/page.tsx`) in 0.6.7, and that page is the reference: open it beside
the page being redesigned and make the second one read like the first.

**The look in one sentence: readings on the page, not boxes on the page.** One dark ground. A hairline
where two things meet. Type doing the hierarchy. One blue. Motion only to say something arrived.
"Modern and clean" here means *fewer edges*, never more decoration — no gradients, no glow, no glass,
no shadows, no icon plates, no badges, no rounded cards floating over the ground.

The passes, in order. Each one is a diff you can review on its own.

1. **Remove the frames.** Every `Panel` becomes `Panel plain` unless it is one of the exceptions in §7:
   a `Pane` (a working region with its own scrolling), a **table** (a region with its own scrolling
   that happens to sit in the page's flow — §2), a `Well` (output you read), a `Group` (a fence
   inside a body), or something that genuinely floats (popover, dropdown, dialog). "It is a card" is
   not a reason. A block that is the whole of a section is a title and a hairline. Two blocks side by
   side are both plain; the gap between them is the separation. A framed block that survives this pass
   has a sentence in a comment saying why.
2. **Figures are tiles.** Any set of headline numbers — utilisation, counts, one-per-module "service
   cards" — is a `StatGrid` of `StatTile`s: hairlines between, nothing around, every tile the same
   inset. A tile that is a destination is wrapped in `StatLink` and takes
   `className="h-full transition-colors group-hover:bg-row-hover"`; the arrow is the link's, not yours.
   A tile whose figure is a question about what is under it — a log's "Auth failures 12" — is a
   `StatButton` instead: a press narrows the pane below rather than leaving the page, its revealed mark
   is the funnel every "only lines like this" carries, and `pressed` says the figure's filter is the
   one on screen (pressing it again lets it go).
   The figure is 24px (`text-2xl`): `text-xl` is not on the ladder. A state colours the figure through
   `tone`, never through a badge beside it.

   **A page you *configure* is not exempt.** §16 makes this argument about `/deploy/new` and it
   generalises to every settings tab in the product: the deployment Runtime tab ran ten fields, four
   selects and two switches with not one figure among them, so none of the three loud things this
   system trades decoration for could fire, and the screen had nothing on it a reader could find
   without reading. It opened on four readings — where it listens, what it may use, how it is
   replaced, and what it can reach — until the operator asked for them to go (below), and each was a
   fact the form beneath it sets, drawn from the draft rather than the saved revision, because what
   you are setting is what the page is about. A reading was `warning` where the *absence* of an
   answer is the answer: an uncapped container and a port open on every interface are the two facts
   an operator wants off that page without opening a fold. Two of them read the running container
   as well as the form: the memory limit was drawn against what the live release peaked at in the
   last hour, and the release strategy is checked
   against the rule the executor applies at the next deployment — a writable mount, a fixed host
   port or the host network cannot run two releases side by side, and blue/green on such a plan was
   offered there and then refused at start. Build took the pass it had skipped (what it builds with,
   the last build read from the live release's own build step, the release tasks, the build
   variables with a warning where a secret would be compiled into browser code), and Domains,
   Storage, Databases and Automation opened on four readings each.

   The rest of the deployment section took the pass as a question of *which* figures. The fleet
   opened on four the chips beneath it cannot say — how many projects are live, requests a minute
   across the fleet, the share of them failing, the build slots in use. The Logs page's readings
   each carry their last hour, as the host Overview's do.

   **And the one page with no tiles, which is the shape of the argument for dropping this pass.**
   `/git` had four — repositories, uncommitted, behind, unpushed — and the operator asked for them to
   go. Nothing was lost, because every one of those numbers already sat on a filter chip under them,
   and a chip says what is waiting *and* narrows the list to it where a tile could only say it. What
   the tiles also did — put the urgent thing first — is done by ordering the repository cards
   worst-first on each shelf, and the shelf with something wrong on it first. A page may drop this pass when it can name where each
   figure went and what now does the job the figures were doing; `app/(dashboard)/git/page.tsx`
   carries that in its doc comment, the way a surviving frame carries its sentence in pass 1.

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
   so forty Chrome renderers are one row rather than forty; a press narrows the table to it. The
   line of two buttons that stood above the identity line went into it, as Metrics' shortcuts did.
   The table took fixed columns — sized to their content, a long command line had pushed Memory
   and every row's menu past the panel's edge at 1280 — and draws CPU and memory as a figure beside
   a short bar; a process new since the last poll rises (`useArrivals`). The process sheet lost its
   two tabs for one scroll that opens on four live readings over the process's recent windows,
   which the sampler keeps for every process it measures, so a sheet opened from a table that has
   been open for minutes opens on those minutes.

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
   and the most memory on Live's bar the size of the machine (`ShareBar`), and a third block of
   what happened — the last units to start, stop, finish or fail since the boot settled, each with
   its time, a failure saying why in the same line and a restart in progress shimmering. A row
   there opens the unit. The figures are systemd's own, read from each unit's cgroup by the list
   route, so a service's share is every process it started. The table took Live's fixed columns
   and its figure-beside-a-bar readings, and its state column says how long a unit has been in its
   state — up for, failed since and why, the restarts it has taken. Only a newly listed unit rises
   (`useArrivals` keyed on its name); starting or stopping an existing unit updates it in place.
   The unit sheet kept
   its Journal tab, whose pane needs the height, and its overview became the process sheet's
   readout: four live tiles (CPU over the unit's recent windows, memory against its limit or over
   its windows, tasks against theirs, automatic restarts with the policy), or the last run's
   counters when it is not running; a failure or a restart loop said first as a Notice with the
   way to its journal; the command coloured as a command rather than systemd's record of it; the
   processes it holds now, each opening Live's sheet; and how it runs, with its type in words and
   its overrides.

   Packages took the same exit in 0.7.1 at the operator's request. Installed is a fact in the
   identity and a count on its view; by-hand and dependencies are scope chips; Updates is its view's
   count, the security chip and a security-first queue; On disk heads the software band. The band
   (`components/packages/software-band.tsx`) is Processes' question applied to installed software:
   the five largest products, or archive sections for packages no product names, as spans of one
   bar of the installed size in the disk measurement's hue, with every other package in one muted
   span. Its figures glide on arrival and to a new inventory; a press narrows Installed to that
   group, including its dependencies. The queue beside it opens a package's readout and carries
   the security/all-upgrade decisions; no advisory data is said rather than counted as no security
   updates. The sheet is one scroll, with version changes and size over commands, registered services,
   file links, the manual, inspectable dependencies and metadata. Catalogue suggestions and results,
   and the sheet's service/file destinations, take the lit choice edge (pass 3). Tables retain
   their frame and gain fixed responsive columns and a measured size bar per installed row.

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
   at a time: `components/procs/schedule-band.tsx`, the next 24 hours of all three on one axis
   that starts now, a lane per schedule soonest first, each drawn as the program it runs. A lane's
   next run is a dot in its kind's `--tag-*` hue (blue for the account's crontab, violet for the
   timers, cyan for the package files — none red or amber, so no lane reads as one that failed),
   the runs after it ticks, and a schedule that fires more often than the axis can draw a band; the
   kinds are chips that count and narrow, and what the axis cannot draw — weekly work past it, what
   runs only at boot, what is switched off — is a line under it. A cron line's runs are its
   expression's (`lib/schedule.ts`); a timer's is the one systemd reports, since the packages'
   timers add a random delay to their calendars and every later run drawn from the calendar would
   be a time they do not fire at. Every countdown ticks, which is the page's motion, and nothing
   else moves. A lane, a job's command and a timer's name open sheets addressed by `?job=` and
   `?timer=`: a job's opens on its next run and its runs in the next day and week, the week as seven
   lines of a day with a tick per run, the command coloured as a command, and the cron log narrowed
   to that command; a timer's on its next and last run and how long that took, its calendar in
   words beside the expression with the random delay said, the command its service runs, and the
   service's Runs view, which used to open inside the timer's table row. The job editor is a sheet
   as well, its frequency a row of toggles rather than a select, drawing the week the schedule
   being written makes as the fields change.

   Docker's Images page took the same shape on 2026-10-08, at the operator's request, because it
   had no life: four grey lines of disk figures, a notice and a column of grey cards, nothing on it
   told apart by anything but its words. It opens on Docker's identity line (the engine's version,
   how many images, the layers' size, how many are in use and untagged as facts; the registry's
   verdict at its right end, a press of which narrows the table to what is behind, beside Pull and
   Build). Under it `components/docker/image-band.tsx` asks Packages' two questions of Docker. On
   disk is one bar of what Docker holds — images, build cache, containers, volumes, each in the
   colour the Docker overview's `DiskSummary` gives that kind — with the part a prune would give
   back hatched inside each span, so the reclaimable share is seen before a figure is read; each
   line keeps its own Reclaim, which now calls that kind's own route rather than the sweep (the
   sweep also removes stopped containers and networks, which the Images and Build cache lines'
   confirmations never said). Registry is one bar of every image by what its registry says —
   update available in amber, current in green, pinned and built here in `--tag-cyan` and
   `--tag-violet` because they are choices rather than states, unanswered and unused muted — with a
   line per answer drawn as the products in it, each narrowing the table; a check the reader asked
   for sweeps the bar. The images are a framed table again (§12): the image as its product, its
   registry and tag a step back from the repository; the containers running it by name with their
   state's dot, each a link to its page; the registry's answer in the band's colours; its age; and
   its size over a bar against the largest, in the Images span's colour. Below `xl` the containers
   and the answer join the name's second line as a dot, a count and the word. A pulled image rises
   into the table (`useArrivals`), and its row says *Pulling…* while its pull runs. The pull dialog
   draws a row per layer from Docker's stream, received bytes in `--chart-2` and written ones in
   `--chart-4` (§10), with the raw transcript folded. The sheet (`?image=`) opens on four readings,
   the containers using the image as lit rows, and where the size went: a bar per layer in build
   order beside the instruction that wrote it, its verb in a `--tag-*` hue and a RUN's command
   coloured by `ShellWords`, the metadata-only steps folded.

   Notifications took it as well. Its four (channels, delivered and failed in the last day, the
   last message) went to the Channels header, which counts the channels and the paused, and to a
   list of the recent messages beside the cards: the day's delivered, failed and retrying are the
   counts in that list's header — a message retried until it went out is one row and counts once,
   as delivered — and the last message is its first row, naming the run it announced.

   Credentials took it after that. Its four (held and across which hosts, in use, never used, last
   used) each said what a card says where the credential is: the count and the hosts went to the
   Saved credentials header, the In use rule counts what a source reads through and each card names
   the projects that do, one never used says so on its own card in amber, and when each was last
   used is beside its name. The GitHub App's state stays in its own section.

   The dashboard's own two pages took the same exit in 0.7.0, and the reason generalises: a figure
   on a page you configure is best drawn beside the control that sets it. Version's Installed,
   Latest and Checked became one identity line and the timeline's marks; Configuration's Answers
   at, Certificate, Port and Two-factor went to the heads of the sections that set them and to
   the proxy in the stack. Both pages' doc comments name where each went.

   Configuration took a second pass in 0.7.1, because the operator found it the greyest page in
   the product: a list of three services, a single 48rem column of fields and nothing on it a
   reader could find without reading. It opens on the Version page's identity line (the address
   it answers at as the name, each part of the URL in its own hue, the checkout as the facts), and
   the stack is drawn as the path a request takes to reach it in the wiring vocabulary
   (`components/config/request-path.tsx`): this browser as itself, the tailnet as Tailscale, Caddy
   with its certificate's issuer, and Next.js and Go behind it with their state in the tile's
   corner. Restart and Rebuild keep their cards and gain the Compose or Docker command each runs,
   coloured by `ShellWords`. The settings are two columns of sections, the certificate across both
   and first; the allowlist is a list of the networks it lets in, each drawn as where its addresses
   are (`components/config/allowlist.tsx`); a section holding a change not yet applied carries
   *Edited* in `--git-modified`, the colour §3 gives a pending change.
   The account's Security page took it for the same reason — the second factor's state and how many
   sessions are signed in sit in the heads of the sections that change them — and Sessions opens on
   the session it is read through, with the count of the rest on their header.

   The Security section took a second pass in 0.7.1, because the operator found its pages grey and
   still. The overview's figures count up as they land (`NumberTicker`) and Intrusion's carries the
   bans per day as its `TileTrend`; over them, the posture is its seven checks as a coloured strip;
   under them, the ways onto the machine are a wiring picture. SSH took the exit for four of its
   eight tiles — the port, passwords, root login and the keyed accounts went into the picture of
   sshd's doors, which reads them as one answer — and kept the auth log's four. Firewall kept its
   tiles and gained the picture of its inbound path over them. The three pictures are described
   under *The Security section draws what it watches*.

   System users kept its four and changed one: the Locked count became a filter chip over the
   cards beside who can sign in and who administers the host, where it also narrows the list, and
   its tile went to the administrators — the members of `sudo`, `wheel` or `admin`, amber while one
   of them needs no password. The audit log, which had no figures at all, gained four readings of
   the last day read from their own query so a filter narrows the trail without narrowing them:
   the changes with their hours as a trend, the failures, the people as their faces and the
   sign-ins with the refused ones.

   Three deployment pages took it in the same pass. A project's General settings did because the
   project identity line already is that page's reading line: each figure of the old Project
   card went beside the control that sets it, and the page's doc comment names where. Variables took the
   `/git` exit exactly — every count (all, pending, secret, config, reaching the build, the runtime
   or a release task, holding a reference) is a filter chip over the list, where it also narrows
   to what it counts, and the products the environment talks to sit under the section's head. And a
   project's Runtime page carries each count in the header of the block it counts. Its five moving
   readings stood as tiles over the charts they move on until the operator asked for the tiles to
   go; each now heads its own chart as the chart's reading now — the processor's share of a core
   or its quota beside Processor, memory against its limit beside Memory, in and out beside
   Network, read and written beside Disk, the process count against its limit beside Processes —
   with a short meter where there is a ceiling, and what the tiles' hints said (the counters since
   the container started, the quota and limits) is a fact list beside the Processes chart. In and
   out stay two readings because a flood and a large download share a sum.

   Deployment history also takes this exit: each pair of readings stands over the chart it explains.
   Success and weekly frequency sit over releases per day; median duration and recovery time sit over
   release time per day, which draws the same days at the same height beside it, with the window's
   median as a dashed rule and the slowest day naming the top of its scale. Why the releases failed
   runs under both, each cause a share of every failed release — and a lone cause a line with its
   count, since a bar with nothing beside it compares nothing. The timing was a list of two figures
   set at the far edge of their labels over a trend with no scale, and the reason a single full bar:
   a drawing that cannot be read off is not a reading. The four readings keep their basis and window
   without a separate strip of tiles over the records.
   The project Overview retains its delivery tiles. History's counted status filters use the
   underlined view-strip look as toggle buttons; filtering the same records does not make them ARIA
   tabs. Switching status or environment reserves the results' height for the page visit, so a
   shorter list does not clamp the shell's scroll position. These rows skip the arrival stagger so
   a filter does not replay the entrance of the same history.

   The rest of the deployment section took the exit in 0.7.1, because the operator asked for every
   row of figures at the top of a deployment page to go. The fleet starts on its runs in flight:
   each card carries its own traffic, the per-state counts are the chips', and a site failing badly
   is an Attention finding. The Build, Runtime, Domains, Storage, Databases and Automation settings
   start on their first section, whose head says what it currently is — Runtime's limits are drawn
   against the last hour's peak beside their fields and a blue/green plan the executor would refuse
   is said at the strategy, Domains' warning about a public bind address sits in its section, and
   Databases' cards say whether the gating job dumps each one. Logs is the one deployment page that
   keeps its row: four readings of the ingress's last hour that no row or chip says. Its fifth,
   Container, went — the Events tab already counts the hour's disruptions.
3. **Lists are rows — and a row you *take* is not a row you read.** `RowList`/`Row` for things with
   a title and a second line, `FindingList` for verdicts, a table for columns. Never a grid of framed
   cards standing in for rows. A scroll container that holds plain rows pads by the rows' bleed
   (`-mx-3 px-3`), or it grows a sideways scrollbar.

   **Then ask of every list on the page whether its rows are destinations.** A row that carries an
   `href` — a project to enter, a site to open, a stack to look inside — is a choice, and the last
   subsection of §16 gives it a `ChoiceList` of `ChoiceRow`s and the lit edge. A row that is a
   *reading* keeps its hairlines and its wash, and a table of readings is never touched.

   This is the pass that was missed everywhere, and the 2026-09-21 revamp exists because of it. The
   lit edge shipped with `/deploy/new` and stayed there: `ChoiceRow` appeared in three files, all of
   them under `new-project/`, so the git import panel was the only surface in the product that
   answered the pointer with an edge — and the complaint that arrived, that one screen had depth and
   the rest were flat, was a correct reading of exactly that. The proxy overview's sites, the Docker
   overview's compose projects and its idle containers were all `Row`s with an `href` on them. It is
   a numbered pass here rather than a sentence in §16 because a sentence in §16 is read by somebody
   redesigning a **flow** page, which is precisely the reader who does not need it.
4. **No decorative glyphs.** Headers, sections, tiles and modals carry no icon (§14). The glyphs that
   stay are wayfinding — the sidebar entry and a module tile's mark, drawn as a 12px `text-brand`
   glyph inline before the tile's eyebrow — and the ones that *are* the message: a `Status`, a
   `Notice`, an `EmptyState`, a verb on a button.
5. **Type on the ladder.** A flow question or headline figure 24 → section 16 → surface 15 → body
   13 → hint 11 → micro 10. Reading page names are accessible but not drawn. Anything in between is
   deleted, not rounded to the nearest.
6. **Colour by role.** Brand blue is a command's face or a location mark. Amber, red and green arrive
   only attached to a reading — a figure's `tone`, a `Status` dot and word, a `Notice`. Selection is
   `bg-accent`; hover is `bg-row-hover` on a row or tile and a border step on a framed destination;
   never both, never movement.
7. **Motion says one of four things** (§11). Add `animate-rise` where a fetch settles: the page once,
   each block once, a figure once (swap its `key`). `transition-colors` on hover. Nothing else moves.
8. **Data, not captions.** No sentence under a title (§5). What the reader needs is a `Tag`, a
   `Status`, a hint on a tile, or a `Notice`. What the page *is* — a hostname, a kernel, a platform —
   is its own row.
9. **Alignment.** Tiles top-align so a row of names is a row; hints truncate rather than wrap; every
   tile in a row, the first included, keeps the same inset.
10. **Verify.** `scripts/test-changed.sh` (it runs `tests/browser/design-system.spec.ts` for any
    UI change), then a screenshot at 1280 and 1720 wide against a mocked
    API in the pattern `mockShell` uses, and look at it: a scrollbar where none belongs, a label a
    line lower than its neighbours, a figure at the wrong size, are things the checks do not catch.

**What the Overview looks like after these passes**, as a checklist for the page you are on: the
machine's identity line first (`HostIdentity` — its distribution drawn as itself, the processor and
hypervisor as bare marks among its facts, the verdict at the right end); a Resources `Section`
whose head carries the socket's `Status live` and the way on to Metrics, holding a four-tile
`StatGrid` of the readings that move — each carrying its last hour in the tile's `trend` slot where a
meter would be, keyed before its name by its line's colour, its figure gliding to every frame — over
the Storage band, a capacity bar per filesystem; the `Health` panel across the full width — a strip of every area checked drawn as the release path's own segments, then its findings as lit cards; the Deployments section; a plain top-processes list beside a
plain activity list; and a `Section` holding a `StatGrid` of eight `StatLink` tiles, one per module,
each naming what it counts with the products themselves. No frame anywhere on the page — the project
cards carry the lit edge of a thing you take, which is not one. Everything that arrived, rose.

The 0.7.1 pass asked of each block whether it answered the question a reader opens the page with,
and three did not. The Health list said only what the recorder measures, so a failed deploy, a
backup gone quiet or a certificate past its renewal was a red figure on a tile two screens down with
no word of what it was; it now carries what every module found (`components/overview/attention.ts`,
the fleet's own Attention findings among them), worst first, each opening the page that fixes it,
and the verdict on the list and the identity line is the worst of all of them.

The 2026-10-08 Health overhaul answered "no life, no colour" without leaving the vocabulary. A
finding opens its fix, so it is a thing you take and a `ChoiceRow` with the lit edge (§16), on its
level's `bg-wash-*` with a short bar of the level's hue before the area's glyph; the figure it was
judged on is a `Meter` with a `mark` at the threshold it crossed. Above them, every area the server
checked is a cell under one `Segment` of the release path's bar (`deploy/run-pipeline.tsx`): green,
amber, red, dashed where the area could not be read, and sweeping while a check the reader asked for
is in flight — so a healthy host is a line of green with a reading under each segment rather than a
sentence saying nothing was found. Notices fold under a quiet `Disclosure`. Inside the sheet the
diagnosis is a banner on the level's wash with the server's evidence as figures, and every fix ends
on a fresh reading drawn as a success or warning outcome line; a control in flight runs `BorderBeam`
round its card and names its participle in `TextShimmer`. With that it took the
row's full width — beside the activity list it was one finding over half a row of nothing. The
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

Reading pages now keep their page name in a screen-reader-only `h1` through `PageContext`. The rail
provides the visible location. A linked parent remains as a compact way back, while pages without
one begin directly with their content. Put a control beside the data it changes: list commands in
their section header or filter bar, workbench commands in the workbench strip, and empty-list commands
in the empty state. The Metrics range, pause and export controls sit in the machine identity line;
the proxy service verbs sit with the engine facts. Detail pages keep their verbs beside the way back
and their resource name in the first facts or identity row. The deployment fleet puts its related
pages and create command with the list filters; an empty fleet has its create command in the empty
state.
The Databases section omits the top metric-card sections on every page. The control center starts
with attention findings carrying their fix, then the saved databases with search, engine chips and
layout controls. The former metric-card `?show=` filters no longer narrow the inventory. Individual
database cards retain the facts needed to compare connections. A database home starts with its
identity line and a `ChartPanel` of recorded activity, with 1-hour, 6-hour, 24-hour and 7-day windows,
then attention, rankings and reference blocks. Activity is recorded by the backend every 30 seconds
while the dashboard runs, including when no database page is open. Performance opens on its view
strip, Advisor on its findings with severity and category filters, Access on its accounts and list
filters, and Backups on its schedule state, next run, action and dump list. These controls stay beside
the content they affect, without recreating a headline metric section.
Every page of one database but Home carries the same compact identity
strip — engine mark, switcher, facts, the environment and protected tags, status, Connect and the
verb menu — and keeps its own name in the screen-reader-only `h1`. The workbenches carry no tiles
over their frame (§2); on Logs the lens's readings are the counts on its quick-view chips
(`ServiceLogs readings="chips"`), a reading no view asks being a chip of its own. What gives the
section its colour is the sanctioned five and nothing else: the engine drawn as itself, figures
that move, colour that names a kind (`--tag-*` for a Redis key type, a BSON type, a catalogue kind,
a statement's verb; `hueFor(name, LANES)` for an account, a schema or a namespace; `--git-*` for a
pending change; `--chart-1..5` for a series), the lit edge on what is taken, and work in flight said
as it happens (a `BorderBeam` round a server being started, the participle in its row). Flow pages
keep their visible question as the `h1`, since the question is the work on that screen (§16).

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
(`LiveFigure` in `components/overview/readings.tsx`) that counts up once on arrival and then glides
to each two-second frame, its unit held outside the count so 980 KB/s becoming 1.0 MB/s swaps
rather than counting down; the CPU figure carries a bar per core from the same frame (the busiest of
neighbours when there are more than 32), which says whether 90% is every core or one pinned; and
each tile's name is keyed by the 2×10 bar a chart legend draws, in its sparkline's series colour.

The 0.7.0 pass took two things off it that had been saying the same figure twice: a `MetricStrip`
of uptime, processes and cores in the header's corner (facts about the machine, now in its identity
line; the cores were on the CPU tile as well) and a "Last hour" panel of four sparklines whose every
value repeated the tile above it (the sparklines are in the tiles now). The Metrics page opens on the
same identity line with the processor as its mark.

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
over it, and swap — beside its chart), **Network** (throughput beside sockets, and the interfaces
table, each row as its owner's mark and a bar of its in and out against the busiest), **Storage**
(the Overview's filesystem rows with their scan, beside capacity; throughput, operations, latency
and inodes), and **Saturation** (pressure beside load). Every series takes its measurement's hue
(§10). Neighbouring heads share a height so their hairlines meet (pass 9). The "Click a chart to
pin a moment" caption and the shortcuts button that stood as two lines above the identity line are
gone: the shortcuts are its last control, and the pinned moment's strip appears only while a
moment is pinned, held at the top of the scroll because the chart it was pinned from is usually a
screen down.

**The logs page took a deployment's Logs page in 0.7.1**, at the operator's request: its one strip
held the source's name, its facts, an outlined Export box and the view tabs, the box and the
underline meeting edge to edge at the right end of a 40px row, over a stray line holding nothing but
the shortcuts button; and nothing on the page counted anything until Insights was opened. It now
opens on the chosen source's identity line (`SourceIdentity`, the Overview's `HostIdentity` with its
rule a step closer, since the console under it needs the height) — the source drawn as its product,
its kind, state, path, size and rotated set as facts, Export and the shortcuts key at its end — and
under it the lens's readings as `StatButton` tiles, each with its window's shape, counting up as it
lands and narrowing the lines to what it counts. They hold still across Live, History and Insights,
so Insights no longer draws its own over the window it picked. The strip is the rail toggle and the
views from its leading edge, as a deployment's pane is. The tiles stand only where five fit across
and the live tail keeps its height under them — a window of at least 1280 by 800, where five broke
three and two at the old 200px tile floor, now 180 — and on a smaller window the same figures are
the counts on the lens row's chips, the database workbench's answer.

**The Network section was made in 0.7.1 out of four Security pages**, at the operator's request: the
firewall, the connections, the interface list and the tools left Security, which kept what is about
who may get in, and joined new pages that change the network as well as read it. Every page is a
reading page, and what makes them alive is the sanctioned five, held hard:

- **Things drawn as themselves.** A device is the product that made it (`network/marks.tsx`:
  Docker's bridge, Tailscale's tunnel, WireGuard's, a container's veth as the product its image runs)
  or a glyph for its kernel kind; an upstream resolver is its provider; a tailnet peer its system.
  Ten marks arrived for it (WireGuard, AdGuard, Pi-hole, CrowdSec, Headscale, Mullvad, Quad9,
  Unbound, OpenVPN, ZeroTier), each lifted to the L 0.72 rung where its own colour vanished on the
  ground (`public/logos/NOTICE`).
- **Pictures whose wires are traffic.** Six pictures stand unframed over `wire-grid` in the
  deployment section's vocabulary: the Overview's topology (`network/topology.tsx`: the outside —
  the internet through the uplink, the tailnet, each tunnel — this server with its firewall and
  gateway as facts, and the inside — each Docker network with its containers' products, each bridge
  made here), the Connections page's callers and what they reached (`connections-map.tsx`), Routing's
  decision (`routing/decision-map.tsx`: the policy rules in the order they are asked, each wired to
  the table it looks in, the one answering this browser lit), each WireGuard tunnel's devices and
  sites (`vpn/tunnel-picture.tsx`), the gateway's forwards, and DNS's resolver chain. A wire carries
  a pulse while its device moves more than a kilobyte a second, quicker the busier it is
  (`pulseDuration`, logarithmic over eight orders of magnitude), running the way most of the bytes
  go; still while idle, dashed where the thing is down or not set up, amber where the internet
  reaches a server no firewall filters. Where nothing is set up a dashed ring stands where it would
  go, and is the way to set it up.
- **Figures that move.** The live ring (`use-live-traffic.ts`, two-second points topped up with only
  what the page has not drawn) feeds `LiveBytes` figures, each tile's fifteen minutes as its
  `TileTrend`, the device rows' two-minute sparklines and every chart's Live window.
- **Colour that names a kind.** A device's role takes a lane hue (`ROLE_HUE`, from the palette that
  cannot be read as a state), an address's prefix the port hue (`network/address.tsx`), in and out
  their chart colours everywhere they are drawn.
- **The lit edge on what is taken.** Devices, peers, forwards and lists open their sheets and are lit
  rows; route tables, rule lists, peers on the tailnet and shaping are readings and stay framed
  tables.

What the guard refuses is drawn, not hidden: a control the server would refuse stays, disabled,
with the guard's sentence beside it (a device's *Set down* on the uplink, forwarding that Docker
needs), because a control that disappears says nothing and one greyed out with no reason says less.
Each page that reads a tool this host may not have opens on its install where it is missing
(`network/install.tsx`: the package's name, what it would do here drawn as the product, and the
Packages page's install job streaming under the button).

## 16. Two registers

Everything above §15 describes a page that **reports**. The Overview, the metrics page, the Docker
lists, Security, the proxy tables, Processes, the audit log: the reader arrives to find out what is
true, and the system is tuned for exactly that. It removes decoration and hands back three loud
things in exchange — a 24px figure, a tone on a reading, a `Status` dot — and every one of them
requires the page to have numbers on it.

A page where the reader is **deciding** has almost none, and spends what it has at the wrong rung.
The 2026-09-20 pass on `/deploy/new` applied §15 and produced a screen on which the 24px step fired
exactly once — the two-word title — against fourteen times on the Overview; both buttons were
`variant="outline"`, so the only brand ink on the page was a 14px icon on the active tab; and
twenty-two repository rows were hand-laid without the arrow `Row` draws through `rowReveal`, so a
page of click targets looked like a read-only listing. The complaint that arrived — "no life,
doesn't look good" — was right.

**But not because the page had no readings.** It had four: the draft count, the App's repositories,
the identity count, and "N of M repositories". Every one was rendered at `text-hint` inside a
`PanelHeader`'s actions. §15 pass 2 names counts explicitly, and `deploy/credentials-page.tsx` — the
same section, also a page you configure rather than read — ran its page heading straight into a
`StatGrid` of `StatTile`s. So the first half of the failure was §15 pass 2 skipped, not §15 being
inapplicable; the HEAD commit that ran the pass audited itself against passes 3, 7 and 9 and never
mentioned 2.

What the second half needed is what this register adds. A flow page keeps §15 pass 2 wherever it has
a genuine set of headline figures — being in register B is not an exemption from readings — and adds
the things a sequence needs that no amount of correctly-applied §15 would have produced: a question,
a spine, a foreground, and a command.

So there are two registers, and a page declares which it is in.

```tsx
<Page register="flow">   // the reader is deciding
<Page>                   // the reader is reading — the default, and most of the product
```

It is stamped on the page element as `data-register` rather than inferred, so which register a page
is in is one grep, a page cannot be half in each by accident, and a browser test can assert that only
the flow pages carry the flow affordances.

### Which register a page is in

The test is what the reader came to do, not what the page contains. A page with a form on it is not a
flow page; Settings is a reading of a project's configuration that happens to be editable. A flow
page is one where there is a **sequence with an outcome at the end**, and the screen's job is to get
the reader through it.

| Page | Register | Why |
| --- | --- | --- |
| Host Overview, metrics, Docker, Security, Network, proxy, Processes, System, Backups, Packages, audit, Git, files, terminal | Reading | The reader arrives to find out what is true. |
| Deployments list, a project's overview, runtime, logs, deployments, requests | Reading | A project that exists is a thing you read. |
| `/deploy/new` — the source chooser | **Flow** | Step one of three, and the screen is asking a question. |
| Databases — the control center, the map, a database's home, Search, Generate, Performance, Advisor, Access, Backups, Settings | Reading | The reader arrives to find out what is true of a server and of everything on it. |
| A database's Data, Query, Schema, Diagram and Logs | Reading, as a workbench | The reader works rather than scrolls, so the page is one frame held to the window (`<Page fill>` through `SectionFrame`, which takes the fact from the engine registry). A workbench is a layout of this register (§2), not a third one: same grounds, same type ladder, no flow panel. |
| `/databases/new` — add a database | **Flow** | A question with an outcome: which database, started here or connected, ending in the one command that does it. The section's only flow page. |
| Any page with a run of *choices* on it | either | The register is about the page; the lit choice is about the thing. A reading page with a picker on it — the generators on a database's Generate page — gets the edge on that picker and changes in no other way. |
| `/deploy/new` — Configure | **Flow** | Step two of three, ending in the one command that creates the project. Each of its four screens is the run page's rail and inspector before the run: the plan read down a rail beside the one focused surface holding the step's fields. |
| A run in progress (`/deploy/[id]/runs/[run]`) | Reading | You are *watching*, not deciding. The page opens on the project header's shape saying what the run is, its verbs — Cancel, Retry, Redeploy, Visit, the release's menu — at the far end beside its state, and nothing above it: the sequence on `/deploy/new` ended when the project was created, so a first run is read the same way as the fortieth, with no spine claiming the screens before it. |
| Deploy settings, credentials, notifications | Reading | Editable readings of state, not a sequence with an end. |
| Sign-in, first-run setup | **Flow** | A sequence with an outcome. |

A section does not pick a register — a *page* does. The deployment section has pages in both, which
is correct: making a project is a flow and reading one is not.

### What register B adds

Each of these is bought against a specific failure, and each is the smallest thing that answers it.

- **The question is the `h1`.** A flow screen asks something — "What are you deploying?", "How should
  it run?" — and the ask is at the page's own rank. It is *not* a sentence under the page's name:
  that is the caption §5 removed from every page in the product, and putting one back at a rank
  invented for it is how the ladder grew thirteen sizes the first time. The step count lives in the
  eyebrow or in the spine, never in a new size between 24 and 15.
- **A spine.** `FlowSteps` says which step this is, what is behind it and what is left, as segments of
  a rule with their names under them — the way `deploy/run-pipeline.tsx` draws the seven stages of a
  release, so the screen where a project is planned and the screen where it is built agree about how
  "where we are" is drawn. **Never numbered circles**: a filled circle under 32px with a character in
  it is the pill §4 deleted, and `tests/browser/design-system.spec.ts` fails any page that renders
  one. Credentials was the last to draw them, for the GitHub App's three setup steps; they are
  three equal segments of the release path's own bar now — green and ticked when done, amber while
  the App waits to be installed, the brand for the step that is simply next — and the spec checks
  that page and Notifications with the showcase's App, accounts and channels in them. Where a spine
  is drawn it *is* the head rule, and `--flow-rule` yields to it.
- **Exactly one surface with depth.** §2 says nothing lifts, and the reason is that forty-nine
  surfaces all claiming the foreground is a page with no foreground. One does not have that problem.
  `FlowPanel` is the thing being decided right now: a step of ground above the card, `border-strong`,
  and `shadow-sm` — the smallest step, and the only place outside a popover, a dropdown and a dialog
  that takes any. **One per screen.** A second is two foregrounds, which is none.
- **One unmistakable advancing gesture.** Either a brand-faced command — `Button` with no variant,
  drawn in `FlowActions` at the foot of the focused surface — or, on a screen where *the choice is
  the advance*, the choices themselves, whose edges light under the pointer. What a flow screen may
  never have is what the Git tab shipped with: a primary action wearing `variant="outline"` while
  nothing else on the page carries the brand either.

  **One, not two.** The Git and Docker tabs each pair a list with a fallback field — paste a
  URL, name a registry image — and the fallback carries a button. While the list has rows in it the
  rows are the advance, so that button is `outline`: a brand face there is the only blue on the
  screen pointing at the secondary path. When the list is *empty* there is nothing to choose, the
  fallback becomes the way forward, and it takes the command face. So the variant is a function of
  whether there is anything to pick —
  `variant={pickable.length > 0 ? "outline" : "default"}` — which is the rule stated in code rather
  than a colour chosen once and left to be wrong half the time.
- **A choice is something you pick.** `ChoiceCard` in a `ChoiceGrid` for the *kinds* of thing — the
  four sources, the templates, the databases. `ChoiceRow` in a `ChoiceList` for *instances* of one
  kind — twenty-two repositories, a page of image tags. The split is load-bearing: a three-column
  grid of twenty-two 13px names is a wall, and a flat row is the listing this register exists to stop
  a decision from looking like. Both keep §12's shape — the title is a real `<button>` carrying the
  verb in its accessible name, the surrounding press is a convenience for the pointer — and add the
  two things a reading row does not have: an arrow that is always drawn, and an edge that answers the
  pointer.
- **The lit edge.** `ui/spotlight-border.tsx` paints a card's one-pixel border with a radial gradient
  centred on the pointer, brand at the centre and the card's own `--border` a couple of hundred pixels
  out. It is Magic UI's `magic-card` with the glow taken out of it. Depth and response; no decoration.

### Three grounds, one ladder

The focused surface began two steps above the card and read as a grey slab laid over the page rather
than as the page's own foreground — at L 0.226 against a 0.145 ground it was the lightest thing in
the product. The border and the lit edge do the separating, so the ground only has to be *a* step:

| Token | L | Is |
| --- | --- | --- |
| `--background` | 0.16 | the page |
| `--choice-surface` | 0.183 | a card you pick — recessed *into* the surface holding it |
| `--flow-surface` | 0.2 | the one focused surface on a flow screen |

The order matters and is easy to get backwards: choices sit **below** the panel that holds them, not
above it. Painting both from one token — which is what shipped first — made every card inside a
`FlowPanel` invisible against it.

### The lit edge is not register B's property

This is the part that decides how the rest of the product changes. The lit edge belongs to **things
you pick**, wherever they are — the deploy chooser, a database engine on Add a database, a credential
kind on a settings page. It is not a flow-page decoration, and `ChoiceCard` carries it for every caller
rather than the deploy pages having a better-looking version of a shared component.

What it does **not** belong to is a row you *read*. A reading page answers the pointer with
`bg-row-hover` and nothing else (§6), and that stays true, because the edge means *this is takeable*
and a table of forty readings where every line glows is a page that means nothing by it. So when a
reading page is revamped:

- a list whose rows are **destinations or choices** — a run to open, a project to enter, an engine to
  start — becomes a `ChoiceList` of `ChoiceRow`s and gets the edge;
- a table of **readings** — the audit log, a metrics grid, a process table — keeps its hairlines, its
  density and its wash. Twelve columns of readings do not become cards. The containers table was the
  example here until 2026-09-23 and is the case that shows where the line is: its cells were live
  readings, but every row opened the container's own page, so the row was a destination with
  readings on it — which is the Git card's shape, not a table's (§12);
- a **figure** is still a `StatTile` (§15 pass 2) in either register.

A page that is mostly readings with one run of choices in it takes the edge on that one run. That is
the honest answer to "use it everywhere": everywhere something is picked.

The deployment section is the widest application of it so far, and it draws the line in both
directions on one page. Everything there that opens something is a lit card — a project, a run, a
credential or a channel that opens its sheet, a service that opens its container, a domain on
Runtime that opens the proxy site serving it, a webhook, a schedule, an approval or a preview, a
variable, a linked database — and so is every run of choices inside a sheet or a dialog: a provider,
a channel's service, an alert's measure, a schedule's action, the commits a version can be deployed
from, whether a database is created or an existing one used, the saved connections to take. The
traffic-alert rules on Automation are the counter-example on the same page: a rule is read against
its threshold, with a `Meter` carrying a tick where the line is, and opens nothing, so its rows stay
rows you read.

### What register B does not get

The bans are not relaxed here. No glass, no glow, no gradient ground, no hover-transform, no shadow
on anything that is not the one focused surface, no pill, no badge, no icon plate, no off-ladder type
or radius, no colour that is not a token. §2 §4 §8 §9 §13 §14 hold in both registers without
exception. What §16 buys is depth on one surface, a mandatory command face, and an edge that responds
— and nothing else. A flow page that also wants a gradient behind its hero has misread this section.

`--flow-surface`, `--flow-lit`, `--flow-lit-soft` and the `flow-rule` utility are register B's whole
palette, and a reading page must not grow a use for them.

## 17. Redesigning a flow page

§15 is the reading register's ordered passes. This is the other one. The reference is `/deploy/new`.

1. **Declare it.** `<Page register="flow">`. If you cannot name the sequence and its outcome in one
   sentence, the page is a reading page and §15 is what you want.
2. **Ask the question.** The `h1` becomes what the screen is asking. The page's name moves to the
   eyebrow beside the back link. No sentence under it — §5 holds.
3. **Draw the spine.** `FlowSteps`, with the steps taken from the state machine underneath rather
   than invented: `/deploy/new`'s three are the draft's own `source` → `configuration` → commit, so
   the spine cannot drift from what the server thinks is happening.
4. **Pick the one focused surface.** The thing being decided on this screen becomes a `FlowPanel`.
   Everything else on the page stays a `Panel plain` — supporting facts do not get depth, and a
   second `FlowPanel` is the pass failing.
5. **Find the command.** One brand-faced button in `FlowActions`, or choices whose edges light. If
   the screen has neither, the reader cannot tell what advances it.
6. **Choices become choices.** Kinds → `ChoiceGrid` of `ChoiceCard`. Instances → `ChoiceList` of
   `ChoiceRow`. A list of things you can pick is never a `RowList`. A run of kinds longer than a
   screen is shelved the way the reader looks for one — `FilterChip`s with counts over the shelves,
   the Git page's filter strip — and laid out as `ChoiceGrid columns="fill"`, whose rows are equal,
   with the card's hint clamped to two lines beside a `logo`: sixty cards at three heights read as a
   grid that failed to load. The strip that picks between kinds of source is a `role="group"` of
   pressed buttons, never a tablist — a tablist must own tabs, and these are four toggles for one
   answer — and on a phone it runs to the screen's edge, where the source cut off is the cue that
   there are more, as a `ChipStrip` does; the scroll shade is drawn in the page's own ground and
   cannot show on it.
7. **Motion on the state change, not only on arrival.** §11's four still apply and no fifth is added:
   the spine's current segment, a `BorderBeam` on a card while its work is in flight, a
   `NumberTicker` on a figure that settled, `Confetti` once when the outcome lands in front of the
   reader. On `/deploy/new` the beam is the repository or image row being inspected
   (`ChoiceRow busy`), beside its *Importing…*, so the row that was pressed is the row that answers.
8. **Hold it to the window.** A flow screen is decided in one view: `<Page fill="xl">` holds it to the
   window at `xl`, the question, the spine and any strip stay put, and what scrolls is the one list or
   form longer than the space left — inside its own surface, under its own toolbar and above its own
   command. A surface with no inner scroll is a surface the page now clips, so each one is capped
   (`max-h-full`) and scrolls itself. A surface that scrolls also clips whatever its contents draw
   outside themselves — a field's focus ring four pixels out, a quiet fold's wash twelve — so a
   scrolling column of fields pays that bleed and takes it back (`-mx-3 -my-1 px-3 py-1`), as a
   scrolling list pays its rows'. `/deploy/new` is the reference: every source is the same two
   columns (the focused surface, capped at the window's height with `max-h-full self-start`, and a 22rem
   column beside it), unfinished setups moved from a block above the strip into a counted button beside
   the question, and a Configure step with more settings than fit scrolls its fields between the heading
   and Continue. The Database tab shares `/databases/new`'s shelved engine catalogue and settings
   panel: each column scrolls independently, with the Create command held at the panel's foot. Below
   `xl` the columns stack and the page scrolls as every other page does — a phone is not a window to hold. `deploy-new.spec.ts` asserts the shell does not scroll at 1280×800 on every
   source and every Configure step.
9. **Verify.** `scripts/test-changed.sh`, then screenshots at 1280 and
   1720 — and look at them. The failure this register exists to catch is one no assertion sees.
