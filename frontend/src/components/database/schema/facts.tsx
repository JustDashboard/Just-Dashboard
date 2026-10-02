import { cn } from "@/lib/utils"

/**
 * A run of small figures that may be many: a table's rows, size and owner,
 * and then whatever facts its engine adds — four on one, nine on another.
 *
 * `MetricStrip` rules its figures apart, which reads well on one line and
 * badly on two: the figure that opens a wrapped line keeps a rule that
 * divides it from nothing. These are parted by space alone, so a run of any
 * length wraps into rows that each start at the edge.
 */
export function Facts({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn("flex flex-wrap gap-x-8 gap-y-3 [&>*]:max-w-full [&>*]:min-w-0", className)}
      {...props}
    />
  )
}
