import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"
import { Filter } from "@/components/icons"
import { rowReveal } from "@/components/icon-action"
import { Meter } from "@/components/meter"

/**
 * A single headline figure.
 *
 * The name sits above the number, small and quiet; the number is the largest
 * type on the page apart from the title. That order is fixed — name, number,
 * meter, detail — because a run of these is read as a table: the eye lands on
 * one row of numbers and reads the names only to tell them apart.
 *
 * It draws no frame of its own. A tile used to be a bordered card, four of
 * them across the top of every page, and a row of four boxes is the first
 * thing a reader sees on a page that then goes on to be a stack of more boxes.
 * The figures now sit on the page's own ground, and `StatGrid` draws the one
 * hairline between neighbours that keeps them from running together.
 *
 * A reading is said in words first (D4): a `ReadingLine`, a fact on the
 * identity line or a node of the picture. A tile stays only in a headline set
 * that is read across as a table (§15 pass 2), and each of its tiles carries
 * a sparkline or a meter and its tone:
 *
 * - Metrics' five readings (processor, memory, load, network, disks), which a
 *   reader compares in one sweep before choosing a chart;
 * - the Docker overview's band of figures over its table;
 * - a log lens's counts, as `StatButton`s that narrow the log.
 *
 * Every other row of tiles is a page waiting for its pass, and a project's
 * Overview and the host's carry none. A tile that is a destination is a
 * `Link` around a `StatTile`; `StatLink`, which drew that, went in 0.9.0.
 */
export function StatTile({
  label,
  value,
  hint,
  meter,
  meterLabel,
  trend,
  tone = "default",
  trailing,
  className,
}: {
  label: React.ReactNode
  value: React.ReactNode
  /** One short line under the figure. Omit it — most of these did not earn it. */
  hint?: React.ReactNode
  /** 0–100. Draws the utilisation bar under the figure. */
  meter?: number
  /**
   * Names the bar when `label` is a node (a mark beside the word) rather than
   * a string, or when the bar measures something the label does not say — the
   * Disk I/O tile's figure is a rate and its bar how busy the disks are.
   * A bar with no name is hidden from a screen reader as decoration.
   */
  meterLabel?: string
  /**
   * The figure's recent shape, drawn where the meter would be — for a reading
   * that moves rather than one that fills. The Overview's tiles carry their
   * last hour here; it was a panel of four sparklines under them, each
   * repeating the figure of the tile above it.
   */
  trend?: React.ReactNode
  tone?: Tone
  /** The figure's unit or a delta, set beside it rather than under it. */
  trailing?: React.ReactNode
  className?: string
}) {
  const meterName = meterLabel ?? (typeof label === "string" ? label : undefined)
  if (meter !== undefined && !meterName && process.env.NODE_ENV !== "production") {
    console.error("StatTile draws a meter with no name: give it meterLabel.")
  }
  return (
    <div
      data-slot="stat-tile"
      // Top-aligned, not centred: a run of these is read across as a table, and
      // a tile with no meter or hint centred itself a line lower than its
      // neighbours, so the row of names stopped being a row.
      className={cn("flex min-w-0 flex-col justify-start gap-1.5 px-5 py-4", className)}
    >
      <p className="eyebrow truncate">{label}</p>
      <div className="flex min-w-0 flex-wrap items-baseline gap-x-2">
        {/*
          `leading-tight`, not `leading-none`: beside `truncate` a line box that
          ends at the baseline clips its own descenders, and "Everything is up"
          would lose the tails of its y, g and p.
        */}
        <span
          className={cn(
            "numeric truncate text-2xl leading-tight font-semibold tracking-tight",
            tone === "warning" && "text-warning",
            tone === "danger" && "text-destructive",
            tone === "success" && "text-success",
          )}
        >
          {value}
        </span>
        {trailing && <span className="text-hint text-muted-foreground">{trailing}</span>}
      </div>

      {meter !== undefined && (
        <Meter
          value={meter}
          tone={tone}
          size="thin"
          className="mt-1"
          {...(meterName ? { label: meterName } : { decorative: true })}
        />
      )}

      {/* `empty:hidden` so a `TileTrend` with too few points to draw leaves no
          band behind it: a caller passes the trend whenever the tile has a
          series, and the line appears when its second point does. */}
      {trend && <div className="-mb-0.5 h-9 min-w-0 empty:hidden">{trend}</div>}

      {hint && <p className="truncate text-hint text-muted-foreground">{hint}</p>}
    </div>
  )
}

/**
 * A tile's picture, on the one line every tile in a row keeps for it. A tile
 * with no picture beside tiles with one ended its hint half a band higher, so
 * the hints stopped being a row; the line stays when there is nothing to draw,
 * for the same reason. `center` sets a strip or a meter on the band's middle
 * rather than at its foot, where a trend's line sits.
 */
export function TileBand({ center, children }: { center?: boolean; children?: React.ReactNode }) {
  return (
    <div className={cn("h-full min-w-0", center && "flex items-center [&>*]:w-full")}>
      {children}
    </div>
  )
}

/**
 * A stat tile that narrows what is under it.
 *
 * A tile for a figure that is a question rather than a place: a
 * log's "Auth failures 12" is the filter those twelve lines answer, and a
 * press applies it to the pane below instead of leaving the page. The mark
 * that appears is the funnel every "only lines like this" carries, for the
 * same reason a link's is an arrow — a tile that washes on hover and says
 * nothing of what a press does is a surface that only reacts. `pressed` is
 * the figure's filter being the one on screen.
 */
