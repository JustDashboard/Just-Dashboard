import { cn } from "@/lib/utils"
import { Input } from "@/components/ui/input"
import { MagnifyingGlass } from "@/components/icons"

/**
 * The filter box, which appeared in six pages as the same three elements
 * assembled slightly differently each time — a different width, a different
 * icon offset, sometimes no icon at all.
 *
 * On a phone it is a field like any other: 40px, beside the 40px `Select
 * size="sm"` it usually shares a toolbar with, and at `Input`'s own 16px so
 * iOS does not zoom the page when it takes focus. It also takes the first
 * line of a wrapping toolbar to itself — a call site's `flex-1` had squeezed
 * the projects search to 95px beside its chips at 390. From `sm` it is the
 * 32px box it always was. The dense one is a pane's chrome and keeps its size.
 */
export function SearchInput({
  dense,
  trailing,
  className,
  containerClassName,
  ...props
}: React.ComponentProps<typeof Input> & {
  /** The filter box inside a pane's own chrome, where 32px is too tall. */
  dense?: boolean
  /** Controls pinned inside the box — a clear button, a regex toggle. */
  trailing?: React.ReactNode
  containerClassName?: string
}) {
  return (
    <div
      className={cn(
        "relative flex w-full min-w-0 items-center sm:w-72",
        !dense && "max-sm:basis-full",
        containerClassName,
      )}
    >
      <MagnifyingGlass
        className={cn(
          "pointer-events-none absolute top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground",
          dense ? "left-2" : "left-2.5",
        )}
      />
      <Input
        // A page's own filter, not one inside a pane's chrome, is what `/` focuses.
        data-page-search={dense ? undefined : ""}
        className={cn(dense ? "h-7 pl-7 text-xs" : "h-10 pl-8 sm:h-8", className)}
        {...props}
      />
      {trailing && <div className="absolute right-1 flex items-center gap-0.5">{trailing}</div>}
    </div>
  )
}
