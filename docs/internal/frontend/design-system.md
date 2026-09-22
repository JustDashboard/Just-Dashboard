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
  images; the attention and storage blocks on a container's page), the
  whole of the Security section (the overview's exposure facts, five area tiles and findings, and on
  every area page the readings, the findings under them, the tables and the twenty probe blocks on
  Tools), every block on the proxy pages (the overview's engine facts, attention list, sites and
  certificate expiry; the sites, certificates, streams and ports tables with their toolbars; the
  TLS report's readings, findings, protocol, certificate, chain and HTTP rows; the password files
  and DNS provider lists),
  health findings, the runtime-health bar, the Overview's last-hour sparklines, and the
  deployment pages' lists, overview facts, run summary and create flow are plain (the framed
  blocks in the deployment section are its two *pictures*: the GitHub App on Credentials — the
  accounts that installed it, the App and this server — and where an outcome goes on
  Notifications — every deployment on the left, a mark per channel on the right, dashed rings
  for the kinds not yet added; each draws its lines with `ui/animated-beam` from the marks' own
  positions, and the line is the state: dotted before the thing exists, still while it is
  paused or not installed, a brand-to-signal pulse travelling along it while it carries —
  because a picture needs an edge to read as one thing. The same vocabulary
  (`components/deploy/wire.tsx`) draws one more picture that sits *unframed* because it is
  inside a block that already has its edge: the way a request reaches a project on the overview —
  source, live release, runtime, domains — in the column beside the preview; each mark paints the
  ground under its tint so the line never shows through it. The release path on a deployment page
  is not a wiring picture but a timeline: one bar in seven segments, each as long as its stage
  took, `components/deploy/run-pipeline.tsx`), and so are the
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
  product is not a new component.

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

**An action on a row is laid out, never overlaid.** The draft list positioned its discard button
`absolute right-2` against the `<li>` while the row reserved space for it with `pr-12` against a box
that `ROW_BLEED` had made twelve pixels wider on each side — so the button sat on top of the last
reading in the row. The arithmetic is not the lesson: two boxes measured against different origins
will drift again the moment either one's padding changes. A second action belongs in a slot in the
row's own flow (`ChoiceRow`'s `actions`), where there is nothing to mis-measure, and it must stop the
press from reaching the row around it — discarding a draft used to navigate to the draft it had just
deleted.

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
beside the field that caused it.

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
control for an option in a list of options, where a sentence names it and it answers. A group sits
*inside* a `Field` and takes its label, its hint and its error; it does not replace one.
**Every rule inside a group is the addon's**, drawn for each child after the first — a call site
adding its own `border-l` to the button it puts there gets two pixels where §2 asked for one, which
is what the environment rows shipped with while the hostname field a section above them drew the
same divider correctly.

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
section's `FormNote`, where it is said once.

`hint` stays on the component, and §5's second exception is why: the ambiguous-candidate rows on
Configure pass `high confidence · Next.js via recipe in apps/web`, which is **data** about the option
rather than prose about the switch, and data stays. Roughly forty-five option rows elsewhere in the
product still pass prose; they are not wrong so much as not yet done.

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

A **form runs four ranks**: section (15, semibold) → option (14, medium) → field label (13, medium)
→ hint (11, muted). `FormSection` opened with the eyebrow's 10px muted small caps, so a Configure
screen of nine sections set every head *quieter than the 13px field labels it opened* and the
loudest line in each block was a label — the same argument the paragraph above makes about a panel's
name, one rung down and one degree worse. The head went to 14 first and 14 was not enough: an
`OptionRow`'s title is 14 so that it outranks the fields it governs, which left "Public address" and
"Publish on a public hostname" two lines apart at one size with a weight step between them and
nothing else. At 15 the four steps are visible and every one of them is a rung the ladder already
had. A `Disclosure` takes the section's rank, because a fold is a section (§7).

A **table header** is `text-hint`, medium weight, muted — not the eyebrow's small caps. At 10px
tracked-out caps a nine-column header was the loudest line in the table, above rows it exists only to
name. It is opaque, so rows scroll *under* a sticky header rather than through it, and the ground it
is opaque with is `--panel-ground` — declared by the panel (`--card` framed, `--background` plain),
never assumed by the table. Reading `--card` there put a faint unexplained band across every table on
a plain panel, overhanging the header hairline by the table's own `-mx-4` bleed.

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

### Motion that arrived with a library

The deployment section brings in registry components — Magic UI's `ui/animated-beam`,
`ui/border-beam`, `ui/blur-fade`, `ui/number-ticker` and `ui/confetti`, and Motion
Primitives' `ui/text-shimmer` — each rewritten onto the tokens and each saying one of the four things
above:

