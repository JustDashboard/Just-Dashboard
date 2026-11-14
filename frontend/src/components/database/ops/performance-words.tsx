import { cn } from "@/lib/utils"
import { percent } from "@/components/rank-list"
import { TableCell, TableHead } from "@/components/ui/table"

/**
 * The words Performance says its figures in, where it used to draw them: a
 * small fact on a view's title line in place of a paragraph under its table,
 * a line of figures read as a sentence in place of a strip of tiles, and a
 * share as a figure in a column of its own in place of a bar under a row.
 */

/**
 * One short fact on a view's title line, where its footnote paragraph was:
 * "1.0 s of statement time counted". It is the first thing in the line's
 * controls, so the filter and the verb after it keep their place.
 */
export function TitleFact({ className, ...props }: React.ComponentProps<"span">) {
  return (
    <span
      data-slot="title-fact"
      className={cn("min-w-0 truncate text-hint text-muted-foreground", className)}
      {...props}
    />
  )
}

/** A figure inside a `TitleFact` or a `FigureLine`: the part that is read first. */
export function Fig({
  tone,
  className,
  ...props
}: React.ComponentProps<"span"> & { tone?: "warning" | "danger" }) {
  return (
    <span
      className={cn(
        "numeric font-medium",
        tone === "warning"
          ? "text-warning"
          : tone === "danger"
            ? "text-destructive"
            : "text-foreground",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A thing's figures as one line that reads as a sentence — "60% of runtime ·
 * 100 calls · 6.0 ms a call" — where a `MetricStrip` set each in a tile of
 * its own with a label over it. It wraps; a figure and its words never part.
 */
export function FigureLine({ className, children, ...props }: React.ComponentProps<"p">) {
  return (
    <p
      data-slot="figure-line"
      className={cn(
        "flex min-w-0 flex-wrap items-baseline gap-x-5 gap-y-1 text-body text-muted-foreground [&>span]:whitespace-nowrap",
        className,
      )}
      {...props}
    >
      {children}
    </p>
  )
}

/** The head of the share column: the same narrow, right-aligned column on every list that has one. */
export function ShareHead({ label = "Share", className }: { label?: string; className?: string }) {
  return <TableHead className={cn("w-14 px-2 text-right", className)}>{label}</TableHead>
}

/**
 * A row's share of the whole as a figure in its own column, the way
 * `RankList` ends a row: what the bar under a row's name used to say by its
 * length, on the line the name is on, lined up down the list.
 */
export function ShareCell({
  part,
  whole,
  className,
}: {
  part: number
  whole: number
  className?: string
}) {
  return (
    <TableCell
      data-slot="share"
      className={cn("numeric w-14 px-2 py-2 text-right text-muted-foreground", className)}
    >
      {whole > 0 && part >= 0 ? percent(part / whole) : ""}
    </TableCell>
  )
}
