import { cn } from "@/lib/utils"

/** A row of filters and actions that stands on its own, outside a panel. */
export function Toolbar({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("flex min-w-0 flex-wrap items-center gap-2", className)} {...props} />
}

/**
 * The controls over a list: its search, its sort, its verbs and its chips, in
 * one order on every list.
 *
 * Six lists had six layouts of the same parts. Sites put the sort at the far
 * right and the chips on the next line; Certificates sorted with a segment;
 * Streams and Config set the chips beside the search; Ports had a third row
 * and a sort that existed only on a phone; Logins put its search on the right.
 * Here the search opens the first line, the sort and the verbs close it, and
 * the chips take a line of their own under it.
 *
 * On a phone the search is the whole first line at 44px, the sort and verbs
 * are the next, and the chips are a strip that scrolls sideways (`ChipStrip`).
 * A sort `Select` here is the search's 44px rather than the compact 40, so the
 * two fields of one toolbar are one height and the select does not read as
 * the main field.
 */
export function ListToolbar({
  search,
  chips,
  sort,
  actions,
  className,
}: {
  /** A `SearchInput`, not dense: the list's own filter is the page's. */
  search: React.ReactNode
  /** A `ChipStrip` of `FilterChip`s, or a `Segments` for a two or three way view. */
  chips?: React.ReactNode
  /** A `Select size="sm"` of the orders the list can take. */
  sort?: React.ReactNode
  /** The list's verbs: a small outline button or two. */
  actions?: React.ReactNode
  className?: string
}) {
  return (
    <div
      data-slot="list-toolbar"
      className={cn(
        "flex w-full min-w-0 flex-wrap items-center gap-2",
        "max-sm:[&_[data-slot=select-trigger]]:h-11 pointer-coarse:[&_[data-slot=select-trigger]]:h-11",
        className,
      )}
    >
      {search}
      {(sort || actions) && (
        <div className="flex min-w-0 flex-wrap items-center gap-2 sm:ml-auto">
          {sort}
          {actions}
        </div>
      )}
      {chips && <div className="min-w-0 basis-full">{chips}</div>}
    </div>
  )
}
