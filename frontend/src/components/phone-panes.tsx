"use client"

import { ArrowLeft } from "@/components/icons"
import { cn } from "@/lib/utils"
import { Button } from "@/components/ui/button"

/**
 * One pane at a time on a phone.
 *
 * Side by side columns are not smaller columns on a phone: stacked, a
 * workspace was short scrolling boxes inside a scrolling page, none tall
 * enough to use — the Logs page gave its sources a 173px box and started the
 * console four fifths of the way down the screen. So a page with several
 * panels shows one, the whole height of the screen, and a bar along the foot
 * picks which, the way a phone app's does. What a row opens is a page of its
 * own, with the bar out of its way and a back arrow in its head.
 *
 * The Git workspace drew this first and the assistant copied it; both are on
 * these parts now, and so are Logs and the network tools. A page keeps its
 * own state for which pane is shown — Git's is its tabs, the assistant's is
 * three words — and hands the parts three things:
 *
 * - `paneShown(shown, until)` on each pane: out of the way below the break
 *   while it is not the one picked, untouched from the break up, where the
 *   panes sit side by side as they always did.
 * - `PaneBar` at the foot of the frame, hidden from the break up and left
 *   out while a detail page is open.
 * - `PaneBack` in the head of a detail page.
 *
 * The frame that holds them is held to the window below the break (`<Page
 * fill>`), or the pane has no height to be the whole of.
 */

/** Where the phone's layout ends and the columns begin. */
export type PaneBreak = "md" | "lg"

// Whole class names: Tailwind reads the source, and cannot see one assembled
// from a variable.
const BELOW: Record<PaneBreak, string> = { md: "max-md:hidden", lg: "max-lg:hidden" }
const FROM: Record<PaneBreak, string> = { md: "md:hidden", lg: "lg:hidden" }

/** The class of a pane: nothing while it is the one shown, hidden below the break while it is not. */
export function paneShown(shown: boolean, until: PaneBreak = "lg"): string | undefined {
  return shown ? undefined : BELOW[until]
}

export type PaneStop<K extends string> = {
  key: K
  label: string
  /** The glyph over the word, drawn at 20px. */
  icon: React.ReactNode
  /** How many are waiting there, on the glyph's shoulder; nothing at zero. */
  count?: number
  /** The count's ground, in the colour of what it counts. */
  countClassName?: string
}

/**
 * The bar at the foot: one stop for each pane, 56px tall, a finger's width,
 * the glyph over its word. It keeps clear of a phone's home indicator, and
 * is not drawn from the break up.
 */
export function PaneBar<K extends string>({
  label,
  stops,
  active,
  onPick,
  until = "lg",
  className,
}: {
  /** What the bar moves between, for whoever cannot see it: "Repository". */
  label: string
  stops: PaneStop<K>[]
  /** The stop whose pane is shown; none while a page outside the bar is. */
  active: K | undefined
  onPick: (key: K) => void
  until?: PaneBreak
  className?: string
}) {
  return (
    <nav
      aria-label={label}
      data-slot="pane-bar"
      className={cn(
        "shrink-0 border-t border-hairline bg-surface-header pb-[env(safe-area-inset-bottom)]",
        FROM[until],
        className,
      )}
    >
      <div
        role="tablist"
        className="grid"
        style={{ gridTemplateColumns: `repeat(${stops.length}, minmax(0, 1fr))` }}
      >
        {stops.map((stop) => {
          const selected = stop.key === active
          const count = stop.count ?? 0
          return (
            <button
              key={stop.key}
              type="button"
              role="tab"
              aria-selected={selected}
              // The count is beside the name, not in it: a stop is found by
              // its word whether or not anything is waiting there.
              aria-description={count > 0 ? `${count}` : undefined}
              onClick={() => onPick(stop.key)}
              className={cn(
                "flex h-14 min-w-0 flex-col items-center justify-center gap-1 focus-ring-inset transition-colors",
                selected ? "text-foreground" : "text-muted-foreground",
              )}
            >
              <span
                aria-hidden
                className={cn("relative flex [&_svg]:size-5", selected && "text-brand")}
              >
                {stop.icon}
                {count > 0 && (
                  <span
                    className={cn(
                      "numeric absolute -top-1.5 -right-2.5 flex h-4 min-w-4 items-center justify-center rounded-sm px-1 text-micro font-semibold text-background",
                      stop.countClassName,
                    )}
                  >
                    {count > 99 ? "99+" : count}
                  </span>
                )}
              </span>
              <span className="text-micro font-medium">{stop.label}</span>
            </button>
          )
        })}
      </div>
    </nav>
  )
}

/**
 * The way out of a page a row opened: an arrow in its head, where a phone's
 * back is. From the break up the same page is a column with a close button
 * of its own, and this is not drawn.
 */
export function PaneBack({
  label = "Back to the list",
  onClick,
  until = "lg",
  className,
}: {
  label?: string
  onClick: () => void
  until?: PaneBreak
  className?: string
}) {
  return (
    <Button
      size="icon-sm"
      variant="ghost"
      className={cn(
        "-ml-1 shrink-0 text-muted-foreground hover:text-foreground",
        FROM[until],
        className,
      )}
      aria-label={label}
      onClick={onClick}
    >
      <ArrowLeft className="size-4" />
    </Button>
  )
}
