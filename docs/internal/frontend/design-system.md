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
  first column starts on the page's own content edge (`framed` restores the box for
  the one case a run sits inside another surface). The first-column rule is written twice, for a tile
  inside a `StatLink` and for a tile that *is* the cell: the descendant form alone never matched the
  second, so until the Security pass every grid of bare tiles started a step in from the content it
  was meant to line up with. The Overview's Services row is one of these too:
  a module's headline figure is a reading, and eight framed cards under a page that had just stopped
  drawing boxes were eight boxes. Each is a `StatLink`, so the arrow says it goes somewhere;
- a list that is the whole of a section — `Panel plain` keeps the panel's anatomy (header, toolbar,
  body, footer) and drops the border and ground, so a title and a hairline mark the block. Recent
  activity on the Overview, every chart, list and hardware reading on the
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
  health findings, the runtime-health bar, and every block of the deployment section — the fleet
  and its archive, Credentials and Notifications, a project's Overview, Deployments, Logs, Runtime
  and Console, the run page, the nine settings pages and the create flow — are plain, with every
  row in them that is *taken* rather than read (a project, a run, a credential, a channel, a
  service, a webhook, a schedule, a variable, a linked database) a lit card — a `ChoiceRow` in a
  `ChoiceList`, or on the fleet's grid a project's own `SpotlightBorder` card with the same lit
  edge (what keeps a frame there is, first, a *picture*, because a picture needs an edge to read as
  one thing: the GitHub App on Credentials — the accounts that
  installed it, the App and this server — where an outcome goes on Notifications — every
  deployment on the left, a mark per channel on the right, dashed rings for the kinds not yet
  added — and four on the settings pages, each inside its rail section and drawn through
  `settings/setting-picture.tsx` or, for Automation's, `settings/automation/wiring.tsx`:
  General's automatic deployment (the repository, the watch, the deploys), Runtime's where it
  listens (the domain, this server, the container, with an amber *Anywhere* node when the port is
  open on every interface), Databases' way the application reaches its databases (each engine, the
  managed network, the application) and Automation's what deploys it (the watched branch, each
  webhook and each schedule, the project, then production and the pull-request previews). Each
  draws its lines with `ui/animated-beam` from the marks' own positions, and the line is the state:
  dashed before the thing exists or where a hop is missing, still while it is paused, manual or
  only a draft, amber where it works but should not be relied on (a branch that cannot be read, a
  port that bypasses the proxy), red where a link has broken, a brand-to-signal pulse travelling
  along it while it carries. The rollback dialog holds a small one of its own — your domains wired
  to the live release by a still line and to the release you are going back to by a dotted one,
  which is the line that carries once Roll back is pressed. The same vocabulary
  (`components/deploy/wire.tsx`) draws one more picture that sits *unframed* because it is inside a
  block that already has its edge: the way a request reaches a project on the overview — source,
  live release, runtime, domains, each drawn as its product — in the column beside the preview; each
  mark paints the ground under its tint so the line never shows through it. The preview beside it is the
  Overview's one framed block, a tile that *is* the website. Past the pictures, the build console
  and the two shells, Docker's and a game server's, are `Pane`s and a game's raw settings file is
  a `Well`, for §7's reasons; and the Danger zone is one `border-rule-danger` panel, because
  everything inside it changes what the deployment is, so one red edge says "careful" once where
  four red cards said it four times. The release path on a deployment page is not a wiring
  picture but a timeline: one bar in seven segments, each as long as its stage took,
  `components/deploy/run-pipeline.tsx`), and so are the
  databases section's connection facts and maintenance rows, its find, monitor and generate panels,
  and every block on the four Processes pages — the live table, the PM2 applications, the systemd
  units, and the cron jobs, timers and system cron files on Scheduled, each a title, a toolbar and
  a hairline under four `StatTile` readings (Scheduled's are what fires next across cron and the
  timers together, the account's jobs, the timers armed and what the packages run), with a detail
  sheet built from plain panels that opens on the thing's own mark — and the
  two System pages follow the same shape: on System users four readings (accounts, administrators,
  who can sign in, the last sign-in) over the accounts as lit cards in a plain list, because each
  opens its keys, with its SSH-keys sheet a plain list of rows and a plain form, and on the audit
  log four readings of the last day over its table and filters, where a request's outcome is a
  `Status` dot and the code in its family's colour rather than a wash across the row; and
  both of the dashboard's own pages — on Version the identity line, the update in flight and the
  history (a timeline: the version in a sticky column, a rail with a mark per release, the notes at
  a readable measure), and on Configuration the stack (its checkout and three services as a
  `RowList`, beside Restart and Rebuild as two `ChoiceCard`s), the restart record, and the settings
  as `FormSection aside`s whose heads sit in a rail — the framed things on those two pages are the
  transcript console, which is a `Pane`, and the cards you pick; the Backups page —
  an attention list of the jobs that failed or went quiet, the jobs as destination cards (a
  `ChoiceList`, each drawn as the products it covers, with its destination's mark and its last
  fourteen runs as a strip) and the coverage list under its filter chips and a meter of how much is
  covered, every thing on it drawn as its product, with a job's own page built from a fact list and
  plain panels; the five account pages — the profile's identity line, readings and capability rows,
  sessions and keys as rows under plain panels where a framed table used to be, the users as cards
  in a `ChoiceList`, and Security as `FormSection aside`s in a rail; and the three views on
  Packages — the installed and updates tables and the software search, under one underlined strip
  (`tabClasses`) rather than a filled tab list, beneath the host's identity line — each a toolbar
  and a hairline over a framed table or, for the search, rows on the page's own edge, with what
  needs acting on (security updates waiting, a reboot owed, a stale index) said as a `Notice` that
  carries its own button rather than as a framed block with a header and nothing in it, and the Git page's repository list under its four readings (its workspace is one framed
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
| `Pane` | A sized region of a workspace that owns its own scrolling — session rail, file tree, log console. `flush` drops its frame for a pane that is one column of a workbench sharing a single frame, with a hairline between columns (the terminal page, the logs page's source rail beside its log workspace, and the files page's sidebar, listing and inspector under the strip that holds the page's commands) | Not a block in a page's flow |
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

**A page that is a form puts its section heads in a rail.** `FormSection aside`, stacked in
`FormSections`, sets the title (and the section's current state, as data — the address it answers
at, the certificate on disk) in a 15rem column beside the fields from `lg`, with a hairline between
sections. It exists for `/dashboard/configuration`, whose five sections with their heads over their
fields read as one column in which a head was as far from the fields above it as from its own; a
dialog's form keeps the stacked head. `railFrom="xl"` moves that width up for a form inside a
shell that already spends a column of its own — a project's settings, beside the project's
navigation, where at 1024 a 15rem rail left a row of three fields about 130px each. It is said
once, on `FormSections`, and inherited, so the heads of one page cannot leave the rail at two
widths.

**The deployment settings are that shape, and they were the last pages in the product that were
all containers.** Each of the nine was a stack of framed cards — a title strip, the form, a footer
holding Save — on the argument that things filled in one at a time read best as bordered boxes,
which is the wrong shape for a page that *is* a form. `settings/setting-card.tsx` draws them now.
`SettingsPage` reads the configuration, then draws what is saved but not live yet (a strip with no
button: the project context row already carries the one "Deploy changes", and a second brand face a
hundred and fifty pixels under it was two commands on one surface), the page's readings, and its
forms in one run of rail sections. `SettingForm` is one `<form>` and one save, and may span several
sections, because what one PUT writes is what one Save means — Runtime is five. `SettingSection`
is a rail head carrying what the section currently is as data (the host and branch it builds from,
the port it answers on) and at most one `settingStatus`: *Not saved*, *Unsaved changes* or *Saved ·
not live yet*. `SettingFoot` ends the form with when its change applies, Discard and Save. Save is
the outline face while the form is clean and the brand face once it holds an edit — the command
face as a function of state, so the one blue on a page of five forms is the form with something to
save — and on the nine settings pages it is never disabled, because saving an untouched Source
checks it again, which is how an operator finds out a credential stopped working (the game server's
settings, which declare a range for each value, hold it only while a value is outside that range). While the form is dirty the foot follows the
reader down it, sticky, opaque (§16 has no glass) and hairlined, its row held to the fields column;
at rest it is the form's last line rather than a strip of chrome. Each form keeps its draft keyed on
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

A menu reads the same way at a smaller size: a `DropdownMenuLabel` is an eyebrow over the group of
verbs it names, and every item, like every `Select` option, is `text-body`, the size of the rows
the menu was opened from. A `SelectLabel` naming a group of options is the same eyebrow. An option
can carry a reading about itself in `SelectItem`'s `hint` — the host a credential signs in to and
its kind — drawn at the option's far end and outside the item's text, which is the part Radix copies
into the closed field and names the option by: the list says "github.com · SSH key" beside each
name, and the chosen field says the name alone. `CredentialSelect` (`deploy/credentials-page.tsx`),
the one picker the new-project steps and a project's Source settings share, draws each option this
way, on its host's glyph.

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
- **A scale fitted a little above a limit splits into unround steps** (143 / 286 / 429 MB), so
  `yTicks` on `MetricChart` and `ChartPanel` names the ticks. A container's memory chart is scaled to
  its limit and ticks at the limit's quarters, so the top tick names the limit — on Docker's
  container page and on a project's Runtime alike, since both draw `ContainerUsage`.
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
never reach its final frame still ends up there.

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
  the fleet's live and build-slot figures, the Credentials and Notifications readings, the
  Overview's requests, the delivery insights, the run page's traffic after activation, Automation's
  revisions awaiting review and alerts firing, the live usage tiles, and the readings on
  Packages, System users and the audit log. A figure that follows a
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
  backup, a game command waiting on its reply; a webhook whose delivery's run is building, a
  schedule firing, a preview environment building; a variable being rotated, a linked database
  being tested, an engine being started from quick setup; and, on `/deploy/new`, a repository or
  an image being inspected. The shimmer lights the stage a release is at and the present
  participles that stand for a wait — *Sending test…*, *Testing…*, *Pausing…*, *Candidate for
  release #14*, *deploying #14*, *running now*;
- *once* — `Confetti` fires only when a release goes live in front of the reader — on the run page,
  and on the Overview when a run watched there from start to finish ends in success — never on
  arrival.

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
round faces in white rings with a filled "+N" circle — §4's pill three times over — where this
product's faces are squares. `pulsating-button` glows a command at rest, and §3 never lets a
command be a state. `dock` magnifies on hover and blurs behind itself; `orbiting-circles` and
`ripple` are perpetual decoration, beside wires that draw the real mechanism and a `StatusDot` that
already breathes. `shine-border` and `magic-card` are `BorderBeam` and `SpotlightBorder` already,
and `animated-shiny-text` is `TextShimmer`. The marquee, the globe and the dotted map (which would
need a GeoIP database and would fabricate the rest), the device mocks, the lens, the highlighter,
the glyph matrix, the flickering grid, particles, patterns and the aurora, sparkle, hyper and
morphing texts each sell a gradient, a glow or a movement this system does not have. What the pass
took instead was more of what was already here, on more of the section: the ticker, the border
beam, the fade, and wire marks that draw the products they connect.

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
containers, images, volumes, networks and stacks lists are cards at every width — the argument
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
trail stays a table.

**The deployment section's rows took the same rule, and it moved the breakpoint twice more.** A run
(`deploy/run-row.tsx`), a runtime service and a channel set their readings beside the name in fixed
measures where there is room and under it where there is not, chosen once with a media query — and
the width they need is measured inside the project's shell, not the window: at 1280 the content
column beside the project's navigation is about 968px, which left a service's name 150px beside
five readings, so the wide shape of a service and of the Overview's runs-beside-previews starts at
`2xl`, as does the build console's rail of stages beside the transcript. Inside a settings page the
column is narrower still, because from `xl` the rail takes its own 15rem, so there the window is
the wrong thing to ask at all: `settings/use-column-width.ts` measures the column a list is drawn
in, before its first paint, and the variables (from 600px), mounts and linked databases (from 480px)
choose beside-or-under from that — still once, still drawn once. A row that only reflows rather
than rearranging, a domain or a mount editor, uses a container query (`@container`, `@min-[40rem]`
for a domain and `@min-[36rem]` for a mount) and draws nothing twice by construction.

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
Deployments row's menu and by the run page's identity line, which is what keeps a finished run from
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

**A product is not a kind, and it is drawn as itself.** The template catalogue is sixty-two products
the reader already knows by their marks — n8n, Grafana, Redis — and set as sixty-two names in one grey
face it was a wall of words, with the language marks on the Git tab the only colour anywhere in the
flow. `components/product-logo.tsx` draws each product's *own* logo, in its own colours, on the recessed
tile `ProjectMark` uses for a deployment's favicon, so a template and the project it becomes are drawn
the same way: every template card, the five database engines, the images on the Images tab (by the
last segment of the reference, Docker's whale for the rest), and the settings panel's header. The
colour lives in the artwork rather than in a token, which is the same argument as the language marks
— the hue is not this product's to choose — taken one step further: the files are bundled in
`public/logos/` (the page's `img-src` is its own origin, and §8's locked-down networks cannot reach a
CDN), picked in the variant drawn for a dark ground, with their licences in `public/logos/NOTICE`. The
one file whose own colour failed that ground, MySQL's navy dolphin, was lifted to the L 0.72 rung the
`--language-*` tokens sit on. The tile is not an icon plate — it carries no tint of this product's and
sits beside a card's words rather than in front of a header's title — and the source strip still
stays muted, because five *kinds* are not five products. A repository row on the Git tab carries its
owner's picture on the same reasoning: the face is the account, which a glyph could only guess at.

The same marks carry into Docker and Databases, because they are the same products. A container,
an image and a stack's service are drawn as the product their image is (`imageProduct` reads the
last segment of the reference; anything it cannot name is Docker's whale), a stack as its services'
products overlapping (`ProductLogos`, the way a group of avatars overlaps; Compose's own mark when
none has a logo), a volume as the product of the container that keeps its data there, and a
database connection as its engine — in the workbench switcher and in every engine picker,
which are one `EngineCard` (`choice-card.tsx`) rather than three shapes that had already drifted.
Networks have no product and keep a glyph on the same tile, so their titles line up with the rest.

**What a backup covers is a product, and so is what a terminal runs.** A coverage row is drawn as
the thing it protects — a saved database as its engine, the proxy's configuration as nginx or Caddy,
a repository as git, a volume as the product of the container that keeps its data there (joined
through the container list, since the report names containers rather than images; a database
container run from a bare image id is its connection's engine), a stack as its services overlapping,
the dashboard as its own mark — and a job as the products of what it covers, with Backblaze drawn as
itself where it writes (S3 is a protocol a dozen providers speak, and keeps a glyph). The terminal
draws the program in each window's foreground the same way. A thing none of these can name keeps its
kind's glyph on the same tile.

**The host is a product too.** The Overview, Metrics and the logs rail draw the machine as what it
reports itself to be — its distribution (`platformProduct`, from `/etc/os-release`'s id), its processor
(`cpuProduct`, from the model string: AMD, Intel, Arm), its hypervisor (`virtualizationProduct`: QEMU for
a KVM guest) — and a running process as the product it is (`processProduct`: `postgres` is Postgres,
`dockerd` is Docker, `node` is Node.js) in the Metrics page's top processes and in every row of the
live process table. The other three Processes pages read their rows the same way: a systemd unit as
the product it runs (`unitProduct` — `postgresql@16-main.service` is Postgres, `pm2-deploy.service`
PM2, `certbot.timer` Let's Encrypt's renewal, by the unit's name with its suffix and instance
dropped, then by its first word), a PM2 application as its interpreter (`pm2Product`: Node unless
the ecosystem file says Bun or Python, a glyph for a binary), and a cron line as the program its
command starts (`programProduct`, the terminal's reader: `docker system prune` is Docker's, a
script of the operator's own keeps the clock). Each returns nothing for a name it does not know,
and the tile keeps a glyph: a Tux on an unrecognised distribution, or a guessed logo on `bash` or
`apt-daily.timer`, would be the drawing lying about the row. A reading that counts products carries
them after its words (`ProductGlyphs`): the Overview's Docker tile draws the images its running
containers are, Databases the engines its connections speak, the live table's Processes tile what
the machine is running, and the Services page's Active and Failed tiles what is up and what is not.
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
describes a thing the same way; a project's header and a deployment's run page are the fifth and
sixth. A game server's three pages add one line under that header (`GameIdentity`) with what only
the game can say — the address a player types, the edition, how full it is — and draw neither the
game nor its name again.

**The Security section draws what it watches, not what it is.** Its pages are about things with
few marks of their own — a firewall backend, sshd, a jail — so the marks it draws are the things
those watch and hold (`components/security/marks.tsx`). The section opens on how the panel is
reached as its identity line (`ExposureIdentity`): Tailscale's mark on a tailnet-only panel, a
glyph for the place otherwise, the grade as the title in its verdict's colour, the allowed ranges
and interfaces as its facts, the address this browser arrived from drawn as the network it is on,
and the posture's verdict at the right end, so the two answers the page is opened for share one
line. Firewall, SSH and Intrusion open on the same line for the thing each is about: the backend by
its own name with the enable switch beside its state, sshd with its port and what holds the
listener with its verdict beside the recent jobs, and fail2ban as its own mark (the project's, from
homarr) with whether it is running. A jail is a card you open — its sheet of held addresses is the
destination — drawn as the service it watches (`jailProduct`: nginx's mark for `nginx-http-auth`,
a glyph for `sshd`, which has none) with its counts in fixed measures. An address anywhere in the
section is drawn as the network it is on (`Address`: Tailscale's mark for the tailnet, a glyph for
the rest), a peer's processes as the products they are (`ProcessList`, through `processProduct`),
a network device as what made it (`interfaceProduct`: Tailscale's tunnel, Docker's bridges and
veths, a Kubernetes CNI; a physical port keeps a glyph for its kind), an account holding a key as
its initials in the users list's hue, and an attacker's attempts against the most persistent
address's as a meter. Connections, logins, devices and routes are readings with verbs, so their
rows stay rows.

**The proxy section draws the engine and the authority.** Its things have two products between
them — the engine serving a site and the authority that signed its certificate — and the pages draw
those where they are true (`components/proxy/marks.ts`). The overview opens on the engine as its
identity line (`EngineIdentity`): nginx's or Caddy's mark on the tile, the version beside the name
the way the Overview sets the kernel beside the distribution, the unit's state, the directory it
reads and the ingress container as facts, certbot drawn as Let's Encrypt — the mark says what it
issues, not who wrote it — and the service verbs at the right end. A site is a card drawn as the
engine serving it (`siteProduct`), with TLS said by whose certificate: Let's Encrypt's glyph where
the site points at certbot's live directory (`certPathProduct`), a shield where the file is
somebody else's. A certificate is drawn as who signed it (`certificateProduct`, through
`issuerProduct` and certbot's directory), and an imported one from a company CA keeps a glyph rather
than a guess. A stream is drawn as the service its port is (`portProduct`: the databases and control
planes the attention list already names by number, and the two Minecraft editions) and a port
nothing names keeps a bare connection. A listening socket's process is its product's glyph, read
from the process name first and the port second, so `postgres` on an odd port is still Postgres and
`python` on 5432 is not. Sites and streams are cards you open and take the edge; certificates,
watched domains and sockets are readings with verbs and stay rows, with how much of its term a
certificate has left drawn as a meter under its verdict (`CertLife`).

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
identity line, and as the field is typed on `/deploy/new`'s Clone URL, Compose Git URL and Image
reference and the credential sheet's Host. A saved credential is the host it signs in to, an SSH key
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

**The same argument buys the git surface its own glyph set.** Heroicons draws no branch, no commit
and no pull request, so `icons.tsx` maps those words onto the share, hash and chat-bubble marks —
near enough on any other page, and wrong on the one screen where the reader identifies the thing *by*
the drawing. `components/git/glyphs.tsx` takes six from Material Design Icons, which is already a
dependency for the language marks (`language-icon.tsx`). Nothing else is imported from MDI there: a glyph that exists in both sets stays Heroicons, or the git pages grow a
second icon weight. `AuthorMark` in `git/marks.tsx` is the coloured-wayfinding rule again — a column
of commits where mine and the bot's are two hues is scanned, one where they are the same grey is read
— drawn as the account face without a picture (`InitialsMark`): the initial on a wash of the `LANES`
hue the name is given in the rail and as a run's actor, hashed without its case, so one person is one
colour in a commit line, a run row and a forge's face alike. A square rather than a circle, because a
filled 16px round mark with a character in it is the pill §4 deleted.

**A log line is read by its shapes, and coloured by the same rules as the rest of the product.**
The logs console used to draw every line as one grey-white string with a 10px level tag in front of it,
so a failed password, the address it came from and a 502 were found only by reading every line.
`lib/log-tokens.ts` cuts a line into its shapes — syslog's `time host proc[pid]:`, the common log
format, logfmt pairs, a JSON object, bracketed and shouted levels, addresses, request lines, statuses,
paths, ids — as spans over the original text, so the search's match ranges still land; and
`components/logs/log-text.tsx` colours them from one map. The **status hues** go only to what is a
reading of state: a level, an HTTP status by its class (2xx success, 4xx warning, 5xx destructive), a
word that says something failed or succeeded. Every other kind takes a `--tag-*` hue, which sit at one
lightness so no kind outshouts another, and what the eye should skip goes muted — the line's own
timestamp (not drawn at all while the time column shows it), this host's name on a syslog line (not
drawn either), the pid, the punctuation, the keys. The message stays in the foreground. A program's
name takes a hue by name from `LANES` (`lib/hue.ts`: the tag hues without red and amber, which on this
console would read as a process that failed), so one process can be followed down a busy page — the
same argument as `AuthorMark`, whose hash now lives there too. An error or a critical row is washed the
way the build console washes a failing step, a warning row in amber; the level column is the level's
word at the line's size. A structured line is drawn as its message and fields in the logfmt shape the
tokenizer reads, most telling field first. The "Colour" toggle beside Wrap and Time turns all of it
off and shows each line exactly as it was written.

A deployment's request log is a log, and is drawn by the same rules: the request console, a request
opened in place, the Insights lists and the scanners notice all take their parts from
`deploy/request-marks.tsx`, which is `log-text.tsx`'s token classes over `lib/requests.ts`'s one
status map. A status is coloured by its family — 2xx success, 3xx the path hue, 4xx amber, 5xx red —
a write takes the method hue while a read stays muted (a `DELETE` is a change, not a danger), the
path and query are tokens, and an address takes the address hue beside the client drawn as itself.
Its Colour switch is the console's own; turned off it keeps a failure, a refusal and an answer
slower than a second, because those are readings of state (§3) rather than decoration.

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
folder is — the listing, the tiles, the sidebar, the inspector, the strip, the finder, the terminal's tree.
The strip's compact folder button sets one colour for every folder, stores it as `files.defaultColour`,
and clears old individual labels. A folder can then be labelled on its own in its inspector or menu;
that label takes precedence until another global choice. Nine names (`--folder-*`, one value each;
the tab and the pressed mark are mixed from the face in `[data-folder]`), blue until somebody says
otherwise, graphite for build output and installed dependencies until a global colour is picked
because nothing in them is yours to edit. The picker is the folder drawn in each colour,
the chosen one `bg-accent` like every selection. The Files page is a workbench and, like the terminal
and a Git working copy, has no page header: its commands sit in the strip across the workbench, beside
the folder they act on.

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
   a fold. Two of them now read the running container as well as the form: the memory limit is drawn
   against what the live release peaked at in the last hour, and the release strategy is checked
   against the rule the executor applies at the next deployment — a writable mount, a fixed host
   port or the host network cannot run two releases side by side, and blue/green on such a plan was
   offered there and then refused at start. Build took the pass it had skipped (what it builds with,
   the last build read from the live release's own build step, the release tasks, the build
   variables with a warning where a secret would be compiled into browser code), and Domains,
   Storage, Databases and Automation open on four readings each. Those four pages draw theirs from
   the saved configuration and what the server observed rather than from a draft, because a page's
   readings stand above its forms, out of the drafts' reach; what is saved and not live yet is said
   by the pending strip and by the rows themselves.

   The rest of the deployment section took the pass as a question of *which* figures. The fleet
   opens on four the chips beneath it cannot say — how many projects are live, requests a minute
   across the fleet, the share of them failing, the build slots in use — and leaves the per-state
   counts to the chips, with the cards ordered worst first. Credentials opens on how many are held
   and across which hosts, how many a source reads through, how many were never used and when one
   last was; the GitHub App's state moved out of the tiles into its own section, where its header
   and its setup path already said it. Notifications opens on whether messages are arriving — a
   message retried until it went out counts once, as delivered. The Logs page's readings each carry
   their last hour, as the host Overview's do.

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

   The dashboard's own two pages took the same exit in 0.7.0, and the reason generalises: a figure
   on a page you configure is best drawn beside the control that sets it. Version's Installed,
   Latest and Checked became one identity line and the timeline's marks; Configuration's Answers
   at, Certificate, Port and Two-factor went to the rail heads of the sections that set them and to
   the proxy's row in the stack. Both pages' doc comments name where each went.
   The account's Security page took it for the same reason — the second factor's state and how many
   sessions are signed in are the rail heads of the sections that change them — and Sessions opens on
   the session it is read through, with the count of the rest on their header.

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
   to what it counts, and the products the environment talks to sit under the rail head. And a
   project's Runtime page carries each count in the header of the block it counts, with the four
   moving readings — processor, memory, processes, network — as the live usage tiles.
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
9. **Alignment.** Tiles top-align so a row of names is a row; hints truncate rather than wrap; the
   first column starts at the page gutter.
10. **Verify.** `scripts/test-changed.sh` (it runs `tests/browser/design-system.spec.ts` for any
    UI change), then a screenshot at 1280 and 1720 wide against a mocked
    API in the pattern `mockShell` uses, and look at it: a scrollbar where none belongs, a label a
    line lower than its neighbours, a figure at the wrong size, are things the checks do not catch.

**What the Overview looks like after these passes**, as a checklist for the page you are on: the
machine's identity line first (`HostIdentity` — its distribution drawn as itself, the processor and
hypervisor as bare marks among its facts, the verdict and Metrics link at the right end); a five-tile
`StatGrid` of readings, the four that move carrying their last hour in
the tile's `trend` slot where a meter would be and the one that fills keeping its meter; a plain
`Health` list beside a plain activity list; and a `Section` holding a `StatGrid` of eight `StatLink`
tiles, one per module, each naming what it counts with the products themselves. No frame anywhere on
the page. Everything that arrived, rose.

Reading pages now keep their page name in a screen-reader-only `h1` through `PageContext`. The rail
provides the visible location. A linked parent remains as a compact way back, while pages without
one begin directly with their content. Put a control beside the data it changes: list commands in
their section header or filter bar, workbench commands in the workbench strip, and empty-list commands
in the empty state. The Metrics range, pause and export controls sit in the machine identity line;
the proxy service verbs sit with the engine facts. Detail pages keep their verbs beside the way back
and their resource name in the first facts or identity row. The deployment fleet puts its related
pages and create command with the list filters; an empty fleet has its create command in the empty
state.
The Databases section opens on a control center with no tiles — the databases as lit cards drawn as
their engines, each carrying its own three figures, an attention list under them, the servers found
here and not yet connected, and the map of what they feed — and a database opens on its own overview:
the connection string, its facts as one list, its largest tables as bars and what reads it. The
section took pass 2's `/git` exit on every page (the control center, the topology, a database's
connection, backups and advisor): each figure went to the card, the header or the lane that counts
the thing it was about, and every page's doc comment names where. Its per-connection pages keep the
connection switcher, facts, status and New command in one compact strip. Flow pages keep their visible question as the `h1`, since the question is the work on that
screen (§16).

The 0.7.0 pass took two things off it that had been saying the same figure twice: a `MetricStrip`
of uptime, processes and cores in the header's corner (facts about the machine, now in its identity
line; the cores were on the CPU tile as well) and a "Last hour" panel of four sparklines whose every
value repeated the tile above it (the sparklines are in the tiles now). The Metrics page opens on the
same identity line with the processor as its mark.

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
| Host Overview, metrics, Docker, Security, proxy, Processes, System, Backups, Packages, audit, Git, files, terminal | Reading | The reader arrives to find out what is true. |
| Deployments list, a project's overview, runtime, logs, deployments, requests | Reading | A project that exists is a thing you read. |
| `/deploy/new` — the source chooser | **Flow** | Step one of three, and the screen is asking a question. |
| Any page with a run of *choices* on it | either | The register is about the page; the lit choice is about the thing. A reading page with an engine picker in a dialog gets the edge on that picker and changes in no other way. |
| `/deploy/new` — Configure | **Flow** | Step two of three, ending in the one command that creates the project. |
| A run in progress (`/deploy/[id]/runs/[run]`) | Reading | You are *watching*, not deciding. The page opens on the run's identity line with its verbs — Cancel, Retry, Redeploy, Visit, the release's menu — at the line's end beside its state, and nothing above it: the sequence on `/deploy/new` ended when the project was created, so a first run is read the same way as the fortieth, with no spine claiming the screens before it. |
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
   pressed buttons, never a tablist — a tablist must own tabs, and these are five toggles for one
   answer — and on a phone it runs to the screen's edge, where the source cut off is the cue that
   there are more, as a `ChipStrip` does; the scroll shade is drawn in the page's own ground and
   cannot show on it.
7. **Motion on the state change, not only on arrival.** §11's four still apply and no fifth is added:
   the spine's current segment, a `BorderBeam` on a card while its work is in flight, a
   `NumberTicker` on a figure that settled, `Confetti` once when the outcome lands in front of the
   reader. On `/deploy/new` the beam is the repository or image row being inspected
   (`ChoiceRow busy`), beside its *Importing…*, so the row that was pressed is the row that answers.
8. **Hold it to the window.** A flow screen is decided in one view: `<Page fill="xl">` holds it to
   the window at `xl`, the question, the spine and any strip stay put, and what scrolls is the one
   list or form longer than the space left — inside its own surface, under its own toolbar and above
   its own command. A surface with no inner scroll is a surface the page now clips, so each one is
   capped (`max-h-full`) and scrolls itself. `/deploy/new` is the reference: every source is the same two columns (the
   focused surface, capped at the window's height with `max-h-full self-start`, and a 22rem column
   beside it), unfinished setups moved from a block above the strip into a counted button beside the
   question, and a Configure step with more settings than fit scrolls its fields between the heading
   and Continue; the Database tab, five engines and two fields, is the one source without a second
   column. Below `xl` the columns stack and the page scrolls as every other page does — a phone is not
   a window to hold. `deploy-new.spec.ts` asserts the shell does not scroll at 1280×800 on every source
   and every Configure step.
9. **Verify.** `scripts/test-changed.sh`, then screenshots at 1280 and
   1720 — and look at them. The failure this register exists to catch is one no assertion sees.