- *arrived* — `BlurFade` staggers the fleet's cards by a beat each; `NumberTicker` counts a figure up
  to its value on the Credentials readings, the delivery insights and the overview's live usage;
- *live* — `AnimatedBeam`'s pulse on a line, `BorderBeam` running around a project card while a run
  is in progress, and `TextShimmer` lighting the name of the stage a release is at, are `breathe`
  for a link, a frame and a word: a reading that is happening now;
- *once* — `Confetti` fires only when a release goes live in front of the reader, never on arrival.

Magic UI's `animated-circular-progress-bar` was tried beside the release path and removed: a ring
saying "100%" next to a header saying "Ready" was the same fact twice, and the timeline now shows how
far a run is by how much of the bar has coloured. Its `safari` device mock was evaluated for the
website preview and not adopted — its chrome is drawn for a hero, and at tile size the address bar's
text is too small to read — so the preview draws its own strip and shrinks a desktop-width frame.

Every one of them honours `prefers-reduced-motion` in JavaScript, because the root CSS rule cannot
reach a JavaScript-driven animation.

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
  by `VerbActions` in a row (inline icons and one menu), `VerbBar` in a sheet or a detail page's
  header (named buttons and
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

`Notice` kept the last one, and it is gone too. A notice's glyph is allowed — a severity is a thing a
shape can say, which is the exception below — but the 28px tinted square around it never was. On a
toned banner it was the fourth tinted object in a box that needed one: a wash, a rule, a plate, and
the glyph on the plate, all the same hue, to say that a certificate was fine. The glyph now stands on
the banner's own ground at the size of the title's line, and the banner is `rounded-lg` — §9's fence
step, beside `Group`, which is what it is — rather than the block step beside `Panel`, which it is
not. **And a `Notice` is for what the reader has to act on.** `/deploy/new`'s public address drew
three: one saying HTTPS was ready, which repeated the field's own hint word for word; one saying a
certificate would be issued, which nobody has to do anything about; and one saying automatic HTTPS
needed attention, which is the only one of the three that asks for a decision. A state is a `Status`
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
Markdown's ink, C's grey) are invisible on a 0.16 ground. The rule is written out as twenty literal
`oklch()` values carrying their source hex in a comment rather than stated once as
`oklch(from <hex> …)`: relative colour syntax is the one modern colour function Lightning CSS cannot
downlevel, so those twenty would have been the only tokens in the file shipping without a fallback,
below the floor Next's default browserslist target declares. Deleting the lightness dimension has a
price, and it is paid by the pairs Linguist separated by lightness alone — Lua and Markdown come out
as the same blue. The glyph shapes and the word beside them still tell those two rows apart, and a
floor high enough to be legible on this ground collapses them either way, so the rule stands as
written. This
does not extend to the marks a reader is choosing *between*: the five source kinds on `/deploy/new`
stay muted with the current one in `--brand`, because there the colour is saying which one you are
on (§3), and twenty hues in a row of five would be saying nothing.

