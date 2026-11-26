import { cn } from "@/lib/utils"

/**
 * A filter chip: which subset of the rows is shown.
 *
 * Squared rather than fully rounded, because a pill is the shape this product
 * reserves for nothing at all — see `components/tag.tsx`.
 *
 * Chosen, it trades its line for the chosen fill (`bg-chosen`) and the
 * foreground ink: the face a chosen segment has, so a row of filters and a
 * segmented control beside it agree on what "chosen" looks like. It used to
 * be a primary tint, the one thing on the page reserved for a *command*, and
 * then the accent ground under the strong line, which was louder than the
 * filter it marked (Just Hub dropped that line on 24 Nov 2026, and so did
 * this on the 25th).
 *
 * At rest it has an edge, the outline every other control in the row has. A
 * chip that was bare text until it was chosen sat in the log pane's filter row
 * beside bordered fields and buttons, and read as a caption: "Errors 238" was
 * a word with a number, not a thing to press. So a row of them reads chosen
 * against unchosen at a glance: a line round the ones you could pick, a soft
 * face on the one you did.
 *
 * It is drawn 28px tall at every pointer, so a row of filters keeps its
 * proportions, and presses as 44px under a finger (`hit-area`).
 */
// Forced colours (Windows High Contrast) repaint every fill as the page and
// every ink as the text's, so the chosen chip read as any other. There it
// takes the system's own selection, Highlight under HighlightText, kept from
// that repaint, as the chosen tab does (`components/tabs.tsx`). Its ring is
// drawn outside it, on the page, so it is Highlight as every focused
// control's is there. Normal colours keep the face with no outline.
const CHOSEN_FORCED =
  "forced-colors:bg-[Highlight] forced-colors:text-[HighlightText] forced-colors:forced-color-adjust-none forced-colors:[--ring:Highlight]"

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
        "hit-area inline-flex h-7 shrink-0 items-center gap-1.5 rounded-md border px-2.5 text-hint font-medium whitespace-nowrap transition-colors",
        "focus-ring",
        selected
          ? // A count inside the chosen chip takes the label's ink.
            cn(
              "border-transparent bg-chosen text-foreground [--count-ink:currentColor]",
              CHOSEN_FORCED,
            )
          : "border-input text-muted-foreground hover:bg-row-hover hover:text-foreground",
        className,
      )}
      {...props}
    />
  )
}

// How far the strip reaches past its box on a phone: the page's own inset, so
// the chip cut at the edge is cut by the screen. `--page-inset` is the page's
// gutter; until the page names it, the 20px Page draws below `md`.
const BLEED = "max-sm:-mx-[var(--page-inset,1.25rem)] max-sm:px-[var(--page-inset,1.25rem)]"

/**
 * A run of `FilterChip`s.
 *
 * On a phone the run scrolls sideways instead of wrapping: wrapped, a strip of
 * five chips broke into ragged lines with "Pending changes" alone on the last,
 * and three lines of filters pushed the list they filter down a screen. It
 * bleeds to the edges of the page's gutter so the chip cut off at the edge is
 * the affordance, with no scrollbar to eat the strip's height. From `sm` it
 * wraps as a row of chips always has.
 *
 * The bleed is sideways only. It used to step out above and below too, for
 * room to draw the focus ring the scroller would clip, and the negative margin
 * that took replaced the gap of the stack the strip stood in: the certificate
 * sources sat flush on the first certificate. The ring is drawn inside the chip
 * on a phone instead.
 *
 * `bleed={false}` keeps the strip in its box, for a strip inside a form, a
 * dialog or a pane, which has no page gutter to reach into.
 */
export function ChipStrip({
  bleed = true,
  className,
  ...props
}: React.ComponentProps<"div"> & { bleed?: boolean }) {
  return (
    <div
      className={cn(
        "flex min-w-0 [scrollbar-width:none] items-center gap-1.5 overflow-x-auto [&::-webkit-scrollbar]:hidden",
        "max-sm:[&_:focus-visible]:outline-offset-[-2px]",
        bleed && BLEED,
        "sm:flex-wrap sm:overflow-visible",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A count beside a chip's or a tab's label, in the faint ink, and the label's
 * own ink inside a chosen chip or tab (they set `--count-ink`) — so the number
 * never competes with the word. Plain tabular text rather than a second small
 * container, because a chip with a chip inside it is exactly the stacking this
 * pass removed. It used to be faded with opacity, which the one-mode rule
 * forbids: `--faint` is the dimmest ink that passes 4.5:1.
 *
 * A chip that is a legend — the request log's status families — passes its
 * colour as a class, so "5xx 13" says which thirteen.
 */
export function ChipCount({
  children,
  className,
}: {
  children: React.ReactNode
  className?: string
}) {
  return (
    <span
      data-slot="chip-count"
      className={cn(
        "numeric text-micro tabular-nums",
        "text-[color:var(--count-ink,var(--faint))]",
        className,
      )}
    >
      {children}
    </span>
  )
}
