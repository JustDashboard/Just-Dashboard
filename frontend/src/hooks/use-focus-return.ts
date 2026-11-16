"use client"

import { useCallback, useEffect, useLayoutEffect, useRef } from "react"

/**
 * The control a menu was opened from, for an element inside that menu.
 *
 * A menu's content is labelled by its trigger (`aria-labelledby`), and a
 * submenu's by the item that opened it, which sits in the menu above: the walk
 * ends on the button outside every menu. `null` for an element in no menu.
 */
export function menuOpener(inside: Element): HTMLElement | null {
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

/**
 * The control a press came from: the element itself, or, for a row of a menu
 * (which leaves with the menu), the button the menu was opened from. `null`
 * when nothing but the document held the keyboard.
 */
export function openerOf(active: Element | null): HTMLElement | null {
  // Read as "can take the keyboard" rather than `instanceof HTMLElement`, so
  // an SVG link counts and the walk can be held in a unit test without a DOM.
  if (!active || active === document.body || !("focus" in active)) return null
  return menuOpener(active) ?? (active as HTMLElement)
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

const DIALOG = '[role="dialog"],[role="alertdialog"]'
/** What a page opens over itself. Focus inside one is not a place to come back to. */
const LAYER = `${DIALOG},[role="menu"],[role="listbox"],[data-radix-popper-content-wrapper]`

/**
 * A page's fallback for the keyboard after a dialog it opened has gone.
 *
 * `useFocusReturn` above (every `Modal` and `ConfirmDialog` runs it) hands
 * the keyboard back to what opened the dialog. That fails in one case: the
 * opener went with what the dialog did — the Delete in a key's row, when the
 * key is gone — and the keyboard falls to the body, so the next Tab starts
 * from the top of the window. This keeps a short trail of what held focus on
 * the page itself and, once a dialog has left and nothing holds the keyboard,
 * gives it to the newest of them still standing: the row behind the one that
 * was removed, the list it was in.
 *
 * It never takes the keyboard from anything. It acts only when a dialog has
 * left and focus is on the body, so where the dialog's own hand-back landed,
 * it stays; and it gives way to a page that said it hands the keyboard back
 * itself (`claimFocusReturn`). A reader who clicks an empty part of the page
 * has put focus nowhere on purpose, and it stays there too.
 */
export function useFocusTrail() {
  useEffect(() => {
    let trail: HTMLElement[] = []
    const onFocusIn = (event: FocusEvent) => {
      const el = event.target
      if (!(el instanceof HTMLElement) || el.closest(LAYER)) return
      trail = [el, ...trail.filter((other) => other !== el)].slice(0, 8)
    }
    const restore = () => {
      if (performance.now() < claimedUntil) return
      const active = document.activeElement
      if (active && active !== document.body) return
      // Another dialog took this one's place: it has the keyboard now.
      if (document.querySelector(DIALOG)) return
      firstReachable(trail.map((el) => (el.hasAttribute("disabled") ? null : el)))?.focus({
        preventScroll: true,
      })
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
