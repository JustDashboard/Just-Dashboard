# Readings

A reading in words, a figure that moves, a band, a rank, a share and a chart (D4, D10).

**Members:** `ReadingLine`, `ReadingLines`, `ReadingMark`, `NoReading` (`reading-line.tsx`);
`StatTile`, `StatGrid`, `StatButton`, `TileBand` (`stat-tile.tsx`); `Meter`, `utilisationTone`
(`meter.tsx`); `BarReading` (`bar-reading.tsx`, its arithmetic in `bar-reading-model.ts`);
`RankList`, `RankSkeleton`, `SplitBar`, `percent` (`rank-list.tsx`); `Sparkline`, `TileTrend`
(`sparkline.tsx`); `LiveFigure`, `LiveBytes`, `LandedFigures`, `HUE`, `CoreBars`, `SeriesKey`,
`StreamState` (`live-figure.tsx`); `ChartPanel` (`chart-panel.tsx`) and `components/metrics/`.

## Which one

| You are saying | Use | Not |
| --- | --- | --- |
| One reading the page is opened for, in words | `ReadingLine` with a `ReadingMark` | a `StatTile` row |
| A run of figures read across as a table, in a sanctioned headline set | `StatGrid` of `StatTile`s | tiles on a project's or the host's Overview |
| A figure whose press narrows what is under it | `StatButton` | a tile wrapped in a button by hand |
| How full something is, 0 to 100 | `Meter` | a hand-drawn track and fill |
| What one whole is made of | `SplitBar` (with `legend` when nothing else names the parts) | four widths in a hand-built `flex` |
| A band between the identity line and the table | `BarReading` | a bar with rows under it |
| The heaviest few, ranked | `RankList` (a `SplitBar` over it for the proportion) | `BarList`, bars that grow row to row |
| A reading's last hour | `TileTrend`; in a table cell `Sparkline` | a chart in a tile |
| A figure fed by a socket | `LiveFigure` / `LiveBytes` | a number re-rendered every frame |
| A measure over time to read values off | `ChartPanel` / `MetricChart` | a sparkline with an axis |

A latency is said in words ("19 in 20 inside 412ms"), never as p95 (standard, T6 not adopted).

## ReadingLine and ReadingMark

**Purpose.** One reading as a sentence on a mark: what a row of `StatTile`s becomes when the figure
is better said than shown ("21.4 requests a minute" under "Traffic").

**Anatomy.** `mark` (a `ReadingMark`), `name` as an eyebrow, the reading in words, one line of
`hint`, and `trend` (a `TileTrend`) at the end from `sm`. `ReadingLines` runs them two to a row from
`xl`, out by the lines' inset so the marks stand on the page's edge.

**Use when** a page reports a handful of measures and the reader wants the words. **Not** for a
part of a whole (`SplitBar`) or a ranked set (`RankList`).

**Props.** `href` makes the line a link and `label` names where it goes; `onPress` makes it a
button laid over the line (a lens narrowing the log) and `label` is what the press does; neither
and it is a reading with no hover and no tab stop. `ReadingMark({ tone, hue, glyph | children })`
draws the measure's glyph on its own hue (`HUE.cpu`) and switches to the warning or danger tile
once the reading is past a line; `success` is not a reading's worry and draws as the hue.

**States.** Every state is one height: the third line holds its place with no hint, and a reading
that has not landed is `NoReading` ("No reading yet", "Not running") in the reading's own place.

**Phone.** The trend hides below `sm`; the words wrap no further than the line's three rows.

**Accessibility.** The link's or button's name is `label`; the mark and the trend are decoration
(the trend carries its own `label` as an image).

**Example.** `components/procs/pm2-app-overview.tsx` (`AppReadings`), the database home's
activity lines (`database/home/activity-lines.tsx`).

```tsx
<ReadingLine
  href={performance}
  label="Sessions on Performance"
  mark={<ReadingMark hue={HUE.cpu} glyph={Users} tone={worry} />}
  name="Sessions"
  hint="2 active · 0 waiting on a lock"
  trend={<TileTrend values={values} label="Sessions over the last hour" />}
>
  12 open of 100
</ReadingLine>
```

The host Overview's `Mark` and the PM2 page's local `ReadingMark` are this mark under other names;
the page passes (WP-28, WP-29) move them and the service and container overviews' `StatGrid`s onto
`ReadingLine`, keeping the `service-readings` and `container-readings` test ids on the wrapper.

## StatTile, StatGrid and StatButton

**Purpose.** A headline figure in a set read across as a table: a small name over a 24px figure,
then a meter or a trend, then at most one hint. `StatGrid` runs them with a hairline between and
nothing around them.

