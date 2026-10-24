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
    // the action goes beside the words only where both fit.
    <div
      data-slot="overview-empty"
      className={cn(
        "@container min-w-0 animate-rise rounded-xl border bg-choice-surface p-4",
        tone === "warning" && "border-rule-warning",
        tone === "success" && "border-rule-success",
      )}
    >
      <div className="flex min-w-0 flex-col gap-4 @xl:flex-row @xl:items-center">
        <div className="flex min-w-0 flex-1 items-start gap-3.5">
          {mark}
          <div className="min-w-0 space-y-1">
            <p className="text-body leading-snug font-medium">{title}</p>
            <p className="max-w-prose text-body leading-relaxed text-pretty text-muted-foreground">
              {children}
            </p>
          </div>
        </div>
        {action && <div className="flex shrink-0 pl-13.5 @xl:pl-2">{action}</div>}
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
            "absolute -right-1 -bottom-1 flex size-5 rounded-md border bg-background",
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
            <Corner className="size-3" />
          </span>
        </span>
      )}
    </span>
  )
}
