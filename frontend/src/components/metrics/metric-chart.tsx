"use client"

import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react"
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  ReferenceArea,
  ReferenceLine,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts"
import { cn } from "@/lib/utils"
import { percent, timestamp } from "@/lib/format"
import {
  clearCrosshair,
  getCrosshair,
  getServerCrosshair,
  setCrosshair,
  subscribeCrosshair,
} from "@/lib/metrics-crosshair"
import type { MouseHandlerDataParam } from "recharts"
import type { MetricEvent } from "@/lib/types"
import { ChartContainer, type ChartConfig } from "@/components/ui/chart"

/**
 * One measurement drawn on a chart.
 *
 * `peakKey` is the companion series holding the bucket's maximum. It is
 * declared here rather than drawn by the caller so that every chart in the
 * dashboard renders a peak the same way — behind its mean, in its mean's
 * colour, because it is the same measurement at a finer resolution and not a
 * second quantity.
 */
export type Series = {
  key: string
  label: string
  color: string
  /** Areas read as volume, lines as level. Default is a line. */
  kind?: "area" | "line"
  peakKey?: string
  /** Stack id. Series sharing one are drawn as a filled 100% breakdown. */
  stack?: string
  /** Overrides the chart-wide formatter for this series' numbers. */
  format?: (value: number) => string
}

export type ChartRowLike = { ts: number } & Record<string, unknown>

/**
 * How one series' numbers are printed, wherever they are printed.
 *
 * The y-axis gutter, the tooltip and the legend each need this, and each had
 * written its own `unit === "%" ? `${v}${unit}` : format(v)`. That fallback
 * concatenates the raw double: every percentage chart on /metrics that did not
 * pass an explicit `format` reported its minimum as "9.061558297024309%" and
 * its maximum as "91.25299841560013%", in a column headed by a mean that *was*
 * rounded — so the one number the reader could compare was the one that
 * disagreed with its neighbours.
 *
 * A percentage gets one decimal. Not zero: pressure and steal live between 0
 * and 5, where rounding to whole numbers reports every interesting value as
 * "0%". Not variable-by-magnitude either — a column whose precision changes
 * per row cannot be read down.
 */
export function seriesFormat(
  own: ((value: number) => string) | undefined,
  format: ((value: number) => string) | undefined,
  unit: string | undefined,
): (value: number) => string {
  if (own) return own
  if (format) return format
  if (unit === "%") return (v) => percent(v, 1)
  return (v) => (Number.isInteger(v) ? String(v) : v.toFixed(2))
}

/**
 * The chart every metric on this dashboard is drawn with.
 *
 * It exists because the alternative — each panel assembling its own recharts
 * tree — is how the four charts on the old Overview page ended up with three
 * different tooltip formats and no way to compare a moment across them. The
 * behaviours worth having are all cross-cutting:
 *
 *  - a **shared crosshair**, so hovering one chart marks the same instant on
 *    every other chart on the page;
 *  - **drag to zoom**, because a range picker with five fixed windows cannot
 *    answer "what happened between 03:10 and 03:25";
 *  - **event markers**, so a step in a line can be read next to the deploy
 *    that caused it;
 *  - a **time-scaled x-axis**, so a gap in the record is a gap of the right
 *    width rather than two points drawn side by side.
 *
 * The axis is numeric-over-time rather than a category axis of pre-formatted
 * labels. A category axis spaces every bucket equally, which quietly lies
 * whenever the record has a hole in it — four hours of downtime and four
 * seconds of it look identical.
 */
