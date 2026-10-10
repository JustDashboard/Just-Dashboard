"use client"

import { useEffect } from "react"

const DIALOG = '[role="dialog"],[role="alertdialog"]'
/** What a page opens over itself. Focus inside one is not a place to come back to. */
const LAYER = `${DIALOG},[role="menu"],[role="listbox"],[data-radix-popper-content-wrapper]`

/**
 * Puts the keyboard back where it was when a dialog closes.
 *
 * The dialogs here are opened from a press — Rename, Delete key, New key, a
 * confirmation — not from a trigger the dialog knows about, so when one
 * closes there is nothing for it to hand focus back to and it falls to the
 * page's body: the next Tab starts from the top of the window. This keeps a
 * short trail of what held focus on the page itself and, when a dialog has
 * gone and nothing holds focus, gives it back to the newest of them that is
 * still there — the control that opened the dialog, or the row behind it
 * when that control went with the thing it removed.
 *
 * It acts only on a dialog leaving. A reader who clicks an empty part of the
 * page has put focus nowhere on purpose, and it stays there.
 */
export function useFocusReturn() {
  useEffect(() => {
    let trail: HTMLElement[] = []
    const onFocusIn = (event: FocusEvent) => {
      const el = event.target
      if (!(el instanceof HTMLElement) || el.closest(LAYER)) return
      trail = [el, ...trail.filter((other) => other !== el)].slice(0, 8)
    }
    const restore = () => {
      const active = document.activeElement
      if (active && active !== document.body) return
      // Another dialog took this one's place: it has the keyboard now.
      if (document.querySelector(DIALOG)) return
      trail
        .find((el) => el.isConnected && !el.hasAttribute("disabled"))
        ?.focus({ preventScroll: true })
    }
    // A dialog is drawn in a layer of its own under the body, and leaves it
    // when its closing motion ends.
    const observer = new MutationObserver((records) => {
      const closed = records.some((record) =>
        Array.from(record.removedNodes).some(
          (node) =>
            node instanceof HTMLElement &&
            (node.matches(DIALOG) || node.querySelector(DIALOG) !== null),
        ),
      )
      if (closed) setTimeout(restore, 0)
    })
    observer.observe(document.body, { childList: true })
    document.addEventListener("focusin", onFocusIn)
    return () => {
      observer.disconnect()
      document.removeEventListener("focusin", onFocusIn)
    }
  }, [])
}
