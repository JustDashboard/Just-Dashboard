# Readings

A reading in words, a figure that moves, a band, a rank and a chart (D4, D10).

**Members:** `ReadingLine`, `StatTile`, `StatGrid`, `StatButton`, `Meter`, `BarReading`,
`RankList`, `SplitBar`, `RankSkeleton`, `Sparkline`, `TileTrend`, `ChartPanel`, `LiveFigure`.

**Status: moved text.** WP-17 rewrites this page in wave 1 as purpose, anatomy, use when and when
not (and what instead), props, states, the phone, accessibility, an example call site, what enforces
it, and related families. Until then it holds the rules that were in `design-system.md`, below.

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
  below two points, which is not yet a shape, nor for a series that never moves on a scale of its
  own — scaled to its own maximum a flat line fills the band and reads as a full meter — and the
  tile's trend band then collapses rather than leave a gap. With a fixed `max` a flat line sits at
  its level and says so. Its colour is a series colour, never a status one: a failing share is
  `--chart-3`, because `--destructive` on a line says the line itself is an error (§3).
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
  readings that move. A flat trail or one under three readings draws nothing, for the reason
  `TileTrend` does not.
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
- `sparkline.tsx` — a bare SVG path for a table cell, not recharts: forty containers would otherwise mount
  forty responsive containers and resize observers. It also exports `TileTrend`, the one shape a
  reading's last hour takes in a `StatTile`'s `trend` slot on the deployment pages (and, on Metrics,
  the window on screen): the tile's full
  width, 36px, rising once. It draws nothing below two points, or for a series that never moves on a
  scale of its own, and the tile then leaves no band. Its colour is a series colour, never a status
  one, so a failing share is `--chart-3`.
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
