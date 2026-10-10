"use client"

import { useEffect, useRef, useState } from "react"

/**
 * Where the keyboard goes when a form or a confirmation closes: back to the
 * control that asked for it.
 *
 * A dialog opened from a button and closed again used to leave focus on the
 * document, so a keyboard reader was sent to the top of the page after every
 * change — and this area is twenty forms. The control is remembered at the
 * press and focused again once the dialog is gone. A press made in a menu
 * names the menu's own button, since the menu's row is gone by then; and a
 * control that the change itself removed (the row of a dropped column, the
 * head of a dropped table) hands the keyboard to the nearest thing that is
 * still there: the open reading's tab, the current row of the tree.
 *
 * If something else has taken the keyboard meanwhile — another dialog, or the
 * dialog's own frame doing this by itself one day — it keeps it.
 */

/** Where focus lands when the control that opened a form is no longer on the page. */
const NEAREST = [
  '[data-slot="schema-browser"] [role="tab"][aria-selected="true"]',
  '[data-slot="schema-rail"] a[aria-current="page"]',
  '[data-slot="schema-rail"] button',
]

const SURFACE = '[role="dialog"], [role="alertdialog"], [role="menu"]'

/** The control a press came from: the focused one, or the button of the menu it sits in. */
export function openerOf(active: Element | null): HTMLElement | null {
  let from = active instanceof HTMLElement && active !== document.body ? active : null
  // A menu's row is taken away with the menu; its button stays. A submenu's
  // button is itself a row of the menu above it.
  for (let depth = 0; from && depth < 4; depth += 1) {
    const by = from.closest('[role="menu"]')?.getAttribute("aria-labelledby")
    const trigger = by ? document.getElementById(by) : null
    if (!trigger) break
    from = trigger
  }
  return from
}

/** The control focused now, as the one to come back to. */
export function currentOpener(): HTMLElement | null {
  return typeof document === "undefined" ? null : openerOf(document.activeElement)
}

/** How long a surface that is closing is waited for, and how long the keyboard is watched after. */
const WAIT = { closing: 12, watching: 20, every: 100 }

/**
 * Gives the keyboard back to `to` once no dialog or menu is on screen.
 *
 * The control it came from may not be there to take it — its row went with a
 * dropped column — or not yet able to: a maintenance button is off while its
 * command runs. And it may be there now and gone a moment later, when the
 * page reads the table again and the row leaves. So the keyboard is placed,
 * and then watched for a few seconds: whenever it falls to the document, it
 * is put on the nearest thing still standing.
 */
export function returnFocus(to: HTMLElement | null) {
  const lost = () => {
    const held = document.activeElement
    return !held || held === document.body || held === document.documentElement
  }
  const takes = (el: HTMLElement | null | undefined): el is HTMLElement =>
    Boolean(el?.isConnected) && !el!.matches(":disabled")
  const place = () => {
    const target = takes(to)
      ? to
      : NEAREST.map((selector) => document.querySelector<HTMLElement>(selector)).find(takes)
    target?.focus({ preventScroll: true })
  }
  let waited = 0
  let watched = 0
  const tick = () => {
    // A surface that is closing is still in the page for its last frames; one
    // that stays is another surface, and the keyboard is its own.
    if (document.querySelector(SURFACE)) {
      waited += 1
      if (waited <= WAIT.closing) window.setTimeout(tick, WAIT.every / 2)
      return
    }
    if (lost()) place()
    watched += 1
    if (watched < WAIT.watching) window.setTimeout(tick, WAIT.every)
  }
  window.setTimeout(tick, 0)
}

/**
 * For a surface that is on the page for as long as `open` is true. The
 * opener is read during the render that opens it — before the surface takes
 * the keyboard — and focused again after the render that closes it.
 *
 * `find` names the control for a surface whose opener cannot be read off the
 * page: a menu opened at a point rather than from a button has no button of
 * its own to return to, and its caller knows which control asked for it.
 */
export function useFocusReturn(open: boolean, find?: () => HTMLElement | null) {
  const [was, setWas] = useState(false)
  const [from, setFrom] = useState<HTMLElement | null>(null)
  if (open !== was) {
    setWas(open)
    if (open) setFrom(currentOpener())
  }
  const finder = useRef(find)
  useEffect(() => {
    finder.current = find
  })
  useEffect(() => {
    if (!open) return
    return () => returnFocus(finder.current?.() ?? from)
  }, [open, from])
}
