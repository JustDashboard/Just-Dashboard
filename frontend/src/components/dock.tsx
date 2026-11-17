"use client"

import { useEffect, useId, useState, useSyncExternalStore } from "react"
import { AnimatePresence, MotionConfig, motion } from "motion/react"
import { cn } from "@/lib/utils"
import { watchOccupant } from "@/lib/stack-occupant"

/**
 * The bottom stack: everything that floats at the foot of the window, and
 * where each piece of it sits so that none of them covers another.
 *
 * Two layers. A **bar** spans the foot — the settings save bar, a pane page's
 * bottom bar — and says how far up from the window's bottom edge it reaches.
 * A **dock** sits in the bottom-right corner above every bar — Remote
 * Desktop's held sessions, the assistant's conversations — and docks stack on
 * each other in `order`, each lifted by the height of the ones below it.
 * Before this, the assistant's dock guessed the held sessions' height in rem
 * and nothing knew about the save bar, so on a phone a dock sat on the save
 * bar's buttons and a focused field near the foot of a settings page sat
 * under the bar.
 *
 * What the bars occupy is published on the root as `--bottom-occupied`, and
 * the top of the whole stack, docks included, as `--bottom-stack`: the shell's
 * scroller keeps a focused field above the first (`scroll-padding-bottom`),
 * and toasts can rise above the second.
 */

export type StackLayer = "bar" | "dock"

export type StackOccupant = {
  layer: StackLayer
  /**
   * Its place from the bottom among its layer: lower first. A bar of a
   * higher order floats over the ones under it (the save bar over a pane
   * page's bottom bar), and a dock over the docks under it.
   */
  order: number
  /** A bar: how far up from the window's bottom edge it reaches. A dock: its height. */
  extent: number
}

/** The window's edge to the first thing on it, and the gap between two things. */
export const STACK_EDGE = 16
export const STACK_GAP = 12

/**
 * Where everything in the stack sits: how much of the foot the bars occupy,
 * how far each bar is lifted over the bars of a lower order, and each dock's
 * distance from the window's bottom edge.
 *
 * Bars share the foot, so two at once took the taller and the second was
 * drawn over the first: a save bar on a phone page with a bottom bar would
 * have covered its stops. A bar of a higher order is lifted by how far the
 * ones under it reach, so they stand one above the other; a bar's extent is
 * measured with its lift in it, so the stack's top is the upper one's.
 */
export function stackOffsets(occupants: ReadonlyMap<string, StackOccupant>) {
  let occupied = 0
  const docks: [string, StackOccupant][] = []
  const bars: [string, StackOccupant][] = []
  for (const [id, occupant] of occupants) {
    if (occupant.layer === "bar") {
      occupied = Math.max(occupied, occupant.extent)
      bars.push([id, occupant])
    } else docks.push([id, occupant])
  }
  const lifts: Record<string, number> = {}
  for (const [id, bar] of bars) {
    lifts[id] = bars.reduce(
      (under, [, other]) => (other.order < bar.order ? Math.max(under, other.extent) : under),
      0,
    )
  }
  // Docks in their order; two of one order keep the order they arrived in.
  docks.sort((a, b) => a[1].order - b[1].order)
  const bottoms: Record<string, number> = {}
  let next = occupied > 0 ? occupied + STACK_GAP : STACK_EDGE
  for (const [id, dock] of docks) {
    bottoms[id] = next
    next += dock.extent + STACK_GAP
  }
  return {
    occupied,
    top: docks.length > 0 ? next - STACK_GAP : occupied,
    docks: bottoms,
    bars: lifts,
  }
}

const occupants = new Map<string, StackOccupant>()
const listeners = new Set<() => void>()
let snapshot = stackOffsets(occupants)

function publish() {
  snapshot = stackOffsets(occupants)
  if (typeof document !== "undefined") {
    const root = document.documentElement.style
    root.setProperty("--bottom-occupied", `${snapshot.occupied}px`)
    root.setProperty("--bottom-stack", `${snapshot.top}px`)
    // The shell's scroller is where a focused field is scrolled into view:
    // with a bar up, it stops short of the bar.
    for (const scroller of document.querySelectorAll<HTMLElement>(
      "[data-workspace-shell-scroll]",
    )) {
      scroller.style.scrollPaddingBottom =
        snapshot.occupied > 0 ? "calc(var(--bottom-occupied) + 0.5rem)" : ""
    }
  }
  for (const listener of listeners) listener()
}