**Use when** the page is one of the sanctioned headline sets, and each tile carries a sparkline or
a meter and its tone:

- Metrics' five readings (processor, memory, load, network, disks);
- the Docker overview's band of figures over its table;
- a log lens's counts, as `StatButton`s that narrow the log.

**Not** on a project's Overview or the host's (the operator's rule: a logo, a sentence, one
picture), and not as the row of bare grey tiles that opens a page (D4). Every other tile row is a
page waiting for its pass (PRM-29). A tile that is a destination is a `Link` around a `StatTile`;
`StatLink`, which drew that, went in 0.9.0.

**Props.** `label`, `value`, `trailing` (a unit or a delta beside the figure), `meter` (0–100) or
`trend`, `tone` (colours the figure, never a mark beside it), `hint`. `StatGrid dense` sets two to a
row on a phone; `framed` restores a box. `StatButton` adds `pressed` and the revealed funnel.

**Example.** `app/(dashboard)/metrics/page.tsx`.

## Meter

**Purpose.** A bar for a figure between 0 and 100, coloured by the same `utilisationTone` that
colours the figure above it.

**Props.** `value` (clamped), `tone` or `color` (a measure's hue, for a bar that says which reading
it is rather than how worried to be), `size` (`thin` under a figure in a tile, `default` in a row,
`cell` a 32px bar beside a figure in a table cell, which is what the process table's `MiniBar` is),
`mark` (a threshold line, in the muted ink), `label`, `decorative`.

**Accessibility.** `role="meter"` with its value and `label`; `decorative` hides it from a screen
reader where the figure beside it already says the reading (a table cell).

## SplitBar

**Purpose.** One whole as one strip of coloured parts, the block's width whatever the figures, so
only the join between the parts moves when the window changes.

**Anatomy.** An 8px track (`size="sm"` 4px, for a table cell), the parts in the order given with
the one 2px gap between them (`gap={false}` for a bar too small to show it), and the track only
where no part is. A part above zero is never narrower than `minShare` (1% by default), taken back
out of the others, so the one key type of a hundred still shows.

**Props.** `parts: { key, count, color, label?, figure?, note?, mark?, pickLabel? }[]`, `label` (the
bar's accessible name), `total` (scale against a larger whole: an interface against the busiest,
the track showing past it), `legend`:

- `none` (the default): the bar alone, for a bar a `RankList` or a sentence names;
- `static`: under it, each part's dot (or its own `mark`), name, figure and a quieter `note`;
- `pick`: each entry a button that narrows the page to the part (`onPick`), `aria-pressed` while it
  is `picked`; pressing it again is the caller's to clear.

**Use when** the reading is parts of one whole: a table's bytes, a database's keys by type, traffic
by country, in and out. **Not** for a list of the heaviest few (`RankList`) or a band
(`BarReading`). Four bars were drawn by hand beside it (the schema's `Composition`, the home's
`TypeBar`, Redis's `TypeComposition`, a table's `Split`) and the metrics page's own `SplitBar`
against the busiest; the page passes move them here (WP-20, WP-23, WP-28).

**Example.**

```tsx
<SplitBar
  label="Keys by type: string 600, hash 399"
  legend="pick"
  picked={type}
  onPick={(key) => setType(key === type ? undefined : key)}
  parts={shares.map((share) => ({
    key: share.type,
    count: share.weight,
    color: kindOf(share.type).color,
    mark: <KindMark type={share.type} className="size-3" />,
    label: kindLabel(share.type),
    figure: share.figure,
    note: percent(share.weight / whole),
  }))}
/>
```

**Enforced by** `rank-list.test.js` (widths, floor, the legend's two forms) and the specs that
read `[data-slot=split-bar]`.

## RankList

**Purpose.** A ranked list that is read: a name, a figure and the name's share in columns that
line up and end on the block head's right edge, every row 32px. **Not** `BarList`: bars of
different lengths are white slabs with figures floating beside them (the `bar-list` lint rule).
A proportion that wants a picture is a `SplitBar` over the list, a row's `swatch` its legend.
`RankSkeleton` waits at the list's own height.

## BarReading

The band's block, one height in every state; the rules are below under *The bar reading*.

## Sparkline and TileTrend

`Sparkline` is one SVG path the width of a table cell, with no axis and no tooltip: it answers "did
anything happen here". `TileTrend` is the one shape of a reading's last hour, the slot's full width
and 36px. A steady series is a level line with no area; under two points nothing is drawn. The
rules are below (§10).

**Enforced by** `sparkline.test.js`.

## LiveFigure and its kin

`LiveFigure` glides to each socket frame's value, counting up once when it lands; `LiveBytes` is
the same on the bytes ladder. The unit stays outside the count, so a change of unit swaps the
figure without counting, and **the space before a unit is a no-break space in the unit's own
span**: as a plain text node it became an anonymous item of a flex parent and lost its space ("Held
by 31.4GB"). `LandedFigures` marks figures that have already landed (a line that swaps which part it
shows). `HUE` is each measure's colour wherever it is drawn; `SeriesKey` the key before a reading's
name; `CoreBars` every core's share; `StreamState` whether the socket is live.

**Enforced by** `live-figure.test.js`.

## Related

[Wiring pictures](wiring.md) (a reading in a node's hint), [Status and tags](status-tag.md) (a
state is a `Status`, never a tinted figure), [Facts lines](values-facts.md) (a fact on the identity
line), [`../patterns/readings-in-words.md`](../patterns/readings-in-words.md),
[`../patterns/page-holds-still.md`](../patterns/page-holds-still.md).

## Rules carried over from design-system.md

The sections below are the rules that were in `design-system.md` §10, kept verbatim but for what
0.9.0 changed.

## 10. Charts

`components/metrics/` is a third design-system file in all but name. Every chart goes through it
rather than assembling its own recharts tree — adding a measurement should mean naming a series.

- The x-axis is **numeric over time**, never a category axis of pre-formatted labels: a category axis
  spaces every bucket equally, which lies whenever the record has a hole in it.
- A series with no numbers anywhere in the window is **dropped rather than drawn flat at zero**.
- **A measure some days have and others do not is a mark on each day that has one**, on the
  window's own days, never a line through the days that do. Deployment history's release time per
  day was a sparkline over the days something shipped: four releases a week apart drew as one
  slope, with no date and no scale to read it against. It is a bar on its day now, a tick on a
  day nothing succeeded, the window's median ruled across and the top of the scale named.
- **Live and recorded data are never spliced into one line** — the cadences differ by two orders of
  magnitude. Where a page offers both, Live is a range of its own beside the recorded ones: a
  project's Runtime draws the container's stats socket over its last five minutes
  (`useContainerLive`, which keeps the window per container so the Overview's minutes are already
  on it), and 1h–7d from the record, in the same panels. Live is drawn as five-second buckets,
  each a mean inside the envelope of its frames' peak (`bucketLive`): a frame a second drew a
  saw-tooth beside recorded ranges that drew a mean and its peaks, and the operator read the two
  as two kinds of chart. Bucketing within one source is not splicing, and the one-second spike
  survives as its bucket's peak.
- **An axis ends on a round figure and ticks at its quarters.** Fitted to the data, recharts split a
  3.1 MB/s peak into 781.3 KB/s steps, and a container idling at 0.2% drew five ticks that all read
  "0%". A container's charts take `byteScale` and `cpuScale` (`lib/container-usage.ts`): byte
  tops whose quarters print as whole figures, a processor axis of at least 1% that grows by the
  hundred past one core, headroom so a peak never sits on the top rule. A percentage tick keeps the
  decimals it needs and drops the zeros it does not ("0.25%", "50%").
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
- **A column of charts read against each other takes one gutter.** The y-axis gutter fits its labels,
  which puts a chart's plot wherever its widest label ends; `axisWidth` on `ChartPanel` fixes it
  instead, so the four charts of a statement's history (a statement's page under Performance, its
  History view) start their plots on one line. Fitted there, the gutter came out narrower than its
  labels and clipped them.
- A chart's `height` is a **floor**, not a fixed size. Panels in a row stretch to the tallest of
  them, and a fixed plot puts the surplus between the chart and its legend as a band of nothing.
- **A chart is a frame and a plot, and only the plot is recharts.** The frame (`MetricChart`) is in
  the page from the first render at the chart's height, with the keyboard, the pinned reading and
  the crosshair. The plot is drawn when the reader comes within 600px of it (`useNearViewport`),
  stays drawn once it is, holds the frame it last drew while it is out of reach
  (`metrics/plot-held.ts`), and is memoised with its legend, so a panel re-rendered for the live
  figure in its head does not reconcile a tree whose rows are the same array. A page therefore
  hands a chart **one array per reading, not one per render**: rows built in the render body redraw
  every chart on every frame of the socket. Opening Metrics drew eleven recharts trees in one
  blocked stretch and redrew them every two seconds; it now draws the first screen's.
- **A scale fitted a little above a limit splits into unround steps** (143 / 286 / 429 MB), so
  `yTicks` on `MetricChart` and `ChartPanel` names the ticks. A container's memory chart is scaled to
  its limit and ticks at the limit's quarters, so the top tick names the limit — on Docker's
  container page and on a project's Runtime alike, since both take `useContainerScales`.
- **A series' hue is the measurement's, not the chart's position.** The five readings' hues come from
  one map (`HUE` in `components/live-figure.tsx`) — processor `--chart-1`, memory `--chart-2`,
  load `--chart-3`, network `--chart-5`, disks `--chart-4` — and Metrics draws every line of a
  measurement in it, so Pressure's CPU, memory and I/O lines are the three tiles' colours; a chart's
  second line takes the hue furthest from its first (out `--chart-2` against in, write `--chart-3`
  against read). A project's Runtime and a container's Usage tab take the same map and the same
  second lines, with processes in load's hue, because the operator asked that a container's
  metrics read the same as the host's: its network is the line the host's network is.
- **A reading's last hour in a deployment page's `StatTile` or an Overview reading line is `TileTrend`**
  (`sparkline.tsx`), the one shape for it: its slot's full width, 36px tall, rising once (§11 *arrived*). Nothing is drawn
  below two points, which is not yet a shape. **A series that never moves is a level line with no
  area under it**: at its level against a fixed `max`, through the middle on a scale of its own
  (where it has no level to sit at), on the floor when it is nothing at all. Until 0.9.0 a steady
  series drew nothing, which on an idle server left three of the database home's four activity
  lines with an empty slot, and against `max` it drew the area under the line, so a cache steady at
  99% was a grey block pinned to the top of the slot. Its colour is a series colour, never a status
  one: a failing share is `--chart-3`, because `--destructive` on a line says the line itself is an
  error (§3).
- **A part of a tree is one bar of two inks.** A measured plan step's time on the Query page
  (`query/plan-view.tsx`) is drawn against the whole plan's: the step's own share in the time
  series' colour and, behind it at a third of that ink, the share of the step with everything under
  it. One bar says what the step took and what its branch took, and only the solid inks down the
  plan add up to the whole. The figure beside it is the step's own, which is what "slowest first"
  orders by.

### The bar reading

A band, the blocks between a page's identity line and its table, draws each block as one `BarReading`
(`components/bar-reading.tsx`; its logic and tests are `bar-reading-model.ts` and
`bar-reading-model.test.js`): a head, a bar, one fixed-height readout line and one strip of marks,
the same height in every state. **No rows under a bar in a band.** The bands over the Docker,
Processes, Packages, Proxy and Database pages were a bar with a list under it, the five heaviest
things as rows and a sentence where there were none yet. Each poll reordered the rows; a block grew
from a sentence to five lines when its first reading landed, which pushed the table under it down the
page; and the rows repeated that table's columns. Any new band takes this component, and what it
would have listed is the table's. Scheduled's hour chart and the Overview's Storage are not shares
of one bar and keep their own shapes.

- **The line holds one part.** At rest that is the heaviest, or the part the table is narrowed to, or
  the first given on a `ticks` or `fixed` reading, so those lead with the one worth reading: Recent's
  newest, Certificates' soonest to end, the worst standing. Hovering a span or a mark, or focusing a
  mark, puts that part on the line and fades the other spans; off the block it returns. A sliver too
  narrow to point at is still reached by its mark, which is also how a keyboard reaches any part. A
  touch press previews first and acts on the second, because a tap has no hover. The line rises when
  its part changes (§11), and its figures have landed already, so reading another part shows its
  value rather than counting up to it (`LandedFigures`).
- **A press is the part's.** `press` narrows the table, and `pressed` keeps that part on the line and
  lit; `press.opens` or `href` opens its page, from the span, the mark or the line alike. `control` is
  a part's own verb, drawn on the line while it is the part there, as a kind's Reclaim is on Images.
- **Order holds between polls** (`settleOrder`): a part must pass the one above it by 25% before they
  change places, so neighbours within a rounding of each other stop swapping and a mark stays where
  it was pointed at. `fixed` keeps the order given: standings, kinds, newest first.
- **`trail`** draws the part's readings since the page opened as a sparkline on the line, only for
  readings that move. A flat trail or one under three readings draws nothing: the line already
  says the part's figure, and a trail is there to show it moving.
- **`live` is reserved for socket-fed figures** (§11's rule) and draws a breathing cap at the end of
  the filled bar. The container socket's bands carry it; a polled reading has a trail and no cap, even
  beside a head figure that is live.
- **`ticks`** makes the bar an axis, each part a tick at its `at`: Recent is a day (`timeAt`, anything
  older on the far end where it can still be reached), the proxy overview's Certificates ninety days.
- **`hatch`** draws the share of a span a prune would take, hatched inside it. **`working`** sweeps the
  bar while work on what it draws runs, a registry check or a prune.
- **`message`** fills the line and the strip, two lines at most: no reading yet, nothing to say, a
  failed read. What needs more goes under the block. **`note`**, at the strip's end, holds what the
  strip leaves out or a control for the whole block. **At most six marks**; a part past them keeps its
  span and is read under the pointer.
- **A mark is bare**: the part's picture alone, drawn a little larger than the glyph a caller passes
  (`scale-115`), with no rule under it. Until 0.8.6 each mark sat on a short rule in its part's
  colour, which made the strip read as a row of tabs; it was removed at the operator's request. The
  colour is the line's tick and the span's, and a pointed or pressed mark takes the row's hover or
  accent ground.
- **A band must not mount, unmount or change columns as data lands, and its page renders it during
  load.** Whether a block is drawn is decided by what the host is, never by what a read has said so
  far, so a page whose readings land does not move.

## Charts

`components/metrics/` is a third design-system file in all but name: every chart goes through it rather
than assembling its own recharts tree, which is how the old Overview page ended up with three tooltip
formats and no way to compare a moment across four charts. Adding a measurement should mean naming a
series.

- `metric-chart.tsx` — `MetricChart` + the `Series` descriptor. The x-axis is **numeric over time**, not a
  category axis of pre-formatted labels: a category axis spaces every bucket equally, which lies whenever
  the record has a hole in it. It owns the shared crosshair, drag-to-zoom, event markers, thresholds and
  the one tooltip listing every series at the hovered instant. Click or Enter/Space pins a shared
  instant; Left/Right and Home/End inspect samples, Escape releases it, and pinned overlays show
  each chart's values without pointer movement changing the instant. The Metrics page adds sample
  controls and a journal History link for the surrounding two-minute window.
  The component is the chart's frame, in the page at its height from the first render; the recharts
  tree is `Plot` inside it, drawn once the chart is within 600px of the view, memoised, and held
  still while out of reach (`plot-held.ts`), so a page of charts pays for the ones on screen.
- `chart-panel.tsx` — the header/chart/legend shape and the empty state. A series with no numbers anywhere
  in the window is **dropped rather than drawn flat at zero**, which is what makes a kernel without PSI
  say so instead of reporting three healthy zeroes.
- `series-legend.tsx` — min/mean/max/last, plus **"At cursor"**: while the pointer is over any chart, every
  legend shows the value its series held at that instant. Max reads the peak column where a series has
  one, since the maximum of the *means* is exactly what a downsampled window hides.
- `components/sparkline.tsx` (shared since 0.9.0) — a bare SVG path for a table cell, not recharts:
  forty containers would otherwise mount forty responsive containers and resize observers. It also
  exports `TileTrend`, the one shape a reading's last hour takes in a `StatTile`'s `trend` slot or at
  a `ReadingLine`'s end: the slot's full width, 36px, rising once. It draws nothing below two points;
  a steady series is a level line with no area. Its colour is a series colour, never a status one,
  so a failing share is `--chart-3`.
- `range-picker.tsx` — the window control (pan and zoom-out appear only once a window has been
  dragged).

`lib/metrics/crosshair.ts` holds the hovered instant **outside React**, for the reason the live buffer is:
a pointer crossing a chart fires continuously, and a context above the router would re-render the terminal
and the log tail on every mousemove. The value is a **timestamp**, not a row index — charts on a page do
not share a row array.

**A date is printed through a format built once**, on an axis and everywhere else. `toLocaleString`,
`toLocaleDateString` and `toLocaleTimeString` build a fresh `Intl.DateTimeFormat` on every call that
carries options (about 75 µs, against 2 µs to ask one that exists), and recharts asks for a label for
every candidate tick of every render: on Metrics that construction was the largest single cost of the
page while nobody touched it. `dateFormatter(options)` in `lib/format.ts` builds the format on first
use and keeps it, rebuilding only when the zone's offset moves under an open tab; the shapes several
modules print are exported beside it (`clockAt`, `minuteAt`, `dayAt`, `weekdayAt`, `dayMinuteAt`), and
`timestamp`, `clock`, `clockMinute`, `calendarDate` and `minuteSpan` go through the same. A shape one
module prints is declared at the top of that module. `eslint.config.mjs` refuses
`toLocale…String(undefined, { … })` on a date (`no-restricted-syntax`); a named locale with a zone
known only when it is drawn (a schedule's next run, read in the schedule's own zone) is left alone.
