import { cn } from "@/lib/utils"
import type { Tone } from "@/components/tone"
import { Meter } from "@/components/meter"

/**
 * A single headline figure.
 *
 * These used to be four-line cards: an eyebrow, a 24px figure on its own line,
 * a meter, and a hint — `p-4`, `gap-2.5`, about 120px tall each. Four of them
 * across the top of Overview spent a fifth of a laptop screen on four numbers,
 * and the page's actual content started below the fold.
 *
 * The name and the figure share a line now. That was previously argued against
 * — "the label would compete for the same line as the figure" — but the
 * competition was a function of the figure being 24px. At `text-base` against a
 * 10px eyebrow there is no contest, and putting them on one line is what lets a
 * row of these scan horizontally as label → value the way a table does, which
 * is what the original docstring said it wanted all along.
 *
 * The order is still fixed — name, number, meter, detail — because a row of
 * them is read as a table: the eye lands on one column of numbers, not on four
 * cards that each start somewhere different.
 */
export function StatTile({
  label,
  value,
  hint,
  icon: Icon,
  meter,
  tone = "default",
  trailing,
  className,
}: {
  label: React.ReactNode
  value: React.ReactNode
  /** One short line under the figure. Omit it — most of these did not earn it. */
  hint?: React.ReactNode
  icon?: React.ComponentType<{ className?: string }>
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
      className={cn(
        "flex min-w-0 flex-col justify-center gap-1.5 rounded-lg border bg-card px-3 py-2.5 text-card-foreground",
        className,
      )}
    >
      <div className="flex min-w-0 items-baseline justify-between gap-3">
        <p className="eyebrow flex min-w-0 items-center gap-1.5 truncate">
          {Icon && <Icon className="size-3 shrink-0 self-center" />}
          <span className="truncate">{label}</span>
        </p>
        <span className="flex shrink-0 items-baseline gap-1.5">
          <span
            className={cn(
              "numeric truncate text-base leading-none font-semibold",
              tone === "warning" && "text-warning",
              tone === "danger" && "text-destructive",
              tone === "success" && "text-success",
            )}
          >
            {value}
          </span>
          {trailing && <span className="text-hint text-muted-foreground">{trailing}</span>}
        </span>
      </div>

      {meter !== undefined && (
        <Meter
          value={meter}
          tone={tone}
          size="thin"
          label={typeof label === "string" ? label : undefined}
        />
      )}

      {hint && <p className="truncate text-hint text-muted-foreground">{hint}</p>}
    </div>
  )
}

/**
 * A run of `StatTile`s as one object rather than as a row of separate cards.
 *
 * Four bordered cards with a gap between them draw eight vertical edges to
 * separate four numbers. One frame with a hairline between each cell draws
 * three, reads as the table the tiles were always trying to be, and saves the
 * gap as well. The tiles inside drop their own border and radius.
 *
 * `columns` is the count at the widest breakpoint; below it they stack two-up
 * and then one-up, which is the arrangement every call site had written out by
 * hand as `sm:grid-cols-2 xl:grid-cols-4`.
 */
export function StatGrid({
  columns = 4,
  className,
  ...props
}: React.ComponentProps<"div"> & { columns?: 2 | 3 | 4 }) {
  return (
    <div
      data-slot="stat-grid"
      className={cn(
        "grid min-w-0 overflow-hidden rounded-xl border bg-card",
        // The tile drops its own frame wherever it sits — some call sites wrap
        // one in a <Link>, so this has to reach a grandchild, not just a child.
        "[&_[data-slot=stat-tile]]:rounded-none [&_[data-slot=stat-tile]]:border-0",
        "[&>*]:min-w-0 [&>a]:block [&>a]:h-full",
        // A hairline between cells and only between them: the grid's own border
        // is the outside edge, so a cell starting a row draws no left edge and
        // the first row draws no top one.
        "[&>*]:border-hairline [&>*]:border-t [&>*:first-child]:border-t-0",
        "sm:[&>*]:border-l sm:[&>*:nth-child(-n+2)]:border-t-0 sm:[&>*:nth-child(2n+1)]:border-l-0",
        "grid-cols-1 sm:grid-cols-2",
        columns === 3 &&
          "lg:grid-cols-3 lg:[&>*:nth-child(-n+3)]:border-t-0 lg:[&>*]:border-l lg:[&>*:nth-child(3n+1)]:border-l-0 lg:[&>*:nth-child(2n+1)]:border-l",
        columns === 4 &&
          "xl:grid-cols-4 xl:[&>*:nth-child(-n+4)]:border-t-0 xl:[&>*]:border-l xl:[&>*:nth-child(4n+1)]:border-l-0 xl:[&>*:nth-child(2n+1)]:border-l",
        className,
      )}
      {...props}
    />
  )
}