export function StatButton({
  label,
  pressed,
  onClick,
  children,
}: {
  label: string
  pressed?: boolean
  onClick: () => void
  children: React.ReactNode
}) {
  return (
    <button
      type="button"
      onClick={onClick}
      aria-label={label}
      aria-pressed={pressed}
      // A column, not a block: a button centres its content vertically, so a
      // tile with no trend beside one with a trend sat half a line lower.
      className="group relative flex h-full w-full min-w-0 flex-col text-left focus-ring-inset [&_[data-slot=stat-tile]>p:last-child]:pr-5"
    >
      {children}
      <Filter
        aria-hidden
        className={cn(
          "absolute right-4 bottom-4 size-3.5 text-muted-foreground",
          pressed ? "text-foreground" : rowReveal(),
        )}
      />
    </button>
  )
}

/**
 * A run of `StatTile`s as one object.
 *
 * No outer frame. Four bordered cards with a gap between them drew eight
 * vertical edges to separate four numbers; one framed grid with hairlines
 * drew five; this draws three — a hairline between neighbours and nothing
 * around the outside — so the figures read as a row of readings on the page
 * rather than as a box of them above it.
 *
 * Every tile keeps the same inset, the first in a row included. The first
 * column used to give up its left padding to line its name up with the title,
 * and on a tile that washes on hover that put the wash flush against the name: one tile
 * of the row drawn tighter than the others beside it.
 *
 * `framed` restores the box, for the one place a run of figures sits inside
 * another surface and needs an edge of its own.
 *
 * `columns` is the count at the widest breakpoint; below it they stack two-up
 * and then one-up, which is the arrangement every call site had written out by
 * hand as `sm:grid-cols-2 xl:grid-cols-4`. A tile left alone on the last row
 * takes the whole row rather than leave a hole beside it — five two-up is two,
 * two and one, and the one used to sit under half a hairline.
 *
 * `dense` keeps them two-up on a phone as well, for a run of short figures
 * that should not stack into four hundred pixels before the first thing the
 * page is about: the Logs and Deployments readings, the settings pages'. The
 * tiles give up a step of padding below `sm` to fit. From `sm` up it is the
 * default grid exactly. It is opt-in because a figure like "Everything is up"
 * needs the whole width of a phone, and only the call site knows which kind it
 * has.
 */
export function StatGrid({
  columns = 4,
  framed,
  dense,
  className,
  ...props
}: React.ComponentProps<"div"> & {
  columns?: 2 | 3 | 4 | 5
  framed?: boolean
  dense?: boolean
}) {
  return (
    <div
      data-slot="stat-grid"
      className={cn(
        "grid min-w-0",
        framed && "overflow-hidden rounded-xl border bg-card",
        "[&>*]:min-w-0 [&>a]:block [&>a]:h-full",
        // A hairline between cells and only between them: a cell starting a
        // row draws no left edge and the first row draws no top one.
        "[&>*]:border-t [&>*]:border-hairline [&>*:first-child]:border-t-0",
        // Dense writes the two-up rules at the base width, where the default
        // writes them from `sm` over a one-up base.
        dense
          ? [
              "grid-cols-2",
              "[&>*]:border-l [&>*:nth-child(-n+2)]:border-t-0 [&>*:nth-child(2n+1)]:border-l-0",
              "max-sm:[&_[data-slot=stat-tile]]:px-4 max-sm:[&_[data-slot=stat-tile]]:py-3",
            ]
          : [
              "grid-cols-1 sm:grid-cols-2",
              "sm:[&>*]:border-l sm:[&>*:nth-child(-n+2)]:border-t-0 sm:[&>*:nth-child(2n+1)]:border-l-0",
            ],
        // The lone last tile while the grid is two-up, bounded by the width
        // where it goes to its full count: past that, an odd tile is no longer
        // alone on its row, and spanning two would push it onto a row of its own.
        columns === 2 && dense && "[&>*:last-child:nth-child(odd)]:col-span-2",
        columns === 2 && !dense && "sm:[&>*:last-child:nth-child(odd)]:col-span-2",
        columns === 3 && dense && "max-lg:[&>*:last-child:nth-child(odd)]:col-span-2",
        columns === 3 && !dense && "sm:max-lg:[&>*:last-child:nth-child(odd)]:col-span-2",
        columns >= 4 && dense && "max-xl:[&>*:last-child:nth-child(odd)]:col-span-2",
        columns >= 4 && !dense && "sm:max-xl:[&>*:last-child:nth-child(odd)]:col-span-2",
        columns === 3 &&
          "lg:grid-cols-3 lg:[&>*]:border-l lg:[&>*:last-child:nth-child(3n+1)]:col-span-3 lg:[&>*:nth-child(-n+3)]:border-t-0 lg:[&>*:nth-child(2n+1)]:border-l lg:[&>*:nth-child(3n+1)]:border-l-0",
        columns === 4 &&
          "xl:grid-cols-4 xl:[&>*]:border-l xl:[&>*:last-child:nth-child(4n+1)]:col-span-4 xl:[&>*:nth-child(-n+4)]:border-t-0 xl:[&>*:nth-child(2n+1)]:border-l xl:[&>*:nth-child(4n+1)]:border-l-0",
        columns === 5 &&
          "xl:grid-cols-5 xl:[&>*]:border-l xl:[&>*:last-child:nth-child(5n+1)]:col-span-5 xl:[&>*:nth-child(-n+5)]:border-t-0 xl:[&>*:nth-child(2n+1)]:border-l xl:[&>*:nth-child(5n+1)]:border-l-0",
        className,
      )}
      {...props}
    />
  )
}
