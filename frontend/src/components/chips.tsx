import { cn } from "@/lib/utils"

/**
 * A filter chip: which subset of the rows is shown.
 *
 * Squared rather than fully rounded, because a pill is the shape this product
 * reserves for nothing at all — see `components/tag.tsx`.
 *
 * Selected is `bg-accent` with a hairline, which is the same mark the terminal
 * rail puts on the active session and `ToggleGroup` puts on the pressed item.
 * It used to be a primary tint, so a row of filters and a row of toggles two
 * panels apart disagreed about what "chosen" looks like — and the primary tint
 * is the one thing on the page reserved for a *command*.
 *
 * At rest it has an edge, the outline every other control in the row has. A
 * chip that was bare text until it was chosen sat in the log pane's filter row
 * beside bordered fields and buttons, and read as a caption: "Errors 238" was
 * a word with a number, not a thing to press. Chosen, the edge steps up to
 * `--border-strong` under the accent ground, so a row of them reads chosen
 * against unchosen at a glance and not by comparing two greys.
 */
export function FilterChip({
  selected,
  className,
  ...props
}: React.ComponentProps<"button"> & { selected?: boolean }) {
  return (
    <button
      type="button"
      aria-pressed={selected}
      data-selected={selected || undefined}
      className={cn(
        "inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md border px-2.5 text-hint font-medium whitespace-nowrap transition-colors",
        "focus-ring",
        selected
          ? "border-border-strong bg-accent text-foreground"
          : "border-input text-muted-foreground hover:bg-row-hover hover:text-foreground",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A run of `FilterChip`s.
 *
 * On a phone the run scrolls sideways instead of wrapping: wrapped, a strip of
 * five chips broke into ragged lines with "Pending changes" alone on the last,
 * and three lines of filters pushed the list they filter down a screen. It
 * bleeds to the edges of the page's gutter so the chip cut off at the edge is
 * the affordance, as `ui/tabs` does, with no scrollbar to eat the strip's
 * height. The vertical step is room for the focus ring, which the scroller
 * would otherwise clip. From `sm` it wraps as a row of chips always has.
 */
export function ChipStrip({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "flex min-w-0 [scrollbar-width:none] items-center gap-1.5 overflow-x-auto [&::-webkit-scrollbar]:hidden",
        "max-sm:-mx-5 max-sm:-my-1 max-sm:px-5 max-sm:py-1",
        "sm:flex-wrap sm:overflow-visible",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A count beside a chip's or a tab's label. Muted inside an unselected control
 * and inherited inside a selected one, so the number never competes with the
 * word — and plain tabular text rather than a second small container, because
 * a chip with a chip inside it is exactly the stacking this pass removed.
 * One step behind the word, and no more: at 60% of an unselected tab's muted
 * ink the count measured 3.4:1 at 10px, where WCAG AA asks 4.5:1. At 80% it
 * is 5.2:1 on the page and 4.9:1 on the faintest ground a tab sits on.
 * A chip that is a legend — the request log's status families — passes its
 * colour for the chosen state, so "5xx 13" says which thirteen.
 */
export function ChipCount({
  children,
  className,
}: {
  children: React.ReactNode
  className?: string
}) {
  return (
    <span className={cn("numeric text-micro tabular-nums opacity-80", className)}>{children}</span>
  )
}
