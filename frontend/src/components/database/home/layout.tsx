import { cn } from "@/lib/utils"

/**
 * Two blocks side by side from the width where each still has room for a
 * ranked list, one above the other below it. Both are plain: the gap is what
 * separates them (§15).
 *
 * A home pairs its blocks by how long they run, not only by what they are
 * about: the two short lists share a row (what needs attention, what uses the
 * database) and the two ranked ones share the next (what it spends its time
 * on, what it holds). Paired the other way — a finding beside six bars, one
 * consumer beside six more — each row left a hole the height of five bars.
 */
export function Pair({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn("grid items-start gap-8 lg:grid-cols-2 [&>*]:min-w-0", className)}
      {...props}
    />
  )
}

/**
 * The reference blocks at a home's foot: short lists of facts, as many across
 * as fit at the width a label and its value need. An engine without one of
 * them — a file has no port to be reachable on — leaves no empty column.
 */
export function Columns({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "grid items-start gap-8 sm:grid-cols-[repeat(auto-fit,minmax(17rem,1fr))] [&>*]:min-w-0",
        className,
      )}
      {...props}
    />
  )
}
