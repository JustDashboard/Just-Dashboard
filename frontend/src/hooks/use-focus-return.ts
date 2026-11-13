"use client"

import { useCallback, useLayoutEffect, useRef } from "react"

/**
 * The control a menu was opened from, for an element inside that menu.
 *
 * A menu's content is labelled by its trigger (`aria-labelledby`), and a
 * submenu's by the item that opened it, which sits in the menu above: the walk
 * ends on the button outside every menu.
 */
function menuOpener(inside: Element): HTMLElement | null {
  let menu = inside.closest('[role="menu"]')
  let opener: HTMLElement | null = null
  for (let depth = 0; menu && depth < 6; depth++) {
    const id = menu.getAttribute("aria-labelledby")
    const labelled = id ? document.getElementById(id) : null
    if (!labelled) break
    opener = labelled
    menu = labelled.closest('[role="menu"]')
  }
  return opener
}

/** The first of these that is still in the document and can take the keyboard. */
export function firstReachable<T extends { isConnected: boolean; disabled?: boolean }>(
  candidates: readonly (T | null | undefined)[],
): T | undefined {
  for (const candidate of candidates) {
    if (candidate?.isConnected && !candidate.disabled) return candidate
  }
  return undefined
}

let claimedUntil = 0

/**
 * A page that hands the keyboard back itself says so, and a dialog closing in
 * the next moment leaves the keyboard alone.
 *
 * The database's data page asks a question before the reader leaves unapplied
 * changes, and the question is opened by the link they pressed. Back to what
 * opened it would be that link; the page puts the keyboard back on the rows
 * they were working in (`focusAfterDialog`), and its answer is the one that
 * stands.
 */
export function claimFocusReturn(forMs: number) {
  claimedUntil = performance.now() + forMs
}

/**
 * Where the keyboard goes when a dialog closes: back to what opened it.
 *
 * Radix hands focus back to a dialog's `DialogTrigger`. Almost every dialog
 * here is opened by state instead (a confirmation, an editor mounted when its
 * subject is chosen), has no trigger, and so left the keyboard on `body`: the
 * next Tab started again at the top of the sidebar, and somebody who cancelled
 * a Remove had to tab back through the page to the row they were on.
 *
 * The opener is whatever held the keyboard when the dialog opened, read in a
 * layout effect, which runs before the dialog takes the focus for itself. For
 * a verb in a row's menu that is the menu item, which is gone by the time the
 * dialog closes, so the menu's own button is remembered beside it and is the
 * one that gets the keyboard back.
 *
 * Returns the handler for the dialog's `onCloseAutoFocus`. It leaves an event
 * somebody already answered alone, gives way to a page that said it hands the
 * keyboard back itself (`claimFocusReturn`), and does nothing when neither element is
 * still on the page (the row was removed), which is Radix's own behaviour.
 */
export function useFocusReturn(open: boolean): (event: Event) => void {
  const back = useRef<{ opener: HTMLElement | null; menu: HTMLElement | null }>({
    opener: null,
    menu: null,
  })
  useLayoutEffect(() => {
    if (!open) return
    const active = document.activeElement
    const opener = active instanceof HTMLElement && active !== document.body ? active : null
    back.current = { opener, menu: opener ? menuOpener(opener) : null }
  }, [open])
  return useCallback((event: Event) => {
    if (event.defaultPrevented) return
    if (performance.now() < claimedUntil) {
      // The page takes it from here; nothing is focused for it.
      event.preventDefault()
      return
    }
    const target = firstReachable<HTMLElement & { disabled?: boolean }>([
      back.current.opener,
      back.current.menu,
    ])
    if (!target) return
    event.preventDefault()
    target.focus()
  }, [])
}