function setOccupant(id: string, occupant: StackOccupant | null) {
  const held = occupants.get(id)
  if (occupant === null) {
    if (!held) return
    occupants.delete(id)
  } else {
    if (
      held &&
      held.layer === occupant.layer &&
      held.order === occupant.order &&
      Math.abs(held.extent - occupant.extent) < 0.5
    )
      return
    occupants.set(id, occupant)
  }
  publish()
}

const subscribe = (listener: () => void) => {
  listeners.add(listener)
  return () => listeners.delete(listener)
}
const read = () => snapshot

/** Where everything in the stack sits now. */
export function useBottomStack() {
  return useSyncExternalStore(subscribe, read, read)
}

/** How far up from its bottom edge an element positioned at the foot reaches. */
function extentOf(element: HTMLElement, layer: StackLayer) {
  const height = element.getBoundingClientRect().height
  if (layer === "dock") return height
  const bottom = Number.parseFloat(getComputedStyle(element).bottom)
  return height + (Number.isFinite(bottom) ? bottom : 0)
}

/**
 * Puts an element in the stack while `active`: measured as it changes size or
 * is lifted, and taken out when it goes. A bar passes the element that is
 * positioned at the foot (the one with `bottom`); a dock passes itself.
 * Returns the stack's id for it, which a dock reads its place under and a bar
 * its lift.
 */
export function useStackOccupant(
  element: HTMLElement | null,
  layer: StackLayer,
  { order = 0, active = true }: { order?: number; active?: boolean } = {},
) {
  const id = useId()
  useEffect(() => {
    if (!element || !active) return
    const measure = () => setOccupant(id, { layer, order, extent: extentOf(element, layer) })
    measure()
    const stop = watchOccupant(element, layer, measure)
    return () => {
      stop()
      setOccupant(id, null)
    }
  }, [element, layer, order, active, id])
  return id
}

const EASE = [0.16, 1, 0.3, 1] as const

/**
 * A dock: a small floating panel in the bottom-right corner that is the way
 * back to something going on behind the page — a remote session left
 * connected, a conversation with the assistant. It rises in when there is
 * something to go back to and sinks away when there is not, and it sits in
 * the bottom stack: above an open save bar, and above the docks of a lower
 * `order`, by their measured heights.
 *
 * The frame is one, so two docks are one kind of thing: a popover's surface
 * (§2), 22rem wide and the window less its gutters on a phone.
 */
export function Dock({
  open,
  label,
  slot,
  order = 0,
  className,
  children,
}: {
  open: boolean
  /** The landmark's name: "Remote sessions". */
  label: string
  slot: string
  /** Its place from the bottom among the docks: lower first. */
  order?: number
  className?: string
  children: React.ReactNode
}) {
  return (
    <MotionConfig reducedMotion="user">
      <AnimatePresence>
        {open && (
          <DockFrame key={slot} label={label} slot={slot} order={order} className={className}>
            {children}
          </DockFrame>
        )}
      </AnimatePresence>
    </MotionConfig>
  )
}

function DockFrame({
  label,
  slot,
  order,
  className,
  children,
}: {
  label: string
  slot: string
  order: number
  className?: string
  children: React.ReactNode
}) {
  const [element, setElement] = useState<HTMLElement | null>(null)
  const id = useStackOccupant(element, "dock", { order })
  const bottom = useBottomStack().docks[id] ?? STACK_EDGE
  return (
    <motion.aside
      ref={setElement}
      aria-label={label}
      data-slot={slot}
      initial={{ opacity: 0, y: 12 }}
      animate={{ opacity: 1, y: 0 }}
      exit={{ opacity: 0, y: 12 }}
      transition={{ duration: 0.22, ease: EASE }}
      style={{ bottom }}
      className={cn(
        "fixed right-4 z-float flex w-inspector max-w-[calc(100vw-2rem)] flex-col gap-1 rounded-xl border border-border-strong bg-popover/95 p-2 text-popover-foreground shadow-lg backdrop-blur transition-[bottom] duration-base ease-arrive",
        className,
      )}
    >
      {children}
    </motion.aside>
  )
}
