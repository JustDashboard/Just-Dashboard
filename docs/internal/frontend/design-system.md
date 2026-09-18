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
there was nothing unframed left to separate from. The ground went darker (`--background` 0.105,
`--card` 0.128) so the one step still reads, and the default flipped: **a block on a page is plain
unless it can say why it needs an edge.** The host Overview ended 0.6.7 with no framed block at all
above its Services row and none in it — see §15 for what that took. Three kinds of thing stopped
taking a frame:

- a run of figures — `StatGrid` draws hairlines *between* tiles and nothing around them, and the
  first column starts on the page's own edge, in line with the title (`framed` restores the box for
  the one case a run sits inside another surface). The first-column rule is written twice, for a tile
  inside a `StatLink` and for a tile that *is* the cell: the descendant form alone never matched the
  second, so until the Security pass every grid of bare tiles started a step in from the title it was
  meant to line up with. The Overview's Services row is one of these too:
  a module's headline figure is a reading, and eight framed cards under a page that had just stopped
  drawing boxes were eight boxes. Each is a `StatLink`, so the arrow says it goes somewhere;
- a list that is the whole of a section — `Panel plain` keeps the panel's anatomy (header, toolbar,
  body, footer) and drops the border and ground, so a title and a hairline mark the block. Recent
  activity and the last-hour sparklines on the Overview, every chart, list and hardware reading on the
  metrics page, every block on the Docker pages (the
  overview's idle containers, attention, compose projects, cleanup and disk; the containers, images,
  volumes, networks, stacks and events lists with their toolbars; the disk breakdown above the
  images; the attention and storage blocks inside a container's detail panel), the
  whole of the Security section (the overview's exposure facts, five area tiles and findings, and on
  every area page the readings, the findings under them, the tables and the twenty probe blocks on
  Tools), every block on the proxy pages (the overview's engine facts, attention list and sites; the
  sites, certificates, streams and ports tables with their toolbars; the TLS report's readings,
  findings, protocol, certificate, chain and HTTP rows; the password files and DNS provider lists),
  health findings, the runtime-health bar, the Overview's last-hour sparklines, and the
  deployment pages' lists, overview facts, run summary and create flow are plain, and so are the
  databases section's connection facts and maintenance rows, its find, monitor and generate panels,
  and every block on the four Processes pages — the live table, the PM2 applications, the systemd
  units, and the cron jobs, timers and system cron files on Scheduled, each a title, a toolbar and
  a hairline under four `StatTile` readings, with a detail sheet built from plain panels — and the
  two System pages follow the same shape: the accounts table on System users under four readings
  (accounts, who can sign in, who is locked, the last sign-in), with its SSH-keys sheet a plain list
  of rows and a plain form, and the audit log's table under its filters, where a request's outcome
  is a `Status` dot and the code rather than a wash across the row; and
  both of the dashboard's own pages — the update in flight and the version history on Settings,
  and the two forms, the restart record and the switches (an `OptionList`) on Configuration, whose
  install paths moved out of a framed drawer into a row of facts under the title; the Backups page —
  four readings, an attention list, the jobs as a plain table under a hairline and the coverage list
  under its filter chips, with a job's sheet built from a fact list and plain panels; and the three views on
  Packages — the installed and updates tables and the software search, under one underlined strip
  (`tabClasses`) rather than a filled tab list — each a toolbar, a hairline and rows on the page's own
  edge, with what needs acting on (security updates waiting, a reboot owed, a stale index) said as a
  `Notice` that carries its own button rather than as a framed block with a header and nothing in it, and the Git page's repository list under its four readings (its workspace is one framed
  workbench of three `Pane flush` columns with a strip across the top, the way the terminal page is
  drawn);
  its Browse, Structure and Diagram tabs are each one `Pane` — a working region sized to the window,
  a rail or an inspector beside a grid or a canvas, with a hairline between the columns. A table
  inside a plain panel bleeds by its cells' own padding (`-mx-4` around the `Table`) so its first
  column starts where the title does; a row laid out by hand takes `ROW_BLEED` from `row-list.tsx`,
  which is the same three classes `Row` applies to itself;
