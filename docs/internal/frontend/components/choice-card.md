# Choice card

One thing you pick out of several kinds, by its mark.

**Members:** `ChoiceGrid`, `ChoiceCard`, `ChoiceCardTitle`, `ChoiceCardHint`, `ProductCard`
(`components/choice-card.tsx`); `PurposeCard`, `ExperimentalMark`
(`components/onboarding/purpose-card.tsx`); `EngineCard` lives with the databases section, in
`components/database/engine-card.tsx`.

## Purpose

A block you **pick**: a deployment source, a template, a database engine, an alert's kind, what the
server is for. It is not a `Panel` (content that happens to be framed) and not a `ChoiceRow` (a thing
you open): it is a button that happens to be large, and its selection is its edge.

## Anatomy and shapes

- **Compact** (no `verb`): the card is the `<button>` and its contents are its name — right when they
  are two short lines. `ProductCard` is this shape with a logo in front.
- **With a sentence** (`verb`): the control moves onto the title, named by `verb`, and the arrow is
  drawn; the press on the card around it is the pointer's convenience (`useChoicePress`, the rule
  [`card-row.md`](card-row.md#the-press) describes). `mark` is a kind's glyph, `logo` the product
  itself beside the words, `description` one or two lines, `trailing` a reading.
- Selection is `border-brand` over `bg-accent` with a `Check`; the lit edge is the hover (§3, §6).

## ChoiceGrid

| Prop | Does |
| --- | --- |
| `columns` | `2`, `3`, `fill` (as many 16rem columns as the grid holds, equal rows) or `compact` (a run of `ProductCard`s) |
| `mode="single"` | One choice out of the set: a `radiogroup` named by `label`, each card's control a `radio` with `aria-checked`, one tab stop (the picked card, or the first), and the arrow keys, Home and End move the pick as a native radio does. Only a bare key does (`choiceStep`): Alt+←, Ctrl/⌘ and Shift with a key stay the browser's and the page's |
| `mode="multi"` (default) | Several may be taken: each card is a toggle with `aria-pressed` |
| `label` | The set's name, which a single-choice grid needs ("Alert kind") |

A grid of single-choice cards without `mode="single"` is a row of toggle buttons with no name (PRM-10).

## States

- **Selected**: brand edge, accent ground, the tick. A chosen card is never faded, disabled or not.
- **Disabled**: set in the faint ink, never an opacity; a sentence-shaped card loses its control
  entirely, because a disabled button still announces itself as the way to use the thing.
- **Working** (`ProductCard working`): a light round the edge while the thing picked is made.

## PurposeCard

`PurposeCard({ purpose, selected, onSelect, size: "hero" | "row", reading?, detail? })` is one
purpose of the first-run question, at two sizes of one anatomy: products as themselves, the name,
then at most the `reading` ("1 project here"), the full `ExperimentalMark` on a line of its own, and
the `detail` (the sections it means), with one tick in a column of its own so nothing runs under it.

- `hero`: Welcome. Logos over the name, the tick in the corner, 176px tall; a row on a phone.
- `row`: Account → Workspace. Logos beside the words, the tick at the right, one height for the
  grid (three lines: the experimental card gives its second section line to the mark).

The control is the card, named by the purpose's title and described by the rest; several purposes
are taken at once, so it is a toggle.

## The phone

A grid steps down to two or one column; `compact` keeps two. Hero cards become rows.

## Accessibility

The compact card's whole content is its name, so keep it to two short lines; past that, use `verb`.
A single-choice grid is a named radiogroup; a multi-choice set sits in a `role="group"` with a name.

## Example

```tsx
<ChoiceGrid mode="single" label="Alert kind" columns={3}>
  {kinds.map((option) => (
    <ChoiceCard key={option} selected={kind === option} onClick={() => setKind(option)}>
      <ChoiceCardTitle>{KIND[option].label}</ChoiceCardTitle>
      <ChoiceCardHint>{KIND[option].hint}</ChoiceCardHint>
    </ChoiceCard>
  ))}
</ChoiceGrid>
```

## Related

[`card-row.md`](card-row.md) for a thing you open, [`forms.md`](forms.md) for a choice inside a
form, [`surfaces.md`](surfaces.md#7-which-surface), [`../recipes/flow-page.md`](../recipes/flow-page.md).
