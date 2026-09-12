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

Press feedback is colour. A control with a face takes `active:bg-control-active`; a ghost or link
button has no face to move and borrows the accent wash. Nothing translates, and nothing casts a
shadow to say it was pressed.

## 3. Ink commands, blue locates, amber interrupts

Three roles, and a colour belongs to exactly one of them.

| Role | Token | Spent on |
| --- | --- | --- |
| Command | `--primary` (white) | The one action on a surface that you press. Never a state, never a selection. |
| Location | `--brand` (blue, 258°) | The wordmark, the current nav entry, a panel's icon plot, the active section tab, `--chart-1`. |
| Attention | `--signal` (amber, 73°) | The focus ring, a search hit in the log console, the terminal bell. |

Status keeps its own three hues (`--success`, `--warning`, `--destructive`) and they are never
borrowed for anything that is not a reading of state.

**Selection is `bg-accent`, everywhere** — a neutral fill, deliberately not a hue. The active session
in the terminal rail, a pressed `ToggleGroupItem`, an applied `FilterChip`, a highlighted command row,
a selected table row and the current file in the tree all take it. A filter that borrowed the brand or
the primary tint would read as the page's main action.

Tinted surfaces come from the three tone tokens, never hand-mixed:

- `bg-plot-*` — the rounded square behind an icon in a header, a notice, a modal.
- `bg-wash-*` — the ground of a banner that is tinted rather than framed.
- `border-rule-*` — the border of that banner, and of a tinted tag.

Hover has exactly three answers: `bg-row-hover` for a table or list row, `bg-menu-hover` for a menu,
select option or command row, and `bg-accent`/`bg-control-hover` for a control with a face.

## 4. One vocabulary per idea

**There is no badge and no pill in this product.** `ui/badge.tsx` was deleted so the decision cannot
come back by accident, and a Playwright test asserts no filled, fully-rounded element with text
renders on any page.

- A **count** is `.numeric` text next to its label.
- A **state** is `Status` — a coloured dot (or an icon) and a word. No border, no fill.
- A fixed **property** of a row is `Tag` — a squared hairline chip, small caps, border and text only.
- Anything longer than three words is **prose**, and belongs in the row's secondary line or a `Notice`.

`components/tone.ts` exports the one severity union: `"default" | "success" | "warning" | "danger"`.
`Verdict` keeps `critical` because that is what the backend sends and `Button` keeps `destructive`
because that is what the control does — but anything choosing a *colour* chooses from the shared
union. A component that declares its own tone names is the drift this file exists to stop.

## 5. No descriptions under titles

**`PageHeader`, `Section`, `PanelHeader` and `ChartPanel` have no `description`.**

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

## 7. Which surface

| Surface | Is | Is not |
| --- | --- | --- |
| `Panel` | A block of content *on* the page: framed, tinted header strip, hairline, body | Not a working region |
| `Pane` | A sized region of a workspace that owns its own scrolling — session rail, file tree, log console | Not a block in a page's flow |
| `Well` | Output you read: command output, a log tail, a diff, a stored secret | Not a fence around controls |
| `Group` | A fence around part of a body: a set of ports, one release task, a repeated form row | Not a `Panel` — no header, no lift |
| `StatTile` | One headline figure, in a `StatGrid` | Not free-form — a row of them is read as a table |

**Reach for `Panel`/`Pane`/`Page`, and add a variant there rather than a one-off in a feature page.**
Before these existed, fourteen pages read as fourteen products.

`Modal`, `PaletteModal` and `SidePanel` are the only assemblers of raw `Dialog` and `Sheet`, and
`no-restricted-imports` enforces it. A page never opens one itself.

## 8. Type

The ladder is `text-micro` (10) → `text-hint` (11) → `text-xs` (12) → `text-body` (13) →
`text-sm` (14) → `text-title` (15). An arbitrary `text-[Npx]` is a departure from it, and the linter
says so.

Weight carries hierarchy where size cannot. The sidebar is the one surface dense enough to need
three: group labels in the eyebrow's small caps, resting entries `font-normal` so the column reads as
a list rather than as forty-nine headings, and the current entry `font-medium` alongside its accent
fill and brand-blue icon.

`.eyebrow` is the small-caps label that opens a section, a panel header or a stat tile. `.numeric` is
any figure meant to be compared with the one above it — tabular digits stop a polling table from
shimmering.

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