- a panel's header — it is no longer a tinted strip. The title sits on the panel's own ground with a
  hairline under it. `--surface-header` survives at a fainter mix for the two places a strip is still
  chrome: a `Pane`'s header and footer.

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
borrowed for anything that is not a reading of state.

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
come back by accident, and a Playwright test asserts no filled, fully-rounded element with text
renders on any page.

- A **count** is `.numeric` text next to its label.
- A **state** is `Status` — a coloured dot (or an icon) and a word. No border, no fill.
- A fixed **property** of a row is `Tag` — **small caps text, and nothing else**. No border, no
  ground, no padding. The chip is gone: 10px caps inside a hairline box is a container three times
  the height of the word it holds, and beside the 13px title where most of the 117 of these sit, the
  box read as the louder of the two. A `mono` tag keeps a quiet recessed ground because its contents
  are literal strings from the host — a cipher, a port map, a config hash — which run together
  otherwise and which small caps would corrupt.
- Anything longer than three words is **prose**, and belongs in the row's secondary line or a `Notice`.

A tag that annotates a **row** belongs at the row's edge, not against its title: ten of them
interrupt ten sentences at ten different points, and in a column they are a column.

`components/tone.ts` exports the one severity union: `"default" | "success" | "warning" | "danger"`.
`Verdict` keeps `critical` because that is what the backend sends and `Button` keeps `destructive`
because that is what the control does — but anything choosing a *colour* chooses from the shared
union. A component that declares its own tone names is the drift this file exists to stop.

## 5. No descriptions under titles

**`PageHeader`, `Section`, `PanelHeader` and `ChartPanel` have no `description`.** They have no `icon`
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

Reveal only where the controls share their space with something else. Where they have a column of
their own — the containers table — use `DimActions` instead: always drawn, one step of opacity until
the pointer is on the row. A reserved column left empty reads as a layout bug, not as an affordance.

## 7. Which surface

| Surface | Is | Is not |
| --- | --- | --- |
| `Panel` | A block of content *on* the page: framed, header, hairline, body — or `plain`, the same anatomy with no frame | Not a working region |
| `RowList` / `Row` | A list of rows with hairlines between them: a leading mark, a title, a second line, a trailing state | Not a table — nothing lines up in columns |
| `Pane` | A sized region of a workspace that owns its own scrolling — session rail, file tree, log console. `flush` drops its frame for a pane that is one column of a workbench sharing a single frame, with a hairline between columns (the terminal page, the logs page's source rail beside its log workspace, and the files page's sidebar, listing and inspector) | Not a block in a page's flow |
| `Well` | Output you read: command output, a log tail, a diff, a stored secret | Not a fence around controls |
| `Group` | A fence around part of a body: a set of ports, one release task, a repeated form row | Not a `Panel` — no header, no lift |
| `StatTile` | One headline figure, in a `StatGrid` | Not free-form — a row of them is read as a table |

**Reach for `Panel`/`Pane`/`Page`, and add a variant there rather than a one-off in a feature page.**
Before these existed, fourteen pages read as fourteen products.

`Modal`, `PaletteModal` and `SidePanel` are the only assemblers of raw `Dialog` and `Sheet`, and
`no-restricted-imports` enforces it. A page never opens one itself.

**What goes inside a task surface is `components/form.tsx`.** A `Field` is a label at `text-body`,
a control, and one line under it — a hint, or the error while there is one; `FieldRow` puts two or
three side by side; `FormSection` opens a part of a longer form with an eyebrow and a hairline;
`OptionRow` is a switch with its sentence, because "Stop on error" as a checkbox label asks the reader
to guess what an error stops; `FormFacts` states what the form operates on as data under the title;
and `Statement` shows the SQL a schema-editing form is about to run, nearest the button that runs it.
The databases section's dialogs had assembled their own forms out of `Label`, `Input` and a
`space-y-1.5` div and arrived at three label sizes, two input heights and no way to write an error
beside the field that caused it.

## 8. Type

The face is **Satoshi**, self-hosted. The variable files sit in `src/app/fonts/` and
`next/font/local` loads them in `app/layout.tsx`, which emits them as `--font-satoshi`; `--font-sans`
in `globals.css` puts it ahead of the system stack, which stays behind it as the fallback. Nothing at
build or run time reaches out to a font CDN — this product is built and run on locked-down networks,
which is why the face was self-hosted rather than fetched, and the licence file travels with the
files.