export function MetricChart({
  rows,
  series,
  config,
  height = 190,
  domain,
  unit,
  format,
  axisFormat,
  events,
  onZoom,
  showPeaks = true,
  stacked = false,
  className,
  thresholds,
}: {
  rows: ChartRowLike[]
  series: Series[]
  config: ChartConfig
  height?: number
  /** Y domain. Omit to let recharts fit the data. */
  domain?: [number | string, number | string]
  unit?: string
  format?: (value: number) => string
  /**
   * Tick labels, when the full format is too wide for an axis.
   *
   * "585.9 KB/s" wraps onto two lines in the gutter recharts reserves and
   * then clips against the top of the plot. The axis only has to establish
   * the scale — the legend and the tooltip are where an exact figure with its
   * units belongs — so it gets a shorter form of the same number.
   */
  axisFormat?: (value: number) => string
  events?: MetricEvent[]
  /** Called with the dragged window. Absent disables zooming on this chart. */
  onZoom?: (from: number, to: number) => void
  showPeaks?: boolean
  /** True when the series stack into one 100% breakdown. */
  stacked?: boolean
  className?: string
  /** Horizontal lines marking where a value stops being acceptable. */
  thresholds?: { value: number; label: string; tone?: "warning" | "danger" }[]
}) {
  // Deliberately *not* subscribed to the crosshair.
  //
  // It used to be, and that is what made a page of ten charts feel heavy: a
  // pointer crossing one chart changed the shared instant every few pixels,
  // and every chart on the page re-rendered its whole recharts tree to move a
  // one-pixel line. On the 1h window that is ~38 series of 240 points being
  // reconciled per pointer move. The line is now drawn by SyncedCrosshair, a
  // sibling overlay that is the only thing which re-renders.
  const chartId = useId()
  // React's ids carry colons, which are legal in an id attribute but a
  // nuisance the moment anything tries to read one back as a selector.
  const fillId = chartId.replace(/[^a-zA-Z0-9_-]/g, "")
  const wrapper = useRef<HTMLDivElement>(null)
  const plot = usePlotArea(wrapper, rows.length)
  const [drag, setDrag] = useState<{ from: number; to: number } | null>(null)

  const bounds = useMemo(() => {
    if (rows.length === 0) return null
    return { from: rows[0].ts, to: rows[rows.length - 1].ts }
  }, [rows])

  // Events outside the drawn window would be clamped to an edge by recharts
  // and read as something that happened at the start of the chart.
  const marks = useMemo(() => {
    if (!events || !bounds) return []
    return events
      .map((e) => ({ event: e, ts: new Date(e.ts).getTime() }))
      .filter((m) => !Number.isNaN(m.ts) && m.ts >= bounds.from && m.ts <= bounds.to)
  }, [events, bounds])

  // activeLabel is the x-axis value under the pointer. Because the axis is
  // the timestamp itself rather than a pre-formatted label, that value is
  // already the instant every other chart on the page needs — no lookup back
  // into the row array, and no dependence on the charts sharing one.
  const handleMove = useCallback(
    (state: MouseHandlerDataParam) => {
      const ts = instantOf(state)
      if (ts === null) return
      setCrosshair(ts, chartId)
      // Returning the same null bails out of the state update, so a pointer
      // that is not dragging does not re-render this chart at all.
      setDrag((d) => (d ? { ...d, to: ts } : null))
    },
    [chartId],
  )

  const handleDown = useCallback(
    (state: MouseHandlerDataParam) => {
      if (!onZoom) return
      const ts = instantOf(state)
      if (ts !== null) setDrag({ from: ts, to: ts })
    },
    [onZoom],
  )

  const handleUp = useCallback(() => {
    if (!drag || !onZoom) {
      setDrag(null)
      return
    }
    const [from, to] = drag.from <= drag.to ? [drag.from, drag.to] : [drag.to, drag.from]
    setDrag(null)
    // A click is a drag of zero width. Zooming to an instant would produce an
    // empty chart with no way back, so a selection has to be worth making.
    if (to - from < 1000) return
    onZoom(from, to)
  }, [drag, onZoom])

  const handleLeave = useCallback(() => {
    clearCrosshair()
    setDrag(null)
  }, [])

  const ticks = useMemo(() => tickFormatterFor(rows), [rows])

  return (
    // A floor, not a fixed height. Panels laid out side by side stretch to the
    // tallest in the row, and with `height` fixed the surplus fell between the
    // plot and its legend as a band of nothing — widest exactly where one panel
    // carries a footer strip and its neighbour does not. The surplus goes to
    // the plot instead, so the space a row is already occupying is spent on the
    // data rather than on a gap.
    <div ref={wrapper} className="relative w-full flex-1" style={{ minHeight: height }}>
      <SyncedCrosshair chartId={chartId} bounds={bounds} plot={plot} />
      {/* Positioned rather than `h-full`, and that is what makes the floor
          above work. A percentage height resolves against a *definite* parent;
          a flex item sized by `min-height` alone is not definite, so `h-full`
          collapsed to zero and recharts drew nothing — visible on exactly the
          panels that stand alone in their row rather than stretching to a
          neighbour. Absolute positioning takes its height from the box it is
          in, whatever produced that box. */}
      <ChartContainer config={config} className={cn("absolute inset-0 aspect-auto", className)}>
        <ComposedChart
          data={rows}
          margin={{ left: 4, right: 8, top: 6, bottom: 0 }}
          onMouseMove={handleMove}
          onMouseDown={handleDown}
          onMouseUp={handleUp}
          onMouseLeave={handleLeave}
        >
          <CartesianGrid vertical={false} strokeDasharray="3 3" opacity={0.35} />
          <XAxis
            dataKey="ts"
            type="number"
            scale="time"
            domain={["dataMin", "dataMax"]}
            tickFormatter={ticks}
            tickLine={false}
            axisLine={false}
            minTickGap={44}
            fontSize={10}
          />
          <YAxis
            domain={domain}
            width={unit === "%" ? 34 : 56}
            tickLine={false}
            axisLine={false}
            fontSize={10}
            // The gutter is the one place a whole number is right: recharts
            // picks the ticks, and "25.0%" beside "50.0%" spends four
            // characters saying nothing the scale did not already say.
            tickFormatter={(v: number) =>
              unit === "%"
                ? `${Math.round(v)}${unit}`
                : (axisFormat ?? seriesFormat(undefined, format, unit))(v)
            }
          />

          <Tooltip
            cursor={{ stroke: "var(--foreground)", strokeOpacity: 0.28, strokeWidth: 1 }}
            isAnimationActive={false}
            // Recharts positions the default tooltip under the pointer, where on
            // a dense page it covers the neighbouring chart the crosshair exists
            // to let you read. Pinning it to the top of the plot keeps both
            // visible.
            position={{ y: 0 }}
            content={<MetricTooltip series={series} format={format} unit={unit} events={marks} />}
          />

          {/* Drawn before the series so a marker sits behind the data it
              explains rather than obscuring the moment being explained. */}
          {marks.map((mark, i) => (
            <ReferenceLine
              key={`${mark.ts}-${i}`}
              x={mark.ts}
              stroke={eventColor(mark.event)}
              strokeDasharray="2 3"
              strokeOpacity={0.75}
              strokeWidth={1}
            />
          ))}

          {thresholds?.map((t) => (
            <ReferenceLine
              key={t.label}
              y={t.value}
              stroke={t.tone === "danger" ? "var(--destructive)" : "var(--warning)"}
              strokeDasharray="4 4"
              strokeOpacity={0.5}
              strokeWidth={1}
              label={{
                value: t.label,
                position: "insideTopRight",
                fontSize: 9,
                fill: "var(--muted-foreground)",
              }}
            />
          ))}

          {/* A fill that fades to nothing at the baseline rather than a flat
              wash of the line's colour.
              At a constant 14% an area chart is a slab: the ink is heaviest
              along the axis, furthest from the line the reader is actually
              following, and two overlapping areas mix into a third colour that
              belongs to neither series. A vertical ramp puts the density under
              the line and lets the one behind it show through. Stacked series
              keep a solid fill — there the block *is* the quantity. */}
          <defs>
            {series.map((s) => (
              <linearGradient key={s.key} id={`${fillId}-${s.key}`} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color} stopOpacity={0.32} />
                <stop offset="92%" stopColor={s.color} stopOpacity={0.02} />
              </linearGradient>
            ))}
          </defs>

          {/* Peaks first, so the mean is drawn on top of the envelope it lives
              inside rather than under it. */}
          {showPeaks &&
            series.map((s) =>
              s.peakKey ? (
                <Area
                  key={s.peakKey}
                  dataKey={s.peakKey}
                  type="monotone"
                  stroke={s.color}
                  strokeWidth={1}
                  strokeOpacity={0.4}
                  fill={`url(#${fillId}-${s.key})`}
                  // The envelope is context for the mean, not a second series.
                  // It shares the gradient so the two read as one shape, at a
                  // fraction of the density so the line stays the subject.
                  fillOpacity={0.4}
                  dot={false}
                  isAnimationActive={false}
                  connectNulls={false}
                />
              ) : null,
            )}

          {series.map((s) =>
            s.kind === "area" || stacked ? (
              <Area
                key={s.key}
                dataKey={s.key}
                type="monotone"
                stackId={s.stack}
                stroke={s.color}
                strokeWidth={stacked ? 0 : 1.5}
                fill={stacked ? s.color : `url(#${fillId}-${s.key})`}
                fillOpacity={stacked ? 0.78 : 1}
                dot={false}
                isAnimationActive={false}
                connectNulls={false}
              />
            ) : (
              <Line
                key={s.key}
                dataKey={s.key}
                type="monotone"
                stroke={s.color}
                strokeWidth={1.5}
                dot={false}
                isAnimationActive={false}
                connectNulls={false}
              />
            ),
          )}

          {drag && drag.from !== drag.to && (
            <ReferenceArea
              x1={Math.min(drag.from, drag.to)}
              x2={Math.max(drag.from, drag.to)}
              fill="var(--primary)"
              fillOpacity={0.12}
              stroke="var(--primary)"
              strokeOpacity={0.4}
            />
          )}
        </ComposedChart>
      </ChartContainer>
    </div>
  )
}

