import { cn } from "@/lib/utils"

/** A row of filters and actions that stands on its own, outside a panel. */
export function Toolbar({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex min-w-0 flex-wrap items-center gap-2", className)} {...props} />
}
