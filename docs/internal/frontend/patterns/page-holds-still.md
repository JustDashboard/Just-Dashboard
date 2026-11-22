# The page holds still

The page has its final shape while it loads, and rows and bands keep one height in every state
(D11).

A reader who is about to press something must find it where it was. A block that grows when its
read lands, a spinner that collapses, a status that pushes a field down a line: each moves the
thing under the pointer.

## What waits in whose place

Every wait is the skeleton of what is coming, at the size it will have.

| What is coming | It waits as |
| --- | --- |
| The identity line | `IdentityHeader loading`, with what is already known (the name from the address, the mark) drawn as itself |
| Card rows | `ChoiceListSkeleton`, at `CHOICE_ROW_BOX` |
| Rows you read | `RowsSkeleton`, at a `Row`'s pitch |
| A ranked list | `RankSkeleton` |
| A rail | `RailGhosts`, at the 48px row |
| A picture | `WireFlow loading`: the hub, and rows where the nodes will be |
| A reading line | `NoReading`, at `readingLineHeight` |
| A band | `BarReading`, which is one height loading, empty, failed and full |
| A search box, a view strip, a toolbar | their own bones (`SearchInputSkeleton`) or the control itself, disabled |
| A panel whose shape is not known | `LoadingPanel`, `LoadingRows`; in a line of words, `Bone` |
| A whole page | `PageState skeleton`, given the page's final shape |

A `Spinner` is only for inside a control or beside its own words. Work of unknown length is a
`Sweep`.

## The rules

1. **Reserve by shape, never by a fixed number.** A skeleton takes its box from the component it
   stands for, so the two cannot drift. A hand-typed `h-14` beside a 58px row is a 2px jump.
2. **Everything below the fold waits its turn.** When an upper block's height depends on its read,
   `Hold` keeps what follows off the page until that read answers, then lets go once and for good.
   It also lets go after `HOLD_AT_MOST_MS`: a hanging read must not hide the rest of the page.
   The rule in `globals.css` that hides the page's later blocks names its subject as one of the
   page's own blocks (`… ~ :is([data-slot="page"] > *)`): written `… ~ *`, every element restyled
   anywhere ran its `:has()` over the subtree of every sibling before it, a quarter of a second a
   frame on a run page with three thousand lines of build log. A rule with `:has()` stays off
   elements that hold long lists for the same reason.
3. **Arrival is one motion.** A block that lands rises once (`animate-rise`, `Page rise`); a row
   that is new since the page opened rises (`useArrivals`); a figure counts up once. A refresh, a
   save and a re-render are not arrivals and move nothing.
4. **A state arrives at the end of a line.** "Unsaved changes", a count, a `Status`: at the head's
   far end or in a column of its own, where it pushes nothing.
5. **A failure does not take the data away.** A read that fails over data already shown says
   `Stale` beside it and keeps the last reading. Only a read with nothing to show is an
   `ErrorState`, in the block's own place and height where the block has one.
6. **A view in the address is written at once.** `?tab=` goes to the history with `replaceState`,
   never debounced: a debounced write cancels a navigation the router has pending.
7. **A row and a band never grow** ([`rows-never-grow.md`](rows-never-grow.md)).

## Checking it

Assert position, not only height: a spec measures where a landmark below the block sits before and
after the read lands (`getBoundingClientRect().top`), on both a wide screen and a phone. Two blocks
of equal height can still swap places. A fixture clock ages with its worker, so assert relative
times by pattern.

## Held by

`design-system.spec.ts` (*nothing on … moves while its reads land*, layout shift under 0.02),
`identity-header.test.js` (the loading form), and each page's own spec for its blocks.

## Related

[`../components/states.md`](../components/states.md),
[`../components/page-header.md`](../components/page-header.md) (the loading line),
[`../components/readings.md`](../components/readings.md#the-bar-reading),
[`../foundations/motion.md`](../foundations/motion.md), [`live-data.md`](live-data.md).