The ladder is `text-micro` (10) → `text-hint` (11) → `text-xs` (12) → `text-body` (13) →
`text-sm` (14) → `text-title` (15). An arbitrary `text-[Npx]` is a departure from it, and the linter
says so.

**`text-title` is what a surface calls itself**: a `PanelHeader`, a `Section`, a `Modal`, a
`SidePanel`. It was `text-body` on the first two, which put a panel's name at the size of the rows
underneath it — legible only because a tinted icon square was sitting in front of it doing the
separating. With the square gone (§14) the title has to be the thing that reads as a heading, and two
pixels is the whole of what that takes: `text-title` is a step the ladder already had, so the ranks
now run page (24, `text-2xl`) → section (16, `text-base`) → surface (15) → body (13) with nothing
invented in between. The page title went from 20 to 24 in 0.6.7: at 20 it sat five pixels from the
panel titles it ranks above, and on the darker ground the page needed one thing that is plainly the
largest. A `StatTile`'s figure is the same 24, because a headline number is the other thing a reader
finds without reading.

Weight carries hierarchy where size cannot. The sidebar is the one surface dense enough to need
three: group labels in the eyebrow's small caps, resting entries `font-normal` so the column reads as
a list rather than as forty-nine headings, and the current entry `font-medium` alongside its accent
fill and brand-blue icon.

A **view strip** — the underlined tabs that switch between two readings of the *same* page — is
`text-body`. It was `text-xs` while the page title was 20px and the strip sat within two pixels of both
the title and the panel titles; with the title at 24 the strip has a rank of its own again, and 12px
chrome under a 24px title read as an afterthought. There is no route-level strip to size: since 0.6.7
the sidebar drills into a section and lists its pages, and a tab that changes the URL is not a thing
this product has.

A **table header** is `text-hint`, medium weight, muted — not the eyebrow's small caps. At 10px
tracked-out caps a nine-column header was the loudest line in the table, above rows it exists only to
name.

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

## 10. Charts

`components/metrics/` is a third design-system file in all but name. Every chart goes through it
rather than assembling its own recharts tree — adding a measurement should mean naming a series.

- The x-axis is **numeric over time**, never a category axis of pre-formatted labels: a category axis
  spaces every bucket equally, which lies whenever the record has a hole in it.
- A series with no numbers anywhere in the window is **dropped rather than drawn flat at zero**.
- **Live and recorded data are never spliced into one line** — the cadences differ by two orders of
  magnitude.
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
never reach its final frame still ends up there.

## 12. A table is a layout, not a contract

A wide table narrows by dropping columns, and that is the right answer until it is not. Past roughly
half of them the reader is no longer looking at a table — they are looking at the remains of one: a
wide first cell, a wedge of empty space, and two stubs where seven columns used to be.

**When more than half the columns would go, replace the layout instead.** The containers table is
nine columns wide and keeps three on a phone, so below `xl` the same rows are drawn down the row
instead of across it (`components/docker/container-card.tsx`). The test of that replacement is that
**nothing was dropped to achieve it**: the image, the ports, both live readings and the issue count
are all present, because a phone is where somebody checks whether the thing they just deployed is
alive and every one of those is part of that answer.

**The breakpoint is where the table stops fitting, not where the viewport stops being wide.** This
one swaps at `xl` (1280) rather than `lg` (1024), because the sidebar takes 256px of it: at 1024 the
table appeared already scrolling inside its own panel, with Issues and the row's actions past the
right edge — a table that arrives broken. The ninth column waits for `2xl` for the same reason.

It is still a row, not a card: no frame of its own, a hairline between it and the next, a wash under
the pointer. Two nested frames spend sixteen pixels of a 390px viewport saying what the panel's own
edge already said.

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
- **Everything else behind one overflow menu**, where each verb carries its word *and* a line of
  plain English underneath. The menu is not where things are hidden — it is where a verb gets a
  sentence, which is the only form most of them are usable in.