/**
 * The line marking the instant another chart is being hovered at.
 *
 * A plain positioned element rather than a recharts `ReferenceLine`, which is
 * the whole point: a ReferenceLine is a child of the chart, so moving it means
 * re-rendering the chart — every series, every point — and ten charts doing
 * that on every pointer move is what made the wider windows feel heavy. This
 * subscribes on its own, so a pointer sweeping the page re-renders ten
 * one-`div` components instead of ten charts.
 *
 * The position is arithmetic rather than a lookup: the x-axis is a linear time
 * scale over exactly `bounds`, so an instant's offset is its fraction of the
 * window across the measured plot area.
 */
function SyncedCrosshair({
  chartId,
  bounds,
  plot,
}: {
  chartId: string
  bounds: { from: number; to: number } | null
  plot: PlotArea | null
}) {
  const { ts, source } = useSyncExternalStore(subscribeCrosshair, getCrosshair, getServerCrosshair)

  if (ts === null || !bounds || !plot) return null
  // The chart the pointer is actually in draws recharts' own cursor; a second
  // line a pixel away from it reads as a rendering fault.
  if (source === chartId) return null
  if (ts < bounds.from || ts > bounds.to) return null

  const span = bounds.to - bounds.from
  const fraction = span > 0 ? (ts - bounds.from) / span : 0

  return (
    <div
      aria-hidden
      className="pointer-events-none absolute z-10 w-px bg-foreground/25"
      style={{
        left: plot.left + fraction * plot.width,
        top: plot.top,
        height: plot.height,
      }}
    />
  )
}

