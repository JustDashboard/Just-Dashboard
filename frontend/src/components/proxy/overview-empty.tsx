import { ProductLogo } from "@/components/product-logo"
import { cn } from "@/lib/utils"

/**
 * What a block says when it has nothing to read: what is missing, why, and
 * the one way to change that where there is one. The overview's blocks each
 * had their own grey sentence at their own size — one at 13px, one at 11px —
 * so four empties on a quiet host read as four different kinds of nothing.
 *
 * It is a card of the Routes rows' own shape — a tile drawn as the product
 * the block is about, the words beside it, the way out at the far end —
 * because a 16px glyph in a column of its own over 12px grey text left a
 * gutter of white down the block and read as nothing at all. The tile holds
 * the words' top line, so the icon and the title stand on one line.
 *
 * It is the size of those rows too — a 12px inset, the name over one
 * hint-sized line, a small outline verb at the far end like a channel card's
 * — because a 16px inset, body-sized prose and a filled button made each one
 * a slab twice the height of every card around it.
 */
export function OverviewEmpty({
  mark,
  title,
  children,
  action,
  tone,
}: {
  /** The block's tile: `EmptyMark`, the product it is about with what is missing in its corner. */
  mark: React.ReactNode
  title: string
  children: React.ReactNode
  action?: React.ReactNode
  /** What the state means: plain HTTP is a warning, an hour with nothing failed good news. */
  tone?: "warning" | "success"
}) {
  return (
    // Sized by its own width rather than the window's, because it stands
    // full-width under Live traffic and in a half of the band beside Errors:
    // the action stands at the card's far end where the line has room for it,
    // and under the words — never under the tile — where it has not.
    <div
      data-slot="overview-empty"
      className={cn(
        "@container min-w-0 animate-rise rounded-xl border bg-choice-surface px-3 py-2.5",
        tone === "warning" && "border-rule-warning",
        tone === "success" && "border-rule-success",
      )}
    >
      <div className="grid min-h-9 min-w-0 grid-cols-[auto_minmax(0,1fr)] items-center gap-x-3 @xl:grid-cols-[auto_minmax(0,1fr)_auto]">
        <span className="row-span-2 flex self-start pt-0.5 @xl:row-span-1 @xl:self-center @xl:pt-0">
          {mark}
        </span>
        <div className="min-w-0">
          <p className="text-body leading-snug font-medium">{title}</p>
          <p className="text-hint leading-snug text-pretty text-muted-foreground">{children}</p>
        </div>
        {action && <div className="mt-2.5 flex @xl:mt-0">{action}</div>}
      </div>
    </div>
  )
}

/**
 * A product's tile with what is missing drawn in its corner — nginx with a
 * log, Let's Encrypt with an open lock — the way a route's tile carries its
 * engine in the corner.
 */
export function EmptyMark({
  product,
  fallback,
  corner: Corner,
  tone,
}: {
  product?: string
  fallback?: React.ComponentType<{ className?: string }>
  corner?: React.ComponentType<{ className?: string }>
  tone?: "warning" | "success" | "danger"
}) {
  return (
    <span className="relative flex shrink-0">
      <ProductLogo id={product} fallback={fallback} />
      {Corner && (
        // The wash is translucent, so it sits on a tile of the page's own
        // ground rather than over the logo's corner.
        <span
          aria-hidden
          className={cn(
            "absolute -right-1 -bottom-1 flex size-4 rounded-sm border bg-background",
            tone === "warning" && "border-rule-warning text-warning",
            tone === "success" && "border-rule-success text-success",
            tone === "danger" && "border-rule-danger text-destructive",
            !tone && "border-hairline text-muted-foreground",
          )}
        >
          <span
            className={cn(
              "flex size-full items-center justify-center rounded-[inherit]",
              tone === "warning" && "bg-wash-warning",
              tone === "success" && "bg-wash-success",
              tone === "danger" && "bg-wash-danger",
            )}
          >
            <Corner className="size-2.5" />
          </span>
        </span>
      )}
    </span>
  )
}
