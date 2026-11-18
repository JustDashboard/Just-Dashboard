"use client"

import { useEffect, useRef, useState, type RefObject } from "react"

/** What a key press inside one of these is already the answer to. */
const OWNED_ESCAPE = '[role="dialog"],[role="alertdialog"],[role="menu"],[role="listbox"]'

function typingIn(target: EventTarget | null) {
  if (!(target instanceof HTMLElement)) return false
  return (
    target.tagName === "INPUT" ||
    target.tagName === "TEXTAREA" ||
    target.tagName === "SELECT" ||
    target.isContentEditable
  )
}

/**
 * Makes everything but `root` inert: each sibling of `root` and of each of
 * its ancestors up to the body, the way a modal dialog hides the page behind
 * it. Returns the undo. A live region is left alone, so a toast still speaks.
 */
function inertOthers(root: HTMLElement) {
  const held: HTMLElement[] = []
  for (let node: HTMLElement | null = root; node && node !== document.body;) {
    const parent: HTMLElement | null = node.parentElement
    if (!parent) break
    for (const sibling of parent.children) {
      if (sibling === node || !(sibling instanceof HTMLElement)) continue
      if (sibling.inert || sibling.hasAttribute("aria-live")) continue
      if (sibling.tagName === "SCRIPT" || sibling.tagName === "STYLE") continue
      sibling.inert = true
      held.push(sibling)
    }
    node = parent
  }
  return () => {
    for (const element of held) element.inert = false
  }
}

/**
 * Full screen for a working surface — a canvas, the terminal, a remote
 * screen: the browser's own where it is offered, and where it is refused (an
 * iframe, a browser that asks and is told no) an `immersive` overlay the
 * caller draws as `fixed inset-0 z-immersive`. The operator asked for the
 * whole window and gets it either way.
 *
 * The overlay is a full screen in behaviour too, where it had been only in
 * look — Tab walked into the hidden shell behind it and Escape did nothing:
 *
 *   - everything outside `root` is inert while it is up, so the keyboard and
 *     a screen reader stay in it;
 *   - Escape leaves it, unless the press was already an answer to something
 *     in front (a menu, a dialog, a field being typed in, or a handler that
 *     called `preventDefault` — a canvas clearing its selection first);
 *   - leaving puts the keyboard back on what had it when it was entered,
 *     the toggle.
 *
 * `escape: false` for a surface whose Escape is its own key: the terminal
 * sends it to the shell, and a remote desktop to the computer.
 */
export function useFullscreen(
  root: RefObject<HTMLElement | null>,
  { escape = true }: { escape?: boolean } = {},
) {
  const [fullscreen, setFullscreen] = useState(false)
  const [immersive, setImmersive] = useState(false)
  const opener = useRef<HTMLElement | null>(null)

  const leave = () => {
    if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined)
    setImmersive(false)
    setFullscreen(false)
  }

  const toggle = () => {
    if (fullscreen || immersive) return leave()
    opener.current = document.activeElement instanceof HTMLElement ? document.activeElement : null
    const el = root.current
    if (el?.requestFullscreen) {
      el.requestFullscreen().then(
        () => setFullscreen(true),
        () => setImmersive(true),
      )
    } else {
      setImmersive(true)
    }
  }

  useEffect(() => {
    const onChange = () => {
      setFullscreen(document.fullscreenElement === root.current && root.current !== null)
      if (!document.fullscreenElement) setImmersive(false)
    }
    document.addEventListener("fullscreenchange", onChange)
    return () => document.removeEventListener("fullscreenchange", onChange)
  }, [root])

  // The overlay's half of a full screen: the page behind it out of reach,
  // Escape out of it, and the keyboard back where it was.
  useEffect(() => {
    const el = root.current
    if (!immersive || !el) return
    const restore = inertOthers(el)
    const onKey = (event: KeyboardEvent) => {
      if (!escape || event.key !== "Escape" || event.defaultPrevented) return
      if (typingIn(event.target)) return
      if (event.target instanceof Element && event.target.closest(OWNED_ESCAPE)) return
      event.preventDefault()
      setImmersive(false)
    }
    document.addEventListener("keydown", onKey)
    return () => {
      document.removeEventListener("keydown", onKey)
      restore()
      const back = opener.current
      opener.current = null
      if (back?.isConnected) back.focus({ preventScroll: true })
    }
  }, [immersive, escape, root])

  return {
    full: fullscreen || immersive,
    fullscreen,
    immersive,
    toggle,
    /** Leaves either kind of full screen. */
    leave,
  }
}
