# Readings in words

A reading is a sentence beside a mark, not a tile (D4). Latency is said in words (D10).

"21.4 requests a minute" under "Traffic" is read. A tile that puts "21.4" over "/min" leaves the
reader to join them, and a row of four grey tiles opening a page is the look this rule removed.

## Where a figure goes

Ask what the reader opened the page for, then put each figure in the first place that fits.

| The figure is | It goes | With |
| --- | --- | --- |
| What the thing is right now (9 containers running, 2 stopped) | a fact on the identity line | `Fact` in `IdentityHeader facts` |
| A part of the picture (what a node carries) | the node's line of detail | `WireFlow` node `hint` |
| A measure with a recent history | one line: a glyph on the measure's hue, the name, the reading in words, its last hour at the end | `ReadingLine` in `ReadingLines`, `ReadingMark`, `NoReading` while it waits |
| A count that narrows the log under it | the same line, pressed | `LensReadingLine` |
| Parts of one whole | one bar with a legend | `SplitBar` |
| The heaviest few | a ranked list, figures in aligned columns | `RankList`, never a list of growing bars |
| A band (what happened across a window) | one block of one height | `BarReading` |
| A series over time | a chart with Min / Mean / Max / Now under it | `ChartPanel` |
| A pair of label and value about the thing | a stacked list | `DetailList` |

A `StatTile` stays only in a set read across as a table, each tile with a sparkline or a meter and
its tone: Metrics' five readings and the Docker overview's band. A project's Overview has none
(the operator's rule: a real logo, a sentence, one coloured picture).

## How it is said

- **A sentence, with the unit in it.** "12 open of 100", "3 certificates, 1 expires in 6 days".
  One helper per kind of value, so the same figure reads the same on every page: `lib/format.ts`
  and the rest of [`../components/values.md`](../components/values.md).
- **Latency in words.** "19 in 20 inside 412ms", never p95. The exact figure can sit behind an
  `Explain`. Lint `design-system/latency-words` refuses p50, p95 and p99 in text.
- **Colour is the reading's, not decoration.** A hue per measure (`HUE`), and a signal tone only
  when the reading needs a look. A state is a `Status`: a dot and its word.
- **Nothing to say is said.** A reading that has not arrived is `NoReading` at the line's own
  height, and one that stopped updating is `Stale` beside it. A part is never left out.
- **In and out are one pair** in their two hues (`InOut`), and an age keeps itself current (`Ago`).

## Held by

Lint `design-system/latency-words` and `design-system/primitives` (the old bar list);
`deploy-project.spec.ts` (no stat tile on a project's Overview); `bar-reading-model.test.js`. The
rest is review.

## Related

[`../components/readings.md`](../components/readings.md),
[`../components/values.md`](../components/values.md),
[`../components/values-facts.md`](../components/values-facts.md),
[`registers.md`](registers.md) (*A project's Overview reads in sentences*),
[`../recipes/reading-page.md`](../recipes/reading-page.md).