**The same argument buys the git surface its own glyph set.** Heroicons draws no branch, no commit
and no pull request, so `icons.tsx` maps those words onto the share, hash and chat-bubble marks —
near enough on any other page, and wrong on the one screen where the reader identifies the thing *by*
the drawing. `components/git/glyphs.tsx` takes six from Material Design Icons, which is already the
dependency `files/file-icon.tsx` reaches for wherever Heroicons has nothing to draw. Nothing else is
imported from MDI there: a glyph that exists in both sets stays Heroicons, or the git pages grow a
second icon weight. `AuthorMark` in `git/marks.tsx` is the coloured-wayfinding rule again — a column
of commits where mine and the bot's are two hues is scanned, one where they are the same grey is read
— on the eight fixed `--tag-*` hues the branch graph gives its lanes, and drawn as a square with a
3px radius rather than a circle, because a filled 16px round mark with a character in it is the pill
§4 deleted.

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
   cards" — is a `StatGrid` of `StatTile`s: hairlines between, nothing around, the first column on the
   page's own edge. A tile that is a destination is wrapped in `StatLink` and takes
   `className="h-full transition-colors group-hover:bg-row-hover"`; the arrow is the link's, not yours.
   The figure is 24px (`text-2xl`): `text-xl` is not on the ladder. A state colours the figure through
   `tone`, never through a badge beside it.

   **A page you *configure* is not exempt.** §16 makes this argument about `/deploy/new` and it
   generalises to every settings tab in the product: the deployment Runtime tab ran ten fields, four
   selects and two switches with not one figure among them, so none of the three loud things this
   system trades decoration for could fire, and the screen had nothing on it a reader could find
   without reading. It opens on four readings now — where it listens, what it may use, how it is
   replaced, and what it can reach — and each is a fact the form beneath it sets, drawn from the
   draft rather than the saved revision, because what you are setting is what the page is about.
   A reading is `warning` where the *absence* of an answer is the answer: an uncapped container and
   a port open on every interface are the two facts an operator wants off that page without opening
   a fold.

   **And the one page with no tiles, which is the shape of the argument for dropping this pass.**
   `/git` had four — repositories, uncommitted, behind, unpushed — and the operator asked for them to
   go. Nothing was lost, because every one of those numbers already sat on a filter chip under them,
   and a chip says what is waiting *and* narrows the list to it where a tile could only say it. What
   the tiles also did — put the urgent thing first — is done by ordering the repository cards
   worst-first under a *Needs attention* rule. A page may drop this pass when it can name where each
   figure went and what now does the job the figures were doing; `app/(dashboard)/git/page.tsx`
   carries that in its doc comment, the way a surviving frame carries its sentence in pass 1.
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
same section, also a page you configure rather than read — runs its `PageHeader` straight into a
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
| Host Overview, metrics, Docker, Security, proxy, Processes, System, Backups, Packages, audit, Git, files, terminal | Reading | The reader arrives to find out what is true. |
| Deployments list, a project's overview, runtime, logs, deployments, requests | Reading | A project that exists is a thing you read. |
| `/deploy/new` — the source chooser | **Flow** | Step one of three, and the screen is asking a question. |
| Any page with a run of *choices* on it | either | The register is about the page; the lit choice is about the thing. A reading page with an engine picker in a dialog gets the edge on that picker and changes in no other way. |
| `/deploy/new` — Configure | **Flow** | Step two of three, ending in the one command that creates the project. |
| A run in progress (`/deploy/[id]/runs/[run]`) | Reading | You are *watching*, not deciding. It carries the spine's last step so the sequence still reads as one, and nothing else changes. |
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
  one. Where a spine is drawn it *is* the head rule, and `--flow-rule` yields to it.
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

  **One, not two.** The Git and Docker-image tabs each pair a list with a fallback field — paste a
  URL, name a registry image — and the fallback carries a button. While the list has rows in it the
  rows are the advance, so that button is `outline`: a brand face there is the only blue on the
  screen pointing at the secondary path. When the list is *empty* there is nothing to choose, the
  fallback becomes the way forward, and it takes the command face. So the variant is a function of
  whether there is anything to pick —
  `variant={pickable.length > 0 ? "outline" : "default"}` — which is the rule stated in code rather
  than a colour chosen once and left to be wrong half the time.
- **A choice is something you pick.** `ChoiceCard` in a `ChoiceGrid` for the *kinds* of thing — the
  five sources, the templates, the databases. `ChoiceRow` in a `ChoiceList` for *instances* of one
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
you pick**, wherever they are — the deploy chooser, a database engine in a dialog, a credential kind
on a settings page. It is not a flow-page decoration, and `ChoiceCard` carries it for every caller
rather than the deploy pages having a better-looking version of a shared component.

What it does **not** belong to is a row you *read*. A reading page answers the pointer with
`bg-row-hover` and nothing else (§6), and that stays true, because the edge means *this is takeable*
and a table of forty readings where every line glows is a page that means nothing by it. So when a
reading page is revamped:

- a list whose rows are **destinations or choices** — a run to open, a project to enter, an engine to
  start — becomes a `ChoiceList` of `ChoiceRow`s and gets the edge;
- a table of **readings** — the audit log, the containers table, a metrics grid — keeps its hairlines,
  its density and its wash. Twelve columns do not become cards. §12 already says what a wide table
  does when it stops fitting, and it is not this;
- a **figure** is still a `StatTile` (§15 pass 2) in either register.

A page that is mostly readings with one run of choices in it takes the edge on that one run. That is
the honest answer to "use it everywhere": everywhere something is picked.

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
   `ChoiceRow`. A list of things you can pick is never a `RowList`.
7. **Motion on the state change, not only on arrival.** §11's four still apply and no fifth is added:
   the spine's current segment, a `BorderBeam` on a card while its work is in flight, a
   `NumberTicker` on a figure that settled, `Confetti` once when the outcome lands in front of the
   reader.
8. **Verify.** `bun run lint`, `bun run build`, `bun run test:browser`, then screenshots at 1280 and
   1720 — and look at them. The failure this register exists to catch is one no assertion sees.
