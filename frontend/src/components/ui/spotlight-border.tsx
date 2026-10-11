import { cn } from "@/lib/utils"

/**
 * A border that lights while the pointer is on the card.
 *
 * It began as Magic UI's `magic-card`, from the shadcn registry, with the glow
 * taken out of it, and kept that component's one idea: a radial gradient in
 * `border-box` that was brand-lit at the pointer and fell to `--border` a
 * couple of hundred pixels away. On a card wider than the light that drew a
 * bright smudge on whichever side the pointer happened to be, and nothing on
 * the rest — so the edge that is meant to say "this whole card is takeable"
 * said it about one corner. The whole edge lights now, at the same
 * `--flow-lit` the gradient's centre was, and the pointer's position is no
 * longer anyone's business.
 *
 * What is left is one pixel of edge changing colour. Nothing lifts, nothing
 * glows, nothing moves — `transition-colors` on a border is §6's hover
 * exactly, and the root `prefers-reduced-motion` rule collapses it like every
 * other transition, so there is no script here to check for itself.
 *
 * It belongs to **things you pick**, wherever they are — not to the flow
 * register. `ChoiceCard` carries it for every caller, so the database dialogs
 * and the credential picker have the same edge the deploy chooser does. What
 * it is not for is a row you *read*: a reading page answers the pointer with
 * `bg-row-hover` and nothing else (§6), because the edge means "this is
 * takeable" and a table of forty readings where every line lights means
 * nothing by it.
 *
 * The fill is clipped to the padding box so a translucent edge composites over
 * the ground the card sits on, not over the card's own fill — the same colour
 * the gradient drew when it was painted under a `padding-box` layer.
 */
export function SpotlightBorder({
  children,
  className,
  /** The edge this card carries when the pointer is elsewhere. */
  resting = "border",
  ground = "choice",
}: {
  children?: React.ReactNode
  className?: string
  resting?: "border" | "lit" | "brand"
  /**
   * Which ground the padding box is filled with. A pickable card sits on
   * `--choice-surface`, a step below the focused surface, so a run of them
   * reads as recessed into the panel holding them; `flow` is for the rare
   * case of one of these *being* the focused surface. Filling both from one
   * token made every card inside a `FlowPanel` invisible against it.
   */
  ground?: "choice" | "flow"
}) {
  return (
    <div
      className={cn(
        "relative isolate rounded-xl border bg-clip-padding transition-colors",
        ground === "flow" ? "bg-flow-surface" : "bg-choice-surface",
        // A selection is the full brand (§3 — on a card the mark is its edge),
        // already brighter than the hover, so a chosen card holds its mark
        // under the pointer rather than dimming to say it was pointed at.
        resting === "brand"
          ? "border-brand"
          : resting === "lit"
            ? "border-flow-lit-soft hover:border-flow-lit"
            : "border-border hover:border-flow-lit",
        className,
      )}
    >
      {children}
    </div>
  )
}