- **The verbs themselves are declared once**, as data, and a surface decides only how many it has
  room to draw. `components/docker/container-actions.tsx` is the pattern: three surfaces used to hold
  three different answers to "what can I do to this container", and they disagreed about which
  capability each needed. `components/verbs.tsx` is that pattern made shareable — a `Verb`, drawn
  by `VerbActions` in a row (inline icons and one menu), `VerbBar` in a sheet (named buttons and
  the same menu) and `VerbMenu` alone in a header — and the process, PM2, unit, timer and cron rows
  all draw theirs through it, so four kinds of row did not arrive at four menus. A process row keeps
  nothing inline: the daily verb on a process table is reading it, and a stop glyph beside four
  hundred rows is four hundred invitations to end something by mis-click, so Terminate and Kill are
  named buttons in the sheet and words in the row's menu.

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
   a `Pane` (a working region with its own scrolling), a `Well` (output you read), a `Group` (a fence
   inside a body), or something that genuinely floats (popover, dropdown, dialog). "It is a card" is
   not a reason. A block that is the whole of a section is a title and a hairline. Two blocks side by
   side are both plain; the gap between them is the separation. A framed block that survives this pass
   has a sentence in a comment saying why.
2. **Figures are tiles.** Any set of headline numbers — utilisation, counts, one-per-module "service
   cards" — is a `StatGrid` of `StatTile`s: hairlines between, nothing around, the first column on the
   page's own edge. A tile that is a destination is wrapped in `StatLink` and takes
   `className="h-full transition-colors group-hover:bg-row-hover"`; the arrow is the link's, not yours.
   The figure is 24px (`text-2xl`): `text-xl` is not on the ladder. A state colours the figure through
   `tone`, never through a badge beside it.
3. **Lists are rows.** `RowList`/`Row` for things with a title and a second line, `FindingList` for
   verdicts, a table for columns. Never a grid of framed cards standing in for rows. A scroll container
   that holds plain rows pads by the rows' bleed (`-mx-3 px-3`), or it grows a sideways scrollbar.
4. **No decorative glyphs.** Headers, sections, tiles and modals carry no icon (§14). The glyphs that
   stay are wayfinding — the sidebar entry and a module tile's mark, drawn as a 12px `text-brand`
   glyph inline before the tile's eyebrow — and the ones that *are* the message: a `Status`, a
   `Notice`, an `EmptyState`, a verb on a button.
5. **Type on the ladder.** Page 24 → section 16 → surface 15 → body 13 → hint 11 → micro 10, and a
   headline figure 24. Anything in between is deleted, not rounded to the nearest.
6. **Colour by role.** Brand blue is a command's face or a location mark. Amber, red and green arrive
   only attached to a reading — a figure's `tone`, a `Status` dot and word, a `Notice`. Selection is
   `bg-accent`; hover is `bg-row-hover` on a row or tile and a border step on a framed destination;
   never both, never movement.
7. **Motion says one of four things** (§11). Add `animate-rise` where a fetch settles: the page once,
   each block once, a figure once (swap its `key`). `transition-colors` on hover. Nothing else moves.
8. **Data, not captions.** No sentence under a title (§5). What the reader needs is a `Tag`, a
   `Status`, a hint on a tile, or a `Notice`. What the page *is* — a hostname, a kernel, a platform —
   is its own row.
9. **Alignment.** Tiles top-align so a row of names is a row; hints truncate rather than wrap; the
   first column starts where the page title starts.
10. **Verify.** `bun run lint`, `bun run build`, `bun run test:browser` (at least
    `tests/browser/design-system.spec.ts`), then a screenshot at 1280 and 1720 wide against a mocked
    API in the pattern `mockShell` uses, and look at it: a scrollbar where none belongs, a label a
    line lower than its neighbours, a figure at the wrong size, are things the checks do not catch.

**What the Overview looks like after these passes**, as a checklist for the page you are on: a page
header with an eyebrow, a 24px title and a `MetricStrip` of figures on the right; a row of facts under
it; a five-tile `StatGrid` of readings; a plain `Health` list; a plain sparkline block beside a plain
activity list; and a `Section` holding a `StatGrid` of eight `StatLink` tiles, one per module. No
frame anywhere on the page. Everything that arrived, rose.
