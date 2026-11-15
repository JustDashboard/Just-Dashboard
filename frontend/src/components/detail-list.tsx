import { cn } from "@/lib/utils"

/**
 * One figure with its name, for a strip of them under a chart or in a card
 * header. The name goes above the number, small and quiet: a column of these
 * scans as a table of values rather than a paragraph of labels.
 */
export function Metric({
  label,
  value,
  hint,
  className,
}: {
  label: React.ReactNode
  value: React.ReactNode
  hint?: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn("min-w-0", className)}>
      <p className="eyebrow truncate">{label}</p>
      <p className="numeric mt-0.5 truncate text-sm font-medium">{value}</p>
      {hint && <p className="truncate text-hint text-muted-foreground">{hint}</p>}
    </div>
  )
}

/**
 * A horizontal run of Metrics, separated by rules rather than by gap alone.
 *
 * Two columns on a phone rather than a wrapping row: wrapped, the first
 * metric of each new line kept the rule and the indent that belonged beside
 * its neighbour on the line above, and a date took whatever width was left of
 * the line and truncated in it. In a grid every rule is between two cells and
 * every value has half the width to itself.
 */
export function MetricStrip({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "grid grid-cols-2 gap-x-4 gap-y-3 [&>*]:min-w-0",
        "max-sm:[&>*:nth-child(2n)]:border-l max-sm:[&>*:nth-child(2n)]:border-hairline max-sm:[&>*:nth-child(2n)]:pl-4",
        "sm:flex sm:flex-wrap sm:gap-x-6 sm:[&>*+*]:border-l sm:[&>*+*]:border-hairline sm:[&>*+*]:pl-6",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A label/value pair in a stacked list — the replacement for the `<dl>` grids
 * that each card used to hand-roll with its own column widths.
 */
export function DetailList({ className, ...props }: React.ComponentProps<"dl">) {
  return (
    <dl
      className={cn("grid grid-cols-[minmax(0,auto)_minmax(0,1fr)] gap-x-4 gap-y-1.5", className)}
      {...props}
    />
  )
}

export function Detail({
  label,
  children,
  className,
}: {
  label: React.ReactNode
  children: React.ReactNode
  className?: string
}) {
  return (
    <>
      <dt className="text-xs text-muted-foreground">{label}</dt>
      <dd className={cn("min-w-0 text-xs", className)}>{children}</dd>
    </>
  )
}
