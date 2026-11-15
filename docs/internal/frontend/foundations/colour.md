# Colour

## Reference

| Rule | Held by |
| --- | --- |
| Dark only. No light palette, no theme switch. | convention |
| Text has three inks: `text-foreground`, `text-muted-foreground`, `text-faint`. Faint keeps 4.5:1 on every ground text is set on, the selection ground included. | spec `design-system.spec.ts` (*faded secondary text keeps 4.5:1*, the selection ground included); unit `lib/utils.test.js` (every ink on every ground) |
| No `/NN` on a text ink. A fainter word is `text-faint`, never `text-muted-foreground/60`. | coming: lint `ink-opacity` (WP-39) |
| Every colour is a token in `globals.css`. No Tailwind palette, no hand-mixed tint, no `[var(--…)]` for a registered colour. | lint `hardcoded-palette`, `tone-tint`; coming: lint `colour-var` (WP-39) |
| The brand blue does three jobs: a command's face, where you are, a location mark. | convention |
| Status hues (`--success`, `--warning`, `--destructive`) arrive only attached to a reading: a figure's `tone`, a `Status` dot and word, a `Notice`. | convention |
| Selection is `bg-accent`, a neutral ground, never a hue. | spec `design-system.spec.ts` (*hover, selection and focus are three different marks*) |
| Tints come from `bg-plot-*`, `bg-wash-*` and `border-rule-*`. | lint `tone-tint` |
| A control (input, select, checkbox, switch) draws its edge in `--control-edge`, at 3:1 against the field and the page ground. Hairlines and card borders keep their tokens (T3). | unit `lib/utils.test.js` (3:1 on the page, a card, a popover and a control face); `border-control-edge` in `Input`, `Textarea`, `SelectTrigger`, `InputGroup`, `Checkbox` and the `Switch` track |
| `--chart-5`, the RX colour of every in/out pair, sits near hue 200, off the success hue (T9). | unit `lib/utils.test.js` (more than 25° from `--success`, 3:1 on the page and a card) |
| The danger button has three opaque steps: `--destructive-face` at rest, `--destructive` on hover, `--destructive-active` pressed, with `--destructive-foreground` on them. White on the bare token is 3.8:1, so the token is never the resting face. | unit `ui/button.test.js` (no face keeps an opacity step) |
| A kind is coloured from the categorical ramp (`--tag-*`); git and pull-request states alias onto it. | convention |

The token families are in [`tokens.md`](tokens.md).

## 1. One mode

**The product is dark. There is no light palette, and there is no theme switch.**

Shipping two grounds meant every tinted surface, status hue, chart colour and terminal ANSI slot had
to be chosen twice and verified twice, and the second set was seen by almost nobody. `lib/themes.ts`
is now one bootstrap script: it puts `.dark` on the root element before first paint so the generated
shadcn primitives' `dark:` variants resolve, sets `color-scheme`, and clears the stored preference
from anyone upgrading. The theme hook and `/appearance` are gone.

**Text has three inks, and the third has a floor.** `text-foreground`, `text-muted-foreground` for
secondary text (7.6:1 on the page), and `text-faint` (`--faint`) for what sits behind that: a count, a
timestamp, a line number, a unit. `--faint` is the dimmest lightness of the muted hue that keeps WCAG
AA's 4.5:1 on every ground text is set on: 5.3:1 on the page, 5.1 on a card, 4.9 on a popover, 4.8 on
`--muted`. On `--accent` (a chosen row) it is 4.7:1, and red 4.6:1: 0.9.0 moved the selection
ground from L 0.278 to 0.22, the current nav entry's step, because faint text measured 4.0:1 and
red 3.9:1 on the old one. A selected row is that much nearer the hover wash (1.08:1 against a
card), and still reads as chosen. **Do not fade
text with an opacity.** `text-muted-foreground/60` measured 3.4:1 and `/40` 2.1:1 at 10 to 12 px, on
about two hundred call sites. Those spellings (`/70` and fainter) now draw the faint ink through one
rule in `globals.css`, which leaves an `svg` its fade (a glyph needs 3:1 where it carries meaning) and
stays at one class's weight so a `hover:` colour still wins; new code writes `text-faint`, and the old
spellings are to be renamed, not copied. A count beside a tab's or a chip's label (`ChipCount`) is the
label's own ink at 80%, 5.2:1 unselected. `design-system.spec.ts` measures all of these in the browser,
painted onto a canvas and read back, against each ground.

Colours live on `:root` in `app/globals.css`. That is the only place a colour is named. One page
holds a copy: `/hub/enter` (`lib/hub-enter.ts`) is a document written by hand that loads no
stylesheet, because its first script must run before anything else the app would load, so it repeats
the few tokens it draws with (its `--muted` is `--muted-foreground`). `hub-enter.test.js` reads both
and fails when a value drifts.

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
`#CAE9FF`, the brand blue of the files in `public/brand-logo/` (inlined in `components/logo.tsx`).
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
