"use client"

import { useSyncExternalStore } from "react"

/**
 * Whether the wires should hold still: the tab is hidden, or the reader has
 * paused the product's animations (`data-motion="paused"` on `<html>`, the
 * shell's control). The stylesheet already stops every CSS animation under
 * that attribute, as it does under reduced motion; this is for the motion a
 * component decides in script — which beams carry a pulse — so a paused page
 * draws the still lines reduced motion draws, and a page left open in a
 * background tab is not keeping a pulse going on every wire.
 */
export function useMotionHeld() {
  return useSyncExternalStore(subscribe, held, () => false)
}

function held() {
  return document.hidden || document.documentElement.dataset.motion === "paused"
}

function subscribe(changed: () => void) {
  document.addEventListener("visibilitychange", changed)
  const observer = new MutationObserver(changed)
  observer.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-motion"],
  })
  return () => {
    document.removeEventListener("visibilitychange", changed)
    observer.disconnect()
  }
}
