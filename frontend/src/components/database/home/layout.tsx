import { cn } from "@/lib/utils"

/**
 * Two blocks side by side from the width where each still has room for a
 * ranked list, one above the other below it. Both are plain: the gap is what
 * separates them (§15). A run of more than two wraps into further rows of two.
 */
export function Pair({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn("grid items-start gap-8 lg:grid-cols-2 [&>*]:min-w-0", className)}
      {...props}
    />
  )
}
