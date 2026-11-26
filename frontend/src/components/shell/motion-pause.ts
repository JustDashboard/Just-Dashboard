"use client"

import { useSyncExternalStore } from "react"
import { hasReducedMotionListener, prefersReducedMotion } from "motion/react"
import { reducedMotion } from "@/lib/motion"
import { MOTION_PAUSED, MOTION_STORAGE_KEY } from "@/lib/themes"

/**
 * "Pause animations": the reduced-motion request, made in the product rather
 * than in the system's settings, for whoever wants the beams, breathing dots,
 * sweeps and shimmers still without asking every app on the machine to stop.
 *
 * The answer is one attribute, `data-motion="paused"` on `<html>`, and three
 * things read it:
 *
 * - the stylesheet, which collapses every animation and transition under it
 *   exactly as it does under `prefers-reduced-motion`, and whose
 *   `motion-reduce:` and `motion-safe:` variants answer to both (globals.css);
 * - script that decides its own motion: `useMotionHeld` for the wires, and
 *   `useReducedMotion` below for every component that renders differently
 *   when motion is reduced. It follows both answers while the component is on
 *   screen, where the motion library's hook of the same name reads its answer
 *   once, when the component mounts, so a conversation open when the switch
 *   was turned kept easing after its newest line (ESLint refuses the
 *   library's);
 * - the motion library itself. Its elements read one module-level answer it
 *   takes from the media query, so the answer is written here instead:
 *   paused, or the system's setting. Each element reads it when it mounts, so
 *   a layout or exit glide of one already on screen keeps what it read.
 *
 * It is kept per browser (`MOTION_STORAGE_KEY`), and the document's bootstrap
 * script puts the attribute back before the first paint
 * (`themeBootstrapScript`), so a paused page never starts moving.
 */

const REDUCED = "(prefers-reduced-motion: reduce)"

const listeners = new Set<() => void>()

function paused() {
  return document.documentElement.dataset.motion === MOTION_PAUSED
}

/**
 * The library's lazy start would overwrite the answer with the media query's
 * alone, so it is told it has started; the listener it would have added is
 * the one in `follow` below.
 */
function tellMotion() {
  hasReducedMotionListener.current = true
  prefersReducedMotion.current = paused() || window.matchMedia(REDUCED).matches
}

function apply(next: boolean) {
  const root = document.documentElement
  if (next) root.dataset.motion = MOTION_PAUSED
  else delete root.dataset.motion
}

/**
 * Once per document, when this module is first evaluated: before the shell
 * renders, so the first `useReducedMotion` under it already reads a paused
 * page as one. The attribute is followed rather than the setter, so whatever
 * writes it (the bootstrap script, the switch, another tab's switch through
 * the storage event) reaches the library and the switch's own reading.
 */
function follow() {
  tellMotion()
  const changed = () => {
    tellMotion()
    for (const listener of listeners) listener()
  }
  new MutationObserver(changed).observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-motion"],
  })
  window.matchMedia(REDUCED).addEventListener("change", changed)
  window.addEventListener("storage", (event) => {
    if (event.key === MOTION_STORAGE_KEY) apply(event.newValue === MOTION_PAUSED)
  })
}

if (typeof window !== "undefined") follow()

function subscribe(changed: () => void) {
  listeners.add(changed)
  return () => {
    listeners.delete(changed)
  }
}

/** Pause or resume the product's animations, here and on the next load. */
export function setMotionPaused(next: boolean) {
  apply(next)
  // A browser that refuses storage is paused until the page is left.
  try {
    if (next) localStorage.setItem(MOTION_STORAGE_KEY, MOTION_PAUSED)
    else localStorage.removeItem(MOTION_STORAGE_KEY)
  } catch {}
}

/** Whether the reader has paused the product's animations, and the switch for it. */
export function useMotionPaused(): [boolean, (next: boolean) => void] {
  return [useSyncExternalStore(subscribe, paused, () => false), setMotionPaused]
}

/**
 * Whether the reader asked for less motion, in the system or with Pause
 * animations, and again each time either changes. In place of the motion
 * library's `useReducedMotion`, which reads once when its component mounts.
 * A component that reads it keeps one element whichever the answer is, so
 * the switch changes how it moves and never what it holds.
 */
export function useReducedMotion(): boolean {
  return useSyncExternalStore(subscribe, reducedMotion, () => false)
}