type PlotArea = { left: number; top: number; width: number; height: number }

/**
 * Where the plotting area sits inside the chart's box.
 *
 * Measured from the rendered grid rather than derived from the margins and the
 * axis width, because those are recharts' arithmetic to do and it changes them
 * between versions. Re-measured on resize and whenever the data changes shape,
 * which is when an axis can grow a digit and shift the plot sideways.
 */
function usePlotArea(
  ref: React.RefObject<HTMLDivElement | null>,
  revision: number,
): PlotArea | null {
  const [plot, setPlot] = useState<PlotArea | null>(null)

  useEffect(() => {
    const host = ref.current
    if (!host) return

    const measure = () => {
      const grid = host.querySelector(".recharts-cartesian-grid")
      if (!grid) return
      const g = grid.getBoundingClientRect()
      const h = host.getBoundingClientRect()
      if (g.width === 0 || g.height === 0) return
      const next = { left: g.left - h.left, top: g.top - h.top, width: g.width, height: g.height }
      setPlot((prev) =>
        prev &&
        prev.left === next.left &&
        prev.top === next.top &&
        prev.width === next.width &&
        prev.height === next.height
          ? prev
          : next,
      )
    }

    // After paint: the grid does not exist until recharts has rendered, and on
    // first mount that is the frame after this effect.
    const raf = requestAnimationFrame(measure)
    const observer = new ResizeObserver(measure)
    observer.observe(host)
    return () => {
      cancelAnimationFrame(raf)
      observer.disconnect()
    }
  }, [ref, revision])

  return plot
}

function instantOf(state: MouseHandlerDataParam): number | null {
  const label = state?.activeLabel
  return typeof label === "number" && Number.isFinite(label) ? label : null
}

type Mark = { event: MetricEvent; ts: number }

