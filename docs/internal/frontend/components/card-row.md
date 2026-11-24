# Card row

A thing you open: a bordered, rounded row with a logo tile, a name over a mono line, state columns
and an arrow (D5).

**Members:** `ChoiceList`, `ChoiceRow`, `ChoiceListSkeleton`, `GroupRule`, `useChoicePress`,
`choicePress`, `CONTROL`, `CHOICE_ROW_BOX` (`components/choice-row.tsx`); `OpenCard`
(`components/open-card.tsx`).

## Purpose

`ChoiceRow` is one *instance* among many of one kind — a repository, a project, a webhook, an alert
rule — that you open or take. It is a card because it is a destination: the lit edge says
*takeable* (§16), which a `Row` you read does not carry. A configured thing in a list is a
`ChoiceRow` by the deploy standard; a reading in a list is a `Row` ([`list-row.md`](list-row.md)).

`OpenCard` is the same job at the size of a shelf card: a repository with its languages and its
pull requests, a drawing with its preview.

## Anatomy

```
╭───────────────────────────────────────────────────────────────╮
│ [leading]  Title (the control)           [trailing]  →  [⋯]   │  56px inside a 1px edge: 58px
│            description (hint, muted)                          │
╰───────────────────────────────────────────────────────────────╯
```

- `leading`: the thing drawn as itself (`ProductLogo size="sm"`, a `ProjectMark`, a `KindTile`, an
  avatar).
- `title` with `verb`: the title is the `<button>` (or the `Link` with `href`) and `verb` is its
  accessible name, "Edit api-production" (§12). The verb holds every word the title shows (WCAG
  2.5.3, label in name; `axe-gate.spec.ts` reads it): "Open ens3" over a title that reads "ens3"
  passes, and the same verb over "ens3 Ethernet" fails, because a voice user says what they see. A
  word that says more about the thing (its kind as a `Tag`, its stack, its protocol) goes in
  `description` or `trailing`, outside the control, or into the verb as well.
- `description`: one line under the title; `columns` sets them side by side for a list read down its
  columns.
- `trailing`: readings and inline controls (a `Status`, a meter, a `Switch`), then the arrow, then
  `actions` (`VerbActions` or a `VerbMenu` in `DimActions`). **Verbs end → then ⋯**: an inline
  control stands before the arrow, the menu is last. `OpenCard` draws the same order.
- `children`: a second band at the card's full width. Avoid it: a row with children is taller than
  its neighbours.

The box is `CHOICE_ROW_BOX` (`min-h-14` with `px-3 py-2.5`) inside its 1px edge, so every row is
58px with a second line or without. A 40px mark makes it 62px.

## Props worth knowing

| Prop | Does |
| --- | --- |
| `onSelect` / `href` | Chooses in place, or navigates |
| `disabled` | Something with nowhere to go yet: keeps its card and actions, loses the arrow, the pointer and the control. Never fade a row with an opacity |
| `busy` | A light runs round its edge while the thing works (§11 *live*) |
| `index` | The arrival stagger; leave it out for a list under a poll |
| `arrow` | `false` leaves the arrow out, where the row's verb is drawn as a button's face in `trailing` instead (the Databases control center's found rows, at the operator's word: *No arrow on a card* in the [standard](../standard.md)) |
| `workspaceItem` | The workspace's item id and name on the title control |

## The press

`useChoicePress({ href, onSelect, disabled })` is the one press of a card's surface, used by
`ChoiceRow`, `ChoiceCard` and `OpenCard`. It returns `onClick` for the surface and `go` for the title
control. The rule (`choicePress`, unit-tested) ignores a press:

- on a control of the card's own (`CONTROL`: links, buttons, inputs, labels, menu items, switches);
- carried up by React from a portal that is not on the surface (an open menu's group label);
- that ends a text selection, so selecting a path to copy it does not open it;
- on a disabled card.

With Cmd, Ctrl or Shift held, a card with `href` opens it in a new tab. The surface is never a second
tab stop: the title is the keyboard's way in.

## OpenCard

`OpenCard({ index, onOpen, href, title, titleHint, mark, aside, subtitle, media, menu, foot, children })`:
`BlurFade`, the lit edge, the surface press, the title button, the arrow and the menu, in that order.

- `media`: a preview across the card's top (a drawing).
- `aside`: a reading at the end of the name's line (how far a branch has drifted).
- `subtitle`: the lines under the name.
- `children`: the body; `foot`: the card's floor, pushed down so a run of cards lines its feet up.
- `href` is what a modified press opens in a new tab; `onOpen` is the plain press.

`RepoCard` (Git), `DrawingCard` (Excalidraw) and the Overview's workspace card use it. The deploy
and database fleet cards still carry the shell by hand.

## States

- **Loading.** `ChoiceListSkeleton({ rows, mark = "sm", lines = 1, aside = true, band })` draws
  ghost cards at `CHOICE_ROW_BOX` inside the same edge: a mark's place (`sm` 32px, `md` 40px,
  `false` none), a bone where the name goes, a second under it with `lines={2}`, and a trailing
  bone hidden on a phone. Pass `lines={2}` whenever the rows carry a `description`: a card with
  one is 59px, a pixel over the box. `band={1 | 2}` is for rows that draw `children` on a phone
  only: below `sm` each ghost holds that many lines of hint under its line, so a phone's list
  waits as tall as it lands. Every piece keeps `data-slot="skeleton"`. `rows={0}` draws nothing. It replaced
  `LoadingRows` forced into a card with `[&>*]:h-14`; the Network area's `GhostRows` and
  `CardRowsSkeleton` are the hand copies still to move onto it.
- **Empty.** `EmptyState` with the products the list is for as its `mark`.
- **Disabled, busy.** Above.
- **Never grows.** A failure, a sample, a detail goes in the trailing state with a popover, or a
  `Notice` above the list (D11).

## The phone

The card keeps one height; trailing readings that do not fit go (`wide &&`), or a short word replaces
a long one. A description may wrap on a phone where the sentence *is* the thing (an alert rule).

## Accessibility

The title control carries the verb; the arrow is `aria-hidden`; the actions are their own controls
and their presses never reach the card. A `ChoiceList` takes an `aria-label` when the page has more
than one.

## Example

```tsx
<ChoiceList aria-label="Alert rules">
  {rules.map((rule) => (
    <ChoiceRow
      key={rule.id}
      verb={`Edit ${label}, ${sentence}`}
      onSelect={() => edit(rule)}
      leading={<KindTile kind={rule.kind} />}
      title={label}
      description={sentence}
      trailing={status}
      actions={<VerbActions dim verbs={verbs} menuLabel={`Actions for ${label}, ${sentence}`} />}
    />
  ))}
</ChoiceList>
```

## Related

[`choice-card.md`](choice-card.md) for a thing you pick, [`list-row.md`](list-row.md) for a row you
read, [`surfaces.md`](surfaces.md#7-which-surface), [`../patterns/registers.md`](../patterns/registers.md).
