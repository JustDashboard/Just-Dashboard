# Rows never grow

A row is one height in every state. Nothing expands under it. (An operator decision; T5, expandable
rows, was not adopted.)

A list is scanned by position. When one row opens in place, every row under it moves, and the
reader's eye loses the one it was on. So a row never gets taller: not for a detail, not for a
failure, not for a second line that appears on hover, and not while it loads.

## One height each

| Row | Height | File |
| --- | --- | --- |
| `ChoiceRow`, a thing you open | 58px inside its edge (`CHOICE_ROW_BOX`) | `components/choice-row.tsx` |
| `Row`, a thing you read | one pitch, with or without its second line reserved by the list | `components/row-list.tsx` |
| `RailRow` | 48px (`railRowClass`) | `components/rail.tsx` |
| A table row | one line; a cell truncates, it does not wrap | `components/ui/table.tsx` |
| `ReadingLine` | `readingLineHeight` | `components/reading-line.tsx` |
| `BarReading`, a band | one height loading, empty, failed and full | `components/bar-reading.tsx` |

## Where the rest goes

| The row wants to show | It goes |
| --- | --- |
| A few facts or a sample | a popover off the row (a finding's evidence, a folded run of events) |
| Everything about the thing | its own page, with the list as a rail beside it ([`../recipes/detail-with-rail.md`](../recipes/detail-with-rail.md)) |
| A small add or edit form | a sheet (`SidePanel`) |
| Its verbs | a `⋯` menu at the row's end, or icon actions revealed in place (`RowActions`), laid out, never overlaid |
| A failure | the trailing state: "Failed" in the danger ink, the server's words in a popover |
| Work in flight | the row's own state (`ChoiceRow busy`), not a bar under it |

## The rules

1. **No fold under a row.** There is no accordion in the product. A group is a `GroupRule` over
   its rows, not a parent row that opens.
2. **No band under a bar.** A bar with rows shared out beneath it is gone; a band is a
   `BarReading`, and a ranked list is a `RankList` of its own.
3. **Long words truncate.** A name ends in an ellipsis with the whole of it as its title; a path or
   an address wraps only where a `MonoValue` lets it, in a place that has the room.
4. **A phone drops columns, it does not stack them into a taller row.** A table's secondary
   columns go below `sm` (`secondary` on the head and the cell), and a card keeps its one line of
   state.
5. **A row waits at its own height** (`ChoiceListSkeleton`, `RowsSkeleton`, `RailGhosts`), and a
   row that arrives rises once.
6. **A row's press is one thing.** The whole card is the pointer's target, and the keyboard's way
   in is the row's name, which says what it opens. Inner controls keep their own press
   (`CONTROL`, `useChoicePress`).

## Held by

`design-system.spec.ts` (*nothing on … moves while its reads land*) and the list specs that
measure every row at one height; `choice-row.test.js` and `rail.test.js` for the boxes. Whether a
detail belongs in a popover or on a page is review.

## Related

[`../components/card-row.md`](../components/card-row.md),
[`../components/list-row.md`](../components/list-row.md),
[`../components/table.md`](../components/table.md), [`../components/rail.md`](../components/rail.md),
[`page-holds-still.md`](page-holds-still.md), [`../standard.md`](../standard.md).