/**
 * The readout for one instant.
 *
 * Every series at that moment in one block, rather than recharts' default of
 * whichever series the pointer happens to be nearest. Comparing "memory was
 * high while the disk was idle" is the entire reason these charts sit on one
 * page, and it cannot be done from a tooltip that shows one line at a time.
 *
 * Peaks are folded into their mean's row as a second figure instead of taking
 * a row of their own: they are the same measurement, and listing them
 * separately doubles the height of the box for no extra fact.
 */
function MetricTooltip({
  active,
  payload,
  label,
  series,
  format,
  unit,
  events,
}: {
  active?: boolean
  payload?: { dataKey?: string | number; value?: number | string }[]
  label?: number | string
  series: Series[]
  format?: (value: number) => string
  unit?: string
  events: Mark[]
}) {
  if (!active || !payload?.length || typeof label !== "number") return null

  const at = new Map<string, number>()
  for (const entry of payload) {
    const key = String(entry.dataKey ?? "")
    if (key && typeof entry.value === "number") at.set(key, entry.value)
  }
  // Every point is on the same bucket, so a marker within half a bucket of it
  // is the event that bucket contains.
  const near = events.filter((m) => Math.abs(m.ts - label) < 1000)

  return (
    <div className="min-w-[11rem] rounded-lg border border-hairline bg-popover/95 px-2.5 py-2 text-popover-foreground shadow-md backdrop-blur-sm">
      <p className="numeric mb-1.5 text-hint text-muted-foreground">{rowInstant(label)}</p>
      <div className="space-y-1">
        {series.map((s) => {
          const value = at.get(s.key)
          if (value === undefined) return null
          const peak = s.peakKey ? at.get(s.peakKey) : undefined
          const render = seriesFormat(s.format, format, unit)
          return (
            <div key={s.key} className="flex items-center gap-2 text-hint">
              <span
                aria-hidden
                className="size-2 shrink-0 rounded-[2px]"
                style={{ background: s.color }}
              />
              <span className="flex-1 truncate text-muted-foreground">{s.label}</span>
              <span className="numeric font-medium">{render(value)}</span>
              {peak !== undefined && peak > value && (
                <span className="numeric w-14 shrink-0 text-right text-muted-foreground">
                  ↑{render(peak)}
                </span>
              )}
            </div>
          )
        })}
      </div>
      {near.length > 0 && (
        <div className="mt-1.5 space-y-0.5 border-t border-hairline pt-1.5">
          {near.map((m, i) => (
            <p key={i} className="flex items-center gap-1.5 text-hint">
              <span
                aria-hidden
                className="size-1.5 shrink-0 rounded-full"
                style={{ background: eventColor(m.event) }}
              />
              <span className="truncate">{m.event.title}</span>
            </p>
          ))}
        </div>
      )}
    </div>
  )
}

/**
 * How the x-axis labels itself, chosen from the span on screen.
 *
 * A bare clock is right for an hour and ambiguous for a week; a date is right
 * for a week and noise for a minute. Deciding per chart from the data it holds
 * is what lets the same component serve a two-second live feed and a
 * seven-day history without either being illegible.
 */
function tickFormatterFor(rows: ChartRowLike[]): (ts: number) => string {
  if (rows.length < 2) return (ts) => new Date(ts).toLocaleTimeString(undefined, { hour12: false })
  const span = rows[rows.length - 1].ts - rows[0].ts
  const day = 86_400_000
  if (span > 3 * day) {
    return (ts) => new Date(ts).toLocaleDateString(undefined, { month: "short", day: "numeric" })
  }
  if (span > day) {
    return (ts) =>
      new Date(ts).toLocaleString(undefined, {
        weekday: "short",
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      })
  }
  if (span > 2 * 3_600_000) {
    return (ts) =>
      new Date(ts).toLocaleTimeString(undefined, {
        hour: "2-digit",
        minute: "2-digit",
        hour12: false,
      })
  }
  return (ts) => new Date(ts).toLocaleTimeString(undefined, { hour12: false })
}

export function eventColor(event: MetricEvent): string {
  if (event.severity === "error") return "var(--destructive)"
  if (event.severity === "warning" || event.kind === "reboot") return "var(--warning)"
  if (event.kind === "deploy") return "var(--chart-3)"
  return "var(--muted-foreground)"
}

/** The full timestamp, which is what a tooltip heading has to say. */
export function rowInstant(ts: number): string {
  return timestamp(new Date(ts).toISOString())
}
