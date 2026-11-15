# List row

A row you read: a mark, a title, a second line and a trailing state, one height in every state.

**Members:** `RowList`, `Row`, `RowsSkeleton`, `ROW_BLEED`, `TABLE_BLEED` (`components/row-list.tsx`);
`RowLink` (`components/row-link.tsx`); `FindingList` (`components/finding-list.tsx`); `RowActions`,
`DimActions`, `IconAction`, `rowReveal` (`components/icon-action.tsx`).

## Purpose

`RowList` and `Row` are the list of *a few things, each with a name and a state* that a reading page
draws: the idle containers on Docker's overview, recent activity, a file's revisions, a check's
findings. One list, one row, one hover. Before it each page drew its own padding, its own hover and
its own idea of where the second line goes.

A row is **read**. A list of things you **open** is a `ChoiceList` of `ChoiceRow` cards
([`card-row.md`](card-row.md), D5). An `href` row is right only where the list is a reading first
and each line merely links to its detail, as an activity feed does.

## Anatomy

```
[leading]  Title ............................  [trailing]  [→ revealed]  [children]
           second line (hint, muted, truncated)
```

- `leading`: a mark, at the size the list uses throughout (a `StatusDot`, a 32px tile).
- `title`: one line, `text-body font-medium`, truncated.
- `subtitle`: one line, `text-hint` muted, truncated; `mono` for a literal from the host (a path, an
  image reference). Prose or a value, never a state.
- `trailing`: the state at the right edge (a `Status`, a `Tag`, a figure, a menu).
- `children`: anything else, drawn after the trailing slot inside the same face.
- The face is `px-5 py-3`: 44px with one line, 60px with a second line or a 32px mark and a second
  line. Inside a plain `Panel` it takes `ROW_BLEED`, so its wash reaches a step past the words.

## Props

| Prop | Does |
| --- | --- |
| `href` | The row is a `Link`, the whole row the target; the arrow appears through `rowReveal` (always on touch) |
| `onClick` | The row is a `<button>` spanning the row |
| neither | The row is inert and takes no hover |
| `current` | The one row open beside the list (a revision in the editor, the finding a link pointed at): `aria-current` (`page` on a link, `true` otherwise) and the selection ground `bg-accent` (§3). Never type `bg-accent` on a row by hand |
| `className` | Goes on the **face**, not the `<li>` |
| anything else | `id`, `data-*`, `aria-*`, `style` go on the row's `<li>`, so an anchor or a spec hook needs no wrapper |

`TABLE_BLEED` is the table's counterpart of `ROW_BLEED`: put it on a table's `containerClassName` or
a flush `PanelBody` inside a plain panel, so the first column starts on the panel title's edge. It
replaces the hand-typed `-mx-4`, `-mx-3` and `-mx-2`.

## States

- **Loading.** `RowsSkeleton({ rows, mark, subtitle })` draws the rows at a `Row`'s own pitch: the
  same face, a bone on each line (`Bone` keeps the line box its words will have), a 32px mark's
  place. The list is as tall loading as loaded, so nothing under it moves (D11). `LoadingRows` is
  left for a list whose shape is not known yet.
- **Empty.** `EmptyNote` inside a framed region, `EmptyState` for a list that is the page's subject
  ([`states.md`](states.md)).
- **Current.** `current`, above.
- **Never open.** A row does not grow in any state (D11, [`../patterns/rows-never-grow.md`](../patterns/rows-never-grow.md)).
  Details go in a popover off the row or on the item's page.

## Row actions

Three answers to *where are this row's controls*, one API each:

- `rowReveal(group?, "control" | "cluster")` is the reveal: hidden until the row's hover
  (`group-hover`), the keyboard (`focus-visible` on the control, `group-focus-visible` on the row,
  `focus-within` in a cluster), an open menu (`data-state=open`), and always on a touch screen.
  `IconAction reveal` is it for one control; `RowActions` is it for a cluster
  (`rowReveal(group, "cluster")`). Use it where the controls are an extra in a shared column: a copy
  glyph, an external-link arrow.
- `DimActions` (`row-dim` in `globals.css`): always drawn, quiet until the pointer is on the row. Use
  it where the controls have a column of their own, which a reveal would leave empty and full width.
- Never type the four mechanisms by hand. A named group (`row`, `path`, `folder`) is passed as
  `group`, because Tailwind reads literal class names only.

## Findings

`FindingList` is a `RowList` of findings: a severity dot, the problem as the title, its consequence
as the second line, and the next action (and a dismiss) at the right, or on a line of its own under
the words on a narrow column (`@container/finding`). The evidence (the measured detail, the advice
and the caller's `extra`: a log sample, a link to copy) opens in a **popover** off the title, which
carries a chevron. It was an accordion, and Traffic's errors grew a log sample under each row.

- `anchor(id)` gives each row an `id` to link to; `defaultOpen` names the linked finding, whose row is
  `current` and whose popover stands open.
- An action with `reveal: true` opens the evidence popover.
- `emphasize` gives the first remedy the command face, for an attention list.

## The phone

Rows keep one line of title and one of subtitle at every width; a narrow face truncates rather than
wraps. `FindingList` moves its actions under the words below `@xl` of its own width. Touch shows
every revealed control.

## Accessibility

- The `<li>` is the list item; the face is the link or button. `current` is announced through
  `aria-current`.
- A finding's title is a button named by the title and described by its severity ("Critical issue",
  "Warning", "Observation") and its consequence; the popover is a dialog named by the title.
- A revealed control is reachable by the keyboard: focus reveals it.

## Example

```tsx
<RowList aria-label="Revisions">
  {revisions.map((revision) => (
    <Row
      key={revision.id}
      onClick={() => setPicked(revision.id)}
      current={revision.id === picked}
      title={actionLabel(revision)}
      subtitle={`${revision.actor} · ${when(revision)}`}
    />
  ))}
</RowList>
```

## What enforces it

- `design-system.spec.ts` (*hover, selection and focus are three different marks*).
- The `finding-list`, `proxy-traffic`, `proxy-tls` and `proxy-tls-monitor` specs read findings
  through their titles and actions.

## Related

[`card-row.md`](card-row.md) for rows you take, [`table.md`](table.md) for readings down columns,
[`surfaces.md`](surfaces.md#7-which-surface), [`states.md`](states.md).
