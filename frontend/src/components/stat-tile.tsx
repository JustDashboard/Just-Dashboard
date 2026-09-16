import Link from "next/link"
import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"
import { ArrowRight } from "@/components/icons"
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
 */
export function StatTile({
  label,
  value,
  hint,
  meter,
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
  tone?: Tone
  /** The figure's unit or a delta, set beside it rather than under it. */
  trailing?: React.ReactNode
  className?: string
}) {
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
          label={typeof label === "string" ? label : undefined}
        />
      )}

      {hint && <p className="truncate text-hint text-muted-foreground">{hint}</p>}
    </div>
  )
}

/**
 * A stat tile that is also a destination.
 *
 * The arrow is the whole point: a tile with a hover wash and nothing else is a
 * surface that reacts without saying what pressing it does. It appears through
 * `rowReveal` rather than a hand-written `group-hover`, which is what makes it
 * permanently visible on a touch screen — where there is no hover to reveal it
 * with, and where a row of these is often the page's main navigation.
 *
 * This lived in the Docker overview and was retyped, without the arrow and
 * with a hover the tile could no longer show, on the proxy overview. Once,
 * here, beside the tile it wraps.
 */
export function StatLink({
  href,
  label,
  children,
}: {
  href: string
  label: string
  children: React.ReactNode
}) {
  return (
    <Link
      href={href}
      aria-label={label}
      // The wash lands on the tile rather than on this anchor: a `StatTile`
      // paints nothing of its own, so the hover class the caller passes it
      // (`group-hover:bg-row-hover`) is what answers the pointer. The hint is
      // the tile's last line and the arrow sits over its right end, so it
      // gives the arrow its width back rather than truncating under it.
      className="group relative block min-w-0 focus-ring-inset [&_[data-slot=stat-tile]>p:last-child]:pr-5"
    >
      {children}
      <ArrowRight
        aria-hidden
        className={cn("absolute right-4 bottom-4 size-3.5 text-muted-foreground", rowReveal())}
      />
    </Link>
  )
}

/**
 * A run of `StatTile`s as one object.
 *
 * No outer frame. Four bordered cards with a gap between them drew eight
 * vertical edges to separate four numbers; one framed grid with hairlines
 * drew five; this draws three — a hairline between neighbours and nothing
 * around the outside — so the figures read as a row of readings on the page
 * rather than as a box of them above it. The first column starts on the page's
 * own edge, in line with the title.
 *
 * `framed` restores the box, for the one place a run of figures sits inside
 * another surface and needs an edge of its own.
 *
 * `columns` is the count at the widest breakpoint; below it they stack two-up
 * and then one-up, which is the arrangement every call site had written out by
 * hand as `sm:grid-cols-2 xl:grid-cols-4`.
 */
export function StatGrid({
  columns = 4,
  framed,
  className,
  ...props
}: React.ComponentProps<"div"> & { columns?: 2 | 3 | 4 | 5; framed?: boolean }) {
  return (
    <div
      data-slot="stat-grid"
      className={cn(
        "grid min-w-0",
        framed && "overflow-hidden rounded-xl border bg-card",
        "[&>*]:min-w-0 [&>a]:block [&>a]:h-full",
        // A hairline between cells and only between them: a cell starting a
        // row draws no left edge and the first row draws no top one. Unframed,
        // the cell that starts a row also drops its left padding so the
        // column of names lines up with the page title above it.
        "[&>*]:border-t [&>*]:border-hairline [&>*:first-child]:border-t-0",
        !framed && "[&_[data-slot=stat-tile]]:pl-0",
        "sm:[&>*]:border-l sm:[&>*:nth-child(-n+2)]:border-t-0 sm:[&>*:nth-child(2n+1)]:border-l-0",
        !framed &&
          "sm:[&_[data-slot=stat-tile]]:pl-5 sm:[&>*:nth-child(2n+1)_[data-slot=stat-tile]]:pl-0",
        "grid-cols-1 sm:grid-cols-2",
        columns === 3 &&
          "lg:grid-cols-3 lg:[&>*]:border-l lg:[&>*:nth-child(-n+3)]:border-t-0 lg:[&>*:nth-child(2n+1)]:border-l lg:[&>*:nth-child(3n+1)]:border-l-0",
        columns === 3 &&
          !framed &&
          "lg:[&>*:nth-child(2n+1)_[data-slot=stat-tile]]:pl-5 lg:[&>*:nth-child(3n+1)_[data-slot=stat-tile]]:pl-0",
        columns === 4 &&
          "xl:grid-cols-4 xl:[&>*]:border-l xl:[&>*:nth-child(-n+4)]:border-t-0 xl:[&>*:nth-child(2n+1)]:border-l xl:[&>*:nth-child(4n+1)]:border-l-0",
        columns === 4 &&
          !framed &&
          "xl:[&>*:nth-child(2n+1)_[data-slot=stat-tile]]:pl-5 xl:[&>*:nth-child(4n+1)_[data-slot=stat-tile]]:pl-0",
        columns === 5 &&
          "xl:grid-cols-5 xl:[&>*]:border-l xl:[&>*:nth-child(-n+5)]:border-t-0 xl:[&>*:nth-child(2n+1)]:border-l xl:[&>*:nth-child(5n+1)]:border-l-0",
        columns === 5 &&
          !framed &&
          "xl:[&>*:nth-child(2n+1)_[data-slot=stat-tile]]:pl-5 xl:[&>*:nth-child(5n+1)_[data-slot=stat-tile]]:pl-0",
        className,
      )}
      {...props}
    />
  )
}
