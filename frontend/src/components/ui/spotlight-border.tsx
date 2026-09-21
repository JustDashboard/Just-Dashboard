"use client"

import { useCallback, useEffect, useRef } from "react"
import { useReducedMotion } from "motion/react"

import { cn } from "@/lib/utils"

/**
 * A border that lights where the pointer is.
 *
 * Magic UI's `magic-card`, from the shadcn registry, with everything this
 * product bans taken out of it: the orb mode is a blurred coloured disc behind
 * the content — a glow — and is gone; the surface wash it painted under the
 * children is gone with it; `next-themes` is gone because there is one mode
 * (§1); and the five hard-coded hexes are `--flow-lit` and `--border`, which
 * is the whole palette this is allowed.
 *
 * What is left is one pixel of edge. The border is painted with a radial
 * gradient in `border-box` that is brand-lit at the pointer and falls to the
 * card's own `--border` a couple of hundred pixels away, over a `padding-box`
 * fill of the card's ground. Nothing lifts, nothing glows, nothing moves but
 * the light on the edge — which is the §16 latitude exactly: depth and
 * response, no decoration.
 *
 * It belongs to **things you pick**, wherever they are — not to the flow
 * register. `ChoiceCard` carries it for every caller, so the database dialogs
 * and the credential picker have the same edge the deploy chooser does. What
 * it is not for is a row you *read*: a reading page answers the pointer with
 * `bg-row-hover` and nothing else (§6), because the edge means "this is
 * takeable" and a table of forty readings where every line lights means
 * nothing by it.
 *
 * The position is written to a CSS custom property rather than held in React
 * state: a pointer move is up to 120 events a second, and a `setState` at that
 * rate re-renders every card in the grid. The two properties are read by the
 * gradient in `style`, so the browser repaints the border and touches nothing
 * else. `motion` is not involved — there is no animation here, only a value
 * that follows the pointer, and a spring on a border's light reads as lag.
 */
export function SpotlightBorder({
  children,
  className,
  radius = 220,
  /** The edge this card carries when the pointer is elsewhere. */
  resting = "border",
  ground = "choice",
}: {
  children?: React.ReactNode
  className?: string
  radius?: number
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
  const host = useRef<HTMLDivElement>(null)
  // A light that follows the pointer is motion, even though no keyframe runs:
  // the root `prefers-reduced-motion` rule in globals.css collapses animation
  // durations and cannot reach a value being written from JavaScript, which is
  // why every vendored component in this directory checks for itself. Reduced
  // motion gets the resting edge and no listeners at all.
  const reduced = useReducedMotion()

  // Park the light outside the card, so a card that has never been pointed at
  // draws its resting edge rather than a gradient centred on its top-left
  // corner — which is what an unset custom property resolves to.
  const park = useCallback(() => {
    const el = host.current
    if (!el) return
    el.style.setProperty("--spot-x", "-9999px")
    el.style.setProperty("--spot-y", "-9999px")
  }, [])

  const follow = useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    const el = event.currentTarget
    const box = el.getBoundingClientRect()
    el.style.setProperty("--spot-x", `${event.clientX - box.left}px`)
    el.style.setProperty("--spot-y", `${event.clientY - box.top}px`)
  }, [])

  useEffect(() => {
    if (reduced) return
    park()
    // A pointer that leaves the window entirely fires no `pointerleave` on the
    // card it was last over, so the light stays stuck where it was until the
    // pointer comes back. The registry component carries the same three
    // listeners for the same reason.
    const gone = (event: PointerEvent) => {
      if (!event.relatedTarget) park()
    }
    const hidden = () => {
      if (document.visibilityState !== "visible") park()
    }
    window.addEventListener("pointerout", gone)
    window.addEventListener("blur", park)
    document.addEventListener("visibilitychange", hidden)
    return () => {
      window.removeEventListener("pointerout", gone)
      window.removeEventListener("blur", park)
      document.removeEventListener("visibilitychange", hidden)
    }
  }, [park, reduced])

  // What the edge falls to away from the pointer: the card's own border, the
  // quiet brand of a card that is already chosen, or the full brand of a
  // selection (§3 — selection is a mark, and on a card the mark is its edge).
  const edge =
    resting === "brand"
      ? "var(--brand)"
      : resting === "lit"
        ? "var(--flow-lit-soft)"
        : "var(--border)"
  const fill = ground === "flow" ? "--flow-surface" : "--choice-surface"

  return (
    <div
      ref={host}
      onPointerMove={reduced ? undefined : follow}
      onPointerLeave={reduced ? undefined : park}
      className={cn("relative isolate rounded-xl border border-transparent", className)}
      style={{
        background: reduced
          ? `linear-gradient(var(${fill}) 0 0) padding-box,
             linear-gradient(${edge} 0 0) border-box`
          : `linear-gradient(var(${fill}) 0 0) padding-box,
             radial-gradient(${radius}px circle at var(--spot-x, -9999px) var(--spot-y, -9999px),
               var(--flow-lit),
               ${edge} 100%) border-box`,
      }}
    >
      {children}
    </div>
  )
}
