"use client"

import { useEffect, type RefObject } from "react"
import { revealDelta } from "@/lib/scroll-reveal"

/** Roughly how long a smooth scroll runs: the reveal holds the wheel that long. */
const SETTLE_MS = 450

/**
 * When the wheel is about to scroll a region the page only partly shows, the
 * page moves first and brings the whole region on screen; the wheel goes back
 * to the region once it is there.
 *
 * Only a wheel the region would take is claimed. One that runs past the
 * region's end is already chaining to the page, and a region with nothing to
 * scroll never asks. While the page is still gliding, the wheel is held rather
 * than handed to the rows, so the grid does not slide under the reader at the
 * same time as the page does.
 */
export function useRevealOnWheel(ref: RefObject<HTMLElement | null>) {
  useEffect(() => {
    const region = ref.current
    if (!region) return
    let settling = 0
    const onWheel = (event: WheelEvent) => {
      if (event.ctrlKey || Math.abs(event.deltaX) > Math.abs(event.deltaY)) return
      const end = region.scrollHeight - region.clientHeight
      if (event.deltaY > 0 ? region.scrollTop >= end - 1 : region.scrollTop <= 0) return
      const port = scrollPort(region)
      if (!port) return
      const top = port.getBoundingClientRect().top + port.clientTop
      const delta = revealDelta(
        region.getBoundingClientRect(),
        { top, bottom: top + port.clientHeight },
        { up: port.scrollTop, down: port.scrollHeight - port.clientHeight - port.scrollTop },
      )
      if (delta === 0) return
      event.preventDefault()
      if (event.timeStamp < settling) return
      const smooth = !matchMedia("(prefers-reduced-motion: reduce)").matches
      port.scrollBy({ top: delta, behavior: smooth ? "smooth" : "auto" })
      settling = smooth ? event.timeStamp + SETTLE_MS : 0
    }
    // React's own `onWheel` is passive, and a passive listener cannot keep the
    // wheel from scrolling the rows while the page is still on its way.
    region.addEventListener("wheel", onWheel, { passive: false })
    return () => region.removeEventListener("wheel", onWheel)
  }, [ref])
}

/** The nearest ancestor that scrolls vertically — the shell's, or a dialog's body. */
function scrollPort(element: HTMLElement): HTMLElement | null {
  for (let node = element.parentElement; node; node = node.parentElement) {
    if (node.scrollHeight <= node.clientHeight) continue
    const { overflowY } = getComputedStyle(node)
    if (overflowY === "auto" || overflowY === "scroll") return node
  }
  return null
}
