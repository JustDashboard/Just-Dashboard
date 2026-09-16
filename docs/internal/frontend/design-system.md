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
`--card` 0.128) so the one step still reads, and three things stopped taking a frame:

- a run of figures — `StatGrid` draws hairlines *between* tiles and nothing around them, and the
  first column starts on the page's own edge, in line with the title (`framed` restores the box for
  the one case a run sits inside another surface);
- a list that is the whole of a section — `Panel plain` keeps the panel's anatomy (header, toolbar,
  body, footer) and drops the border and ground, so a title and a hairline mark the block. Recent
  activity on the Overview, the idle containers and compose projects on the Docker overview, the
  areas list on the Security overview, "Needs attention" on the proxy overview and on every security
  area page, health findings, the runtime-health bar, and the deployment pages' lists, overview
  facts, run summary and create flow are plain;
- a panel's header — it is no longer a tinted strip. The title sits on the panel's own ground with a
  hairline under it. `--surface-header` survives at a fainter mix for the two places a strip is still
  chrome: a `Pane`'s header and footer.

A panel that is also a destination takes `interactive`: its border steps up to `--border-strong`
under the pointer, and nothing else moves.

Press feedback is colour. A control with a face takes its own `active:` step — `active:bg-control-active`
for the neutral faces, `active:bg-brand-active` for the orange command; a ghost or link button has no
face to move and borrows the accent wash. Nothing translates, and nothing casts a shadow to say it was
pressed.

## 3. One orange, three jobs

The product has one colour, and it is asked to do three things. The brand orange is the face of a
command *and* the mark of where you are; the lit step is attention. What keeps the first two from
reading as each other is form rather than hue — a command is a filled face you press, a location is a
tint, a glyph or a fill behind a word — and what keeps all three apart is that only the lit step is
never at rest.

| Role | Token | Spent on |
| --- | --- | --- |
| Command | `--brand` (`#E05623`) | The face of the one action on a surface: `bg-brand` at rest, `bg-brand-hover` under the pointer, `bg-brand-active` on press. Never a state, never a selection. |
| Location | `--brand` (`#E05623`, oklch 0.629/0.183/39°) | The mark, the current nav entry, a module tile's mark on the overview, the active section tab, `--chart-1`. |
| Attention | `--signal` (the same orange lit, L 0.72) | The focus ring, a search hit in the log console, the terminal bell. |

White is still a fill, but it is no longer the command face. `--primary` is the neutral ink a
*reading* draws as a solid mark — the checked state of a checkbox or a switch, a meter's bar — so a
filled state can never be mistaken for the one thing you press.

The two orange tokens are one hue, because the product has a mark and the mark is one colour:
`#E05623`, the orange the J in `components/logo.tsx` is drawn in. The palette carried a blue in the
attention role until 0.6.7 — a second identity nobody chose, reading as chrome borrowed from
elsewhere next to a logo that is emphatically not blue. **What separates the two is lightness, not
hue**: `--brand` is the logo's own value, the colour at rest; `--signal` is that orange a step
brighter, and the brightest thing on this ground, so what is happening *now* is found before what is
always there.

Orange sits at 39°, not at the 78° the amber status hue occupies, so the identity hue cannot be read
as a warning and `--warning` stays where every operator already expects amber. The cost of the move
is at the other end: 39° is close enough to `--destructive` (25°) that the two are no longer the
arm's-length pair they were, which is why the brand hue is never spent on a *state* — a red that
means failure always arrives attached to a status word, a dot or a toast, and orange never does.

**Nothing reads a hue by its old name.** The terminal's ANSI blue is `--chart-2`, which is a literal
blue rather than a reference to any role token, or `ls` paints directories orange. `--chart-3` is a
magenta, not the red it was: with `--chart-1` now at the warm-red end of the ramp, a red slot three
put two indistinguishable lines on the same load chart.

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
| `Pane` | A sized region of a workspace that owns its own scrolling — session rail, file tree, log console | Not a block in a page's flow |
| `Well` | Output you read: command output, a log tail, a diff, a stored secret | Not a fence around controls |
| `Group` | A fence around part of a body: a set of ports, one release task, a repeated form row | Not a `Panel` — no header, no lift |
| `StatTile` | One headline figure, in a `StatGrid` | Not free-form — a row of them is read as a table |

**Reach for `Panel`/`Pane`/`Page`, and add a variant there rather than a one-off in a feature page.**
Before these existed, fourteen pages read as fourteen products.

`Modal`, `PaletteModal` and `SidePanel` are the only assemblers of raw `Dialog` and `Sheet`, and
`no-restricted-imports` enforces it. A page never opens one itself.

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
fill and brand-orange icon.

A **section tab strip** is `text-body`. It was `text-xs` while the page title was 20px and the strip
sat within two pixels of both the title and the panel titles; with the title at 24 the strip has a
rank of its own again, and 12px chrome under a 24px title read as an afterthought.

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

## 11. Motion says one of three things

Before 0.6.7 the only shared motion in the product was whatever Radix and `tw-animate-css` happened
to ship: a `transition-colors` here, an `animate-pulse` there, and no answer to "what should this
look like while it is happening". Three names in `@theme`, because three is what the interface
actually has to say:

| Token | Says | Spent on |
| --- | --- | --- |
| `animate-breathe` | this reading is live | A halo breathing out of a `StatusDot`, at an amplitude low enough to read as "still arriving" and never as an alarm. |
| `animate-rise` | this was not here a moment ago | Opacity and four pixels, once, on arrival: a panel that appears, a disclosure that opens, a sparkline whose data landed. |
| `animate-sweep` | this is working, and cannot say how far along | An indeterminate bar for a pull or a prune, where a spinner in the corner of a wide panel is too small to be the answer. |

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
  capability each needed.

A control that changes state should also **say that it is changing**. A Docker stop takes ten seconds
to honour while the socket keeps reporting the old state, so the row answers the press by sitting
still and then jumping — indistinguishable from a button that did not work, and the reason anybody
presses restart twice. The row carries the present participle (`Stopping…`) until the change lands.

## 14. A header is its title

**A surface's header carries its name and its actions. It does not carry a picture of itself.**

Every `PanelHeader`, `Modal`, `SidePanel` and `Section` used to open with a 28px brand-tinted plot
and a glyph inside it. On a page of six panels that is six orange marks down the left edge, each one
the same weight as the one control the reader is actually meant to press — and none of them said
anything the word beside them did not. `Servers` in front of "Filesystems", `Cpu` in front of
"Processor", `ShieldOff` in front of "Health": a glyph is a guess at a word the header has already
spelled out. Where the picture and the title disagreed, the title was the one that was right.

The prop is gone from all four components, so this one is enforced by the type checker rather than by
judgement: there is no `icon` to pass. `ChartPanel` forwarded one too, and does not any more.

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
